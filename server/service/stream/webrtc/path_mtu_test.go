package webrtc

import (
	"NanoKVM-Server/service/stream"
	"testing"
)

func TestAdaptiveMTUPreservesSequenceAndCaptureClock(t *testing.T) {
	for _, codec := range []stream.VideoCodec{stream.VideoCodecH264, stream.VideoCodecH265} {
		packetizer := newAdaptiveVideoPacketizer(codec)
		data := append([]byte{0, 0, 0, 1, 0x65}, make([]byte, 5000)...)
		if codec == stream.VideoCodecH265 {
			data = append([]byte{0, 0, 0, 1, 19 << 1, 1}, make([]byte, 5000)...)
		}
		var origin uint32
		var next uint16
		// Preserve fractional 60 Hz intervals, dropped delivery time, a long
		// session and the 32-bit RTP-clock wrap while changing the path MTU.
		for index, sample := range []struct {
			micros int64
			mtu    uint16
		}{
			{0, 1216},
			{16666, 1456},
			{33333, 1152},
			{2033333, 1216},
			{3600000000, 1456},
			{47721859000, 1216},
		} {
			micros, mtu := sample.micros, sample.mtu
			packets := packetizer.packetize(data, micros, mtu)
			if len(packets) < 2 {
				t.Fatal("frame not fragmented")
			}
			if index == 0 {
				origin = packets[0].Timestamp
				next = packets[0].SequenceNumber
			}
			for i, p := range packets {
				if p.SequenceNumber != next {
					t.Fatal("MTU change reset sequence")
				}
				next++
				if p.Timestamp != origin+uint32(uint64(micros)*90/1000) {
					t.Fatal("MTU change reset capture clock")
				}
				if p.MarshalSize() > int(mtu) {
					t.Fatal("RTP exceeds budget")
				}
				if p.Marker != (i == len(packets)-1) {
					t.Fatal("invalid marker")
				}
			}
		}
	}
}
