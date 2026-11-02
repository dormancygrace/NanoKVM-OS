#ifndef NANOKVM_JPEG_WORKER_H
#define NANOKVM_JPEG_WORKER_H
#include <stdint.h>
#include "kvm_vision.h"

/* A native thread runs one JPEG read at a time on request: the caller fills
   the parameters, publishes request, rings the doorbell and waits on ready_fd
   (an eventfd) until request is back to NK_JPEG_IDLE. The JPEG or tiled
   message is assembled in data; the caller copies it before the next request.
   The caller serializes requests and holds the native library's lock. */
enum { NK_JPEG_IDLE = 0, NK_JPEG_TILES = 1, NK_JPEG_PICTURE = 2 };
typedef struct {
    int32_t request;              /* atomic: NK_JPEG_* */
    int32_t result;               /* native result of the read */
    uint16_t width, height, quality, max_tiles;
    uint8_t key, failed;
    uint32_t size, total;         /* size < total: incomplete */
    uint32_t capacity;
    uint8_t *data;                /* worker-owned */
} nk_jpeg_shared;

/* Caller owns ready_fd; the worker owns the doorbell, which the caller may
   only write (eight bytes) while the worker exists. */
typedef struct nk_jpeg_worker nk_jpeg_worker;
nk_jpeg_worker *nk_jpeg_create(int *ready_fd, int *doorbell_fd, nk_jpeg_shared **shared);
/* No request may be in flight. */
void nk_jpeg_destroy(nk_jpeg_worker *);
#endif
