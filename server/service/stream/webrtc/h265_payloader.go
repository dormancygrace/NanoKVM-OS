package webrtc

import (
	"bytes"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// h265Payloader retains decoder configuration for the lifetime of one peer.
// SmartP can emit CRA without repeating VPS/SPS/PPS. Chromium rejects such an
// IRAP access unit even if it decoded those parameter sets earlier.
// Pion's own parameter-set cache is consumed after the next emitted NAL, so it
// cannot supply the configuration again at a later CRA.
type h265Payloader struct {
	payloader     codecs.H265Payloader
	parameterSets [3][]byte
}

func (p *h265Payloader) Payload(mtu uint16, payload []byte) [][]byte {
	var present [3]bool
	irap := false
	ownedH264EmitNalus(payload, func(nalu []byte) {
		if len(nalu) < 2 {
			return
		}
		typ := (nalu[0] >> 1) & 63
		if typ >= 32 && typ <= 34 {
			i := typ - 32
			present[i] = true
			if !bytes.Equal(p.parameterSets[i], nalu) {
				p.parameterSets[i] = bytes.Clone(nalu)
			}
		}
		irap = irap || (typ >= 16 && typ <= 23)
	})
	if irap && (!present[0] || !present[1] || !present[2]) {
		size := len(payload)
		complete := true
		for _, nalu := range p.parameterSets {
			complete = complete && len(nalu) != 0
			size += 4 + len(nalu)
		}
		if complete {
			configured := make([]byte, 0, size+4)
			for _, nalu := range p.parameterSets {
				configured = append(configured, 0, 0, 0, 1)
				configured = append(configured, nalu...)
			}
			// Replace a partial in-band set with the complete latest chain.
			// Keep other NALs byte-exact and in their original order.
			ownedH264EmitNalus(payload, func(nalu []byte) {
				if len(nalu) >= 2 {
					typ := (nalu[0] >> 1) & 63
					if typ >= 32 && typ <= 34 {
						return
					}
				}
				configured = append(configured, 0, 0, 0, 1)
				configured = append(configured, nalu...)
			})
			payload = configured
		}
	}
	return p.payloader.Payload(mtu, payload)
}

var _ rtp.Payloader = (*h265Payloader)(nil)
