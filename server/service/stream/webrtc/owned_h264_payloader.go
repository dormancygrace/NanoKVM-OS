// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-FileCopyrightText: 2026 NanoKVM OS contributors
// SPDX-License-Identifier: MIT
//
// Annex-B splitting and wire behavior follow Pion RTP v1.10.5 H264Payloader
// (MIT, The Pion community). Payload storage is owned per access unit.
package webrtc

import (
	"bytes"
	"encoding/binary"

	"github.com/pion/rtp"
)

// ownedH264Payloader keeps the Pion H264 wire format while putting all emitted
// payloads in one fresh slab. No slab is recycled: an interceptor may keep any
// packet for a later retransmission. The caller may reuse or mutate its input
// after Payload returns, including an access unit containing only SPS/PPS.
type ownedH264Payloader struct {
	spsNalu, ppsNalu []byte
	DisableStapA     bool
}

type ownedH264PayloadPlan struct {
	nalu, sps, pps []byte
	count, size    int
	stap           bool
}

var ownedH264StartCode = []byte{0, 0, 1}

func ownedH264EmitNalus(nals []byte, emit func([]byte)) {
	start := bytes.Index(nals, ownedH264StartCode)
	offset := 3
	if start == -1 {
		emit(nals)
		return
	}
	for start < len(nals) {
		end := bytes.Index(nals[start+offset:], ownedH264StartCode)
		if end == -1 {
			emit(nals[start+offset:])
			break
		}
		nextStart := start + offset + end
		fourByte := nals[nextStart-1] == 0
		if fourByte {
			nextStart--
		}
		emit(nals[start+offset : nextStart])
		start = nextStart
		offset = 3
		if fourByte {
			offset = 4
		}
	}
}

func (p *ownedH264Payloader) Payload(mtu uint16, payload []byte) [][]byte {
	if len(payload) == 0 {
		return nil
	}
	// The usual access unit has few NALs even when it has many FU packets.
	// Planning per NAL avoids both a second Annex-B scan and per-FU allocation.
	var initialPlans [8]ownedH264PayloadPlan
	plans := initialPlans[:0]
	count, size := 0, 0
	sps, pps := p.spsNalu, p.ppsNalu
	spsChanged, ppsChanged := false, false
	add := func(plan ownedH264PayloadPlan) {
		plans = append(plans, plan)
		count += plan.count
		size += plan.size
	}
	ownedH264EmitNalus(payload, func(nalu []byte) {
		if len(nalu) == 0 {
			return
		}
		typ := nalu[0] & 0x1f
		switch {
		case typ == 9 || typ == 12: // AUD and filler never emit a packet.
			return
		case typ == 7:
			if !p.DisableStapA {
				sps, spsChanged = nalu, true
				return
			}
		case typ == 8:
			if !p.DisableStapA {
				pps, ppsChanged = nalu, true
				return
			}
		case !p.DisableStapA && sps != nil && pps != nil:
			stapSize := 5 + len(sps) + len(pps)
			if stapSize <= int(mtu) {
				add(ownedH264PayloadPlan{sps: sps, pps: pps, count: 1, size: stapSize, stap: true})
			}
			// Pion consumes the pair even when its STAP-A cannot fit the MTU.
			sps, pps = nil, nil
			spsChanged, ppsChanged = false, false
		}
		if len(nalu) <= int(mtu) {
			add(ownedH264PayloadPlan{nalu: nalu, count: 1, size: len(nalu)})
			return
		}
		fragmentSize := int(mtu) - 2
		bodySize := len(nalu) - 1
		if fragmentSize <= 0 || bodySize <= 0 {
			return
		}
		fragments := (bodySize + fragmentSize - 1) / fragmentSize
		add(ownedH264PayloadPlan{nalu: nalu, count: fragments, size: bodySize + 2*fragments})
	})
	// Only parameter sets surviving this call need separate retained ownership.
	if spsChanged {
		sps = bytes.Clone(sps)
	}
	if ppsChanged {
		pps = bytes.Clone(pps)
	}
	p.spsNalu, p.ppsNalu = sps, pps
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
		if plan.stap {
			out := next(plan.size)
			out[0] = 0x78
			binary.BigEndian.PutUint16(out[1:3], uint16(len(plan.sps)))
			copy(out[3:], plan.sps)
			index := 3 + len(plan.sps)
			binary.BigEndian.PutUint16(out[index:index+2], uint16(len(plan.pps)))
			copy(out[index+2:], plan.pps)
			continue
		}
		if plan.count == 1 && plan.size == len(plan.nalu) {
			copy(next(plan.size), plan.nalu)
			continue
		}
		nalu := plan.nalu
		body := nalu[1:]
		fragmentSize := int(mtu) - 2
		for index := 0; index < plan.count; index++ {
			length := min(fragmentSize, len(body))
			out := next(length + 2)
			out[0] = 28 | (nalu[0] & 0x60)
			out[1] = nalu[0] & 0x1f
			if index == 0 {
				out[1] |= 0x80
			} else if index == plan.count-1 {
				out[1] |= 0x40
			}
			copy(out[2:], body[:length])
			body = body[length:]
		}
	}
	return packets
}

var _ rtp.Payloader = (*ownedH264Payloader)(nil)
