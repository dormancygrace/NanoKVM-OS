/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef C906_USERCOPY_TEST_H
#define C906_USERCOPY_TEST_H
#include <linux/types.h>
#include <linux/ioctl.h>

#define SM_LIMIT 32768
struct sm_req {
	__u64 user, output, residual, status;
	__u32 variant, direction, n, offset, zero;
};
#define SM_IOCTL _IOWR('s', 1, struct sm_req)

static inline unsigned char sm_pattern(unsigned i)
{
	unsigned x = i + 0x9062002U;

	x ^= x >> 16;
	x *= 0x7feb352dU;
	x ^= x >> 15;
	x *= 0x846ca68bU;
	x ^= x >> 16;
	return x;
}
#endif
