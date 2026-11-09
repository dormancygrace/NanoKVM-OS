// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by the BSD-style LICENSE in this directory.

//go:build gc && !purego && !appengine

package websocket

// This firmware runs only on the SG2002/C906: XTheadVector, fast native
// misaligned words, and a kernel that enables vector state for every user
// thread. maskBytesC906 relies on that platform contract.

func maskBytes(key [4]byte, pos int, b []byte) int {
	// Keep tiny messages in Go: the assembly ABI call costs more than this loop.
	if len(b) < 8 {
		for i := range b {
			b[i] ^= key[pos&3]
			pos++
		}
		return pos & 3
	}
	return maskBytesC906(key, pos, b)
}

// maskBytesC906 consumes exactly b and returns (pos+len(b))&3.
//
//go:noescape
func maskBytesC906(key [4]byte, pos int, b []byte) int
