/* SPDX-License-Identifier: GPL-2.0-only */
/* nanokvm_c906l for other modules: GHASH jobs on the small core (nanokvm_hwcrypto's gcm(aes)). */
#ifndef NANOKVM_C906L_API_H
#define NANOKVM_C906L_API_H

#include <linux/types.h>

struct c906l_owner {
	/*
	 * The job's result, in bottom-half context, or NULL: the small core
	 * was given up and the owner does the work itself (its staging buffer
	 * is still as it put it there). The slot is the owner's until this
	 * returns.
	 */
	void (*done)(struct c906l_owner *o, const u64 *acc, s64 value);
	u8 *stage;			/* C906L_STAGE bytes, the slot's input */
	const u8 *out;			/* the slot's output, valid in done() */
	u64 seq;
};

/* A slot and its staging buffer, or false: no small core, or the ring full. */
bool c906l_reserve(struct c906l_owner *o);
/*
 * GHASH of len bytes of the staging buffer continuing acc (lo, hi), then the
 * 16 bytes of lens; len 0 only gives the slot back. Never fails: if the small
 * core is gone, done() gets NULL.
 */
void c906l_submit_ghash(struct c906l_owner *o, const u8 *h, const u64 *acc, const u8 *lens, u32 len);

#endif
