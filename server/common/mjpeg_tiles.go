package common

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Tiled MJPEG (kvm_vision.h, kvmv_read_mjpeg_tiles_sink): the parts of the
// picture that changed, found by the IVE engine, as JPEG tiles in one message.
const (
	// MjpegTilesResult: the data is a message.
	MjpegTilesResult = 6
	// MjpegTilesUnsupported: the native library has no tiled MJPEG.
	MjpegTilesUnsupported = -9
	MjpegTileSize         = 128
	// MjpegTilesWhole flags a message that holds the whole picture.
	MjpegTilesWhole = 1

	mjpegTilesHeader = 8
	mjpegTilesRecord = 12
)

// The IVE driver is loaded on demand (it is kept out at boot). A driver
// without osdrv patch 0020 gives up on a full HD operation after 4 ms and
// leaves wrong maps; it has no timeout_ms parameter.
const iveTimeoutParameter = "/sys/module/cv181x_ive/parameters/timeout_ms"

var (
	mjpegTilesOnce      sync.Once
	mjpegTilesAvailable bool
)

// MjpegTilesAvailable loads the IVE driver on first use and reports whether
// tiled MJPEG can find changes with it. The native library checks again.
func MjpegTilesAvailable() bool {
	mjpegTilesOnce.Do(func() {
		if VideoDisabled() {
			return
		}
		if _, err := os.Stat(iveTimeoutParameter); err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if output, err := exec.CommandContext(ctx, "modprobe", "cv181x_ive").CombinedOutput(); err != nil {
				log.Warnf("tiled MJPEG unavailable: cannot load the IVE driver: %v: %s", err, strings.TrimSpace(string(output)))
				return
			}
		}
		raw, err := os.ReadFile(iveTimeoutParameter)
		timeout, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || parseErr != nil || timeout < 20 {
			log.Warn("tiled MJPEG unavailable: the IVE driver gives up on full HD operations too early")
			return
		}
		mjpegTilesAvailable = true
	})
	return mjpegTilesAvailable
}

// MjpegTilesWholeMessage wraps a JPEG of the whole picture as a message.
func MjpegTilesWholeMessage(jpeg []byte) []byte {
	width, height, ok := JpegSize(jpeg)
	if !ok {
		return nil
	}
	message := make([]byte, mjpegTilesHeader+mjpegTilesRecord, mjpegTilesHeader+mjpegTilesRecord+len(jpeg))
	message[0] = 1
	message[1] = MjpegTilesWhole
	binary.LittleEndian.PutUint16(message[2:], 1)
	binary.LittleEndian.PutUint16(message[4:], uint16(width))
	binary.LittleEndian.PutUint16(message[6:], uint16(height))
	record := message[mjpegTilesHeader:]
	binary.LittleEndian.PutUint16(record[4:], uint16(width))
	binary.LittleEndian.PutUint16(record[6:], uint16(height))
	binary.LittleEndian.PutUint32(record[8:], uint32(len(jpeg)))
	return append(message, jpeg...)
}

// MjpegTilesMessageWhole reports whether a message holds the whole picture.
func MjpegTilesMessageWhole(message []byte) bool {
	return len(message) >= mjpegTilesHeader && message[1]&MjpegTilesWhole != 0
}

// JpegSize reads the picture size from a JPEG's start-of-frame segment.
func JpegSize(jpeg []byte) (width, height int, ok bool) {
	if len(jpeg) < 4 || jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		return 0, 0, false
	}
	for at := 2; at+9 <= len(jpeg); {
		if jpeg[at] != 0xff {
			return 0, 0, false
		}
		marker := jpeg[at+1]
		if marker == 0xff {
			at++
			continue
		}
		length := int(binary.BigEndian.Uint16(jpeg[at+2:]))
		// SOF0..SOF15 except DHT (c4), JPG (c8) and DAC (cc).
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			height = int(binary.BigEndian.Uint16(jpeg[at+5:]))
			width = int(binary.BigEndian.Uint16(jpeg[at+7:]))
			return width, height, width > 0 && height > 0
		}
		if marker == 0xda || length < 2 {
			return 0, 0, false
		}
		at += 2 + length
	}
	return 0, 0, false
}
