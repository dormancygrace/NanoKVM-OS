package webrtc

import (
	"NanoKVM-Server/service/stream"
	"bytes"
	"fmt"
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// Fixtures model Annex-B framing and burst sizes, not decodable video or encoder
// output. 24 KiB P frames approximate 10 Mbit/s at 50 FPS; a 192 KiB IDR exercises
// the larger packet burst. Frames stay immutable for every consumer lifetime.
func ownedPayloadFixture(codec stream.VideoCodec, keyframe bool, size int) []byte {
	var frame []byte
	addNAL := func(header []byte, body []byte) {
		frame = append(frame, 0, 0, 0, 1)
		frame = append(frame, header...)
		frame = append(frame, body...)
	}
	body := bytes.Repeat([]byte{0x55}, size)
	if codec == stream.VideoCodecH264 {
		if keyframe {
			addNAL([]byte{0x09}, []byte{0xf0})
			addNAL([]byte{0x67}, []byte{0x42, 0, 0x1f, 0xe5, 0x88, 0x68})
			addNAL([]byte{0x68}, []byte{0xce, 0x3c, 0x80})
			addNAL([]byte{0x06}, []byte{0x05, 0x01, 0x55, 0x80})
			addNAL([]byte{0x65}, body)
		} else {
			addNAL([]byte{0x41}, body[:len(body)/2])
			addNAL([]byte{0x41}, body[len(body)/2:])
		}
	} else if keyframe {
		addNAL([]byte{35 << 1, 1}, []byte{0x50})                   // AUD
		addNAL([]byte{39 << 1, 1}, []byte{0x05, 0x01, 0x55, 0x80}) // prefix SEI
		addNAL([]byte{19 << 1, 1}, body)
	} else {
		addNAL([]byte{1 << 1, 1}, body[:len(body)/2])
		addNAL([]byte{1 << 1, 1}, body[len(body)/2:])
	}
	return frame
}

func ownedPayloadPionPayloader(codec stream.VideoCodec) rtp.Payloader {
	if codec == stream.VideoCodecH265 {
		return &codecs.H265Payloader{}
	}
	return &codecs.H264Payloader{}
}

var ownedPayloadBenchmarkPackets []*rtp.Packet

func BenchmarkOwnedFramePacketization(b *testing.B) {
	for _, codec := range []stream.VideoCodec{stream.VideoCodecH264, stream.VideoCodecH265} {
		codecName := "H264"
		if codec == stream.VideoCodecH265 {
			codecName = "H265"
		}
		for _, shape := range []struct {
			name     string
			keyframe bool
			size     int
		}{{"P24KiB", false, 24 << 10}, {"IDR192KiB", true, 192 << 10}} {
			frame := ownedPayloadFixture(codec, shape.keyframe, shape.size)
			for _, mtu := range []uint16{1216, 600} {
				for _, peers := range []int{1, 4} {
					for _, mode := range []string{"active", "pion"} {
						name := fmt.Sprintf("%s/%s/MTU%d/peers%d/%s", codecName, shape.name, mtu, peers, mode)
						b.Run(name, func(b *testing.B) {
							active := make([]*adaptiveVideoPacketizer, peers)
							pion := make([]rtp.Packetizer, peers)
							for i := 0; i < peers; i++ {
								active[i] = newAdaptiveVideoPacketizer(codec)
								pion[i] = rtp.NewPacketizer(mtu, 100, 0x1234abcd,
									ownedPayloadPionPayloader(codec), rtp.NewFixedSequencer(0), 90000)
							}
							b.SetBytes(int64(len(frame) * peers))
							b.ReportAllocs()
							b.ResetTimer()
							for i := 0; i < b.N; i++ {
								for peer := 0; peer < peers; peer++ {
									if mode == "active" {
										ownedPayloadBenchmarkPackets = active[peer].packetize(frame, int64(i)*20000, mtu)
									} else {
										ownedPayloadBenchmarkPackets = pion[peer].Packetize(frame, 1800)
									}
								}
							}
						})
					}
				}
			}
		}
	}
}

// Compare full RTP bytes with pinned Pion for multi-NAL P/IDR traffic, sequence
// rollover and changing MTU. Old packets and input frames remain observable
// after subsequent packetizations, matching asynchronous NACK ownership.
func TestOwnedFramePacketizationWireAndRetention(t *testing.T) {
	for _, codec := range []stream.VideoCodec{stream.VideoCodecH264, stream.VideoCodecH265} {
		active := newAdaptiveVideoPacketizer(codec)
		active.origin = 12345
		active.sequence = rtp.NewFixedSequencer(65530)
		sequence := rtp.NewFixedSequencer(65530)
		var retained []*rtp.Packet
		var retainedBytes [][]byte
		for round, mtu := range []uint16{1216, 1216, 600, 600, 1400} {
			keyframe := round%2 == 0
			frame := ownedPayloadFixture(codec, keyframe, 24<<10)
			original := bytes.Clone(frame)
			reference := rtp.NewPacketizer(mtu, 100, 0x1234abcd,
				ownedPayloadPionPayloader(codec), sequence, 90000)
			// Changing MTU resets payloader state in the active packetizer. These
			// fixtures repeat SPS/PPS in every H264 IDR, so the reference emits
			// the same self-contained access unit without relying on old state.
			want := reference.Packetize(frame, 0)
			got := active.packetize(frame, int64(round)*20000, mtu)
			if len(got) == 0 || len(got) != len(want) {
				t.Fatalf("codec=%v round=%d packet count got=%d want=%d", codec, round, len(got), len(want))
			}
			for i := range got {
				want[i].Timestamp = active.origin + uint32(round*1800)
				wantBytes, err := want[i].Marshal()
				if err != nil {
					t.Fatal(err)
				}
				gotBytes, err := got[i].Marshal()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(gotBytes, wantBytes) {
					t.Fatalf("codec=%v round=%d packet=%d wire bytes differ", codec, round, i)
				}
				if got[i].MarshalSize() > int(mtu) {
					t.Fatalf("packet exceeds MTU: %d > %d", got[i].MarshalSize(), mtu)
				}
				retained = append(retained, got[i])
				retainedBytes = append(retainedBytes, gotBytes)
			}
			if !bytes.Equal(frame, original) {
				t.Fatal("packetization changed immutable source frame")
			}
		}
		for i, packet := range retained {
			now, err := packet.Marshal()
			if err != nil || !bytes.Equal(now, retainedBytes[i]) {
				t.Fatalf("codec=%v retained packet=%d changed: %v", codec, i, err)
			}
		}
	}
}
