package webrtc

import (
	"NanoKVM-Server/service/stream"
	"errors"
	"sync"
	"time"
)

// Buffer brief IDR transmission bursts without losing dependent frames.
// The default eight pending frames cover ~267 ms at 30 FPS; the opt-in
// experiment can extend this to 32 or 64 while retaining recovery semantics.
var errVideoBudget = errors.New("video PMTU cannot carry a frame")

// Independently implemented from the bounded-slot/recovery design in IronKVM
// (yuzi-co / Vadim), e324cdae and 488712da. Packetization stays per peer so PMTU
// changes never constrain another viewer. Pending frames remain in capture order.
type queuedVideoFrame struct {
	frame    stream.VideoFrame
	queuedAt time.Time
}

type peerVideoWriter struct {
	mu          sync.Mutex
	frames      chan queuedVideoFrame
	done        chan struct{}
	closed      bool
	repairing   bool
	queuedBytes int
	diagnostics *peerWriterDiagnostics
}

func newPeerVideoWriter(write func(stream.VideoFrame) error, failed func(error)) *peerVideoWriter {
	w := &peerVideoWriter{
		frames:      make(chan queuedVideoFrame, peerWriterQueueCapacity()),
		done:        make(chan struct{}),
		repairing:   true,
		diagnostics: newPeerWriterDiagnostics(),
	}
	go func() {
		defer close(w.done)
		for queued := range w.frames {
			frame := queued.frame
			if w.diagnostics != nil {
				// The receive above is the dequeue point; record before the
				// accounting lock so mutex contention is not included.
				w.diagnostics.recordQueueWait(time.Since(queued.queuedAt))
			}
			w.mu.Lock()
			w.removeQueuedBytesLocked(frame)
			closed := w.closed
			w.mu.Unlock()
			if closed {
				if w.diagnostics != nil {
					w.diagnostics.recordDrop(frame, peerWriterDropClosed)
				}
				return
			}

			var err error
			if w.diagnostics == nil {
				err = write(frame)
			} else {
				start := time.Now()
				err = write(frame)
				w.diagnostics.recordWrite(frame, time.Since(start), err)
			}
			if err != nil {
				if errors.Is(err, errVideoBudget) {
					if w.diagnostics != nil {
						w.diagnostics.recordBudgetError()
					}
					w.mu.Lock()
					w.drainLocked()
					w.repairing = true
					w.mu.Unlock()
					continue
				}
				failed(err)
				return
			}
		}
	}()
	return w
}

func (w *peerVideoWriter) offer(frame stream.VideoFrame) bool {
	if w.diagnostics != nil {
		w.diagnostics.recordOffer(frame)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		if w.diagnostics != nil {
			w.diagnostics.recordDrop(frame, peerWriterDropClosed)
		}
		return false
	}
	if w.repairing && !frame.IsKeyframe() {
		if w.diagnostics != nil {
			w.diagnostics.recordDrop(frame, peerWriterDropRepairing)
		}
		return false
	}
	if w.tryEnqueueLocked(frame) {
		w.repairing = false
		return true
	}

	// Dropping a reference picture invalidates following delta pictures.
	// Clear stale pending work, then wait for an actually accepted IDR.
	if w.diagnostics != nil {
		w.diagnostics.recordOverflow()
	}
	w.drainLocked()
	w.repairing = true
	if frame.IsKeyframe() {
		// The queue is empty after drainLocked. This also permits one native
		// frame larger than the byte budget, but never a second queued frame.
		if w.tryEnqueueLocked(frame) {
			w.repairing = false
			return true
		}
	}
	if w.diagnostics != nil {
		w.diagnostics.recordDrop(frame, peerWriterDropRepairing)
	}
	return false
}

func (w *peerVideoWriter) tryEnqueueLocked(frame stream.VideoFrame) bool {
	bytes := peerWriterFrameBytes(frame)
	if bytes > peerVideoSingleFrameBytes {
		return false
	}
	// A single native frame may exceed the fixed budget. Permit it only when
	// no frame is queued; a following frame must drain it before proceeding.
	// The consumer receives before taking mu, so queuedBytes can briefly include
	// one in-flight frame while len(frames) is zero. That transient is outside
	// the pending queue bound; the consumer removes it under mu exactly once.
	if len(w.frames) != 0 && (bytes > peerVideoQueueBytes || w.queuedBytes+bytes > peerVideoQueueBytes) {
		return false
	}
	queued := queuedVideoFrame{frame: frame}
	if w.diagnostics != nil {
		queued.queuedAt = time.Now()
	}
	select {
	case w.frames <- queued:
		w.queuedBytes += bytes
		if w.diagnostics != nil {
			w.diagnostics.recordAccepted(len(w.frames), w.queuedBytes)
		}
		return true
	default:
		return false
	}
}

func (w *peerVideoWriter) removeQueuedBytesLocked(frame stream.VideoFrame) {
	w.queuedBytes -= peerWriterFrameBytes(frame)
}

func (w *peerVideoWriter) drainLocked() {
	drained := false
	for {
		select {
		case queued, ok := <-w.frames:
			if !ok {
				return
			}
			frame := queued.frame
			w.removeQueuedBytesLocked(frame)
			if w.diagnostics != nil {
				if !drained {
					w.diagnostics.recordDrainStart()
					drained = true
				}
				w.diagnostics.recordDrained(frame)
			}
		default:
			return
		}
	}
}

// Closing never waits for an in-flight network write. PeerConnection.Close
// releases network resources on terminal disconnect; ICE reconnect can create
// another slot while Client.videoWriteMutex preserves per-peer RTP ownership.
func (w *peerVideoWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	w.drainLocked()
	close(w.frames)
	if w.diagnostics != nil {
		w.diagnostics.logClose()
	}
}

func (w *peerVideoWriter) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}
