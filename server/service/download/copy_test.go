package download

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
)

func TestCopyImageChecksum(t *testing.T) {
	payload := bytes.Repeat([]byte("image-data"), 10000)
	sum := sha256.Sum256(payload)
	for _, tc := range []struct {
		name string
		sum  []byte
		want error
	}{
		{"optional", nil, nil},
		{"correct", sum[:], nil},
		{"mismatch", make([]byte, sha256.Size), errSHA256Mismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dst bytes.Buffer
			n, err := copyImage(&dst, bytes.NewReader(payload), tc.sum)
			if n != int64(len(payload)) || !errors.Is(err, tc.want) || !bytes.Equal(dst.Bytes(), payload) {
				t.Fatalf("copy returned %d, %v; output length %d", n, err, dst.Len())
			}
		})
	}
}

type failingImageWriter struct{}

func (failingImageWriter) Write(p []byte) (int, error) { return 3, io.ErrClosedPipe }

func TestCopyImagePreservesWriteError(t *testing.T) {
	for _, expected := range [][]byte{nil, make([]byte, sha256.Size)} {
		n, err := copyImage(failingImageWriter{}, bytes.NewReader([]byte("partial image")), expected)
		if n != 3 || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("copy returned %d, %v; want partial write error", n, err)
		}
	}
}
