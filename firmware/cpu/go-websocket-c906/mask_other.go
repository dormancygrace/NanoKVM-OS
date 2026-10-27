// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by the BSD-style LICENSE in this directory.

//go:build !appengine && (!linux || !riscv64 || !gc || purego)

package websocket

func maskBytes(key [4]byte, pos int, b []byte) int {
	return maskBytesGeneric(key, pos, b)
}
