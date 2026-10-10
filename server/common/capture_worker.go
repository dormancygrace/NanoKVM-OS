//go:build cgo

package common

/*
#cgo CFLAGS: -I../include
#cgo LDFLAGS: -lpthread
#include "capture_worker.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// videoCaptureWorker owns the native capture thread. That thread paces the
// reads, waits in the vendor library and assembles each access unit into a
// C-owned slot. Go waits for a slot through the netpoller and copies it into
// Go-owned storage without a cgo call; nothing calls back from C into Go and
// C never sees a Go pointer.
type videoCaptureWorker struct {
	native   *C.nk_capture_worker
	shared   *C.nk_capture_shared
	ready    *os.File
	doorbell uintptr
	next     int // consumer slot; used only by the active VideoCapture

	frames, waits, rings, updates, pauses atomic.Uint64
	debugStop, debugDone                  chan struct{}
}

func newVideoCaptureWorker() (*videoCaptureWorker, error) {
	var readyFD, doorbellFD C.int
	var shared *C.nk_capture_shared
	native := C.nk_capture_create(&readyFD, &doorbellFD, &shared)
	if native == nil {
		return nil, fmt.Errorf("create native capture worker")
	}
	ready := os.NewFile(uintptr(readyFD), "nanokvm-capture-ready")
	// SetReadDeadline rejects non-pollable descriptors. Deadlines are used only
	// to interrupt a waiting consumer; native reads end by themselves.
	if err := ready.SetReadDeadline(time.Time{}); err != nil {
		C.nk_capture_destroy(native)
		ready.Close()
		return nil, fmt.Errorf("capture notification is not pollable: %w", err)
	}
	return &videoCaptureWorker{native: native, shared: shared, ready: ready, doorbell: uintptr(doorbellFD)}, nil
}

func (w *videoCaptureWorker) slotState(index int) *int32 {
	return (*int32)(unsafe.Pointer(&w.shared.slot[index].state))
}

func (w *videoCaptureWorker) sharedFlag(flag *C.int32_t) *int32 {
	return (*int32)(unsafe.Pointer(flag))
}

// Copies the next ready slot into Go-owned storage and returns the slot. A
// raw write wakes the capture thread only if it is waiting for this slot: it
// cannot block and does not release the P like a syscall or cgo call would.
func (w *videoCaptureWorker) take(headroom int) ([]byte, []byte, int) {
	slot := &w.shared.slot[w.next]
	state := videoPackStorage{headroom: headroom}
	if slot.size > 0 {
		state.appendPack(unsafe.Slice((*byte)(unsafe.Pointer(slot.data)), int(slot.size)), 0, int(slot.total))
	}
	result := int(slot.result)
	w.release()
	return state.videoResult(result)
}

func (w *videoCaptureWorker) release() {
	atomic.StoreInt32(w.slotState(w.next), C.NK_SLOT_FREE)
	w.next ^= 1
	if atomic.LoadInt32(w.sharedFlag(&w.shared.producer_waiting)) != 0 {
		one := uint64(1)
		syscall.RawSyscall(syscall.SYS_WRITE, w.doorbell, uintptr(unsafe.Pointer(&one)), 8)
		w.rings.Add(1)
	}
}

func (w *videoCaptureWorker) slotReady() bool {
	return atomic.LoadInt32(w.slotState(w.next)) == C.NK_SLOT_READY
}

// wait blocks in the netpoller until the next slot is ready. The flag is
// published before the final check, so the capture thread signals exactly
// when the consumer may sleep; a stale signal only causes another check.
func (w *videoCaptureWorker) wait() error {
	waiting := w.sharedFlag(&w.shared.consumer_waiting)
	var signal [8]byte
	for !w.slotReady() {
		atomic.StoreInt32(waiting, 1)
		if w.slotReady() {
			atomic.StoreInt32(waiting, 0)
			break
		}
		if _, err := w.ready.Read(signal[:]); err != nil {
			atomic.StoreInt32(waiting, 0)
			return err
		}
		w.waits.Add(1)
	}
	return nil
}

// Drops slots completed after the consumer stopped. The thread is stopped.
func (w *videoCaptureWorker) discard() {
	for w.slotReady() {
		w.release()
	}
}

func captureParams(p VideoCaptureParams) C.nk_capture_params {
	return C.nk_capture_params{width: C.uint16_t(p.Width), height: C.uint16_t(p.Height),
		bitrate: C.uint16_t(p.BitRate), codec: C.uint8_t(p.Codec), gop: C.uint8_t(p.GOP), fps: C.uint8_t(p.FPS)}
}

func (w *videoCaptureWorker) pause() {
	w.pauses.Add(1)
	C.nk_capture_pause(w.native)
}

func (w *videoCaptureWorker) resume() { C.nk_capture_resume(w.native) }

// The native thread is joined before the notification descriptor is closed.
func (w *videoCaptureWorker) close() {
	if w.debugStop != nil {
		close(w.debugStop)
		<-w.debugDone
	}
	C.nk_capture_destroy(w.native)
	w.ready.Close()
}

// VideoCapture is one stream paced by the native capture thread. Next is used
// by a single goroutine; Stop may be called from any goroutine.
type VideoCapture struct {
	worker  *videoCaptureWorker
	stopped atomic.Bool
	params  VideoCaptureParams
	failed  error
	closing func(*VideoCapture)
	once    sync.Once
	done    chan struct{}
}

func (w *videoCaptureWorker) start(params VideoCaptureParams, closing func(*VideoCapture)) *VideoCapture {
	w.ready.SetReadDeadline(time.Time{})
	native := captureParams(params)
	C.nk_capture_start(w.native, &native)
	return &VideoCapture{worker: w, params: params, closing: closing, done: make(chan struct{})}
}

// Update publishes changed parameters to the capture thread; they apply to
// its next read. Unchanged parameters cost no cgo call.
func (c *VideoCapture) Update(params VideoCaptureParams) {
	if params == c.params {
		return
	}
	c.params = params
	native := captureParams(params)
	C.nk_capture_update(c.worker.native, &native)
	c.worker.updates.Add(1)
}

// Next waits for the next access unit with headroom bytes before it. Results
// and storage match ReadVideoWithHeadroom. ok is false after Stop or when the
// worker failed; the caller then calls Close.
func (c *VideoCapture) Next(headroom int) (storage []byte, data []byte, result int, ok bool) {
	if c.stopped.Load() || c.failed != nil {
		return nil, nil, -1, false
	}
	if err := c.worker.wait(); err != nil {
		if !c.stopped.Load() || !errors.Is(err, os.ErrDeadlineExceeded) {
			c.failed = err
		}
		return nil, nil, -1, false
	}
	c.worker.frames.Add(1)
	storage, data, result = c.worker.take(headroom)
	return storage, data, result, true
}

// Stop interrupts a waiting Next promptly.
func (c *VideoCapture) Stop() {
	if !c.stopped.Swap(true) {
		c.worker.ready.SetReadDeadline(time.Unix(1, 0))
	}
}

// Close stops the capture thread, waits for its read in flight and drops
// frames nobody consumed, so the next stream starts clean.
func (c *VideoCapture) Close() {
	c.once.Do(func() {
		c.Stop()
		C.nk_capture_stop(c.worker.native)
		c.worker.discard()
		atomic.StoreInt32(c.worker.sharedFlag(&c.worker.shared.consumer_waiting), 0)
		if c.closing != nil {
			c.closing(c)
		}
		close(c.done)
	})
}

// Err reports why Next stopped other than Stop.
func (c *VideoCapture) Err() error { return c.failed }

// captureStats counts handoffs. One wakeup per frame means waits and signals
// at most equal frames; rings count Go waking a throttled capture thread.
type captureStats struct {
	frames, reads, signals, waits, rings, producerWaits, updates, pauses uint64
}

func (w *videoCaptureWorker) stats() captureStats {
	load := func(counter *C.uint32_t) uint64 { return uint64(atomic.LoadUint32((*uint32)(unsafe.Pointer(counter)))) }
	return captureStats{
		frames: w.frames.Load(), reads: load(&w.shared.reads), signals: load(&w.shared.signals),
		waits: w.waits.Load(), rings: w.rings.Load(), producerWaits: load(&w.shared.producer_waits),
		updates: w.updates.Load(), pauses: w.pauses.Load(),
	}
}

func (s captureStats) since(previous captureStats) captureStats {
	// Native counters are 32-bit; uint32 subtraction handles their wrap.
	wrap := func(now, before uint64) uint64 { return uint64(uint32(now) - uint32(before)) }
	return captureStats{
		frames: s.frames - previous.frames, reads: wrap(s.reads, previous.reads),
		signals: wrap(s.signals, previous.signals), waits: s.waits - previous.waits,
		rings: s.rings - previous.rings, producerWaits: wrap(s.producerWaits, previous.producerWaits),
		updates: s.updates - previous.updates, pauses: s.pauses - previous.pauses,
	}
}

// NANOKVM_CAPTURE_DEBUG=1 logs the handoffs of each interval.
func (w *videoCaptureWorker) startDebugLog(interval time.Duration, logf func(string, ...any)) {
	w.debugStop, w.debugDone = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(w.debugDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		last := w.stats()
		for {
			select {
			case <-w.debugStop:
				return
			case <-ticker.C:
			}
			now := w.stats()
			d := now.since(last)
			last = now
			logf("capture worker %v: frames=%d reads=%d ready-signals=%d go-waits=%d producer-rings=%d producer-waits=%d updates=%d pauses=%d",
				interval, d.frames, d.reads, d.signals, d.waits, d.rings, d.producerWaits, d.updates, d.pauses)
		}
	}()
}
