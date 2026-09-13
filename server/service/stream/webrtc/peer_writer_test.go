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

func TestPeerWriterCloseDiscardsPending(t *testing.T) {
	entered := make(chan stream.VideoFrame, 2)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(f stream.VideoFrame) error { entered <- f; <-unblock; return nil }, func(error) {})
	w.offer(stream.VideoFrame{Result: 3})
	receiveFrame(t, entered)
	for i := 0; i < peerVideoQueueCapacity; i++ {
		w.offer(stream.VideoFrame{Result: 4})
	}
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

// A large IDR can hold the sender while several ordinary frames arrive.
// They must survive in order instead of forcing another one-second IDR wait.
func TestPeerWriterPreservesIDRBurst(t *testing.T) {
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

func TestPeerWriterBudgetFailureDiscardsBurst(t *testing.T) {
	entered := make(chan stream.VideoFrame, 16)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(f stream.VideoFrame) error {
		entered <- f
		if f.Timestamp == 1 {
			<-unblock
			return errVideoBudget
		}
		return nil
	}, func(error) { t.Error("recoverable budget failure terminated writer") })
	defer w.close()
	defer close(unblock)
	w.offer(stream.VideoFrame{Result: 3, Timestamp: 1})
	receiveFrame(t, entered)
	for i := 0; i < peerVideoQueueCapacity; i++ {
		w.offer(stream.VideoFrame{Result: 4, Timestamp: 2})
	}
	unblock <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		repairing := w.repairing
		w.mu.Unlock()
		if repairing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("budget failure did not recover")
		}
		time.Sleep(time.Millisecond)
	}
	if w.offer(stream.VideoFrame{Result: 4}) {
		t.Fatal("accepted damaged delta chain")
	}
	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 3}) {
		t.Fatal("recovery key rejected")
	}
	if f := receiveFrame(t, entered); f.Timestamp != 3 {
		t.Fatal("stale pending frame sent after budget failure")
	}
}
