//go:build teststub && cgo

package common

/*
#include <stdint.h>
void nk_fake_capture_reset(int block);
int nk_fake_capture_push(const uint8_t *data, const uint32_t *sizes, int count,
	int result, uint32_t total_skew, int offset_skew);
void nk_fake_capture_unblock(void);
int nk_fake_capture_reads(uint16_t *args, int64_t *times, int max, int *waiting);
int64_t nk_capture_advance(int64_t previous, int64_t now, int64_t period);
*/
import "C"
import "time"

// fakeCaptureReset empties the frame queue. With block, a read of an empty
// queue waits for fakeCapturePush or fakeCaptureUnblock.
func fakeCaptureReset(block bool) {
	value := 0
	if block {
		value = 1
	}
	C.nk_fake_capture_reset(C.int(value))
}

// fakeCapturePush queues one native read. totalSkew and offsetSkew produce
// incomplete and malformed access units.
func fakeCapturePush(packs [][]byte, result int, totalSkew uint32, offsetSkew int) {
	var data []byte
	sizes := make([]C.uint32_t, len(packs)+1)
	for i, pack := range packs {
		data = append(data, pack...)
		sizes[i] = C.uint32_t(len(pack))
	}
	if len(data) > 4096 {
		panic("fake capture frame is too large")
	}
	data = append(data, 0) // never empty
	if C.nk_fake_capture_push((*C.uint8_t)(&data[0]), &sizes[0], C.int(len(packs)), C.int(result), C.uint32_t(totalSkew), C.int(offsetSkew)) != 0 {
		panic("fake capture queue is full")
	}
}

func fakeCaptureUnblock() { C.nk_fake_capture_unblock() }

type fakeCaptureRead struct {
	args [6]uint16 // width, height, codec, bitrate, gop, fps
	at   time.Duration
}

// fakeCaptureReads lists the reads started so far and whether one waits
// for a frame.
func fakeCaptureReads() ([]fakeCaptureRead, bool) {
	var args [64 * 6]C.uint16_t
	var times [64]C.int64_t
	var waiting C.int
	count := int(C.nk_fake_capture_reads(&args[0], &times[0], 64, &waiting))
	reads := make([]fakeCaptureRead, min(count, 64))
	for i := range reads {
		for j := range 6 {
			reads[i].args[j] = uint16(args[6*i+j])
		}
		reads[i].at = time.Duration(times[i])
	}
	return reads, waiting != 0
}

func captureAdvance(previous, now, period time.Duration) time.Duration {
	return time.Duration(C.nk_capture_advance(C.int64_t(previous), C.int64_t(now), C.int64_t(period)))
}
