// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by a BSD-style license matching Go.

package runtime

// nanokvmFastMisaligned lets memmove_riscv64.s copy buffers whose source and
// destination alignments differ with 8-byte loads instead of byte loads. It
// is set by cpuinit from riscv_hwprobe (internal/cpu HasFastMisaligned).
var nanokvmFastMisaligned bool

// nanokvmVectorMemmove sends copies of at least nanokvmVectorMemmoveMin bytes
// to nanokvmMemmoveVector (nanokvm_memmove_riscv64.s, XTheadVector loads and
// stores). cpuinit sets it where riscv_hwprobe reports XTheadVector and the
// kernel enables vector state on first use (nanokvmVectorEnabled).
// GODEBUG=cpu.xtheadvector=off keeps memmove scalar.
var nanokvmVectorMemmove bool

// nanokvmVectorMemmoveMin is the smallest copy for the vector loop; below it
// the scalar loops are as fast (memmove_riscv64.s reads it from go_asm.h).
const nanokvmVectorMemmoveMin = 64
