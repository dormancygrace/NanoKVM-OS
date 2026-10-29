// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by a BSD-style license matching Go.

package runtime

// nanokvmVectorMemclr sends clears of at least nanokvmVectorMemclrMin bytes
// to nanokvmMemclrVector (nanokvm_memclr_riscv64.s, XTheadVector stores).
// cpuinit sets it under the same conditions as nanokvmVectorMemmove;
// GODEBUG=cpu.xtheadvector=off keeps memclrNoHeapPointers scalar.
var nanokvmVectorMemclr bool

// nanokvmVectorMemclrMin is the smallest clear for the vector loop
// (memclr_riscv64.s reads it from go_asm.h).
const nanokvmVectorMemclrMin = 64
