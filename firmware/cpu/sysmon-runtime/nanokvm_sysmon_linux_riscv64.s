// Copyright 2026 NanoKVM Enhanced contributors.
// Use of this source code is governed by a BSD-style license matching Go.
#include "textflag.h"

// Linux RISC-V: __NR_prctl=167; only timer-slack options 29/30 are used.
TEXT runtime·nanokvmPrctl(SB),NOSPLIT|NOFRAME,$0-24
	MOV option+0(FP), A0
	MOV value+8(FP), A1
	MOV ZERO, A2
	MOV ZERO, A3
	MOV ZERO, A4
	MOV $167, A7
	ECALL
	MOV A0, ret+16(FP)
	RET
