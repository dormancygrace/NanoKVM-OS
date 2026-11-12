/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * gcm(aes) with the CryptoDMA engine for AES-CTR and the CPU for GHASH, the two
 * overlapped. The gcm template runs them one after the other: a CTR request
 * that waits for the engine, then GHASH over the whole message. Here each
 * chunk of up to HWCRYPTO_MAX bytes is started on the engine, and while
 * it runs the CPU hashes the associated data (first chunk) and the ciphertext
 * of the previous chunk (encryption) or of the same chunk (decryption, whose
 * ciphertext is the input). E(J0) for the tag comes from the first chunk, as
 * in the template: 16 zero bytes in front of the data, counter J0.
 *
 * The engine owns the caller's DMA set between crypto_start_locked() and
 * crypto_finish_locked(); the GHASH in between reads only the caller's
 * scatterlists. GHASH is lib/crypto's (the C906 assembly with patch 0041).
 *
 * The engine lock stays held across each chunk's GHASH (about 40 us for
 * 4 KiB with the table GHASH), so hardware SRTP requests wait behind a gcm(aes)
 * request. Below about 4 KiB this gains little over the template: a
 * one-chunk encryption overlaps only the associated data.
 */
#include <crypto/internal/aead.h>
#include <crypto/gcm.h>
#include <crypto/gf128hash.h>
#include <crypto/scatterwalk.h>
#include <crypto/utils.h>

struct sg2_gcm_ctx { u8 key[32]; u32 keylen; struct ghash_key ghash; struct crypto_aead *fallback; };
/* fallback_req is last: the fallback's own request context follows it. */
struct sg2_gcm_req { struct work_struct work; struct aead_request *req; bool encrypt; struct aead_request fallback_req; };

/* The software gcm(aes): for short requests and after the poison latch
 * (crypto_linux_api.inc). */
static int sg2_gcm_fallback(struct aead_request *req, bool encrypt)
{
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(crypto_aead_reqtfm(req));
 struct aead_request *sub = &((struct sg2_gcm_req *)aead_request_ctx(req))->fallback_req;
 aead_request_set_tfm(sub, ctx->fallback);
 aead_request_set_callback(sub, req->base.flags, NULL, NULL);
 aead_request_set_crypt(sub, req->src, req->dst, req->cryptlen, req->iv);
 aead_request_set_ad(sub, req->assoclen);
 return encrypt ? crypto_aead_encrypt(sub) : crypto_aead_decrypt(sub);
}

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
 struct hwcrypto_request r = { .version = 1, .algorithm = HWCRYPTO_ALG_AES,
  .mode = HWCRYPTO_MODE_CTR, .encrypt = 1, .key_length = ctx->keylen };
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
  unsigned int n = min_t(unsigned int, HWCRYPTO_MAX - lead, cryptlen - pos);
  struct crypto_sizes s = {};

  r.length = lead + n;
  r.output_length = r.length;
  sg2_own();
  if (poisoned) { err = -EIO; goto unlock; }
  memset(cur->in, 0, lead);
  if (n && sg_pcopy_to_buffer(req->src, in_nents, cur->in + lead, n, assoclen + pos) != n) {
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
  if (first) memcpy(ej0, cur->out, 16);
  if (n && sg_pcopy_from_buffer(req->dst, out_nents, cur->out + lead, n, assoclen + pos) != n)
   err = -EINVAL;
  sg2_inc_counter(r.iv, 16, DIV_ROUND_UP(r.length, 16));
clean:
  crypto_clean_locked(&r);
unlock:
  sg2_disown();
  if (err) goto out;
  prev_pos = pos; prev_len = n; pos += n; first = false;
  sg2_resched();
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
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(crypto_aead_reqtfm(req));
 int err = sg2_use_fallback(ctx->fallback) ? sg2_gcm_fallback(req, r->encrypt) :
  sg2_gcm_process(req, r->encrypt);
 sg2_async_leave();
 sg2_complete(aead_request_complete, req, err);
}

static int sg2_gcm_submit(struct aead_request *req, bool encrypt)
{
 struct crypto_aead *tfm = crypto_aead_reqtfm(req);
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 struct sg2_gcm_req *r = aead_request_ctx(req);
 int err;
 if (!ctx->keylen) return -ENOKEY;
 if (!encrypt && req->cryptlen < crypto_aead_authsize(tfm)) return -EINVAL;
 if (sg2_use_fallback(ctx->fallback)) return sg2_gcm_fallback(req, encrypt);
 if (ctx->fallback && req->cryptlen - (encrypt ? 0 : crypto_aead_authsize(tfm)) <= sg2_gcm_soft_max(ctx->keylen))
  return sg2_gcm_fallback(req, encrypt);
 if ((req->base.flags & CRYPTO_TFM_REQ_MAY_SLEEP) || sg2_inline()) return sg2_gcm_process(req, encrypt);
 err = sg2_async_enter(req->base.flags);
 if (err) return err;
 r->req = req; r->encrypt = encrypt; INIT_WORK(&r->work, sg2_gcm_work);
 queue_work(crypto_api_wq, &r->work);
 return -EINPROGRESS;
}
static int sg2_gcm_encrypt(struct aead_request *req) { return sg2_gcm_submit(req, true); }
static int sg2_gcm_decrypt(struct aead_request *req) { return sg2_gcm_submit(req, false); }

static int sg2_gcm_setkey(struct crypto_aead *tfm, const u8 *key, unsigned int len)
{
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 struct hwcrypto_request r = { .version = 1, .algorithm = HWCRYPTO_ALG_AES,
  .mode = HWCRYPTO_MODE_ECB, .encrypt = 1, .key_length = len, .length = 16, .output_length = 16 };
 struct crypto_aead *fallback = ctx->fallback;
 u8 h[GHASH_BLOCK_SIZE];
 int err;
 memzero_explicit(ctx, sizeof(*ctx));
 ctx->fallback = fallback;
 if (len != 16 && len != 24 && len != 32) return -EINVAL;
 if (fallback) {
  err = crypto_aead_setkey(fallback, key, len);
  if (err) return err;
  if (READ_ONCE(poisoned)) { memcpy(ctx->key, key, len); ctx->keylen = len; return 0; }
 }
 memcpy(r.key, key, len);
 /* H = AES_K(0^128), on the engine. */
 sg2_own();
 if (poisoned) { err = -EIO; goto unlock; }
 memset(cur->in, 0, 16);
 err = crypto_run_locked(&r);
 if (err) goto unlock;
 memcpy(h, cur->out, 16);
 crypto_clean_locked(&r);
unlock:
 sg2_disown();
 if (!err) {
  ghash_preparekey(&ctx->ghash, h);
  memcpy(ctx->key, key, len); ctx->keylen = len;
 } else if (sg2_use_fallback(fallback)) {
  /* The engine failed on this key: the requests go to the software one. */
  memcpy(ctx->key, key, len); ctx->keylen = len; err = 0;
 }
 memzero_explicit(h, sizeof(h)); memzero_explicit(&r, sizeof(r));
 return err;
}
static int sg2_gcm_setauthsize(struct crypto_aead *tfm, unsigned int authsize)
{
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 int err = crypto_gcm_check_authsize(authsize);
 if (!err && ctx->fallback) err = crypto_aead_setauthsize(ctx->fallback, authsize);
 return err;
}
static int sg2_gcm_init(struct crypto_aead *tfm)
{
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 ctx->fallback = crypto_alloc_aead("gcm(aes)", 0, CRYPTO_ALG_ASYNC | CRYPTO_ALG_NEED_FALLBACK);
 if (IS_ERR(ctx->fallback)) ctx->fallback = NULL;	/* no software gcm: the engine only */
 crypto_aead_set_reqsize(tfm, sizeof(struct sg2_gcm_req) +
  (ctx->fallback ? crypto_aead_reqsize(ctx->fallback) : 0));
 return 0;
}
static void sg2_gcm_exit(struct crypto_aead *tfm)
{
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 if (ctx->fallback) crypto_free_aead(ctx->fallback);
 memzero_explicit(ctx, sizeof(*ctx));
}

/* Above the gcm template over nanokvm-aes-ctr (300), so rfc4106(gcm(aes)) and
 * other users of gcm(aes) get the overlapped version. */
static struct aead_alg sg2_gcm_alg = {
 .setkey = sg2_gcm_setkey, .setauthsize = sg2_gcm_setauthsize,
 .encrypt = sg2_gcm_encrypt, .decrypt = sg2_gcm_decrypt,
 .init = sg2_gcm_init, .exit = sg2_gcm_exit,
 .ivsize = GCM_AES_IV_SIZE, .maxauthsize = 16, .chunksize = 16,
 .base = { .cra_name = "gcm(aes)", .cra_driver_name = "nanokvm-gcm-aes", .cra_priority = 400,
  .cra_flags = CRYPTO_ALG_ASYNC | CRYPTO_ALG_KERN_DRIVER_ONLY | CRYPTO_ALG_NEED_FALLBACK, .cra_blocksize = 1,
  .cra_ctxsize = sizeof(struct sg2_gcm_ctx), .cra_module = THIS_MODULE },
};

/*
 * HWCRYPTO_GCM: the same overlap for user space (OpenSSL's AES-GCM records).
 * Each chunk of up to HWCRYPTO_MAX bytes is started on the engine, and while
 * it runs the CPU hashes ciphertext from a kernel copy: this chunk's input
 * when decrypting, the previous chunk's output when encrypting. One caller at
 * a time: the prepared hash key (four tables of 4 KiB) and the copy are
 * shared, and the engine serializes callers anyway.
 */
static DEFINE_MUTEX(gcm_user_lock);
static struct {
 u8 h[GHASH_BLOCK_SIZE];	/* the key the tables were built for */
 bool valid;
 struct ghash_key key;
 u8 text[HWCRYPTO_MAX];
} gcm_user;

static long crypto_gcm_ioctl(unsigned long arg)
{
 struct hwcrypto_gcm_request q;
 struct hwcrypto_request r = { .version = 1, .algorithm = HWCRYPTO_ALG_AES,
  .mode = HWCRYPTO_MODE_CTR, .encrypt = 1 };
 const u8 __user *in;
 u8 __user *out;
 struct ghash_ctx g, saved;
 unsigned int pos = 0, pending = 0;	/* pending: encrypted bytes in gcm_user.text not hashed yet */
 int err = -EINVAL;

 if (copy_from_user(&q, (void __user *)arg, sizeof(q))) { err = -EFAULT; goto clear; }
 if (q.version != HWCRYPTO_ABI || q.encrypt > 1 || !q.input || !q.output ||
     (q.key_length != 16 && q.key_length != 24 && q.key_length != 32) ||
     !q.length || q.length % 16 || memchr_inv(q.reserved, 0, sizeof(q.reserved))) goto clear;
 r.key_length = q.key_length;
 memcpy(r.key, q.key, q.key_length);
 memcpy(r.iv, q.ctr, 16);
 in = u64_to_user_ptr(q.input);
 out = u64_to_user_ptr(q.output);
 err = mutex_lock_interruptible(&gcm_user_lock);
 if (err) goto clear;
 /* A TLS connection sends all its records with one key. */
 if (!gcm_user.valid || crypto_memneq(gcm_user.h, q.h, GHASH_BLOCK_SIZE)) {
  ghash_preparekey(&gcm_user.key, q.h);
  memcpy(gcm_user.h, q.h, GHASH_BLOCK_SIZE);
  gcm_user.valid = true;
 }
 ghash_init(&g, &gcm_user.key);
 /* GHASH byte order to the POLYVAL-format accumulator of lib/crypto. */
 g.acc.lo = cpu_to_le64(get_unaligned_be64(&q.xi[8]));
 g.acc.hi = cpu_to_le64(get_unaligned_be64(&q.xi[0]));

 do {
  /* GCM's counter is the last 32 bits; a chunk never wraps it. */
  u64 room = (1ULL << 32) - get_unaligned_be32(r.iv + 12);
  unsigned int n = min_t(u64, min_t(u64, HWCRYPTO_MAX, room * 16), q.length - pos);
  struct crypto_sizes s = {};
  bool hashed = false;

  /* Decryption hashes its input: copy it where a page fault is allowed. */
  if (!q.encrypt && copy_from_user(gcm_user.text, in + pos, n)) { err = -EFAULT; break; }
  saved = g;
  r.length = r.output_length = n;
  sg2_own();
  if (poisoned) { err = -EIO; goto unlock; }
  if (q.encrypt) {
   if (copy_from_user(cur->in, in + pos, n)) { err = -EFAULT; goto clean; }
  } else {
   memcpy(cur->in, gcm_user.text, n);
  }
  err = crypto_start_locked(&r, &s);
  if (err) goto clean;
  /* While the engine runs: kernel memory only. */
  ghash_update(&g, gcm_user.text, q.encrypt ? pending : n);
  pending = 0;
  hashed = true;
  err = crypto_finish_locked(&r, &s);
  if (err) goto unlock;	/* engine state uncertain: leave its buffers alone */
  if (copy_to_user(out + pos, cur->out, n)) {
   err = -EFAULT;
  } else {
   if (q.encrypt) { memcpy(gcm_user.text, cur->out, n); pending = n; }
   put_unaligned_be32(get_unaligned_be32(r.iv + 12) + n / 16, r.iv + 12);
  }
clean:
  crypto_clean_locked(&r);
unlock:
  sg2_disown();
  if (err) {
   /* A failed decryption chunk is not done: take its ciphertext back out. */
   if (hashed && !q.encrypt) g = saved;
   break;
  }
  pos += n;
  /* cond_resched() is empty with PREEMPT_LAZY: a task woken by the unlock
   * (an SRTP packet waiting for the engine) would otherwise wait for the
   * tick or for the return to user space, several chunks later. */
  if (need_resched() || test_thread_flag(TIF_NEED_RESCHED_LAZY)) schedule();
  /* A killed caller stops at a chunk boundary; the prefix is complete. */
 } while (pos < q.length && !fatal_signal_pending(current));
 /* The last encrypted chunk, or the one before a failed chunk. */
 if (q.encrypt && pending) ghash_update(&g, gcm_user.text, pending);
 if (pos) {
  q.length = pos;
  ghash_final(&g, q.xi);
  memcpy(q.ctr, r.iv, 16);
  err = copy_to_user((void __user *)arg, &q, sizeof(q)) ? -EFAULT : 0;
 }
 mutex_unlock(&gcm_user_lock);
clear:
 memzero_explicit(&q, sizeof(q)); memzero_explicit(&r, sizeof(r));
 memzero_explicit(&g, sizeof(g)); memzero_explicit(&saved, sizeof(saved));
 return err;
}
