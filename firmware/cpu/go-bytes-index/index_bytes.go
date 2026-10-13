// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bytes

// NanoKVM: Index falls back to Rabin-Karp when IndexByte keeps stopping at
// the first byte of sep. On riscv64 (bytealg.MaxLen is 0) that is every
// search whose first byte is frequent in s, such as the Annex-B start code
// 00 00 01 in coded video: Rabin-Karp then hashes every byte. Horspool
// looks at the last byte of each window and moves by up to len(sep) bytes,
// so for a start code it reads about a third of the bytes and hashes none.

// horspoolMaxLen bounds sep for indexHorspool: its shifts are bytes.
const horspoolMaxLen = 255

// indexHorspool returns the index of the first instance of sep in s, or -1.
// It requires 2 <= len(sep) <= horspoolMaxLen. Boyer-Moore-Horspool: the
// window is compared when its last byte matches, then moves by the distance
// from that byte's last occurrence in sep[:n-1] to the end of sep, which
// never skips a match.
func indexHorspool(s, sep []byte) int {
	n := len(sep)
	var shift [256]uint8
	for i := range shift {
		shift[i] = uint8(n)
	}
	for i, c := range sep[:n-1] {
		shift[c] = uint8(n - 1 - i)
	}
	last := sep[n-1]
	head := sep[:n-1]
	for i := 0; i <= len(s)-n; {
		c := s[i+n-1]
		if c == last && Equal(s[i:i+n-1], head) {
			return i
		}
		i += int(shift[c])
	}
	return -1
}
