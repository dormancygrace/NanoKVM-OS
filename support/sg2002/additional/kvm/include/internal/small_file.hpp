#ifndef NANOKVM_SMALL_FILE_HPP
#define NANOKVM_SMALL_FILE_HPP

#include <cerrno>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <unistd.h>

namespace nanokvm {
// Replaces shell redirection while preserving creation mode and existing file
// permissions. Temporary status files must not trigger a global filesystem sync.
inline bool write_small_file(const char *path, const char *text, bool durable = false) {
    int fd;
    do { fd = open(path, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0666); }
    while (fd < 0 && errno == EINTR);
    if (fd < 0) {
        std::fprintf(stderr, "[kvmv] open %s: %s\n", path, std::strerror(errno));
        return false;
    }
    size_t offset = 0, length = std::strlen(text);
    int failure = 0;
    while (offset < length) {
        ssize_t n = write(fd, text + offset, length - offset);
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) { failure = n < 0 ? errno : EIO; break; }
        offset += static_cast<size_t>(n);
    }
    if (!failure && durable) {
        int rc;
        do { rc = fsync(fd); } while (rc < 0 && errno == EINTR);
        if (rc < 0) failure = errno;
    }
    // Linux releases the descriptor even on EINTR: never retry close.
    if (close(fd) < 0 && !failure) failure = errno;
    if (failure) std::fprintf(stderr, "[kvmv] write %s: %s\n", path, std::strerror(failure));
    return failure == 0;
}

inline bool write_small_uint(const char *path, unsigned value, bool durable = false) {
    char text[32];
    std::snprintf(text, sizeof(text), "%u\n", value);
    return write_small_file(path, text, durable);
}
} // namespace nanokvm
#endif
