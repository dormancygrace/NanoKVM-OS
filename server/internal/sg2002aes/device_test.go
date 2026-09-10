package sg2002aes

import (
	"bytes"
	"errors"
	"testing"
)

func TestFailedIOCTLPreservesInPlaceInputAndDisablesDevice(t *testing.T) {
	calls, closes, warnings := 0, 0, 0
	fault := errors.New("injected ioctl failure after buffer mutation")
	d := &Device{
		submit:  func(r *request) error { calls++; clear(r.Data[:]); return fault },
		closeFD: func() error { closes++; return nil },
		onError: func(err error) {
			warnings++
			if err != fault {
				t.Fatal(err)
			}
		},
	}
	plain := bytes.Repeat([]byte{0xa5}, 1188)
	before := bytes.Clone(plain)
	key, iv := bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)
	if d.TryXORKeyStream(key, iv, plain, plain) {
		t.Fatal("failed request accepted")
	}
	if !bytes.Equal(plain, before) {
		t.Fatal("software fallback input corrupted")
	}
	if !bytes.Equal(key, bytes.Repeat([]byte{1}, 16)) || !bytes.Equal(iv, bytes.Repeat([]byte{2}, 16)) {
		t.Fatal("key/IV changed")
	}
	if d.TryXORKeyStream(key, iv, plain, plain) {
		t.Fatal("disabled device accepted request")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || closes != 1 || warnings != 1 {
		t.Fatalf("calls=%d closes=%d warnings=%d", calls, closes, warnings)
	}
	if d.req.Key != [16]byte{} || d.req.IV != [16]byte{} || !bytes.Equal(d.req.Data[:len(plain)], make([]byte, len(plain))) {
		t.Fatal("request material retained")
	}
}

func TestInvalidCompletionPreservesDestination(t *testing.T) {
	d := &Device{submit: func(r *request) error { r.Status = 2; return nil }}
	src := bytes.Repeat([]byte{0x5a}, 512)
	dst := bytes.Repeat([]byte{0xa5}, 512)
	if d.TryXORKeyStream(make([]byte, 16), make([]byte, 16), dst, src) {
		t.Fatal("invalid completion accepted")
	}
	if !bytes.Equal(dst, bytes.Repeat([]byte{0xa5}, 512)) || !d.disabled {
		t.Fatal("invalid completion damaged output or failed to disable")
	}
}
