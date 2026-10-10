package stream

import (
	"testing"
	"time"

	"NanoKVM-Server/common"
)

func TestBlockedMjpegCaptureHasAMessage(t *testing.T) {
	status := newCaptureStatus(CaptureModeMJPEG, common.MjpegBlockedResult, time.Now())
	if status.Ok || status.Severity != CaptureSeverityError || status.Message != common.MjpegBlockedMessage {
		t.Fatalf("status %+v", status)
	}
}

// Native capture refuses an MJPEG read at a 3840x2160 input with its own code
// (IMG_MJPEG_INPUT_BLOCKED, -8), which ReadMjpeg hands on as MjpegBlockedResult;
// it must read the same as a refusal by the file check, and not as the generic
// "Capture failed".
func TestNativeMjpegRefusalReadsLikeTheFileCheckRefusal(t *testing.T) {
	const nativeCode = -8
	native := newCaptureStatus(CaptureModeMJPEG, nativeCode, time.Now())
	file := newCaptureStatus(CaptureModeMJPEG, common.MjpegBlockedResult, time.Now())
	if native.Ok || native.Message != common.MjpegBlockedMessage || native.Severity != CaptureSeverityError {
		t.Fatalf("native refusal status %+v", native)
	}
	if native.Result != file.Result || native.Message != file.Message || !samePublicStatus(native, file) {
		t.Fatalf("native %+v differs from file check %+v", native, file)
	}
}
