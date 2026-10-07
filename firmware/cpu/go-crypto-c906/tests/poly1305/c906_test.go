// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package poly1305

import (
	"math/rand"
	"testing"
)

// TestC906Misaligned compares the assembly with and without misaligned
// 64-bit loads against the generic code, for all message offsets mod 16,
// one-shot and through Write in random pieces.
func TestC906Misaligned(t *testing.T) {
	t.Logf("useMisalignedLoads=%v", useMisalignedLoads)
	defer func(old bool) { useMisalignedLoads = old }(useMisalignedLoads)
	rnd := rand.New(rand.NewSource(3))
	checks := 0
	for _, misaligned := range []bool{false, true} {
		useMisalignedLoads = misaligned
		for iter := 0; iter < 4000; iter++ {
			var key [32]byte
			rnd.Read(key[:])
			n := rnd.Intn(2100)
			off := iter % 16
			msg := make([]byte, n+off+16)[off:][:n]
			rnd.Read(msg)
			var want, got [16]byte
			sumGeneric(&want, msg, &key)
			Sum(&got, msg, &key)
			if got != want {
				t.Fatalf("misaligned=%v n=%d off=%d: Sum differs", misaligned, n, off)
			}
			h := New(&key)
			for rest := msg; len(rest) > 0; {
				k := 1 + rnd.Intn(100)
				if k > len(rest) {
					k = len(rest)
				}
				h.Write(rest[:k])
				rest = rest[k:]
			}
			if string(h.Sum(nil)) != string(want[:]) {
				t.Fatalf("misaligned=%v n=%d off=%d: Write/Sum differs", misaligned, n, off)
			}
			checks++
		}
	}
	t.Logf("%d checks", checks)
}
