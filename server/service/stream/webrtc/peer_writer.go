package webrtc

import (
	"errors"
	"sync"

	"NanoKVM-Server/service/stream"
)

// Buffer brief IDR transmission bursts without losing dependent frames.
// Eight pending frames cover ~267 ms at 30 FPS; sustained overload still
// discards the damaged chain and recovers at the next IDR.
const peerVideoQueueCapacity = 8

var errVideoBudget = errors.New("video PMTU cannot carry a frame")

// Independently implemented from the bounded-slot/recovery design in IronKVM
// (yuzi-co / Vadim), e324cdae and 488712da. Packetization stays per peer so PMTU
// changes never constrain another viewer. Pending frames remain in capture order.
type peerVideoWriter struct {
	mu        sync.Mutex
	frames    chan stream.VideoFrame
	done      chan struct{}
	closed    bool
	repairing bool
}

func newPeerVideoWriter(write func(stream.VideoFrame) error, failed func(error)) *peerVideoWriter {
	w := &peerVideoWriter{frames: make(chan stream.VideoFrame, peerVideoQueueCapacity), done: make(chan struct{}), repairing: true}
	go func() {
		defer close(w.done)
		for frame := range w.frames {
			w.mu.Lock()
			closed := w.closed
			w.mu.Unlock()
			if closed {
				return
			}
			if err := write(frame); err != nil {
				if errors.Is(err, errVideoBudget) {
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || (w.repairing && !frame.IsKeyframe()) {
		return false
	}
	select {
	case w.frames <- frame:
		w.repairing = false
		return true
	default:
		// Dropping a reference picture invalidates following delta pictures.
		// Clear stale pending work, then wait for an actually accepted IDR.
		w.drainLocked()
		w.repairing = true
		if frame.IsKeyframe() {
			w.frames <- frame
			w.repairing = false
			return true
		}
		return false
	}
}

func (w *peerVideoWriter) drainLocked() {
	for {
		select {
		case _, ok := <-w.frames:
			if !ok {
				return
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
}

func (w *peerVideoWriter) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}
