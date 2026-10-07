#ifndef NANOKVM_CAPTURE_WORKER_H
#define NANOKVM_CAPTURE_WORKER_H
#include <stdint.h>
#include "kvm_vision.h"

/* A native thread paces H.264/H.265 reads at the requested rate and assembles
   each access unit into one of two slots. The consumer waits on ready_fd
   (an eventfd) only after setting consumer_waiting, so a frame costs at most
   one wakeup. Freeing a slot rings the doorbell only if producer_waiting. */
enum { NK_SLOT_FREE = 0, NK_SLOT_READY = 1 };
typedef struct {
    int32_t state;           /* atomic: NK_SLOT_FREE or NK_SLOT_READY */
    int32_t result;          /* native result of the read */
    uint32_t size, total;    /* size < total: incomplete access unit */
    uint8_t *data;           /* producer-owned while FREE */
    uint32_t capacity;
} nk_capture_slot;
typedef struct {
    nk_capture_slot slot[2];
    int32_t consumer_waiting, producer_waiting; /* atomic */
    uint32_t reads, signals, producer_waits;    /* atomic statistics */
} nk_capture_shared;
typedef struct {
    uint16_t width, height, bitrate;
    uint8_t codec, gop, fps;
} nk_capture_params;

/* Caller owns ready_fd; the worker owns the doorbell, which the consumer may
   only write (eight bytes) while the worker exists. */
typedef struct nk_capture_worker nk_capture_worker;
nk_capture_worker *nk_capture_create(int *ready_fd, int *doorbell_fd,
    nk_capture_shared **shared);
/* Starts pacing: the first read is one period from now. */
void nk_capture_start(nk_capture_worker *, const nk_capture_params *);
/* Applies to the next read; an fps change restarts the cadence. */
void nk_capture_update(nk_capture_worker *, const nk_capture_params *);
/* Stops starting reads and waits for the one in flight. */
void nk_capture_stop(nk_capture_worker *);
/* Counted exclusion for other native operations; waits for the read in flight. */
void nk_capture_pause(nk_capture_worker *);
void nk_capture_resume(nk_capture_worker *);
/* Joins an in-flight native read; does not forcibly cancel the vendor library. */
void nk_capture_destroy(nk_capture_worker *);
/* Next deadline after one at previous, observed at now: retains phase after a
   short delay but drops stale work after a stall (advanceCaptureDeadline). */
int64_t nk_capture_advance(int64_t previous, int64_t now, int64_t period);
#endif
