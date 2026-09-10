package webrtc

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

func TestCaptureClockPreservesDroppedTimeAndFragmentation(t *testing.T) {
	for _, codec := range []string{"H264", "H265"} {
		t.Run(codec, func(t *testing.T) {
			var payloader rtp.Payloader = &codecs.H264Payloader{}
			data := append([]byte{0, 0, 0, 1, 0x65}, make([]byte, 4000)...)
			if codec == "H265" {
				payloader = &codecs.H265Payloader{}
				data = append([]byte{0, 0, 0, 1, 19 << 1, 1}, make([]byte, 4000)...)
			}
			packetizer := rtp.NewPacketizer(videoRTPMTU, 100, 42, payloader, rtp.NewFixedSequencer(100), 90000)
			var origin uint32
			var sequence uint16 = 100
			// 60 Hz capture, a two-second delivery gap, an hour of elapsed time,
			// then the 32-bit RTP wrap boundary; no need to emit omitted frames.
			for _, micros := range []int64{0, 16666, 33333, 2033333, 3600000000, 47721859000} {
				packets := packetizeCapturedVideo(packetizer, data, micros)
				if len(packets) < 2 {
					t.Fatal("expected fragmented access unit")
				}
				if micros == 0 {
					origin = packets[0].Timestamp
				}
				want := origin + uint32(uint64(micros)*90/1000)
				for i, packet := range packets {
					if packet.Timestamp != want {
						t.Fatalf("capture %d: timestamp %d, want %d", micros, packet.Timestamp, want)
					}
					if packet.SequenceNumber != sequence {
						t.Fatalf("sequence %d, want %d", packet.SequenceNumber, sequence)
					}
					sequence++
					if packet.MarshalSize()+16+8+40 > 1280 {
						t.Fatal("MTU exceeded")
					}
					if packet.Marker != (i == len(packets)-1) {
						t.Fatal("incorrect access-unit marker")
					}
				}
			}
		})
	}
}
