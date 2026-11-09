// Copyright 2026 The NanoKVM OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package poly1305

import (
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"testing"
)

// The reduction and guard tests also run the CRYPTOGAMS update on its own.
func init() {
	polyVariants = append(polyVariants, struct {
		name string
		f    func(*macState, []byte)
	}{"scalar", updateScalar})
}

// TestC906VectorSelected fails on a T-Head core with vector state enabled
// that does not select the vector kernel when NANOKVM_REQUIRE_XTHEADVECTOR=1.
func TestC906VectorSelected(t *testing.T) {
	t.Logf("vector kernel selected: %v", vectorSelected())
	if !vectorSelected() && os.Getenv("NANOKVM_REQUIRE_XTHEADVECTOR") == "1" {
		t.Fatal("NANOKVM_REQUIRE_XTHEADVECTOR=1, but the vector kernel is not selected")
	}
}

// TestC906Vector compares update (vector kernel for whole groups, CRYPTOGAMS
// for the rest) with updateScalar and a big-integer Poly1305: lengths around
// the group and threshold boundaries, every offset mod 16, random and extreme
// keys, accumulators up to h2 = 7 and all-ones messages.
func TestC906Vector(t *testing.T) {
	if !vectorSelected() {
		t.Skip("vector kernel not selected (no XTheadVector or vector state disabled)")
	}
	rng := rand.New(rand.NewSource(1305))
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))
	bound := new(big.Int).Lsh(new(big.Int).Set(p), 1)
	min := vectorMinGroups * vectorGroup
	var lengths []int
	for n := min - 17; n <= min+2*vectorGroup+17; n++ {
		lengths = append(lengths, n)
	}
	lengths = append(lengths, 1023, 1024, 1400, 1408, 1420, 1460, 4096, 4101, 16384, 16389, 65536)
	checks := 0
	for _, n := range lengths {
		for off := 0; off < 16; off++ {
			for variant := 0; variant < 3; variant++ {
				var key [32]byte
				rng.Read(key[:])
				msg := make([]byte, n+off+16)[off:][:n]
				rng.Read(msg)
				var s macState
				switch variant {
				case 1: // clamped-maximum r, all-ones message, maximal accumulator
					for i := range key {
						key[i] = 0xff
					}
					for i := range msg {
						msg[i] = 0xff
					}
					initialize(&key, &s)
					s.h = [3]uint64{^uint64(0), ^uint64(0), 7}
				case 2:
					initialize(&key, &s)
					s.h = [3]uint64{rng.Uint64(), rng.Uint64(), uint64(rng.Intn(8))}
				default:
					initialize(&key, &s)
				}
				want := s
				updateScalar(&want, msg)
				got := s
				update(&got, msg)
				var tagWant, tag [16]byte
				finalize(&tagWant, &want.h, &want.s)
				finalize(&tag, &got.h, &got.s)
				if tag != tagWant || got.r != s.r || got.s != s.s {
					t.Fatalf("n=%d off=%d variant=%d: tag/key mismatch", n, off, variant)
				}
				if got.h[2] > 4 || polyStateInt(got.h).Cmp(bound) >= 0 {
					t.Fatalf("n=%d off=%d variant=%d: state bound, h2=%d", n, off, variant, got.h[2])
				}
				if off == 5 || variant == 1 {
					if new(big.Int).Mod(polyStateInt(got.h), p).Cmp(polyBig(s, msg)) != 0 {
						t.Fatalf("n=%d off=%d variant=%d: big-int mismatch", n, off, variant)
					}
				}
				checks++
			}
		}
	}
	t.Logf("PASS %d vector/scalar/oracle comparisons", checks)
}

// BenchmarkC906VectorGroups times updateVector against updateScalar for runs
// of whole groups (offset 5, as in TLS records); vectorMinGroups is where
// the vector code starts to win.
func BenchmarkC906VectorGroups(b *testing.B) {
	if !vectorSelected() {
		b.Skip("vector kernel not selected")
	}
	var key [32]byte
	for i := range key {
		key[i] = byte(i)
	}
	buf := make([]byte, 205*vectorGroup+5)
	for _, groups := range []int{2, 3, 4, 5, 6, 8, 10, 17, 204} {
		msg := buf[5 : 5+groups*vectorGroup]
		for _, v := range []string{"vector", "scalar"} {
			b.Run(fmt.Sprintf("%d/%s", len(msg), v), func(b *testing.B) {
				b.SetBytes(int64(len(msg)))
				var s macState
				initialize(&key, &s)
				for i := 0; i < b.N; i++ {
					if v == "vector" {
						updateVector(&s, msg, groups)
					} else {
						updateScalar(&s, msg)
					}
				}
			})
		}
	}
}
