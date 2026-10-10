package common

// MjpegMaxLongSide is the longest side of the HDMI input MJPEG may run with.
// At a 3840x2160 input MJPEG is not usable whatever the stream limit is: next
// to a 4K stream the JPEG channel wedges the hardware encoder ("cviGetEncodedInfo
// = 2501" until reboot), and with the stream limited to 1440p it produces no
// frames.
const MjpegMaxLongSide = 2560

// MjpegBlockedResult is the ReadMjpeg result of a capture that was refused
// because the HDMI input is 3840x2160. It is not a native code.
const MjpegBlockedResult = -8

// MjpegBlockedMessage tells the user the rule and the way out.
const MjpegBlockedMessage = "MJPEG is unavailable while the HDMI input is 3840x2160: use H.264 or H.265"

// nativeStateDir holds the values native capture reports.
var nativeStateDir = "/run/nanokvm"

// CaptureInputSize is the HDMI capture size, 0x0 without a signal. It is read
// on every call: a cached size could let a JPEG channel start at the new size.
func CaptureInputSize() (width, height int) {
	return ReadVideoValue(nativeStateDir + "/width"), ReadVideoValue(nativeStateDir + "/height")
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
