package mjpeg

import (
	"context"
	"testing"
	"time"

	"NanoKVM-Server/common"
)

func tileMessage(whole bool) []byte {
	message := []byte{1, 0, 1, 0, 0x80, 0, 0x80, 0}
	if whole {
		message[1] = common.MjpegTilesWhole
	}
	return message
}

func TestTiledClientWaitsForAWholePicture(t *testing.T) {
	c := newTiledClient(context.Background())
	if c.offerTile(tileMessage(false), false) {
		t.Fatal("a new viewer took a partial message")
	}
	if !c.offerTile(tileMessage(true), true) || c.resync {
		t.Fatal("a new viewer did not take the whole picture")
	}
	if !c.offerTile(tileMessage(false), false) {
		t.Fatal("a synchronised viewer refused a partial message")
	}
}

func TestFullTiledQueueResynchronises(t *testing.T) {
	c := newTiledClient(context.Background())
	c.offerTile(tileMessage(true), true)
	for len(c.frames) < cap(c.frames) {
		c.offerTile(tileMessage(false), false)
	}
	if c.offerTile(tileMessage(false), false) || !c.resync || len(c.frames) != 0 {
		t.Fatalf("overflow: resync %t, queued %d", c.resync, len(c.frames))
	}
	if c.offerTile(tileMessage(false), false) {
		t.Fatal("a resynchronising viewer took a partial message")
	}
	if !c.offerTile(tileMessage(true), true) || c.resync || len(c.frames) != 1 {
		t.Fatal("the next whole picture did not resynchronise the viewer")
	}
}

func TestTilesOnlyWithTiledViewersAndDriver(t *testing.T) {
	defer func(available func() bool) { tilesAvailable = available }(tilesAvailable)
	tiled := newTiledClient(context.Background())
	classic := newMjpegClient(context.Background())
	tilesAvailable = func() bool { return true }
	if !tilesOnly([]*mjpegClient{tiled}) || tilesOnly([]*mjpegClient{tiled, classic}) || tilesOnly(nil) {
		t.Fatal("tiles mode with the driver")
	}
	tilesAvailable = func() bool { return false }
	if tilesOnly([]*mjpegClient{tiled}) {
		t.Fatal("tiles mode without the driver")
	}
}

func TestClassicReadsReachTiledViewersWhole(t *testing.T) {
	tiled := newTiledClient(context.Background())
	classic := newMjpegClient(context.Background())
	delivery := frameDelivery{}
	jpeg := []byte{0xff, 0xd8, 0xff, 0xc0, 0x00, 0x0b, 0x08, 0x02, 0xd0, 0x05, 0x00, 0x01, 0x01, 0x11, 0x00, 0xff, 0xd9}
	if !delivery.offer([]*mjpegClient{tiled, classic}, jpeg, time.Now()) {
		t.Fatal("nothing was offered")
	}
	message := <-tiled.frames
	if !common.MjpegTilesMessageWhole(message) || tiled.resync {
		t.Fatalf("tiled viewer got % x", message)
	}
	if got := <-classic.frames; string(got) != string(jpeg) {
		t.Fatal("classic viewer did not get the JPEG")
	}
}

func TestTileReadAsksForWholePictures(t *testing.T) {
	defer func(read func(uint16, uint16, uint16, bool, uint16) ([]byte, int)) { readTiles = read }(readTiles)
	var keys []bool
	result, data := common.MjpegTilesResult, tileMessage(true)
	readTiles = func(_, _, _ uint16, key bool, _ uint16) ([]byte, int) {
		keys = append(keys, key)
		return data, result
	}
	c := newTiledClient(context.Background())
	tiles := tileDelivery{wantWhole: true}
	screen := &common.Screen{Width: 1920, Height: 1080}
	now := time.Now()
	if !tiles.read([]*mjpegClient{c}, &frameDelivery{}, screen, 80, now) {
		t.Fatal("whole picture not delivered")
	}
	data = tileMessage(false)
	tiles.read([]*mjpegClient{c}, &frameDelivery{}, screen, 80, now.Add(time.Second))
	result = -2 // a failed read: the next one is whole again
	tiles.read([]*mjpegClient{c}, &frameDelivery{}, screen, 80, now.Add(2*time.Second))
	result = common.MjpegTilesResult
	tiles.read([]*mjpegClient{c}, &frameDelivery{}, screen, 80, now.Add(3*time.Second))
	tiles.read([]*mjpegClient{c}, &frameDelivery{}, screen, 80, now.Add(duplicateRefreshInterval+time.Second))
	want := []bool{true, false, false, true, true}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys %v, want %v", keys, want)
		}
	}
}

func TestBareJpegsFromTileReadsAreDeduplicated(t *testing.T) {
	defer func(read func(uint16, uint16, uint16, bool, uint16) ([]byte, int)) { readTiles = read }(readTiles)
	jpeg := []byte{0xff, 0xd8, 0xff, 0xc0, 0x00, 0x0b, 0x08, 0x02, 0xd0, 0x05, 0x00, 0x01, 0x01, 0x11, 0x00, 0xff, 0xd9}
	readTiles = func(_, _, _ uint16, _ bool, _ uint16) ([]byte, int) { return append([]byte(nil), jpeg...), 0 }
	c := newTiledClient(context.Background())
	tiles := tileDelivery{wantWhole: true}
	delivery := frameDelivery{}
	screen := &common.Screen{Width: 2560, Height: 1440}
	now := time.Now()
	if !tiles.read([]*mjpegClient{c}, &delivery, screen, 80, now) || !common.MjpegTilesMessageWhole(<-c.frames) {
		t.Fatal("the first bare JPEG did not reach the viewer whole")
	}
	if tiles.read([]*mjpegClient{c}, &delivery, screen, 80, now.Add(time.Second)) || len(c.frames) != 0 {
		t.Fatal("an unchanged bare JPEG was sent again")
	}
	if !tiles.read([]*mjpegClient{c}, &delivery, screen, 80, now.Add(duplicateRefreshInterval+time.Second)) {
		t.Fatal("no periodic refresh")
	}
}
