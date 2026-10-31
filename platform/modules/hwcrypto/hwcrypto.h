/* SPDX-License-Identifier: GPL-2.0 WITH Linux-syscall-note */
#ifndef HWCRYPTO_H
#define HWCRYPTO_H
#include <linux/types.h>
#include <linux/ioctl.h>

#define HWCRYPTO_ABI 1
#define HWCRYPTO_MAX 4096
#define HWCRYPTO_OUT_MAX 8192
enum hwcrypto_algorithm {
 HWCRYPTO_ALG_AES = 1, HWCRYPTO_ALG_DES, HWCRYPTO_ALG_TDES, HWCRYPTO_ALG_SM4,
 HWCRYPTO_ALG_SHA1, HWCRYPTO_ALG_SHA256, HWCRYPTO_ALG_BASE64,
 HWCRYPTO_ALG_COUNT
};
enum hwcrypto_mode { HWCRYPTO_MODE_ECB, HWCRYPTO_MODE_CBC, HWCRYPTO_MODE_CTR };
/* SHA requests transform complete 64-byte blocks. state contains/returns the
 * canonical big-endian chaining state; callers handle initialization/padding.
 * input/output are userspace virtual addresses, never physical/DMA addresses.
 * output_length is capacity on input and actual length on successful return.
 */
struct hwcrypto_request {
 __u32 version, algorithm, mode, encrypt;
 __u32 key_length, length, output_length, status;
 __u8 key[32], iv[16], state[32];
 __aligned_u64 input, output;
 __u32 reserved[4];
};
struct hwcrypto_info {
 __u32 version, algorithms, max_input, max_output;
 __u32 poisoned, reserved[3];
 __aligned_u64 submitted[HWCRYPTO_ALG_COUNT];
 __aligned_u64 completed[HWCRYPTO_ALG_COUNT];
};
#define HWCRYPTO_RUN _IOWR('K', 0xc2, struct hwcrypto_request)
#define HWCRYPTO_INFO _IOR('K', 0xc3, struct hwcrypto_info)
#endif
