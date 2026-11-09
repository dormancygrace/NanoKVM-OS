// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego && nanokvmqemu

package poly1305

// QEMU has no XTheadVector: updateScalar only (sum_xtheadvector_riscv64.go).
const useVector = false
