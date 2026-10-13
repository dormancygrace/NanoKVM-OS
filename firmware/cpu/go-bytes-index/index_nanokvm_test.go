// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bytes_test

import (
	. "bytes"
	"internal/bytealg"
	"math/rand/v2"
	"testing"
)

// naiveIndex is the reference: the first i with s[i:i+len(sep)] == sep.
func naiveIndex(s, sep []byte) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if Equal(s[i:i+len(sep)], sep) {
			return i
		}
	}
	return -1
}

// TestIndexHorspool compares indexHorspool and Index with the reference on
// inputs from small alphabets (many partial matches) and on start-code
// searches in video-like data.
func TestIndexHorspool(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 20000; iter++ {
		alphabet := 1 + r.IntN(4)
		if iter%3 == 0 {
			alphabet = 256
		}
		s := make([]byte, r.IntN(400))
		for i := range s {
			s[i] = byte(r.IntN(alphabet))
		}
		n := 2 + r.IntN(12)
		if iter%50 == 0 {
			n = 2 + r.IntN(HorspoolMaxLen-1)
		}
		sep := make([]byte, n)
		if len(s) >= n && r.IntN(2) == 0 {
			copy(sep, s[r.IntN(len(s)-n+1):])
		} else {
			for i := range sep {
				sep[i] = byte(r.IntN(alphabet))
			}
		}
		want := naiveIndex(s, sep)
		if got := IndexHorspool(s, sep); got != want {
			t.Fatalf("IndexHorspool(%x, %x) = %d, want %d", s, sep, got, want)
		}
		if got := Index(s, sep); got != want {
			t.Fatalf("Index(%x, %x) = %d, want %d", s, sep, got, want)
		}
	}
	startCode := []byte{0, 0, 1}
	frame := videoLike(64<<10, 3)
	for _, at := range []int{0, 1, 100, len(frame) - 3, -1} {
		f := append([]byte(nil), frame...)
		if at >= 0 {
			copy(f[at:], startCode)
		}
		if got, want := Index(f, startCode), naiveIndex(f, startCode); got != want {
			t.Fatalf("start code at %d: Index %d, want %d", at, got, want)
		}
	}
}

// videoLike is coded-video-like data: mostly random bytes, frequent zero
// runs, and no 00 00 0x (x <= 3) triple (emulation prevention).
func videoLike(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		if r.IntN(16) == 0 {
			b[i] = 0
		} else {
			b[i] = byte(r.IntN(256))
		}
		if i >= 2 && b[i-2] == 0 && b[i-1] == 0 && b[i] <= 3 {
			b[i] = 4
		}
	}
	return b
}

func BenchmarkIndexStartCode(b *testing.B) {
	frame := videoLike(32<<10, 5)
	startCode := []byte{0, 0, 1}
	b.SetBytes(int64(len(frame)))
	for b.Loop() {
		if Index(frame, startCode) != -1 {
			b.Fatal("found")
		}
	}
}

func BenchmarkIndexStartCodeRabinKarp(b *testing.B) {
	frame := videoLike(32<<10, 5)
	startCode := []byte{0, 0, 1}
	b.SetBytes(int64(len(frame)))
	for b.Loop() {
		if bytealg.IndexRabinKarp(frame, startCode) != -1 {
			b.Fatal("found")
		}
	}
}

// The repeated-prefix case from review: every window matches its last byte
// and compares almost all of sep for a shift of two.
func repeatedPrefix() ([]byte, []byte) {
	s := Repeat([]byte("a"), 1<<20)
	sep := append(Repeat([]byte("a"), 253), 'b', 'a')
	return s, sep
}

func BenchmarkIndexRepeatedPrefix(b *testing.B) {
	s, sep := repeatedPrefix()
	b.SetBytes(int64(len(s)))
	for b.Loop() {
		if Index(s, sep) != -1 {
			b.Fatal("unexpected match")
		}
	}
}

func BenchmarkIndexRepeatedPrefixRabinKarp(b *testing.B) {
	s, sep := repeatedPrefix()
	b.SetBytes(int64(len(s)))
	for b.Loop() {
		if bytealg.IndexRabinKarp(s, sep) != -1 {
			b.Fatal("unexpected match")
		}
	}
}

func TestIndexRepeatedPrefix(t *testing.T) {
	s, sep := repeatedPrefix()
	if got := Index(s, sep); got != -1 {
		t.Fatalf("Index = %d, want -1", got)
	}
	copy(s[len(s)-len(sep):], sep)
	if got, want := Index(s, sep), len(s)-len(sep); got != want {
		t.Fatalf("Index = %d, want %d", got, want)
	}
}
