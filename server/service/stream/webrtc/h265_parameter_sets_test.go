package webrtc

import (
	"bytes"
	"encoding/binary"
	"testing"

	"NanoKVM-Server/service/stream"
	"github.com/pion/rtp"
)

// Chromium H26xPacketBuffer requires VPS/SPS/PPS in each IRAP access unit,
// including SmartP CRA frames. The native encoder may send them only at IDR.
func TestH265RepeatsParameterSetsAtSmartPCRA(t *testing.T) {
	p := newAdaptiveVideoPacketizer(stream.VideoCodecH265)
	sets := [][]byte{{32 << 1, 1, 0x10}, {33 << 1, 1, 0x20}, {34 << 1, 1, 0xc0}}
	first := append(appendH265AnnexB(nil, sets...), appendH265AnnexB(nil, []byte{19 << 1, 1, 0x80, 7})...)
	firstPackets := p.packetize(first, 0, 1216)
	wantH265NALs(t, firstPackets, append(sets, []byte{19 << 1, 1, 0x80, 7}))
	// Cached data must remain owned after capture buffers are reused.
	for i := range first {
		first[i] = 0xff
	}
	delta := []byte{1 << 1, 1, 0x80, 9}
	wantH265NALs(t, p.packetize(appendH265AnnexB(nil, delta), 16666, 1216), [][]byte{delta})
	cra := append([]byte{21 << 1, 1}, bytes.Repeat([]byte{0x5a}, 4000)...)
	packets := p.packetize(appendH265AnnexB(nil, cra), 500000, 1216)
	wantH265NALs(t, packets, append(sets, cra))
	// PMTU discovery must not discard the retained decoder configuration.
	resized := p.packetize(appendH265AnnexB(nil, cra), 1000000, 800)
	wantH265NALs(t, resized, append(sets, cra))
	for i, packet := range resized {
		if packet.MarshalSize() > 800 {
			t.Fatalf("packet %d exceeds new MTU: %d", i, packet.MarshalSize())
		}
		if packet.Marker != (i == len(resized)-1) {
			t.Fatalf("packet %d has wrong marker", i)
		}
		if i > 0 && packet.SequenceNumber != resized[i-1].SequenceNumber+1 {
			t.Fatal("sequence discontinuity within frame")
		}
	}
	if resized[0].SequenceNumber != packets[len(packets)-1].SequenceNumber+1 {
		t.Fatal("MTU change reset RTP sequence")
	}
	if resized[0].Timestamp-packets[0].Timestamp != 45000 {
		t.Fatal("MTU change reset RTP clock")
	}
}

func appendH265AnnexB(dst []byte, nalus ...[]byte) []byte {
	for _, nalu := range nalus {
		dst = append(dst, 0, 0, 0, 1)
		dst = append(dst, nalu...)
	}
	return dst
}

// Independent RFC7798 reconstruction checks complete NAL bytes, AP sizes and FU
// headers rather than comparing our packetizer with the same Pion payloader.
func wantH265NALs(t *testing.T, packets []*rtp.Packet, want [][]byte) {
	t.Helper()
	var got [][]byte
	var fragmented []byte
	for _, packet := range packets {
		b := packet.Payload
		if len(b) < 2 {
			t.Fatal("short H265 payload")
		}
		switch (b[0] >> 1) & 63 {
		case 48:
			for b = b[2:]; len(b) > 0; {
				if len(b) < 2 {
					t.Fatal("short AP size")
				}
				n := int(binary.BigEndian.Uint16(b))
				b = b[2:]
				if n < 2 || n > len(b) {
					t.Fatal("invalid AP NAL size")
				}
				got = append(got, bytes.Clone(b[:n]))
				b = b[n:]
			}
		case 49:
			if len(b) < 4 {
				t.Fatal("short FU")
			}
			if b[2]&0x80 != 0 {
				if fragmented != nil {
					t.Fatal("unfinished FU before new start")
				}
				fragmented = []byte{(b[0] & 0x81) | ((b[2] & 63) << 1), b[1]}
			} else if fragmented == nil {
				t.Fatal("FU without start")
			}
			fragmented = append(fragmented, b[3:]...)
			if b[2]&0x40 != 0 {
				got = append(got, fragmented)
				fragmented = nil
			}
		default:
			got = append(got, bytes.Clone(b))
		}
	}
	if fragmented != nil {
		t.Fatal("unterminated FU")
	}
	if len(got) != len(want) {
		t.Fatalf("NAL count = %d, want %d; types=%v", len(got), len(want), h265NALTypes(got))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("NAL %d mismatch: types=%v want=%v", i, h265NALTypes(got), h265NALTypes(want))
		}
	}
}

func h265NALTypes(nalus [][]byte) []byte {
	out := make([]byte, len(nalus))
	for i, nalu := range nalus {
		out[i] = (nalu[0] >> 1) & 63
	}
	return out
}

// An encoder update must replace retained configuration, including the partial
// parameter-set access units that precede a keyframe. Every HEVC IRAP type is
// independently accepted by Chromium only with the full in-band chain.
func TestH265ParameterSetsUpdateWithoutDuplicates(t *testing.T) {
	for typ := byte(16); typ <= 23; typ++ {
		p := &h265Payloader{}
		sets := [][]byte{{32 << 1, 1, 0x10}, {33 << 1, 1, 0x20}, {34 << 1, 1, 0xc0}}
		p.Payload(1204, appendH265AnnexB(nil, append(sets, []byte{19 << 1, 1, 0x80})...))
		updated := []byte{33 << 1, 1, 0x40}
		irap := []byte{typ << 1, 1, 0x80, 0x55}
		data := appendH265AnnexB(nil, updated, irap)
		payloads := p.Payload(1204, data)
		packets := make([]*rtp.Packet, len(payloads))
		for i, payload := range payloads {
			packets[i] = &rtp.Packet{Payload: payload}
		}
		wantH265NALs(t, packets, [][]byte{sets[0], updated, sets[2], irap})
		for i := range data {
			data[i] = 0xff
		}
		// A prefixless single NAL uses the updated, independently owned SPS too.
		payloads = p.Payload(1204, irap)
		packets = make([]*rtp.Packet, len(payloads))
		for i, payload := range payloads {
			packets[i] = &rtp.Packet{Payload: payload}
		}
		wantH265NALs(t, packets, [][]byte{sets[0], updated, sets[2], irap})
	}
}
