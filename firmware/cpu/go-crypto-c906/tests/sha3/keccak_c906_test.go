// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build riscv64 && !purego

package sha3

import (
	"crypto/internal/fips140deps/cpu"
	"math/rand/v2"
	"os"
	"testing"
)

// On a T-Head core the C906 permutation must be selected; otherwise every
// other test here would pass on the generic code.
func TestKeccakC906Selected(t *testing.T) {
	if !cpu.RISCV64HasXTheadBb {
		if os.Getenv("NANOKVM_REQUIRE_XTHEADBB") == "1" {
			t.Fatal("XTheadBb not detected")
		}
		t.Skip("not a T-Head core")
	}
	if !useC906 {
		t.Fatal("XTheadBb detected but the C906 permutation is not selected")
	}
}

// Differential test against the generic permutation: random, all-zero and
// all-one states, each also chained through a second permutation.
func TestKeccakF1600C906(t *testing.T) {
	if !cpu.RISCV64HasXTheadBb {
		t.Skip("not a T-Head core")
	}
	rng := rand.New(rand.NewPCG(0xc906, 1600))
	for n := 0; n < 20000; n++ {
		var a, b [200]byte
		switch n {
		case 0:
		case 1:
			for i := range a {
				a[i] = 0xff
			}
		default:
			for i := range a {
				a[i] = byte(rng.Uint32())
			}
		}
		b = a
		for round := 0; round < 2; round++ {
			keccakF1600C906(&a)
			keccakF1600Generic(&b)
			if a != b {
				t.Fatalf("state %d, permutation %d differs", n, round)
			}
		}
	}
}

func BenchmarkKeccakF1600C906(b *testing.B) {
	var s [200]byte
	b.SetBytes(168)
	for i := 0; i < b.N; i++ {
		keccakF1600C906(&s)
	}
}

func BenchmarkKeccakF1600Generic(b *testing.B) {
	var s [200]byte
	b.SetBytes(168)
	for i := 0; i < b.N; i++ {
		keccakF1600Generic(&s)
	}
}
