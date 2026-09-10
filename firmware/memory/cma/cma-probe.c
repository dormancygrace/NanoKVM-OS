/* SPDX-License-Identifier: MIT */
/* Bounded anonymous-memory + ION migration probe, synthetic data only. */
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>
#include "ion.h"
#include "ion_cvitek.h"

static volatile sig_atomic_t stop;
static void stopped(int sig) { (void)sig; stop = 1; }
static unsigned number(const char *s, unsigned limit)
{
    char *end; errno = 0;
    unsigned long n = strtoul(s, &end, 10);
    if (errno || !*s || *end || n > limit) exit(2);
    return n;
}
static unsigned long memory(const char *key)
{
    FILE *f = fopen("/proc/meminfo", "r");
    if (!f) return 0;
    char line[160], name[80]; unsigned long value, found = 0;
    while (fgets(line, sizeof(line), f))
        if (sscanf(line, "%79s %lu", name, &value) == 2 && !strcmp(key, name)) { found = value; break; }
    fclose(f); return found;
}
static void memstate(const char *phase)
{
    printf("{\"phase\":\"%s\",\"available_kib\":%lu,\"cma_total_kib\":%lu,\"cma_free_kib\":%lu}\n",
           phase, memory("MemAvailable:"), memory("CmaTotal:"), memory("CmaFree:"));
    fflush(stdout);
}
static uint32_t pattern(size_t word, uint32_t seed) { return (uint32_t)word * 0x9e3779b9U ^ seed; }
static unsigned verify(volatile uint32_t *p, size_t bytes, uint32_t seed)
{
    unsigned errors = 0;
    for (size_t i = 0; i < bytes / 4; i += 1024)
        if (p[i] != pattern(i, seed)) errors++;
    return errors;
}
static uint64_t ns(void)
{
    struct timespec t; clock_gettime(CLOCK_MONOTONIC, &t);
    return (uint64_t)t.tv_sec * 1000000000ULL + t.tv_nsec;
}
int main(int argc, char **argv)
{
    if (argc != 4) return 2;
    unsigned anon_mib = number(argv[1], 128), ion_mib = number(argv[2], 60), seconds = number(argv[3], 120);
    if (!ion_mib || !seconds || !memory("CmaTotal:")) return 2;
    signal(SIGTERM, stopped); signal(SIGINT, stopped);
    memstate("before");
    size_t anon_bytes = (size_t)anon_mib << 20, touched = 0;
    uint32_t *anon = NULL;
    if (anon_bytes) {
        anon = mmap(NULL, anon_bytes, PROT_READ|PROT_WRITE, MAP_PRIVATE|MAP_ANONYMOUS, -1, 0);
        if (anon == MAP_FAILED) return 1;
        /* Leave the requested ION size plus at least 32 MiB estimated
         * available before each 1 MiB step.
         * This is a bound on this probe, not an OOM guarantee under other load. */
        while (touched < anon_bytes && !stop && memory("MemAvailable:") >= 33792UL + ion_mib*1024UL) {
            for (size_t i = touched/4; i < (touched+(1U<<20))/4; i++) anon[i] = pattern(i, 0x12345678U);
            touched += 1U<<20;
        }
    }
    printf("{\"anonymous_touched_bytes\":%zu}\n", touched);
    memstate("after_anonymous");
    int fd = open("/dev/ion", O_RDWR), rc = 1;
    void *mapped = MAP_FAILED;
    struct ion_allocation_data a = {.len = (uint64_t)ion_mib<<20, .fd = UINT32_MAX};
    struct ion_heap_data heaps[8] = {0};
    struct ion_heap_query q = {.cnt = 8, .heaps = (uintptr_t)heaps};
    if (stop) goto done;
    if (fd < 0 || ioctl(fd, ION_IOC_HEAP_QUERY, &q)) goto done;
    for (unsigned i = 0; i < q.cnt && i < 8; i++)
        if (heaps[i].type == ION_HEAP_TYPE_CARVEOUT && heaps[i].heap_id < 32) { a.heap_id_mask = 1U << heaps[i].heap_id; break; }
    if (!a.heap_id_mask) goto done;
    strcpy(a.name, "cma-migration-probe");
    uint64_t start = ns();
    int result = ioctl(fd, ION_IOC_ALLOC, &a), saved = errno;
    printf("{\"allocate_result\":%d,\"errno\":%d,\"allocate_us\":%" PRIu64 ",\"ion_bytes\":%" PRIu64 "}\n",
           result, result ? saved : 0, (ns()-start)/1000, (uint64_t)a.len);
    if (result) { a.fd = UINT32_MAX; goto done; }
    mapped = mmap(NULL, a.len, PROT_READ|PROT_WRITE, MAP_SHARED, a.fd, 0);
    if (mapped == MAP_FAILED) goto done;
    volatile uint32_t *ion = mapped;
    for (size_t i = 0; i < a.len/4; i += 1024) ion[i] = pattern(i, 0x87654321U);
    __sync_synchronize();
    memstate("after_ion");
    unsigned errors = verify(anon, touched, 0x12345678U) + verify(ion, a.len, 0x87654321U);
    for (unsigned s = 0; s < seconds && !stop; s++) sleep(1);
    errors += verify(anon, touched, 0x12345678U) + verify(ion, a.len, 0x87654321U);
    printf("{\"sampled_page_errors\":%u}\n", errors);
    rc = errors ? 1 : 0;
done:
    if (mapped != MAP_FAILED) munmap(mapped, a.len);
    if (a.fd != UINT32_MAX) close(a.fd);
    if (fd >= 0) close(fd);
    memstate("after_ion_free");
    if (anon) munmap(anon, anon_bytes);
    memstate("after_all_free");
    return rc;
}
