#ifndef NANOKVM_CAPTURE_WORKER_H
#define NANOKVM_CAPTURE_WORKER_H
#include <stdint.h>
#include "kvm_vision.h"
typedef struct nk_capture_worker nk_capture_worker;
/* One outstanding request. Caller owns read_fd; worker owns its write end.
   It receives one byte per borrowed pack and one when the read completes. */
nk_capture_worker *nk_capture_create(int *read_fd);
int nk_capture_submit_video(nk_capture_worker *, uint16_t width, uint16_t height,
    uint8_t codec, uint16_t bitrate, uint8_t gop, uint8_t fps);
int nk_capture_submit_mjpeg(nk_capture_worker *, uint16_t width, uint16_t height,
    uint16_t quality);
/* After each notification: copies the borrowed pack through sink (*done 0),
   or reports the read result and returns to idle (*done 1). EAGAIN otherwise. */
int nk_capture_take(nk_capture_worker *, kvmv_video_sink sink, uintptr_t context,
    int *done, int *result);
/* Aborts a borrowed pack and joins an in-flight native read; does not
   forcibly cancel the vendor library. */
void nk_capture_destroy(nk_capture_worker *);
#endif
