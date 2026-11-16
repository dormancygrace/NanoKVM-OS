// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-FileCopyrightText: 2026 NanoKVM OS contributors
// SPDX-License-Identifier: MIT
//
// Packetization rules follow Pion RTP v1.10.6 H265Payloader (MIT, The Pion
// community) for access units without parameter sets.
package webrtc

import "encoding/binary"

// H.265 NAL unit types that never become a packet of their own here.
const (
	h265NaluAP     = 48
	h265NaluFU     = 49
	h265NaluPACI   = 50
	h265NaluAUD    = 35
	h265NaluFiller = 38
	h265NaluVPS    = 32
	h265NaluPPS    = 34
)

type ownedH265Plan struct {
	nalu    []byte
	first   int // aggregation: first member in members
	count   int // aggregation: members; fragmentation: packets
	size    int
	kind    uint8 // 0 single NAL, 1 aggregation packet, 2 fragmentation units
	packets int
}

// ownedH265Payload is Pion's H265Payloader output for an access unit with no
// VPS, SPS or PPS while Pion's parameter-set cache is empty: single NAL
// packets, aggregation packets of consecutive small NALs and fragmentation
// units, with the same aggregation decisions. All packets share one fresh
// slab, so the access unit is copied once; Pion copies it, then serializes
// every packet into an allocation of its own. The slab is never recycled: an
// interceptor may keep any packet for a later retransmission.
func ownedH265Payload(mtu uint16, payload []byte) [][]byte {
	var memberStore [16][]byte
	members := memberStore[:0]
	var planStore [16]ownedH265Plan
	plans := planStore[:0]
	count, size := 0, 0
	add := func(plan ownedH265Plan) {
		plans = append(plans, plan)
		count += plan.packets
		size += plan.size
	}
	// The aggregation buffer: members[bufStart:bufStart+bufN], bufWire bytes.
	bufStart, bufN, bufWire := 0, 0, 0
	flush := func() {
		switch bufN {
		case 0:
			return
		case 1:
			nalu := members[bufStart]
			add(ownedH265Plan{nalu: nalu, size: len(nalu), packets: 1})
		default:
			total := 2
			for _, nalu := range members[bufStart : bufStart+bufN] {
				if len(nalu) > 0xffff { // Pion drops such an aggregation packet
					bufN, bufWire = 0, 0
					return
				}
				total += 2 + len(nalu)
			}
			add(ownedH265Plan{kind: 1, first: bufStart, count: bufN, size: total, packets: 1})
		}
		bufN, bufWire = 0, 0
	}
	single := func(nalu []byte) { add(ownedH265Plan{nalu: nalu, size: len(nalu), packets: 1}) }
	ownedH264EmitNalus(payload, func(nalu []byte) {
		if len(nalu) < 2 {
			return
		}
		switch (nalu[0] >> 1) & 63 {
		case h265NaluAP, h265NaluFU, h265NaluPACI, h265NaluAUD, h265NaluFiller:
			return
		}
		wire := len(nalu)
		switch {
		case wire > int(mtu):
			flush()
			slice := int(mtu) - 3
			body := len(nalu) - 2
			if slice <= 0 || body <= slice {
				return // Pion: errShortPacket, nothing sent
			}
			fragments := (body + slice - 1) / slice
			add(ownedH265Plan{kind: 2, nalu: nalu, count: fragments, size: body + 3*fragments, packets: fragments})
		case wire <= 2:
			flush()
			single(nalu)
		case bufN == 0:
			// canAggregateH265: room for the AP header, two sizes and another NAL.
			if wire+2*2+2+1 <= int(mtu) {
				members = append(members, nalu)
				bufStart, bufN, bufWire = len(members)-1, 1, wire
			} else {
				single(nalu)
			}
		default:
			// shouldAggregateH265Now: the AP would exceed the MTU with this NAL.
			if 2+(bufN+1)*2+bufWire+wire > int(mtu) {
				flush()
			}
			members = append(members, nalu)
			if bufN == 0 {
				bufStart = len(members) - 1
			}
			bufN++
			bufWire += wire
		}
	})
	flush()
	if count == 0 {
		return nil
	}

	slab := make([]byte, size)
	packets := make([][]byte, count)
	offset, packet := 0, 0
	next := func(length int) []byte {
		out := slab[offset : offset+length : offset+length]
		offset += length
		packets[packet] = out
		packet++
		return out
	}
	for _, plan := range plans {
		switch plan.kind {
		case 0:
			copy(next(plan.size), plan.nalu)
		case 1:
			out := next(plan.size)
			group := members[plan.first : plan.first+plan.count]
			f, layer, tid := false, uint16(0x3f), uint16(7)
			at := 2
			for _, nalu := range group {
				header := binary.BigEndian.Uint16(nalu)
				f = f || header&0x8000 != 0
				layer = min(layer, header>>3&0x3f)
				tid = min(tid, header&7)
				binary.BigEndian.PutUint16(out[at:], uint16(len(nalu)))
				at += 2 + copy(out[at+2:], nalu)
			}
			header := uint16(h265NaluAP)<<9 | layer<<3 | tid
			if f {
				header |= 0x8000
			}
			binary.BigEndian.PutUint16(out, header)
		case 2:
			nalu := plan.nalu
			header := binary.BigEndian.Uint16(nalu)&^0x7e00 | uint16(h265NaluFU)<<9
			typ := (nalu[0] >> 1) & 63
			body := nalu[2:]
			slice := int(mtu) - 3
			for index := 0; index < plan.count; index++ {
				length := min(slice, len(body))
				out := next(length + 3)
				binary.BigEndian.PutUint16(out, header)
				out[2] = typ
				if index == 0 {
					out[2] |= 0x80
				} else if index == plan.count-1 {
					out[2] |= 0x40
				}
				copy(out[3:], body[:length])
				body = body[length:]
			}
		}
	}
	return packets
}

// h265ParameterSets reports whether the access unit has a VPS, SPS or PPS,
// and whether Pion is left with parameter sets in its cache after it, given
// whether it had some before: it caches them until it sends the next NAL.
func h265ParameterSets(payload []byte, cached bool) (found, cachedAfter bool) {
	cachedAfter = cached
	ownedH264EmitNalus(payload, func(nalu []byte) {
		if len(nalu) < 2 {
			return
		}
		switch typ := (nalu[0] >> 1) & 63; {
		case typ >= h265NaluVPS && typ <= h265NaluPPS:
			found, cachedAfter = true, true
		case typ == h265NaluAP || typ == h265NaluFU || typ == h265NaluPACI || typ == h265NaluAUD || typ == h265NaluFiller:
		default:
			cachedAfter = false
		}
	})
	return found, cachedAfter
}
