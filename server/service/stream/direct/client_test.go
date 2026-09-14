package direct

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMarkDiscontinuityWaitsForNextKeyframe(t *testing.T) {
	queue := newFrameQueue(defaultQueueFrames, defaultQueueBytes)
	queue.enableFlowControl(1)

	firstKey := newOutboundFrame(true, 10, nil, []byte("key"))
	if !queue.offer(firstKey) {
		t.Fatal("initial keyframe was rejected")
	}
	if got := queue.popForWrite(); got != firstKey {
		t.Fatalf("popped frame = %p, want %p", got, firstKey)
	}
	if !queue.canAdvanceStream() {
		t.Fatal("full ACK window must leave room for bounded pending capture")
	}

	queue.markDiscontinuity()
	queue.acknowledge(firstKey.timestamp)
	if !queue.canAdvanceStream() {
		t.Fatal("acknowledged discontinuity did not let the stream seek a keyframe")
	}
	if queue.offer(newOutboundFrame(false, 20, nil, []byte("delta"))) {
		t.Fatal("delta frame after a discontinuity was accepted")
	}

	nextKey := newOutboundFrame(true, 30, nil, []byte("key"))
	if !queue.offer(nextKey) {
		t.Fatal("recovery keyframe was rejected")
	}
	if got := queue.popForWrite(); got != nextKey {
		t.Fatalf("popped recovery frame = %p, want %p", got, nextKey)
	}
}

func TestNewOutboundFrameReusesHeadroom(t *testing.T) {
	storage := make([]byte, 13)
	data := storage[9:]
	copy(data, []byte{1, 2, 3, 4})

	frame := newOutboundFrame(true, 42, storage, data)

	if &frame.payload[0] != &storage[0] {
		t.Fatal("payload did not reuse capture storage")
	}
	if frame.payload[0] != 1 {
		t.Fatalf("keyframe marker = %d, want 1", frame.payload[0])
	}
	if timestamp := binary.LittleEndian.Uint64(frame.payload[1:9]); timestamp != 42 {
		t.Fatalf("timestamp = %d, want 42", timestamp)
	}
	if !bytes.Equal(frame.payload[9:], []byte{1, 2, 3, 4}) {
		t.Fatalf("frame data = %v", frame.payload[9:])
	}
}

func TestNewOutboundDeltaClearsReusedKeyframeMarker(t *testing.T) {
	storage := make([]byte, 13)
	storage[0] = 1
	data := storage[9:]
	copy(data, []byte{1, 2, 3, 4})

	frame := newOutboundFrame(false, 43, storage, data)

	if frame.payload[0] != 0 {
		t.Fatalf("delta-frame marker = %d, want 0", frame.payload[0])
	}
}

func TestNewOutboundFrameFallsBackWithoutHeadroom(t *testing.T) {
	data := []byte{5, 6, 7}
	frame := newOutboundFrame(false, 7, nil, data)

	if frame.payload[0] != 0 {
		t.Fatalf("keyframe marker = %d, want 0", frame.payload[0])
	}
	if timestamp := binary.LittleEndian.Uint64(frame.payload[1:9]); timestamp != 7 {
		t.Fatalf("timestamp = %d, want 7", timestamp)
	}
	if !bytes.Equal(frame.payload[9:], data) {
		t.Fatalf("frame data = %v, want %v", frame.payload[9:], data)
	}
}

func TestNewOutboundFrameFallsBackForUnrelatedStorage(t *testing.T) {
	storage := make([]byte, 12)
	data := []byte{8, 9, 10}
	frame := newOutboundFrame(false, 11, storage, data)

	if &frame.payload[0] == &storage[0] {
		t.Fatal("payload reused storage that does not contain frame data")
	}
	if !bytes.Equal(frame.payload[9:], data) {
		t.Fatalf("frame data = %v, want %v", frame.payload[9:], data)
	}
}

func TestAckStallPreservesQueuedPredictionChain(t *testing.T) {
	q := newFrameQueue(2, 64)
	q.enableFlowControl(1)
	key := newOutboundFrame(true, 10, nil, []byte("key"))
	delta := newOutboundFrame(false, 20, nil, []byte("delta"))
	q.offer(key)
	q.popForWrite()
	if !q.offer(delta) {
		t.Fatal("brief ACK stall discarded delta")
	}
	if got := q.popForWrite(); got != nil {
		t.Fatal("writer exceeded ACK window")
	}
	select {
	case <-q.wake:
	default:
	}
	q.acknowledge(10)
	select {
	case <-q.wake:
	default:
		t.Fatal("ACK did not wake writer")
	}
	if got := q.popForWrite(); got != delta {
		t.Fatal("prediction chain not preserved")
	}
}

func TestAckStallStillBoundsPendingFramesAndBytes(t *testing.T) {
	for _, limits := range []struct{ frames, bytes int }{{1, 1024}, {8, 16}} {
		q := newFrameQueue(limits.frames, limits.bytes)
		q.enableFlowControl(1)
		q.offer(newOutboundFrame(true, 10, nil, []byte("k")))
		q.popForWrite()
		if !q.offer(newOutboundFrame(false, 20, nil, []byte("d"))) {
			t.Fatal("first delta rejected")
		}
		if q.offer(newOutboundFrame(false, 30, nil, []byte("d"))) {
			t.Fatal("queue limit exceeded")
		}
		q.acknowledge(10)
		if q.offer(newOutboundFrame(false, 40, nil, []byte("d"))) {
			t.Fatal("broken chain accepted")
		}
		key := newOutboundFrame(true, 50, nil, []byte("k"))
		if !q.offer(key) || q.popForWrite() != key {
			t.Fatal("keyframe recovery failed")
		}
	}
}
