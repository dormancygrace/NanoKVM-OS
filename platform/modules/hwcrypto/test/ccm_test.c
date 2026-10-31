// SPDX-License-Identifier: GPL-2.0-only
/* Device qualification: RFC3610 vector 1, software differential tests,
 * invalid requests and CCM/GCM throughput. Repeated test keys/nonces are
 * for timing only. Successful load means PASS; no user data is processed. */
#include <crypto/aead.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/random.h>
#include <linux/scatterlist.h>
#include <linux/slab.h>
#include <linux/workqueue.h>

static unsigned int cases = 3000;
module_param(cases, uint, 0444);
static unsigned int bench_ms = 500;
module_param(bench_ms, uint, 0444);
#define MAXLEN (65536 + 65536 + 16)

/* Splits cut through AAD, blocks and DMA chunks; the second list is chained. */
static void segments(struct scatterlist sg[4], u8 *buf, unsigned int len, bool split)
{
	unsigned int second = min(len > 2 ? len - 2 : 0, 4093U);
	if (!split || len < 3) {
		sg_init_one(sg, buf, len);
		return;
	}
	sg_init_table(sg, 3);
	sg_set_buf(sg, buf, 1);
	sg_set_buf(sg + 1, buf + 1, second);
	sg_init_one(sg + 3, buf + 1 + second, len - 1 - second);
	sg_chain(sg, 3, sg + 3);
}

static int run(struct crypto_aead *tfm, bool enc, u8 *src, u8 *dst,
	       unsigned int aad, unsigned int len, const u8 *nonce, bool split, bool queued)
{
	struct aead_request *req = aead_request_alloc(tfm, GFP_KERNEL);
	struct scatterlist ssg[4], dsg[4];
	DECLARE_CRYPTO_WAIT(wait);
	u8 iv[16];
	unsigned int tag = crypto_aead_authsize(tfm);
	int err;
	if (!req)
		return -ENOMEM;
	memcpy(iv, nonce, sizeof(iv));
	segments(ssg, src, aad + len + (enc ? 0 : tag), split);
	segments(dsg, dst, aad + len + (enc ? tag : 0), split);
	if (src == dst)
		segments(ssg, src, aad + len + tag, split);
	aead_request_set_callback(req, CRYPTO_TFM_REQ_MAY_BACKLOG |
		(queued ? 0 : CRYPTO_TFM_REQ_MAY_SLEEP), crypto_req_done, &wait);
	aead_request_set_ad(req, aad);
	aead_request_set_crypt(req, ssg, src == dst ? ssg : dsg, len + (enc ? 0 : tag), iv);
	err = crypto_wait_req(enc ? crypto_aead_encrypt(req) : crypto_aead_decrypt(req), &wait);
	aead_request_free(req);
	return err;
}

static int known_vector(struct crypto_aead *tfm)
{
	static const u8 expected[] = {
		0x58,0x8c,0x97,0x9a,0x61,0xc6,0x63,0xd2,0xf0,0x66,0xd0,0xc2,0xc0,0xf9,0x89,0x80,
		0x6d,0x5f,0x6b,0x61,0xda,0xc3,0x84,0x17,0xe8,0xd1,0x2c,0xfd,0xf9,0x26,0xe0,
	};
	u8 key[16], iv[16] = {1,0,0,0,3,2,1,0,0xa0,0xa1,0xa2,0xa3,0xa4,0xa5};
	u8 *buf = kmalloc(39, GFP_KERNEL);
	int i, err;
	if (!buf)
		return -ENOMEM;
	for (i = 0; i < 16; i++)
		key[i] = 0xc0 + i;
	for (i = 0; i < 31; i++)
		buf[i] = i;
	err = crypto_aead_setkey(tfm, key, 16) ?: crypto_aead_setauthsize(tfm, 8);
	if (!err)
		err = run(tfm, true, buf, buf, 8, 23, iv, true, true);
	if (!err && memcmp(buf + 8, expected, sizeof(expected)))
		err = -EINVAL;
	kfree_sensitive(buf);
	return err;
}

static int invalid(struct crypto_aead *tfm)
{
	struct aead_request *req = aead_request_alloc(tfm, GFP_KERNEL);
	struct scatterlist sg;
	u8 key[32] = {}, iv[16] = {1}, *buf = kzalloc(32, GFP_KERNEL);
	int i, err = -EINVAL;
	static const struct { u8 lp; u32 aad, len; int error; } bad[] = {
		{0,0,0,-EINVAL}, {8,0,0,-EINVAL}, {255,0,0,-EINVAL},
		{1,0,65536,-EOVERFLOW}, {2,0,16777216,-EOVERFLOW},
		{7,U32_MAX,1,-EOVERFLOW}, {7,U32_MAX-15,0,-EOVERFLOW},
	};
	if (!req || !buf) {
		err = -ENOMEM;
		goto out;
	}
	for (i = 0; i <= 33; i++) {
		if (i == 16 || i == 24 || i == 32)
			continue;
		if (crypto_aead_setkey(tfm, key, i) != -EINVAL)
			goto out;
	}
	if (crypto_aead_setkey(tfm, key, 16))
		goto out;
	for (i = 0; i <= 17; i++)
		if ((i < 4 || i > 16 || i % 2) && crypto_aead_setauthsize(tfm, i) != -EINVAL)
			goto out;
	if (crypto_aead_setauthsize(tfm, 16))
		goto out;
	sg_init_one(&sg, buf, 32);
	aead_request_set_callback(req, CRYPTO_TFM_REQ_MAY_SLEEP, NULL, NULL);
	for (i = 0; i < ARRAY_SIZE(bad); i++) {
		iv[0] = bad[i].lp;
		aead_request_set_ad(req, bad[i].aad);
		aead_request_set_crypt(req, &sg, &sg, bad[i].len, iv);
		if (crypto_aead_encrypt(req) != bad[i].error)
			goto out;
	}
	iv[0] = 1;
	aead_request_set_ad(req, 0);
	aead_request_set_crypt(req, &sg, &sg, 15, iv);
	if (crypto_aead_decrypt(req) != -EINVAL)
		goto out;
	err = 0;
out:
	if (req)
		aead_request_free(req);
	kfree_sensitive(buf);
	return err;
}

struct parallel_job { struct work_struct work; unsigned int index; int err; };

static void parallel_worker(struct work_struct *work)
{
	struct parallel_job *job = container_of(work, struct parallel_job, work);
	bool ccm = !(job->index & 1);
	struct crypto_aead *t = crypto_alloc_aead(ccm ? "nanokvm-ccm-aes" : "nanokvm-gcm-aes", 0, 0);
	struct crypto_aead *ref = crypto_alloc_aead(ccm ? "ccm(aes)" : "gcm(aes)", 0,
		CRYPTO_ALG_ASYNC | CRYPTO_ALG_NEED_FALLBACK);
	u8 *a = kmalloc(8240, GFP_KERNEL), *b = kmalloc(8240, GFP_KERNEL);
	u8 *c = kmalloc(8240, GFP_KERNEL), key[16], iv[16] = {};
	unsigned int i;
	int err = -ENOMEM;

	if (IS_ERR(t) || IS_ERR(ref) || !a || !b || !c)
		goto out;
	memset(key, job->index + 1, sizeof(key));
	iv[0] = ccm ? 3 : 0;
	iv[1] = job->index;
	err = crypto_aead_setkey(t, key, 16) ?: crypto_aead_setkey(ref, key, 16) ?:
		crypto_aead_setauthsize(t, 16) ?: crypto_aead_setauthsize(ref, 16);
	if (err)
		goto out;
	memset(a, 0x30 + job->index, 8224);
	memcpy(c, a, 8224);
	err = run(ref, true, c, c, 32, 8192, iv, true, false);
	if (err)
		goto out;
	for (i = 0; i < 64; i++) {
		memcpy(b, a, 32);
		err = run(t, true, a, b, 32, 8192, iv, true, i & 1);
		if (err || memcmp(b, c, 8240)) { err = -EINVAL; break; }
		err = run(t, false, b, b, 32, 8192, iv, true, !(i & 1));
		if (err || memcmp(b, a, 8224)) { err = -EINVAL; break; }
		cond_resched();
	}
out:
	if (!IS_ERR(t)) crypto_free_aead(t);
	if (!IS_ERR(ref)) crypto_free_aead(ref);
	kfree_sensitive(a); kfree_sensitive(b); kfree_sensitive(c);
	job->err = err;
}

static int parallel_check(void)
{
	struct workqueue_struct *wq = alloc_workqueue("sg2-aead-test", WQ_UNBOUND, 4);
	struct parallel_job jobs[4];
	unsigned int i;
	int err = 0;

	if (!wq)
		return -ENOMEM;
	for (i = 0; i < ARRAY_SIZE(jobs); i++) {
		jobs[i].index = i;
		INIT_WORK(&jobs[i].work, parallel_worker);
		queue_work(wq, &jobs[i].work);
	}
	flush_workqueue(wq);
	for (i = 0; i < ARRAY_SIZE(jobs); i++)
		if (jobs[i].err) err = jobs[i].err;
	destroy_workqueue(wq);
	if (!err)
		pr_info("ccm_test: PASS four concurrent CCM/GCM streams, sync and queued\n");
	return err;
}

static int check(void)
{
	struct crypto_aead *t = crypto_alloc_aead("nanokvm-ccm-aes", 0, 0);
	struct crypto_aead *ref = crypto_alloc_aead("ccm(aes)", 0,
					 CRYPTO_ALG_ASYNC | CRYPTO_ALG_NEED_FALLBACK);
	u8 *a = kmalloc(MAXLEN, GFP_KERNEL), *b = kmalloc(MAXLEN, GFP_KERNEL);
	u8 *c = kmalloc(MAXLEN, GFP_KERNEL), key[32], iv[16];
	unsigned int i;
	int err = -ENODEV;
	if (IS_ERR(t) || IS_ERR(ref) || !a || !b || !c)
		goto out;
	pr_info("ccm_test: %s vs %s\n", crypto_aead_driver_name(t), crypto_aead_driver_name(ref));
	err = known_vector(t) ?: invalid(t);
	if (err) {
		pr_err("ccm_test: known-vector/invalid-request failure %d\n", err);
		goto out;
	}
	for (i = 0; i < cases; i++) {
		static const unsigned int aads[] = {0,1,14,15,16,17,4074,4078,4095,4096,65279,65280,65536};
		static const unsigned int lens[] = {0,1,15,16,17,4079,4080,4081,4095,4096,4097,8191,8192,8193,65535};
		unsigned int aad = i < ARRAY_SIZE(aads)*ARRAY_SIZE(lens) ?
			aads[i % ARRAY_SIZE(aads)] : get_random_u32_below(65537);
		unsigned int len = i < ARRAY_SIZE(aads)*ARRAY_SIZE(lens) ?
			lens[i / ARRAY_SIZE(aads)] : get_random_u32_below(65536);
		unsigned int tag = 4 + 2*(i % 7), kl = 16 + 8*(i % 3), total = aad + len, j;
		bool inplace = i & 1, queued = i & 2, split = i % 3;
		get_random_bytes(key, sizeof(key));
		get_random_bytes(iv, sizeof(iv));
		iv[0] = 1 + ((i / 7) % 7);
		get_random_bytes(a, total);
		err = crypto_aead_setkey(t, key, kl) ?: crypto_aead_setkey(ref, key, kl) ?:
			crypto_aead_setauthsize(t, tag) ?: crypto_aead_setauthsize(ref, tag);
		if (err)
			goto mismatch;
		memcpy(c, a, total);
		err = run(ref, true, c, c, aad, len, iv, false, false);
		if (err)
			goto mismatch;
		memset(b, 0xa5, total + tag);
		memcpy(b, a, inplace ? total : aad);
		err = run(t, true, inplace ? b : a, b, aad, len, iv, split, queued);
		if (err || memcmp(b, c, total + tag))
			goto mismatch;
		err = run(ref, false, b, b, aad, len, iv, false, false);
		if (err || memcmp(b, a, total))
			goto mismatch;
		memcpy(b, c, total + tag);
		err = run(t, false, inplace ? b : c, b, aad, len, iv, split, queued);
		if (err || memcmp(b, a, total))
			goto mismatch;
		/* Independently corrupt each available region, plus the nonce. */
		for (j = 0; j < 4; j++) {
			unsigned int off;
			if ((j == 0 && !aad) || (j == 1 && !len))
				continue;
			memcpy(b, c, total + tag);
			off = j == 0 ? aad/2 : j == 1 ? aad + len/2 : total + tag/2;
			if (j == 3)
				iv[1] ^= 1;
			else
				b[off] ^= 1;
			err = run(t, false, b, b, aad, len, iv, split, queued);
			if (j == 3)
				iv[1] ^= 1;
			if (err != -EBADMSG)
				goto mismatch;
		}
		err = 0;
		cond_resched();
		continue;
mismatch:
		pr_err("ccm_test: case %u key=%u aad=%u len=%u tag=%u L=%u inplace=%d queued=%d split=%d err=%d\n",
			i,kl,aad,len,tag,iv[0]+1,inplace,queued,split,err);
		err = -EINVAL;
		goto out;
	}
	pr_info("ccm_test: PASS RFC3610, invalid requests, %u differential/tamper cases\n", cases);
out:
	if (!IS_ERR(t))
		crypto_free_aead(t);
	if (!IS_ERR(ref))
		crypto_free_aead(ref);
	kfree_sensitive(a);
	kfree_sensitive(b);
	kfree_sensitive(c);
	return err;
}

static int rate(const char *name, bool enc, unsigned int len)
{
	struct crypto_aead *tfm = crypto_alloc_aead(name, 0, 0);
	u8 key[16] = {7}, iv[16] = {}, *buf = NULL, *out = NULL;
	u64 start, end, bytes = 0;
	int err = -ENOMEM;
	if (IS_ERR(tfm))
		return PTR_ERR(tfm);
	buf = kmalloc(len + 48, GFP_KERNEL);
	out = kmalloc(len + 48, GFP_KERNEL);
	if (!buf || !out)
		goto out;
	iv[0] = strstr(name, "ccm") ? 3 : 0; /* CCM's 11-byte SMB nonce. */
	err = crypto_aead_setkey(tfm, key, 16) ?: crypto_aead_setauthsize(tfm, 16);
	if (err)
		goto out;
	memset(buf, 0x5a, len + 48);
	if (!enc) {
		err = run(tfm, true, buf, buf, 32, len, iv, false, false);
		if (err)
			goto out;
	}
	start = ktime_get_ns();
	do {
		err = run(tfm, enc, buf, out, 32, len, iv, false, false);
		if (err)
			goto out;
		bytes += len;
		cond_resched();
		end = ktime_get_ns();
	} while (end - start < (u64)bench_ms * NSEC_PER_MSEC);
	pr_info("ccm_test: bench driver=%s op=%s len=%u bytes=%llu ns=%llu\n",
		name, enc ? "encrypt" : "decrypt", len, bytes, end - start);
out:
	kfree_sensitive(buf);
	kfree_sensitive(out);
	crypto_free_aead(tfm);
	return err;
}

static int __init ccm_test_init(void)
{
	static const unsigned int sizes[] = {64,512,1420,4096,16384,65536};
	unsigned int i;
	int err = check();
	if (!err)
		err = parallel_check();
	if (err || !bench_ms)
		return err;
	for (i = 0; i < ARRAY_SIZE(sizes); i++) {
		err = rate("nanokvm-ccm-aes", true, sizes[i]) ?:
			rate("nanokvm-gcm-aes", true, sizes[i]) ?:
			rate("nanokvm-ccm-aes", false, sizes[i]) ?:
			rate("nanokvm-gcm-aes", false, sizes[i]);
		if (err)
			return err;
	}
	return 0;
}
static void __exit ccm_test_exit(void) {}
module_init(ccm_test_init);
module_exit(ccm_test_exit);
MODULE_DESCRIPTION("SG2002 hardware CCM qualification and GCM comparison");
MODULE_LICENSE("GPL");
