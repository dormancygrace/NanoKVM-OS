// Copyright 2026 The NanoKVM OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package poly1305

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math/big"
	"math/rand"
	"runtime"
	"syscall"
	"testing"
)

var polyNegative = flag.Bool("c906.negative", false, "corrupt the first captured result")
var polyVariants = []struct {
	name string
	f    func(*macState, []byte)
}{{"selected", update}}

func TestC906Dispatch(t *testing.T) {
	fmt.Printf("POLY_CONFIG fast_unaligned=%v arch=%s\n", useMisalignedLoads, runtime.GOARCH)
}
func polyIntLE(p []byte) *big.Int {
	q := append([]byte(nil), p...)
	for i, j := 0, len(q)-1; i < j; i, j = i+1, j-1 {
		q[i], q[j] = q[j], q[i]
	}
	return new(big.Int).SetBytes(q)
}
func polyStateInt(h [3]uint64) *big.Int {
	var a [24]byte
	for i := range h {
		binary.LittleEndian.PutUint64(a[8*i:], h[i])
	}
	return polyIntLE(a[:])
}
func polyBig(state macState, msg []byte) *big.Int {
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))
	h := polyStateInt(state.h)
	var key [16]byte
	binary.LittleEndian.PutUint64(key[:8], state.r[0])
	binary.LittleEndian.PutUint64(key[8:], state.r[1])
	r := polyIntLE(key[:])
	for len(msg) > 0 {
		n := min(16, len(msg))
		x := polyIntLE(msg[:n])
		x.SetBit(x, n*8, 1)
		h.Add(h, x)
		h.Mul(h, r)
		h.Mod(h, p)
		msg = msg[n:]
	}
	return h.Mod(h, p)
}
func TestC906Reduction(t *testing.T) {
	old := useMisalignedLoads
	defer func() { useMisalignedLoads = old }()
	rng := rand.New(rand.NewSource(9061305))
	checks := 0
	check := func(n, off int, state macState, msg []byte, independent bool) {
		want := state
		updateGeneric(&want, msg)
		var tagWant [16]byte
		finalize(&tagWant, &want.h, &want.s)
		p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))
		bound := new(big.Int).Lsh(new(big.Int).Set(p), 1)
		var oracle *big.Int
		if independent {
			oracle = polyBig(state, msg)
		}
		for _, v := range polyVariants {
			got := state
			v.f(&got, msg)
			var tag [16]byte
			finalize(&tag, &got.h, &got.s)
			if *polyNegative && checks == 0 {
				tag[0] ^= 1
			}
			if tag != tagWant || got.r != state.r || got.s != state.s {
				t.Fatalf("tag/key mismatch variant=%s n=%d off=%d fast=%v", v.name, n, off, useMisalignedLoads)
			}
			value := polyStateInt(got.h)
			if value.Cmp(bound) >= 0 {
				t.Fatalf("state bound %s", v.name)
			}
			if independent && new(big.Int).Mod(value, p).Cmp(oracle) != 0 {
				t.Fatalf("big-int mismatch %s n=%d", v.name, n)
			}
			checks++
		}
	}
	for _, fast := range []bool{false, true} {
		useMisalignedLoads = fast
		lengths := make([]int, 257)
		for i := range lengths {
			lengths[i] = i
		}
		lengths = append(lengths, 511, 512, 513, 1023, 1024, 1025, 1400, 1460, 4095, 4096, 4097, 16383, 16384, 16385, 65536)
		for _, n := range lengths {
			for off := 0; off < 16; off++ {
				var key [32]byte
				rng.Read(key[:])
				var s macState
				initialize(&key, &s)
				msg := make([]byte, n+off+16)[off:][:n]
				rng.Read(msg)
				check(n, off, s, msg, n <= 32 || off == 5)
			}
		}
		// Nonzero accumulators and clamped-key extremes exercise the carry bounds.
		for i := 0; i < 5000; i++ {
			var key [32]byte
			rng.Read(key[:])
			if i < 128 {
				for j := range key {
					key[j] = 0xff
				}
				key[0] = byte(i)
			}
			var s macState
			initialize(&key, &s)
			s.h = [3]uint64{rng.Uint64(), rng.Uint64(), uint64(i % 6)}
			if i < 128 {
				s.h[0] = ^uint64(0)
				s.h[1] = ^uint64(0)
			}
			n := i % 66
			off := i % 16
			msg := make([]byte, n+off)[off:]
			rng.Read(msg)
			check(n, off, s, msg, true)
		}
	}
	t.Logf("PASS %d candidate/oracle comparisons", checks)
}
func TestC906Guard(t *testing.T) {
	mem, e := syscall.Mmap(-1, 0, 12288, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if e != nil {
		t.Fatal(e)
	}
	defer syscall.Munmap(mem)
	for i := 4096; i < 8192; i++ {
		mem[i] = byte(i * 37)
	}
	for _, range_ := range [][]byte{mem[:4096], mem[8192:]} {
		if e = syscall.Mprotect(range_, syscall.PROT_NONE); e != nil {
			t.Fatal(e)
		}
	}
	if e = syscall.Mprotect(mem[4096:8192], syscall.PROT_READ); e != nil {
		t.Fatal(e)
	}
	old := useMisalignedLoads
	defer func() { useMisalignedLoads = old }()
	lengths := make([]int, 257)
	for i := range lengths {
		lengths[i] = i
	}
	lengths = append(lengths, 511, 512, 513, 1400, 1460, 4095, 4096)
	checks := 0
	for _, fast := range []bool{false, true} {
		useMisalignedLoads = fast
		for _, n := range lengths {
			for edge := 0; edge < 2; edge++ {
				start := 4096
				if edge == 1 {
					start = 8192 - n
				}
				msg := mem[start : start+n]
				var key [32]byte
				for i := range key {
					key[i] = byte(i*19 + 7)
				}
				var init macState
				initialize(&key, &init)
				want := init
				updateGeneric(&want, msg)
				var tagWant [16]byte
				finalize(&tagWant, &want.h, &want.s)
				for _, v := range polyVariants {
					s := init
					v.f(&s, msg)
					var tag [16]byte
					finalize(&tag, &s.h, &s.s)
					if tag != tagWant {
						t.Fatalf("guard tag %s n=%d edge=%d", v.name, n, edge)
					}
					checks++
				}
			}
		}
	}
	t.Logf("PASS %d read-only/guard-page cases", checks)
}
