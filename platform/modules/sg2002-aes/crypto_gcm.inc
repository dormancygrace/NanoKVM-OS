/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * gcm(aes) with the CryptoDMA engine for AES-CTR and the CPU for GHASH, the two
 * overlapped. The gcm template runs them one after the other: a CTR request
 * that waits for the engine, then GHASH over the whole message. Here each
 * chunk of up to SG2002_CRYPTO_MAX bytes is started on the engine, and while
 * it runs the CPU hashes the associated data (first chunk) and the ciphertext
 * of the previous chunk (encryption) or of the same chunk (decryption, whose
 * ciphertext is the input). E(J0) for the tag comes from the first chunk, as
 * in the template: 16 zero bytes in front of the data, counter J0.
 *
 * The engine owns buffer, crypto_output and desc between crypto_start_locked()
 * and crypto_finish_locked(); the GHASH in between reads only the caller's
 * scatterlists. GHASH is lib/crypto's (the C906 assembly with patch 0041).
 */
#include <crypto/internal/aead.h>
#include <crypto/gcm.h>
#include <crypto/gf128hash.h>
#include <crypto/scatterwalk.h>
#include <crypto/utils.h>

struct sg2_gcm_ctx { u8 key[32]; u32 keylen; struct ghash_key ghash; };
struct sg2_gcm_req { struct work_struct work; struct aead_request *req; bool encrypt; };

/* GHASH over len bytes of sg from offset off. */
static void sg2_ghash_sg(struct ghash_ctx *g, struct scatterlist *sg, unsigned int off, unsigned int len)
{
 struct scatterlist tmp[2];
 struct scatter_walk walk;
 if (!len) return;
 scatterwalk_start(&walk, scatterwalk_ffwd(tmp, sg, off));
 do {
  unsigned int n = scatterwalk_next(&walk, len);
  ghash_update(g, walk.addr, n);
  scatterwalk_done_src(&walk, n);
  len -= n;
 } while (len);
}

static void sg2_ghash_pad(struct ghash_ctx *g, unsigned int len)
{
 static const u8 zeroes[GHASH_BLOCK_SIZE];
 unsigned int pad = -len % GHASH_BLOCK_SIZE;
 if (pad) ghash_update(g, zeroes, pad);
}

static int sg2_gcm_process(struct aead_request *req, bool encrypt)
{
 struct crypto_aead *tfm = crypto_aead_reqtfm(req);
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 unsigned int authsize = crypto_aead_authsize(tfm), assoclen = req->assoclen;
 unsigned int cryptlen = req->cryptlen - (encrypt ? 0 : authsize);
 unsigned int pos = 0, prev_pos = 0, prev_len = 0;
 struct sg2002_crypto_request r = { .version = 1, .algorithm = SG2002_ALG_AES,
  .mode = SG2002_MODE_CTR, .encrypt = 1, .key_length = ctx->keylen };
 __be64 lengths[2] = { cpu_to_be64(8 * (u64)assoclen), cpu_to_be64(8 * (u64)cryptlen) };
 u8 ej0[16], tag[16], expected[16];
 struct ghash_ctx g;
 bool first = true;
 int err = 0, in_nents, out_nents;

 in_nents = sg_nents_for_len(req->src, assoclen + req->cryptlen);
 out_nents = sg_nents_for_len(req->dst, assoclen + cryptlen + (encrypt ? authsize : 0));
 if (in_nents < 0 || out_nents < 0) return -EINVAL;
 memcpy(r.key, ctx->key, ctx->keylen);
 memcpy(r.iv, req->iv, GCM_AES_IV_SIZE);
 put_unaligned_be32(1, r.iv + GCM_AES_IV_SIZE);		/* J0 */
 ghash_init(&g, &ctx->ghash);

 do {
  unsigned int lead = first ? 16 : 0;
  unsigned int n = min_t(unsigned int, SG2002_CRYPTO_MAX - lead, cryptlen - pos);
  struct crypto_sizes s = {};

  r.length = lead + n;
  r.output_length = r.length;
  mutex_lock(&lock);
  if (poisoned) { err = -EIO; goto unlock; }
  memset(buffer, 0, lead);
  if (n && sg_pcopy_to_buffer(req->src, in_nents, buffer + lead, n, assoclen + pos) != n) {
   err = -EINVAL; goto clean;
  }
  err = crypto_start_locked(&r, &s);
  if (err) goto clean;
  /* While the engine runs: hash what is ready without touching its buffers. */
  if (first) {
   sg2_ghash_sg(&g, req->src, 0, assoclen);
   sg2_ghash_pad(&g, assoclen);
  }
  if (encrypt)
   sg2_ghash_sg(&g, req->dst, assoclen + prev_pos, prev_len);
  else
   sg2_ghash_sg(&g, req->src, assoclen + pos, n);
  err = crypto_finish_locked(&r, &s);
  if (err) goto unlock;	/* engine state uncertain: leave its buffers alone */
  if (first) memcpy(ej0, crypto_output, 16);
  if (n && sg_pcopy_from_buffer(req->dst, out_nents, crypto_output + lead, n, assoclen + pos) != n)
   err = -EINVAL;
  sg2_inc_counter(r.iv, 16, DIV_ROUND_UP(r.length, 16));
clean:
  crypto_clean_locked(&r);
unlock:
  mutex_unlock(&lock);
  if (err) goto out;
  prev_pos = pos; prev_len = n; pos += n; first = false;
  cond_resched();
 } while (pos < cryptlen);

 if (encrypt)
  sg2_ghash_sg(&g, req->dst, assoclen + prev_pos, prev_len);
 sg2_ghash_pad(&g, cryptlen);
 ghash_update(&g, (const u8 *)lengths, sizeof(lengths));
 ghash_final(&g, tag);
 crypto_xor(tag, ej0, 16);
 if (encrypt) {
  scatterwalk_map_and_copy(tag, req->dst, assoclen + cryptlen, authsize, 1);
 } else {
  scatterwalk_map_and_copy(expected, req->src, assoclen + cryptlen, authsize, 0);
  if (crypto_memneq(tag, expected, authsize)) err = -EBADMSG;
 }
out:
 memzero_explicit(&r, sizeof(r)); memzero_explicit(&g, sizeof(g));
 memzero_explicit(ej0, sizeof(ej0)); memzero_explicit(tag, sizeof(tag));
 memzero_explicit(expected, sizeof(expected));
 return err;
}

static void sg2_gcm_work(struct work_struct *work)
{
 struct sg2_gcm_req *r = container_of(work, struct sg2_gcm_req, work);
 struct aead_request *req = r->req;
 int err = sg2_gcm_process(req, r->encrypt);
 aead_request_complete(req, err);
}

static int sg2_gcm_submit(struct aead_request *req, bool encrypt)
{
 struct crypto_aead *tfm = crypto_aead_reqtfm(req);
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 struct sg2_gcm_req *r = aead_request_ctx(req);
 if (!ctx->keylen) return -ENOKEY;
 if (!encrypt && req->cryptlen < crypto_aead_authsize(tfm)) return -EINVAL;
 if (req->base.flags & CRYPTO_TFM_REQ_MAY_SLEEP) return sg2_gcm_process(req, encrypt);
 r->req = req; r->encrypt = encrypt; INIT_WORK(&r->work, sg2_gcm_work);
 queue_work(crypto_api_wq, &r->work);
 return -EINPROGRESS;
}
static int sg2_gcm_encrypt(struct aead_request *req) { return sg2_gcm_submit(req, true); }
static int sg2_gcm_decrypt(struct aead_request *req) { return sg2_gcm_submit(req, false); }

static int sg2_gcm_setkey(struct crypto_aead *tfm, const u8 *key, unsigned int len)
{
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 struct sg2002_crypto_request r = { .version = 1, .algorithm = SG2002_ALG_AES,
  .mode = SG2002_MODE_ECB, .encrypt = 1, .key_length = len, .length = 16, .output_length = 16 };
 u8 h[GHASH_BLOCK_SIZE];
 int err;
 memzero_explicit(ctx, sizeof(*ctx));
 if (len != 16 && len != 24 && len != 32) return -EINVAL;
 memcpy(r.key, key, len);
 /* H = AES_K(0^128), on the engine. */
 mutex_lock(&lock);
 if (poisoned) { err = -EIO; goto unlock; }
 memset(buffer, 0, 16);
 err = crypto_run_locked(&r);
 if (err) goto unlock;
 memcpy(h, crypto_output, 16);
 crypto_clean_locked(&r);
unlock:
 mutex_unlock(&lock);
 if (!err) {
  ghash_preparekey(&ctx->ghash, h);
  memcpy(ctx->key, key, len); ctx->keylen = len;
 }
 memzero_explicit(h, sizeof(h)); memzero_explicit(&r, sizeof(r));
 return err;
}
static int sg2_gcm_setauthsize(struct crypto_aead *tfm, unsigned int authsize)
{
 return crypto_gcm_check_authsize(authsize);
}
static int sg2_gcm_init(struct crypto_aead *tfm)
{
 crypto_aead_set_reqsize(tfm, sizeof(struct sg2_gcm_req)); return 0;
}
static void sg2_gcm_exit(struct crypto_aead *tfm) { memzero_explicit(crypto_aead_ctx(tfm), sizeof(struct sg2_gcm_ctx)); }

/* Above the gcm template over sg2002-aes-ctr (300), so rfc4106(gcm(aes)) and
 * other users of gcm(aes) get the overlapped version. */
static struct aead_alg sg2_gcm_alg = {
 .setkey = sg2_gcm_setkey, .setauthsize = sg2_gcm_setauthsize,
 .encrypt = sg2_gcm_encrypt, .decrypt = sg2_gcm_decrypt,
 .init = sg2_gcm_init, .exit = sg2_gcm_exit,
 .ivsize = GCM_AES_IV_SIZE, .maxauthsize = 16, .chunksize = 16,
 .base = { .cra_name = "gcm(aes)", .cra_driver_name = "sg2002-gcm-aes", .cra_priority = 400,
  .cra_flags = CRYPTO_ALG_ASYNC | CRYPTO_ALG_KERN_DRIVER_ONLY, .cra_blocksize = 1,
  .cra_ctxsize = sizeof(struct sg2_gcm_ctx), .cra_module = THIS_MODULE },
};
