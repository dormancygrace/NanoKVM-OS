package audio

import (
	"testing"
	"time"
)

func TestPauseGap(t *testing.T) {
	for _, c := range []struct {
		elapsed time.Duration
		dropped uint64
		want    time.Duration
	}{
		{20 * time.Millisecond, 0, 0},
		// burst jitter is not a pause
		{150 * time.Millisecond, 0, 0},
		{400 * time.Millisecond, 0, 0},
		{600 * time.Millisecond, 0, 580 * time.Millisecond},
		// dropped frames are counted by PrevDroppedPackets, not here
		{520 * time.Millisecond, 10, 0},
		{5 * time.Second, 0, 5*time.Second - 20*time.Millisecond},
		{5 * time.Second, 4, 5*time.Second - 100*time.Millisecond},
		// a capture restart starts the index again; the clock still counts
		{-time.Second, 0, 0},
	} {
		if got := PauseGap(c.elapsed, c.dropped); got != c.want {
			t.Errorf("PauseGap(%s, %d) = %s, want %s", c.elapsed, c.dropped, got, c.want)
		}
	}
}
