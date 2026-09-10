#ifndef NANOKVM_CAPTURE_WORKER_H
#define NANOKVM_CAPTURE_WORKER_H
#include <stdint.h>
typedef struct nk_capture_worker nk_capture_worker;
/* One outstanding request. Caller owns read_fd; worker owns its write end. */
nk_capture_worker *nk_capture_create(int *read_fd);
int nk_capture_submit(nk_capture_worker *, uint16_t, uint16_t, uint8_t, uint16_t, uint8_t, uint8_t);
int nk_capture_take(nk_capture_worker *, uint8_t **, uint32_t *, int *);
/* Joins an in-flight native read; does not forcibly cancel the vendor library. */
void nk_capture_destroy(nk_capture_worker *);
#endif
