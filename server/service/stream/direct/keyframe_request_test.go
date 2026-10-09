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

func TestOversizedFramesDoNotRequestKeyframes(t *testing.T) {
	var probe keyframeProbe
	q := probe.attach(newFrameQueue(8, 100))
	q.offer(keyOfSize(1, 20))
	if q.offer(deltaOfSize(2, 100)) {
		t.Fatal("delta above the byte limit was accepted")
	}
	probe.expect(t, 0, "single delta above the byte limit")
	probe.advance(10 * keyframeRequestInterval)
	q.offer(deltaOfSize(3, 10))
	probe.expect(t, 0, "later deltas while an IDR cannot fit")

	if q.offer(keyOfSize(4, 100)) {
		t.Fatal("keyframe above the byte limit was accepted")
	}
	probe.advance(10 * keyframeRequestInterval)
	q.offer(deltaOfSize(5, 10))
	probe.expect(t, 0, "keyframe above the byte limit")

	// A keyframe that fits proves requests are useful again.
	if !q.offer(keyOfSize(6, 20)) {
		t.Fatal("fitting keyframe rejected")
	}
	q.markDiscontinuity()
	probe.expect(t, 1, "discontinuity after recovery")
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
