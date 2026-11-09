// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

package poly1305

import (
	"sync"
	"syscall"
	"unsafe"
)

// vectorGroup is what poly1305BlocksVector hashes per iteration: four blocks
// in XTheadVector lanes and a fifth in scalar registers.
const vectorGroup = 5 * TagSize

// vectorMinGroups is the shortest run of whole groups worth the vector code,
// which computes r^2..r^5 on every call; shorter runs are faster in
// updateScalar (measured on the C906).
const vectorMinGroups = 4

const mask26 = 1<<26 - 1

// poly1305BlocksVector hashes groups whole groups of msg into h; r and h are
// 26-bit limbs, h's below 2^27 on entry and carried on exit.
//
//go:noescape
func poly1305BlocksVector(r *[5]uint64, msg *byte, groups int, h *[5]uint64)

// updateScalar (sum_riscv64.s) is the CRYPTOGAMS update.
//
//go:noescape
func updateScalar(state *macState, msg []byte)

var (
	vectorOnce sync.Once
	useVector  bool
)

// vectorSelected reports, on first use, whether to use poly1305BlocksVector:
// riscv_hwprobe reports XTheadVector on every CPU and the kernel enables
// vector state for this thread (prctl PR_RISCV_V_GET_CONTROL; Go threads
// inherit it). NANOKVM_POLY1305=generic keeps the scalar code. The kernel
// also uses XTheadBa, XTheadBb and XTheadMac, which every T-Head core with
// XTheadVector implements.
func vectorSelected() bool {
	vectorOnce.Do(func() {
		if env, _ := syscall.Getenv("NANOKVM_POLY1305"); env == "generic" {
			return
		}
		useVector = hwprobeXTheadVector() && vectorEnabled()
	})
	return useVector
}

func hwprobeXTheadVector() bool {
	const (
		sysRISCVHwprobe       = 258
		keyVendorExtThead0    = 11
		vendorExtXTheadVector = 1 << 0
	)
	pair := struct {
		key   int64
		value uint64
	}{key: keyVendorExtThead0}
	_, _, errno := syscall.RawSyscall6(sysRISCVHwprobe, uintptr(unsafe.Pointer(&pair)), 1, 0, 0, 0, 0)
	return errno == 0 && pair.key == keyVendorExtThead0 && pair.value&vendorExtXTheadVector != 0
}

func vectorEnabled() bool {
	const (
		prRISCVVGetControl  = 70
		prRISCVVCtrlCurMask = 3
		prRISCVVCtrlOn      = 2
	)
	r, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prRISCVVGetControl, 0, 0)
	return errno == 0 && r&prRISCVVCtrlCurMask == prRISCVVCtrlOn
}

// update hashes msg into state: runs of at least vectorMinGroups whole groups
// in the vector code (which reads the blocks with misaligned scalar loads, so
// only where they are fast), the rest and everything elsewhere in updateScalar.
func update(state *macState, msg []byte) {
	if groups := len(msg) / vectorGroup; groups >= vectorMinGroups && state.h[2] < 8 && useMisalignedLoads && vectorSelected() {
		updateVector(state, msg[:groups*vectorGroup], groups)
		msg = msg[groups*vectorGroup:]
	}
	if len(msg) > 0 {
		updateScalar(state, msg)
	}
}

func updateVector(state *macState, msg []byte, groups int) {
	r0, r1 := state.r[0], state.r[1]
	r := [5]uint64{r0 & mask26, r0 >> 26 & mask26, (r0>>52 | r1<<12) & mask26, r1 >> 14 & mask26, r1 >> 40}
	// h < 2^131 (h2 < 8): the top limb is below 2^27.
	h0, h1, h2 := state.h[0], state.h[1], state.h[2]
	h := [5]uint64{h0 & mask26, h0 >> 26 & mask26, (h0>>52 | h1<<12) & mask26, h1 >> 14 & mask26, h1>>40 | h2<<24}
	poly1305BlocksVector(&r, &msg[0], groups, &h)
	// Carried limbs: h[1] may exceed 2^26 by a little. Carry it up; h[4] < 2^27
	// keeps h2 <= 4.
	h[2] += h[1] >> 26
	h[1] &= mask26
	h[3] += h[2] >> 26
	h[2] &= mask26
	h[4] += h[3] >> 26
	h[3] &= mask26
	state.h[0] = h[0] | h[1]<<26 | h[2]<<52
	state.h[1] = h[2]>>12 | h[3]<<14 | h[4]<<40
	state.h[2] = h[4] >> 24
}
