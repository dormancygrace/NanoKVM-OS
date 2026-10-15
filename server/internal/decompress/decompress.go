// Package decompress reads gzip and xz streams with zlib-ng and liblzma.
//
// The image build links both libraries statically from the tools step
// (platform/build.sh): on the C906 zlib-ng inflates about three times as fast
// as compress/gzip, and liblzma about four times as fast as pure-Go xz.
//
// The C stream state lives in C memory. Go buffers reach C only as arguments
// of one call, and the C side clears its pointers to them before it returns,
// so no Go pointer is kept in C memory between calls.
package decompress

import (
	"errors"
	"io"
)

var (
	// ErrHeader reports data that does not start like the expected format.
	ErrHeader = errors.New("decompress: invalid header")
	// ErrChecksum reports a stream whose check value does not match its data.
	ErrChecksum = errors.New("decompress: checksum mismatch")
	// ErrCorrupt reports compressed data that cannot be decoded.
	ErrCorrupt = errors.New("decompress: corrupt data")
	// ErrMemoryLimit reports an xz stream that needs more memory than allowed.
	ErrMemoryLimit = errors.New("decompress: memory limit exceeded")
	// ErrClosed reports a read after Close.
	ErrClosed = errors.New("decompress: reader closed")
)

// Input is read in pieces of inChunk. Output is decoded into a buffer of
// outChunk per cgo call and copied out by Read: callers read in small pieces
// (tar in 512 bytes, io.Copy in 32 KiB). On the C906 reads of 8 KiB, one cgo
// call each, inflated 30 MB/s; through the buffer 40 MB/s. Reads of at least
// outChunk are decoded in place, saving the copy.
const (
	inChunk  = 256 << 10
	outChunk = 256 << 10
)

// output holds decoded data that Read has not returned yet.
type output struct {
	buf      []byte
	pos, end int
}

// take copies buffered data into p.
func (o *output) take(p []byte) int {
	n := copy(p, o.buf[o.pos:o.end])
	o.pos += n
	return n
}

func (o *output) empty() bool { return o.pos == o.end }

// source buffers the compressed input. Both decoders read it the same way.
type source struct {
	r     io.Reader
	buf   []byte
	pos   int
	end   int
	eof   bool
	err   error
	empty int // consecutive reads that returned nothing
}

func newSource(r io.Reader) *source { return &source{r: r, buf: make([]byte, inChunk)} }

// fill makes input available unless the reader is exhausted. A reader error
// other than io.EOF is kept and reported by the decoder.
func (s *source) fill() {
	if s.pos < s.end || s.eof || s.err != nil {
		return
	}
	n, err := s.r.Read(s.buf)
	s.pos, s.end = 0, n
	if errors.Is(err, io.EOF) {
		s.eof = true
	} else if err != nil {
		s.err = err
	} else if n == 0 {
		// As bufio: a reader that keeps returning nothing is broken.
		if s.empty++; s.empty >= 100 {
			s.err = io.ErrNoProgress
		}
	} else {
		s.empty = 0
	}
}

func (s *source) pending() []byte { return s.buf[s.pos:s.end] }

// exhausted reports that no input remains, reading ahead if needed.
func (s *source) exhausted() bool {
	s.fill()
	return s.pos == s.end && (s.eof || s.err != nil)
}
