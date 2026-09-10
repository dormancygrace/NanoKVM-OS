//go:build !teststub

#define _GNU_SOURCE
#include "capture_worker.h"
#include "kvm_vision.h"
#include <errno.h>

enum { IDLE, QUEUED, RUNNING, DONE };
struct nk_capture_worker {
    pthread_t thread;
    pthread_mutex_t lock;
    pthread_cond_t changed;
    int state, stopping, notify_fd, result;
    uint16_t width, height, bitrate;
    uint8_t codec, gop, fps;
    uint8_t *data;
    uint32_t size;
};

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
        int result = kvmv_read_video(w->width, w->height, w->codec,
            w->bitrate, w->gop, w->fps, &w->data, &w->size);
        pthread_mutex_lock(&w->lock);
        w->result = result;
        w->state = DONE;
        unsigned char notification = 1;
        ssize_t n;
        do { n = write(w->notify_fd, &notification, 1); } while (n < 0 && errno == EINTR);
        /* At most one outstanding byte; EAGAIN indicates a broken invariant.
           Closing the writer wakes Go with EOF rather than losing the wakeup. */
        if (n != 1) {
            close(w->notify_fd);
            w->notify_fd = -1;
            w->stopping = 1;
        }
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
    *read_fd = fds[0];
    return w;
fail_cond:
    pthread_cond_destroy(&w->changed);
fail_mutex:
    pthread_mutex_destroy(&w->lock);
fail_pipe:
    close(fds[0]); close(fds[1]); free(w); return NULL;
}

int nk_capture_submit(nk_capture_worker *w, uint16_t width, uint16_t height,
        uint8_t codec, uint16_t bitrate, uint8_t gop, uint8_t fps) {
    pthread_mutex_lock(&w->lock);
    int error = w->stopping ? EPIPE : w->state != IDLE ? EBUSY : 0;
    if (!error) {
        w->width=width; w->height=height; w->codec=codec;
        w->bitrate=bitrate; w->gop=gop; w->fps=fps;
        w->state=QUEUED;
        pthread_cond_signal(&w->changed);
    }
    pthread_mutex_unlock(&w->lock);
    return error;
}

int nk_capture_take(nk_capture_worker *w, uint8_t **data, uint32_t *size, int *result) {
    pthread_mutex_lock(&w->lock);
    int error = w->state == DONE ? 0 : EAGAIN;
    if (!error) {
        *data=w->data; *size=w->size; *result=w->result;
        w->data=NULL; w->size=0; w->state=IDLE;
    }
    pthread_mutex_unlock(&w->lock);
    return error;
}

void nk_capture_destroy(nk_capture_worker *w) {
    if (!w) return;
    pthread_mutex_lock(&w->lock);
    w->stopping=1;
    pthread_cond_signal(&w->changed);
    pthread_mutex_unlock(&w->lock);
    pthread_join(w->thread, NULL);
    if (w->data) free_kvmv_data(&w->data);
    if (w->notify_fd >= 0) close(w->notify_fd);
    pthread_cond_destroy(&w->changed);
    pthread_mutex_destroy(&w->lock);
    free(w);
}
