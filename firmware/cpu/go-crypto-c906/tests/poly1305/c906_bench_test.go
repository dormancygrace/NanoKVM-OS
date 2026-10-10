// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package poly1305

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

func newSum(size, off int) func() {
	var out [16]byte
	var key [32]byte
	in := make([]byte, size+off)[off:]
	return func() { Sum(&out, in, &key) }
}

func BenchmarkC906Sum(b *testing.B) {
	for _, size := range []int{1400, 16384} {
		for _, off := range []int{0, 5} {
			b.Run(strconv.Itoa(size)+"/off"+strconv.Itoa(off), func(b *testing.B) {
				f := newSum(size, off)
				b.SetBytes(int64(size))
				for b.Loop() {
					f()
				}
			})
		}
	}
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
		for _, off := range []int{0, 5} {
			cpu, wall := measure(*c906Measure, size, newSum(size, off))
			t.Logf("MEASURE poly1305 %5d off%d: %6.1f MB/s cpu, %6.1f MB/s wall", size, off, cpu, wall)
		}
	}
}
