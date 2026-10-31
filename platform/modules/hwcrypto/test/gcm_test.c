// SPDX-License-Identifier: GPL-2.0-only
/*
 * nanokvm-gcm-aes against the synchronous software gcm(aes): random keys (128,
 * 192, 256 bits), IVs, associated data, lengths, tag sizes, in place and out of
 * place, encryption and decryption, tampered tags; then throughput of the
 * driver and of the gcm template over the engine's ctr(aes). Loading prints
 * the results and fails with -EAGAIN on purpose.
 */
#include <crypto/aead.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/random.h>
#include <linux/scatterlist.h>
#include <linux/slab.h>

static int cases = 3000;
module_param(cases, int, 0444);
static int bench_ms = 400;
module_param(bench_ms, int, 0444);
static char *drv = "nanokvm-gcm-aes";
module_param(drv, charp, 0444);

#define MAXLEN (3 * 4096 + 100)

static int run(struct crypto_aead *tfm, bool enc, u8 *src, u8 *dst, unsigned int assoclen,
	       unsigned int cryptlen, u8 *iv)
{
	struct aead_request *req = aead_request_alloc(tfm, GFP_KERNEL);
	struct scatterlist ssg, dsg;
	DECLARE_CRYPTO_WAIT(wait);
	unsigned int total = assoclen + cryptlen + (enc ? crypto_aead_authsize(tfm) : 0);
	int err;

	if (!req)
		return -ENOMEM;
	sg_init_one(&ssg, src, total);
	sg_init_one(&dsg, dst, total);
	aead_request_set_callback(req, CRYPTO_TFM_REQ_MAY_SLEEP | CRYPTO_TFM_REQ_MAY_BACKLOG,
				  crypto_req_done, &wait);
	aead_request_set_ad(req, assoclen);
	aead_request_set_crypt(req, &ssg, src == dst ? &ssg : &dsg, cryptlen, iv);
	err = crypto_wait_req(enc ? crypto_aead_encrypt(req) : crypto_aead_decrypt(req), &wait);
	aead_request_free(req);
	return err;
}

static int check(void)
{
	struct crypto_aead *t = crypto_alloc_aead(drv, 0, 0);
	struct crypto_aead *ref = crypto_alloc_aead("gcm(aes)", 0, CRYPTO_ALG_ASYNC);
	u8 *a = kmalloc(MAXLEN + 16, GFP_KERNEL), *b = kmalloc(MAXLEN + 16, GFP_KERNEL), *c = kmalloc(MAXLEN + 16, GFP_KERNEL);
	u8 key[32], iv[12], iv2[12];
	int i, fails = 0, err = 0;

	if (IS_ERR(t) || IS_ERR(ref) || !a || !b || !c) {
		pr_err("gcm_drv: setup failed (%d, %d)\n", PTR_ERR_OR_ZERO(t), PTR_ERR_OR_ZERO(ref));
		err = -ENODEV;
		goto out;
	}
	pr_info("gcm_drv: testing %s against %s\n", crypto_aead_driver_name(t),
		crypto_aead_driver_name(ref));
	for (i = 0; i < cases && fails < 5; i++) {
		static const unsigned int keylens[] = { 16, 24, 32 };
		unsigned int keylen = keylens[i % 3];
		unsigned int authsize = (i % 7 == 0) ? 4 + 4 * get_random_u32_below(3) :
					12 + get_random_u32_below(5);
		unsigned int assoclen = (i % 5 == 0) ? 0 : get_random_u32_below(i % 11 == 0 ? 600 : 33);
		unsigned int cryptlen = (i % 13 == 0) ? 0 :
			(i % 4 == 0) ? 4096 * (1 + get_random_u32_below(3)) - 16 + get_random_u32_below(33) :
			get_random_u32_below(i % 3 ? 2000 : MAXLEN - 700);
		bool inplace = i & 1;
		unsigned int total = assoclen + cryptlen;

		get_random_bytes(key, keylen);
		get_random_bytes(iv, 12);
		get_random_bytes(a, total);
		if (crypto_aead_setkey(t, key, keylen) || crypto_aead_setkey(ref, key, keylen) ||
		    crypto_aead_setauthsize(t, authsize) || crypto_aead_setauthsize(ref, authsize)) {
			pr_err("gcm_drv: setkey/setauthsize failed\n");
			fails++;
			continue;
		}
		/* Reference ciphertext in c. */
		memcpy(c, a, total);
		memcpy(iv2, iv, 12);
		err = run(ref, true, c, c, assoclen, cryptlen, iv2);
		/* Driver encryption into b. */
		memcpy(iv2, iv, 12);
		memset(b, 0xa5, total + authsize);
		if (inplace) {
			memcpy(b, a, total);
			err |= run(t, true, b, b, assoclen, cryptlen, iv2);
		} else {
			memcpy(b, c, assoclen);	/* the AAD is not copied to dst */
			err |= run(t, true, a, b, assoclen, cryptlen, iv2);
		}
		if (err || memcmp(b, c, total + authsize)) {
			pr_err("gcm_drv: encrypt mismatch case %d key %u aad %u len %u tag %u inplace %d err %d\n",
			       i, keylen, assoclen, cryptlen, authsize, inplace, err);
			fails++;
			continue;
		}
		/* Driver decryption of the reference ciphertext. */
		memcpy(iv2, iv, 12);
		if (inplace) {
			memcpy(b, c, total + authsize);
			err = run(t, false, b, b, assoclen, cryptlen + authsize, iv2);
		} else {
			memset(b, 0x5a, total);
			memcpy(b, c, assoclen);
			err = run(t, false, c, b, assoclen, cryptlen + authsize, iv2);
		}
		if (err || memcmp(b, a, total)) {
			pr_err("gcm_drv: decrypt mismatch case %d key %u aad %u len %u tag %u inplace %d err %d\n",
			       i, keylen, assoclen, cryptlen, authsize, inplace, err);
			fails++;
			continue;
		}
		/* A flipped bit in the AAD, ciphertext or tag must fail. */
		memcpy(b, c, total + authsize);
		b[get_random_u32_below(total + authsize)] ^= 1 << get_random_u32_below(8);
		memcpy(iv2, iv, 12);
		err = run(t, false, b, b, assoclen, cryptlen + authsize, iv2);
		if (err != -EBADMSG) {
			pr_err("gcm_drv: tampered case %d accepted, err %d\n", i, err);
			fails++;
		}
		err = 0;
		cond_resched();
	}
	pr_info("gcm_drv: %d cases, %d failures\n", i, fails);
	err = fails ? -EINVAL : 0;
out:
	kfree(a);
	kfree(b);
	kfree(c);
	if (!IS_ERR(t))
		crypto_free_aead(t);
	if (!IS_ERR(ref))
		crypto_free_aead(ref);
	return err;
}

static u64 rate(const char *name, bool enc, unsigned int len)
{
	struct crypto_aead *tfm = crypto_alloc_aead(name, 0, 0);
	u8 key[16] = { 7 }, iv[12] = {}, *buf, *out;
	u64 start, end, bytes = 0;
	int err = 0;

	if (IS_ERR(tfm))
		return 0;
	buf = kmalloc(len + 32, GFP_KERNEL);
	out = kmalloc(len + 32, GFP_KERNEL);
	if (!buf || !out || crypto_aead_setkey(tfm, key, 16) || crypto_aead_setauthsize(tfm, 16))
		goto out;
	memset(buf, 0x5a, len + 32);
	if (!enc)	/* decrypt the same valid ciphertext out of place */
		err = run(tfm, true, buf, buf, 16, len, iv);
	start = ktime_get_ns();
	do {
		memset(iv, 0, 12);
		if (enc)
			err |= run(tfm, true, buf, buf, 16, len, iv);
		else
			err |= run(tfm, false, buf, out, 16, len + 16, iv);
		bytes += len;
		cond_resched();
		end = ktime_get_ns();
	} while (!err && end - start < (u64)bench_ms * NSEC_PER_MSEC);
	bytes = err ? 0 : div64_u64(bytes * 1000, end - start);
out:
	kfree(buf);
	kfree(out);
	crypto_free_aead(tfm);
	return bytes;
}

static int __init gcm_drv_init(void)
{
	static const unsigned int sizes[] = { 64, 512, 1420, 4096, 16384, 65536 };
	int i, err = check();

	if (err)
		return err;
	pr_info("gcm_drv: %6s %13s %13s %11s %11s (MB/s)\n",
		"bytes", "template-enc", "template-dec", "driver-enc", "driver-dec");
	for (i = 0; i < ARRAY_SIZE(sizes); i++)
		pr_info("gcm_drv: %6u %13llu %13llu %11llu %11llu\n", sizes[i],
			rate("gcm_base(nanokvm-aes-ctr,ghash-lib)", true, sizes[i]),
			rate("gcm_base(nanokvm-aes-ctr,ghash-lib)", false, sizes[i]),
			rate(drv, true, sizes[i]), rate(drv, false, sizes[i]));
	return -EAGAIN;
}
module_init(gcm_drv_init);

MODULE_DESCRIPTION("nanokvm-gcm-aes qualification");
MODULE_LICENSE("GPL");
