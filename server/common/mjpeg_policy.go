package common

import (
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// MjpegMaxLongSide is the longest side of the HDMI input MJPEG may run with.
// A UHD input plus a secondary JPEG output can stall VPSS frame delivery,
// including JPEG downscaled to 1920x1080. The guard therefore uses the HDMI
// input size rather than the requested stream size; see docs/VIDEO.md.
const MjpegMaxLongSide = 2560

// MjpegBlockedResult is the ReadMjpeg result of a capture that was refused
// because the HDMI input is 3840x2160. Native capture returns the same value
// (IMG_MJPEG_INPUT_BLOCKED in kvm_vision.h) from kvmv_read_mjpeg_sink.
const MjpegBlockedResult = -8

// nativeMjpegBlocked mirrors IMG_MJPEG_INPUT_BLOCKED. The cgo build asserts at
// compile time that the two are equal; a test compares it with the header.
const nativeMjpegBlocked = -8

// normalizeNativeMjpegResult maps a native JPEG read result to a ReadMjpeg
// result, so that the stream, MCP and screenshot paths treat a refusal by
// native capture and one by the refusal hold below the same.
func normalizeNativeMjpegResult(native int) int {
	if native == nativeMjpegBlocked {
		return MjpegBlockedResult
	}
	return native
}

// readMjpegChecked is the body of KvmVision.ReadMjpeg; read does the native
// capture, which refuses a 3840x2160 input under vi_mutex against the size it
// has detected. A refusal is remembered for mjpegRefusalHold: while the input
// stays 3840x2160 the stream ticks stop here instead of taking the capture
// lock, which the video reader shares, on every tick. The state files are not
// read here: read for every frame they cost a dozen system calls a frame,
// about 1900 a second at 1080p60 with a viewer.
func readMjpegChecked(read func() ([]byte, int)) ([]byte, int) {
	if mjpegRecentlyRefused() {
		return nil, MjpegBlockedResult
	}
	data, result := read()
	result = normalizeNativeMjpegResult(result)
	if result == MjpegBlockedResult {
		deadline := time.Now().Add(mjpegRefusalHold)
		mjpegRefusal.Store(&deadline)
	}
	return data, result
}

// mjpegRefusalHold is how long a native refusal answers for native capture:
// MJPEG resumes at most this late after the input leaves 3840x2160.
const mjpegRefusalHold = 500 * time.Millisecond

// Keep the monotonic clock reading: a wall-clock correction must neither
// extend a hold nor reactivate an expired one. nil means no native refusal.
var mjpegRefusal atomic.Pointer[time.Time]

func mjpegRecentlyRefused() bool {
	deadline := mjpegRefusal.Load()
	return deadline != nil && time.Now().Before(*deadline)
}

// MjpegRefusalHeld reports a native refusal within the hold: the stream loop
// checks it on every tick instead of reading the state files.
func MjpegRefusalHeld() bool {
	return mjpegRecentlyRefused()
}

// MjpegBlockedMessage tells the user the rule and the way out.
const MjpegBlockedMessage = "MJPEG is unavailable while the HDMI input is 3840x2160: use H.264 or H.265"

// nativeStateDir holds the values native capture reports.
var nativeStateDir = "/run/nanokvm"

// CaptureInputSize is the HDMI capture size, 0x0 without a signal. It is read
// on every call: a cached size could let a JPEG channel start at the new size.
func CaptureInputSize() (width, height int) {
	return ReadVideoValue(nativeStateDir + "/width"), ReadVideoValue(nativeStateDir + "/height")
}

// RejectedInput is an HDMI input that native capture refused because the
// video memory of this boot cannot hold it: a device still running the fixed
// carveout of an earlier image, until it restarts with the updated kernel. It
// is 0x0 when no input is refused for that reason.
func RejectedInput() (width, height int) {
	data, err := os.ReadFile(nativeStateDir + "/rejected_input")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return 0, 0
	}
	w, widthErr := strconv.ParseUint(fields[0], 10, 16)
	h, heightErr := strconv.ParseUint(fields[1], 10, 16)
	if widthErr != nil || heightErr != nil || w == 0 || h == 0 {
		return 0, 0
	}
	return int(w), int(h)
}

// MjpegAllowedFor reports whether MJPEG may run with a capture size. The stream
// limit does not matter. An unknown input (0x0) is allowed: nothing is encoded
// until it is known, and it is checked again.
func MjpegAllowedFor(inputWidth, inputHeight int) bool {
	return max(inputWidth, inputHeight) <= MjpegMaxLongSide
}

// MjpegAllowed reports whether MJPEG may run with the current capture.
func MjpegAllowed() bool {
	return MjpegAllowedFor(CaptureInputSize())
}
