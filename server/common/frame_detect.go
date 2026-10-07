package common

import (
	"os"
	"strings"
)

// FrameDetectFile keeps the MJPEG frame-detect choice across restarts. The
// native encoder starts with frame detection off, so the saved choice is
// applied when KvmVision is initialized.
var FrameDetectFile = "/etc/kvm/frame_detect"

// FrameDetectInterval is the number of unchanged frames after which the
// encoder stops sending MJPEG frames while frame detection is enabled.
const FrameDetectInterval uint8 = 60

// FrameDetectEnabled reports the saved choice; it is off when unset.
func FrameDetectEnabled() bool {
	data, err := os.ReadFile(FrameDetectFile)
	return err == nil && strings.TrimSpace(string(data)) == "1"
}

// SaveFrameDetect persists the choice. Callers apply it to the encoder.
func SaveFrameDetect(enabled bool) error {
	value := "0\n"
	if enabled {
		value = "1\n"
	}
	return os.WriteFile(FrameDetectFile, []byte(value), 0o644)
}

// FrameDetectFrames is the encoder setting for a frame-detect choice.
func FrameDetectFrames(enabled bool) uint8 {
	if enabled {
		return FrameDetectInterval
	}
	return 0
}
