package decompress

/*
#cgo LDFLAGS: -llzma
#include <stdlib.h>
#include <lzma.h>

// calloc gives the all-zero LZMA_STREAM_INIT state.
static lzma_stream *nkos_xz_new(uint64_t memlimit, lzma_ret *ret) {
	lzma_stream *s = calloc(1, sizeof(*s));
	if (s == NULL) {
		*ret = LZMA_MEM_ERROR;
		return NULL;
	}
	*ret = lzma_stream_decoder(s, memlimit, LZMA_CONCATENATED);
	if (*ret != LZMA_OK) {
		free(s);
		return NULL;
	}
	return s;
}

static lzma_ret nkos_xz_code(lzma_stream *s, int finish, uint8_t *in, size_t in_len,
		uint8_t *out, size_t out_len, size_t *in_used, size_t *out_used) {
	s->next_in = in;
	s->avail_in = in_len;
	s->next_out = out;
	s->avail_out = out_len;
	lzma_ret r = lzma_code(s, finish ? LZMA_FINISH : LZMA_RUN);
	*in_used = in_len - s->avail_in;
	*out_used = out_len - s->avail_out;
	s->next_in = NULL;
	s->avail_in = 0;
	s->next_out = NULL;
	s->avail_out = 0;
	return r;
}

static void nkos_xz_free(lzma_stream *s) {
	lzma_end(s);
	free(s);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"unsafe"
)

// DefaultXZMemoryLimit bounds the decoder's memory. "xz -9" needs 65 MiB; the
// device has 256 MiB of RAM.
const DefaultXZMemoryLimit = 128 << 20

// XZReader decompresses an .xz stream; concatenated streams are read as one.
// It is not safe for concurrent use.
type XZReader struct {
	src    *source
	stream *C.lzma_stream
	err    error
}

// NewXZReader returns a reader of the xz data in r with DefaultXZMemoryLimit.
func NewXZReader(r io.Reader) (*XZReader, error) {
	return NewXZReaderLimit(r, DefaultXZMemoryLimit)
}

// NewXZReaderLimit is NewXZReader with a decoder memory limit in bytes.
func NewXZReaderLimit(r io.Reader, memlimit uint64) (*XZReader, error) {
	var ret C.lzma_ret
	stream := C.nkos_xz_new(C.uint64_t(memlimit), &ret)
	if stream == nil {
		return nil, fmt.Errorf("decompress: cannot initialise liblzma (%d)", int(ret))
	}
	x := &XZReader{src: newSource(r), stream: stream}
	runtime.SetFinalizer(x, (*XZReader).Close)
	return x, nil
}

// Read implements io.Reader.
func (x *XZReader) Read(p []byte) (int, error) {
	if x.err != nil {
		return 0, x.err
	}
	if x.stream == nil {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	for {
		finish := x.src.exhausted()
		if finish && x.src.err != nil {
			x.err = x.src.err
			return 0, x.err
		}
		in := x.src.pending()
		out := p
		if len(out) > chunk {
			out = out[:chunk]
		}
		var inUsed, outUsed C.size_t
		var inPtr *C.uint8_t
		if len(in) > 0 {
			inPtr = (*C.uint8_t)(unsafe.Pointer(&in[0]))
		}
		var fin C.int
		if finish {
			fin = 1
		}
		r := C.nkos_xz_code(x.stream, fin, inPtr, C.size_t(len(in)),
			(*C.uint8_t)(unsafe.Pointer(&out[0])), C.size_t(len(out)), &inUsed, &outUsed)
		runtime.KeepAlive(in)
		runtime.KeepAlive(out)
		x.src.pos += int(inUsed)
		n := int(outUsed)
		switch r {
		case C.LZMA_OK:
		case C.LZMA_STREAM_END:
			x.err = io.EOF
			if n > 0 {
				return n, nil
			}
			return 0, io.EOF
		case C.LZMA_BUF_ERROR:
			// No progress although LZMA_FINISH was given: truncated input.
			if finish {
				x.err = io.ErrUnexpectedEOF
				return n, x.err
			}
		case C.LZMA_FORMAT_ERROR:
			x.err = ErrHeader
			return n, x.err
		case C.LZMA_DATA_ERROR:
			x.err = fmt.Errorf("%w: xz data error", ErrCorrupt)
			return n, x.err
		case C.LZMA_MEMLIMIT_ERROR:
			x.err = ErrMemoryLimit
			return n, x.err
		case C.LZMA_OPTIONS_ERROR:
			x.err = errors.New("decompress: unsupported xz options")
			return n, x.err
		case C.LZMA_MEM_ERROR:
			x.err = errors.New("decompress: liblzma out of memory")
			return n, x.err
		default:
			x.err = fmt.Errorf("decompress: liblzma error %d", int(r))
			return n, x.err
		}
		if n > 0 {
			return n, nil
		}
	}
}

// Close releases the liblzma state. It does not close the underlying reader
// and may be called more than once.
func (x *XZReader) Close() error {
	if x.stream != nil {
		C.nkos_xz_free(x.stream)
		x.stream = nil
		runtime.SetFinalizer(x, nil)
	}
	return nil
}
