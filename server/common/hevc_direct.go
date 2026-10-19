package common

import (
	"os"
	"strings"
)

// H.265 straight from the capture buffer. The encoder reads the receiver's
// packed 4:2:2 frame itself instead of a VPSS conversion to NV21: one copy
// and conversion less per frame (about 6 ms at 1080p, 25 ms at 2160p). It
// needs a codec driver that takes packed sources (nk_packed_src); libkvm
// reads the setting and applies a change within a second.
var (
	HevcDirectSettingFile = "/etc/kvm/hevc_vi_direct"
	hevcDirectDriverParam = "/sys/module/cvi_vc_driver/parameters/nk_packed_src"
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
// 4:2:2 sources, so the setting has an effect.
func HevcDirectSupported() bool {
	data, err := os.ReadFile(hevcDirectDriverParam)
	return err == nil && strings.TrimSpace(string(data)) == "1"
}
