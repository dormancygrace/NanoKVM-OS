//go:build cgo

#define _GNU_SOURCE
#include "capture_worker.h"
#include <errno.h>

enum { IDLE, QUEUED, RUNNING, BORROWED, DONE };
struct nk_capture_worker {
    pthread_t thread;
    pthread_mutex_t lock;
    pthread_cond_t changed;
    int state, stopping, notify_fd, result, mjpeg, pack_result;
    uint16_t width, height, quality;
    uint8_t codec, gop, fps;
    /* Vendor memory; valid only while state == BORROWED. */
    const uint8_t *pack;
    uint32_t size, offset, total;
};

/* Called with the lock held. The worker never runs ahead of the consumer, so
   at most one byte is outstanding and EAGAIN indicates a broken invariant.
   Closing the writer wakes Go with EOF rather than losing the wakeup. */
static int notify_locked(nk_capture_worker *w) {
    unsigned char notification = 1;
    ssize_t n = -1;
    if (w->notify_fd >= 0) {
        do { n = write(w->notify_fd, &notification, 1); } while (n < 0 && errno == EINTR);
    }
    if (n == 1) return 0;
    if (w->notify_fd >= 0) close(w->notify_fd);
    w->notify_fd = -1;
    w->stopping = 1;
    return -1;
}

/* Runs inside the native read before the vendor stream is released. Lends the
   pack to the consumer and waits until take has copied it. */
static int capture_sink(uintptr_t context, const uint8_t *data, uint32_t size,
        uint32_t offset, uint32_t total) {
    nk_capture_worker *w = (nk_capture_worker *)context;
    int result = -1;
    pthread_mutex_lock(&w->lock);
    if (!w->stopping) {
        w->pack = data; w->size = size; w->offset = offset; w->total = total;
        w->state = BORROWED;
        if (notify_locked(w) == 0) {
            while (!w->stopping && w->state == BORROWED)
                pthread_cond_wait(&w->changed, &w->lock);
            if (w->state != BORROWED) result = w->pack_result;
        }
        w->pack = NULL;
        w->state = RUNNING;
    }
    pthread_mutex_unlock(&w->lock);
    return result;
}

static void *capture_main(void *arg) {
    nk_capture_worker *w = arg;
    pthread_mutex_lock(&w->lock);
    for (;;) {
        while (!w->stopping && w->state != QUEUED)
            pthread_cond_wait(&w->changed, &w->lock);
        if (w->stopping) break;
        w->state = RUNNING;
        pthread_mutex_unlock(&w->lock);
        /* Parameters stay immutable until take returns the worker to IDLE. */
        int result = w->mjpeg
            ? kvmv_read_mjpeg_sink(w->width, w->height, w->quality,
                capture_sink, (uintptr_t)w)
            : kvmv_read_video_sink(w->width, w->height, w->codec,
                w->quality, w->gop, w->fps, capture_sink, (uintptr_t)w);
        pthread_mutex_lock(&w->lock);
        w->result = result;
        w->state = DONE;
        notify_locked(w);
    }
    pthread_mutex_unlock(&w->lock);
    return NULL;
}

nk_capture_worker *nk_capture_create(int *read_fd) {
    *read_fd = -1;
    nk_capture_worker *w = calloc(1, sizeof(*w));
    if (!w) return NULL;
    int fds[2];
    if (pipe2(fds, O_CLOEXEC | O_NONBLOCK) < 0) { free(w); return NULL; }
    w->notify_fd = fds[1];
    if (pthread_mutex_init(&w->lock, NULL)) goto fail_pipe;
    if (pthread_cond_init(&w->changed, NULL)) goto fail_mutex;
    if (pthread_create(&w->thread, NULL, capture_main, w)) goto fail_cond;
    /* Distinguishes capture time from Go threads in per-thread statistics. */
    pthread_setname_np(w->thread, "nkos-capture");
    *read_fd = fds[0];
    return w;
fail_cond:
    pthread_cond_destroy(&w->changed);
fail_mutex:
    pthread_mutex_destroy(&w->lock);
fail_pipe:
    close(fds[0]); close(fds[1]); free(w); return NULL;
}

static int submit(nk_capture_worker *w, int mjpeg, uint16_t width, uint16_t height,
        uint8_t codec, uint16_t quality, uint8_t gop, uint8_t fps) {
    pthread_mutex_lock(&w->lock);
    int error = w->stopping ? EPIPE : w->state != IDLE ? EBUSY : 0;
    if (!error) {
        w->mjpeg=mjpeg; w->width=width; w->height=height; w->codec=codec;
        w->quality=quality; w->gop=gop; w->fps=fps;
        w->state=QUEUED;
        pthread_cond_signal(&w->changed);
    }
    pthread_mutex_unlock(&w->lock);
    return error;
}

int nk_capture_submit_video(nk_capture_worker *w, uint16_t width, uint16_t height,
        uint8_t codec, uint16_t bitrate, uint8_t gop, uint8_t fps) {
    return submit(w, 0, width, height, codec, bitrate, gop, fps);
}

int nk_capture_submit_mjpeg(nk_capture_worker *w, uint16_t width, uint16_t height,
        uint16_t quality) {
    return submit(w, 1, width, height, 0, quality, 0, 0);
}

int nk_capture_take(nk_capture_worker *w, kvmv_video_sink sink, uintptr_t context,
        int *done, int *result) {
    pthread_mutex_lock(&w->lock);
    int error = 0;
    if (w->state == BORROWED) {
        /* capture_sink waits for this state change, so the vendor still holds
           the pack while it is copied. */
        w->pack_result = sink(context, w->pack, w->size, w->offset, w->total);
        w->state = RUNNING;
        pthread_cond_signal(&w->changed);
        *done = 0;
    } else if (w->state == DONE) {
        *result = w->result;
        w->state = IDLE;
        *done = 1;
    } else {
        error = EAGAIN;
    }
    pthread_mutex_unlock(&w->lock);
    return error;
}

void nk_capture_destroy(nk_capture_worker *w) {
    if (!w) return;
    pthread_mutex_lock(&w->lock);
    w->stopping=1;
    pthread_cond_broadcast(&w->changed);
    pthread_mutex_unlock(&w->lock);
    pthread_join(w->thread, NULL);
    if (w->notify_fd >= 0) close(w->notify_fd);
    pthread_cond_destroy(&w->changed);
    pthread_mutex_destroy(&w->lock);
    free(w);
}
