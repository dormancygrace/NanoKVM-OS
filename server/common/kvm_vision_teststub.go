//go:build teststub

package common

import "errors"

// KvmVision is a no-hardware implementation used by architecture-independent
// unit tests. Production builds compile kvm_vision.go and use the real C ABI.
type KvmVision struct{}

var testKvmVision = &KvmVision{}

func GetKvmVision() *KvmVision {
	return testKvmVision
}

func (k *KvmVision) ReadMjpeg(uint16, uint16, uint16) ([]byte, int) {
	return readMjpegChecked(func() ([]byte, int) { return nil, -1 })
}

func (k *KvmVision) ReadVideoWithHeadroom(uint16, uint16, uint8, uint16, uint8, int) ([]byte, []byte, int) {
	return nil, nil, -1
}

// Tests use the Go-paced loop unless they inject a VideoStream.
func (k *KvmVision) StartVideoCapture(VideoCaptureParams) VideoStream { return nil }

func (k *KvmVision) SetHDMI(bool) int {
	return 0
}

func (k *KvmVision) SetCaptureParked(bool) int   { return 0 }
func (k *KvmVision) CaptureReclaimPending() bool { return false }

func (k *KvmVision) HasHDMISignal() bool {
	return false
}

func (k *KvmVision) SetFrameDetect(uint8) {}

func (k *KvmVision) Close() {}

func (k *KvmVision) ApplyMonitorProfile(string) error { return nil }

func (k *KvmVision) ReadMonitorEDID() ([]byte, error) { return nil, errors.New("no HDMI receiver") }

func (k *KvmVision) RequestKeyframe() {}

func GetMjpegChromaStatus() (uint16, string) { return 420, "" }
func (k *KvmVision) SetMjpegChroma(chroma uint16) int {
	if chroma != 420 && chroma != 422 {
		return -1
	}
	return 0
}
