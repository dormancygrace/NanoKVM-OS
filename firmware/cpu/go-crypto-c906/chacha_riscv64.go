// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

package chacha20

// bufSize is five blocks: xorKeyStreamC906 computes four blocks in
// XTheadVector lanes and a fifth one in scalar registers per iteration.
const bufSize = 5 * blockSize

// xorKeyStreamC906 needs len(dst) == len(src), a multiple of bufSize, and
// must only be called when useC906 is true.
//
//go:noescape
func xorKeyStreamC906(dst, src []byte, key *[8]uint32, nonce *[3]uint32, counter *uint32)

func (c *Cipher) xorKeyStreamBlocks(dst, src []byte) {
	if !useC906 {
		c.xorKeyStreamBlocksGeneric(dst, src)
		return
	}
	if len(dst) != len(src) || len(src)%bufSize != 0 {
		panic("chacha20: internal error: wrong dst and/or src length")
	}
	xorKeyStreamC906(dst, src, &c.key, &c.nonce, &c.counter)
}
