#ifndef NANOKVM_RECEIVER_LOCK_H
#define NANOKVM_RECEIVER_LOCK_H
/* Cooperative ownership of the bridge's banked register interface. The EDID
 * tool holds this for its complete operation; capture and sensor probes use
 * the same file. A new open is required for each owner, including threads. */
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <sys/file.h>
#include <time.h>
#include <unistd.h>

/* Control/programming may wait for a short detector or sensor session.
 * Detector admission itself remains nonblocking. */
#define NANOKVM_RECEIVER_CONTROL_TIMEOUT_MS 2000U

#ifndef NANOKVM_RECEIVER_LOCK_PATH
#define NANOKVM_RECEIVER_LOCK_PATH "/run/nanokvm-edid.lock"
#endif

static inline int nanokvm_receiver_lock_at(const char *path, unsigned timeout_ms)
{
    int fd;
    do { fd = open(path, O_CREAT | O_RDWR | O_CLOEXEC, 0600); }
    while (fd < 0 && errno == EINTR);
    if (fd < 0) return -1;
    struct timespec start;
    if (clock_gettime(CLOCK_MONOTONIC, &start) != 0) {
        int saved = errno; close(fd); errno = saved; return -1;
    }
    for (;;) {
        if (flock(fd, LOCK_EX | LOCK_NB) == 0) return fd;
        int saved = errno;
        if (saved != EINTR && saved != EAGAIN && saved != EWOULDBLOCK) {
            close(fd); errno = saved; return -1;
        }
        struct timespec now;
        if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) {
            saved = errno; close(fd); errno = saved; return -1;
        }
        int64_t elapsed_ns = (int64_t)(now.tv_sec - start.tv_sec) * 1000000000LL
            + now.tv_nsec - start.tv_nsec;
        if (elapsed_ns >= (int64_t)timeout_ms * 1000000LL) {
            close(fd); errno = EBUSY; return -1;
        }
        const struct timespec delay = {0, 2000000};
        nanosleep(&delay, NULL);
    }
}
static inline int nanokvm_receiver_lock(unsigned timeout_ms)
{
    return nanokvm_receiver_lock_at(NANOKVM_RECEIVER_LOCK_PATH, timeout_ms);
}
static inline void nanokvm_receiver_unlock(int fd)
{
    int saved = errno;
    if (fd >= 0) { flock(fd, LOCK_UN); close(fd); }
    errno = saved;
}
#endif
