//go:build !teststub

package common

/*
#cgo CFLAGS: -I../include
#include "kvm_vision.h"
extern int goVideoPack(uintptr_t context, void *data, uint32_t size, uint32_t offset, uint32_t total);
static inline int nkosVideoPack(uintptr_t context, const uint8_t *data, uint32_t size, uint32_t offset, uint32_t total) {
 return goVideoPack(context, (void *)data, size, offset, total);
}
static inline int nkosReadVideo(uint16_t w, uint16_t h, uint8_t codec, uint16_t rate, uint8_t gop, uint8_t fps, uintptr_t context) {
 return kvmv_read_video_sink(w, h, codec, rate, gop, fps, nkosVideoPack, context);
}
*/
import "C"
import (
	"runtime/cgo"
	"unsafe"
)

//export goVideoPack
func goVideoPack(context C.uintptr_t, data unsafe.Pointer, size, offset, total C.uint32_t) C.int {
	state := cgo.Handle(context).Value().(*videoPackStorage)
	if data == nil || size == 0 || uint64(size) > 64<<20 {
		state.failed = true
		return -1
	}
	if !state.appendPack(unsafe.Slice((*byte)(data), int(size)), int(offset), int(total)) {
		return -1
	}
	return 0
}

func readVideoIntoOwnedStorage(width, height uint16, codec uint8, rate uint16, gop, fps uint8, headroom int) ([]byte, []byte, int) {
	state := &videoPackStorage{headroom: headroom}
	handle := cgo.NewHandle(state)
	defer handle.Delete()
	result := int(C.nkosReadVideo(C.uint16_t(width), C.uint16_t(height), C.uint8_t(codec), C.uint16_t(rate), C.uint8_t(gop), C.uint8_t(fps), C.uintptr_t(handle)))
	if result < 0 {
		return nil, nil, result
	}
	if !state.complete() {
		return nil, nil, -2
	}
	return state.storage, state.storage[headroom:], result
}
