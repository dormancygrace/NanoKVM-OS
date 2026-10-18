// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by a BSD-style license matching Go.

package runtime

// nanokvmFastMisaligned lets memmove_riscv64.s copy buffers whose source and
// destination alignments differ with 8-byte loads instead of byte loads. It
// is set by cpuinit from riscv_hwprobe (internal/cpu HasFastMisaligned).
var nanokvmFastMisaligned bool
