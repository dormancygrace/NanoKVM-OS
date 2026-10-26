#ifndef NANOKVM_VI_BUFFERING_H
#define NANOKVM_VI_BUFFERING_H
#include <stdint.h>
#include <stdio.h>
#include <unistd.h>

/* The four-buffer policy is qualified on the shared 144MiB CMA layout.
 * Earlier fixed carveouts and smaller CMA regions retain their reservation. */
static inline uint32_t nanokvm_vi_shared_cma(void)
{
    if (access("/proc/device-tree/reserved-memory/ion/reusable", F_OK) != 0) return 0;
    FILE *file = fopen("/proc/device-tree/reserved-memory/ion/size", "rb");
    if (!file) return 0;
    uint8_t size[4];
    const size_t count = fread(size, 1, sizeof(size), file);
    fclose(file);
    if (count != sizeof(size)) return 0;
    const uint32_t bytes = (uint32_t)size[0] << 24 | (uint32_t)size[1] << 16
        | (uint32_t)size[2] << 8 | size[3];
    return bytes >= 144U * 1024 * 1024;
}

/* Keep the newest completed frame for a late reader. The driver replaces an
 * unread frame, so this does not build a backlog. Larger inputs retain their
 * two-buffer, no-queue policy to fit the reserved video heap. */
static inline uint32_t nanokvm_vi_ready_depth(uint32_t width, uint32_t height)
{
    return width && height && (uint64_t)width * height <= 2560ULL * 1440
        && nanokvm_vi_shared_cma() ? 1 : 0;
}

static inline uint32_t nanokvm_vi_source_buffers(uint32_t width, uint32_t height)
{
    /* Two DMA slots, one encoder lease and one completed frame. VPSS can
     * share those frames, but must not keep the next DMA refill waiting. */
    if (!width || !height || (uint64_t)width * height > 2560ULL * 1440) return 2;
    return nanokvm_vi_ready_depth(width, height) ? 4 : 3;
}
#endif
