package annexb

import (
	"bytes"
	"math/rand"
	"testing"
)

// coded returns size bytes shaped like the stand's coded video: a zero byte
// in about 170, a zero pair in about 2200, each pair followed by the
// emulation prevention byte 03, and no start code.
func coded(r *rand.Rand, size int) []byte {
	b := make([]byte, 0, size+2)
	prevZero := false
	for len(b) < size {
		p := 1.0 / 170
		if prevZero {
			p = 170.0 / 2200
		}
		switch {
		case r.Float64() >= p:
			b = append(b, byte(1+r.Intn(255)))
			prevZero = false
		case prevZero:
			b = append(b, 0, 3)
			prevZero = false
		default:
			b = append(b, 0)
			prevZero = true
		}
	}
	return b[:size]
}

// check compares Index and, where it runs, the vector search with
// bytes.Index on b.
func check(t *testing.T, b []byte) {
	t.Helper()
	want := bytes.Index(b, startCode)
	if got := Index(b); got != want {
		t.Fatalf("Index(% x) = %d, want %d", b, got, want)
	}
	if vector() {
		if got := indexStartCode(b); got != want {
			t.Fatalf("indexStartCode(% x) = %d, want %d", b, got, want)
		}
	}
}

func TestIndexEveryPosition(t *testing.T) {
	codes := [][]byte{{0, 0, 1}, {0, 0, 0, 1}, {0, 0, 0, 0, 1}}
	for n := 0; n <= 300; n++ {
		base := bytes.Repeat([]byte{0x55}, n)
		check(t, base)
		for _, code := range codes {
			for at := 0; at+len(code) <= n; at++ {
				b := bytes.Clone(base)
				copy(b[at:], code)
				check(t, b)
				// Something like a start code just before it changes nothing.
				if at >= 2 {
					b[at-2], b[at-1] = 0, 0
					check(t, b)
				}
			}
		}
		// Near misses: a lone 00 00 at the end, 00 00 02, 00 01.
		for _, tail := range [][]byte{{0, 0}, {0, 0, 2}, {0, 1}, {0}} {
			if len(tail) <= n {
				b := bytes.Clone(base)
				copy(b[n-len(tail):], tail)
				check(t, b)
			}
		}
	}
}

func TestIndexCodedVideo(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for k := 0; k < 400; k++ {
		b := coded(r, 1+r.Intn(30000))
		for j := r.Intn(4); j > 0 && len(b) >= 4; j-- {
			copy(b[r.Intn(len(b)-3):], []byte{0, 0, 0, 1}[r.Intn(2):])
		}
		for off := 0; off < len(b); off += 1 + r.Intn(700) {
			check(t, b[off:])
		}
	}
}

// The search reads no byte past the slice: a start code that would end
// beyond it is not found.
func TestIndexStopsAtLength(t *testing.T) {
	buf := bytes.Repeat([]byte{0x55}, 512)
	for n := 3; n < 300; n++ {
		copy(buf[n-2:], []byte{0, 0, 1})
		check(t, buf[:n])
		check(t, buf[:n-1])
		copy(buf[n-2:], []byte{0x55, 0x55, 0x55})
	}
}

func benchmarkIndex(b *testing.B, index func([]byte) int) {
	r := rand.New(rand.NewSource(2))
	units := make([][]byte, 64)
	for i := range units {
		units[i] = append([]byte{0, 0, 0, 1, 0x26, 1}, coded(r, 9000)...)
	}
	b.SetBytes(int64(len(units[0])))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u := units[i%len(units)]
		start := index(u)
		if start != 1 || index(u[start+3:]) != -1 {
			b.Fatal("start codes")
		}
	}
}

// One access unit as the stand's encoder sends it (9 KB, one start code).
func BenchmarkIndex(b *testing.B) { benchmarkIndex(b, Index) }

func BenchmarkBytesIndex(b *testing.B) {
	benchmarkIndex(b, func(s []byte) int { return bytes.Index(s, startCode) })
}
