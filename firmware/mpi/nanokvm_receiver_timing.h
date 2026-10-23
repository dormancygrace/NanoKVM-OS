#pragma once
#include <errno.h>
#include <time.h>
/* Minimum register-bus quiet windows, not HDMI-lock deadlines. C and D use
 * separate policies based on published board measurements; NanoKVM C/D
 * physical qualification is still required. UXC retains its bench value. */
#define NANOKVM_RECEIVER_C_SETTLE_MS 2500U
#define NANOKVM_RECEIVER_D_SETTLE_MS 2500U
#define NANOKVM_RECEIVER_UXC_SETTLE_MS 250U
#define NANOKVM_RECEIVER_SETTLE_MS NANOKVM_RECEIVER_UXC_SETTLE_MS
static inline unsigned nanokvm_receiver_settle_ms(char model)
{
    switch (model) {
    case 'u': return NANOKVM_RECEIVER_UXC_SETTLE_MS;
    case 'd': return NANOKVM_RECEIVER_D_SETTLE_MS;
    default: return NANOKVM_RECEIVER_C_SETTLE_MS;
    }
}
static inline int nanokvm_receiver_wait_ready_for(char model)
{
    const unsigned ms = nanokvm_receiver_settle_ms(model);
    struct timespec remaining = {(time_t)(ms / 1000U), (long)(ms % 1000U) * 1000000L};
    while (nanosleep(&remaining, &remaining) < 0)
        if (errno != EINTR) return -1;
    return 0;
}
