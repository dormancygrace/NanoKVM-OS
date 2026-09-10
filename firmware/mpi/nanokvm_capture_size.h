#ifndef NANOKVM_CAPTURE_SIZE_H
#define NANOKVM_CAPTURE_SIZE_H
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
/* Outputs are changed only after both values validate. ENOENT for both uses 1080p. */
int nanokvm_read_capture_size(const char *directory, uint32_t *width, uint32_t *height);
#ifdef __cplusplus
}
#endif
#endif
