// SPDX-License-Identifier: GPL-2.0-only
/*
 * Device qualification of the XTheadVector ChaCha backend (NanoKVM OS).
 * Carries the same assembly and glue as lib/crypto/riscv/chacha.h and checks
 * them against chacha_block_generic(), also through the library entry point
 * chacha_crypt() of whichever libchacha is loaded, then times chacha_crypt(),
 * the vector backend and the kernel's Poly1305. Loading prints the results and fails with
 * -EAGAIN on purpose, so nothing stays loaded.
 */
#include <asm/simd.h>
#include <asm/vector.h>
#include <crypto/chacha.h>
#include <crypto/internal/simd.h>
#include <crypto/poly1305.h>
#include <crypto/utils.h>
#include <linux/bottom_half.h>
#include <linux/kernel.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/random.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/vmalloc.h>

static int min_bytes = 2 * CHACHA_BLOCK_SIZE + 1;
module_param(min_bytes, int, 0444);
static int bench_ms = 300;
module_param(bench_ms, int, 0444);

#define GROUP (5 * CHACHA_BLOCK_SIZE)

asmlinkage void chacha_xtheadvector(struct chacha_state *state, const u8 *in,
				    u8 *out, size_t ngroups, int nrounds);

/* Same as chacha_crypt_xtheadvector() in lib/crypto/riscv/chacha.h. */
static void xtv_crypt(struct chacha_state *state, u8 *dst, const u8 *src,
		      unsigned int bytes, int nrounds)
{
	u8 buffer[GROUP];
	unsigned int groups = bytes / GROUP, tail = bytes % GROUP;

	kernel_vector_begin();
	if (groups) {
		chacha_xtheadvector(state, src, dst, groups, nrounds);
		src += groups * GROUP;
		dst += groups * GROUP;
	}
	if (tail) {
		u32 counter = state->x[12];

		memcpy(buffer, src, tail);
		chacha_xtheadvector(state, buffer, buffer, 1, nrounds);
		memcpy(dst, buffer, tail);
		state->x[12] = counter + DIV_ROUND_UP(tail, CHACHA_BLOCK_SIZE);
		memzero_explicit(buffer, tail);
	}
	kernel_vector_end();
}

/* The reference: chacha_crypt_generic() of lib/crypto/chacha.c. */
static void ref_crypt(struct chacha_state *state, u8 *dst, const u8 *src,
		      unsigned int bytes, int nrounds)
{
	u8 stream[CHACHA_BLOCK_SIZE] __aligned(sizeof(long));

	while (bytes >= CHACHA_BLOCK_SIZE) {
		chacha_block_generic(state, stream, nrounds);
		crypto_xor_cpy(dst, src, stream, CHACHA_BLOCK_SIZE);
		bytes -= CHACHA_BLOCK_SIZE;
		dst += CHACHA_BLOCK_SIZE;
		src += CHACHA_BLOCK_SIZE;
	}
	if (bytes) {
		chacha_block_generic(state, stream, nrounds);
		crypto_xor_cpy(dst, src, stream, bytes);
	}
}

static const char sunscreen[] =
	"Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.";
static const char rfc242[] =
	"6e2e359a2568f98041ba0728dd0d6981e97e7aec1d4360c20a27afccfd9fae0b"
	"f91b65c5524733ab8f593dabcd62b3571639d624e65152ab8f530c359f0861d8"
	"07ca0dbf500d6a6156a38e088a22b65e52bc514d16ccf806818ce91ab7793736"
	"5af90bbf74a35be6b40b8eedf2785e42874d";

static int check_rfc(void)
{
	struct chacha_state st;
	u32 key[8];
	u8 iv[16] = { 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x4a, 0, 0, 0, 0 };
	u8 out[sizeof(sunscreen)], want[sizeof(sunscreen)];
	size_t len = sizeof(sunscreen) - 1;
	int i;

	for (i = 0; i < 8; i++)
		key[i] = 0x03020100u + 0x04040404u * i;
	if (hex2bin(want, rfc242, len))
		return -EINVAL;
	chacha_init(&st, key, iv);
	xtv_crypt(&st, out, (const u8 *)sunscreen, len, 20);
	if (memcmp(out, want, len) || st.x[12] != 1 + DIV_ROUND_UP(len, 64)) {
		pr_err("chacha_xtv: RFC 8439 2.4.2 FAILED\n");
		return -EINVAL;
	}
	pr_info("chacha_xtv: RFC 8439 2.4.2 ok\n");
	return 0;
}

/* Random lengths, offsets, counters (also across the 32-bit wrap), rounds. */
static int check_random(int cases, bool bh, bool lib)
{
	u8 *a = kmalloc(4096 + 192, GFP_KERNEL), *b = kmalloc(4096 + 192, GFP_KERNEL);
	u8 *src = kmalloc(4096 + 128, GFP_KERNEL);
	int n, fails = 0;

	if (!a || !b || !src) {
		fails = -ENOMEM;
		goto out;
	}
	for (n = 0; n < cases; n++) {
		struct chacha_state s1, s2;
		u32 key[8], r = get_random_u32();
		u8 iv[16];
		unsigned int len = 1 + get_random_u32_below(n % 4 ? 1500 : 4096);
		unsigned int so = get_random_u32_below(64), d1 = get_random_u32_below(64);
		int rounds = (r & 1) ? 20 : 12;
		bool inplace = r & 2;
		u8 *dst2 = b + d1;

		get_random_bytes(key, sizeof(key));
		get_random_bytes(iv, sizeof(iv));
		if (n % 5 == 0)	/* counter near the 32-bit wrap */
			put_unaligned_le32(0xffffffffu - get_random_u32_below(80), iv);
		get_random_bytes(src, 4096 + 128);
		chacha_init(&s1, key, iv);
		s2 = s1;
		ref_crypt(&s1, a, src + so, len, rounds);
		if (inplace) {
			memcpy(dst2, src + so, len);
		}
		if (bh)
			local_bh_disable();
		if (!crypto_simd_usable()) {
			if (bh)
				local_bh_enable();
			pr_err("chacha_xtv: simd not usable (bh=%d)\n", bh);
			fails++;
			break;
		}
		if (lib)	/* the library entry point, as its users call it */
			chacha_crypt(&s2, dst2, inplace ? dst2 : src + so, len, rounds);
		else
			xtv_crypt(&s2, dst2, inplace ? dst2 : src + so, len, rounds);
		if (bh)
			local_bh_enable();
		if (memcmp(a, dst2, len) || s1.x[12] != s2.x[12] ||
		    memcmp(&s1, &s2, sizeof(s1))) {
			if (fails++ < 5)
				pr_err("chacha_xtv: MISMATCH len %u so %u do %u rounds %d inplace %d ctr %08x->%08x/%08x\n",
				       len, so, d1, rounds, inplace,
				       get_unaligned_le32(iv), s1.x[12], s2.x[12]);
		}
		if (n % 256 == 0)
			cond_resched();
	}
	pr_info("chacha_xtv: %d random cases (%s): %d failures\n", cases,
		bh ? "bh disabled" : "process", fails);
out:
	kfree(a);
	kfree(b);
	kfree(src);
	return fails;
}

typedef void (*crypt_fn)(struct chacha_state *, u8 *, const u8 *, unsigned int, int);

/* MB/s of fn over len bytes, by wall time, preemption allowed in between. */
static u64 rate(crypt_fn fn, u8 *buf, unsigned int len)
{
	struct chacha_state st;
	u32 key[8] = { 1, 2, 3, 4, 5, 6, 7, 8 };
	u8 iv[16] = { 0 };
	u64 start, end, deadline, bytes = 0;

	chacha_init(&st, key, iv);
	start = ktime_get_ns();
	deadline = start + (u64)bench_ms * NSEC_PER_MSEC;
	do {
		int i;

		for (i = 0; i < 16; i++)
			fn(&st, buf, buf, len, 20);
		bytes += 16 * (u64)len;
		cond_resched();
		end = ktime_get_ns();
	} while (end < deadline);
	return div64_u64(bytes * 1000, end - start); /* bytes/us = MB/s */
}

static u64 poly_rate(u8 *buf, unsigned int len)
{
	struct poly1305_desc_ctx ctx;
	u8 key[POLY1305_KEY_SIZE] = { 7 }, mac[POLY1305_DIGEST_SIZE];
	u64 start, end, deadline, bytes = 0;

	start = ktime_get_ns();
	deadline = start + (u64)bench_ms * NSEC_PER_MSEC;
	do {
		int i;

		for (i = 0; i < 16; i++) {
			poly1305_init(&ctx, key);
			poly1305_update(&ctx, buf, len);
			poly1305_final(&ctx, mac);
		}
		bytes += 16 * (u64)len;
		cond_resched();
		end = ktime_get_ns();
	} while (end < deadline);
	return div64_u64(bytes * 1000, end - start);
}

static void bench(void)
{
	static const unsigned int sizes[] = { 64, 128, 129, 192, 256, 320, 512, 1024, 1420, 4096, 16384 };
	u8 *buf = vmalloc(16384);
	int i;

	if (!buf)
		return;
	memset(buf, 0x5a, 16384);
	pr_info("chacha_xtv: %6s %10s %10s %10s  (MB/s)\n", "bytes", "generic", "xtheadvec", "poly1305");
	for (i = 0; i < ARRAY_SIZE(sizes); i++) {
		u64 g = rate(chacha_crypt, buf, sizes[i]);
		u64 v = rate(xtv_crypt, buf, sizes[i]);
		u64 p = poly_rate(buf, sizes[i]);

		pr_info("chacha_xtv: %6u %10llu %10llu %10llu\n", sizes[i], g, v, p);
	}
	vfree(buf);
}

static int __init chacha_xtv_init(void)
{
	int fails;

	if (!has_xtheadvector() || riscv_vector_vlen() < 128) {
		pr_err("chacha_xtv: no XTheadVector (vlen %d)\n", riscv_vector_vlen());
		return -ENODEV;
	}
	pr_info("chacha_xtv: vlen %d, min_bytes %d\n", riscv_vector_vlen(), min_bytes);
	fails = check_rfc();
	fails += check_random(20000, false, false);
	fails += check_random(5000, true, false);
	fails += check_random(10000, false, true);
	fails += check_random(5000, true, true);
	if (fails) {
		pr_err("chacha_xtv: FAILED\n");
		return -EINVAL;
	}
	bench();
	pr_info("chacha_xtv: done\n");
	return -EAGAIN;
}
module_init(chacha_xtv_init);

MODULE_DESCRIPTION("XTheadVector ChaCha qualification");
MODULE_LICENSE("GPL");
