package decompress

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"testing/iotest"
)

// xzBytes compresses data with the xz command, or skips the test without it.
func xzBytes(t testing.TB, data []byte, args ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz command not available")
	}
	cmd := exec.Command("xz", append([]string{"-c"}, args...)...)
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readAllXZ(t testing.TB, compressed []byte, wrap func(io.Reader) io.Reader) ([]byte, error) {
	t.Helper()
	x, err := NewXZReader(wrap(bytes.NewReader(compressed)))
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	return io.ReadAll(x)
}

// testdata/text.xz holds samples()["text"], written by "xz -6 --check=crc64".
func TestXZFixture(t *testing.T) {
	compressed, err := os.ReadFile("testdata/text.xz")
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAllXZ(t, compressed, iotest.OneByteReader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	want := sha256.Sum256(samples()["text"])
	if sum != want {
		t.Fatalf("fixture decodes to %s", hex.EncodeToString(sum[:8]))
	}
}

func TestXZMatchesInput(t *testing.T) {
	for name, data := range samples() {
		for _, args := range [][]string{
			{"-0"}, {"-6", "--check=crc32"}, {"-6", "--check=sha256"}, {"-6", "--check=none"},
			{"-6", "-T2", "--block-size=65536"},
		} {
			compressed := xzBytes(t, data, args...)
			for wrapName, wrap := range map[string]func(io.Reader) io.Reader{
				"whole": identity, "one-byte": iotest.OneByteReader,
			} {
				got, err := readAllXZ(t, compressed, wrap)
				if err != nil {
					t.Fatalf("%s %v %s: %v", name, args, wrapName, err)
				}
				if !bytes.Equal(got, data) {
					t.Fatalf("%s %v %s: output differs", name, args, wrapName)
				}
			}
		}
	}
}

func TestXZConcatenated(t *testing.T) {
	a, b := []byte("first stream\n"), bytes.Repeat([]byte("second stream\n"), 5000)
	stream := append(xzBytes(t, a), xzBytes(t, b)...)
	got, err := readAllXZ(t, stream, identity)
	if err != nil || !bytes.Equal(got, append(append([]byte{}, a...), b...)) {
		t.Fatalf("concatenated: %v", err)
	}
}

func TestXZErrors(t *testing.T) {
	good := xzBytes(t, samples()["text"], "-6", "--check=crc64")
	corrupt := append([]byte{}, good...)
	corrupt[len(corrupt)/2] ^= 0x55
	cases := []struct {
		name  string
		input []byte
		want  error
	}{
		{"truncated", good[:len(good)/2], io.ErrUnexpectedEOF},
		{"truncated footer", good[:len(good)-4], io.ErrUnexpectedEOF},
		{"corrupt", corrupt, ErrCorrupt},
		{"not xz", []byte("plain text, not xz at all"), ErrHeader},
		// Too short for a stream header: liblzma waits for the rest of one.
		{"short trailing garbage", append(append([]byte{}, good...), []byte("garbage!")...), io.ErrUnexpectedEOF},
		// After the first stream liblzma reports a bad header as a data error.
		{"trailing garbage", append(append([]byte{}, good...), bytes.Repeat([]byte("garbage!"), 8)...), ErrCorrupt},
	}
	for _, c := range cases {
		if _, err := readAllXZ(t, c.input, identity); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestXZMemoryLimit(t *testing.T) {
	compressed := xzBytes(t, []byte("needs a 64 MiB dictionary"), "-9")
	x, err := NewXZReaderLimit(bytes.NewReader(compressed), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if _, err := io.ReadAll(x); !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("got %v, want ErrMemoryLimit", err)
	}
	got, err := readAllXZ(t, compressed, identity)
	if err != nil || string(got) != "needs a 64 MiB dictionary" {
		t.Fatalf("default limit: %v", err)
	}
}

func TestXZClose(t *testing.T) {
	x, err := NewXZReader(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	x.Close()
	if err := x.Close(); err != nil {
		t.Fatal("second Close:", err)
	}
	if _, err := x.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after Close: %v", err)
	}
}

func BenchmarkXZ(b *testing.B) {
	data := samples()["mixed"]
	compressed := xzBytes(b, data, "-6")
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		x, _ := NewXZReader(bytes.NewReader(compressed))
		if _, err := io.Copy(io.Discard, x); err != nil {
			b.Fatal(err)
		}
		x.Close()
	}
}
