package mjpeg

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
	"context"
	"time"
)

// Tiled MJPEG viewers get messages of the changed tiles (common/mjpeg_tiles.go)
// over a WebSocket. A message builds on the ones before it, so a viewer that
// misses one waits for the next whole picture.

// About the encode time and bytes of one whole 1080p picture: more changed
// tiles than this make a whole picture.
const mjpegMaxTiles = 24

// Tile messages queued per viewer before it is resynchronised.
const tiledQueue = 8

var (
	tilesAvailable = common.MjpegTilesAvailable
	readTiles      = func(width, height, quality uint16, key bool, maxTiles uint16) ([]byte, int) {
		return common.GetKvmVision().ReadMjpegTiles(width, height, quality, key, maxTiles)
	}
	releaseTiles = func() { common.GetKvmVision().ReleaseMjpegTiles() }
)

func newTiledClient(ctx context.Context) *mjpegClient {
	return &mjpegClient{
		ctx:    ctx,
		frames: make(chan []byte, tiledQueue),
		tiled:  true,
		resync: true,
	}
}

// offerTile queues a message. A full queue is dropped: the viewer then takes
// nothing but the next whole picture.
func (c *mjpegClient) offerTile(message []byte, whole bool) bool {
	if c.resync && !whole {
		return false
	}
	select {
	case c.frames <- message:
		c.resync = false
		return true
	default:
	}
	for drained := false; !drained; {
		select {
		case <-c.frames:
		default:
			drained = true
		}
	}
	c.resync = true
	if whole {
		c.frames <- message
		c.resync = false
		return true
	}
	return false
}

// tilesOnly: every viewer takes tiles and the device can find changes. With
// a classic viewer every read is a whole JPEG, which tiled viewers get as a
// whole-picture message.
func tilesOnly(clients []*mjpegClient) bool {
	for _, c := range clients {
		if !c.tiled {
			return false
		}
	}
	return len(clients) > 0 && tilesAvailable()
}

type tileDelivery struct {
	nextWhole time.Time
	wantWhole bool
	// used: tiles were read since the stream started.
	used bool
}

// release frees the native map and channels when the stream stops.
func (t *tileDelivery) release() {
	if t.used {
		releaseTiles()
		t.used = false
	}
}

// read reads one message and offers it to the viewers. A bare JPEG (no
// change map) goes through the classic delivery, which sends it only when it
// differs. It reports whether a viewer took one.
func (t *tileDelivery) read(clients []*mjpegClient, delivery *frameDelivery, screen *common.Screen, quality int, now time.Time) bool {
	key := t.wantWhole || !now.Before(t.nextWhole)
	for _, c := range clients {
		key = key || c.resync
	}
	t.used = true
	data, result := readTiles(screen.Width, screen.Height, uint16(quality), key, mjpegMaxTiles)
	status := result
	if status == common.MjpegTilesResult {
		status = 0
	}
	stream.UpdateCaptureStatus(stream.CaptureModeMJPEG, status)
	if result == common.MjpegBlockedResult {
		logBlocked()
	} else if result >= 0 {
		blockedLogged.Store(false)
	}
	if result == 5 {
		return false
	}
	if result == 0 && len(data) > 0 {
		return delivery.offer(clients, data, now)
	}
	message := data
	if result != common.MjpegTilesResult || len(message) == 0 {
		// Signal loss and recovery start again from a whole picture.
		t.wantWhole = true
		delivery.last = nil
		return false
	}
	// The viewers now hold more than the last bare JPEG.
	delivery.last = nil
	whole := common.MjpegTilesMessageWhole(message)
	if whole {
		t.wantWhole = false
		t.nextWhole = now.Add(duplicateRefreshInterval)
	}
	sent := false
	for _, c := range clients {
		if c.ctx.Err() == nil && c.offerTile(message, whole) {
			sent = true
		}
	}
	return sent
}
