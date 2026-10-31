/* SPDX-License-Identifier: GPL-2.0-only */
/* AES-CMAC over the shared CBC engine. Keep the last block until final so
 * RFC4493's K1/K2 distinction survives arbitrary update/scatterlist splits.
 * The software AES key schedule also lets an unfailed request continue after
 * another engine user trips the poison latch, without touching its mappings.
 * A request whose own DMA fails still fails; it is never silently replayed.
 */
#include <crypto/aes.h>

struct sg2_cmac_ctx {
	u8 key[32], subkey[2][16];
	u32 keylen;
	struct aes_enckey software;
};

#define SG2_CMAC_MAGIC 0x5347324d
struct sg2_cmac_state {
	u64 bytes;
	u32 magic, used, failed;
	u8 mac[16], tail[16];
};
struct sg2_cmac_req {
	struct sg2_cmac_state state;
	struct work_struct work;
	struct ahash_request *req;
	unsigned int operation;
};

static bool sg2_cmac_valid(const struct sg2_cmac_state *s)
{
	return s->magic == SG2_CMAC_MAGIC && !s->failed &&
		s->used == (s->bytes ? ((s->bytes - 1) % 16) + 1 : 0);
}

static int sg2_cmac_read(struct ahash_request *req, int nents,
			 unsigned int pos, u8 *dst, unsigned int len)
{
	if (req->base.flags & CRYPTO_AHASH_REQ_VIRT) {
		memcpy(dst, req->svirt + pos, len);
		return 0;
	}
	return sg_pcopy_to_buffer(req->src, nents, dst, len, pos) == len ? 0 : -EINVAL;
}

static int sg2_cmac_blocks(struct ahash_request *req, int nents,
			   unsigned int pos, const u8 *block, unsigned int len)
{
	struct sg2_cmac_ctx *ctx = crypto_ahash_ctx(crypto_ahash_reqtfm(req));
	struct sg2_cmac_state *s = &((struct sg2_cmac_req *)ahash_request_ctx(req))->state;
	struct hwcrypto_request r = {
		.version = 1, .algorithm = HWCRYPTO_ALG_AES, .mode = HWCRYPTO_MODE_CBC,
		.encrypt = 1, .key_length = ctx->keylen, .length = len, .output_length = len,
	};
	u8 tmp[16];
	unsigned int off;
	int err = 0;

	mutex_lock(&lock);
	if (poisoned) {
		mutex_unlock(&lock);
		/* CPU-only fallback, including a state already advanced by hardware.
		 * Never read or clean a buffer still owned by uncertain DMA. */
		for (off = 0; off < len; off += 16) {
			if (block)
				memcpy(tmp, block + off, 16);
			else {
				err = sg2_cmac_read(req, nents, pos + off, tmp, 16);
				if (err)
					break;
			}
			crypto_xor(tmp, s->mac, 16);
			aes_encrypt(&ctx->software, s->mac, tmp);
		}
		memzero_explicit(tmp, sizeof(tmp));
		return err;
	}
	memcpy(r.key, ctx->key, ctx->keylen);
	memcpy(r.iv, s->mac, 16);
	if (block)
		memcpy(buffer, block, len);
	else
		err = sg2_cmac_read(req, nents, pos, buffer, len);
	if (!err)
		err = crypto_run_locked(&r);
	if (!err)
		memcpy(s->mac, crypto_output + len - 16, 16);
	crypto_clean_locked(&r);
	mutex_unlock(&lock);
	memzero_explicit(&r, sizeof(r));
	return err;
}

static int sg2_cmac_init(struct ahash_request *req)
{
	struct sg2_cmac_ctx *ctx = crypto_ahash_ctx(crypto_ahash_reqtfm(req));
	struct sg2_cmac_state *s = &((struct sg2_cmac_req *)ahash_request_ctx(req))->state;

	memset(s, 0, sizeof(*s));
	if (!ctx->keylen)
		return -ENOKEY;
	s->magic = SG2_CMAC_MAGIC;
	return 0;
}

/* operation: 0 update, 1 finup, 2 final (ignores stale req->nbytes). */
static int sg2_cmac_process(struct ahash_request *req, unsigned int operation)
{
	struct sg2_cmac_ctx *ctx = crypto_ahash_ctx(crypto_ahash_reqtfm(req));
	struct sg2_cmac_state *s = &((struct sg2_cmac_req *)ahash_request_ctx(req))->state;
	unsigned int remaining = operation == 2 ? 0 : req->nbytes, pos = 0, n;
	u8 last[16] = {};
	int nents = 0, err = 0;

	if (!ctx->keylen || !sg2_cmac_valid(s))
		return -EINVAL;
	if (remaining > U64_MAX - s->bytes)
		return -EOVERFLOW;
	if (remaining) {
		if (req->base.flags & CRYPTO_AHASH_REQ_VIRT) {
			if (!req->svirt)
				return -EINVAL;
		} else {
			nents = sg_nents_for_len(req->src, remaining);
			if (nents < 0)
				return nents;
		}
	}
	s->bytes += remaining;
	while (remaining) {
		if (s->used || remaining <= 16) {
			n = min(remaining, 16 - s->used);
			if (n) {
				err = sg2_cmac_read(req, nents, pos, s->tail + s->used, n);
				if (err)
					goto failed;
			}
			s->used += n;
			pos += n;
			remaining -= n;
			if (!remaining)
				break;
			err = sg2_cmac_blocks(req, nents, 0, s->tail, 16);
			if (err)
				goto failed;
			s->used = 0;
			memzero_explicit(s->tail, sizeof(s->tail));
		} else {
			n = min_t(unsigned int, HWCRYPTO_MAX, (remaining - 1) & ~15U);
			err = sg2_cmac_blocks(req, nents, pos, NULL, n);
			if (err)
				goto failed;
			pos += n;
			remaining -= n;
		}
		cond_resched();
	}
	if (!operation)
		return 0;
	if (!req->result) {
		err = -EINVAL;
		goto failed;
	}
	memcpy(last, s->tail, s->used);
	if (s->used < 16)
		last[s->used] = 0x80;
	crypto_xor(last, ctx->subkey[s->used < 16], 16);
	err = sg2_cmac_blocks(req, nents, 0, last, 16);
	memzero_explicit(last, sizeof(last));
	if (err)
		goto failed;
	memcpy(req->result, s->mac, 16);
	memzero_explicit(s, sizeof(*s));
	return 0;
failed:
	s->failed = 1;
	return err;
}

static void sg2_cmac_work(struct work_struct *work)
{
	struct sg2_cmac_req *r = container_of(work, struct sg2_cmac_req, work);
	struct ahash_request *req = r->req;
	int err = sg2_cmac_process(req, r->operation);

	sg2_async_leave();
	ahash_request_complete(req, err);
}

static int sg2_cmac_submit(struct ahash_request *req, unsigned int operation)
{
	struct sg2_cmac_req *r = ahash_request_ctx(req);
	int err;

	if (req->base.flags & CRYPTO_TFM_REQ_MAY_SLEEP)
		return sg2_cmac_process(req, operation);
	err = sg2_async_enter(req->base.flags);
	if (err)
		return err;
	r->req = req;
	r->operation = operation;
	INIT_WORK(&r->work, sg2_cmac_work);
	queue_work(crypto_api_wq, &r->work);
	return -EINPROGRESS;
}
static int sg2_cmac_update(struct ahash_request *req) { return sg2_cmac_submit(req, 0); }
static int sg2_cmac_finup(struct ahash_request *req) { return sg2_cmac_submit(req, 1); }
static int sg2_cmac_final(struct ahash_request *req) { return sg2_cmac_submit(req, 2); }
static int sg2_cmac_digest(struct ahash_request *req)
{
	int err = sg2_cmac_init(req);
	return err ? err : sg2_cmac_finup(req);
}

static int sg2_cmac_export(struct ahash_request *req, void *out)
{
	struct sg2_cmac_req *r = ahash_request_ctx(req);
	if (!sg2_cmac_valid(&r->state))
		return -EINVAL;
	memcpy(out, &r->state, sizeof(r->state));
	return 0;
}
static int sg2_cmac_import(struct ahash_request *req, const void *in)
{
	struct sg2_cmac_req *r = ahash_request_ctx(req);
	struct sg2_cmac_state s;
	int err = -EINVAL;

	memcpy(&s, in, sizeof(s));
	if (sg2_cmac_valid(&s)) {
		memcpy(&r->state, &s, sizeof(s));
		err = 0;
	}
	memzero_explicit(&s, sizeof(s));
	return err;
}

/* Multiplication by x in GF(2^128), without a secret-dependent branch. */
static void sg2_cmac_double(u8 block[16])
{
	u8 carry = 0;
	int i;
	for (i = 15; i >= 0; i--) {
		u8 next = block[i] >> 7;
		block[i] = (block[i] << 1) | carry;
		carry = next;
	}
	block[15] ^= 0x87 & -carry;
}

static int sg2_cmac_setkey(struct crypto_ahash *tfm, const u8 *key, unsigned int len)
{
	struct sg2_cmac_ctx *ctx = crypto_ahash_ctx(tfm);
	struct hwcrypto_request r = {
		.version = 1, .algorithm = HWCRYPTO_ALG_AES, .mode = HWCRYPTO_MODE_ECB,
		.encrypt = 1, .key_length = len, .length = 16, .output_length = 16,
	};
	u8 zero[16] = {};
	int err;

	memzero_explicit(ctx, sizeof(*ctx));
	err = aes_prepareenckey(&ctx->software, key, len);
	if (err)
		return err;
	memcpy(r.key, key, len);
	mutex_lock(&lock);
	if (poisoned) {
		err = -EIO;
	} else {
		memset(buffer, 0, 16);
		err = crypto_run_locked(&r);
		if (!err)
			memcpy(ctx->subkey[0], crypto_output, 16);
		crypto_clean_locked(&r);
	}
	mutex_unlock(&lock);
	if (err && READ_ONCE(poisoned)) {
		aes_encrypt(&ctx->software, ctx->subkey[0], zero);
		err = 0;
	}
	if (!err) {
		sg2_cmac_double(ctx->subkey[0]);
		memcpy(ctx->subkey[1], ctx->subkey[0], 16);
		sg2_cmac_double(ctx->subkey[1]);
		memcpy(ctx->key, key, len);
		ctx->keylen = len;
	} else {
		memzero_explicit(ctx, sizeof(*ctx));
	}
	memzero_explicit(&r, sizeof(r));
	return err;
}

static void sg2_cmac_exit(struct crypto_ahash *tfm)
{
	memzero_explicit(crypto_ahash_ctx(tfm), sizeof(struct sg2_cmac_ctx));
}

static struct ahash_alg sg2_cmac_alg = {
	.init = sg2_cmac_init, .update = sg2_cmac_update, .final = sg2_cmac_final,
	.finup = sg2_cmac_finup, .digest = sg2_cmac_digest,
	.export = sg2_cmac_export, .import = sg2_cmac_import,
	.setkey = sg2_cmac_setkey, .exit_tfm = sg2_cmac_exit,
	.halg = {
		.digestsize = 16, .statesize = sizeof(struct sg2_cmac_state),
		.base = {
			.cra_name = "cmac(aes)", .cra_driver_name = "nanokvm-cmac-aes",
			.cra_priority = 400,
			.cra_flags = CRYPTO_ALG_ASYNC | CRYPTO_ALG_KERN_DRIVER_ONLY |
				CRYPTO_ALG_REQ_VIRT | CRYPTO_ALG_NO_FALLBACK,
			.cra_blocksize = 16, .cra_ctxsize = sizeof(struct sg2_cmac_ctx),
			.cra_reqsize = sizeof(struct sg2_cmac_req), .cra_module = THIS_MODULE,
		},
	},
};
