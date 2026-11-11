package remotemedia

import (
	"context"
	"errors"
	"sync"
	"time"
)

// fetchBlock fills target from offset. started is when the NBD read waiting
// for the data began, or when a prefetch was issued. ctx ends when a prefetch
// is discarded or the source closes.
type fetchBlock func(ctx context.Context, started time.Time, offset uint64, target []byte) error

// window is the bounded read cache of a network source. A random read fetches
// exactly the requested bytes. After two sequential transitions of at least
// 64 KiB it also keeps one prefetch of the following block of up to 1 MiB, so
// the network works while the USB side drains the current block.
//
// There are two buffers. The current one is lent to the NBD writer until the
// next read; only the other one can receive a prefetch. At most one fetch runs
// at a time: read joins the prefetch before fetching itself, because the HTTP
// source updates its pinned target in a fetch and the SMB reader protocol has
// neither request IDs nor cancellation.
type window struct {
	ctx     context.Context // source lifetime
	size    uint64
	fetch   fetchBlock
	mu      sync.Mutex // serializes read and close; a prefetch never takes it
	buffers [2][]byte  // the second is allocated with the first prefetch
	current int
	start   uint64
	valid   int
	ahead   *prefetch
	next    uint64
	streak  uint8
}

// A prefetch owns the other buffer until read has joined done. err is written
// before done is closed and read only after it.
type prefetch struct {
	offset uint64
	count  int
	err    error
	cancel context.CancelFunc
	done   chan struct{}
}

func newWindow(ctx context.Context, fetch fetchBlock) *window {
	w := &window{ctx: ctx, fetch: fetch, next: ^uint64(0)}
	w.buffers[0] = make([]byte, MaxRead)
	return w
}

func (w *window) read(offset uint64, length uint32) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	if length == 0 || length > MaxRead || offset > w.size || uint64(length) > w.size-offset {
		return nil, errors.New("Invalid image read bounds")
	}
	started := time.Now()
	if offset == w.next && length >= 64<<10 {
		w.streak = min(w.streak+1, 2)
	} else {
		w.streak = 0
	}
	end := offset + uint64(length)
	w.next = end
	windowEnd := w.start + uint64(w.valid)
	if offset >= w.start && end <= windowEnd {
		w.prefetchNext()
		return w.buffers[w.current][offset-w.start : end-w.start], nil
	}
	if p := w.ahead; p != nil && end <= p.offset+uint64(p.count) {
		inside := offset >= p.offset
		// NBD reads do not align with block ends. A read which begins in the
		// current block and ends in the next is assembled below.
		straddles := offset >= w.start && offset < windowEnd && windowEnd == p.offset
		if inside || straddles {
			// The prefetch has its own deadline, so this wait is bounded.
			<-p.done
			err := p.err
			if err == nil {
				err = w.ctx.Err()
			}
			if err != nil {
				w.discard()
				return nil, err
			}
		}
		if inside {
			w.discard()
			w.current ^= 1
			w.start, w.valid = p.offset, p.count
			w.prefetchNext()
			return w.buffers[w.current][offset-w.start : end-w.start], nil
		}
		if straddles {
			// The lease of the current buffer ended with this call: move its
			// tail to the front and append the head of the next block. That
			// block stays complete until a read falls wholly inside it.
			data := w.buffers[w.current][:length]
			n := copy(data, w.buffers[w.current][offset-w.start:w.valid])
			copy(data[n:], w.buffers[w.current^1])
			w.start, w.valid = offset, int(length)
			return data, nil
		}
	}
	w.discard()
	w.valid = 0
	data := w.buffers[w.current][:length]
	if err := w.fetch(w.ctx, started, offset, data); err != nil {
		return nil, err
	}
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	w.start, w.valid = offset, int(length)
	w.prefetchNext()
	return data, nil
}

// prefetchNext requests the block after the current one as soon as a stream
// is being served from it. Its error is kept for the read that needs the data.
func (w *window) prefetchNext() {
	offset := w.start + uint64(w.valid)
	if w.streak < 2 || w.ahead != nil || offset >= w.size {
		return
	}
	if w.buffers[1] == nil {
		w.buffers[1] = make([]byte, MaxRead)
	}
	ctx, cancel := context.WithCancel(w.ctx)
	p := &prefetch{offset: offset, count: int(min(uint64(MaxRead), w.size-offset)), cancel: cancel, done: make(chan struct{})}
	target := w.buffers[w.current^1][:p.count]
	w.ahead = p
	go func() {
		defer close(p.done)
		p.err = w.fetch(ctx, time.Now(), p.offset, target)
	}()
}

// discard cancels the prefetch and waits for it, so that its buffer is free
// and no request is left running. A source without cancellation finishes the
// request first; its error is dropped with the data.
func (w *window) discard() {
	if p := w.ahead; p != nil {
		p.cancel()
		<-p.done
		w.ahead = nil
	}
}

// close joins the prefetch. The owner cancels ctx first, which also makes a
// read holding the mutex return.
func (w *window) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.discard()
}
