package download

import (
	"bytes"
	"crypto/sha256"
	"errors"
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
