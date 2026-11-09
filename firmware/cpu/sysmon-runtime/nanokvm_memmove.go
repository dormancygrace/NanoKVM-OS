// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by a BSD-style license matching Go.

package runtime

// nanokvmVectorMemmoveMin is the smallest copy for nanokvmMemmoveVector
// (nanokvm_memmove_riscv64.s, XTheadVector loads and stores); below it the
// scalar loops are as fast (memmove_riscv64.s reads it from go_asm.h).
const nanokvmVectorMemmoveMin = 64
