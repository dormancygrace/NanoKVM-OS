package common

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
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
	for _, c := range [][5]int{{3840, 2160, 0, 0, 30}, {3840, 2160, 2560, 1440, 30}, {2560, 1440, 0, 0, 50}, {2560, 1440, 1920, 1080, 50}, {2560, 1440, 1280, 720, 50}, {1920, 1080, 0, 0, 75}, {1920, 1080, 1280, 720, 75}, {1280, 720, 0, 0, 120}, {640, 480, 640, 480, 120}} {
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

func TestCaptureRateOrientationParity(t *testing.T) {
	for _, size := range [][3]int{{1280, 720, 120}, {1920, 1080, 75}, {1920, 1088, 75}, {2560, 1440, 50}, {2304, 1296, 50}, {3840, 2160, 30}} {
		if got := CaptureRateLimit(size[0], size[1]); got != size[2] {
			t.Errorf("%v: %d", size, got)
		}
		if got := CaptureRateLimit(size[1], size[0]); got != size[2] {
			t.Errorf("rotated %v: %d", size, got)
		}
	}
}

// The native capture library enforces the same table under its own mutex.
func TestCaptureRateTiersMatchNative(t *testing.T) {
	source, err := os.ReadFile("../../support/sg2002/additional/kvm_mmf/include/internal/capture_rate.hpp")
	if err != nil {
		t.Fatal(err)
	}
	table := regexp.MustCompile(`(?s)capture_rate_tiers\[\] = \{(.*?)\n\};`).FindSubmatch(source)
	if table == nil {
		t.Fatal("capture_rate_tiers not found")
	}
	var native []CaptureRateTier
	for _, m := range regexp.MustCompile(`\{(\d+), (\d+), (\d+)\}`).FindAllStringSubmatch(string(table[1]), -1) {
		l, _ := strconv.Atoi(m[1])
		s, _ := strconv.Atoi(m[2])
		f, _ := strconv.Atoi(m[3])
		native = append(native, CaptureRateTier{LongSide: l, ShortSide: s, FPS: f})
	}
	if !reflect.DeepEqual(native, CaptureRateTiers) {
		t.Fatalf("native %v, server %v", native, CaptureRateTiers)
	}
}
