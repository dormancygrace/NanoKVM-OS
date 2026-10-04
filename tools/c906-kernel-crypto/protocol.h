/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef C906_CRYPTO_PROTOCOL_H
#define C906_CRYPTO_PROTOCOL_H
#include <linux/ioctl.h>
#include <linux/types.h>
#define KC_MAX 65536U
#define KC_PAD 64U
#define KC_RUN _IOWR('Q', 0x61, struct kc_request)
enum kc_operation { KC_CHACHA20, KC_CHACHA12, KC_AEAD_SG,
	KC_CRC32, KC_CRC32C, KC_COPY_CSUM, KC_OPERATIONS };
/* 0=kernel; 1=scalar slicing8; 2=RVV; 3=RVV request in hard IRQ;
 * 4=ChaCha RVV only above 256 bytes. Flags: 1=inplace, 2=bad tag, 4=split SG. */
struct kc_request {
	__u64 bytes, iterations, elapsed_ns, result, vector_calls;
	__u32 operation, variant, offset, dst_offset, pattern, seed, flags, completed;
};
#endif
