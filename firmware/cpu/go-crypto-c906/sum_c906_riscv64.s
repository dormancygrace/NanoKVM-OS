// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

#define LOAD64U(base, offset, t0, t1, t2, t3, dst) \
	MOVBU	(offset+0*1)(base), t0; \
	MOVBU	(offset+1*1)(base), t1; \
	MOVBU	(offset+2*1)(base), t2; \
	MOVBU	(offset+3*1)(base), t3; \
	SLL	$8, t1; \
	SLL	$16, t2; \
	SLL	$24, t3; \
	OR	t1, t0; \
	OR	t3, t2; \
	OR	t2, t0, dst; \
	MOVBU	(offset+4*1)(base), t0; \
	MOVBU	(offset+5*1)(base), t1; \
	MOVBU	(offset+6*1)(base), t2; \
	MOVBU	(offset+7*1)(base), t3; \
	SLL	$32, t0; \
	SLL	$40, t1; \
	SLL	$48, t2; \
	SLL	$56, t3; \
	OR	t1, t0; \
	OR	t3, t2; \
	OR	t2, t0; \
	OR	t0, dst

// func update(state *macState, msg []byte)
TEXT ·update(SB), $0-32
	MOV	state+0(FP), X5
	MOV	msg_base+8(FP), X6
	MOV	msg_len+16(FP), X7

	MOV	$16, X8
	BEQZ	X7, return_empty

	AND	$7, X6, X28
	MOVBU	·useMisalignedLoads(SB), X29
	BEQZ	X29, alignment_checked
	MOV	$0, X28		// NanoKVM: no byte loads

alignment_checked:

	MOV	(0*8)(X5), X9		// h0
	MOV	(1*8)(X5), X10		// h1
	MOV	(2*8)(X5), X11		// h2
	MOV	(3*8)(X5), X12		// r0
	MOV	(4*8)(X5), X13		// r1

	SRL	$2, X13, X24
	SLL	$2, X24, X25
	ADD	X25, X24

	BLT	X7, X8, tail

loop:
	BEQZ	X28, aligned_load

	LOAD64U(X6, 0*8, X16, X18, X19, X20, X15)	// msg[0:8]
	LOAD64U(X6, 1*8, X16, X18, X19, X20, X17)	// msg[8:16]
	JMP	block

aligned_load:
	MOV	(0*8)(X6), X15		// msg[0:8]
	MOV	(1*8)(X6), X17		// msg[8:16]

block:
	ADD	X15, X9		// h0 (x1 + y1 = z1', if z1' < x1 then z1' overflow)
	SLTU	X15, X9, X19	// h0.carry
	ADD	X17, X10, X22
	SLTU	X17, X22, X23
	ADD	X22, X19, X10	// h1
	SLTU	X22, X10, X19
	OR	X23, X19	// h1.carry
	ADD	$1, X19
	ADD	X19, X11	// h2

	ADD	$16, X6		// msg = msg[16:]

multiply:
	// With B=2^64 and p=4*B^2-5, clamping gives r1=4*q.
	// Fold the h1*r1*B^2 and h2*r1*B^3 terms before carrying:
	//   s1=5*q; L=h0*r0+h1*s1; M=h0*r1+h1*r0+h2*s1.
	// The remaining high word is below 9*2^60+2, so its final
	// 5*(high>>2) reduction fits in uint64. The output remains <2*p.
	// The dependency-preserving order below uses the C906's measured
	// MUL/MULHU issue occupancy4 and result latency6.
	MUL	X9, X12, X15
	MUL	X10, X24, X14
	MULHU	X9, X12, X16
	MULHU	X10, X24, X17
	ADD	X14, X15
	SLTU	X14, X15, X19
	MUL	X9, X13, X14
	MUL	X10, X12, X20
	ADD	X17, X16
	MULHU	X9, X13, X17
	MULHU	X10, X12, X21
	MUL	X11, X24, X22
	MUL	X11, X12, X23
	ADD	X19, X16
	ADD	X14, X20
	SLTU	X14, X20, X19
	ADD	X17, X21
	ADD	X19, X21
	ADD	X22, X20
	SLTU	X22, X20, X19
	ADD	X19, X21
	ADD	X16, X20, X10
	ADD	X23, X21
	SLTU	X16, X10, X19
	ADD	X19, X21
	SRL	$2, X21, X20
	SLL	$2, X20, X22
	ADD	X20, X22
	ADD	X22, X15, X9
	SLTU	X22, X9, X19
	ADD	X19, X10
	AND	$3, X21, X11
	SLTU	X19, X10, X19
	ADD	X19, X11

	SUB	$16, X7, X7
	BGE	X7, X8, loop

tail:
	BEQ	X7, X0, done
	MOV	$1, X15
	MOV $0, X16
	ADD	X7, X6, X6

flush_buffer:
	MOVBU	-1(X6), X20
	SRL	$56, X15, X19
	SLL	$8, X16, X23
	SLL	$8, X15, X15
	OR	X19, X23, X16
	XOR	X20, X15
	SUB	$1, X7, X7
	SUB	$1, X6, X6
	BNE	X7, X0, flush_buffer

	ADD	X15, X9
	SLTU	X15, X9, X19
	ADD	X16, X10, X22
	SLTU	X16, X22, X23
	ADD	X22, X19, X10
	SLTU	X22, X10, X19
	OR	X23, X19
	ADD	X19, X11

	MOV	$16, X7
	JMP	multiply

done:
	MOV	X9, (0*8)(X5)	// h0
	MOV	X10, (1*8)(X5)
	MOV	X11, (2*8)(X5)
return_empty:
	RET

// Keep the qualified Go 1.27.3 reference function extent (506 bytes).
// This padding is unreachable; it preserves surrounding code placement.
// Go assembler relaxation makes this a toolchain-specific measurement contract.
// After changing Go or this routine, rederive both candidate/reference extents
// and requalify matched whole-binary code placement; do not keep 40 bytes by rote.
// tests/test-c906-poly1305.py checks both 506-byte symbols in the pinned build.
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
	WORD	$0x00000013
