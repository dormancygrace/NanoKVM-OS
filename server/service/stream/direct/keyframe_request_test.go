package direct

import (
	"testing"
	"time"
)

// keyframeProbe replaces the capture owner and the wall clock so recovery
// requests can be counted deterministically.
type keyframeProbe struct {
	requests int
	now      time.Time
}

func (p *keyframeProbe) attach(q *frameQueue) *frameQueue {
	p.now = time.Unix(1000, 0)
	q.requestKeyframe = func() { p.requests++ }
	q.now = func() time.Time { return p.now }
	return q
}

func (p *keyframeProbe) advance(d time.Duration) { p.now = p.now.Add(d) }

func (p *keyframeProbe) expect(t *testing.T, want int, what string) {
	t.Helper()
	if p.requests != want {
		t.Fatalf("%s: %d keyframe request(s), want %d", what, p.requests, want)
	}
}

func deltaOfSize(timestamp int64, size int) *outboundFrame {
	return newOutboundFrame(false, timestamp, nil, make([]byte, size))
}

func keyOfSize(timestamp int64, size int) *outboundFrame {
	return newOutboundFrame(true, timestamp, nil, make([]byte, size))
}

func TestOverflowRequestsKeyframeAndRepeatsWhileStalled(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(2, 1024))
	q.offer(keyOfSize(1, 1))
	probe.expect(t, 0, "keyframe")
	q.offer(deltaOfSize(2, 4))
	probe.expect(t, 0, "queue exactly full")
	if q.offer(deltaOfSize(4, 4)) {
		t.Fatal("overflowing delta was accepted")
	}
	probe.expect(t, 1, "overflow")

	// The decoder still looks healthy to the browser, so the server has to
	// keep asking while it drops frames; but not for every frame.
	for ts := int64(5); ts < 50; ts++ {
		probe.advance(10 * time.Millisecond)
		q.offer(deltaOfSize(ts, 4))
	}
	probe.expect(t, 1, "deltas dropped inside the repeat interval")
	probe.advance(keyframeRequestInterval)
	q.offer(deltaOfSize(60, 4))
	probe.expect(t, 2, "first dropped delta after the interval")
	q.offer(deltaOfSize(61, 4))
	probe.expect(t, 2, "second dropped delta after the repeat")

	if !q.offer(keyOfSize(70, 1)) {
		t.Fatal("recovery keyframe rejected")
	}
	if !q.offer(deltaOfSize(71, 4)) {
		t.Fatal("delta after recovery rejected")
	}
	probe.expect(t, 2, "recovered stream")
}

func TestOversizedKeyframeRequestsBackOff(t *testing.T) {
	var p keyframeProbe
	q := p.attach(newFrameQueue(8, 100))
	// The encoder answers every request with an IDR that does not fit, one
	// step later. The window after each oversized IDR is 2, 4, 8, 16 and then
	// 30 s, so requests go out at ~2.0, ~6.1, ~14.2 and ~30.3 s; the next one
	// is due at ~60.4 s, after the observed period.
	const step = 100 * time.Millisecond
	q.offer(keyOfSize(1, 200))
	var times []time.Duration
	var elapsed time.Duration
	answered := p.requests
	for ts := int64(2); elapsed < 60*time.Second; ts++ {
		p.advance(step)
		elapsed += step
		if p.requests != answered {
			answered = p.requests
			q.offer(keyOfSize(ts, 200))
			continue
		}
		q.offer(deltaOfSize(ts, 10))
		if p.requests != answered {
			times = append(times, elapsed)
		}
	}
	want := []time.Duration{2 * time.Second, 6100 * time.Millisecond, 14200 * time.Millisecond, 30300 * time.Millisecond}
	if len(times) != len(want) {
		t.Fatalf("requests at %v, want at %v", times, want)
	}
	for i := range want {
		if times[i] != want[i] {
			t.Fatalf("requests at %v, want at %v", times, want)
		}
	}
	if q.oversizedBackoff != oversizedBackoffMax {
		t.Fatalf("window = %v, want the %v cap", q.oversizedBackoff, oversizedBackoffMax)
	}
}
