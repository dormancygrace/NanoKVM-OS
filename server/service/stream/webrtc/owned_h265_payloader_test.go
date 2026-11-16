package webrtc

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/pion/rtp/codecs"
)

// The one-slab path must send exactly what Pion sends, packet for packet.
func TestOwnedH265PayloadMatchesPion(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 9))
	// Slice, SEI and other NAL types; AUD, filler, AP and FU ones are dropped.
	types := []byte{0, 1, 2, 19, 20, 21, 39, 40, 35, 38, 48, 49, 50, 41}
	sizes := []int{0, 1, 2, 3, 8, 40, 300, 900, 1180, 1185, 1186, 1187, 1188, 1400, 2500, 9000, 70000}
	for trial := 0; trial < 3000; trial++ {
		mtu := uint16([]int{1188, 1200, 1388, 1400, 200, 64}[trial%6])
		var au []byte
		for n := 1 + r.IntN(6); n > 0; n-- {
			size := sizes[r.IntN(len(sizes))]
			if r.IntN(3) == 0 {
				size = r.IntN(3000)
			}
			nalu := make([]byte, 2+size)
			for i := range nalu {
				nalu[i] = byte(1 + r.IntN(255)) // no start codes inside
			}
			typ := types[r.IntN(len(types))]
			nalu[0] = typ<<1 | byte(r.IntN(2)) | byte(r.IntN(8))<<7&0x80
			nalu[1] = byte(r.IntN(32))<<3 | byte(1+r.IntN(7))
			if r.IntN(2) == 0 {
				au = append(au, 0, 0, 0, 1)
			} else {
				au = append(au, 0, 0, 1)
			}
			au = append(au, nalu...)
		}
		if found, _ := h265ParameterSets(au, false); found {
			continue
		}
		want := (&codecs.H265Payloader{}).Payload(mtu, bytes.Clone(au))
		got := ownedH265Payload(mtu, au)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("trial %d, MTU %d: %d packets, Pion %d", trial, mtu, len(got), len(want))
		}
	}
}
