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
static inline int nkosReadMjpeg(uint16_t w, uint16_t h, uint16_t quality, uintptr_t context) {
 return kvmv_read_mjpeg_sink(w, h, quality, nkosVideoPack, context);
}
*/
import "C"
import "runtime/cgo"

// Blocking path: the calling M waits inside cgo for the next access unit.
func readVideoIntoOwnedStorage(width, height uint16, codec uint8, rate uint16, gop, fps uint8, headroom int) ([]byte, []byte, int) {
	state := &videoPackStorage{headroom: headroom}
	handle := cgo.NewHandle(state)
	defer handle.Delete()
	result := int(C.nkosReadVideo(C.uint16_t(width), C.uint16_t(height), C.uint8_t(codec), C.uint16_t(rate), C.uint8_t(gop), C.uint8_t(fps), C.uintptr_t(handle)))
	return state.videoResult(result)
}

// JPEG is one borrowed pack. The returned bytes are owned by Go before the
// native callback returns, allowing JPU and VI leases to be released promptly.
func readMjpegIntoOwnedStorage(width, height, quality uint16) ([]byte, int) {
	state := &videoPackStorage{}
	handle := cgo.NewHandle(state)
	defer handle.Delete()
	result := int(C.nkosReadMjpeg(C.uint16_t(width), C.uint16_t(height), C.uint16_t(quality), C.uintptr_t(handle)))
	return state.mjpegResult(result)
}
