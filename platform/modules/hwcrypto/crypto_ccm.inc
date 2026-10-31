/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * CCM with both CBC-MAC and CTR on CryptoDMA. The generic CCM template
 * requires a synchronous MAC, so registering an asynchronous cbcmac(aes)
 * would not accelerate it. Keep the complete AEAD here instead.
 *
 * B0 and the encoded/padded AAD are authenticated first. For each payload
 * chunk, encrypt authenticates plaintext before CTR; decrypt does CTR before
 * authenticating plaintext. The CBC chaining value survives chunk boundaries.
 * The first CTR chunk also generates S0 for masking the tag. No message-sized
 * allocation, and no CPU AES: the engine lock covers at most two 4 KiB DMA
 * operations before yielding to other users (including SRTP).
 */
#include <crypto/internal/aead.h>
#include <crypto/scatterwalk.h>
#include <crypto/utils.h>
#include <linux/overflow.h>

struct sg2_ccm_ctx {
	u8 key[32];
	u32 keylen;
	struct crypto_aead *fallback;
};

/* The fallback's variable-sized request context follows this last member. */
struct sg2_ccm_req {
	struct work_struct work;
	struct aead_request *req;
	bool encrypt;
	struct aead_request fallback_req;
};

static int sg2_ccm_fallback(struct aead_request *req, bool encrypt)
{
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(crypto_aead_reqtfm(req));
	struct sg2_ccm_req *r = aead_request_ctx(req);
	struct aead_request *sub = &r->fallback_req;

	aead_request_set_tfm(sub, ctx->fallback);
	aead_request_set_callback(sub, req->base.flags, NULL, NULL);
	aead_request_set_crypt(sub, req->src, req->dst, req->cryptlen, req->iv);
	aead_request_set_ad(sub, req->assoclen);
	return encrypt ? crypto_aead_encrypt(sub) : crypto_aead_decrypt(sub);
}

/* buffer contains whole, padded blocks; caller holds lock. */
static int sg2_ccm_mac_locked(struct hwcrypto_request *r, u8 mac[16])
{
	int err;

	r->mode = HWCRYPTO_MODE_CBC;
	r->output_length = r->length;
	memcpy(r->iv, mac, 16);
	err = crypto_run_locked(r);
	if (!err)
		memcpy(mac, crypto_output + r->length - 16, 16);
	return err;
}

/* NIST SP 800-38C A.2: B0 || encoded AAD || zero padding. */
static int sg2_ccm_auth_start(struct aead_request *req,
			      struct hwcrypto_request *r, u8 mac[16],
			      unsigned int cryptlen, int in_nents)
{
	unsigned int l = req->iv[0] + 1, pos = 0, head = 16;
	u8 prefix[22] = {};
	int err;

	memcpy(prefix, req->iv, 16 - l);
	prefix[0] |= ((crypto_aead_authsize(crypto_aead_reqtfm(req)) - 2) / 2) << 3;
	/* cryptlen is u32; bytes above it in an L > 4 field stay zero. */
	if (l >= 4)
		put_unaligned_be32(cryptlen, prefix + 12);
	else if (l == 3) {
		prefix[13] = cryptlen >> 16;
		put_unaligned_be16(cryptlen, prefix + 14);
	} else
		put_unaligned_be16(cryptlen, prefix + 14);
	if (req->assoclen) {
		prefix[0] |= 0x40;
		if (req->assoclen < 0xff00) {
			put_unaligned_be16(req->assoclen, prefix + 16);
			head += 2;
		} else {
			put_unaligned_be16(0xfffe, prefix + 16);
			put_unaligned_be32(req->assoclen, prefix + 18);
			head += 6;
		}
	}
	do {
		unsigned int n = min_t(unsigned int, HWCRYPTO_MAX - head,
					 req->assoclen - pos);

		r->length = ALIGN(head + n, 16);
		mutex_lock(&lock);
		if (poisoned) {
			err = -EIO;
			goto unlock;
		}
		memcpy(buffer, prefix, head);
		if (n && sg_pcopy_to_buffer(req->src, in_nents, buffer + head, n, pos) != n) {
			err = -EINVAL;
			goto clean;
		}
		memset(buffer + head + n, 0, r->length - head - n);
		err = sg2_ccm_mac_locked(r, mac);
clean:
		crypto_clean_locked(r);
unlock:
		mutex_unlock(&lock);
		if (err)
			break;
		pos += n;
		head = 0;
		cond_resched();
	} while (pos < req->assoclen);
	memzero_explicit(prefix, sizeof(prefix));
	return err;
}

static int sg2_ccm_process(struct aead_request *req, bool encrypt)
{
	struct crypto_aead *tfm = crypto_aead_reqtfm(req);
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(tfm);
	unsigned int authsize = crypto_aead_authsize(tfm);
	unsigned int cryptlen = req->cryptlen - (encrypt ? 0 : authsize);
	unsigned int pos = 0, l = req->iv[0] + 1;
	struct hwcrypto_request r = {
		.version = 1, .algorithm = HWCRYPTO_ALG_AES,
		.encrypt = 1, .key_length = ctx->keylen,
	};
	u8 ctr[16] = {}, mac[16] = {}, s0[16], expected[16];
	bool first = true;
	int err, in_nents, out_nents;

	/* Length sums and nonce capacity were checked before queueing. */
	in_nents = sg_nents_for_len(req->src, req->assoclen + req->cryptlen);
	out_nents = sg_nents_for_len(req->dst,
			req->assoclen + cryptlen + (encrypt ? authsize : 0));
	if (in_nents < 0 || out_nents < 0)
		return -EINVAL;
	memcpy(r.key, ctx->key, ctx->keylen);
	memcpy(ctr, req->iv, 16 - l); /* A0: ignore unused IV tail bytes. */
	if (!encrypt)
		scatterwalk_map_and_copy(expected, req->src,
					req->assoclen + cryptlen, authsize, 0);
	err = sg2_ccm_auth_start(req, &r, mac, cryptlen, in_nents);
	if (err)
		goto out;
	do {
		unsigned int lead = first ? 16 : 0;
		unsigned int n = min_t(unsigned int, HWCRYPTO_MAX - lead,
					 cryptlen - pos);

		mutex_lock(&lock);
		if (poisoned) {
			err = -EIO;
			goto unlock;
		}
		/* Encryption first MACs plaintext in buffer[0..n). */
		r.length = lead + n;
		if (n && sg_pcopy_to_buffer(req->src, in_nents,
				buffer + (encrypt ? 0 : lead), n,
				req->assoclen + pos) != n) {
			err = -EINVAL;
			goto clean;
		}
		if (encrypt && n) {
			r.length = ALIGN(n, 16);
			memset(buffer + n, 0, r.length - n);
			err = sg2_ccm_mac_locked(&r, mac);
			if (err)
				goto clean;
			if (lead)
				memmove(buffer + lead, buffer, n);
		}
		memset(buffer, 0, lead);
		r.mode = HWCRYPTO_MODE_CTR;
		r.length = lead + n;
		r.output_length = r.length;
		memcpy(r.iv, ctr, 16);
		err = crypto_run_locked(&r);
		if (err)
			goto clean;
		if (first)
			memcpy(s0, crypto_output, 16);
		if (n && sg_pcopy_from_buffer(req->dst, out_nents,
				crypto_output + lead, n, req->assoclen + pos) != n) {
			err = -EINVAL;
			goto clean;
		}
		sg2_inc_counter(ctr + 16 - l, l, DIV_ROUND_UP(r.length, 16));
		if (!encrypt && n) {
			memcpy(buffer, crypto_output + lead, n);
			r.length = ALIGN(n, 16);
			memset(buffer + n, 0, r.length - n);
			err = sg2_ccm_mac_locked(&r, mac);
		}
clean:
		/* CTR's first chunk is larger than its CBC chunk; clear both. The
		 * poison latch makes this a no-op if DMA ownership is uncertain. */
		r.length = lead + n;
		crypto_clean_locked(&r);
unlock:
		mutex_unlock(&lock);
		if (err)
			goto out;
		pos += n;
		first = false;
		cond_resched();
	} while (pos < cryptlen);
	crypto_xor(mac, s0, 16);
	if (encrypt)
		scatterwalk_map_and_copy(mac, req->dst, req->assoclen + cryptlen, authsize, 1);
	else if (crypto_memneq(mac, expected, authsize))
		err = -EBADMSG;
out:
	memzero_explicit(&r, sizeof(r));
	memzero_explicit(ctr, sizeof(ctr));
	memzero_explicit(mac, sizeof(mac));
	memzero_explicit(s0, sizeof(s0));
	memzero_explicit(expected, sizeof(expected));
	return err;
}

static void sg2_ccm_work(struct work_struct *work)
{
	struct sg2_ccm_req *r = container_of(work, struct sg2_ccm_req, work);
	struct aead_request *req = r->req;
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(crypto_aead_reqtfm(req));
	int err = sg2_use_fallback(ctx->fallback) ? sg2_ccm_fallback(req, r->encrypt) :
		sg2_ccm_process(req, r->encrypt);

	sg2_async_leave();
	aead_request_complete(req, err);
}

static int sg2_ccm_submit(struct aead_request *req, bool encrypt)
{
	struct crypto_aead *tfm = crypto_aead_reqtfm(req);
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(tfm);
	struct sg2_ccm_req *r = aead_request_ctx(req);
	unsigned int authsize = crypto_aead_authsize(tfm), cryptlen, total;
	int err;

	if (!ctx->keylen)
		return -ENOKEY;
	if (req->iv[0] < 1 || req->iv[0] > 7 || (!encrypt && req->cryptlen < authsize))
		return -EINVAL;
	cryptlen = req->cryptlen - (encrypt ? 0 : authsize);
	if (req->iv[0] < 3 && cryptlen >= (1U << (8 * (req->iv[0] + 1))))
		return -EOVERFLOW;
	if (check_add_overflow(req->assoclen, cryptlen, &total) ||
	    check_add_overflow(total, authsize, &total))
		return -EOVERFLOW;
	if (sg2_use_fallback(ctx->fallback))
		return sg2_ccm_fallback(req, encrypt);
	if (req->base.flags & CRYPTO_TFM_REQ_MAY_SLEEP)
		return sg2_ccm_process(req, encrypt);
	err = sg2_async_enter(req->base.flags);
	if (err)
		return err;
	r->req = req;
	r->encrypt = encrypt;
	INIT_WORK(&r->work, sg2_ccm_work);
	queue_work(crypto_api_wq, &r->work);
	return -EINPROGRESS;
}

static int sg2_ccm_encrypt(struct aead_request *req) { return sg2_ccm_submit(req, true); }
static int sg2_ccm_decrypt(struct aead_request *req) { return sg2_ccm_submit(req, false); }

static int sg2_ccm_setkey(struct crypto_aead *tfm, const u8 *key, unsigned int len)
{
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(tfm);
	int err;

	memzero_explicit(ctx->key, sizeof(ctx->key));
	ctx->keylen = 0;
	if (len != 16 && len != 24 && len != 32)
		return -EINVAL;
	if (ctx->fallback) {
		err = crypto_aead_setkey(ctx->fallback, key, len);
		if (err)
			return err;
	}
	memcpy(ctx->key, key, len);
	ctx->keylen = len;
	return 0;
}

static int sg2_ccm_setauthsize(struct crypto_aead *tfm, unsigned int size)
{
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(tfm);

	if (size < 4 || size > 16 || size % 2)
		return -EINVAL;
	return ctx->fallback ? crypto_aead_setauthsize(ctx->fallback, size) : 0;
}

static int sg2_ccm_init(struct crypto_aead *tfm)
{
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(tfm);

	ctx->fallback = crypto_alloc_aead("ccm(aes)", 0,
					 CRYPTO_ALG_ASYNC | CRYPTO_ALG_NEED_FALLBACK);
	if (IS_ERR(ctx->fallback))
		ctx->fallback = NULL;
	crypto_aead_set_reqsize(tfm, sizeof(struct sg2_ccm_req) +
		(ctx->fallback ? crypto_aead_reqsize(ctx->fallback) : 0));
	return 0;
}

static void sg2_ccm_exit(struct crypto_aead *tfm)
{
	struct sg2_ccm_ctx *ctx = crypto_aead_ctx(tfm);

	if (ctx->fallback)
		crypto_free_aead(ctx->fallback);
	memzero_explicit(ctx, sizeof(*ctx));
}

static struct aead_alg sg2_ccm_alg = {
	.setkey = sg2_ccm_setkey, .setauthsize = sg2_ccm_setauthsize,
	.encrypt = sg2_ccm_encrypt, .decrypt = sg2_ccm_decrypt,
	.init = sg2_ccm_init, .exit = sg2_ccm_exit,
	.ivsize = 16, .maxauthsize = 16, .chunksize = 16,
	.base = {
		.cra_name = "ccm(aes)", .cra_driver_name = "sg2002-ccm-aes",
		.cra_priority = 400,
		.cra_flags = CRYPTO_ALG_ASYNC | CRYPTO_ALG_KERN_DRIVER_ONLY | CRYPTO_ALG_NEED_FALLBACK,
		.cra_blocksize = 1, .cra_ctxsize = sizeof(struct sg2_ccm_ctx),
		.cra_module = THIS_MODULE,
	},
};
