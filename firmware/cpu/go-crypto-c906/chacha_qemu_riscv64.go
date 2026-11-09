// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego && nanokvmqemu

package chacha20

// QEMU has no XTheadVector: the generic code (chacha_xtheadvector_riscv64.go).
const useC906 = false
