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

func TestUnsupportedInputAsksForTheRestart(t *testing.T) {
	previous := rejectedInput
	t.Cleanup(func() { rejectedInput = previous })

	rejectedInput = func() (int, int) { return 2560, 1440 }
	status := newCaptureStatus(CaptureModeH264, -6, time.Now())
	if status.InputWidth != 2560 || status.InputHeight != 1440 || !status.VideoMemory ||
		status.Message != "The HDMI input 2560x1440 needs more video memory: restart the device to use the updated kernel" {
		t.Fatalf("status %+v", status)
	}
	// A different refused size is a new status for subscribers.
	rejectedInput = func() (int, int) { return 3840, 2160 }
	if samePublicStatus(status, newCaptureStatus(CaptureModeH264, -6, time.Now())) {
		t.Fatal("a new refused size was treated as the same status")
	}

	// Without a refused size, -6 keeps the generic message.
	rejectedInput = func() (int, int) { return 0, 0 }
	status = newCaptureStatus(CaptureModeH264, -6, time.Now())
	if status.VideoMemory || status.Message != "Unsupported HDMI resolution" {
		t.Fatalf("status %+v", status)
	}
	// Other results never carry an input.
	rejectedInput = func() (int, int) { return 2560, 1440 }
	if status = newCaptureStatus(CaptureModeH264, -1, time.Now()); status.VideoMemory {
		t.Fatalf("status %+v", status)
	}
}
