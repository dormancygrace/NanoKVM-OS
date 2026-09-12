package webrtc

import (
	"NanoKVM-Server/service/stream"
	"testing"
	"time"
)

func receiveFrame(t *testing.T, ch <-chan stream.VideoFrame) stream.VideoFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(time.Second):
		t.Fatal("writer stalled")
		return stream.VideoFrame{}
	}
}

func TestPeerWriterSlowPeerAndRecovery(t *testing.T) {
	entered := make(chan stream.VideoFrame, 8)
	unblock := make(chan struct{})
	slow := newPeerVideoWriter(func(f stream.VideoFrame) error { entered <- f; <-unblock; return nil }, func(error) { t.Error("unexpected write failure") })
	defer slow.close()
	defer close(unblock)
	fastFrames := make(chan stream.VideoFrame, 8)
	fast := newPeerVideoWriter(func(f stream.VideoFrame) error { fastFrames <- f; return nil }, func(error) { t.Error("unexpected write failure") })
	defer fast.close()
	key := stream.VideoFrame{Result: 3, Timestamp: 1}
	delta := stream.VideoFrame{Result: 4, Timestamp: 2}
	if slow.offer(delta) {
		t.Fatal("accepted initial delta")
	}
	slow.offer(key)
	receiveFrame(t, entered)
	if !slow.offer(delta) {
		t.Fatal("pending slot unavailable")
	}
	if slow.offer(delta) {
		t.Fatal("overflow accepted")
	}
	if slow.offer(delta) {
		t.Fatal("accepted delta after loss")
	}
	if !fast.offer(key) {
		t.Fatal("fast peer rejected keyframe")
	}
	receiveFrame(t, fastFrames)
	if !fast.offer(delta) {
		t.Fatal("slow peer blocked fast peer")
	}
	receiveFrame(t, fastFrames)
	if !slow.offer(key) {
		t.Fatal("IDR did not repair overflow")
	}
	unblock <- struct{}{}
	if f := receiveFrame(t, entered); !f.IsKeyframe() {
		t.Fatal("sent damaged delta sequence")
	}
	// Close must return even though the second track write is blocked.
	slow.close()
	if slow.offer(key) {
		t.Fatal("accepted frame after close")
	}
	slow.close()
}

func TestPeerWriterCloseDiscardsPending(t *testing.T) {
	entered := make(chan stream.VideoFrame, 2)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(f stream.VideoFrame) error { entered <- f; <-unblock; return nil }, func(error) {})
	w.offer(stream.VideoFrame{Result: 3})
	receiveFrame(t, entered)
	w.offer(stream.VideoFrame{Result: 4})
	w.close()
	close(unblock)
	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("writer leaked")
	}
	if len(entered) != 0 {
		t.Fatal("closed writer sent pending data")
	}
}
