// Copyright 2026 The NanoKVM OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package poly1305

import (
	"flag"
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"
)

var productionMeasure = flag.Bool("c906.matrix", false, "measure full public Sum")

func TestC906Matrix(t *testing.T) {
	if !*productionMeasure {
		t.Skip("enable -c906.matrix")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	rng := rand.New(rand.NewSource(250906))
	var key [32]byte
	rng.Read(key[:])
	sizes := []int{0, 1, 5, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 255, 256, 257, 511, 512, 513, 1400, 1460, 4096, 16384, 65536}
	for rep := 0; rep < 3; rep++ {
		for j := range len(sizes) * 2 {
			id := (j + rep*11) % (len(sizes) * 2)
			n := sizes[id/2]
			off := 0
			if id%2 != 0 {
				off = 5
			}
			msg := make([]byte, n+off)[off:]
			rng.Read(msg)
			var tag, want [16]byte
			sumGeneric(&want, msg, &key)
			calls := 1048576 / max(16, n)
			calls = max(256, min(calls, 32768))
			Sum(&tag, msg, &key)
			wall := time.Now()
			cpu := threadCPU().Nanoseconds()
			for i := 0; i < calls; i++ {
				Sum(&tag, msg, &key)
			}
			c := threadCPU().Nanoseconds() - cpu
			w := time.Since(wall).Nanoseconds()
			if tag != want {
				t.Fatalf("Sum mismatch n=%d off=%d", n, off)
			}
			fmt.Printf("POLY_SUM %d,%d,%d,%d,%d,%d\n", n, off, rep, calls, c, w)
		}
	}
}
