//go:build cgo

package common

/*
#include <stdint.h>
*/
import "C"
import (
	"runtime/cgo"
	"unsafe"
)

// Shared by the blocking reads and the capture worker. data is borrowed for
// the duration of the call; appendPack copies it into Go-owned storage.
//
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
