package decompress

/*
#cgo LDFLAGS: -lz
#include <stdlib.h>
#include <string.h>
#include <zlib.h>

static z_stream *nkos_gzip_new(void) {
	z_stream *s = calloc(1, sizeof(*s));
	// 16 + 15: gzip framing only, as compress/gzip, with the largest window.
	if (s != NULL && inflateInit2(s, 16 + MAX_WBITS) != Z_OK) {
		free(s);
		s = NULL;
	}
	return s;
}

static int nkos_gzip_inflate(z_stream *s, unsigned char *in, unsigned in_len,
		unsigned char *out, unsigned out_len, unsigned *in_used, unsigned *out_used) {
	s->next_in = in;
	s->avail_in = in_len;
	s->next_out = out;
	s->avail_out = out_len;
	int r = inflate(s, Z_NO_FLUSH);
	*in_used = in_len - s->avail_in;
	*out_used = out_len - s->avail_out;
	s->next_in = NULL;
	s->avail_in = 0;
	s->next_out = NULL;
	s->avail_out = 0;
	return r;
}

// The kind of a Z_DATA_ERROR, from zlib's message: 1 header, 2 check value.
static int nkos_gzip_error_kind(z_stream *s) {
	const char *m = s->msg;
	if (m == NULL)
		return 0;
	if (strstr(m, "header") != NULL || strstr(m, "unknown compression method") != NULL)
		return 1;
	if (strstr(m, "incorrect data check") != NULL || strstr(m, "incorrect length check") != NULL)
		return 2;
	return 0;
}

static void nkos_gzip_free(z_stream *s) {
	inflateEnd(s);
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

// GzipReader decompresses a gzip stream. Like compress/gzip it reads
// concatenated members as one stream. It is not safe for concurrent use.
type GzipReader struct {
	src    *source
	stream *C.z_stream
	member bool // a member has started and not yet ended
	err    error
}

// NewGzipReader returns a reader of the data that r holds in gzip format.
// Errors in the data are reported by Read.
func NewGzipReader(r io.Reader) (*GzipReader, error) {
	stream := C.nkos_gzip_new()
	if stream == nil {
		return nil, errors.New("decompress: cannot initialise zlib")
	}
	z := &GzipReader{src: newSource(r), stream: stream}
	runtime.SetFinalizer(z, (*GzipReader).Close)
	return z, nil
}

// Read implements io.Reader.
func (z *GzipReader) Read(p []byte) (int, error) {
	if z.err != nil {
		return 0, z.err
	}
	if z.stream == nil {
		return 0, ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if !z.member && z.src.exhausted() {
			// Between members: a clean end, or nothing at all.
			if z.src.err != nil {
				z.err = z.src.err
			} else {
				z.err = io.EOF
			}
			return 0, z.err
		}
		z.src.fill()
		in := z.src.pending()
		out := p
		if len(out) > chunk {
			out = out[:chunk]
		}
		var inUsed, outUsed C.uint
		var inPtr *C.uchar
		if len(in) > 0 {
			inPtr = (*C.uchar)(unsafe.Pointer(&in[0]))
		}
		r := C.nkos_gzip_inflate(z.stream, inPtr, C.uint(len(in)),
			(*C.uchar)(unsafe.Pointer(&out[0])), C.uint(len(out)), &inUsed, &outUsed)
		runtime.KeepAlive(in)
		runtime.KeepAlive(out)
		z.src.pos += int(inUsed)
		n := int(outUsed)
		switch r {
		case C.Z_OK:
			z.member = true
		case C.Z_STREAM_END:
			z.member = false
			if C.inflateReset(z.stream) != C.Z_OK {
				z.err = errors.New("decompress: cannot reset zlib")
				return n, z.err
			}
		case C.Z_BUF_ERROR:
			// No progress possible: the input ended inside a member.
			if z.src.exhausted() {
				if z.src.err != nil {
					z.err = z.src.err
				} else {
					z.err = io.ErrUnexpectedEOF
				}
				return n, z.err
			}
		case C.Z_DATA_ERROR:
			switch C.nkos_gzip_error_kind(z.stream) {
			case 1:
				z.err = ErrHeader
			case 2:
				z.err = ErrChecksum
			default:
				z.err = fmt.Errorf("%w: %s", ErrCorrupt, C.GoString(z.stream.msg))
			}
			return n, z.err
		case C.Z_MEM_ERROR:
			z.err = errors.New("decompress: zlib out of memory")
			return n, z.err
		default:
			z.err = fmt.Errorf("decompress: zlib error %d", int(r))
			return n, z.err
		}
		if n > 0 {
			return n, nil
		}
	}
}

// Close releases the zlib state. It does not close the underlying reader
// and may be called more than once.
func (z *GzipReader) Close() error {
	if z.stream != nil {
		C.nkos_gzip_free(z.stream)
		z.stream = nil
		runtime.SetFinalizer(z, nil)
	}
	return nil
}
