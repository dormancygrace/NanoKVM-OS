/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef C906_VECTOR_PROTOCOL_H
#define C906_VECTOR_PROTOCOL_H
#include <linux/types.h>
#include <linux/ioctl.h>
#define C906_MAX_BYTES (1024U * 1024U)
#define C906_PAD 64U
enum c906_operation { C906_FROM_USER, C906_TO_USER, C906_COPY, C906_FILL };
struct c906_request {
	__u64 user_pointer;
	__u64 bytes;
	__u64 iterations;
	__u64 elapsed_ns;
	__u64 not_copied;
	__u64 vector_calls;
	__u32 operation;
	__u32 vector;
	__u32 source_offset;
	__u32 destination_offset;
	__u32 fill;
	__u32 completed;
};
#define C906_RUN _IOWR('V', 0x71, struct c906_request)
#endif
