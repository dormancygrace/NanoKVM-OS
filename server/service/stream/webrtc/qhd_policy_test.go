package webrtc

import (
	"NanoKVM-Server/service/stream"
	"testing"
)

func TestQHDPolicy(t *testing.T) {
	for _, c := range []struct {
		name    string
		codec   stream.VideoCodec
		cap     uint16
		w, h    int
		blocked bool
	}{
		{"H265 QHD", stream.VideoCodecH265, 1440, 2560, 1440, true},
		{"H265 auto QHD", stream.VideoCodecH265, 0, 2560, 1440, true},
		{"H265 capped FHD", stream.VideoCodecH265, 1080, 2560, 1440, false},
		{"H265 auto FHD", stream.VideoCodecH265, 0, 1920, 1080, false},
		{"H265 auto portrait FHD", stream.VideoCodecH265, 0, 1088, 1920, false},
		{"H265 capped portrait FHD", stream.VideoCodecH265, 1080, 1088, 1920, false},
		{"H265 portrait QHD cap", stream.VideoCodecH265, 1440, 1088, 1920, true},
		{"H265 normalized FHD", stream.VideoCodecH265, 0, 1920, 1080, false},
		{"H265 unqualified padded landscape", stream.VideoCodecH265, 0, 1920, 1088, true},
		{"H265 unqualified portrait padding", stream.VideoCodecH265, 0, 1088, 1919, true},
		{"H265 normalized portrait FHD", stream.VideoCodecH265, 0, 1080, 1920, false},
		{"H264 QHD", stream.VideoCodecH264, 1440, 2560, 1440, false},
		{"H265 no signal", stream.VideoCodecH265, 0, 0, 0, false},
		{"H265 unknown signal", stream.VideoCodecH265, 0, 1088, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := blocksQHD(c.codec, c.cap, c.w, c.h); got != c.blocked {
				t.Fatalf("blocked=%v want %v", got, c.blocked)
			}
		})
	}
}
