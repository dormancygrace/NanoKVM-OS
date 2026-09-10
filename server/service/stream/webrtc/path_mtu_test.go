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
		for index, mtu := range []uint16{1216, 1456, 1152, 1216, 1456} {
			micros := int64(index) * 2033333
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
