// SPDX-License-Identifier: MIT

//go:build !riscv64

package sctp

import "hash/crc32"

// crc32cUpdate is the packet checksum where hash/crc32 has its own assembly.
func crc32cUpdate(crc uint32, p []byte) uint32 {
	return crc32.Update(crc, castagnoliTable, p)
}
