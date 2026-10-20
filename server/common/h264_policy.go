package common

// The H.264 encoder (CODA980) is used up to QHD: at 3840x2160 it writes
// corrupted blocks into the bitstream (H.265 of the same picture is clean),
// and the vendor documents 2880x1620 as its largest frame. H.264 therefore
// runs only when the encoded picture has a long side of at most 2560 and a
// height of at most 1920: 2560x1440, and 1080x1920 in portrait. Larger
// pictures need H.265 or a stream resolution of 1440 or lower (1080 or lower
// for the 1440x2560 portrait, which it fits into 1080x1920).
//
// The native guard uses the same numbers (h264_max_long_side and
// h264_max_height in support/sg2002/additional/kvm/include/stream_size.hpp);
// a test compares them.
const (
	H264MaxLongSide = 2560
	H264MaxHeight   = 1920
)

// H264BlockedMessage tells the user the rule and the way out.
const H264BlockedMessage = "H.264 takes at most 2560x1440 (1080x1920 in portrait): use H.265 or a stream resolution of 1440 or lower (1080 or lower in portrait)"

// H264OutputAllowed reports whether H.264 may encode a picture of this size.
func H264OutputAllowed(width, height int) bool {
	return max(width, height) <= H264MaxLongSide && height <= H264MaxHeight
}

// h264OutputSize is the size native capture encodes for an input and a stream
// limit (0 is the input size), as stream_size() in stream_size.hpp computes
// it: the input fitted into the limit box, never enlarged. The limits are
// landscape boxes; for the portrait monitor profiles the box is turned
// upright. Rounding to even sizes is left out: it cannot cross either limit.
func h264OutputSize(inputWidth, inputHeight int, streamHeight uint16) (width, height int) {
	boxWidth, boxHeight := int(ResolutionMap[streamHeight]), int(streamHeight)
	if boxWidth == 0 || boxHeight == 0 {
		return inputWidth, inputHeight
	}
	if portraitProfileInput(inputWidth, inputHeight) {
		boxWidth, boxHeight = boxHeight, boxWidth
	}
	if inputWidth <= boxWidth && inputHeight <= boxHeight {
		return inputWidth, inputHeight
	}
	if boxWidth*inputHeight <= boxHeight*inputWidth {
		return boxWidth, inputHeight * boxWidth / inputWidth
	}
	return inputWidth * boxHeight / inputHeight, boxHeight
}

func portraitProfileInput(width, height int) bool {
	switch [2]int{width, height} {
	case [2]int{720, 1280}, [2]int{1080, 1920}, [2]int{1088, 1920}, [2]int{1440, 2560}:
		return true
	}
	return false
}

// H264AllowedFor reports whether H.264 may run with a capture size and a
// stream limit. An unknown input (0x0) is allowed: nothing is encoded until it
// is known, and native capture checks the size it encodes.
func H264AllowedFor(inputWidth, inputHeight int, streamHeight uint16) bool {
	if inputWidth <= 0 || inputHeight <= 0 {
		return true
	}
	return H264OutputAllowed(h264OutputSize(inputWidth, inputHeight, streamHeight))
}

// monitorTargetSize is a seam for tests.
var monitorTargetSize = MonitorTargetSize

// MonitorTargetSize is the input the saved monitor setting asks the source
// for: the portrait profile, else the landscape monitor (Auto: the size it
// stands for). It is 0x0 when the receiver cannot be programmed or Auto is the
// stock profile.
func MonitorTargetSize() (width, height int) {
	if !MonitorProfileSupported() {
		return 0, 0
	}
	monitorMutex.Lock()
	portrait, monitor, resolution := monitorPortraitEnabledLocked(), savedMonitorResolutionLocked(), savedPortraitResolutionLocked()
	monitorMutex.Unlock()
	if portrait {
		return int(PortraitWidth(resolution)), int(resolution)
	}
	if monitor == 0 {
		monitor = AutoMonitorHeight()
	}
	return int(ResolutionMap[monitor]), int(monitor)
}

// H264SelectionAllowed reports whether H.264 may become the device codec with
// a capture size and a stream limit. The source renders a new monitor setting
// a few seconds after it is saved, so the saved monitor also qualifies: a
// settings change that lowers the monitor and selects H.264 is not refused
// against the input it is replacing. Each stream still checks the input it
// encodes.
func H264SelectionAllowed(captureWidth, captureHeight int, streamHeight uint16) bool {
	if H264AllowedFor(captureWidth, captureHeight, streamHeight) {
		return true
	}
	width, height := monitorTargetSize()
	return width > 0 && height > 0 && H264AllowedFor(width, height, streamHeight)
}
