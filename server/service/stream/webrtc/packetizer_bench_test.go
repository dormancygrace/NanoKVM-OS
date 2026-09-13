package webrtc

import (
	"NanoKVM-Server/service/stream"
	"bytes"
	"fmt"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"testing"
)

func BenchmarkPeerPacketization(b *testing.B) {
	// Synthetic Annex-B keyframe, benchmark packetization only, not codec or SRTP.
	frame := bytes.Repeat([]byte{0x55}, 128*1024)
	copy(frame, []byte{0, 0, 0, 1, 0x65})
	for _, peers := range []int{1, 2, 4} {
		b.Run(fmt.Sprint(peers), func(b *testing.B) {
			ps := make([]*adaptiveVideoPacketizer, peers)
			for i := range ps {
				ps[i] = newAdaptiveVideoPacketizer(stream.VideoCodecH264)
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, p := range ps {
					p.packetize(frame, int64(i)*33333, 1216)
				}
			}
		})
	}
}

func TestPacketSlabMatchesPionAndRetainsPreviousFrames(t *testing.T) {
	for _, codec := range []stream.VideoCodec{stream.VideoCodecH264, stream.VideoCodecH265} {
		p := newAdaptiveVideoPacketizer(codec)
		p.sequence = rtp.NewFixedSequencer(65530)
		p.origin = 12345
		seq := rtp.NewFixedSequencer(65530)
		var saved []byte
		var first *rtp.Packet
		for _, mtu := range []uint16{1216, 600, 1400} {
			var pay rtp.Payloader = &codecs.H264Payloader{}
			head := byte(0x65)
			if codec == stream.VideoCodecH265 {
				pay = &codecs.H265Payloader{}
				head = 0x26
			}
			data := bytes.Repeat([]byte{0x55}, 10000)
			copy(data, []byte{0, 0, 0, 1, head, 1})
			ref := rtp.NewPacketizer(mtu, 100, 0x1234ABCD, pay, seq, 90000)
			want := ref.Packetize(data, 0)
			got := p.packetize(data, 1000000, mtu)
			if len(got) != len(want) || len(got) == 0 {
				t.Fatal("packet count")
			}
			for i := range got {
				want[i].Timestamp = 12345 + 90000
				a, _ := want[i].Marshal()
				b, _ := got[i].Marshal()
				if !bytes.Equal(a, b) {
					t.Fatalf("codec %v mtu %d packet %d differs", codec, mtu, i)
				}
			}
			if first == nil {
				first = got[0]
				saved, _ = first.Marshal()
			}
		}
		now, _ := first.Marshal()
		if !bytes.Equal(now, saved) {
			t.Fatal("old frame overwritten")
		}
	}
}

func BenchmarkPionPacketizationBaseline(b *testing.B) {
 frame:=bytes.Repeat([]byte{0x55},128*1024);copy(frame,[]byte{0,0,0,1,0x65})
 p:=rtp.NewPacketizer(1216,100,0x1234ABCD,&codecs.H264Payloader{},rtp.NewRandomSequencer(),90000)
 b.ReportAllocs()
 for i:=0;i<b.N;i++ { p.Packetize(frame,3000) }
}
