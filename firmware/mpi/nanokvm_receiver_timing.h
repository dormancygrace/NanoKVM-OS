#pragma once
#include <errno.h>
#include <time.h>
/* Conservative UXC/PCIe starting value, verified by the runtime bench probe.
 * Other receiver variants still require physical timing qualification. */
#define NANOKVM_RECEIVER_SETTLE_MS 250U
static inline int nanokvm_receiver_wait_ready(void)
{
    struct timespec remaining = {0, NANOKVM_RECEIVER_SETTLE_MS * 1000000L};
    while (nanosleep(&remaining, &remaining) < 0)
        if (errno != EINTR) return -1;
    return 0;
}
