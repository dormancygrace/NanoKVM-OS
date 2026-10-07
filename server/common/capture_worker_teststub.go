//go:build teststub && cgo

package common

/*
#include <stdint.h>
void nk_fake_capture_script(const uint8_t *data, const uint32_t *sizes, int count,
	int result, uint32_t total_skew, int offset_skew);
int nk_fake_capture_last(uint16_t *args);
*/
import "C"

// fakeCaptureScript sets the packs and result of the following native reads.
// totalSkew and offsetSkew produce incomplete and malformed access units.
func fakeCaptureScript(packs [][]byte, result int, totalSkew uint32, offsetSkew int) {
	var data []byte
	sizes := make([]C.uint32_t, len(packs)+1)
	for i, pack := range packs {
		data = append(data, pack...)
		sizes[i] = C.uint32_t(len(pack))
	}
	if len(data) > 4096 || len(packs) > 8 {
		panic("fake capture script is too large")
	}
	data = append(data, 0)
	C.nk_fake_capture_script((*C.uint8_t)(&data[0]), &sizes[0], C.int(len(packs)), C.int(result), C.uint32_t(totalSkew), C.int(offsetSkew))
}

// fakeCaptureLast reports width, height, codec, rate, gop and fps of the last
// native read, and the last nonzero result returned by its sink.
func fakeCaptureLast() ([6]uint16, int) {
	var args [6]C.uint16_t
	sink := int(C.nk_fake_capture_last(&args[0]))
	var out [6]uint16
	for i, v := range args {
		out[i] = uint16(v)
	}
	return out, sink
}
