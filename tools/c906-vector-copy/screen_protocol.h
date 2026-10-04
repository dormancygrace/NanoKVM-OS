/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef C906_SCREEN_PROTOCOL_H
#define C906_SCREEN_PROTOCOL_H
#include <linux/ioctl.h>
#include <linux/types.h>
#define SCREEN_MAX (1024U * 1024U)
#define SCREEN_PAD 64U
#define SCREEN_RUN _IOWR('Q', 0x52, struct screen_request)
enum screen_operation { SCREEN_CSUM, SCREEN_LZ4, SCREEN_PREFIX, SCREEN_LZ4_LIMITED };
/* variant: 0=running kernel, 1=scalar T-Head, 2=RVV, 3=RVV request in hard IRQ, 4=lazy LZ4 */
struct screen_request {
	__u64 bytes, iterations, elapsed_ns, result, vector_calls;
	__u32 operation, variant, offset, pattern, difference, completed;
};
#endif
