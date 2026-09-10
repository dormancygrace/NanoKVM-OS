//go:build !teststub

package common

/*
	#cgo CFLAGS: -I../include
	#cgo LDFLAGS: -L../dl_lib -lkvm
	#include "kvm_vision.h"
	#include <string.h>
*/
import "C"
import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

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

func GetKvmVision() *KvmVision {
	kvmVisionOnce.Do(func() {
		kvmVision = &KvmVision{captureWorkerEnabled: os.Getenv("NANOKVM_NATIVE_CAPTURE_WORKER") == "1"}

		logLevel := C.uint8_t(0)
		C.kvmv_init(logLevel)
		log.Debugf("kvm vision initialized")
	})

	return kvmVision
}

func (k *KvmVision) ReadMjpeg(width uint16, height uint16, quality uint16) (data []byte, result int) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed {
		return nil, -1
	}

	var (
		kvmData  *C.uint8_t
		dataSize C.uint32_t
	)

	result = int(C.kvmv_read_img(
		C.uint16_t(width),
		C.uint16_t(height),
		C.uint8_t(0),
		C.uint16_t(quality),
		&kvmData,
		&dataSize,
	))
	if result < 0 {
		log.Errorf("failed to read kvm image: %v", result)
		return
	}
	defer C.free_kvmv_data(&kvmData)

	data = C.GoBytes(unsafe.Pointer(kvmData), C.int(dataSize))
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

	var (
		kvmData  *C.uint8_t
		dataSize C.uint32_t
	)

	if k.captureWorkerEnabled {
		if k.captureWorker == nil {
			worker, err := newVideoCaptureWorker()
			if err != nil {
				log.Errorf("initialize native capture worker: %v", err)
				return nil, nil, -1
			}
			k.captureWorker = worker
			log.Info("native video capture worker enabled")
		}
		pointer, size, code, err := k.captureWorker.read(width, height, codec, bitRate, gop, fps)
		if err != nil {
			log.Errorf("native video capture worker: %v", err)
			return nil, nil, -1
		}
		kvmData, dataSize, result = (*C.uint8_t)(pointer), C.uint32_t(size), code
	} else {
		result = int(C.kvmv_read_video(
			C.uint16_t(width),
			C.uint16_t(height),
			C.uint8_t(codec),
			C.uint16_t(bitRate),
			C.uint8_t(gop),
			C.uint8_t(fps),
			&kvmData,
			&dataSize,
		))
	}
	if result < 0 {
		log.Errorf("failed to read kvm image: %v", result)
		return
	}
	defer C.free_kvmv_data(&kvmData)

	storage = make([]byte, headroom+int(dataSize))
	data = storage[headroom:]
	if dataSize != 0 {
		C.memcpy(unsafe.Pointer(&data[0]), unsafe.Pointer(kvmData), C.size_t(dataSize))
	}
	return
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
	defer C.kvmv_hdmi_control(1)
	if C.kvmv_hdmi_control(0) < 0 {
		return fmt.Errorf("cannot pause HDMI for monitor change")
	}
	output, err := exec.Command("/usr/sbin/nanokvm_update_edid", path).CombinedOutput()
	if err != nil {
		log.Errorf("monitor EDID programming failed: %v: %s", err, output)
		return fmt.Errorf("monitor EDID programming failed")
	}
	return nil
}
