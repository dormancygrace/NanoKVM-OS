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

func useDefaultPeerWriterQueue(t *testing.T) {
	t.Helper()
	t.Setenv(peerVideoQueueEnv, "")
}

func TestPeerWriterSlowPeerAndRecovery(t *testing.T) {
	useDefaultPeerWriterQueue(t)
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
	for i := 0; i < peerVideoQueueCapacity; i++ {
		if !slow.offer(delta) {
			t.Fatal("pending queue unavailable")
		}
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

// A large IDR can hold the sender while several ordinary frames arrive.
// They must survive in order instead of forcing another one-second IDR wait.
func TestPeerWriterPreservesIDRBurst(t *testing.T) {
	useDefaultPeerWriterQueue(t)
	entered := make(chan stream.VideoFrame, 16)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(f stream.VideoFrame) error {
		entered <- f
		if f.IsKeyframe() {
			<-unblock
		}
		return nil
	}, func(error) { t.Error("unexpected write failure") })
	defer w.close()
	defer close(unblock)
	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 1}) {
		t.Fatal("key rejected")
	}
	receiveFrame(t, entered)
	for i := int64(2); i <= 6; i++ {
		if !w.offer(stream.VideoFrame{Result: 4, Timestamp: i}) {
			t.Fatal("ordinary IDR burst discarded")
		}
	}
	unblock <- struct{}{}
	for i := int64(2); i <= 6; i++ {
		if f := receiveFrame(t, entered); f.Timestamp != i {
			t.Fatalf("frame order: got %d want %d", f.Timestamp, i)
		}
	}
}
