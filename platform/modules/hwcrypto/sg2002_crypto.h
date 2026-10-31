/* SPDX-License-Identifier: GPL-2.0 WITH Linux-syscall-note */
#ifndef SG2002_CRYPTO_H
#define SG2002_CRYPTO_H
#include <linux/types.h>
#include <linux/ioctl.h>

#define SG2002_CRYPTO_ABI 1
#define SG2002_CRYPTO_MAX 4096
#define SG2002_CRYPTO_OUT_MAX 8192
enum sg2002_crypto_algorithm {
 SG2002_ALG_AES = 1, SG2002_ALG_DES, SG2002_ALG_TDES, SG2002_ALG_SM4,
 SG2002_ALG_SHA1, SG2002_ALG_SHA256, SG2002_ALG_BASE64,
 SG2002_ALG_COUNT
};
enum sg2002_crypto_mode { SG2002_MODE_ECB, SG2002_MODE_CBC, SG2002_MODE_CTR };
/* SHA requests transform complete 64-byte blocks. state contains/returns the
 * canonical big-endian chaining state; callers handle initialization/padding.
 * input/output are userspace virtual addresses, never physical/DMA addresses.
 * output_length is capacity on input and actual length on successful return.
 */
struct sg2002_crypto_request {
 __u32 version, algorithm, mode, encrypt;
 __u32 key_length, length, output_length, status;
 __u8 key[32], iv[16], state[32];
 __aligned_u64 input, output;
 __u32 reserved[4];
};
struct sg2002_crypto_info {
 __u32 version, algorithms, max_input, max_output;
 __u32 poisoned, reserved[3];
 __aligned_u64 submitted[SG2002_ALG_COUNT];
 __aligned_u64 completed[SG2002_ALG_COUNT];
};
#define SG2002_CRYPTO_RUN _IOWR('K', 0xc2, struct sg2002_crypto_request)
#define SG2002_CRYPTO_INFO _IOR('K', 0xc3, struct sg2002_crypto_info)
#endif
