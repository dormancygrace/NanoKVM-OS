package vm

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
)

func init() {
	common.FirstCaptureYUV420 = firstCaptureYUV420
}

// selectedEncoder is a seam for tests.
var selectedEncoder = stream.SelectedEncoderConfig

// captureSizes is a seam for tests: the capture input, then the input the
// saved monitor setting asks for (each 0x0 when unknown).
var captureSizes = func() (inputWidth, inputHeight, targetWidth, targetHeight int) {
	inputWidth, inputHeight = common.CaptureInputSize()
	targetWidth, targetHeight = common.MonitorTargetSize()
	return
}

// inputRates is a seam for tests: the measured input rate, then the rate of
// the programmed monitor profile (each 0 when unknown).
var inputRates = func() (measured, profile int) {
	return common.ReadVideoValue("/run/nanokvm/input_fps"), common.MonitorRefreshHz()
}

// assumedInputFPS stands in for an unknown input rate: what a source sends
// without a rate profile.
const assumedInputFPS = 60

// rateThins mirrors mmf_vpss_set_rate (kvm_mmf.cpp): a stream rate below the
// input rate, beyond the measurement tolerance, thins frames in the VPSS, and
// the native mode rule then keeps 4:2:2.
func rateThins(inputFPS, streamFPS int) bool {
	return streamFPS < inputFPS-max(inputFPS/20, 2)
}

// firstCaptureYUV420 reports whether the device's own stream, WebRTC or
// Direct with the device codec, will take 4:2:0 capture by the native mode
// rule (_mmf_vi_yuv420_rule): H.264 at the input size and rate. Both
// transports subscribe through the device codec selection, so a browser
// asking for another codec is refused while H.264 is selected. Native capture
// applies the rest of the rule itself: the direct setting and the VI driver at
// every start. libkvm fixes mirror and flip at init (no setting changes them),
// so the first start never sees a mirrored capture. Before a signal the input
// is unknown: the saved monitor setting's input stands in, and with neither
// known only a stream at the input size counts; an unknown input rate is
// taken as 60 Hz. A wrong guess costs one capture restart when the first
// stream starts.
func firstCaptureYUV420() bool {
	if StreamTransport() == TransportMJPEG {
		return false
	}
	config, selected := selectedEncoder()
	if !selected || config.Codec != stream.VideoCodecH264 {
		return false
	}
	screen := common.GetScreen()
	streamHeight := screen.Height
	inputFPS, profileFPS := inputRates()
	if inputFPS <= 0 {
		inputFPS = profileFPS
	}
	if inputFPS <= 0 {
		inputFPS = assumedInputFPS
	}
	width, height, targetWidth, targetHeight := captureSizes()
	if width <= 0 || height <= 0 {
		width, height = targetWidth, targetHeight
	}
	if width <= 0 || height <= 0 {
		return streamHeight == 0 && !rateThins(inputFPS, screen.FPS)
	}
	// The stream rate as GetCaptureScreen caps it: by the input size and by the
	// stream limit, even when the input fits within that limit.
	streamFPS := min(screen.FPS, common.CaptureRateLimit(width, height),
		common.CaptureRateLimit(int(screen.Width), int(screen.Height)))
	return common.H264AllowedFor(width, height, streamHeight) &&
		!common.StreamScaled(width, height, streamHeight) &&
		!rateThins(inputFPS, streamFPS)
}
