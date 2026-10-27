#ifndef NANOKVM_VI_BUFFERING_H
#define NANOKVM_VI_BUFFERING_H
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif

enum nanokvm_cma_read_status {
    NANOKVM_CMA_READ_OK, NANOKVM_CMA_READ_FAILED, NANOKVM_CMA_READ_INVALID
};
struct nanokvm_vi_buffering {
    uint32_t width, height, cma_bytes, source_buffers, ready_depth;
    int reusable;
    enum nanokvm_cma_read_status cma_read;
};

/* One capture-start snapshot supplies both the VB reservation and VI depth.
 * Caller serializes configure/reset with capture startup and teardown. */
struct nanokvm_vi_buffering nanokvm_vi_configure_buffering(uint32_t width, uint32_t height);
/* Reject geometry that changed after the source pool was configured. On an
 * error, leave depth untouched and do not enable the VI channel. */
int nanokvm_vi_startup_depth(uint32_t width, uint32_t height, uint32_t *depth);
void nanokvm_vi_reset_buffering(void);
#ifdef __cplusplus
}
#endif
#endif
