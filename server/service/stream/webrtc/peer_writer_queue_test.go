package webrtc

import (
	"NanoKVM-Server/service/stream"
	"testing"
)

func TestPeerWriterQueueCapacityKnob(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  int
	}{
		{name: "default", value: "", want: 8},
		{name: "eight", value: "8", want: 8},
		{name: "thirty two", value: "32", want: 32},
		{name: "sixty four", value: "64", want: 64},
		{name: "invalid", value: "17", want: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(peerVideoQueueEnv, tc.value)
			t.Setenv(peerWriterDiagnosticsEnv, "0")
			w := newPeerVideoWriter(func(stream.VideoFrame) error { return nil }, func(error) {
				t.Error("unexpected write failure")
			})
			if got := cap(w.frames); got != tc.want {
				t.Fatalf("queue capacity=%d want=%d", got, tc.want)
			}
			w.close()
			<-w.done
		})
	}
}

func TestPeerWriterFrameBytesUsesStorageThenData(t *testing.T) {
	if got := peerWriterFrameBytes(stream.VideoFrame{Storage: make([]byte, 9), Data: make([]byte, 3)}); got != 9 {
		t.Fatalf("storage frame bytes=%d want 9", got)
	}
	if got := peerWriterFrameBytes(stream.VideoFrame{Data: make([]byte, 7)}); got != 7 {
		t.Fatalf("data frame bytes=%d want 7", got)
	}
}

func TestPeerWriterByteBudgetDrainsAndResumesAtIDR(t *testing.T) {
	t.Setenv(peerVideoQueueEnv, "8")
	t.Setenv(peerWriterDiagnosticsEnv, "1")
	entered := make(chan stream.VideoFrame, 8)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(frame stream.VideoFrame) error {
		entered <- frame
		<-unblock
		return nil
	}, func(error) { t.Error("unexpected write failure") })

	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 1, Data: []byte{1}}) {
		t.Fatal("initial keyframe rejected")
	}
	<-entered
	delta := stream.VideoFrame{Result: 4, Timestamp: 2, Storage: make([]byte, 700000)}
	if !w.offer(delta) || !w.offer(delta) {
		t.Fatal("frames below byte budget were rejected")
	}
	if w.offer(delta) {
		t.Fatal("frame over byte budget was accepted")
	}
	w.mu.Lock()
	queuedBytes := w.queuedBytes
	repairing := w.repairing
	w.mu.Unlock()
	if queuedBytes != 0 || !repairing {
		t.Fatalf("after byte overflow queued_bytes=%d repairing=%t", queuedBytes, repairing)
	}
	recovery := stream.VideoFrame{Result: 3, Timestamp: 3, Storage: make([]byte, 600000)}
	if !w.offer(recovery) {
		t.Fatal("recovery keyframe rejected")
	}
	w.mu.Lock()
	queuedBytes = w.queuedBytes
	w.mu.Unlock()
	if queuedBytes != len(recovery.Storage) {
		t.Fatalf("queued recovery bytes=%d want=%d", queuedBytes, len(recovery.Storage))
	}
	close(unblock)
	<-entered
	w.close()
	<-w.done
	d := w.diagnostics
	if d.overflow.Load() != 1 || d.drainedFrames.Load() != 2 {
		t.Fatalf("byte overflow counters overflow=%d drained=%d", d.overflow.Load(), d.drainedFrames.Load())
	}
	if d.drainedDeltas.Load() != 2 || d.queueByteHighWater.Load() < 1400000 {
		t.Fatalf("byte diagnostics drained_deltas=%d highwater=%d", d.drainedDeltas.Load(), d.queueByteHighWater.Load())
	}
}

func TestPeerWriterAllowsOneOversizedQueuedFrame(t *testing.T) {
	t.Setenv(peerVideoQueueEnv, "8")
	t.Setenv(peerWriterDiagnosticsEnv, "1")
	entered := make(chan stream.VideoFrame, 8)
	unblock := make(chan struct{})
	w := newPeerVideoWriter(func(frame stream.VideoFrame) error {
		entered <- frame
		<-unblock
		return nil
	}, func(error) { t.Error("unexpected write failure") })

	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 1, Data: []byte{1}}) {
		t.Fatal("initial keyframe rejected")
	}
	<-entered
	oversized := stream.VideoFrame{Result: 4, Timestamp: 2, Storage: make([]byte, peerVideoQueueBytes+1)}
	if !w.offer(oversized) {
		t.Fatal("single oversized frame rejected while queue was empty")
	}
	w.mu.Lock()
	queuedBytes := w.queuedBytes
	w.mu.Unlock()
	if queuedBytes != len(oversized.Storage) {
		t.Fatalf("oversized queued bytes=%d want=%d", queuedBytes, len(oversized.Storage))
	}
	if w.offer(stream.VideoFrame{Result: 4, Timestamp: 3, Data: []byte{2}}) {
		t.Fatal("second frame accepted behind oversized frame")
	}
	w.mu.Lock()
	queuedBytes = w.queuedBytes
	repairing := w.repairing
	w.mu.Unlock()
	if queuedBytes != 0 || !repairing {
		t.Fatalf("oversized drain queued_bytes=%d repairing=%t", queuedBytes, repairing)
	}
	if !w.offer(stream.VideoFrame{Result: 3, Timestamp: 4, Data: []byte{3}}) {
		t.Fatal("recovery keyframe rejected")
	}
	close(unblock)
	<-entered
	w.close()
	<-w.done
	if w.diagnostics.drainedFrames.Load() != 1 {
		t.Fatalf("oversized frame drain count=%d want 1", w.diagnostics.drainedFrames.Load())
	}
}
