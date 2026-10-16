package decompress

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"math/rand"
	"testing"
	"testing/iotest"
)

func gzipBytes(t testing.TB, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func samples() map[string][]byte {
	rnd := rand.New(rand.NewSource(7))
	random := make([]byte, 300<<10)
	for i := range random {
		random[i] = byte(rnd.Intn(256))
	}
	text := bytes.Repeat([]byte("NanoKVM gzip and xz readers. "), 40000)
	mixed := append(append([]byte{}, random[:70000]...), text[:200000]...)
	return map[string][]byte{"empty": {}, "byte": {42}, "random": random, "text": text, "mixed": mixed}
}

func readAllGzip(t testing.TB, compressed []byte, wrap func(io.Reader) io.Reader) ([]byte, error) {
	t.Helper()
	z, err := NewGzipReader(wrap(bytes.NewReader(compressed)))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	return io.ReadAll(z)
}

func identity(r io.Reader) io.Reader { return r }

func TestGzipMatchesStdlib(t *testing.T) {
	for name, data := range samples() {
		compressed := gzipBytes(t, data)
		for wrapName, wrap := range map[string]func(io.Reader) io.Reader{
			"whole": identity, "one-byte": iotest.OneByteReader, "half": iotest.HalfReader,
		} {
			got, err := readAllGzip(t, compressed, wrap)
			if err != nil {
				t.Fatalf("%s/%s: %v", name, wrapName, err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("%s/%s: output differs", name, wrapName)
			}
		}
	}
}

func TestGzipSmallReadBuffers(t *testing.T) {
	data := samples()["mixed"]
	z, err := NewGzipReader(bytes.NewReader(gzipBytes(t, data)))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	got, err := io.ReadAll(iotest.OneByteReader(z))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("one-byte reads: %v, equal %v", err, bytes.Equal(got, data))
	}
}

func TestGzipMultistream(t *testing.T) {
	a, b := []byte("first member\n"), bytes.Repeat([]byte("second member\n"), 5000)
	stream := append(gzipBytes(t, a), gzipBytes(t, b)...)
	got, err := readAllGzip(t, stream, iotest.OneByteReader)
	if err != nil || !bytes.Equal(got, append(append([]byte{}, a...), b...)) {
		t.Fatalf("multistream: %v", err)
	}
}

func TestGzipErrors(t *testing.T) {
	data := samples()["text"]
	good := gzipBytes(t, data)
	badCRC := append([]byte{}, good...)
	badCRC[len(badCRC)-6] ^= 0xff // CRC-32 in the trailer
	badLength := append([]byte{}, good...)
	badLength[len(badLength)-2] ^= 0x01 // ISIZE in the trailer
	badMagic := append([]byte{}, good...)
	badMagic[0] = 0x1e
	cases := []struct {
		name  string
		input []byte
		want  error
	}{
		{"truncated body", good[:len(good)/2], io.ErrUnexpectedEOF},
		{"truncated trailer", good[:len(good)-3], io.ErrUnexpectedEOF},
		{"truncated header", good[:5], io.ErrUnexpectedEOF},
		{"crc", badCRC, ErrChecksum},
		{"length", badLength, ErrChecksum},
		{"magic", badMagic, ErrHeader},
		{"trailing garbage", append(append([]byte{}, good...), []byte("garbage!")...), ErrHeader},
		{"not gzip", []byte("plain text, not gzip"), ErrHeader},
	}
	for _, c := range cases {
		_, err := readAllGzip(t, c.input, identity)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		// compress/gzip rejects the same inputs.
		if r, err := gzip.NewReader(bytes.NewReader(c.input)); err == nil {
			if _, err := io.ReadAll(r); err == nil {
				t.Errorf("%s: compress/gzip accepts the input", c.name)
			}
		}
	}
}

func TestGzipSourceError(t *testing.T) {
	boom := errors.New("boom")
	compressed := gzipBytes(t, samples()["random"])
	r := io.MultiReader(bytes.NewReader(compressed[:1000]), iotest.ErrReader(boom))
	z, err := NewGzipReader(r)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if _, err := io.ReadAll(z); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the source error", err)
	}
}

func TestGzipClose(t *testing.T) {
	z, err := NewGzipReader(bytes.NewReader(gzipBytes(t, []byte("x"))))
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal("second Close:", err)
	}
	if _, err := z.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after Close: %v", err)
	}
}

// FuzzGzip checks that whenever compress/gzip decodes an input, this reader
// decodes it to the same bytes, and that it never accepts what compress/gzip
// rejects. One difference is intended: zlib rejects the reserved header flag
// bits, as RFC 1952 requires, while compress/gzip ignores them.
func FuzzGzip(f *testing.F) {
	for _, data := range samples() {
		if len(data) < 4096 {
			f.Add(gzipBytes(f, data))
		}
	}
	f.Add(gzipBytes(f, []byte("seed")))
	f.Add([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) == 0 {
			return // compress/gzip reports io.EOF from NewReader; this reader reads nothing.
		}
		var want []byte
		var wantErr error
		if r, err := gzip.NewReader(bytes.NewReader(input)); err != nil {
			wantErr = err
		} else {
			want, wantErr = io.ReadAll(r)
		}
		got, err := readAllGzip(t, input, identity)
		if len(input) > 3 && input[0] == 0x1f && input[1] == 0x8b && input[3]&0xe0 != 0 {
			if !errors.Is(err, ErrHeader) {
				t.Fatalf("reserved flag bits: got %v, want ErrHeader", err)
			}
			return
		}
		if (wantErr == nil) != (err == nil) {
			t.Fatalf("stdlib error %v, ours %v", wantErr, err)
		}
		if err == nil && !bytes.Equal(got, want) {
			t.Fatal("outputs differ")
		}
	})
}

func BenchmarkGzip(b *testing.B) {
	data := samples()["mixed"]
	compressed := gzipBytes(b, data)
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		z, _ := NewGzipReader(bytes.NewReader(compressed))
		if _, err := io.Copy(io.Discard, z); err != nil {
			b.Fatal(err)
		}
		z.Close()
	}
}

func BenchmarkStdlibGzip(b *testing.B) {
	data := samples()["mixed"]
	compressed := gzipBytes(b, data)
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		z, _ := gzip.NewReader(bytes.NewReader(compressed))
		if _, err := io.Copy(io.Discard, z); err != nil {
			b.Fatal(err)
		}
	}
}

// readMixed alternates small reads (through the buffer) and reads larger than
// outChunk (decoded in place).
func readMixed(r io.Reader) ([]byte, error) {
	var out []byte
	sizes := []int{1, 700, outChunk + 3, 4096, 2 * outChunk}
	for i := 0; ; i++ {
		buf := make([]byte, sizes[i%len(sizes)])
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

func TestMixedReadSizes(t *testing.T) {
	data := bytes.Repeat(samples()["mixed"], 4)
	z, err := NewGzipReader(bytes.NewReader(gzipBytes(t, data)))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if got, err := readMixed(z); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("gzip: %v, equal %v", err, bytes.Equal(got, data))
	}
	x, err := NewXZReader(bytes.NewReader(xzBytes(t, data, "-6")))
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if got, err := readMixed(x); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("xz: %v, equal %v", err, bytes.Equal(got, data))
	}
}
