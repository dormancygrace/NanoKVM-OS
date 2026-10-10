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

func TestByteOverflowRequestsKeyframe(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 20))
	q.offer(deltaOfSize(2, 40))
	if q.offer(deltaOfSize(3, 60)) {
		t.Fatal("frame beyond the byte limit was accepted")
	}
	probe.expect(t, 1, "combined size over the limit")
}

func TestOversizedFramesDoNotRequestKeyframesInsideTheWindow(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 20))
	if q.offer(deltaOfSize(2, 100)) {
		t.Fatal("delta above the byte limit was accepted")
	}
	probe.expect(t, 0, "single delta above the byte limit")
	probe.advance(oversizedBackoffMin - time.Millisecond)
	q.offer(deltaOfSize(3, 10))
	probe.expect(t, 0, "later deltas inside the suppression window")

	if q.offer(keyOfSize(4, 100)) {
		t.Fatal("keyframe above the byte limit was accepted")
	}
	probe.advance(time.Millisecond)
	q.offer(deltaOfSize(5, 10))
	probe.expect(t, 1, "oversized keyframe inside the window does not extend it")
	q.offer(deltaOfSize(6, 10))
	probe.expect(t, 1, "second refused delta right after the request")

	// A keyframe that fits proves requests are useful again.
	if !q.offer(keyOfSize(7, 20)) {
		t.Fatal("fitting keyframe rejected")
	}
	probe.advance(keyframeRequestInterval)
	q.markDiscontinuity()
	probe.expect(t, 2, "discontinuity after recovery")
}

func TestRecoveryAfterTransientOversizedDelta(t *testing.T) {
	var p keyframeProbe
	q := p.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 20))
	q.offer(deltaOfSize(2, 200))
	p.advance(2 * time.Second)
	q.offer(deltaOfSize(3, 10))
	if p.requests == 0 {
		t.Fatal("recovery requests remain disabled after a transient oversized frame")
	}
	p.expect(t, 1, "first request after the window")
	if !q.offer(keyOfSize(4, 20)) {
		t.Fatal("recovery keyframe rejected")
	}
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

func TestOversizedBackoffIsCapped(t *testing.T) {
	var p keyframeProbe
	q := p.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 200))
	for range 10 {
		p.advance(oversizedBackoffMax)
		q.offer(keyOfSize(2, 200)) // arrives right after the previous window
	}
	if q.oversizedBackoff != oversizedBackoffMax {
		t.Fatalf("window = %v, want the %v cap", q.oversizedBackoff, oversizedBackoffMax)
	}
	before := p.requests
	p.advance(oversizedBackoffMax)
	q.offer(deltaOfSize(3, 10))
	p.expect(t, before+1, "request after a capped window")
}

func TestSustainedOversizedDeltasStillRetry(t *testing.T) {
	var p keyframeProbe
	q := p.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 20))
	// Every delta is too big and no IDR is ever produced: the retries stay
	// bounded by the backoff instead of stopping for good.
	for ts := int64(2); ts < 2+60*30; ts++ {
		p.advance(time.Second / 30)
		q.offer(deltaOfSize(ts, 200))
	}
	if p.requests < 3 || p.requests > 6 {
		t.Fatalf("%d requests in 60 s of oversized deltas, want 3..6", p.requests)
	}
}

func TestOversizedWindowIsForgottenAfterQuietPeriod(t *testing.T) {
	var p keyframeProbe
	q := p.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 200))
	p.advance(oversizedBackoffMin)
	q.offer(keyOfSize(2, 200))
	if q.oversizedBackoff != 2*oversizedBackoffMin {
		t.Fatalf("window = %v, want it doubled", q.oversizedBackoff)
	}
	p.advance(q.oversizedBackoff + oversizedBackoffMax)
	q.offer(keyOfSize(3, 200))
	if q.oversizedBackoff != oversizedBackoffMin {
		t.Fatalf("window = %v after a quiet period, want %v", q.oversizedBackoff, oversizedBackoffMin)
	}
}

func TestFittingKeyframeResetsTheBackoff(t *testing.T) {
	var p keyframeProbe
	q := p.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 200))
	for range 2 {
		p.advance(q.oversizedBackoff)
		q.offer(keyOfSize(2, 200))
	}
	if q.oversizedBackoff != 4*oversizedBackoffMin {
		t.Fatalf("window = %v, expected it to have grown to %v", q.oversizedBackoff, 4*oversizedBackoffMin)
	}
	if !q.offer(keyOfSize(3, 20)) {
		t.Fatal("fitting keyframe rejected")
	}
	before := p.requests
	q.markDiscontinuity()
	p.expect(t, before+1, "discontinuity right after a fitting keyframe")

	// The next oversized frame starts again at the minimum window.
	p.advance(keyframeRequestInterval)
	q.offer(deltaOfSize(4, 200))
	p.advance(oversizedBackoffMin)
	q.offer(deltaOfSize(5, 10))
	p.expect(t, before+2, "request after one minimum window")
}

func TestMarkDiscontinuityRequestsKeyframe(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(8, 1024))
	q.enableFlowControl(1)
	q.offer(keyOfSize(1, 1))
	q.markDiscontinuity()
	probe.expect(t, 1, "first discontinuity")
	// Streamer.run calls it for every captured frame while nobody can accept.
	for range 100 {
		probe.advance(5 * time.Millisecond)
		q.markDiscontinuity()
	}
	probe.expect(t, 1, "discontinuity repeated inside the interval")
	probe.advance(keyframeRequestInterval)
	q.markDiscontinuity()
	probe.expect(t, 2, "discontinuity after the interval")
}

func TestJoiningViewerRequestsKeyframeOnFirstDroppedDelta(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(8, 1024))
	q.enableFlowControl(4)
	probe.expect(t, 0, "before any frame")
	q.offer(deltaOfSize(2, 4))
	probe.expect(t, 1, "delta arriving before the first keyframe")
	q.offer(keyOfSize(3, 1))
	q.offer(deltaOfSize(4, 4))
	probe.expect(t, 1, "healthy stream")
}

func TestBrowserResyncSuppressesDuplicateRequests(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(8, 1024))
	q.offer(keyOfSize(1, 1))
	q.requestResync() // handleControl forwards the browser request itself
	q.offer(deltaOfSize(2, 4))
	probe.expect(t, 0, "delta refused after an explicit resync")
	probe.advance(keyframeRequestInterval)
	q.offer(deltaOfSize(3, 4))
	probe.expect(t, 1, "resync keyframe never arrived")
}
