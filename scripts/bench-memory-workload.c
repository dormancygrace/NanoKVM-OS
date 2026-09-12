#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/resource.h>
#include <time.h>
#include <unistd.h>

static double seconds(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec + t.tv_nsec / 1e9;
}
static uint64_t digest(const unsigned char *p, size_t n) {
    uint64_t h = 1469598103934665603ULL;
    for (size_t i = 0; i < n; i++) h = (h ^ p[i]) * 1099511628211ULL;
    return h;
}
static void fill(unsigned char *p, size_t n) {
    uint32_t state = 1234567;
    for (size_t page = 0; page < n; page += 4096) {
        for (size_t offset = 0; offset < 4096; offset += 128) {
            state ^= state << 13; state ^= state >> 17; state ^= state << 5;
            memset(p + page + offset, ' ', 128);
            snprintf((char *)p + page + offset, 128,
                     "{\"device\":\"NanoKVM Enhanced\",\"page\":%zu,\"sample\":%08x,\"offset\":%zu,\"status\":\"connected\",\"fps\":30}",
                     page / 4096, state, offset);
        }
    }
}
int main(int argc, char **argv) {
    if (argc != 4 || (strcmp(argv[1], "pageout") && strcmp(argv[1], "reclaim"))) {
        fprintf(stderr, "Usage: memory-workload pageout|reclaim SIZE_MIB SECONDS\n"); return 2;
    }
    int mb = atoi(argv[2]), duration = atoi(argv[3]);
    if (mb < 8 || mb > 144 || duration < 1 || duration > 90) return 2;
    size_t n = (size_t)mb * 1048576;
    unsigned char *p = mmap(NULL, n, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (p == MAP_FAILED) { perror("mmap"); return 1; }
    setvbuf(stdout, NULL, _IOLBF, 0);
    fill(p, n);
    uint64_t expected = digest(p, n);
    printf("READY mib=%d\n", mb);
    double start = seconds();
    if (!strcmp(argv[1], "pageout")) {
        if (madvise(p, n, MADV_PAGEOUT)) { perror("MADV_PAGEOUT"); return 1; }
        printf("PAGEOUT seconds=%.6f\n", seconds() - start);
        sleep(duration);
    } else {
        // Read an existing regular SD file into page cache while keeping 8 MiB hot.
        // No new test data file, global cache drop or forced swapout is used here.
        int fd = open("/kvmapp/server/NanoKVM-Server", O_RDONLY);
        if (fd < 0) { perror("open server"); return 1; }
        char buf[65536];
        size_t passes = 0;
        volatile uint64_t sink = 0;
        while (seconds() - start < duration) {
            if (lseek(fd, 0, SEEK_SET) < 0) return 1;
            ssize_t got;
            while ((got = read(fd, buf, sizeof(buf))) > 0) sink += buf[got - 1];
            if (got < 0) { perror("read"); return 1; }
            for (size_t i = 0; i < 8 * 1048576; i += 4096) sink += p[i];
            passes++;
        }
        close(fd);
        printf("RECLAIM passes=%zu elapsed=%.6f sink=%" PRIu64 "\n", passes, seconds() - start, sink);
    }
    double verify_start = seconds();
    uint64_t actual = digest(p, n);
    struct rusage usage;
    getrusage(RUSAGE_SELF, &usage);
    printf("VERIFY ok=%d seconds=%.6f major_faults=%ld minor_faults=%ld user_s=%.6f system_s=%.6f\n",
           actual == expected, seconds() - verify_start, usage.ru_majflt, usage.ru_minflt,
           usage.ru_utime.tv_sec + usage.ru_utime.tv_usec / 1e6,
           usage.ru_stime.tv_sec + usage.ru_stime.tv_usec / 1e6);
    munmap(p, n);
    return actual == expected ? 0 : 1;
}
