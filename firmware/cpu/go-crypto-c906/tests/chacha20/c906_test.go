// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package chacha20

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"io"
	"math/bits"
	"math/rand"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var c906Stress = flag.Duration("c906.stress", 3*time.Second, "duration of TestC906Stress")

const (
	implGeneric = iota
	implVector
)

var implNames = [...]string{implGeneric: "generic", implVector: "xtheadvector"}

func haveVector() bool { return cpuinfoHasExtension("xtheadvector") && vectorEnabled() }

func selectedImpl() int {
	if c906() {
		return implVector
	}
	return implGeneric
}

// availableImpls returns the implementations that can run on this machine.
func availableImpls() []int {
	if haveVector() {
		return []int{implGeneric, implVector}
	}
	return []int{implGeneric}
}

// withImpl runs f with the given implementation selected.
func withImpl(id int, f func()) {
	c906()
	old := useC906
	useC906 = id == implVector
	defer func() { useC906 = old }()
	f()
}

// refKeyStream is a direct RFC 8439 section 2.3 block function, independent
// of the package code. It returns n blocks starting at counter.
func refKeyStream(key, nonce []byte, counter uint32, n int) []byte {
	var in [16]uint32
	in[0], in[1], in[2], in[3] = 0x61707865, 0x3320646e, 0x79622d32, 0x6b206574
	for i := 0; i < 8; i++ {
		in[4+i] = binary.LittleEndian.Uint32(key[4*i:])
	}
	for i := 0; i < 3; i++ {
		in[13+i] = binary.LittleEndian.Uint32(nonce[4*i:])
	}
	qr := func(x *[16]uint32, a, b, c, d int) {
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 16)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 12)
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 8)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 7)
	}
	out := make([]byte, 0, 64*n)
	for blk := 0; blk < n; blk++ {
		in[12] = counter + uint32(blk)
		x := in
		for r := 0; r < 10; r++ {
			qr(&x, 0, 4, 8, 12)
			qr(&x, 1, 5, 9, 13)
			qr(&x, 2, 6, 10, 14)
			qr(&x, 3, 7, 11, 15)
			qr(&x, 0, 5, 10, 15)
			qr(&x, 1, 6, 11, 12)
			qr(&x, 2, 7, 8, 13)
			qr(&x, 3, 4, 9, 14)
		}
		for i := range x {
			out = binary.LittleEndian.AppendUint32(out, x[i]+in[i])
		}
	}
	return out
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func TestC906Selection(t *testing.T) {
	env, _ := syscall.Getenv("NANOKVM_CHACHA20")
	vec := cpuinfoHasExtension("xtheadvector")
	en := vectorEnabled()
	got := selectedImpl()
	t.Logf("NANOKVM_CHACHA20=%q cpuinfo xtheadvector=%v prctl vector on=%v selected=%s", env, vec, en, implNames[got])
	want := implGeneric
	if env != "generic" && vec && en {
		want = implVector
	}
	if got != want {
		t.Fatalf("selected %s, want %s", implNames[got], implNames[want])
	}
	for _, c := range []struct {
		isa  string
		want bool
	}{
		{"rv64imafdc_zicntr_zicsr_zifencei_zihpm_zaamo_zalrsc_zca_zcd_xtheadvector", true},
		{"rv64imafdc_xtheadvector_zicsr", true},
		{"rv64imafdc_zicsr", false},
		{"rv64imafdc_xtheadvectorx", false},
		{"rv64imafdc_x_theadvector", false},
		{"", false},
	} {
		if hasToken([]byte(c.isa), "xtheadvector") != c.want {
			t.Errorf("hasToken(%q) != %v", c.isa, c.want)
		}
	}
}

// RFC 8439 sections 2.3.2, 2.4.2 and A.1 vectors 1 and 2.
func TestC906RFC8439(t *testing.T) {
	seq := make([]byte, 32)
	for i := range seq {
		seq[i] = byte(i)
	}
	sunscreen := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")
	cases := []struct {
		name    string
		key     []byte
		nonce   string
		counter uint32
		in      []byte
		out     string
	}{
		{"2.3.2", seq, "000000090000004a00000000", 1, make([]byte, 64),
			"10f1e7e4d13b5915500fdd1fa32071c4c7d1f4c733c068030422aa9ac3d46c4e" +
				"d2826446079faa0914c2d705d98b02a2b5129cd1de164eb9cbd083e8a2503c4e"},
		{"2.4.2", seq, "000000000000004a00000000", 1, sunscreen,
			"6e2e359a2568f98041ba0728dd0d6981e97e7aec1d4360c20a27afccfd9fae0b" +
				"f91b65c5524733ab8f593dabcd62b3571639d624e65152ab8f530c359f0861d8" +
				"07ca0dbf500d6a6156a38e088a22b65e52bc514d16ccf806818ce91ab7793736" +
				"5af90bbf74a35be6b40b8eedf2785e42874d"},
		{"A.1#1", make([]byte, 32), "000000000000000000000000", 0, make([]byte, 64),
			"76b8e0ada0f13d90405d6ae55386bd28bdd219b8a08ded1aa836efcc8b770dc7" +
				"da41597c5157488d7724e03fb8d84a376a43b8f41518a11cc387b669b2ee6586"},
		{"A.1#2", make([]byte, 32), "000000000000000000000000", 1, make([]byte, 64),
			"9f07e7be5551387a98ba977c732d080dcb0f29a048e3656912c6533e32ee7aed" +
				"29b721769ce64e43d57133b074d839d531ed1f28510afb45ace10a1f4b794d6f"},
	}
	for _, id := range availableImpls() {
		for _, c := range cases {
			withImpl(id, func() {
				nonce, _ := hex.DecodeString(c.nonce)
				// Repeat the input past several kernel iterations, then check
				// the first part against the RFC and the rest against the
				// reference function.
				in := bytes.Repeat(c.in, 1+2000/len(c.in))
				s, _ := NewUnauthenticatedCipher(c.key, nonce)
				s.SetCounter(c.counter)
				out := make([]byte, len(in))
				s.XORKeyStream(out, in)
				if got := hex.EncodeToString(out[:len(c.in)]); got != c.out {
					t.Errorf("%s %s: got %s, want %s", implNames[id], c.name, got, c.out)
				}
				want := xorBytes(in, refKeyStream(c.key, nonce, c.counter, (len(in)+63)/64)[:len(in)])
				if !bytes.Equal(out, want) {
					t.Errorf("%s %s: differs from the reference after the vector", implNames[id], c.name)
				}
			})
		}
	}
}

// TestC906Kernel calls the assembly directly: lengths, counters including
// 32-bit wrap inside one call, unaligned and in-place buffers, and guard bytes
// around dst.
func TestC906Kernel(t *testing.T) {
	if !haveVector() {
		t.Skip("no XTheadVector")
	}
	rnd := rand.New(rand.NewSource(1))
	const guard = 64
	checks := 0
	for iter := 0; iter < 1000; iter++ {
		key := make([]byte, 32)
		nonce := make([]byte, 12)
		rnd.Read(key)
		rnd.Read(nonce)
		n := bufSize * (rnd.Intn(12) + 1)
		if iter < 4 {
			n = 0
		}
		counter := rnd.Uint32()
		switch iter % 4 {
		case 1:
			counter = 0
		case 2:
			counter = -uint32(1 + rnd.Intn(n/blockSize+1)) // wraps in this call
		}
		c, _ := NewUnauthenticatedCipher(key, nonce)
		src := make([]byte, n+guard)[rnd.Intn(16):][:n]
		rnd.Read(src)
		want := xorBytes(src, refKeyStream(key, nonce, counter, n/blockSize))
		inPlace := iter%5 == 0
		buf := make([]byte, n+2*guard+16)
		rnd.Read(buf)
		orig := append([]byte(nil), buf...)
		off := guard + rnd.Intn(16)
		dst := buf[off : off+n]
		if inPlace {
			copy(dst, src)
			src = dst
		}
		ctr := counter
		xorKeyStreamC906(dst, src, &c.key, &c.nonce, &ctr)
		if !bytes.Equal(dst, want) {
			t.Fatalf("n=%d counter=%#x inPlace=%v: output differs", n, counter, inPlace)
		}
		if !bytes.Equal(buf[:off], orig[:off]) || !bytes.Equal(buf[off+n:], orig[off+n:]) {
			t.Fatalf("n=%d: wrote outside dst", n)
		}
		if ctr != counter+uint32(n/blockSize) {
			t.Fatalf("n=%d: counter %#x, want %#x", n, ctr, counter+uint32(n/blockSize))
		}
		checks++
	}
	t.Logf("%d kernel checks", checks)
}

// TestC906CrossCheck drives the public API with random chunking, SetCounter,
// alignment and overlap, for every available implementation, against the
// reference, including the end of the 32-bit block counter.
func TestC906CrossCheck(t *testing.T) {
	rnd := rand.New(rand.NewSource(2))
	iters := 3000
	if testing.Short() {
		iters = 300
	}
	checks := 0
	for _, id := range availableImpls() {
		withImpl(id, func() {
			for iter := 0; iter < iters; iter++ {
				key := make([]byte, 32)
				nonce := make([]byte, 12)
				rnd.Read(key)
				rnd.Read(nonce)
				var total int
				switch iter % 8 {
				case 0:
					total = rnd.Intn(64 * 1024)
				case 1:
					total = rnd.Intn(17) * 64
				default:
					total = rnd.Intn(3000)
				}
				start := rnd.Uint32()
				switch iter % 3 {
				case 0:
					start = 0
				case 1:
					// Near the end of the counter space: the last blocks and
					// the generic tail handling in XORKeyStream.
					start = -uint32(1 + rnd.Intn(12))
				}
				avail := (uint64(1)<<32 - uint64(start)) * 64
				if uint64(total) > avail {
					total = int(avail)
				}
				blocks := (uint64(total) + 63) / 64
				ks := refKeyStream(key, nonce, start, int(blocks))
				src := make([]byte, total+16)[rnd.Intn(16):][:total]
				rnd.Read(src)
				dstBuf := make([]byte, total+16)
				dst := dstBuf[rnd.Intn(16):][:total]
				inPlace := iter%4 == 3
				if inPlace {
					copy(dst, src)
				}
				want := xorBytes(src, ks[:total])

				s, _ := NewUnauthenticatedCipher(key, nonce)
				if start != 0 {
					s.SetCounter(start)
				}
				for pos := 0; pos < total; {
					step := 1 + rnd.Intn(700)
					if rnd.Intn(4) == 0 {
						step = 64 * (1 + rnd.Intn(12))
					}
					if pos+step > total {
						step = total - pos
					}
					if inPlace {
						s.XORKeyStream(dst[pos:pos+step], dst[pos:pos+step])
					} else {
						s.XORKeyStream(dst[pos:pos+step], src[pos:pos+step])
					}
					pos += step
					// Occasionally skip ahead to the next block boundary, as
					// chacha20poly1305 does with SetCounter(1).
					if next := (pos + 63) / 64 * 64; next < total && pos%64 != 0 && rnd.Intn(10) == 0 {
						s.SetCounter(start + uint32(next/64))
						copy(dst[pos:next], want[pos:next])
						pos = next
					}
				}
				if !bytes.Equal(dst, want) {
					t.Fatalf("%s iter %d total=%d start=%#x inPlace=%v: output differs", implNames[id], iter, total, start, inPlace)
				}
				if uint64(start)+blocks == 1<<32 && total > 0 && total%64 == 0 {
					// All key stream is used up now.
					if !panics(func() { s.XORKeyStream(make([]byte, 1), make([]byte, 1)) }) {
						t.Fatalf("%s: no panic after the counter overflowed", implNames[id])
					}
				}
				checks++
			}
		})
	}
	t.Logf("%d cross checks", checks)
}

// TestC906CounterEnd uses up the key stream from 2^32-k with a short first
// call, which buffers key stream, then expects the documented panic at the
// end of the counter space.
func TestC906CounterEnd(t *testing.T) {
	key := make([]byte, 32)
	nonce := make([]byte, 12)
	for i := range key {
		key[i] = byte(3 * i)
	}
	for _, id := range availableImpls() {
		withImpl(id, func() {
			for k := 1; k <= 3*bufSize/blockSize; k++ {
				for _, first := range []int{1, 10, 64, 70, bufSize - 1, bufSize} {
					total := 64 * k
					if first > total {
						continue
					}
					start := -uint32(k)
					want := refKeyStream(key, nonce, start, k)
					got := make([]byte, total)
					s, _ := NewUnauthenticatedCipher(key, nonce)
					s.SetCounter(start)
					s.XORKeyStream(got[:first], got[:first])
					s.XORKeyStream(got[first:], got[first:])
					if !bytes.Equal(got, want) {
						t.Fatalf("%s k=%d first=%d: wrong key stream", implNames[id], k, first)
					}
					if !panics(func() { s.XORKeyStream(make([]byte, 1), make([]byte, 1)) }) {
						t.Errorf("%s k=%d first=%d: no panic after the last block", implNames[id], k, first)
					}
				}
			}
		})
	}
}

func panics(f func()) (didPanic bool) {
	defer func() {
		didPanic = recover() != nil
	}()
	f()
	return
}

var stressSink atomic.Pointer[[]byte]

// TestC906Stress runs the vector kernel in several goroutines with a CPU
// profile (SIGPROF), garbage collection and preemption, and compares every
// result. Run two copies of the test binary at once to also exercise
// context switches between processes using vector state.
func TestC906Stress(t *testing.T) {
	if !haveVector() {
		t.Skip("no XTheadVector")
	}
	if selectedImpl() != implVector {
		t.Skip("vector implementation not selected")
	}
	if err := pprof.StartCPUProfile(io.Discard); err == nil {
		defer pprof.StopCPUProfile()
	}
	deadline := time.Now().Add(*c906Stress)
	var wg sync.WaitGroup
	var mu sync.Mutex
	runs := 0
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(100 + g)))
			key := make([]byte, 32)
			nonce := make([]byte, 12)
			rnd.Read(key)
			rnd.Read(nonce)
			size := 64 * 320 * (1 + g)
			src := make([]byte, size+7)[g:][:size]
			rnd.Read(src)
			want := xorBytes(src, refKeyStream(key, nonce, 7, size/64))
			dst := make([]byte, size+7)[3:][:size]
			n := 0
			for time.Now().Before(deadline) {
				s, _ := NewUnauthenticatedCipher(key, nonce)
				s.SetCounter(7)
				s.XORKeyStream(dst, src)
				if !bytes.Equal(dst, want) {
					t.Errorf("goroutine %d run %d: output differs", g, n)
					return
				}
				clear(dst)
				garbage := make([]byte, 4096) // keep the GC busy
				stressSink.Store(&garbage)
				n++
				if n%16 == 0 {
					runtime.Gosched()
				}
			}
			mu.Lock()
			runs += n
			mu.Unlock()
		}(g)
	}
	wg.Wait()
	t.Logf("%d verified runs in %v", runs, *c906Stress)
}
