package webrtc

import (
	"NanoKVM-Server/service/stream"
	"testing"
	"time"
)

func waitPeerWriterDiagnostic(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("peer writer diagnostic condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPeerWriterDiagnosticsDisabled(t *testing.T) {
	t.Setenv(peerWriterDiagnosticsEnv, "0")
	w := newPeerVideoWriter(func(stream.VideoFrame) error { return nil }, func(error) {
		t.Error("unexpected write failure")
	})
	if w.diagnostics != nil {
		t.Fatal("diagnostics allocated while disabled")
	}
	if !w.offer(stream.VideoFrame{Result: 3, Data: []byte{1, 2}}) {
		t.Fatal("keyframe rejected")
	}
	w.close()
	<-w.done
}

func TestPeerWriterDiagnosticsCountersAndUniqueIDs(t *testing.T) {
	t.Setenv(peerWriterDiagnosticsEnv, "1")
	written := make(chan stream.VideoFrame, 4)
	write := func(frame stream.VideoFrame) error {
		written <- frame
		return nil
	}
	first := newPeerVideoWriter(write, func(error) { t.Error("unexpected first write failure") })
	second := newPeerVideoWriter(write, func(error) { t.Error("unexpected second write failure") })
	if first.diagnostics == nil || second.diagnostics == nil {
		t.Fatal("diagnostics not enabled")
	}
	if first.diagnostics.id.Load() == 0 || first.diagnostics.id.Load() == second.diagnostics.id.Load() {
		t.Fatal("diagnostic writer IDs are not unique")
	}

	key := stream.VideoFrame{Result: 3, Data: []byte{1, 2, 3}}
	delta := stream.VideoFrame{Result: 4, Data: []byte{4, 5, 6, 7, 8}}
	if !first.offer(key) || !first.offer(delta) {
		t.Fatal("frame rejected")
	}
	<-written
	<-written
	d := first.diagnostics
	if d.offers.Load() != 2 || d.accepted.Load() != 2 || d.written.Load() != 2 {
		t.Fatalf("frame counters: offers=%d accepted=%d written=%d", d.offers.Load(), d.accepted.Load(), d.written.Load())
	}
	if d.sentBytes.Load() != 8 || d.maxFrameSize.Load() != 5 {
		t.Fatalf("byte counters: sent=%d max=%d", d.sentBytes.Load(), d.maxFrameSize.Load())
	}
	if d.writtenKeyframes.Load() != 1 || d.writtenDeltas.Load() != 1 {
		t.Fatalf("written frame types: key=%d delta=%d", d.writtenKeyframes.Load(), d.writtenDeltas.Load())
	}
	if d.offeredKeyframes.Load() != 1 || d.offeredDeltas.Load() != 1 {
		t.Fatalf("frame types: key=%d delta=%d", d.offeredKeyframes.Load(), d.offeredDeltas.Load())
	}
	if d.queueHighWater.Load() == 0 {
		t.Fatal("queue high water was not recorded")
	}
	first.close()
	second.close()
	<-first.done
	<-second.done
	if !d.logged.Load() {
		t.Fatal("close did not emit the diagnostic snapshot")
	}
}

func TestPeerWriterDiagnosticsQueueWait(t *testing.T) {
	t.Setenv(peerWriterDiagnosticsEnv, "1")
	entered := make(chan stream.VideoFrame, 2)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(frame stream.VideoFrame) error {
		entered <- frame
		<-unblock
		return nil
	}, func(error) { t.Error("unexpected write failure") })

	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 1, Data: []byte{1}}) {
		t.Fatal("keyframe rejected")
	}
	<-entered
	if !w.offer(stream.VideoFrame{Result: 4, Timestamp: 2, Data: []byte{2}}) {
		t.Fatal("delta rejected")
	}
	// Keep the first write in flight long enough to make the queued wait
	// observable without relying on the source frame timestamp.
	time.Sleep(2 * time.Millisecond)
	close(unblock)
	<-entered
	d := w.diagnostics
	waitPeerWriterDiagnostic(t, func() bool { return d.queueWaitCount.Load() >= 2 })
	if d.queueWaitNs.Load() == 0 || d.maxQueueWaitNs.Load() == 0 {
		t.Fatalf("queue wait was not recorded: count=%d total_ns=%d max_ns=%d", d.queueWaitCount.Load(), d.queueWaitNs.Load(), d.maxQueueWaitNs.Load())
	}
	if d.maxQueueWaitNs.Load() > d.queueWaitNs.Load() {
		t.Fatalf("queue wait max=%d exceeds total=%d", d.maxQueueWaitNs.Load(), d.queueWaitNs.Load())
	}
	w.close()
	<-w.done
}

func TestPeerWriterDiagnosticsOverflowAndRepairingDrop(t *testing.T) {
	useDefaultPeerWriterQueue(t)
	t.Setenv(peerWriterDiagnosticsEnv, "1")
	entered := make(chan stream.VideoFrame, 16)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(frame stream.VideoFrame) error {
		entered <- frame
		<-unblock
		return nil
	}, func(error) { t.Error("unexpected write failure") })
	key := stream.VideoFrame{Result: 3, Timestamp: 1, Data: []byte{1, 2, 3, 4}}
	if !w.offer(key) {
		t.Fatal("keyframe rejected")
	}
	<-entered
	delta := stream.VideoFrame{Result: 4, Timestamp: 2, Data: []byte{5, 6}}
	for i := 0; i < peerVideoQueueCapacity; i++ {
		if !w.offer(delta) {
			t.Fatal("delta rejected before queue overflow")
		}
	}
	if w.offer(delta) {
		t.Fatal("overflowing delta accepted")
	}
	if w.offer(stream.VideoFrame{Result: 3, Timestamp: 3, Data: []byte{7, 8, 9}}) == false {
		t.Fatal("repair keyframe rejected")
	}
	d := w.diagnostics
	if d.overflow.Load() != 1 || d.drainedFrames.Load() != peerVideoQueueCapacity {
		t.Fatalf("overflow counters: overflow=%d drained=%d", d.overflow.Load(), d.drainedFrames.Load())
	}
	if d.drainEvents.Load() != 1 || d.repairingDropped.Load() == 0 {
		t.Fatalf("recovery counters: drains=%d repairing_dropped=%d", d.drainEvents.Load(), d.repairingDropped.Load())
	}
	close(unblock)
	waitPeerWriterDiagnostic(t, func() bool { return d.written.Load() >= 2 })
	w.close()
	<-w.done
}

func TestPeerWriterDiagnosticsBudgetRecovery(t *testing.T) {
	t.Setenv(peerWriterDiagnosticsEnv, "1")
	budget := make(chan struct{})
	w := newPeerVideoWriter(func(frame stream.VideoFrame) error {
		if frame.Timestamp == 1 {
			close(budget)
			return errVideoBudget
		}
		return nil
	}, func(error) { t.Error("budget error terminated writer") })
	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 1, Data: []byte{1}}) {
		t.Fatal("initial keyframe rejected")
	}
	<-budget
	waitPeerWriterDiagnostic(t, func() bool {
		w.mu.Lock()
		repairing := w.repairing
		w.mu.Unlock()
		return repairing
	})
	if w.diagnostics.budgetErrors.Load() != 1 {
		t.Fatal("budget error was not counted separately")
	}
	if w.offer(stream.VideoFrame{Result: 4, Timestamp: 2, Data: []byte{2}}) {
		t.Fatal("delta accepted during budget repair")
	}
	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 3, Data: []byte{3, 4}}) {
		t.Fatal("repair keyframe rejected")
	}
	waitPeerWriterDiagnostic(t, func() bool { return w.diagnostics.written.Load() >= 1 })
	w.close()
	<-w.done
}
