package webrtc

import (
	"NanoKVM-Server/service/stream"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func countKeyframeRequests(t *testing.T) *atomic.Int32 {
	t.Helper()
	var requests atomic.Int32
	previous := requestVideoKeyframe
	requestVideoKeyframe = func() { requests.Add(1) }
	t.Cleanup(func() { requestVideoKeyframe = previous })
	return &requests
}

// A viewer that joins while another watches starts on deltas: it asks for a
// keyframe instead of staying black until the periodic one.
func TestANewWriterAsksForAKeyframe(t *testing.T) {
	useDefaultPeerWriterQueue(t)
	requests := countKeyframeRequests(t)
	clock := time.Unix(1000, 0)
	w := newPeerVideoWriter(func(stream.VideoFrame) error { return nil }, func(error) {})
	defer w.close()
	w.now = func() time.Time { return clock }
	delta := stream.VideoFrame{Result: 4, Timestamp: 1}
	for range 10 {
		w.offer(delta)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("%d keyframe requests in one second, want 1", got)
	}
	// Still waiting: again after 1 s, then 2 s, 4 s, then every 8 s.
	asked := []time.Duration{}
	start := clock
	for range 30 {
		clock = clock.Add(time.Second)
		before := requests.Load()
		w.offer(delta)
		if requests.Load() > before {
			asked = append(asked, clock.Sub(start))
		}
	}
	want := []time.Duration{1, 3, 7, 15, 23}
	for i, seconds := range want {
		want[i] = seconds * time.Second
	}
	if len(asked) != len(want) {
		t.Fatalf("asked at %v, want %v", asked, want)
	}
	for i := range want {
		if asked[i] != want[i] {
			t.Fatalf("asked at %v, want %v", asked, want)
		}
	}
	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 2}) {
		t.Fatal("keyframe refused")
	}
	clock = clock.Add(5 * time.Second)
	for range 5 {
		if !w.offer(delta) {
			t.Fatal("delta after the keyframe refused")
		}
	}
	if got := requests.Load(); got != 6 {
		t.Fatalf("a writer that has its keyframe asked again: %d", got)
	}
	// A new wait starts over at once and at 1 s.
	w.mu.Lock()
	w.repairing = true
	w.mu.Unlock()
	w.offer(delta)
	if got := requests.Load(); got != 7 {
		t.Fatalf("a new wait did not ask at once: %d", got)
	}
}

// A queue overflow drops reference pictures without an RTP gap, so the
// browser sends no PLI: the writer asks itself.
func TestAnOverflowAsksForAKeyframe(t *testing.T) {
	useDefaultPeerWriterQueue(t)
	requests := countKeyframeRequests(t)
	unblock := make(chan struct{})
	entered := make(chan stream.VideoFrame, 1)
	w := newPeerVideoWriter(func(f stream.VideoFrame) error {
		select {
		case entered <- f:
		default:
		}
		<-unblock
		return nil
	}, func(error) {})
	defer w.close()
	defer close(unblock)
	w.offer(stream.VideoFrame{Result: 3, Timestamp: 1})
	<-entered
	base := requests.Load()
	delta := stream.VideoFrame{Result: 4, Timestamp: 2}
	for i := 0; i <= peerVideoQueueCapacity; i++ {
		w.offer(delta)
	}
	if requests.Load() <= base {
		t.Fatal("an overflow did not ask for a keyframe")
	}
}

// A frame the path MTU cannot carry drains the queue; the next delta asks.
func TestABudgetErrorAsksForAKeyframe(t *testing.T) {
	useDefaultPeerWriterQueue(t)
	requests := countKeyframeRequests(t)
	written := make(chan struct{}, 1)
	w := newPeerVideoWriter(func(stream.VideoFrame) error {
		written <- struct{}{}
		return errVideoBudget
	}, func(err error) {
		if !errors.Is(err, errVideoBudget) {
			t.Error(err)
		}
	})
	defer w.close()
	clock := time.Unix(1000, 0)
	w.now = func() time.Time { return clock }
	w.offer(stream.VideoFrame{Result: 3, Timestamp: 1})
	<-written
	clock = clock.Add(2 * time.Second)
	deadline := time.Now().Add(time.Second)
	for requests.Load() == 0 && time.Now().Before(deadline) {
		w.offer(stream.VideoFrame{Result: 4, Timestamp: 2})
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() == 0 {
		t.Fatal("a budget error did not lead to a keyframe request")
	}
}
