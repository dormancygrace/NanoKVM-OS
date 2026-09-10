package webrtc

import (
	"NanoKVM-Server/service/stream"
	"github.com/pion/srtp/v3"
	"testing"
)

func TestVideoSRTPFitsIPv6MinimumMTU(t *testing.T) {
	for _, codec := range []stream.VideoCodec{stream.VideoCodecH264, stream.VideoCodecH265} {
		for _, profile := range []srtp.ProtectionProfile{srtp.ProtectionProfileAes128CmHmacSha1_80, srtp.ProtectionProfileAeadAes128Gcm} {
			saltLen := 14
			if profile == srtp.ProtectionProfileAeadAes128Gcm {
				saltLen = 12
			}
			ctx, err := srtp.CreateContext(make([]byte, 16), make([]byte, saltLen), profile)
			if err != nil {
				t.Fatal(err)
			}
			data := append([]byte{0, 0, 0, 1, 0x65}, make([]byte, 5000)...)
			if codec == stream.VideoCodecH265 {
				data = append([]byte{0, 0, 0, 1, 19 << 1, 1}, make([]byte, 5000)...)
			}
			packets := newVideoPacketizer(codec).Packetize(data, 3000)
			if len(packets) < 2 {
				t.Fatal("expected fragmented frame")
			}
			maxSize := 0
			for _, packet := range packets {
				raw, err := packet.Marshal()
				if err != nil {
					t.Fatal(err)
				}
				encrypted, err := ctx.EncryptRTP(nil, raw, nil)
				if err != nil {
					t.Fatal(err)
				}
				size := len(encrypted) + 8 + 40
				if size > 1280 {
					t.Fatalf("%s profile %v: IPv6 packet %d > 1280", codec, profile, size)
				}
				if size > maxSize {
					maxSize = size
				}
			}
			want := 1274
			if profile == srtp.ProtectionProfileAeadAes128Gcm {
				want = 1280
			}
			if maxSize != want {
				t.Fatalf("%s profile %v: largest packet %d, want %d", codec, profile, maxSize, want)
			}
		}
	}
}
