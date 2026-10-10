// Copyright 2026 NanoKVM Enhanced contributors.
// Use of this source code is governed by a BSD-style license matching Go.

//go:build !linux || !riscv64

package runtime

func nanokvmSysmonInit() {}
func nanokvmThreadInit() {}

//go:nosplit
func nanokvmSysmonDelay(delay uint32) uint32 { return delay }
