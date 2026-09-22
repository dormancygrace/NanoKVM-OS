package common

import (
	"testing"
	"time"
)

func TestCaptureRatesAcrossSourceAndDownscale(t *testing.T) {
	screen := GetScreen()
	before := *screen
	defer func() { *screen = before }()
	sourceTiming.Lock()
	sw, sh, exp := sourceTiming.width, sourceTiming.height, sourceTiming.expires
	sourceTiming.Unlock()
	defer func() {
		sourceTiming.Lock()
		sourceTiming.width, sourceTiming.height, sourceTiming.expires = sw, sh, exp
		sourceTiming.Unlock()
	}()
	for _, c := range [][5]int{{2560, 1440, 0, 0, 50}, {2560, 1440, 1920, 1080, 50}, {2560, 1440, 1280, 720, 50}, {1920, 1080, 0, 0, 75}, {1920, 1080, 1280, 720, 75}, {1280, 720, 0, 0, 120}, {640, 480, 640, 480, 120}} {
		sourceTiming.Lock()
		sourceTiming.width, sourceTiming.height, sourceTiming.expires = c[0], c[1], time.Now().Add(time.Hour)
		sourceTiming.Unlock()
		screen.Width, screen.Height, screen.FPS = uint16(c[2]), uint16(c[3]), 120
		if got := GetCaptureScreen().FPS; got != c[4] {
			t.Errorf("%v got %d", c, got)
		}
		if screen.FPS != 120 {
			t.Fatal("saved request changed")
		}
		screen.FPS = 25
		if got := GetCaptureScreen().FPS; got != 25 {
			t.Errorf("25 FPS request raised to %d", got)
		}
	}
}
