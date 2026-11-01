package direct

import (
	"testing"
	"time"
)

func TestQueueDepthFollowsStreamRate(t *testing.T) {
	for _, tc := range []struct {
		fps  int
		want int
	}{
		{10, defaultQueueFrames}, // the floor keeps slow streams as tolerant as before
		{30, defaultQueueFrames},
		{50, defaultQueueFrames}, // 7.5 frames
		{60, 9},
		{75, 11},
		{100, 15},
		{120, 18},
		{240, maxQueueFrames},
	} {
		if got := queueDepthFor(time.Second / time.Duration(tc.fps)); got != tc.want {
			t.Errorf("%d fps: depth %d, want %d", tc.fps, got, tc.want)
		}
	}
	if got := queueDepthFor(0); got != defaultQueueFrames {
		t.Errorf("unknown rate: depth %d, want %d", got, defaultQueueFrames)
	}
}
