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
