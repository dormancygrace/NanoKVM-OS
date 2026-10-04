/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef C906_CRYPTO_CANDIDATES_H
#define C906_CRYPTO_CANDIDATES_H
#include <crypto/chacha.h>
#include <crypto/chacha20poly1305.h>
#include <linux/scatterlist.h>
#include "protocol.h"
void kc_tables_init(void);
u32 kc_crc_scalar(u32 crc, const u8 *p, size_t n, bool castagnoli);
u32 kc_crc_vector(u32 crc, const u8 *p, size_t n, bool castagnoli);
u32 kc_copy_csum(void *dst, const void *src, size_t n);
void kc_chacha_blocks(struct chacha_state *, u8 *, size_t, int);
void kc_xor_bytes(u8 *, const u8 *, const u8 *, size_t);
/* This namespaced library hook is used by the copied kernel AEAD source. */
void kc_candidate_chacha_crypt(struct chacha_state *, u8 *, const u8 *, unsigned int, int);
bool kc_chacha20poly1305_encrypt_sg_inplace(struct scatterlist *, size_t,
	const u8 *, size_t, u64, const u8 key[32]);
bool kc_chacha20poly1305_decrypt_sg_inplace(struct scatterlist *, size_t,
	const u8 *, size_t, u64, const u8 key[32]);
#endif
