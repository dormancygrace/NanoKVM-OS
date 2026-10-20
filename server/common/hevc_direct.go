package common

import (
	"os"
	"strings"
)

// Video straight from the capture buffer. H.265 reads the receiver's packed
// 4:2:2 frame itself instead of a VPSS conversion to NV21: one copy and
// conversion less per frame (about 6 ms at 1080p, 25 ms at 2160p). It needs a
// codec driver that takes packed sources (nk_packed_src). H.264 takes only
// 4:2:0: with a VI driver that has nk_yuv420 the capture switches to 4:2:0
// while H.264 streams at the input size, and H.264 reads that frame (MJPEG is
// 4:2:0 meanwhile). libkvm reads the setting and applies a change within a
// second. The file name predates H.264 and is kept.
var (
	HevcDirectSettingFile = "/etc/kvm/hevc_vi_direct"
	hevcDirectDriverParam = "/sys/module/cvi_vc_driver/parameters/nk_packed_src"
	viYuv420DriverParam   = "/sys/module/cv181x_vi/parameters/nk_yuv420"
)

// HevcDirect returns the setting: 1 (default) direct capture, 0 through VPSS.
func HevcDirect() int {
	data, err := os.ReadFile(HevcDirectSettingFile)
	if err == nil && strings.TrimSpace(string(data)) == "0" {
		return 0
	}
	return 1
}

// HevcDirectSupported reports whether the loaded codec driver takes packed
// 4:2:2 sources, so the setting has an effect on H.265.
func HevcDirectSupported() bool {
	data, err := os.ReadFile(hevcDirectDriverParam)
	return err == nil && strings.TrimSpace(string(data)) == "1"
}

// ViDirectH264Supported reports whether the loaded VI driver can capture
// 4:2:0 (osdrv patch 0013), so the setting has an effect on H.264.
func ViDirectH264Supported() bool {
	_, err := os.Stat(viYuv420DriverParam)
	return err == nil
}
