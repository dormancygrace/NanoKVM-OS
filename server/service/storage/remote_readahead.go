package storage

import (
	"errors"
	"io"

	"NanoKVM-Server/remotemedia"
)

const (
	remotePrefetchBlock = remotemedia.MaxRead
	remotePrefetchSlots = 2
)

type remoteChunk struct {
	offset uint64
	data   []byte
	buffer []byte
	err    error
}

// Two buffers bound both memory and overfetch. The current chunk stays leased
// until the next NBD read, while the other can receive the following block.
type remoteReadAhead struct {
	cancel   chan struct{}
	done     chan struct{}
	free     chan []byte
	chunks   chan remoteChunk
	current  *remoteChunk
	position uint64
}

func (r *remoteReader) newReadAhead(offset uint64) *remoteReadAhead {
	offset = offset / remotePrefetchBlock * remotePrefetchBlock
	p := &remoteReadAhead{
		cancel: make(chan struct{}), done: make(chan struct{}),
		free:   make(chan []byte, remotePrefetchSlots),
		chunks: make(chan remoteChunk, remotePrefetchSlots), position: offset,
	}
	for i := 0; i < remotePrefetchSlots; i++ {
		p.free <- remoteAligned(remotePrefetchBlock)
	}
	go func() {
		defer close(p.done)
		defer close(p.chunks)
		for offset < r.session.size {
			var buffer []byte
			select {
			case <-p.cancel:
				return
			case <-r.session.closed:
				return
			case buffer = <-p.free:
			}
			// If cancellation and a free slot were both ready, do not start
			// another browser request just because select chose the slot.
			select {
			case <-p.cancel:
				return
			default:
			}
			length := min(uint64(remotePrefetchBlock), r.session.size-offset)
			data := buffer[:length]
			err := r.fetch(offset, data)
			select {
			case p.chunks <- remoteChunk{offset: offset, data: data, buffer: buffer, err: err}:
			case <-p.cancel:
				return
			case <-r.session.closed:
				return
			}
			if err != nil {
				return
			}
			offset += length
		}
	}()
	return p
}

// The reader owns stop and calls it once, after the last borrowed slice was
// consumed or after the NBD attachment is closed.
func (p *remoteReadAhead) stop() { close(p.cancel); <-p.done }

func (p *remoteReadAhead) advance() error {
	if p.current != nil {
		p.position = p.current.offset + uint64(len(p.current.data))
		p.free <- p.current.buffer
		p.current = nil
	}
	chunk, ok := <-p.chunks
	if !ok {
		return io.EOF
	}
	if chunk.err != nil {
		return chunk.err
	}
	p.current = &chunk
	return nil
}

func (p *remoteReadAhead) read(offset uint64, length uint32, output []byte) ([]byte, bool, error) {
	if p.current == nil {
		if offset < p.position {
			return nil, false, nil
		}
		if err := p.advance(); err != nil {
			return nil, false, err
		}
	}
	c := p.current
	if offset == c.offset+uint64(len(c.data)) {
		if err := p.advance(); err != nil {
			return nil, false, err
		}
		c = p.current
	}
	if offset < c.offset || offset >= c.offset+uint64(len(c.data)) {
		return nil, false, nil
	}
	start := int(offset - c.offset)
	if start+int(length) <= len(c.data) {
		return c.data[start : start+int(length)], true, nil
	}
	copied := copy(output, c.data[start:])
	for copied < int(length) {
		if err := p.advance(); err != nil {
			return nil, false, err
		}
		if p.current.offset != offset+uint64(copied) {
			return nil, false, errors.New("noncontiguous browser prefetch")
		}
		copied += copy(output[copied:int(length)], p.current.data)
	}
	return output[:length], true, nil
}
