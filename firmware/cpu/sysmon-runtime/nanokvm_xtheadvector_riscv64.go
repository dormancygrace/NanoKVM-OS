// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by a BSD-style license matching Go.

//go:build !nanokvmqemu

package runtime

// NanoKVM OS runs only on the SG2002 (T-Head C906, XTheadVector), and its
// kernel enables vector state for every user thread. Defining this constant
// makes memmove and memclrNoHeapPointers use the XTheadVector loops
// (go_asm.h: #ifdef const_nanokvmXTheadVector). The nanokvmqemu build tag
// leaves it undefined, for QEMU, which has no XTheadVector.
const nanokvmXTheadVector = 1
