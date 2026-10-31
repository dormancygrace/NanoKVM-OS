/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef SG2002_AES_PROBE_H
#define SG2002_AES_PROBE_H
#include <linux/types.h>
#include <linux/ioctl.h>
#define HWCRYPTO_AES_MAX 4096
struct hwcrypto_aes_request {
 __u32 length;
 __u32 reserved;
 __u8 key[16];
 __u8 iv[16];
 __u32 status;
 __u8 data[HWCRYPTO_AES_MAX];
};
#define HWCRYPTO_AES_CTR _IOWR('K', 0xc1, struct hwcrypto_aes_request)
#endif
