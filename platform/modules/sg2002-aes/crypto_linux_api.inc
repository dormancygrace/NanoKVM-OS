/* SPDX-License-Identifier: GPL-2.0-only */
/* Linux Crypto API frontend. One shared DMA owner; atomic callers are queued. */
#include <crypto/internal/skcipher.h>
#include <crypto/internal/hash.h>
#include <crypto/internal/des.h>
#include <linux/workqueue.h>
#include <linux/scatterlist.h>
#include <linux/unaligned.h>

static struct workqueue_struct *crypto_api_wq;
struct sg2_cipher_alg {
 struct skcipher_alg alg;
 u32 hardware, mode, block;
};
struct sg2_cipher_ctx { u8 key[32]; u32 keylen; const struct sg2_cipher_alg *alg; };
struct sg2_cipher_req { struct work_struct work; struct skcipher_request *req; bool encrypt; };
static void sg2_inc_counter(u8 *v, unsigned int bytes, unsigned int blocks)
{
 while (blocks--) { unsigned int i = bytes; while (i && !++v[--i]); }
}
static int sg2_cipher_process(struct skcipher_request *req, bool encrypt)
{
 struct sg2_cipher_ctx *ctx = crypto_skcipher_ctx(crypto_skcipher_reqtfm(req));
 const struct sg2_cipher_alg *a = ctx->alg;
 struct sg2002_crypto_request r = { .version = 1, .algorithm = a->hardware,
  .mode = a->mode, .encrypt = encrypt, .key_length = ctx->keylen };
 u8 next_iv[16]; unsigned int pos = 0; int err = 0, in_nents, out_nents;
 if (!req->cryptlen) return 0;
 in_nents = sg_nents_for_len(req->src, req->cryptlen);
 out_nents = sg_nents_for_len(req->dst, req->cryptlen);
 if (in_nents < 0 || out_nents < 0) return -EINVAL;
 memcpy(r.key, ctx->key, ctx->keylen);
 if (a->mode) memcpy(r.iv, req->iv, a->block);
 while (pos < req->cryptlen) {
  r.length = min_t(unsigned int, SG2002_CRYPTO_MAX, req->cryptlen - pos);
  r.output_length = r.length;
  mutex_lock(&lock);
  if (poisoned) { err = -EIO; mutex_unlock(&lock); break; }
  if (sg_pcopy_to_buffer(req->src, in_nents, buffer, r.length, pos) != r.length) { err = -EINVAL; goto chunk_done; }
  if (a->mode == SG2002_MODE_CBC && !encrypt) memcpy(next_iv, buffer + r.length - a->block, a->block);
  err = crypto_run_locked(&r);
  if (err) goto chunk_done;
  if (sg_pcopy_from_buffer(req->dst, out_nents, buffer, r.length, pos) != r.length) { err = -EINVAL; goto chunk_done; }
  if (a->mode == SG2002_MODE_CBC) memcpy(r.iv, encrypt ? buffer + r.length - a->block : next_iv, a->block);
  if (a->mode == SG2002_MODE_CTR) sg2_inc_counter(r.iv, a->block, DIV_ROUND_UP(r.length, a->block));
chunk_done:
  crypto_clean_locked(&r);
  mutex_unlock(&lock);
  if (err) break;
  pos += r.length;
  cond_resched();
 }
 if (!err && a->mode) memcpy(req->iv, r.iv, a->block);
 memzero_explicit(next_iv, sizeof(next_iv)); memzero_explicit(&r, sizeof(r));
 return err;
}
static void sg2_cipher_work(struct work_struct *work)
{
 struct sg2_cipher_req *r = container_of(work, struct sg2_cipher_req, work);
 struct skcipher_request *req = r->req;
 int err = sg2_cipher_process(req, r->encrypt);
 skcipher_request_complete(req, err);
}
static int sg2_cipher_submit(struct skcipher_request *req, bool encrypt)
{
 struct sg2_cipher_ctx *ctx = crypto_skcipher_ctx(crypto_skcipher_reqtfm(req));
 struct sg2_cipher_req *r = skcipher_request_ctx(req);
 if (!ctx->keylen || (ctx->alg->mode != SG2002_MODE_CTR && req->cryptlen % ctx->alg->block)) return -EINVAL;
 if (!req->cryptlen) return 0;
 if (req->base.flags & CRYPTO_TFM_REQ_MAY_SLEEP) return sg2_cipher_process(req, encrypt);
 r->req = req; r->encrypt = encrypt; INIT_WORK(&r->work, sg2_cipher_work);
 queue_work(crypto_api_wq, &r->work);
 return -EINPROGRESS;
}
static int sg2_cipher_encrypt(struct skcipher_request *req) { return sg2_cipher_submit(req, true); }
static int sg2_cipher_decrypt(struct skcipher_request *req) { return sg2_cipher_submit(req, false); }
/* The 4 weak + 12 semi-weak DES keys, compared without parity bits.
 * Avoid depending on the disabled software DES implementation solely for
 * validating hardware keys. The list is defined by the DES key schedule.
 */
static int sg2_verify_des_key(struct crypto_skcipher *tfm, const u8 *key)
{
 static const u64 weak[] = {
  0x0101010101010101ULL, 0xfefefefefefefefeULL, 0x1f1f1f1f0e0e0e0eULL, 0xe0e0e0e0f1f1f1f1ULL,
  0x01fe01fe01fe01feULL, 0xfe01fe01fe01fe01ULL, 0x1fe01fe00ef10ef1ULL, 0xe01fe01ff10ef10eULL,
  0x01e001e001f101f1ULL, 0xe001e001f101f101ULL, 0x1ffe1ffe0efe0efeULL, 0xfe1ffe1ffe0efe0eULL,
  0x011f011f010e010eULL, 0x1f011f010e010e01ULL, 0xe0fee0fef1fef1feULL, 0xfee0fee0fef1fef1ULL };
 u64 value = get_unaligned_be64(key) & 0xfefefefefefefefeULL;
 unsigned int i, match = 0;
 if (!(crypto_skcipher_get_flags(tfm) & CRYPTO_TFM_REQ_FORBID_WEAK_KEYS)) return 0;
 for (i = 0; i < ARRAY_SIZE(weak); i++) match |= value == (weak[i] & 0xfefefefefefefefeULL);
 return match ? -EINVAL : 0;
}
static int sg2_cipher_setkey(struct crypto_skcipher *tfm, const u8 *key, unsigned int len)
{
 struct sg2_cipher_ctx *ctx = crypto_skcipher_ctx(tfm);
 bool valid = ctx->alg->hardware == SG2002_ALG_AES ? (len == 16 || len == 24 || len == 32) :
  ctx->alg->hardware == SG2002_ALG_DES ? len == 8 : ctx->alg->hardware == SG2002_ALG_TDES ? len == 24 : len == 16;
 memzero_explicit(ctx->key, sizeof(ctx->key)); ctx->keylen = 0;
 if (!valid) return -EINVAL;
 if (ctx->alg->hardware == SG2002_ALG_DES && sg2_verify_des_key(tfm, key)) return -EINVAL;
 if (ctx->alg->hardware == SG2002_ALG_TDES && verify_skcipher_des3_key(tfm, key)) return -EINVAL;
 memcpy(ctx->key, key, len); ctx->keylen = len; return 0;
}
static int sg2_cipher_init(struct crypto_skcipher *tfm)
{
 struct sg2_cipher_ctx *ctx = crypto_skcipher_ctx(tfm);
 ctx->alg = container_of(crypto_skcipher_alg(tfm), struct sg2_cipher_alg, alg);
 crypto_skcipher_set_reqsize(tfm, sizeof(struct sg2_cipher_req)); return 0;
}
static void sg2_cipher_exit(struct crypto_skcipher *tfm) { memzero_explicit(crypto_skcipher_ctx(tfm), sizeof(struct sg2_cipher_ctx)); }
#define SG2_CIPHER(mode_name, cipher_name, id, mode_id, bs, min_key, max_key) \
 { .hardware = id, .mode = mode_id, .block = bs, .alg = { \
 .base = { .cra_name = mode_name "(" cipher_name ")", .cra_driver_name = "sg2002-" cipher_name "-" mode_name, \
 .cra_priority = 300, .cra_flags = CRYPTO_ALG_ASYNC | CRYPTO_ALG_KERN_DRIVER_ONLY, \
 .cra_blocksize = mode_id == SG2002_MODE_CTR ? 1 : bs, .cra_ctxsize = sizeof(struct sg2_cipher_ctx), .cra_module = THIS_MODULE }, \
 .min_keysize = min_key, .max_keysize = max_key, .ivsize = mode_id ? bs : 0, .chunksize = bs, \
 .setkey = sg2_cipher_setkey, .encrypt = sg2_cipher_encrypt, .decrypt = sg2_cipher_decrypt, .init = sg2_cipher_init, .exit = sg2_cipher_exit } }
#define SG2_MODES(name, id, bs, min_key, max_key) \
 SG2_CIPHER("ecb", name, id, 0, bs, min_key, max_key), \
 SG2_CIPHER("cbc", name, id, 1, bs, min_key, max_key), \
 SG2_CIPHER("ctr", name, id, 2, bs, min_key, max_key)
static struct sg2_cipher_alg sg2_ciphers[] = {
 SG2_MODES("aes", SG2002_ALG_AES, 16, 16, 32),
 SG2_MODES("des", SG2002_ALG_DES, 8, 8, 8),
 SG2_MODES("des3_ede", SG2002_ALG_TDES, 8, 24, 24),
 SG2_MODES("sm4", SG2002_ALG_SM4, 16, 16, 16),
};

#define SG2_HASH_MAGIC 0x53473248
struct sg2_hash_state { u64 bytes; u32 magic; u32 failed; u8 state[32]; u8 tail[64]; u32 used; };
struct sg2_hash_req { struct sg2_hash_state hash; struct work_struct work; struct ahash_request *req; unsigned int final; };
static int sg2_hash_init(struct ahash_request *req)
{
 struct sg2_hash_req *r = ahash_request_ctx(req);
 static const u32 sha256_iv[] = {0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19};
 static const u32 sha1_iv[] = {0x67452301,0xefcdab89,0x98badcfe,0x10325476,0xc3d2e1f0};
 unsigned int words = crypto_ahash_digestsize(crypto_ahash_reqtfm(req)) / 4, i;
 memset(&r->hash, 0, sizeof(r->hash)); r->hash.magic = SG2_HASH_MAGIC;
 for (i = 0; i < words; i++) put_unaligned_be32(words == 8 ? sha256_iv[i] : sha1_iv[i], r->hash.state + i*4);
 return 0;
}
static int sg2_hash_read(struct ahash_request *req, int nents, unsigned int offset, void *dst, unsigned int len)
{
 if (req->base.flags & CRYPTO_AHASH_REQ_VIRT) { memcpy(dst, req->svirt + offset, len); return 0; }
 return sg_pcopy_to_buffer(req->src, nents, dst, len, offset) == len ? 0 : -EINVAL;
}
static int sg2_hash_blocks(struct ahash_request *req, int nents, unsigned int offset, const u8 *tail, unsigned int len)
{
 struct sg2_hash_req *ctx = ahash_request_ctx(req);
 struct sg2002_crypto_request r = { .version = 1,
  .algorithm = crypto_ahash_digestsize(crypto_ahash_reqtfm(req)) == 32 ? SG2002_ALG_SHA256 : SG2002_ALG_SHA1, .length = len };
 int err;
 memcpy(r.state, ctx->hash.state, sizeof(r.state));
 mutex_lock(&lock);
 if (poisoned) { err = -EIO; goto unlock; }
 if (tail) { memcpy(buffer, tail, len); err = 0; } else err = sg2_hash_read(req, nents, offset, buffer, len);
 if (!err) err = crypto_run_locked(&r);
 if (!err) memcpy(ctx->hash.state, r.state, sizeof(r.state));
 crypto_clean_locked(&r);
unlock:
 mutex_unlock(&lock); memzero_explicit(&r, sizeof(r)); return err;
}
static int sg2_hash_process(struct ahash_request *req, bool final, bool consume)
{
 struct sg2_hash_req *ctx = ahash_request_ctx(req);
 struct sg2_hash_state *s = &ctx->hash;
 unsigned int remaining = consume ? req->nbytes : 0, pos = 0, n;
 int nents = 0, err = 0;
 u8 pad[128];
 if (s->magic != SG2_HASH_MAGIC || s->failed || s->used >= 64 || (s->bytes & 63) != s->used) return -EINVAL;
 if (remaining > U64_MAX/8 - s->bytes) return -EOVERFLOW;
 if (remaining) {
  if (req->base.flags & CRYPTO_AHASH_REQ_VIRT) { if (!req->svirt) return -EINVAL; }
  else { nents = sg_nents_for_len(req->src, remaining); if (nents < 0) return nents; }
 }
 s->bytes += remaining;
 while (remaining) {
  if (s->used || remaining < 64) {
   n = min(remaining, 64 - s->used);
   err = sg2_hash_read(req, nents, pos, s->tail + s->used, n);
   if (err) goto failed;
   s->used += n; pos += n; remaining -= n;
   if (s->used == 64) {
    err = sg2_hash_blocks(req, nents, 0, s->tail, 64); if (err) goto failed;
    s->used = 0; memzero_explicit(s->tail, sizeof(s->tail));
   }
  } else {
   n = min_t(unsigned int, SG2002_CRYPTO_MAX, remaining & ~63U);
   err = sg2_hash_blocks(req, nents, pos, NULL, n); if (err) goto failed;
   pos += n; remaining -= n;
  }
  cond_resched();
 }
 if (!final) return 0;
 if (!req->result) { err = -EINVAL; goto failed; }
 memset(pad, 0, sizeof(pad)); memcpy(pad, s->tail, s->used); pad[s->used] = 0x80;
 n = s->used < 56 ? 64 : 128; put_unaligned_be64(s->bytes * 8, pad + n - 8);
 err = sg2_hash_blocks(req, nents, 0, pad, n); memzero_explicit(pad, sizeof(pad));
 if (err) goto failed;
 memcpy(req->result, s->state, crypto_ahash_digestsize(crypto_ahash_reqtfm(req)));
 memzero_explicit(s, sizeof(*s)); return 0;
failed:
 s->failed = 1; return err;
}
/* The request context contains no mappings or borrowed stack pointers. */
;
static void sg2_hash_work(struct work_struct *work)
{
 struct sg2_hash_req *ctx = container_of(work, struct sg2_hash_req, work);
 struct ahash_request *req = ctx->req;
 /* 0=update, 1=finup, 2=final without consuming req->nbytes. */
 bool consume = ctx->final != 2;
 int err = sg2_hash_process(req, ctx->final, consume);
 ahash_request_complete(req, err);
}
static int sg2_hash_submit(struct ahash_request *req, unsigned int operation)
{
 struct sg2_hash_req *ctx = ahash_request_ctx(req);
 if (req->base.flags & CRYPTO_TFM_REQ_MAY_SLEEP) return sg2_hash_process(req, operation != 0, operation != 2);
 ctx->req = req; ctx->final = operation; INIT_WORK(&ctx->work, sg2_hash_work);
 queue_work(crypto_api_wq, &ctx->work); return -EINPROGRESS;
}
static int sg2_hash_update(struct ahash_request *req) { return sg2_hash_submit(req, 0); }
static int sg2_hash_final(struct ahash_request *req) { return sg2_hash_submit(req, 2); }
static int sg2_hash_finup(struct ahash_request *req) { return sg2_hash_submit(req, 1); }
static int sg2_hash_digest(struct ahash_request *req) { sg2_hash_init(req); return sg2_hash_submit(req, 1); }
static int sg2_hash_export(struct ahash_request *req, void *out)
{
 struct sg2_hash_req *r = ahash_request_ctx(req);
 if (r->hash.magic != SG2_HASH_MAGIC || r->hash.failed) return -EINVAL;
 memcpy(out, &r->hash, sizeof(r->hash)); return 0;
}
static int sg2_hash_import(struct ahash_request *req, const void *in)
{
 struct sg2_hash_req *r = ahash_request_ctx(req); struct sg2_hash_state s;
 memcpy(&s, in, sizeof(s));
 if (s.magic != SG2_HASH_MAGIC || s.failed || s.used >= 64 || (s.bytes & 63) != s.used || s.bytes > U64_MAX/8) { memzero_explicit(&s, sizeof(s)); return -EINVAL; }
 memcpy(&r->hash, &s, sizeof(s)); memzero_explicit(&s, sizeof(s)); return 0;
}
#define SG2_HASH(name, digest_len) { .init = sg2_hash_init, .update = sg2_hash_update, .final = sg2_hash_final, \
 .finup = sg2_hash_finup, .digest = sg2_hash_digest, .export = sg2_hash_export, .import = sg2_hash_import, \
 .halg = { .digestsize = digest_len, .statesize = sizeof(struct sg2_hash_state), .base = { \
 .cra_name = name, .cra_driver_name = "sg2002-" name, .cra_priority = 300, \
 .cra_flags = CRYPTO_ALG_ASYNC | CRYPTO_ALG_KERN_DRIVER_ONLY | CRYPTO_ALG_NO_FALLBACK, \
 .cra_blocksize = 64, .cra_reqsize = sizeof(struct sg2_hash_req), .cra_module = THIS_MODULE } } }
static struct ahash_alg sg2_hashes[] = { SG2_HASH("sha1", 20), SG2_HASH("sha256", 32) };
static int crypto_linux_register(void)
{
 int err; unsigned int i;
 crypto_api_wq = alloc_ordered_workqueue("sg2002-crypto", WQ_MEM_RECLAIM);
 if (!crypto_api_wq) return -ENOMEM;
 for (i = 0; i < ARRAY_SIZE(sg2_ciphers); i++) {
  err = crypto_register_skcipher(&sg2_ciphers[i].alg); if (err) goto ciphers;
 }
 err = crypto_register_ahashes(sg2_hashes, ARRAY_SIZE(sg2_hashes)); if (!err) return 0;
ciphers:
 while (i) crypto_unregister_skcipher(&sg2_ciphers[--i].alg);
 destroy_workqueue(crypto_api_wq); crypto_api_wq = NULL; return err;
}
static void crypto_linux_unregister(void)
{
 unsigned int i;
 crypto_unregister_ahashes(sg2_hashes, ARRAY_SIZE(sg2_hashes));
 for (i = ARRAY_SIZE(sg2_ciphers); i; i--) crypto_unregister_skcipher(&sg2_ciphers[i-1].alg);
 destroy_workqueue(crypto_api_wq); crypto_api_wq = NULL;
}
