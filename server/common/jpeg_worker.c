//go:build !teststub

#define _GNU_SOURCE
#include "jpeg_worker.h"
#include <dlfcn.h>
#include <errno.h>
#include <poll.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <sys/eventfd.h>
#include <unistd.h>

#define MAX_JPEG_MESSAGE (64u << 20)

typedef int (*tiles_fn)(uint16_t, uint16_t, uint16_t, uint8_t, uint16_t, kvmv_video_sink, uintptr_t);

struct nk_jpeg_worker {
    nk_jpeg_shared shared; /* first: published to the caller */
    pthread_t thread;
    int ready_fd, doorbell_fd;
    int stopping; /* atomic */
    tiles_fn tiles; /* NULL: the library has no tiled MJPEG (-9) */
};

static void ring(int fd) {
    uint64_t one = 1;
    ssize_t n;
    do { n = write(fd, &one, sizeof(one)); } while (n < 0 && errno == EINTR);
}

/* Copies each borrowed pack before the vendor buffers are released; the checks
   match videoPackStorage.appendPack. A failure aborts the picture. */
static int collect(uintptr_t context, const uint8_t *data, uint32_t size, uint32_t offset, uint32_t total) {
    nk_jpeg_shared *s = &((nk_jpeg_worker *)context)->shared;
    if (s->failed || data == NULL || size == 0 || total == 0 || total > MAX_JPEG_MESSAGE
            || offset != s->size || offset > total || size > total - offset
            || (s->total != 0 && s->total != total)) {
        s->failed = 1;
        return -1;
    }
    if (s->total == 0) {
        if (s->capacity < total) {
            uint8_t *grown = realloc(s->data, total);
            if (grown == NULL) { s->failed = 1; return -1; }
            s->data = grown;
            s->capacity = total;
        }
        s->total = total;
    }
    memcpy(s->data + offset, data, size);
    s->size += size;
    return 0;
}

static void *jpeg_main(void *arg) {
    nk_jpeg_worker *w = arg;
    nk_jpeg_shared *s = &w->shared;
    for (;;) {
        struct pollfd pfd = { .fd = w->doorbell_fd, .events = POLLIN };
        if (poll(&pfd, 1, -1) < 0) {
            if (errno == EINTR) continue;
            break;
        }
        uint64_t count;
        while (read(w->doorbell_fd, &count, sizeof(count)) < 0 && errno == EINTR) {}
        if (__atomic_load_n(&w->stopping, __ATOMIC_ACQUIRE)) break;
        int32_t request = __atomic_load_n(&s->request, __ATOMIC_ACQUIRE);
        if (request == NK_JPEG_IDLE) continue;
        s->size = s->total = 0;
        s->failed = 0;
        int result;
        if (request == NK_JPEG_TILES)
            result = w->tiles ? w->tiles(s->width, s->height, s->quality, s->key, s->max_tiles, collect, (uintptr_t)w) : -9;
        else
            result = kvmv_read_mjpeg_sink(s->width, s->height, s->quality, collect, (uintptr_t)w);
        if (s->failed) s->size = 0;
        s->result = result;
        __atomic_store_n(&s->request, NK_JPEG_IDLE, __ATOMIC_RELEASE);
        ring(w->ready_fd);
    }
    return NULL;
}

nk_jpeg_worker *nk_jpeg_create(int *ready_fd, int *doorbell_fd, nk_jpeg_shared **shared) {
    nk_jpeg_worker *w = calloc(1, sizeof(*w));
    if (w == NULL) return NULL;
    w->doorbell_fd = -1;
    w->ready_fd = eventfd(0, EFD_NONBLOCK | EFD_CLOEXEC);
    w->doorbell_fd = eventfd(0, EFD_CLOEXEC);
    if (w->ready_fd < 0 || w->doorbell_fd < 0) goto fail;
    /* Older libraries lack tiled MJPEG; tiled requests then return -9. */
    w->tiles = (tiles_fn)dlsym(RTLD_DEFAULT, "kvmv_read_mjpeg_tiles_sink");
    if (pthread_create(&w->thread, NULL, jpeg_main, w) != 0) goto fail;
    pthread_setname_np(w->thread, "nkos-jpeg");
    *ready_fd = w->ready_fd;
    *doorbell_fd = w->doorbell_fd;
    *shared = &w->shared;
    return w;
fail:
    if (w->ready_fd >= 0) close(w->ready_fd);
    if (w->doorbell_fd >= 0) close(w->doorbell_fd);
    free(w);
    return NULL;
}

void nk_jpeg_destroy(nk_jpeg_worker *w) {
    __atomic_store_n(&w->stopping, 1, __ATOMIC_RELEASE);
    ring(w->doorbell_fd);
    pthread_join(w->thread, NULL);
    close(w->doorbell_fd);
    free(w->shared.data);
    free(w);
}
