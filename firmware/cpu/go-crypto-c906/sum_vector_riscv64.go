// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

package poly1305

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

// update hashes msg into state: runs of at least vectorMinGroups whole groups
// in the vector code (useVector, sum_xtheadvector_riscv64.go), the rest in
// updateScalar.
func update(state *macState, msg []byte) {
	if groups := len(msg) / vectorGroup; useVector && groups >= vectorMinGroups && state.h[2] < 8 {
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
