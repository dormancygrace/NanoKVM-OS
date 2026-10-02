/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef SG2002_AES_PROBE_H
#define SG2002_AES_PROBE_H
#include <linux/types.h>
#include <linux/ioctl.h>
#define SG2002_AES_MAX 4096
struct sg2002_aes_request {
 __u32 length;
 __u32 reserved;
 __u8 key[16];
 __u8 iv[16];
 __u32 status;
 __u8 data[SG2002_AES_MAX];
};
#define SG2002_AES_CTR _IOWR('K', 0xc1, struct sg2002_aes_request)
#endif
