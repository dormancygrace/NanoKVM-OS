package sha1mb

import (
	"encoding/binary"
	"unsafe"
)

// modelBuffer is a staging buffer of kernelModel: the words of one step,
// transposed, and the per-lane metadata.
type modelBuffer struct {
	w     [16][4]uint32
	start [4]uint32
	fin   [4]unsafe.Pointer
	any   bool
}

func (b *modelBuffer) stage(j int, p []byte) {
	for t := range 16 {
		b.w[t][j] = binary.BigEndian.Uint32(p[4*t:])
	}
}

// kernelModel is sha1x4 in Go, step by step as the assembly does it: stage
// the next step's blocks, hash the current ones, then write tags and reset
// lanes. It lets the host tests check the segment queues built by sumLanes.
// Reads from message parts are bounds-checked.
func kernelModel(msgs *Message, scratch *byte, segs *seg, lanes *[4][2]uint32, init *[2][5]uint32, tags *[Size]byte, nsteps int) {
	type cursor struct {
		ptr                   []byte // rest of the current segment
		rem, next, end, flags uint32
		tag                   unsafe.Pointer
	}
	segAt := func(i uint32) *seg { return (*seg)(unsafe.Add(unsafe.Pointer(segs), unsafe.Sizeof(seg{})*uintptr(i))) }
	scratchAt := func(off uint32) unsafe.Pointer { return unsafe.Add(unsafe.Pointer(scratch), off) }
	zero := unsafe.Slice((*byte)(scratchAt(zeroOff)), blockSize)

	var cur [4]cursor
	for j := range cur {
		cur[j].next, cur[j].end = lanes[j][0], lanes[j][1]
	}
	advance := func(nxt *modelBuffer) {
		*nxt = modelBuffer{}
		for j := range cur {
			c := &cur[j]
			if c.rem == 0 {
				if c.next == c.end {
					nxt.stage(j, zero)
					continue
				}
				s := segAt(c.next)
				c.next++
				if s.n == 0 {
					panic("empty segment")
				}
				if s.part == partScratch {
					c.ptr = unsafe.Slice((*byte)(scratchAt(s.off)), int(s.n)*blockSize)
				} else {
					part := (*[]byte)(unsafe.Add(unsafe.Pointer(msgs), unsafe.Sizeof([]byte{})*uintptr(s.part)))
					c.ptr = (*part)[s.off : int(s.off)+int(s.n)*blockSize]
				}
				c.rem, c.flags, c.tag = s.n, s.flags, unsafe.Add(unsafe.Pointer(tags), s.tag)
				nxt.start[j] = s.flags & (flagInner | flagOuter)
			}
			var p []byte
			p, c.ptr = c.ptr[:blockSize], c.ptr[blockSize:]
			c.rem--
			if c.rem == 0 && c.flags&flagTag != 0 {
				nxt.fin[j] = c.tag
			}
			nxt.any = nxt.any || nxt.start[j] != 0 || nxt.fin[j] != nil
			nxt.stage(j, p)
		}
	}
	var h [5][4]uint32
	for i := range h {
		for j := range h[i] {
			h[i][j] = 0xdeadbeef
		}
	}
	fixup := func(cur, nxt *modelBuffer) {
		if !cur.any && !nxt.any {
			return
		}
		for j := range 4 {
			if p := cur.fin[j]; p != nil {
				for i := range 5 {
					binary.BigEndian.PutUint32(unsafe.Slice((*byte)(unsafe.Add(p, 4*i)), 4), h[i][j])
				}
			}
			switch nxt.start[j] {
			case flagInner:
				for i := range 5 {
					h[i][j] = init[0][i]
				}
			case flagOuter:
				for i := range 5 {
					nxt.w[i][j], h[i][j] = h[i][j], init[1][i]
				}
			}
		}
	}
	var bufs [2]modelBuffer
	c, n := &bufs[0], &bufs[1]
	advance(c)
	fixup(n, c)
	for range nsteps {
		advance(n)
		for j := range 4 {
			var st [5]uint32
			var p [blockSize]byte
			for t := range 16 {
				binary.BigEndian.PutUint32(p[4*t:], c.w[t][j])
			}
			for i := range 5 {
				st[i] = h[i][j]
			}
			block(&st, &p)
			for i := range 5 {
				h[i][j] = st[i]
			}
		}
		fixup(c, n)
		c, n = n, c
	}
}
