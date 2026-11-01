package mjpeg

import (
	"context"
	"testing"

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
