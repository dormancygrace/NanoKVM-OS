// SPDX-License-Identifier: MIT

//go:build riscv64

package sctp

// The packet checksum on the T-Head C906 (NanoKVM runs on nothing else of
// this architecture). hash/crc32 has no assembly for riscv64, and its
// slicing-by-8 in Go costs 12.2 ns for a byte there; this loop costs less
// (NANOKVM-PATCHES.md has the number).

// crc32cTables[k][b] is the checksum state after byte b followed by k zero bytes.
var crc32cTables = func() (t [8][256]uint32) { //nolint:gochecknoglobals
	const castagnoliReversed = 0x82f63b78
	for i := range 256 {
		c := uint32(i)
		for range 8 {
			c = c>>1 ^ castagnoliReversed&-(c&1)
		}
		t[0][i] = c
	}
	for i := range 256 {
		c := t[0][i]
		for k := 1; k < 8; k++ {
			c = t[0][c&0xff] ^ c>>8
			t[k][i] = c
		}
	}

	return t
}()

//go:noescape
func crc32cWords(crc uint32, p *byte, words int, tables *[8][256]uint32) uint32

// crc32cUpdate is crc32.Update(crc, crc32.MakeTable(crc32.Castagnoli), p).
func crc32cUpdate(crc uint32, p []byte) uint32 {
	crc = ^crc
	if words := len(p) / 8; words > 0 {
		crc = crc32cWords(crc, &p[0], words, &crc32cTables)
		p = p[words*8:]
	}
	for _, b := range p {
		crc = crc32cTables[0][byte(crc)^b] ^ crc>>8
	}

	return ^crc
}
