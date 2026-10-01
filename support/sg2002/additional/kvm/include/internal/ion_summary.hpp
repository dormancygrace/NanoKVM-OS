#ifndef NANOKVM_ION_SUMMARY_HPP
#define NANOKVM_ION_SUMMARY_HPP

#include <cstdio>
#include <cstring>

namespace nanokvm {
// Adapted from IronKVM 452b2232 (yuzi-co): read the bounded debugfs summary
// directly instead of spawning cat/grep/awk on every watchdog poll.
inline int ion_usage_rate_from_summary(const char *text) {
    if (text == nullptr) return -1;
    static const char key[] = "usage rate:";
    const char *line = text;
    while (*line != '\0') {
        const char *p = line;
        while (*p == ' ' || *p == '\t') ++p;
        if (std::strncmp(p, key, sizeof(key) - 1) == 0) {
            p += sizeof(key) - 1;
            while (*p == ' ' || *p == '\t') ++p;
            if (*p < '0' || *p > '9') return -1;
            int rate = 0;
            do {
                rate = rate * 10 + (*p++ - '0');
                // Bound before the next multiplication, including huge input.
                if (rate > 100) return -1;
            } while (*p >= '0' && *p <= '9');
            while (*p == ' ' || *p == '\t') ++p;
            return *p == '%' ? rate : -1;
        }
        const char *next = std::strchr(line, '\n');
        if (next == nullptr) break;
        line = next + 1;
    }
    return -1;
}

inline int ion_usage_rate_from_stream(FILE *fp) {
    if (fp == nullptr) return -1;
    // The percentage is on the third line. Avoid generating the unbounded
    // per-buffer table that follows it in debugfs.
    char text[512];
    const size_t size = std::fread(text, 1, sizeof(text) - 1, fp);
    if (std::ferror(fp)) return -1;
    text[size] = '\0';
    return ion_usage_rate_from_summary(text);
}

inline int ion_usage_rate_from_file(const char *path) {
    if (path == nullptr) return -1;
    FILE *fp = std::fopen(path, "r");
    if (fp == nullptr) return -1;
    const int rate = ion_usage_rate_from_stream(fp);
    return std::fclose(fp) == 0 ? rate : -1;
}
} // namespace nanokvm
#endif
