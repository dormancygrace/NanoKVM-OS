// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stringslite

// NanoKVM: the string form of bytes.indexHorspool (index_nanokvm.go in
// package bytes), used where Index would fall back to Rabin-Karp; strings.Index
// is this Index.

// horspoolMaxLen bounds substr for indexHorspool: its shifts are bytes.
const horspoolMaxLen = 255

// indexHorspool returns the index of the first instance of substr in s, or
// -1. It requires 2 <= len(substr) <= horspoolMaxLen.
func indexHorspool(s, substr string) int {
	n := len(substr)
	var shift [256]uint8
	for i := range shift {
		shift[i] = uint8(n)
	}
	for i := 0; i < n-1; i++ {
		shift[substr[i]] = uint8(n - 1 - i)
	}
	last := substr[n-1]
	head := substr[:n-1]
	for i := 0; i <= len(s)-n; {
		c := s[i+n-1]
		if c == last && s[i:i+n-1] == head {
			return i
		}
		i += int(shift[c])
	}
	return -1
}
