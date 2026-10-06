package common

import (
	"testing"
	"time"
)

// Portrait sizes share the landscape tiers: 1080x1920 at 100, 1440x2560 at 60.
func TestPortraitEffectiveFPSIsBoundedWithoutChangingRequest(t *testing.T) {
	screen := GetScreen()
	previous := *screen
	defer func() { *screen = previous }()
	screen.FPS, screen.Width, screen.Height = 120, 0, 0
	sourceTiming.Lock()
	width, height, expires := sourceTiming.width, sourceTiming.height, sourceTiming.expires
	sourceTiming.width, sourceTiming.height = 1088, 1920
	sourceTiming.expires = time.Now().Add(time.Hour)
	sourceTiming.Unlock()
	defer func() {
		sourceTiming.Lock()
		sourceTiming.width, sourceTiming.height, sourceTiming.expires = width, height, expires
		sourceTiming.Unlock()
	}()
	if got := GetCaptureScreen().FPS; got != 100 {
		t.Fatalf("portrait effective FPS = %d, want 100", got)
	}
	if screen.FPS != 120 {
		t.Fatal("effective cap changed the saved request")
	}
	for _, size := range [][3]int{{720, 1280, 120}, {1080, 1920, 100}, {1296, 2304, 60}, {1440, 2560, 60}} {
		sourceTiming.Lock()
		sourceTiming.width, sourceTiming.height = size[0], size[1]
		sourceTiming.Unlock()
		if got := GetCaptureScreen().FPS; got != size[2] {
			t.Fatalf("%v FPS = %d", size, got)
		}
	}
	screen.FPS = 40
	if got := GetCaptureScreen().FPS; got != 40 {
		t.Fatalf("portrait effective FPS = %d, want 40", got)
	}
	sourceTiming.Lock()
	sourceTiming.width, sourceTiming.height = 1440, 2560
	sourceTiming.Unlock()
	screen.FPS, screen.Width, screen.Height = 60, 2560, 1440
	if got := GetCaptureScreen().FPS; got != 60 {
		t.Fatalf("maximum portrait effective FPS = %d, want 60", got)
	}
}
