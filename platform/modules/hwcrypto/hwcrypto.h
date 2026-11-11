/* SPDX-License-Identifier: GPL-2.0 WITH Linux-syscall-note */
#ifndef HWCRYPTO_H
#define HWCRYPTO_H
#include <linux/types.h>
#include <linux/ioctl.h>

#define HWCRYPTO_ABI 1
/* One message of HWCRYPTO_CTR_SHA1 and one chunk of HWCRYPTO_GCM. HWCRYPTO_RUN
 * takes up to max_input of HWCRYPTO_INFO (64 KiB). */
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
/* AES-GCM bulk data: AES-CTR with GCM's 32-bit counter on the engine and, while
 * it runs, GHASH of the ciphertext on the CPU. length is a nonzero multiple of
 * 16. h is the hash key E_K(0^128); xi (GHASH accumulator) and ctr (counter
 * block, big-endian 32-bit counter in the last word) are in GCM byte order and
 * come back advanced over the returned length. If a chunk after the first
 * fails, the call succeeds with the completed prefix in length. Associated
 * data, partial blocks, lengths and the tag stay with the caller.
 */
struct hwcrypto_gcm_request {
 __u32 version, encrypt, key_length, length;
 __u8 key[32], h[16], xi[16], ctr[16];
 __aligned_u64 input, output;
 __u32 reserved[4];
};
#define HWCRYPTO_GCM _IOWR('K', 0xc4, struct hwcrypto_gcm_request)
/* For each of count messages, in order: AES-128-CTR with key and the
 * message's iv over crypt_length bytes (a nonzero multiple of 16) at
 * crypt_offset (a multiple of 4), in place, then SHA-1 from the request's state over the first length
 * bytes of the message as they are then (a multiple of 64, at most
 * HWCRYPTO_MAX), the resulting state in the message's state. Both states are
 * the canonical big-endian bytes; the caller pads the message and finishes
 * the hash (for HMAC: the inner hash's padding in the message, the outer
 * block itself). The engine runs the two as one chain, so the hash costs no
 * second request. count returns the number of messages completed: if one
 * after the first fails, the call succeeds with that prefix and leaves the
 * others as they were.
 */
#define HWCRYPTO_CTR_SHA1_MAX 64
struct hwcrypto_ctr_sha1_packet {
 __aligned_u64 data;
 __u32 length, crypt_offset, crypt_length, reserved;
 __u8 iv[16], state[20];
 __u32 reserved2;
};
struct hwcrypto_ctr_sha1_request {
 __u32 version, count;
 __u8 key[16], state[20];
 __u32 reserved;
 __aligned_u64 packets;
 __u32 reserved2[4];
};
#define HWCRYPTO_CTR_SHA1 _IOWR('K', 0xc5, struct hwcrypto_ctr_sha1_request)
#define HWCRYPTO_INFO _IOR('K', 0xc3, struct hwcrypto_info)
#endif
