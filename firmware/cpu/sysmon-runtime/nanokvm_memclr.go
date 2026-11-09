// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by a BSD-style license matching Go.

package runtime

// nanokvmVectorMemclrMin is the smallest clear for nanokvmMemclrVector
// (nanokvm_memclr_riscv64.s, XTheadVector stores; memclr_riscv64.s reads it
// from go_asm.h).
const nanokvmVectorMemclrMin = 64
