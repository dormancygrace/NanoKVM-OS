// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

package poly1305

// poly1305BlocksFast and poly1305BlocksAligned (sum_riscv64.s) are CRYPTOGAMS
// poly1305_blocks. Only updateScalar calls them, with the context, message, length
// and padbit in a0-a3.
func poly1305BlocksFast()
func poly1305BlocksAligned()
