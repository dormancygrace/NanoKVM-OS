// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !purego

package sha1

import (
	"crypto/internal/impl"
	"internal/cpu"
)

// On T-Head cores (XTheadBb, detected from mvendorid), blockC906 is the
// generated copy of blockRV64 with one-instruction rotates and byte reverses
// (sha1block_c906_riscv64.s); elsewhere blockRV64, Go's riscv64 block.
var useC906 = cpu.RISCV64.HasXTheadBb

func init() {
	impl.Register("sha1", "XTheadBb", &useC906)
}

//go:noescape
func blockRV64(dig *digest, p []byte)

//go:noescape
func blockC906(dig *digest, p []byte)

func block(dig *digest, p []byte) {
	if useC906 {
		blockC906(dig, p)
	} else {
		blockRV64(dig, p)
	}
}
