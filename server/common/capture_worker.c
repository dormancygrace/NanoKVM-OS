//go:build cgo

#define _GNU_SOURCE
#include "capture_worker.h"
#include <errno.h>
#include <poll.h>
#include <sys/eventfd.h>

#define MAX_ACCESS_UNIT (64u << 20)

struct nk_capture_worker {
    nk_capture_shared shared; /* first: published to the consumer */
    pthread_t thread;
    pthread_mutex_t lock;
    pthread_cond_t idle;
    int ready_fd, doorbell_fd;
    /* Guarded by lock. */
    int stopping, running, paused, in_flight, fps_changed;
    nk_capture_params params;
    int64_t period, deadline;
    /* Capture thread only. */
    int produce, failed;
    nk_capture_slot *filling;
};

static int64_t monotonic_ns(void) {
    struct timespec now;
    clock_gettime(CLOCK_MONOTONIC, &now);
    return (int64_t)now.tv_sec * 1000000000 + now.tv_nsec;
}

static int64_t period_ns(uint8_t fps) {
    return 1000000000 / (fps ? fps : 30);
}

int64_t nk_capture_advance(int64_t previous, int64_t now, int64_t period) {
    int64_t next = previous + period;
    return next < now - period ? now : next;
}

static void ring(int fd) {
    uint64_t one = 1;
    ssize_t n;
    do { n = write(fd, &one, sizeof(one)); } while (n < 0 && errno == EINTR);
}

/* Waits for the doorbell or until timeout_ns (negative: indefinitely). The
   caller re-evaluates its state either way, so stale rings are harmless. */
static void wait_doorbell(nk_capture_worker *w, int64_t timeout_ns) {
    struct pollfd pfd = { .fd = w->doorbell_fd, .events = POLLIN };
    struct timespec timeout = { timeout_ns / 1000000000, timeout_ns % 1000000000 };
    if (ppoll(&pfd, 1, timeout_ns < 0 ? NULL : &timeout, NULL) > 0) {
        uint64_t count;
        while (read(w->doorbell_fd, &count, sizeof(count)) < 0 && errno == EINTR) {}
    }
}

/* Copies each borrowed pack before the vendor stream is released; the checks
   match videoPackStorage.appendPack. A failure aborts the access unit. */
static int assemble_pack(uintptr_t context, const uint8_t *data, uint32_t size,
        uint32_t offset, uint32_t total) {
    nk_capture_worker *w = (nk_capture_worker *)context;
    nk_capture_slot *s = w->filling;
    if (w->failed || data == NULL || size == 0 || total == 0 || total > MAX_ACCESS_UNIT
            || offset != s->size || offset > total || size > total - offset
            || (s->total != 0 && s->total != total)) {
        w->failed = 1;
        return -1;
    }
    if (s->total == 0) {
        if (s->capacity < total) {
            uint8_t *grown = realloc(s->data, total);
            if (grown == NULL) { w->failed = 1; return -1; }
            s->data = grown;
            s->capacity = total;
        }
        s->total = total;
    }
    memcpy(s->data + offset, data, size);
    s->size += size;
    return 0;
}

static void read_access_unit(nk_capture_worker *w, nk_capture_slot *s, const nk_capture_params *p) {
    w->filling = s;
    w->failed = 0;
    s->size = s->total = 0;
    s->result = kvmv_read_video_sink(p->width, p->height, p->codec, p->bitrate,
        p->gop, p->fps, assemble_pack, (uintptr_t)w);
    if (w->failed) s->size = 0;
    w->filling = NULL;
}

static void *capture_main(void *arg) {
    nk_capture_worker *w = arg;
    nk_capture_shared *sh = &w->shared;
    pthread_mutex_lock(&w->lock);
    while (!w->stopping) {
        nk_capture_slot *slot = &sh->slot[w->produce];
        if (!w->running || w->paused) {
            pthread_mutex_unlock(&w->lock);
            wait_doorbell(w, -1);
            pthread_mutex_lock(&w->lock);
            continue;
        }
        if (__atomic_load_n(&slot->state, __ATOMIC_SEQ_CST) != NK_SLOT_FREE) {
            /* The consumer rings only after seeing this flag. */
            __atomic_store_n(&sh->producer_waiting, 1, __ATOMIC_SEQ_CST);
            if (__atomic_load_n(&slot->state, __ATOMIC_SEQ_CST) != NK_SLOT_FREE) {
                __atomic_fetch_add(&sh->producer_waits, 1, __ATOMIC_RELAXED);
                pthread_mutex_unlock(&w->lock);
                wait_doorbell(w, -1);
                pthread_mutex_lock(&w->lock);
            }
            __atomic_store_n(&sh->producer_waiting, 0, __ATOMIC_SEQ_CST);
            continue;
        }
        int64_t now = monotonic_ns();
        if (now < w->deadline) {
            /* Absolute cadence; stop, pause and start still wake it early. */
            pthread_mutex_unlock(&w->lock);
            wait_doorbell(w, w->deadline - now);
            pthread_mutex_lock(&w->lock);
            continue;
        }
        if (w->fps_changed) {
            w->fps_changed = 0;
            w->period = period_ns(w->params.fps);
            w->deadline = now + w->period;
        } else {
            w->deadline = nk_capture_advance(w->deadline, now, w->period);
        }
        nk_capture_params params = w->params;
        w->in_flight = 1;
        pthread_mutex_unlock(&w->lock);

        read_access_unit(w, slot, &params);
        __atomic_store_n(&slot->state, NK_SLOT_READY, __ATOMIC_SEQ_CST);
        w->produce ^= 1;
        __atomic_fetch_add(&sh->reads, 1, __ATOMIC_RELAXED);
        if (__atomic_exchange_n(&sh->consumer_waiting, 0, __ATOMIC_SEQ_CST)) {
            /* An eventfd write fails only on counter overflow. */
            ring(w->ready_fd);
            __atomic_fetch_add(&sh->signals, 1, __ATOMIC_RELAXED);
        }

        pthread_mutex_lock(&w->lock);
        w->in_flight = 0;
        pthread_cond_broadcast(&w->idle);
    }
    pthread_mutex_unlock(&w->lock);
    return NULL;
}

nk_capture_worker *nk_capture_create(int *ready_fd, int *doorbell_fd, nk_capture_shared **shared) {
    *ready_fd = *doorbell_fd = -1;
    *shared = NULL;
    nk_capture_worker *w = calloc(1, sizeof(*w));
    if (!w) return NULL;
    w->ready_fd = eventfd(0, EFD_CLOEXEC | EFD_NONBLOCK);
    w->doorbell_fd = eventfd(0, EFD_CLOEXEC | EFD_NONBLOCK);
    if (w->ready_fd < 0 || w->doorbell_fd < 0) goto fail_fds;
    if (pthread_mutex_init(&w->lock, NULL)) goto fail_fds;
    if (pthread_cond_init(&w->idle, NULL)) goto fail_mutex;
    if (pthread_create(&w->thread, NULL, capture_main, w)) goto fail_cond;
    /* Distinguishes capture time from Go threads in per-thread statistics. */
    pthread_setname_np(w->thread, "nkos-capture");
    *ready_fd = w->ready_fd;
    *doorbell_fd = w->doorbell_fd;
    *shared = &w->shared;
    return w;
fail_cond:
    pthread_cond_destroy(&w->idle);
fail_mutex:
    pthread_mutex_destroy(&w->lock);
fail_fds:
    if (w->ready_fd >= 0) close(w->ready_fd);
    if (w->doorbell_fd >= 0) close(w->doorbell_fd);
    free(w);
    return NULL;
}

void nk_capture_start(nk_capture_worker *w, const nk_capture_params *params) {
    pthread_mutex_lock(&w->lock);
    w->params = *params;
    w->period = period_ns(params->fps);
    w->deadline = monotonic_ns() + w->period;
    w->fps_changed = 0;
    w->running = 1;
    pthread_mutex_unlock(&w->lock);
    ring(w->doorbell_fd);
}

void nk_capture_update(nk_capture_worker *w, const nk_capture_params *params) {
    pthread_mutex_lock(&w->lock);
    if (params->fps != w->params.fps) w->fps_changed = 1;
    w->params = *params;
    pthread_mutex_unlock(&w->lock);
}

static void wait_idle_locked(nk_capture_worker *w) {
    while (w->in_flight) pthread_cond_wait(&w->idle, &w->lock);
}

void nk_capture_stop(nk_capture_worker *w) {
    pthread_mutex_lock(&w->lock);
    w->running = 0;
    wait_idle_locked(w);
    pthread_mutex_unlock(&w->lock);
}

void nk_capture_pause(nk_capture_worker *w) {
    pthread_mutex_lock(&w->lock);
    w->paused++;
    wait_idle_locked(w);
    pthread_mutex_unlock(&w->lock);
}

void nk_capture_resume(nk_capture_worker *w) {
    pthread_mutex_lock(&w->lock);
    if (w->paused > 0) w->paused--;
    pthread_mutex_unlock(&w->lock);
    ring(w->doorbell_fd);
}

void nk_capture_destroy(nk_capture_worker *w) {
    if (!w) return;
    pthread_mutex_lock(&w->lock);
    w->stopping = 1;
    pthread_mutex_unlock(&w->lock);
    ring(w->doorbell_fd);
    pthread_join(w->thread, NULL);
    close(w->doorbell_fd);
    for (int i = 0; i < 2; i++) free(w->shared.slot[i].data);
    pthread_cond_destroy(&w->idle);
    pthread_mutex_destroy(&w->lock);
    free(w);
}
