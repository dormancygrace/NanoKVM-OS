// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package chacha20poly1305

import (
	"flag"
	"runtime"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// BenchmarkC906Seal seals TLS-sized records with a 5 byte TLS 1.3 header as
// additional data, as crypto/tls does.
func BenchmarkC906Seal(b *testing.B) {
	for _, size := range []int{1400, 16384} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			seal := newSeal(size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				seal()
			}
		})
	}
}

func BenchmarkC906Open(b *testing.B) {
	for _, size := range []int{1400, 16384} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			key := make([]byte, KeySize)
			nonce := make([]byte, NonceSize)
			ad := make([]byte, 5)
			aead, _ := New(key)
			ct := aead.Seal(nil, nonce, make([]byte, size), ad)
			out := make([]byte, 0, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				var err error
				out, err = aead.Open(out[:0], nonce, ct, ad)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func newSeal(size int) func() {
	key := make([]byte, KeySize)
	nonce := make([]byte, NonceSize)
	ad := make([]byte, 5)
	// TLS records start after a 5 byte header, so the payload is unaligned.
	buf := make([]byte, size+5)[5:]
	out := make([]byte, 5, 5+size+Overhead)
	aead, _ := New(key)
	return func() { aead.Seal(out[:5], nonce, buf, ad) }
}

var c906Measure = flag.Int("c906.measure", 0, "rounds of TestC906Measure (0 skips it)")

// threadCPU returns the CPU time of the calling thread.
func threadCPU() time.Duration {
	const clockThreadCPUTimeID = 3
	var ts syscall.Timespec
	syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockThreadCPUTimeID, uintptr(unsafe.Pointer(&ts)), 0)
	return time.Duration(ts.Nano())
}

// measure runs f for about 150 ms of thread CPU time per round and returns
// the median MB/s by thread CPU time and by wall time. Other processes share
// the single core, so the CPU time figure is the stable one.
func measure(rounds, size int, f func()) (cpu, wall float64) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	n := 1
	for {
		c0 := threadCPU()
		for i := 0; i < n; i++ {
			f()
		}
		if threadCPU()-c0 > 20*time.Millisecond {
			break
		}
		n *= 2
	}
	n *= 8
	var cpus, walls []float64
	for r := 0; r < rounds; r++ {
		runtime.GC()
		w0, c0 := time.Now(), threadCPU()
		for i := 0; i < n; i++ {
			f()
		}
		c, w := threadCPU()-c0, time.Since(w0)
		bytes := float64(n * size)
		cpus = append(cpus, bytes/c.Seconds()/1e6)
		walls = append(walls, bytes/w.Seconds()/1e6)
	}
	slices.Sort(cpus)
	slices.Sort(walls)
	return cpus[len(cpus)/2], walls[len(walls)/2]
}

func TestC906Measure(t *testing.T) {
	if *c906Measure == 0 {
		t.Skip("enable with -c906.measure=ROUNDS")
	}
	for _, size := range []int{1400, 16384} {
		cpu, wall := measure(*c906Measure, size, newSeal(size))
		t.Logf("MEASURE seal %5d: %6.1f MB/s cpu, %6.1f MB/s wall", size, cpu, wall)
	}
}
