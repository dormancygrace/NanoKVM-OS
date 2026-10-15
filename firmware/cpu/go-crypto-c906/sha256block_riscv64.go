// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !purego

package sha256

import (
	"crypto/internal/fips140deps/cpu"
	"crypto/internal/impl"
)

// On T-Head cores (XTheadBb, detected from mvendorid), blockC906 is the
// generated copy of blockRV64 with one-instruction rotates and byte reverses
// (sha256block_c906_riscv64.s); elsewhere blockRV64, Go's riscv64 block.
var useC906 = cpu.RISCV64HasXTheadBb

func init() {
	impl.Register("sha256", "XTheadBb", &useC906)
}

//go:noescape
func blockRV64(dig *Digest, p []byte)

//go:noescape
func blockC906(dig *Digest, p []byte)

func block(dig *Digest, p []byte) {
	if useC906 {
		blockC906(dig, p)
	} else {
		blockRV64(dig, p)
	}
}
