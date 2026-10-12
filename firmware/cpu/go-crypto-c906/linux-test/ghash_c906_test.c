// SPDX-License-Identifier: GPL-2.0-only
/*
 * Device qualification of the C906 scalar GHASH (NanoKVM OS). Carries the
 * same assembly and key preparation as lib/crypto/riscv/gf128hash.h and checks
 * them against a bit-serial GHASH, also through the library entry points
 * ghash_update()/ghash_final() of whichever libgf128hash is loaded (with
 * partial blocks), then times the library, the assembly and gcm(aes).
 * Loading prints the results and fails with -EAGAIN on purpose, so nothing
 * stays loaded.
 */
#include <crypto/aead.h>
#include <crypto/gf128hash.h>
#include <linux/kernel.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/random.h>
#include <linux/scatterlist.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/unaligned.h>
#include <linux/vmalloc.h>

static int cases = 20000;
module_param(cases, int, 0444);
static int bench_ms = 300;
module_param(bench_ms, int, 0444);

asmlinkage void ghash_blocks_c906(struct polyval_elem *acc, const u64 kp[40],
				  const u8 *data, size_t nblocks);

/* Same as ghash_c906_preparekey() in lib/crypto/riscv/gf128hash.h. */
static void c906_preparekey(u64 kp[40], const struct polyval_elem *h)
{
	static const u64 holes[4] = {
		0x1111111111111111ULL, 0x2222222222222222ULL,
		0x4444444444444444ULL, 0x8888888888888888ULL,
	};
	u64 lo = le64_to_cpu(h->lo), hi = le64_to_cpu(h->hi);
	u64 parts[3] = { lo, hi, lo ^ hi };
	int p, r, j;

	for (j = 0; j < 4; j++)
		kp[j] = holes[j];
	for (p = 0; p < 3; p++) {
		u64 x0 = (u32)parts[p], x1 = parts[p] >> 32;
		u64 ops[3] = { x0, x1, x0 ^ x1 };

		for (r = 0; r < 3; r++)
			for (j = 0; j < 4; j++)
				kp[4 + 4 * (3 * p + r) + j] = ops[r] & holes[j];
	}
}

struct c906_key {
	struct ghash_key key;	/* for ->h, in POLYVAL format */
	u64 kp[40];
};

static void c906_setkey(struct c906_key *k, const u8 raw[GHASH_BLOCK_SIZE])
{
	ghash_preparekey(&k->key, raw);
	c906_preparekey(k->kp, &k->key.h);
}

/* GHASH of whole blocks with the assembly, as a GHASH-format digest. */
static void c906_ghash(const struct c906_key *k, const u8 *data, size_t nblocks,
		       u8 out[GHASH_BLOCK_SIZE])
{
	struct polyval_elem acc = {};

	ghash_blocks_c906(&acc, k->kp, data, nblocks);
	put_unaligned_be64(le64_to_cpu(acc.hi), out);
	put_unaligned_be64(le64_to_cpu(acc.lo), out + 8);
}

/* NIST SP 800-38D multiplication, one bit at a time: x = x * h. */
static void ref_mul(u8 x[16], const u8 h[16])
{
	u8 z[16] = {}, v[16];
	int i, k;

	memcpy(v, h, 16);
	for (i = 0; i < 128; i++) {
		u8 lsb = v[15] & 1;

		if ((x[i / 8] >> (7 - i % 8)) & 1)
			for (k = 0; k < 16; k++)
				z[k] ^= v[k];
		for (k = 15; k > 0; k--)
			v[k] = (v[k] >> 1) | (v[k - 1] << 7);
		v[0] >>= 1;
		if (lsb)
			v[0] ^= 0xe1;
	}
	memcpy(x, z, 16);
}

/* y = GHASH continued over len bytes, a last partial block zero-padded. */
static void ref_ghash(const u8 h[16], const u8 *data, size_t len, u8 y[16])
{
	size_t i, k;

	for (i = 0; i < len; i += 16) {
		for (k = 0; k < 16 && i + k < len; k++)
			y[k] ^= data[i + k];
		ref_mul(y, h);
	}
}

static int check(void)
{
	u8 *buf = vmalloc(1024 + 16), raw[16], want[16], got[16];
	struct c906_key *k = kmalloc(sizeof(*k), GFP_KERNEL);
	struct ghash_ctx ctx;
	int i, fails = 0, lib_fails = 0;

	if (!buf || !k) {
		vfree(buf);
		kfree(k);
		return -ENOMEM;
	}
	for (i = 0; i < cases; i++) {
		size_t len = i < 64 ? 16 * (i % 4) : get_random_u32_below(1025);
		unsigned int off = get_random_u32_below(16);	/* any alignment */
		size_t split = len ? get_random_u32_below(len + 1) : 0;

		get_random_bytes(raw, 16);
		get_random_bytes(buf + off, len);
		if (i % 97 == 0)
			memset(raw, 0xff, 16);
		if (i % 89 == 0)
			memset(buf + off, 0xff, len);
		c906_setkey(k, raw);

		/* The assembly on the whole blocks. */
		memset(want, 0, 16);
		ref_ghash(raw, buf + off, len & ~15, want);
		c906_ghash(k, buf + off, len / 16, got);
		if (memcmp(got, want, 16) && fails++ < 3)
			pr_err("ghash_c906: assembly mismatch, %zu blocks, offset %u\n",
			       len / 16, off);

		/* The library, in two updates, with a partial block. */
		ref_ghash(raw, buf + off + (len & ~15), len & 15, want);
		ghash_init(&ctx, &k->key);
		ghash_update(&ctx, buf + off, split);
		ghash_update(&ctx, buf + off + split, len - split);
		ghash_final(&ctx, got);
		if (memcmp(got, want, 16) && lib_fails++ < 3)
			pr_err("ghash_c906: ghash_update() mismatch, %zu bytes split at %zu\n",
			       len, split);
		cond_resched();
	}
	memzero_explicit(k, sizeof(*k));
	kfree(k);
	vfree(buf);
	pr_info("ghash_c906: %d cases against a bit-serial GHASH: assembly %d failures, ghash_update() %d failures\n",
		cases, fails, lib_fails);
	return fails || lib_fails ? -EINVAL : 0;
}

static u64 mbps(u64 bytes, u64 ns)
{
	return div64_u64(bytes * 1000, ns);
}

static u64 rate_lib(const u8 *buf, unsigned int len)
{
	struct ghash_key key;
	struct ghash_ctx ctx;
	u8 raw[16] = { 1 }, out[16];
	u64 start, end, bytes = 0;

	ghash_preparekey(&key, raw);
	start = ktime_get_ns();
	do {
		ghash_init(&ctx, &key);
		ghash_update(&ctx, buf, len);
		ghash_final(&ctx, out);
		bytes += len;
		end = ktime_get_ns();
	} while (end - start < (u64)bench_ms * NSEC_PER_MSEC);
	return mbps(bytes, end - start);
}

static u64 rate_asm(const u8 *buf, unsigned int len)
{
	static struct c906_key k;
	u8 raw[16] = { 1 }, out[16];
	u64 start, end, bytes = 0;

	c906_setkey(&k, raw);
	start = ktime_get_ns();
	do {
		c906_ghash(&k, buf, len / 16, out);
		bytes += len;
		end = ktime_get_ns();
	} while (end - start < (u64)bench_ms * NSEC_PER_MSEC);
	return mbps(bytes, end - start);
}

/* gcm(aes) encryption; mask CRYPTO_ALG_ASYNC asks for a synchronous one. */
static u64 rate_gcm(u32 mask, unsigned int len, char *driver, size_t drvlen)
{
	struct crypto_aead *tfm = crypto_alloc_aead("gcm(aes)", 0, mask);
	struct aead_request *req = NULL;
	struct scatterlist sg;
	DECLARE_CRYPTO_WAIT(wait);
	u8 key[16] = { 7 }, iv[12] = {};
	u8 *buf = NULL;
	u64 start, end, bytes = 0;
	int err = 0;

	if (IS_ERR(tfm))
		return 0;
	strscpy(driver, crypto_aead_driver_name(tfm), drvlen);
	buf = kmalloc(len + 16, GFP_KERNEL);
	req = aead_request_alloc(tfm, GFP_KERNEL);
	if (!buf || !req || crypto_aead_setkey(tfm, key, 16) ||
	    crypto_aead_setauthsize(tfm, 16))
		goto out;
	memset(buf, 0x5a, len + 16);
	sg_init_one(&sg, buf, len + 16);
	aead_request_set_callback(req, CRYPTO_TFM_REQ_MAY_SLEEP | CRYPTO_TFM_REQ_MAY_BACKLOG,
				  crypto_req_done, &wait);
	aead_request_set_ad(req, 0);
	start = ktime_get_ns();
	do {
		aead_request_set_crypt(req, &sg, &sg, len, iv);
		err = crypto_wait_req(crypto_aead_encrypt(req), &wait);
		bytes += len;
		cond_resched();
		end = ktime_get_ns();
	} while (!err && end - start < (u64)bench_ms * NSEC_PER_MSEC);
	bytes = err ? 0 : mbps(bytes, end - start);
out:
	aead_request_free(req);
	kfree(buf);
	crypto_free_aead(tfm);
	return bytes;
}

static int __init ghash_c906_init(void)
{
	static const unsigned int sizes[] = { 64, 512, 1420, 16384 };
	char hw[CRYPTO_MAX_ALG_NAME] = "-", sw[CRYPTO_MAX_ALG_NAME] = "-";
	u8 *buf;
	int i, err = check();

	if (err)
		return err;
	buf = vmalloc(16384);
	if (!buf)
		return -ENOMEM;
	memset(buf, 0x33, 16384);
	pr_info("ghash_c906: %6s %10s %10s %10s %10s  (MB/s)\n",
		"bytes", "library", "assembly", "gcm", "gcm-sync");
	for (i = 0; i < ARRAY_SIZE(sizes); i++) {
		u64 lib = rate_lib(buf, sizes[i]);
		u64 as = rate_asm(buf, sizes[i]);
		u64 gcm = rate_gcm(0, sizes[i], hw, sizeof(hw));
		u64 gsync = rate_gcm(CRYPTO_ALG_ASYNC, sizes[i], sw, sizeof(sw));

		pr_info("ghash_c906: %6u %10llu %10llu %10llu %10llu\n",
			sizes[i], lib, as, gcm, gsync);
	}
	pr_info("ghash_c906: gcm = %s; gcm-sync = %s\n", hw, sw);
	vfree(buf);
	return -EAGAIN;
}
module_init(ghash_c906_init);

MODULE_DESCRIPTION("C906 scalar GHASH qualification");
MODULE_LICENSE("GPL");
