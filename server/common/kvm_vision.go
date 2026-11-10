//go:build !teststub

package common

/*
	#cgo CFLAGS: -I../../support/additional/kvm/include
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
    static int set_first_capture_yuv420(unsigned char want) {
        int (*fn)(unsigned char) = (int (*)(unsigned char))dlsym(RTLD_DEFAULT, "kvmv_set_first_capture_yuv420");
        return fn ? fn(want) : -1;
    }
    static int capture_reclaim_pending(void) {
        int (*fn)(void) = (int (*)(void))dlsym(RTLD_DEFAULT, "kvmv_capture_reclaim_pending");
        // An older library cannot report completion: retain conservative retries.
        return fn ? fn() : 1;
    }
    static int set_capture_parked(unsigned char parked) {
        int (*fn)(unsigned char) = (int (*)(unsigned char))dlsym(RTLD_DEFAULT, "kvmv_set_capture_parked");
        return fn ? fn(parked) : -1;
    }
    static int mjpeg_tiles_release(void) {
        int (*fn)(void) = (int (*)(void))dlsym(RTLD_DEFAULT, "kvmv_mjpeg_tiles_release");
        return fn ? fn() : 0;
    }
    static int read_bgr_planar(unsigned short width, unsigned short height, unsigned char *out, unsigned int size) {
        int (*fn)(unsigned short, unsigned short, unsigned char *, unsigned int) =
            (int (*)(unsigned short, unsigned short, unsigned char *, unsigned int))dlsym(RTLD_DEFAULT, "kvmv_read_bgr_planar");
        return fn ? fn(width, height, out, size) : -9;
    }
*/
import "C"
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	log "github.com/sirupsen/logrus"
)

var (
	kvmVision     *KvmVision
	kvmVisionOnce sync.Once
)

type KvmVision struct {
	mutex  sync.RWMutex
	closed bool
	// H.264/H.265 streams are paced by a native capture thread and waited for
	// through the netpoller. NANOKVM_NATIVE_CAPTURE_WORKER=0 selects the
	// previous Go-paced blocking reads.
	captureWorkerEnabled bool
	captureWorker        *videoCaptureWorker
	capture              *VideoCapture
}

func init() {
	// Older native libraries lack the symbol; they process every input frame.
	applyCaptureFPS = func(fps int) {
		if !VideoDisabled() {
			C.set_capture_fps(C.uchar(fps))
		}
	}
}

func GetKvmVision() *KvmVision {
	kvmVisionOnce.Do(func() {
		if VideoDisabled() {
			kvmVision = &KvmVision{closed: true}
			return
		}
		kvmVision = &KvmVision{captureWorkerEnabled: os.Getenv("NANOKVM_NATIVE_CAPTURE_WORKER") != "0"}
		if !kvmVision.captureWorkerEnabled {
			log.Info("native capture worker disabled; using Go-paced blocking frame reads")
		}

		// kvmv_init starts the first capture; older libraries lack the symbol
		// and start in 4:2:2. FirstCaptureYUV420 takes monitorMutex, so no
		// monitorMutex holder may be first to call GetKvmVision: main calls it
		// (Enable/DisableHdmiCapture) before the monitor profile goroutine.
		C.set_first_capture_yuv420(C.uchar(boolToInt(FirstCaptureYUV420())))
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

func (k *KvmVision) ReadMjpeg(width uint16, height uint16, quality uint16) (data []byte, result int) {
	// Every JPEG capture passes here: the stream, MCP and the screenshots.
	// Native capture refuses a 3840x2160 input under its own mutex; after a
	// refusal readMjpegChecked holds the next reads off that lock, which the
	// video reader shares.
	data, result = readMjpegChecked(func() ([]byte, int) {
		k.mutex.Lock()
		defer k.mutex.Unlock()
		if k.closed {
			return nil, -1
		}
		// No pause of the video stream: see pauseCaptureLocked.
		return readMjpegIntoOwnedStorage(width, height, quality)
	})
	// A refusal repeats on every tick while the input is 3840x2160.
	if result < 0 && result != MjpegBlockedResult {
		log.Errorf("failed to read kvm image: %v", result)
	}
	return
}

// ReadMjpegTiles reads tiled MJPEG (kvm_vision.h): MjpegTilesResult with a
// message, 0 with a bare JPEG of the whole picture, 5 when nothing changed,
// MjpegTilesUnsupported from a library without it.
func (k *KvmVision) ReadMjpegTiles(width, height, quality uint16, key bool, maxTiles uint16) (data []byte, result int) {
	data, result = readMjpegChecked(func() ([]byte, int) {
		k.mutex.Lock()
		defer k.mutex.Unlock()
		if k.closed {
			return nil, -1
		}
		return readMjpegTilesIntoOwnedStorage(width, height, quality, key, maxTiles)
	})
	if result < 0 && result != MjpegBlockedResult && result != MjpegTilesUnsupported {
		log.Errorf("failed to read tiled kvm image: %v", result)
	}
	return
}

// ReleaseMjpegTiles frees what tiled reads keep once nobody reads tiles.
func (k *KvmVision) ReleaseMjpegTiles() {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if !k.closed {
		C.mjpeg_tiles_release()
	}
}

// ReadVideoWithHeadroom is the Go-paced blocking read: the calling M waits
// inside cgo for the next access unit.
func (k *KvmVision) ReadVideoWithHeadroom(width uint16, height uint16, codec uint8, bitRate uint16, fps uint8, headroom int) (storage []byte, data []byte, result int) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed || headroom < 0 {
		return nil, nil, -1
	}
	defer k.pauseCaptureLocked()()

	return readVideoIntoOwnedStorage(width, height, codec, bitRate, fps, headroom)
}

// StartVideoCapture starts a stream paced by the native capture thread. nil
// selects Go-paced ReadVideoWithHeadroom: the worker is disabled or failed,
// vision is closed, or the previous stream has not been closed yet.
func (k *KvmVision) StartVideoCapture(params VideoCaptureParams) VideoStream {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed || k.capture != nil {
		return nil
	}
	worker := k.captureWorkerLocked()
	if worker == nil {
		return nil
	}
	k.capture = worker.start(params, k.releaseCapture)
	return k.capture
}

// Runs from VideoCapture.Close after its thread stopped and its frames were
// dropped. A broken notification sequence retires the worker for this process;
// the aborted access unit may have been a reference, so the next one is an IDR.
func (k *KvmVision) releaseCapture(capture *VideoCapture) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.capture == capture {
		k.capture = nil
	}
	if err := capture.Err(); err != nil && k.captureWorker == capture.worker {
		log.Errorf("native capture worker failed; using Go-paced blocking frame reads: %v", err)
		k.captureWorker.close()
		k.captureWorker = nil
		k.captureWorkerEnabled = false
		C.kvmv_request_keyframe()
	}
}

// captureWorkerLocked starts the native capture thread on first use. nil
// selects the blocking read. The caller holds k.mutex for writing.
func (k *KvmVision) captureWorkerLocked() *videoCaptureWorker {
	if !k.captureWorkerEnabled || k.captureWorker != nil {
		return k.captureWorker
	}
	worker, err := newVideoCaptureWorker()
	if err != nil {
		log.Errorf("native capture worker unavailable; using blocking frame reads: %v", err)
		k.captureWorkerEnabled = false
		return nil
	}
	k.captureWorker = worker
	if os.Getenv("NANOKVM_CAPTURE_DEBUG") == "1" {
		worker.startDebugLog(10*time.Second, log.Infof)
	}
	log.Info("native capture worker enabled")
	return worker
}

// Go-paced reads held k.mutex for their whole duration. The native-paced
// stream reads without it, so a native operation that changes the capture
// pauses the stream and waits for its read in flight. The caller holds
// k.mutex (either mode) and calls the returned function when done.
//
// A JPEG read is not such an operation: the library runs it beside a video
// read under its own locks (vi_mutex, the JPEG reader mutex and the JPEG
// hold), and on MJPEG's own output it waits for its frame and encodes with
// vi_mutex released so that the video stream reads on. Pausing the stream for
// every JPEG read made the two take turns: a 2560x1440 H.265 stream (15 ms a
// frame in the encoder) beside an MJPEG viewer (about 22 ms a read: its frame
// at 33 fps, the change map, the JPEG) ran at 27 fps each, with a third of the
// CPU idle. Without the pause the video stream keeps 59 to 60 fps there and
// MJPEG gets 23 to 26 (the library gives a waiting JPEG read the next turn
// at its capture lock), with the CPU 91 to 93 % busy.
func (k *KvmVision) pauseCaptureLocked() func() {
	if k.capture == nil {
		return func() {}
	}
	worker := k.captureWorker
	worker.pause()
	return worker.resume
}

func (k *KvmVision) SetHDMI(enable bool) int {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return -1
	}
	defer k.pauseCaptureLocked()()

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

// SetCaptureParked disables the VPSS outputs while nobody reads frames and
// enables them again; a frame read also resumes them. -1 means the library
// cannot park.
func (k *KvmVision) SetCaptureParked(parked bool) int {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return -1
	}
	defer k.pauseCaptureLocked()()
	return int(C.set_capture_parked(C.uchar(boolToInt(parked))))
}

// ReadBGRPlanar returns one picture of the screen scaled by VPSS to width x
// height (even, at most 1920 wide) as B, G and R planes, the TPU models' input
// layout. The capture must run: hold a capture lease while reading.
func (k *KvmVision) ReadBGRPlanar(width, height int) ([]byte, error) {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return nil, ErrPlanarPictureUnavailable
	}
	out := make([]byte, width*height*3)
	switch result := C.read_bgr_planar(C.ushort(width), C.ushort(height), (*C.uchar)(unsafe.Pointer(&out[0])), C.uint(len(out))); result {
	case 0:
		return out, nil
	case -9:
		return nil, ErrPlanarPictureUnavailable
	case -3:
		return nil, ErrPlanarPictureTimeout
	case -10:
		return nil, ErrPlanarPictureNoSignal
	case -11:
		return nil, ErrPlanarCaptureNotReady
	case -12:
		return nil, ErrPlanarCaptureUnsupported
	case -13:
		return nil, ErrPlanarInputChanged
	case -14:
		return nil, ErrPlanarInputTooLarge
	default:
		return nil, fmt.Errorf("no picture of the screen (%d)", int(result))
	}
}

// CaptureReclaimPending reports disabled outputs that still own private pools.
// It does not retry reclamation or change capture state.
func (k *KvmVision) CaptureReclaimPending() bool {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	return !k.closed && C.capture_reclaim_pending() != 0
}

func (k *KvmVision) HasHDMISignal() bool {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return false
	}

	return C.kvmv_hdmi_signal_active() != 0
}

func (k *KvmVision) SetFrameDetect(frame uint8) {
	k.mutex.RLock()
	defer k.mutex.RUnlock()
	if k.closed {
		return
	}
	defer k.pauseCaptureLocked()()

	_frame := C.uint8_t(frame)
	C.set_frame_detact(_frame)
}

func (k *KvmVision) Close() {
	k.mutex.Lock()
	if k.closed {
		k.mutex.Unlock()
		return
	}
	k.closed = true
	capture := k.capture
	k.mutex.Unlock()
	// Its reader closes the stream; that takes k.mutex, as may the reader's
	// RequestKeyframe meanwhile, so wait without holding it.
	if capture != nil {
		capture.Stop()
		<-capture.done
	}

	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.captureWorker != nil {
		k.captureWorker.close()
		k.captureWorker = nil
	}
	closeJpegWorkerLocked()
	C.kvmv_deinit()
	log.Debugf("stop kvm vision...")
}

// ApplyMonitorProfile serializes maintenance with frame reads and HDMI controls.
// Receiver maintenance preserves capture intent and MMF buffers. The utility
// pulses reset while this lock keeps capture callers away from its I2C sequence.
func (k *KvmVision) ApplyMonitorProfile(path string) (err error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed {
		return fmt.Errorf("video capture is closed")
	}
	defer k.pauseCaptureLocked()()
	cube := MonitorRequiresPowerCycle()
	if C.edid_maintenance(1) != 0 {
		return fmt.Errorf("cannot pause HDMI for monitor change")
	}
	defer func() {
		if C.edid_maintenance(0) != 0 {
			err = errors.Join(err, fmt.Errorf("cannot restore HDMI after monitor change"))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{path}
	pendingBefore := false
	if cube {
		_, statErr := os.Stat(monitorPowerCyclePendingFile)
		pendingBefore = statErr == nil
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
	// The receiver already held this EDID and its flash was not written: no
	// power cycle is needed for it, so the reminder this call raised goes.
	if cube && !pendingBefore && bytes.Contains(output, []byte(edidWriteSkipped)) {
		if err := os.Remove(monitorPowerCyclePendingFile); err != nil && !os.IsNotExist(err) {
			log.Warnf("cannot clear the power-cycle reminder: %v", err)
		}
	}
	return nil
}

// edidWriteSkipped is what nanokvm_update_edid prints when the receiver
// already holds the requested EDID.
const edidWriteSkipped = "Requested EDID already installed; flash write skipped"

// ReadMonitorEDID copies the HDMI receiver's EDID (256 bytes) under the same
// serialization as ApplyMonitorProfile: nanokvm_update_edid reads it with the
// register sequence that programming uses to compare the installed EDID.
func (k *KvmVision) ReadMonitorEDID() (data []byte, err error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed {
		return nil, fmt.Errorf("video capture is closed")
	}
	defer k.pauseCaptureLocked()()
	if C.edid_maintenance(1) != 0 {
		return nil, fmt.Errorf("cannot pause HDMI to read the monitor EDID")
	}
	defer func() {
		if C.edid_maintenance(0) != 0 {
			data = nil
			err = errors.Join(err, fmt.Errorf("cannot restore HDMI after reading the monitor EDID"))
		}
	}()
	f, err := os.CreateTemp("/run", "nanokvm-edid-*.bin")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Close(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/sbin/nanokvm_update_edid", "--dump", name).CombinedOutput()
	if err != nil {
		log.Errorf("reading the monitor EDID failed: %v: %s", err, output)
		return nil, fmt.Errorf("reading the monitor EDID failed")
	}
	data, err = os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if len(data) != 256 {
		return nil, fmt.Errorf("monitor EDID copy has %d bytes", len(data))
	}
	return data, nil
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
	if VideoDisabled() {
		return 420, "recovery"
	}
	state := uint8(C.get_mjpeg_chroma_status())
	active := uint16(420)
	if state&uint8(C.MJPEG_CHROMA_ACTIVE_422) != 0 {
		active = 422
	}
	reason := ""
	switch {
	case state&uint8(C.MJPEG_CHROMA_CAPTURE_420) != 0:
		// H.264 straight from capture: the capture itself is 4:2:0.
		reason = "h264Capture"
	case state&uint8(C.MJPEG_CHROMA_FALLBACK_ERROR) != 0:
		reason = "hardware"
	case state&uint8(C.MJPEG_CHROMA_LIMIT_WIDTH) != 0 && active == 422:
		reason = "resolution"
	case state&uint8(C.MJPEG_CHROMA_SHARED_VIDEO) != 0:
		reason = "video"
	case state&uint8(C.MJPEG_CHROMA_FRAME_DETECT) != 0:
		reason = "frameDetection"
	}
	return active, reason
}

func (k *KvmVision) SetMjpegChroma(chroma uint16) int {
	k.mutex.Lock()
	defer k.mutex.Unlock()
	if k.closed || (chroma != 420 && chroma != 422) {
		return -1
	}
	defer k.pauseCaptureLocked()()
	return int(C.set_mjpeg_chroma(C.uint8_t(boolToInt(chroma == 422))))
}
