package storage

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"
	"unsafe"

	"NanoKVM-Server/remotemedia"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	remoteSequentialMin = 64 << 10
	remoteReadTimeout   = 15 * time.Second
)

type remoteReadRequest struct {
	id     uint32
	target []byte
}

type remoteReadResponse struct {
	err error
}

// remoteReader has one NBD consumer and one WebSocket receiver. The consumer
// borrows each returned slice until its next read; Serve completes the NBD
// socket write before calling read again. Only the prefetch producer can fetch
// while a window exists, and a seek joins it before issuing another request.
type remoteReader struct {
	session         *remoteSession
	mu              sync.Mutex // serializes read and close, never held by the producer
	pipe            *remoteReadAhead
	prefetchBuffers [remotePrefetchSlots][]byte
	readTimeout     time.Duration // total NBD read budget, including draining a seek
	output          []byte
	requests        chan remoteReadRequest
	responses       chan remoteReadResponse
	done            chan struct{}
	readID          uint32 // owned by fetch; never more than one fetch is active
	next            uint64
	streak          uint8
}

func remoteAligned(n int) []byte {
	b := make([]byte, n+63)
	offset := int((-uintptr(unsafe.Pointer(&b[0]))) & 63)
	return b[offset : offset+n : offset+n]
}

func newRemoteReader(s *remoteSession) *remoteReader {
	r := &remoteReader{
		session: s, output: remoteAligned(remotemedia.MaxRead), readTimeout: remoteReadTimeout,
		requests:  make(chan remoteReadRequest, 1),
		responses: make(chan remoteReadResponse, 1), done: make(chan struct{}),
		next: ^uint64(0),
	}
	go r.receive()
	return r
}

func (r *remoteReader) close() {
	r.session.close() // unblock a receive/fetch before waiting for the producer
	r.mu.Lock()
	if r.pipe != nil {
		r.pipe.stop()
		r.pipe = nil
	}
	r.mu.Unlock()
	<-r.done
}

// Keep receiving even while the host is idle, so a browser close or the
// authentication watcher's socket close still tears down the NBD attachment.
func (r *remoteReader) receive() {
	defer close(r.done)
	defer r.session.close()
	for {
		kind, message, err := r.session.ws.NextReader()
		if err != nil || kind != websocket.BinaryMessage {
			return
		}
		var request remoteReadRequest
		select {
		case request = <-r.requests:
		default:
			return // unsolicited data must never overwrite a borrowed NBD slice
		}
		var header [4]byte
		_, err = io.ReadFull(message, header[:])
		if err == nil && binary.BigEndian.Uint32(header[:]) != request.id {
			err = errors.New("wrong browser response ID")
		}
		if err == nil {
			_, err = io.ReadFull(message, request.target)
		}
		if err == nil {
			var extra [1]byte
			_, end := io.ReadFull(message, extra[:])
			if end != io.EOF {
				err = errors.New("oversized browser response")
			}
		}
		select {
		case r.responses <- remoteReadResponse{err: err}:
		case <-r.session.closed:
			return
		}
		if err != nil {
			return
		}
	}
}

func (r *remoteReader) fetch(offset uint64, target []byte) error {
	s := r.session
	r.readID++
	request := remoteReadRequest{id: r.readID, target: target}
	select {
	case r.requests <- request:
	case <-s.closed:
		return errors.New("browser disconnected")
	}
	if err := s.send(gin.H{"type": "read", "id": request.id, "offset": offset, "length": len(target)}); err != nil {
		s.close()
		return err
	}
	timer := time.NewTimer(remoteReadTimeout)
	defer timer.Stop()
	select {
	case <-s.closed:
		return errors.New("browser disconnected")
	case <-timer.C:
		s.close()
		return errors.New("browser read timed out")
	case response := <-r.responses:
		if response.err != nil {
			s.close()
			return response.err
		}
		s.readBytes.Add(uint64(len(target)))
		return nil
	}
}

func (r *remoteReader) read(offset uint64, length uint32) (data []byte, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.session.closed:
		return nil, errors.New("browser disconnected")
	default:
	}
	if length == 0 || length > remotemedia.MaxRead || offset > r.session.size || uint64(length) > r.session.size-offset {
		return nil, errors.New("invalid read bounds")
	}
	// One deadline covers a queued prefetch, draining it on a seek, sending
	// the replacement request and receiving its reply. Closing is necessary:
	// this protocol cannot cancel a response without losing frame alignment.
	expired := make(chan struct{})
	timer := time.AfterFunc(r.readTimeout, func() {
		r.session.close()
		close(expired)
	})
	defer func() {
		if !timer.Stop() {
			<-expired // join an expired callback before returning to NBD
			data, err = nil, errors.New("browser read timed out")
		}
	}()
	if offset == r.next && length >= remoteSequentialMin {
		if r.streak < 2 {
			r.streak++
		}
	} else {
		r.streak = 0
	}
	r.next = offset + uint64(length)
	if r.pipe == nil {
		// Two consecutive transitions between substantial reads establish a
		// stream. Until then reuse the output buffer for exact-length reads.
		if r.streak < 2 {
			return r.readDirect(offset, length)
		}
		r.pipe = r.newReadAhead(offset)
	}
	data, ok, err := r.pipe.read(offset, length, r.output)
	if err != nil || ok {
		return data, err
	}
	// Drain the one outstanding WebSocket response on a seek. The browser
	// protocol has no cancellation message, so abandoning it would lose framing.
	r.pipe.stop()
	r.pipe = nil
	r.streak = 0
	return r.readDirect(offset, length)
}

func (r *remoteReader) readDirect(offset uint64, length uint32) ([]byte, error) {
	data := r.output[:length]
	if err := r.fetch(offset, data); err != nil {
		return nil, err
	}
	return data, nil
}
