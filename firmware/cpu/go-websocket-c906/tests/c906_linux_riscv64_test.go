// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by the BSD-style LICENSE in this directory.

//go:build gc && !purego && !appengine

package websocket

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var c906Measure = flag.Bool("c906.measure", false, "measure hot and streaming masks")
var c906Negative = flag.Bool("c906.negative", false, "corrupt one result to verify the checker")
var c906Masks = []struct {
	name string
	f    func([4]byte, int, []byte) int
}{
	{"generic", maskBytesGeneric}, {"selected", maskBytes},
}

func c906Require(t *testing.T) {
	t.Helper()
	if !maskC906Available() {
		if os.Getenv("NANOKVM_MASK_REQUIRE_C906") == "1" {
			t.Fatal("required C906 kernel contract unavailable")
		}
		t.Skip("requires C906 vector state and fast misaligned words")
	}
}
func c906Oracle(key [4]byte, pos int, b []byte) int {
	for i := range b {
		b[i] ^= key[pos&3]
		pos++
	}
	return pos & 3
}
func c906CPUTime() int64 {
	var ts syscall.Timespec
	_, _, errno := syscall.RawSyscall(syscall.SYS_CLOCK_GETTIME, 3, uintptr(unsafe.Pointer(&ts)), 0)
	if errno != 0 {
		panic(errno)
	}
	return ts.Nano()
}
func TestC906Selection(t *testing.T) {
	c906Require(t)
	want := os.Getenv("NANOKVM_WEBSOCKET_MASK") != "generic"
	if useC906Mask != want {
		t.Fatalf("selected=%v want=%v", useC906Mask, want)
	}
	t.Logf("C906 available; selected=%v", useC906Mask)
}
func TestC906ISA(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want bool
	}{
		{"isa : rv64imafdc_xtheadvector_zicsr\n", true},
		{"isa : rv64imafdc_v\n", false},
		{"isa : rv64imafdc_xtheadvectorx\n", false},
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc\n", false},
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc_xtheadvector\n", true},
		{"uarch : xtheadvector\n", false},
	} {
		if got := maskHasXTheadVector(tc.s); got != tc.want {
			t.Errorf("%q: %v", tc.s, got)
		}
	}
}
func TestC906Correct(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c906Require(t)
	rng := rand.New(rand.NewSource(906260))
	lengths := make([]int, 513)
	for i := range lengths {
		lengths[i] = i
	}
	lengths = append(lengths, 1023, 1024, 1025, 1400, 1460, 4095, 4096, 4097, 16383, 16384, 16385, 65535, 65536, 65537)
	variants := append(append([]struct {
		name string
		f    func([4]byte, int, []byte) int
	}{}, c906Masks...), struct {
		name string
		f    func([4]byte, int, []byte) int
	}{"assembly", maskBytesC906})
	count := 0
	for _, n := range lengths {
		for off := 0; off < 16; off++ {
			original := make([]byte, n+off+32)
			rng.Read(original)
			base := 16 + off
			for pos := 0; pos < 4; pos++ {
				var key [4]byte
				rng.Read(key[:])
				if n == 4 {
					key = [4]byte{}
				}
				if n == 8 {
					key = [4]byte{255, 255, 255, 255}
				}
				want := append([]byte(nil), original...)
				end := c906Oracle(key, pos, want[base:base+n])
				for _, v := range variants {
					got := append([]byte(nil), original...)
					ret := v.f(key, pos, got[base:base+n])
					if *c906Negative && count == 0 {
						got[base] ^= 1
					}
					if ret != end || !bytes.Equal(got, want) {
						t.Fatalf("%s n=%d off=%d pos=%d ret=%d/%d", v.name, n, off, pos, ret, end)
					}
					count++
				}
			}
		}
	}
	for j := 0; j < 1000; j++ {
		n := rng.Intn(16385)
		off := rng.Intn(16)
		pos := rng.Intn(4)
		var key [4]byte
		rng.Read(key[:])
		original := make([]byte, n+off+16)
		rng.Read(original)
		want := append([]byte(nil), original...)
		end := c906Oracle(key, pos, want[off:off+n])
		for _, v := range variants {
			got := append([]byte(nil), original...)
			phase := pos
			for used := 0; used < n; {
				step := c906Min(n-used, 1+rng.Intn(97))
				phase = v.f(key, phase, got[off+used:off+used+step])
				used += step
			}
			if phase != end || !bytes.Equal(got, want) {
				t.Fatalf("chunk mismatch %s", v.name)
			}
			count++
		}
	}
	t.Logf("PASS %d payload/guard/phase/chunk comparisons", count)
}
func TestC906GuardPages(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c906Require(t)
	const page = 4096
	mem, e := syscall.Mmap(-1, 0, 34*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if e != nil {
		t.Fatal(e)
	}
	defer syscall.Munmap(mem)
	for _, p := range [][]byte{mem[:page], mem[len(mem)-page:]} {
		if e = syscall.Mprotect(p, syscall.PROT_NONE); e != nil {
			t.Fatal(e)
		}
	}
	mid := mem[page : len(mem)-page]
	original := make([]byte, len(mid))
	for i := range original {
		original[i] = byte(i*37 + 0x65)
	}
	lengths := make([]int, 129)
	for i := range lengths {
		lengths[i] = i
	}
	lengths = append(lengths, 255, 256, 257, 511, 512, 513, 1400, 4095, 4096, 4097, 16384, 65536, 131072)
	count := 0
	for _, n := range lengths {
		for edge := 0; edge < 2; edge++ {
			start := page
			if edge == 1 {
				start = len(mem) - page - n
			}
			if n == 0 {
				start = 0
			}
			for pos := 0; pos < 4; pos++ {
				key := [4]byte{0x19, 0x83, 0xd7, 0xfa}
				want := append([]byte(nil), original...)
				end := pos
				if n > 0 {
					end = c906Oracle(key, pos, want[start-page:start-page+n])
				}
				for _, v := range c906Masks {
					copy(mid, original)
					ret := v.f(key, pos, mem[start:start+n])
					if ret != end || !bytes.Equal(mid, want) {
						t.Fatalf("%s n=%d edge=%d pos=%d", v.name, n, edge, pos)
					}
					count++
				}
			}
		}
	}
	t.Logf("PASS %d inaccessible-page cases", count)
}
func TestC906Measure(t *testing.T) {
	if !*c906Measure {
		t.Skip("enable -c906.measure")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c906Require(t)
	sizes := []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 129, 255, 256, 257, 511, 512, 513, 1400, 1460, 4096, 16384, 65536, 262144, 1048576}
	offsets := []int{0, 1, 5, 7}
	buf := make([]byte, 1048576+32)
	for i := range buf {
		buf[i] = byte(i*37 + 11)
	}
	key := [4]byte{0x17, 0x91, 0xc3, 0x6e}
	for rep := 0; rep < 3; rep++ {
		for j := 0; j < len(sizes)*len(offsets); j++ {
			id := (j + rep*31) % (len(sizes) * len(offsets))
			n := sizes[id/len(offsets)]
			off := offsets[id%len(offsets)]
			data := buf[16+off : 16+off+n]
			calls := c906Max(16, c906Min(262144, 8*1024*1024/c906Max(1, n)))
			calls &= ^1
			for k := range c906Masks {
				v := c906Masks[(k+rep+j)%len(c906Masks)]
				want := append([]byte(nil), data...)
				ret := 0
				start := time.Now()
				cpu := c906CPUTime()
				for i := 0; i < calls; i++ {
					ret = v.f(key, 1, data)
				}
				cpu = c906CPUTime() - cpu
				wall := time.Since(start).Nanoseconds()
				if ret != (n+1)&3 || !bytes.Equal(data, want) {
					t.Fatal("timed payload", v.name, n)
				}
				fmt.Printf("MEASURE warm,%s,%d,%d,%d,%d,%d,%d\n", v.name, n, off, rep, calls, cpu, wall)
			}
		}
	}
}
func TestC906Streaming(t *testing.T) {
	if !*c906Measure {
		t.Skip("enable -c906.measure")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c906Require(t)
	const working = 8 << 20
	arena := make([]byte, working+128)
	original := make([]byte, len(arena))
	for i := range original {
		original[i] = byte(i*37 + 11)
	}
	sizes := []int{256, 1400, 4096, 16384, 65536, 1048576}
	key := [4]byte{0x17, 0x91, 0xc3, 0x6e}
	for rep := 0; rep < 3; rep++ {
		for j := 0; j < len(sizes)*2; j++ {
			id := (j + rep*5) % (len(sizes) * 2)
			n := sizes[id/2]
			off := id % 2
			stride := ((n + 63) &^ 63) + 64
			count := working / stride
			tiles := make([][]byte, count)
			for k := range tiles {
				start := 64 + off + k*stride
				tiles[k] = arena[start : start+n]
			}
			for k := range c906Masks {
				v := c906Masks[(k+rep+j)%len(c906Masks)]
				copy(arena, original)
				ret := 0
				start := time.Now()
				cpu := c906CPUTime()
				for pass := 0; pass < 2; pass++ {
					for _, data := range tiles {
						ret = v.f(key, 1, data)
					}
				}
				cpu = c906CPUTime() - cpu
				wall := time.Since(start).Nanoseconds()
				if ret != (n+1)&3 || !bytes.Equal(arena, original) {
					t.Fatal("stream payload", v.name, n)
				}
				fmt.Printf("MEASURE stream,%s,%d,%d,%d,%d,%d,%d\n", v.name, n, off, rep, 2*count, cpu, wall)
			}
		}
	}
}
func TestC906Stress(t *testing.T) {
	c906Require(t)
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	var wg sync.WaitGroup
	var iterations atomic.Uint64
	until := time.Now().Add(5 * time.Second)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			c906Require(t)
			arena := make([]byte, 65568)
			want := make([]byte, len(arena))
			key := [4]byte{byte(id), 0x82, 0x19, 0xdb}
			for k := 0; time.Now().Before(until); k++ {
				n := 1 + (k*157+id*31)%65536
				off := (k + id) & 15
				for i := range arena {
					arena[i] = byte(i + k)
				}
				copy(want, arena)
				end := c906Oracle(key, k&3, want[off:off+n])
				ret := maskBytes(key, k&3, arena[off:off+n])
				if ret != end || !bytes.Equal(arena, want) {
					t.Errorf("stress worker %d", id)
					return
				}
				iterations.Add(1)
				runtime.Gosched()
			}
		}(worker)
	}
	wg.Wait()
	if iterations.Load() < 100 {
		t.Fatal("too few stress iterations")
	}
	t.Logf("PASS %d iterations across four locked threads", iterations.Load())
}

func c906Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func c906Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
