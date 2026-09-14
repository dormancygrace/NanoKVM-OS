package webrtc

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
)

const qhdH265Error = "qhd-h265-webrtc-disabled"

func qhdH265Blocked(codec stream.VideoCodec) bool {
	if codec != stream.VideoCodecH265 {
		return false
	}
	s := common.GetScreen()
	if s.Height != 0 {
		return s.Height > 1080
	}
	return blocksQHD(codec, s.Height, common.ReadVideoValue("/run/nanokvm/width"), common.ReadVideoValue("/run/nanokvm/height"))
}

// blocksQHD keeps the existing H.265 WebRTC restriction for QHD while
// treating an orientation-normalized FHD signal as FHD. In particular,
// 720x1280 and 1080x1920 are standard portrait geometries; legacy
// 1088x1920 remains compatible.
func blocksQHD(codec stream.VideoCodec, height uint16, inputWidth, inputHeight int) bool {
	if codec != stream.VideoCodecH265 {
		return false
	}
	if height > 1080 {
		return true
	}
	if height != 0 {
		return false
	}
	if inputWidth == 0 || inputHeight == 0 {
		return false
	}
	return !common.IsFHDClassDimensions(inputWidth, inputHeight)
}
