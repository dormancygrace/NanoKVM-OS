// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by a BSD-style license matching Go.

//go:build !nanokvmqemu

package bytealg

// As runtime's nanokvm_xtheadvector_riscv64.go: defining this constant makes
// memequal use the XTheadVector loop (nanokvm_memequal_riscv64.s) from
// nanokvmVectorMemequalMin bytes (go_asm.h: #ifdef const_nanokvmXTheadVector).
// The nanokvmqemu build tag leaves it undefined, for QEMU.
const nanokvmXTheadVector = 1

// nanokvmVectorMemequalMin is the smallest comparison for the vector loop;
// below it the scalar loops are as fast.
const nanokvmVectorMemequalMin = 64
