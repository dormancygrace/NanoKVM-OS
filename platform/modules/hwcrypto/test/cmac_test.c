// SPDX-License-Identifier: GPL-2.0-only
/* RFC4493 and streaming/async CMAC qualification against the AES library. */
#include <crypto/aes-cbc-macs.h>
#include <crypto/hash.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/random.h>
#include <linux/scatterlist.h>
#include <linux/slab.h>
#include <linux/workqueue.h>

static unsigned int cases = 2000;
module_param(cases, uint, 0444);
static unsigned int bench_ms = 500;
module_param(bench_ms, uint, 0444);
static bool crossover;
module_param(crossover, bool, 0444);
/* Only for a separately built fault-injection driver: poison its second CBC
 * completion after returning DMA ownership. Never enable on a normal driver. */
static bool expect_failure;
module_param(expect_failure, bool, 0444);

enum { QUEUED = 1, VIRTUAL = 2, SPLIT = 4, STREAM = 8, FINUP = 16 };

static void source(struct ahash_request *req, struct scatterlist sg[4],
		   u8 *buf, u32 len, u8 out[16], unsigned int mode)
{
	if (mode & VIRTUAL) {
		ahash_request_set_virt(req, buf, out, len);
		return;
	}
	if ((mode & SPLIT) && len >= 3) {
		unsigned int n = min(len - 2, 4093U);
		sg_init_table(sg, 3);
		sg_set_buf(sg, buf, 1);
		sg_set_buf(sg + 1, buf + 1, n);
		sg_init_one(sg + 3, buf + n + 1, len - n - 1);
		sg_chain(sg, 3, sg + 3);
	} else {
		sg_init_one(sg, buf, len);
	}
	ahash_request_set_crypt(req, sg, out, len);
}

static int run(struct crypto_ahash *tfm, u8 *buf, u32 len, u8 out[16], unsigned int mode)
{
	struct ahash_request *req = ahash_request_alloc(tfm, GFP_KERNEL), *next;
	struct scatterlist sg[4];
	DECLARE_CRYPTO_WAIT(wait);
	u8 *state = NULL;
	u32 pos = 0, step = 0;
	u32 flags = CRYPTO_TFM_REQ_MAY_BACKLOG | ((mode & QUEUED) ? 0 : CRYPTO_TFM_REQ_MAY_SLEEP);
	int err = -ENOMEM;
	bool imported = false, finished = false;

	if (!req)
		return -ENOMEM;
	ahash_request_set_callback(req, flags, crypto_req_done, &wait);
	source(req, sg, buf, len, out, mode);
	if (!(mode & STREAM)) {
		err = crypto_wait_req(crypto_ahash_digest(req), &wait);
		goto out;
	}
	state = kmalloc(crypto_ahash_statesize(tfm), GFP_KERNEL);
	if (!state)
		goto out;
	err = crypto_wait_req(crypto_ahash_init(req), &wait);
	if (err)
		goto out;
	/* A zero-byte update must not flush a pending full last block. */
	source(req, sg, buf, 0, out, mode);
	err = crypto_wait_req(crypto_ahash_update(req), &wait);
	if (err)
		goto out;
	while (pos < len) {
		static const unsigned int sizes[] = {1,15,16,17,4095,4096,4097,16384};
		u32 n = min(len - pos, sizes[step++ % ARRAY_SIZE(sizes)]);
		source(req, sg, buf + pos, n, out, mode);
		finished = (mode & FINUP) && pos + n == len;
		err = crypto_wait_req(finished ? crypto_ahash_finup(req) : crypto_ahash_update(req), &wait);
		if (err)
			goto out;
		pos += n;
		if (!imported && !finished && pos >= len/2) {
			err = crypto_ahash_export(req, state);
			if (err)
				goto out;
			next = ahash_request_alloc(tfm, GFP_KERNEL);
			if (!next) { err = -ENOMEM; goto out; }
			ahash_request_free(req);
			req = next;
			ahash_request_set_callback(req, flags, crypto_req_done, &wait);
			/* A byte-count change inconsistent with the held tail is invalid. */
			state[0] ^= 1;
			err = crypto_ahash_import(req, state);
			state[0] ^= 1;
			if (err != -EINVAL) { err = -EINVAL; goto out; }
			err = crypto_ahash_import(req, state);
			if (err)
				goto out;
			imported = true;
		}
	}
	if (!finished) {
		/* final() must ignore a stale nonzero input length. */
		source(req, sg, buf, min(len, 13U), out, mode);
		err = crypto_wait_req(crypto_ahash_final(req), &wait);
	}
out:
	kfree_sensitive(state);
	ahash_request_free(req);
	return err;
}

static int vectors(struct crypto_ahash *tfm)
{
	static const u8 key[16] = "\x2b\x7e\x15\x16\x28\xae\xd2\xa6\xab\xf7\x15\x88\x09\xcf\x4f\x3c";
	static const u8 input[64] =
		"\x6b\xc1\xbe\xe2\x2e\x40\x9f\x96\xe9\x3d\x7e\x11\x73\x93\x17\x2a"
		"\xae\x2d\x8a\x57\x1e\x03\xac\x9c\x9e\xb7\x6f\xac\x45\xaf\x8e\x51"
		"\x30\xc8\x1c\x46\xa3\x5c\xe4\x11\xe5\xfb\xc1\x19\x1a\x0a\x52\xef"
		"\xf6\x9f\x24\x45\xdf\x4f\x9b\x17\xad\x2b\x41\x7b\xe6\x6c\x37\x10";
	static const u8 expected[4][16] = {
		"\xbb\x1d\x69\x29\xe9\x59\x37\x28\x7f\xa3\x7d\x12\x9b\x75\x67\x46",
		"\x07\x0a\x16\xb4\x6b\x4d\x41\x44\xf7\x9b\xdd\x9d\xd0\x4a\x28\x7c",
		"\xdf\xa6\x67\x47\xde\x9a\xe6\x30\x30\xca\x32\x61\x14\x97\xc8\x27",
		"\x51\xf0\xbe\xbf\x7e\x3b\x9d\x92\xfc\x49\x74\x17\x79\x36\x3c\xfe",
	};
	static const unsigned int lens[] = {0,16,40,64};
	u8 *buf = kmemdup(input, 64, GFP_KERNEL), tag[16];
	unsigned int i, mode;
	int err = -ENOMEM;
	if (!buf)
		return err;
	err = crypto_ahash_setkey(tfm, key, 16);
	if (err)
		goto out;
	for (i = 0; i < 4; i++)
		for (mode = 0; mode < 32; mode++) {
			err = run(tfm, buf, lens[i], tag, mode);
			if (err || memcmp(tag, expected[i], 16)) {
				pr_err("cmac_test: vector=%u mode=%u err=%d got=%*phN expected=%*phN\n",
					i, mode, err, 16, tag, 16, expected[i]);
				err = -EINVAL;
				goto out;
			}
		}
out:
	kfree_sensitive(buf);
	return err;
}

static int check(void)
{
	struct crypto_ahash *tfm = crypto_alloc_ahash("nanokvm-cmac-aes", 0, 0);
	struct aes_cmac_key ref __cleanup(aes_cmac_zeroize_key);
	u8 *buf = kmalloc(65536, GFP_KERNEL), key[32], expected[16], tag[16];
	unsigned int i;
	int err = -ENOMEM;
	if (IS_ERR(tfm)) { err = PTR_ERR(tfm); goto out; }
	if (!buf)
		goto out;
	err = vectors(tfm);
	if (err) { pr_err("cmac_test: RFC4493/split/export failure %d\n", err); goto out; }
	for (i = 0; i < 34; i++)
		if (i != 16 && i != 24 && i != 32 && crypto_ahash_setkey(tfm, key, i) != -EINVAL) {
			err = -EINVAL;
			goto out;
		}
	for (i = 0; i < cases; i++) {
		static const unsigned int lens[] = {0,1,15,16,17,31,32,33,4095,4096,4097,8191,8192,8193,65535,65536};
		unsigned int len = i < 512 ? lens[i % ARRAY_SIZE(lens)] : get_random_u32_below(65537);
		unsigned int kl = 16 + 8*(i % 3), mode = i % 32;
		get_random_bytes(key, sizeof(key));
		get_random_bytes(buf, len);
		err = aes_cmac_preparekey(&ref, key, kl) ?: crypto_ahash_setkey(tfm, key, kl);
		if (err)
			goto out;
		aes_cmac(&ref, buf, len, expected);
		err = run(tfm, buf, len, tag, mode);
		if (err || memcmp(tag, expected, 16)) {
			pr_err("cmac_test: mismatch case=%u key=%u len=%u mode=%u err=%d\n", i,kl,len,mode,err);
			err = -EINVAL;
			goto out;
		}
		cond_resched();
	}
	pr_info("cmac_test: PASS RFC4493 (32 modes), invalid keys, %u differential cases\n", cases);
out:
	kfree_sensitive(buf);
	memzero_explicit(key, sizeof(key));
	if (!IS_ERR(tfm))
		crypto_free_ahash(tfm);
	return err;
}

struct parallel_job { struct work_struct work; unsigned int index; int err; };
static void parallel_worker(struct work_struct *work)
{
	struct parallel_job *job = container_of(work, struct parallel_job, work);
	struct crypto_ahash *tfm = crypto_alloc_ahash("nanokvm-cmac-aes", 0, 0);
	struct aes_cmac_key ref __cleanup(aes_cmac_zeroize_key);
	u8 key[32], expected[16], tag[16], *buf = kmalloc(8193, GFP_KERNEL);
	unsigned int i;
	int err = -ENOMEM;
	if (IS_ERR(tfm) || !buf)
		goto out;
	memset(key, job->index + 1, sizeof(key));
	memset(buf, job->index + 17, 8193);
	err = aes_cmac_preparekey(&ref, key, 32) ?: crypto_ahash_setkey(tfm, key, 32);
	if (err)
		goto out;
	aes_cmac(&ref, buf, 8193, expected);
	for (i = 0; i < 64; i++) {
		err = run(tfm, buf, 8193, tag, i % 32);
		if (err || memcmp(tag, expected, 16)) { err = -EINVAL; break; }
		cond_resched();
	}
out:
	if (!IS_ERR(tfm)) crypto_free_ahash(tfm);
	kfree_sensitive(buf);
	memzero_explicit(key, sizeof(key));
	job->err = err;
}

static int concurrent(void)
{
	struct workqueue_struct *wq = alloc_workqueue("sg2-cmac-test", WQ_UNBOUND, 4);
	struct parallel_job jobs[4];
	unsigned int i;
	int err = 0;
	if (!wq)
		return -ENOMEM;
	for (i = 0; i < 4; i++) {
		jobs[i].index = i;
		INIT_WORK(&jobs[i].work, parallel_worker);
		queue_work(wq, &jobs[i].work);
	}
	flush_workqueue(wq);
	for (i = 0; i < 4; i++)
		if (jobs[i].err) err = jobs[i].err;
	destroy_workqueue(wq);
	if (!err)
		pr_info("cmac_test: PASS four concurrent CMAC streams\n");
	return err;
}

static int rate(const char *driver, u32 len, bool fresh_key)
{
	struct crypto_ahash *tfm = NULL;
	u8 key[16] = {1}, tag[16], *buf = kmalloc(len, GFP_KERNEL);
	u64 start, end, bytes = 0;
	int err = -ENOMEM;
	if (!buf)
		goto out;
	memset(buf, 0x5a, len);
	if (!fresh_key) {
		tfm = crypto_alloc_ahash(driver, 0, 0);
		if (IS_ERR(tfm)) { err = PTR_ERR(tfm); goto out; }
		err = crypto_ahash_setkey(tfm, key, 16);
		if (err)
			goto out;
	}
	start = ktime_get_ns();
	do {
		/* CIFS currently owns a new transform/key for each signature. Include
		 * its setup and teardown as well as the MAC in this second timing. */
		if (fresh_key) {
			tfm = crypto_alloc_ahash(driver, 0, 0);
			if (IS_ERR(tfm)) { err = PTR_ERR(tfm); goto out; }
			err = crypto_ahash_setkey(tfm, key, 16);
			if (err)
				goto out;
		}
		err = run(tfm, buf, len, tag, 0);
		if (err)
			goto out;
		if (fresh_key) {
			crypto_free_ahash(tfm);
			tfm = NULL;
		}
		bytes += len;
		cond_resched();
		end = ktime_get_ns();
	} while (end - start < (u64)bench_ms * NSEC_PER_MSEC);
	pr_info("cmac_test: bench driver=%s len=%u bytes=%llu ns=%llu keys=%s\n",
		driver,len,bytes,end-start,fresh_key ? "fresh" : "reused");
out:
	if (!IS_ERR_OR_NULL(tfm)) crypto_free_ahash(tfm);
	kfree_sensitive(buf);
	return err;
}

static int failure(void)
{
	struct crypto_ahash *tfm = crypto_alloc_ahash("nanokvm-cmac-aes", 0, 0);
	struct ahash_request *survivor = NULL, *failed = NULL;
	struct aes_cmac_key ref __cleanup(aes_cmac_zeroize_key);
	struct scatterlist sg[4];
	DECLARE_CRYPTO_WAIT(wait);
	u8 key[16] = {1}, expected[16], tag[16], *buf = kmalloc(64, GFP_KERNEL);
	u8 *state = NULL;
	int err = -ENOMEM;

	if (IS_ERR(tfm)) { err = PTR_ERR(tfm); goto out; }
	if (!buf)
		goto out;
	memset(buf, 0x5a, 64);
	state = kmalloc(crypto_ahash_statesize(tfm), GFP_KERNEL);
	survivor = ahash_request_alloc(tfm, GFP_KERNEL);
	failed = ahash_request_alloc(tfm, GFP_KERNEL);
	if (!state || !survivor || !failed)
		goto out;
	err = aes_cmac_preparekey(&ref, key, 16) ?: crypto_ahash_setkey(tfm, key, 16);
	if (err)
		goto out;
	aes_cmac(&ref, buf, 64, expected);
	ahash_request_set_callback(survivor, CRYPTO_TFM_REQ_MAY_SLEEP, crypto_req_done, &wait);
	ahash_request_set_callback(failed, CRYPTO_TFM_REQ_MAY_SLEEP, crypto_req_done, &wait);
	err = crypto_ahash_init(survivor) ?: crypto_ahash_init(failed);
	if (err)
		goto out;
	/* Leave one request with a hardware-generated CBC chain. */
	source(survivor, sg, buf, 32, tag, VIRTUAL);
	err = crypto_wait_req(crypto_ahash_update(survivor), &wait);
	if (err)
		goto out;
	/* The second CBC operation trips the test-only latch. Its request must
	 * fail and must not become exportable or emit an authentication tag. */
	memset(tag, 0xa5, 16);
	source(failed, sg, buf, 32, tag, VIRTUAL);
	if (crypto_wait_req(crypto_ahash_update(failed), &wait) != -EIO ||
	    crypto_wait_req(crypto_ahash_final(failed), &wait) != -EINVAL ||
	    crypto_ahash_export(failed, state) != -EINVAL ||
	    memchr_inv(tag, 0xa5, 16)) {
		err = -EINVAL;
		goto out;
	}
	/* An unfailed request can finish from its saved chain without further DMA. */
	source(survivor, sg, buf + 32, 32, tag, VIRTUAL);
	err = crypto_wait_req(crypto_ahash_finup(survivor), &wait);
	if (err || memcmp(tag, expected, 16)) { err = -EINVAL; goto out; }
	err = run(tfm, buf, 64, tag, QUEUED | SPLIT);
	if (err || memcmp(tag, expected, 16)) { err = -EINVAL; goto out; }
	pr_info("cmac_test: PASS failed request rejected, saved-chain and new-request fallback\n");
out:
	ahash_request_free(survivor);
	ahash_request_free(failed);
	kfree_sensitive(state);
	kfree_sensitive(buf);
	if (!IS_ERR(tfm))
		crypto_free_ahash(tfm);
	return err;
}

static int __init cmac_test_init(void)
{
	static const u32 sizes[] = {64,512,1420,4096,16384,65536};
	unsigned int i;
	int err = (expect_failure ? failure() : 0) ?: check() ?: concurrent();
	if (err || !bench_ms)
		return err;
	if (crossover) {
		/* Resolve the DMA crossover to one AES block, then verify the trend
		 * through typical header sizes. Both paths allocate a request per MAC. */
		for (i = 16; i <= 1024; i += i < 384 ? 16 : i < 512 ? 128 : 256) {
			err = rate("nanokvm-cmac-aes", i, false) ?: rate("cmac-aes-lib", i, false) ?:
				rate("cmac-aes-lib", i, true) ?: rate("nanokvm-cmac-aes", i, true);
			if (err)
				return err;
		}
		return 0;
	}
	for (i = 0; i < ARRAY_SIZE(sizes); i++) {
		err = rate("nanokvm-cmac-aes", sizes[i], false) ?: rate("cmac-aes-lib", sizes[i], false);
		if (err)
			return err;
	}
	return 0;
}
static void __exit cmac_test_exit(void) {}
module_init(cmac_test_init);
module_exit(cmac_test_exit);
MODULE_DESCRIPTION("SG2002 CMAC qualification and software comparison");
MODULE_LICENSE("GPL");
