package sg2002aes

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"syscall"
	"testing"
)

// fakeEngine hashes like the module: whole blocks, at most shaChunk bytes,
// big-endian state. failAt makes call number failAt return err instead.
type fakeEngine struct {
	calls, bytes int
	failAt       int
	err          error
}

func (f *fakeEngine) engine() *shaEngine {
	return &shaEngine{run: func(state *[32]byte, blocks []byte) error {
		f.calls++
		if f.calls == f.failAt {
			return f.err
		}
		if len(blocks) == 0 || len(blocks)%64 != 0 || len(blocks) > shaChunk {
			return errors.New("bad request length")
		}
		var h [8]uint32
		for i := range h {
			h[i] = binary.BigEndian.Uint32(state[i*4:])
		}
		blockGeneric(&h, blocks)
		for i, v := range h {
			binary.BigEndian.PutUint32(state[i*4:], v)
		}
		f.bytes += len(blocks)
		return nil
	}}
}

var shaLengths = []int{0, 1, 55, 56, 63, 64, 65, 119, 120, 4095, 4096, 4097, 8191, 8192, 8193, 65537, 200003}

func TestSHA256MatchesCryptoSHA256(t *testing.T) {
	data := make([]byte, 200003+7)
	rand.Read(data)
	for _, n := range shaLengths {
		for _, write := range []int{1, 63, 4096, 4097, 1 << 20} {
			for _, off := range []int{0, 3} {
				f := &fakeEngine{}
				d := newSHA256(f.engine())
				p := data[off : off+n]
				for q := p; len(q) > 0; {
					c := min(write, len(q))
					d.Write(q[:c])
					q = q[c:]
				}
				want := sha256.Sum256(p)
				if got := d.Sum(nil); !bytes.Equal(got, want[:]) {
					t.Fatalf("%d bytes in %d-byte writes at offset %d: wrong digest", n, write, off)
				}
				if n <= shaChunk && f.calls != 0 {
					t.Fatalf("%d bytes reached the engine", n)
				}
				if n > shaChunk && f.bytes < n-shaChunk {
					t.Fatalf("%d bytes: only %d hashed on the engine", n, f.bytes)
				}
			}
		}
	}
}

func TestSHA256SumIsRepeatableAndWritable(t *testing.T) {
	data := make([]byte, 10000)
	rand.Read(data)
	d := newSHA256((&fakeEngine{}).engine())
	d.Write(data[:9000])
	first := d.Sum(nil)
	if !bytes.Equal(d.Sum(nil), first) {
		t.Fatal("second Sum differs")
	}
	if want := sha256.Sum256(data[:9000]); !bytes.Equal(first, want[:]) {
		t.Fatal("Sum after 9000 bytes")
	}
	d.Write(data[9000:])
	if want := sha256.Sum256(data); !bytes.Equal(d.Sum(nil), want[:]) {
		t.Fatal("Write after Sum")
	}
	d.Reset()
	d.Write(data[:5000])
	if want := sha256.Sum256(data[:5000]); !bytes.Equal(d.Sum([]byte("x"))[1:], want[:]) {
		t.Fatal("after Reset")
	}
}

func TestSHA256EngineFailureContinuesInSoftware(t *testing.T) {
	data := make([]byte, 50000)
	rand.Read(data)
	want := sha256.Sum256(data)
	f := &fakeEngine{failAt: 3, err: syscall.EIO}
	e := f.engine()
	d := newSHA256(e)
	d.Write(data)
	if !bytes.Equal(d.Sum(nil), want[:]) {
		t.Fatal("wrong digest after an engine error")
	}
	if !e.disabled.Load() || f.calls != 3 {
		t.Fatalf("engine not disabled after an error (calls %d)", f.calls)
	}
	d2 := newSHA256(e)
	d2.Write(data)
	if !bytes.Equal(d2.Sum(nil), want[:]) || f.calls != 3 {
		t.Fatal("a disabled engine was used again")
	}
}

func TestSHA256InterruptedRequestKeepsTheEngine(t *testing.T) {
	data := make([]byte, 50000)
	rand.Read(data)
	f := &fakeEngine{failAt: 2, err: syscall.EINTR}
	e := f.engine()
	d := newSHA256(e)
	d.Write(data)
	if want := sha256.Sum256(data); !bytes.Equal(d.Sum(nil), want[:]) {
		t.Fatal("wrong digest after EINTR")
	}
	if e.disabled.Load() || f.calls < 5 {
		t.Fatalf("EINTR disabled the engine (calls %d)", f.calls)
	}
}

func TestNewSHA256WithoutDeviceIsCryptoSHA256(t *testing.T) {
	if openSHAEngine() != nil {
		t.Skip("device present")
	}
	if want := sha256.Sum256([]byte("abc")); !bytes.Equal(sha256Of(NewSHA256(), []byte("abc")), want[:]) {
		t.Fatal("wrong digest")
	}
}

func sha256Of(h interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}, p []byte) []byte {
	h.Write(p)
	return h.Sum(nil)
}

// On the device: the real engine against crypto/sha256.
func TestNewSHA256OnDevice(t *testing.T) {
	e := openSHAEngine()
	if e == nil {
		t.Skip("no SG2002 engine")
	}
	data := make([]byte, 16<<20+3)
	rand.Read(data)
	lengths := append([]int{4096, 4097, 4096 + 64, 3 * 4096, 1 << 20, 16 << 20}, shaLengths...)
	for _, n := range lengths {
		for _, off := range []int{0, 3} {
			d := newSHA256(e)
			d.Write(data[off : off+n])
			want := sha256.Sum256(data[off : off+n])
			if got := d.Sum(nil); !bytes.Equal(got, want[:]) {
				t.Fatalf("%d bytes at offset %d: wrong digest", n, off)
			}
		}
	}
	if e.disabled.Load() {
		t.Fatal("the engine failed during the test")
	}
}
