// Copyright 2026 The NanoKVM OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package chacha20poly1305

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"
)

var productionMeasure = flag.Bool("c906.matrix", false, "measure TLS-sized AEAD Seal/Open")

func TestC906Matrix(t *testing.T) {
	if !*productionMeasure {
		t.Skip("enable -c906.matrix")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	rng := rand.New(rand.NewSource(250906))
	key := make([]byte, KeySize)
	rng.Read(key)
	nonce := make([]byte, NonceSize)
	rng.Read(nonce)
	ad := []byte{23, 3, 3, 0, 0}
	aead, e := New(key)
	if e != nil {
		t.Fatal(e)
	}
	sizes := []int{0, 1, 5, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 255, 256, 257, 511, 512, 513, 1400, 1460, 4096, 16384, 65536}
	for rep := 0; rep < 3; rep++ {
		for j := range len(sizes) * 2 {
			id := (j + rep*5) % (len(sizes) * 2)
			n := sizes[id/2]
			mode := "seal"
			if id%2 != 0 {
				mode = "open"
			}
			msg := make([]byte, n+5)[5:]
			rng.Read(msg)
			// Both plaintext and ciphertext start after a five-byte TLS header.
			wire := make([]byte, 5, 5+n+Overhead)
			wire = aead.Seal(wire, nonce, msg, ad)
			ct := wire[5:]
			out := make([]byte, 5, 5+n+Overhead)
			calls := 1048576 / max(16, n)
			calls = max(128, min(calls, 8192))
			var got []byte
			var err error
			wall := time.Now()
			cpu := threadCPU()
			if mode == "seal" {
				for i := 0; i < calls; i++ {
					got = aead.Seal(out[:5], nonce, msg, ad)
				}
			} else {
				for i := 0; i < calls; i++ {
					got, err = aead.Open(out[:5], nonce, ct, ad)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			c := (threadCPU() - cpu).Nanoseconds()
			w := time.Since(wall).Nanoseconds()
			want := ct
			if mode == "open" {
				want = msg
			}
			if !bytes.Equal(got[5:], want) {
				t.Fatalf("AEAD payload mode=%s n=%d", mode, n)
			}
			fmt.Printf("POLY_AEAD %s,%d,%d,%d,%d,%d\n", mode, n, rep, calls, c, w)
		}
	}
}
