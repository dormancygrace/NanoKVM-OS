// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings_test

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// TestIndexHorspoolStrings compares strings.Index (internal/stringslite,
// where the Rabin-Karp fallback is Horspool now) with a naive reference.
func TestIndexHorspoolStrings(t *testing.T) {
	naive := func(s, sub string) int {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return i
			}
		}
		return -1
	}
	r := rand.New(rand.NewPCG(3, 4))
	for iter := 0; iter < 20000; iter++ {
		alphabet := 1 + r.IntN(4)
		if iter%3 == 0 {
			alphabet = 256
		}
		b := make([]byte, r.IntN(400))
		for i := range b {
			b[i] = byte(r.IntN(alphabet))
		}
		n := 2 + r.IntN(12)
		if iter%50 == 0 {
			n = 2 + r.IntN(254)
		}
		sub := make([]byte, n)
		if len(b) >= n && r.IntN(2) == 0 {
			copy(sub, b[r.IntN(len(b)-n+1):])
		} else {
			for i := range sub {
				sub[i] = byte(r.IntN(alphabet))
			}
		}
		if got, want := strings.Index(string(b), string(sub)), naive(string(b), string(sub)); got != want {
			t.Fatalf("Index(%q, %q) = %d, want %d", b, sub, got, want)
		}
	}
}
