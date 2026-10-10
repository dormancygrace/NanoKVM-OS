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

// queuedAtOverflow offers a keyframe and then deltas of the given interval
// and reports how many frames the queue holds when the first delta is refused.
func queuedAtOverflow(t *testing.T, interval time.Duration, timeBased bool) int {
	t.Helper()
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(defaultQueueFrames, defaultQueueBytes))
	q.timeBased = timeBased
	key := keyOfSize(0, 100)
	key.duration = interval
	q.offer(key)
	for i := 1; i < 100; i++ {
		delta := deltaOfSize(int64(i), 100)
		delta.duration = interval
		if !q.offer(delta) {
			return i
		}
	}
	t.Fatal("queue never overflowed")
	return 0
}

func TestQueueHoldsAboutQueueDelayAtAnyRate(t *testing.T) {
	for _, tc := range []struct {
		fps  int
		want int
	}{{30, 8}, {60, 9}, {100, 15}} {
		interval := time.Second / time.Duration(tc.fps)
		if got := queuedAtOverflow(t, interval, true); got != tc.want {
			t.Errorf("%d fps: %d frames queued, want %d", tc.fps, got, tc.want)
		}
	}
	// Only streams opened by newClient adapt: the fixed depth stays available.
	if got := queuedAtOverflow(t, 10*time.Millisecond, false); got != defaultQueueFrames {
		t.Errorf("fixed depth: %d frames queued, want %d", got, defaultQueueFrames)
	}
}

func TestClientQueueIsTimeBased(t *testing.T) {
	if !newClient(nil).queue.timeBased {
		t.Fatal("client queue does not follow the stream rate")
	}
}

func TestCaptureDemandUsesCurrentDepth(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(defaultQueueFrames, defaultQueueBytes))
	q.timeBased = true
	q.enableFlowControl(1)
	interval := 10 * time.Millisecond
	for i := range 12 {
		frame := deltaOfSize(int64(i), 100)
		if i == 0 {
			frame = keyOfSize(0, 100)
		}
		frame.duration = interval
		q.offer(frame)
	}
	// Twelve frames pending is over the old fixed depth of eight.
	if _, _, canAdvance := q.captureState(); !canAdvance {
		t.Fatal("capture stopped before the time-based depth was reached")
	}
}
