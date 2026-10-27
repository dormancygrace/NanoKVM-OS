#include "nanokvm_vi_buffering.h"
#include <stdio.h>
#include <string.h>
#include <unistd.h>

static struct nanokvm_vi_buffering startup;

struct nanokvm_vi_buffering nanokvm_vi_configure_buffering(uint32_t width, uint32_t height)
{
    struct nanokvm_vi_buffering config;
    memset(&config, 0, sizeof(config));
    config.width = width;
    config.height = height;
    config.reusable = access("/proc/device-tree/reserved-memory/ion/reusable", F_OK) == 0;
    config.cma_read = NANOKVM_CMA_READ_FAILED;
    FILE *file = fopen("/proc/device-tree/reserved-memory/ion/size", "rb");
    if (file) {
        /* The board uses #size-cells = <1>. Reject a truncated or wider cell. */
        uint8_t size[5];
        const size_t count = fread(size, 1, sizeof(size), file);
        config.cma_read = count == 4 && !ferror(file)
            ? NANOKVM_CMA_READ_OK : NANOKVM_CMA_READ_INVALID;
        fclose(file);
        if (config.cma_read == NANOKVM_CMA_READ_OK)
            config.cma_bytes = (uint32_t)size[0] << 24 | (uint32_t)size[1] << 16
                | (uint32_t)size[2] << 8 | size[3];
    }
    const char *reason;
    if (!width || !height || (uint64_t)width * height > 2560ULL * 1440) {
        config.source_buffers = 2;
        reason = width && height ? "large-input" : "unknown-input";
    } else {
        config.source_buffers = 3;
        if (!config.reusable) reason = "fixed-layout";
        else if (config.cma_read != NANOKVM_CMA_READ_OK) reason = "unknown-cma-size";
        else if (config.cma_bytes < 144U * 1024 * 1024) reason = "small-cma";
        else {
            /* Two DMA slots, an encoder lease and the latest completed frame. */
            config.source_buffers = 4;
            config.ready_depth = 1;
            reason = "shared-cma";
        }
    }
    startup = config;
    fprintf(stderr, "[kvm_mmf] VI buffering input=%ux%u reusable=%d cma_bytes=%u "
        "cma_read=%s buffers=%u depth=%u policy=%s\n", width, height, config.reusable,
        config.cma_bytes, config.cma_read == NANOKVM_CMA_READ_OK ? "ok"
            : config.cma_read == NANOKVM_CMA_READ_FAILED ? "failed" : "invalid",
        config.source_buffers, config.ready_depth, reason);
    return config;
}

int nanokvm_vi_startup_depth(uint32_t width, uint32_t height, uint32_t *depth)
{
    if (!depth || !width || !height || startup.width != width || startup.height != height)
        return -1;
    *depth = startup.ready_depth;
    return 0;
}

void nanokvm_vi_reset_buffering(void)
{
    memset(&startup, 0, sizeof(startup));
}
