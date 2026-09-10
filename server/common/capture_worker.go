//go:build !teststub

package common

/*
#cgo CFLAGS: -I../include
#cgo LDFLAGS: -lpthread
#include "capture_worker.h"
*/
import "C"
import (
	"fmt"
	"os"
	"time"
	"unsafe"
)

type videoCaptureWorker struct {
	native *C.nk_capture_worker
	ready  *os.File
	failed bool
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

// Caller serializes read/close using KvmVision.mutex. No Go pointer is retained
// by C. Buffer ownership transfers only after the completed notification.
func (w *videoCaptureWorker) read(width, height uint16, codec uint8, bitrate uint16, gop, fps uint8) (unsafe.Pointer, uint32, int, error) {
	if w.failed {
		return nil, 0, -1, fmt.Errorf("capture worker notification failed")
	}
	if code := C.nk_capture_submit(w.native, C.uint16_t(width), C.uint16_t(height), C.uint8_t(codec), C.uint16_t(bitrate), C.uint8_t(gop), C.uint8_t(fps)); code != 0 {
		return nil, 0, -1, fmt.Errorf("submit native capture: %d", code)
	}
	var signal [1]byte
	if _, err := w.ready.Read(signal[:]); err != nil {
		w.failed = true
		return nil, 0, -1, fmt.Errorf("wait for native capture: %w", err)
	}
	var data *C.uint8_t
	var size C.uint32_t
	var result C.int
	if code := C.nk_capture_take(w.native, &data, &size, &result); code != 0 {
		w.failed = true
		return nil, 0, -1, fmt.Errorf("take native capture: %d", code)
	}
	return unsafe.Pointer(data), uint32(size), int(result), nil
}

func (w *videoCaptureWorker) close() {
	C.nk_capture_destroy(w.native)
	w.ready.Close()
}
