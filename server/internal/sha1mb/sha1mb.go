// Package sha1mb computes HMAC-SHA1 tags of many messages under one key,
// four at a time in XTheadVector lanes on the SG2002's C906 core (see
// sha1mb_riscv64.s). It serves SRTP's AES128_CM_HMAC_SHA1_80 profile, which
// authenticates every packet of a video frame with the session auth key.
// Elsewhere, or with NANOKVM_SHA1MB=generic, it uses crypto/sha1.
package sha1mb

import (
	"crypto/sha1"
	"encoding"
	"encoding/binary"
	"math/bits"
	"sync"
)

// Size is the size of an HMAC-SHA1 tag.
const Size = sha1.Size

const blockSize = sha1.BlockSize

// Message is hashed as the concatenation of its parts, any of which may be
// empty or nil, for example an SRTP header, its payload and the 4-byte ROC.
// Parts are read in place; only the bytes of blocks that straddle parts and
// the final padded block are copied.
type Message [3][]byte

// Key holds the HMAC-SHA1 inner and outer states of one key. It is
// immutable, so one Key may be used by any number of goroutines.
type Key struct {
	states [2][5]uint32 // ipad and opad chaining values for the kernel
	inner  []byte       // the same states marshaled by crypto/sha1
	outer  []byte
}

// NewKey precomputes the HMAC-SHA1 states for key, which may have any
// length (keys longer than 64 bytes are hashed first, as in crypto/hmac).
func NewKey(key []byte) *Key {
	if len(key) > blockSize {
		h := sha1.Sum(key)
		key = h[:]
	}
	k := new(Key)
	var ipad, opad [blockSize]byte
	copy(ipad[:], key)
	copy(opad[:], key)
	for i := range ipad {
		ipad[i] ^= 0x36
		opad[i] ^= 0x5c
	}
	k.states[0] = initState
	block(&k.states[0], &ipad)
	k.states[1] = initState
	block(&k.states[1], &opad)
	k.inner = marshal(ipad[:])
	k.outer = marshal(opad[:])
	return k
}

func marshal(pad []byte) []byte {
	h := sha1.New()
	h.Write(pad)
	b, err := h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		panic("sha1mb: " + err.Error())
	}
	return b
}

// SumBatch sets tags[i] to the HMAC-SHA1 of msgs[i] (its parts
// concatenated) under k. It panics if len(tags) != len(msgs). Callers keep
// the first 10 bytes for the SRTP 80-bit tag.
func (k *Key) SumBatch(tags [][Size]byte, msgs []Message) {
	if len(tags) != len(msgs) {
		panic("sha1mb: len(tags) != len(msgs)")
	}
	for len(msgs) > 0 {
		n := min(len(msgs), maxBatch)
		if n < minVector || !vector() {
			k.sumGeneric(tags[:n], msgs[:n])
		} else {
			k.sumLanes(tags[:n], msgs[:n], kernel)
		}
		tags, msgs = tags[n:], msgs[n:]
	}
}

const (
	// maxBatch bounds the work buffers and keeps all offsets in uint32.
	maxBatch = 256
	// minVector is the smallest batch for the vector kernel: with one
	// message, three of its four lanes would idle.
	minVector = 2
)

func (k *Key) sumGeneric(tags [][Size]byte, msgs []Message) {
	h := sha1.New()
	u := h.(encoding.BinaryUnmarshaler)
	var inner [Size]byte
	for i, m := range msgs {
		if err := u.UnmarshalBinary(k.inner); err != nil {
			panic("sha1mb: " + err.Error())
		}
		h.Write(m[0])
		h.Write(m[1])
		h.Write(m[2])
		h.Sum(inner[:0])
		if err := u.UnmarshalBinary(k.outer); err != nil {
			panic("sha1mb: " + err.Error())
		}
		h.Write(inner[:])
		h.Sum(tags[i][:0])
	}
}

// seg is a run of n consecutive 64-byte blocks of one lane, read from
// msgs[part/3][part%3][off:] or, for part == partScratch, scratch[off:].
// Keep in sync with gen_sha1mb_riscv64.py.
type seg struct {
	part, off, n uint32
	flags        uint32 // flagInner, flagOuter, flagTag
	tag          uint32 // byte offset of the tag in tags if flagTag
	_            uint32
}

const (
	partScratch = ^uint32(0)
	// flagInner: the segment starts a message; reset the lane to the ipad state.
	flagInner = 1
	// flagOuter: the segment is an outer block; reset the lane to the opad
	// state, with the lane's previous chaining value (the inner hash) as
	// message words 0-4.
	flagOuter = 2
	// flagTag: after the last block, write the chaining value big-endian
	// to tags[tag:].
	flagTag = 4
)

// Scratch layout: a zero block for idle lanes, the outer block template,
// then the copied blocks of the inner messages.
const (
	zeroOff  = 0
	outerOff = blockSize
	copyOff  = 2 * blockSize
)

// maxSegs bounds the segments of one message: a run of whole blocks per
// part, a copied gap before each run and at the end, and the outer block.
const maxSegs = 3 + 4 + 1

type work struct {
	segs    []seg
	scratch []byte
	lane    []uint8
	lanes   [4][2]uint32
}

var workPool = sync.Pool{New: func() any { return new(work) }}

// kernelFunc processes nsteps steps of the four lane queues (see
// sha1mb_riscv64.s); kernelModel in the tests is a Go version.
type kernelFunc func(msgs *Message, scratch *byte, segs *seg, lanes *[4][2]uint32, init *[2][5]uint32, tags *[Size]byte, nsteps int)

// sumLanes schedules the messages onto four lanes, inner blocks followed by
// the outer block on the same lane, and runs kern over all of them at once.
func (k *Key) sumLanes(tags [][Size]byte, msgs []Message, kern kernelFunc) {
	w := workPool.Get().(*work)
	defer workPool.Put(w)

	// Assign each message to the least loaded lane, in order. A message
	// takes its inner blocks plus one outer block.
	if cap(w.lane) < len(msgs) {
		w.lane = make([]uint8, len(msgs))
	}
	lane := w.lane[:len(msgs)]
	var load, count [4]int
	copied := 0 // blocks copied to scratch, at most
	for i := range msgs {
		m := &msgs[i]
		n := (len(m[0]) + len(m[1]) + len(m[2]) + 8 + blockSize) / blockSize
		j := 0
		for l := 1; l < 4; l++ {
			if load[l] < load[j] {
				j = l
			}
		}
		lane[i] = uint8(j)
		load[j] += n + 1
		count[j]++
		// A copied block holds a part boundary or padding: at most two
		// boundaries and two padding blocks.
		copied += min(n, 4)
	}
	nsteps := max(load[0], load[1], load[2], load[3])

	// Lane j's segments go to its own region of segs.
	if cap(w.segs) < maxSegs*len(msgs) {
		w.segs = make([]seg, maxSegs*len(msgs))
	}
	var first, next [4]int
	for j, c := 0, 0; j < 4; j++ {
		first[j], next[j] = c, c
		c += maxSegs * count[j]
	}
	need := copyOff + blockSize*copied
	if cap(w.scratch) < need {
		w.scratch = make([]byte, need, need+need/4)
	}
	b := builder{segs: w.segs[:maxSegs*len(msgs)], scratch: w.scratch[:need], used: copyOff}
	clear(b.scratch[:copyOff])
	b.scratch[outerOff+Size] = 0x80
	binary.BigEndian.PutUint64(b.scratch[outerOff+blockSize-8:], (blockSize+Size)*8)
	for i := range msgs {
		j := lane[i]
		next[j] = b.add(next[j], i, &msgs[i])
	}
	for j := range 4 {
		w.lanes[j] = [2]uint32{uint32(first[j]), uint32(next[j])}
	}

	kern(&msgs[0], &b.scratch[0], &b.segs[0], &w.lanes, &k.states, &tags[0], nsteps)
}

type builder struct {
	segs    []seg
	scratch []byte
	used    int // bytes of scratch in use
}

// add writes the segments of message i to segs[c:] and returns the index
// after them: runs of whole blocks inside one part are read in place, the
// other blocks are copied to scratch; then the outer block.
func (b *builder) add(c, i int, m *Message) int {
	length := len(m[0]) + len(m[1]) + len(m[2])
	n := (length + 8 + blockSize) / blockSize
	flags := uint32(flagInner)
	done := 0 // blocks in segments so far
	start := 0
	for p := range 3 {
		end := start + len(m[p])
		if first, last := (start+blockSize-1)/blockSize, end/blockSize; first < last {
			if done < first {
				b.segs[c] = b.copy(m, length, n, done, first, flags)
				c++
				flags = 0
			}
			b.segs[c] = seg{part: uint32(3*i + p), off: uint32(first*blockSize - start),
				n: uint32(last - first), flags: flags}
			c++
			flags = 0
			done = last
		}
		start = end
	}
	b.segs[c] = b.copy(m, length, n, done, n, flags) // at least the padding block
	b.segs[c+1] = seg{part: partScratch, off: outerOff, n: 1, flags: flagOuter | flagTag, tag: uint32(Size * i)}
	return c + 2
}

// copy copies blocks [from, to) of the padded inner message of n blocks to
// scratch and returns their segment.
func (b *builder) copy(m *Message, length, n, from, to int, flags uint32) seg {
	off := b.used
	dst := b.scratch[off : off+(to-from)*blockSize]
	b.used += len(dst)
	pos := from * blockSize // message offset of dst[0]
	start := 0
	for p := range 3 {
		part := m[p]
		if s := max(start, pos); s < start+len(part) && s < pos+len(dst) {
			copy(dst[s-pos:], part[s-start:])
		}
		start += len(part)
	}
	if p := length - pos; p < len(dst) {
		if p >= 0 {
			dst[p] = 0x80
			p++
		} else {
			p = 0
		}
		clear(dst[p:])
	}
	if to == n {
		binary.BigEndian.PutUint64(dst[len(dst)-8:], uint64(blockSize+length)*8)
	}
	return seg{part: partScratch, off: uint32(off), n: uint32(to - from), flags: flags}
}

var initState = [5]uint32{0x67452301, 0xEFCDAB89, 0x98BADCFE, 0x10325476, 0xC3D2E1F0}

// block is the SHA-1 compression function (FIPS 180-4), used to set up keys.
func block(h *[5]uint32, p *[blockSize]byte) {
	var w [80]uint32
	for t := range 16 {
		w[t] = binary.BigEndian.Uint32(p[4*t:])
	}
	for t := 16; t < 80; t++ {
		w[t] = bits.RotateLeft32(w[t-3]^w[t-8]^w[t-14]^w[t-16], 1)
	}
	a, b, c, d, e := h[0], h[1], h[2], h[3], h[4]
	for t := range 80 {
		var f, k uint32
		switch {
		case t < 20:
			f, k = d^(b&(c^d)), 0x5A827999
		case t < 40:
			f, k = b^c^d, 0x6ED9EBA1
		case t < 60:
			f, k = (b&c)|(b&d)|(c&d), 0x8F1BBCDC
		default:
			f, k = b^c^d, 0xCA62C1D6
		}
		a, b, c, d, e = bits.RotateLeft32(a, 5)+f+e+k+w[t], a, bits.RotateLeft32(b, 30), c, d
	}
	h[0] += a
	h[1] += b
	h[2] += c
	h[3] += d
	h[4] += e
}
