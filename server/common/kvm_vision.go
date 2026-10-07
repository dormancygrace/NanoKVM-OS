//go:build !teststub

package common

/*
	#cgo CFLAGS: -I../include
	#cgo LDFLAGS: -L../dl_lib -lkvm
	#include "kvm_vision.h"
	#include <string.h>
    #include <dlfcn.h>
    static int edid_maintenance(unsigned char pause) {
        int (*fn)(unsigned char) = (int (*)(unsigned char))dlsym(RTLD_DEFAULT, "kvmv_edid_maintenance");
        return fn ? fn(pause) : -1;
    }
    static int set_capture_fps(unsigned char fps) {
        int (*fn)(unsigned char) = (int (*)(unsigned char))dlsym(RTLD_DEFAULT, "kvmv_set_capture_fps");
        return fn ? fn(fps) : -1;
    }
*/
import "C"
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	kvmVision     *KvmVision
	kvmVisionOnce sync.Once
)

type KvmVision struct {
	mutex                sync.RWMutex
	closed               bool
	captureWorkerEnabled bool
	captureWorker        *videoCaptureWorker
}

func init() {
	// Older native libraries lack the symbol; they process every input frame.
	applyCaptureFPS = func(fps int) { C.set_capture_fps(C.uchar(fps)) }
}

func GetKvmVision() *KvmVision {
	kvmVisionOnce.Do(func() {
		kvmVision = &KvmVision{captureWorkerEnabled: os.Getenv("NANOKVM_NATIVE_CAPTURE_WORKER") == "1"}

		// initialize() loads Screen before it enables or disables HDMI, and both
		// HDMI paths call GetKvmVision. Select the saved H.265 GOP mode before
		// kvmv_init can start capture threads or create an encoder channel.
		selectedGOPMode := C.uint8_t(GetScreen().GOPMode)
		if C.set_h265_gop_mode(selectedGOPMode) != 0 {
			log.Errorf("failed to select saved H.265 GOP mode %d", selectedGOPMode)
		}
		logLevel := C.uint8_t(0)
		C.kvmv_init(logLevel)
		// Native VI mutex is ready; apply intent before HDMI readers start.
		if C.set_mjpeg_chroma(C.uint8_t(boolToInt(GetScreen().MjpegChroma == 422))) != 0 {
			log.Error("failed to select saved MJPEG chroma")
		}
		C.set_frame_detact(C.uint8_t(FrameDetectFrames(FrameDetectEnabled())))
		log.Debugf("kvm vision initialized")
	})

	return kvmVision
}

// GetActiveGOPMode reports the mode accepted by the native capture stack.
// It intentionally does not return the saved Screen preference: their
// difference is what tells the UI that a restart is still required.
func GetActiveGOPMode() uint8 {
	return uint8(C.get_h265_gop_mode())
}

func (k *KvmVision) ReadMjpeg(width uint16, height uint16, quality uint16) (data []byte, result int) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed {
		return nil, -1
	}

	data, result = readMjpegIntoOwnedStorage(width, height, quality)
	if result < 0 {
		log.Errorf("failed to read kvm image: %v", result)
	}
	return
}

func (k *KvmVision) ReadH264(width uint16, height uint16, bitRate uint16) (data []byte, result int) {
	screen := GetScreen()
	return k.ReadVideo(width, height, 1, bitRate, screen.GOP, uint8(screen.FPS))
}

func (k *KvmVision) ReadVideo(width uint16, height uint16, codec uint8, bitRate uint16, gop uint8, fps uint8) (data []byte, result int) {
	_, data, result = k.ReadVideoWithHeadroom(width, height, codec, bitRate, gop, fps, 0)
	return
}

func (k *KvmVision) ReadVideoWithHeadroom(width uint16, height uint16, codec uint8, bitRate uint16, gop uint8, fps uint8, headroom int) (storage []byte, data []byte, result int) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed || headroom < 0 {
		return nil, nil, -1
	}

	return readVideoIntoOwnedStorage(width, height, codec, bitRate, gop, fps, headroom)
}

func (k *KvmVision) SetHDMI(enable bool) int {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return -1
	}

	hdmiEnable := C.uint8_t(0)
	if enable {
		hdmiEnable = C.uint8_t(1)
	}

	result := int(C.kvmv_hdmi_control(hdmiEnable))
	if result < 0 {
		log.Errorf("failed to set hdmi to %t", enable)
		return result
	}

	return result
}

func (k *KvmVision) HasHDMISignal() bool {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return false
	}

	return C.kvmv_hdmi_signal_active() != 0
}

func (k *KvmVision) SetGop(gop uint8) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed {
		return
	}

	_gop := C.uint8_t(gop)
	C.set_h264_gop(_gop)
}

func (k *KvmVision) SetFrameDetect(frame uint8) {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return
	}

	_frame := C.uint8_t(frame)
	C.set_frame_detact(_frame)
}

func (k *KvmVision) Close() {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed {
		return
	}

	k.closed = true
	if k.captureWorker != nil {
		k.captureWorker.close()
		k.captureWorker = nil
	}
	C.kvmv_deinit()
	log.Debugf("stop kvm vision...")
}

// ApplyMonitorProfile serializes maintenance with frame reads and HDMI controls.
// Native HDMI-off pauses its detector. The utility pulses reset while this lock
// keeps the detector and capture callers away from its I2C programming sequence.
func (k *KvmVision) ApplyMonitorProfile(path string) error {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed {
		return fmt.Errorf("video capture is closed")
	}
	cube := MonitorRequiresPowerCycle()
	if cube {
		if C.edid_maintenance(1) < 0 {
			return fmt.Errorf("Cube EDID maintenance requires updated native video library")
		}
		defer C.edid_maintenance(0)
	} else {
		defer C.kvmv_hdmi_control(1)
		if C.kvmv_hdmi_control(0) < 0 {
			return fmt.Errorf("cannot pause HDMI for monitor change")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{path}
	if cube {
		// Marker is durable before writing: even a timeout or process crash can
		// leave receiver flash changed. Do not erase it on uncertain failure.
		if err := atomicWriteMonitorFile(monitorPowerCyclePendingFile, []byte("1\n"), ".monitor_pending-*"); err != nil {
			return err
		}
		args = []string{"--accept-power-cycle", path}
	}
	output, err := exec.CommandContext(ctx, "/usr/sbin/nanokvm_update_edid", args...).CombinedOutput()
	if err != nil {
		log.Errorf("monitor EDID programming failed: %v: %s", err, output)
		return fmt.Errorf("monitor EDID programming failed")
	}
	return nil
}

// RequestKeyframe queues native intent; it does not touch VENC from this thread.
func (k *KvmVision) RequestKeyframe() {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if !k.closed {
		C.kvmv_request_keyframe()
	}
}

func boolToInt(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

// Reports the actual VPSS producer and any temporary or latched fallback.
func GetMjpegChromaStatus() (uint16, string) {
	state := uint8(C.get_mjpeg_chroma_status())
	active := uint16(420)
	if state&uint8(C.MJPEG_CHROMA_ACTIVE_422) != 0 {
		active = 422
	}
	reason := ""
	switch {
	case state&uint8(C.MJPEG_CHROMA_FALLBACK_ERROR) != 0:
		reason = "hardware"
	case state&uint8(C.MJPEG_CHROMA_LIMIT_WIDTH) != 0 && active == 422:
		reason = "resolution"
	case state&uint8(C.MJPEG_CHROMA_SHARED_VIDEO) != 0:
		reason = "video"
	case state&uint8(C.MJPEG_CHROMA_FRAME_DETECT) != 0:
		reason = "frameDetection"
	case state&uint8(C.MJPEG_CHROMA_FORCE_COPY) != 0:
		reason = "diagnostic"
	}
	return active, reason
}

func (k *KvmVision) SetMjpegChroma(chroma uint16) int {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed || (chroma != 420 && chroma != 422) {
		return -1
	}
	return int(C.set_mjpeg_chroma(C.uint8_t(boolToInt(chroma == 422))))
}
