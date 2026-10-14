/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef C906_LZ4_PROTOCOL_H
#define C906_LZ4_PROTOCOL_H
#include <linux/types.h>
#include <linux/ioctl.h>
struct lz_request { __u64 bytes, iterations, elapsed_ns; __u32 variant, pattern, offset, operation, output, reserved; };
#define LZ_RUN _IOWR('L', 1, struct lz_request)
#define LZ_MAX 65536
#define LZ_VARIANTS 11
#endif
