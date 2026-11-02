//go:build !teststub

package common

/*
#cgo CFLAGS: -I../../support/additional/kvm/include
#include "jpeg_worker.h"
*/
import "C"

import (
	"os"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	log "github.com/sirupsen/logrus"
)

// jpegWorker runs JPEG and tiled MJPEG reads on a native thread. A direct cgo
// call lasted the whole read (four SADs and the tile encodes, often tens of
// milliseconds) and called back into Go for every pack: on the single P each
// read woke sysmon, lost the P to a retake and moved the read between threads.
// Here the goroutine only waits in the netpoller; the request and the wakeups
// are raw syscalls, and the message is copied once. Go still decides every
// parameter of every read.
type jpegWorker struct {
	native   *C.nk_jpeg_worker
	shared   *C.nk_jpeg_shared
	ready    *os.File
	readyRaw syscall.RawConn
	doorbell uintptr
}

// Guarded by KvmVision.mutex, which every JPEG read holds.
var (
	nativeJpegWorker       *jpegWorker
	nativeJpegWorkerFailed = os.Getenv("NANOKVM_NATIVE_JPEG_WORKER") == "0"
)

// jpegWorkerLocked starts the worker on first use; nil selects direct cgo reads.
func jpegWorkerLocked() *jpegWorker {
	if nativeJpegWorkerFailed {
		return nil
	}
	if nativeJpegWorker != nil {
		return nativeJpegWorker
	}
	var readyFD, doorbellFD C.int
	var shared *C.nk_jpeg_shared
	native := C.nk_jpeg_create(&readyFD, &doorbellFD, &shared)
	if native == nil {
		nativeJpegWorkerFailed = true
		log.Warn("native JPEG worker unavailable; using direct reads")
		return nil
	}
	ready := os.NewFile(uintptr(readyFD), "nanokvm-jpeg-ready")
	readyRaw, err := ready.SyscallConn()
	if err == nil {
		err = ready.SetReadDeadline(time.Time{})
	}
	if err != nil {
		C.nk_jpeg_destroy(native)
		ready.Close()
		nativeJpegWorkerFailed = true
		log.WithError(err).Warn("native JPEG worker notification is not pollable; using direct reads")
		return nil
	}
	nativeJpegWorker = &jpegWorker{native: native, shared: shared, ready: ready, readyRaw: readyRaw, doorbell: uintptr(doorbellFD)}
	return nativeJpegWorker
}

// closeJpegWorkerLocked stops the worker; no read is in flight under the lock.
func closeJpegWorkerLocked() {
	if nativeJpegWorker == nil {
		return
	}
	C.nk_jpeg_destroy(nativeJpegWorker.native)
	nativeJpegWorker.ready.Close()
	nativeJpegWorker = nil
}

// read runs one request and returns the assembled bytes as a pack store.
// Every caller holds KvmVision's write lock, so requests never overlap; a
// request found in flight (a caller without that lock) is waited for, never
// overwritten, and never run beside a direct native read.
func (w *jpegWorker) read(request C.int32_t, width, height, quality uint16, key bool, maxTiles uint16) (state *videoPackStorage, result int) {
	s := w.shared
	if atomic.LoadInt32((*int32)(unsafe.Pointer(&s.request))) != C.NK_JPEG_IDLE {
		log.Error("native JPEG worker request already in flight; waiting for it")
		for atomic.LoadInt32((*int32)(unsafe.Pointer(&s.request))) != C.NK_JPEG_IDLE {
			time.Sleep(time.Millisecond)
		}
	}
	s.width, s.height, s.quality, s.max_tiles = C.uint16_t(width), C.uint16_t(height), C.uint16_t(quality), C.uint16_t(maxTiles)
	s.key = C.uint8_t(boolToInt(key))
	// The atomic store publishes the plain parameter stores above: it is a
	// release on riscv64, paired with the worker's __ATOMIC_ACQUIRE load.
	pending := (*int32)(unsafe.Pointer(&s.request))
	atomic.StoreInt32(pending, int32(request))
	one := uint64(1)
	syscall.RawSyscall(syscall.SYS_WRITE, w.doorbell, uintptr(unsafe.Pointer(&one)), 8)
	var signal [8]byte
	for atomic.LoadInt32(pending) != C.NK_JPEG_IDLE {
		if err := w.wait(&signal); err != nil {
			// The native read must end before anything else enters the
			// library: poll for it, then use direct reads from now on.
			log.WithError(err).Error("native JPEG worker wait failed; using direct reads")
			for atomic.LoadInt32(pending) != C.NK_JPEG_IDLE {
				time.Sleep(time.Millisecond)
			}
			nativeJpegWorkerFailed = true
			break
		}
	}
	state = &videoPackStorage{}
	if s.size > 0 {
		state.appendPack(unsafe.Slice((*byte)(unsafe.Pointer(s.data)), int(s.size)), 0, int(s.total))
	}
	return state, int(s.result)
}

// wait consumes the ready eventfd: netpoller wait, raw read.
func (w *jpegWorker) wait(signal *[8]byte) error {
	var readErr error
	err := w.readyRaw.Read(func(fd uintptr) bool {
		_, _, errno := syscall.RawSyscall(syscall.SYS_READ, fd, uintptr(unsafe.Pointer(&signal[0])), uintptr(len(signal)))
		if errno == syscall.EAGAIN {
			return false
		}
		if errno != 0 && errno != syscall.EINTR {
			readErr = os.NewSyscallError("read", errno)
		}
		return true
	})
	if err != nil {
		return err
	}
	return readErr
}

func (w *jpegWorker) readPicture(width, height, quality uint16) (*videoPackStorage, int) {
	return w.read(C.NK_JPEG_PICTURE, width, height, quality, false, 0)
}

func (w *jpegWorker) readTiles(width, height, quality uint16, key bool, maxTiles uint16) (*videoPackStorage, int) {
	return w.read(C.NK_JPEG_TILES, width, height, quality, key, maxTiles)
}
