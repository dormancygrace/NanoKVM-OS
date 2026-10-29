// Package annexb finds start codes in H.264 and H.265 Annex-B byte streams.
package annexb

import "bytes"

var startCode = []byte{0, 0, 1}

// vectorMin is the shortest input for the vector search; its setup costs
// more than it saves on fewer bytes.
const vectorMin = 64

// Index returns the index of the first start code 00 00 01 in b, or -1. A
// four-byte start code 00 00 00 01 is found one byte after its first zero.
//
// bytes.Index stops at every zero byte of coded video (one in about 170 on
// the stand's stream) and calls IndexByte again from there; on the C906 the
// vector search reads the whole buffer without stopping, about 3x faster.
func Index(b []byte) int {
	if len(b) >= vectorMin && vector() {
		return indexStartCode(b)
	}
	return bytes.Index(b, startCode)
}
