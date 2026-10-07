//go:build cgo

package common

/*
#cgo CFLAGS: -I../include
#cgo LDFLAGS: -lpthread
#include "capture_worker.h"
extern int goVideoPack(uintptr_t context, void *data, uint32_t size, uint32_t offset, uint32_t total);
static int nkosCapturePack(uintptr_t context, const uint8_t *data, uint32_t size, uint32_t offset, uint32_t total) {
 return goVideoPack(context, (void *)data, size, offset, total);
}
static inline int nkosCaptureTake(nk_capture_worker *w, uintptr_t context, int *done, int *result) {
 return nk_capture_take(w, nkosCapturePack, context, done, result);
}
*/
import "C"
import (
	"fmt"
	"os"
	"runtime/cgo"
	"time"
)

// videoCaptureWorker runs the blocking native read on its own pthread. The
// goroutine waits for its notifications through the netpoller instead of
// holding an M in cgo for a whole frame period, and copies each pack with a
// short cgo call while the native read still holds the vendor stream.
type videoCaptureWorker struct {
	native *C.nk_capture_worker
	ready  *os.File
}

func newVideoCaptureWorker() (*videoCaptureWorker, error) {
	var fd C.int
	native := C.nk_capture_create(&fd)
	if native == nil {
		return nil, fmt.Errorf("create native capture worker")
	}
	ready := os.NewFile(uintptr(fd), "nanokvm-capture-ready")
	// SetReadDeadline rejects non-pollable descriptors. No deadline is imposed:
	// native capture cancellation remains the vendor library's responsibility.
	if err := ready.SetReadDeadline(time.Time{}); err != nil {
		C.nk_capture_destroy(native)
		ready.Close()
		return nil, fmt.Errorf("capture notification is not pollable: %w", err)
	}
	return &videoCaptureWorker{native: native, ready: ready}, nil
}

// Caller serializes reads and close using KvmVision.mutex. Parameters are
// passed per call, as with kvmv_read_video_sink. An error means the
// notification invariant is broken; the caller must close the worker.
func (w *videoCaptureWorker) readVideo(width, height uint16, codec uint8, bitrate uint16, gop, fps uint8, state *videoPackStorage) (int, error) {
	if err := w.submitVideo(width, height, codec, bitrate, gop, fps); err != nil {
		return -1, err
	}
	return w.collect(state)
}

func (w *videoCaptureWorker) submitVideo(width, height uint16, codec uint8, bitrate uint16, gop, fps uint8) error {
	if code := C.nk_capture_submit_video(w.native, C.uint16_t(width), C.uint16_t(height), C.uint8_t(codec), C.uint16_t(bitrate), C.uint8_t(gop), C.uint8_t(fps)); code != 0 {
		return fmt.Errorf("submit native capture: %d", code)
	}
	return nil
}

func (w *videoCaptureWorker) readMjpeg(width, height, quality uint16, state *videoPackStorage) (int, error) {
	if code := C.nk_capture_submit_mjpeg(w.native, C.uint16_t(width), C.uint16_t(height), C.uint16_t(quality)); code != 0 {
		return -1, fmt.Errorf("submit native capture: %d", code)
	}
	return w.collect(state)
}

// Each notification is followed by exactly one take: a borrowed pack is
// appended to state, and the final one carries the native result. No Go
// pointer is retained by C; the handle lives only for this read.
func (w *videoCaptureWorker) collect(state *videoPackStorage) (int, error) {
	handle := cgo.NewHandle(state)
	defer handle.Delete()
	var signal [1]byte
	for {
		if _, err := w.ready.Read(signal[:]); err != nil {
			return -1, fmt.Errorf("wait for native capture: %w", err)
		}
		var done, result C.int
		if code := C.nkosCaptureTake(w.native, C.uintptr_t(handle), &done, &result); code != 0 {
			return -1, fmt.Errorf("take native capture: %d", code)
		}
		if done != 0 {
			return int(result), nil
		}
	}
}

// Aborts a borrowed pack and joins the native thread before the read end of
// the notification pipe is closed.
func (w *videoCaptureWorker) close() {
	C.nk_capture_destroy(w.native)
	w.ready.Close()
}
