package common

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestH264AllowedForFollowsTheEncodedSize(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		limit         uint16
		outW, outH    int
		allowed       bool
	}{
		{3840, 2160, 0, 3840, 2160, false},
		{3840, 2160, 2160, 3840, 2160, false},
		{3840, 2160, 1440, 2560, 1440, true},
		{3840, 2160, 1080, 1920, 1080, true},
		{3840, 2160, 720, 1280, 720, true},
		{3840, 2160, 600, 800, 450, true},
		{2560, 1440, 0, 2560, 1440, true},
		{2560, 1440, 2160, 2560, 1440, true},
		{2560, 1440, 1080, 1920, 1080, true},
		{1920, 1080, 0, 1920, 1080, true},
		{1920, 1080, 2160, 1920, 1080, true},
		// Ultrawide: too long at its own size, fitted into 2560x1440 it is fine.
		{3440, 1440, 0, 3440, 1440, false},
		{3440, 1440, 2160, 3440, 1440, false},
		{3440, 1440, 1440, 2560, 1071, true},
		// Portrait: the limits are turned upright, as native capture does.
		{1440, 2560, 0, 1440, 2560, false},
		{1440, 2560, 2160, 1440, 2560, false},
		{1440, 2560, 1440, 1440, 2560, false},
		{1440, 2560, 1080, 1080, 1920, true},
		{1440, 2560, 720, 720, 1280, true},
		{1088, 1920, 0, 1088, 1920, true},
		{1080, 1920, 0, 1080, 1920, true},
		{720, 1280, 0, 720, 1280, true},
	} {
		w, h := h264OutputSize(tc.width, tc.height, tc.limit)
		if w != tc.outW || h != tc.outH {
			t.Errorf("%dx%d at %d: encodes %dx%d, want %dx%d", tc.width, tc.height, tc.limit, w, h, tc.outW, tc.outH)
		}
		if got := H264AllowedFor(tc.width, tc.height, tc.limit); got != tc.allowed {
			t.Errorf("%dx%d at %d: allowed %v, want %v", tc.width, tc.height, tc.limit, got, tc.allowed)
		}
	}
	if !H264AllowedFor(0, 0, 0) || !H264AllowedFor(0, 0, 2160) {
		t.Error("an unknown input is refused")
	}
}

func TestH264SelectionAcceptsTheSavedMonitor(t *testing.T) {
	previous := monitorTargetSize
	t.Cleanup(func() { monitorTargetSize = previous })
	for _, tc := range []struct {
		name            string
		capture, target [2]int
		limit           uint16
		allowed         bool
	}{
		{"4K input, no programmable monitor", [2]int{3840, 2160}, [2]int{0, 0}, 0, false},
		{"4K input, 4K monitor", [2]int{3840, 2160}, [2]int{3840, 2160}, 0, false},
		{"4K input, monitor lowered to 1440p", [2]int{3840, 2160}, [2]int{2560, 1440}, 0, true},
		{"4K input, limited", [2]int{3840, 2160}, [2]int{3840, 2160}, 1440, true},
		{"1080p input, 4K monitor", [2]int{1920, 1080}, [2]int{3840, 2160}, 0, true},
	} {
		target := tc.target
		monitorTargetSize = func() (int, int) { return target[0], target[1] }
		if got := H264SelectionAllowed(tc.capture[0], tc.capture[1], tc.limit); got != tc.allowed {
			t.Errorf("%s: allowed %v, want %v", tc.name, got, tc.allowed)
		}
	}
}

// The native guard and the server use the same limits.
func TestH264LimitsMatchTheNativeHeader(t *testing.T) {
	const header = "../../support/sg2002/additional/kvm/include/stream_size.hpp"
	text, err := os.ReadFile(header)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"h264_max_long_side": H264MaxLongSide, "h264_max_height": H264MaxHeight} {
		match := regexp.MustCompile(`(?m)^\s*inline constexpr unsigned ` + name + ` = (\d+);`).FindSubmatch(text)
		if match == nil {
			t.Fatalf("%s: %s is not defined", header, name)
		}
		if got, _ := strconv.Atoi(string(match[1])); got != want {
			t.Errorf("%s: %s = %d, Go %d", header, name, got, want)
		}
	}
}
