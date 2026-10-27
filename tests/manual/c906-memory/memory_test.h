/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef C906_MEMORY_TEST_H
#define C906_MEMORY_TEST_H

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
