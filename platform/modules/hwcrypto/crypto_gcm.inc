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

/*
 * The same with the engine on the caller's memory (crypto_direct.inc), for a
 * request whose text qualifies, and without a copy.
 *
 * GHASH is the slower of the two (100 against the engine's 333 MB/s), so the
 * time of a request is that of its GHASH plus whatever the engine runs with
 * nothing for GHASH to do. A start takes up to SG2_GCM_PIECE bytes, and while
 * the engine has them the CPU hashes:
 *
 * - encrypting: the ciphertext of the pieces before. The first piece has
 *   nothing before it, so it is a short one (a quarter, at most
 *   SG2_GCM_SHORT).
 * - decrypting in place, where the engine writes the plaintext over the
 *   ciphertext the tag is of: the piece after this one. The first piece is
 *   hashed before its start; the last has nothing after it, so it is the
 *   short one.
 * - decrypting to other memory: the piece itself.
 *
 * The engine is given back as soon as it is done, while GHASH goes on
 * (sg2_gcm_hash()): what SRTP may have to wait for is the engine's time for a
 * piece (16 KiB: 50 us at its rate, computed, not measured) and one step of
 * GHASH.
 */
#define SG2_GCM_PIECE 16384
#define SG2_GCM_SHORT 5120
/* GHASH looks at the engine this often while it runs (10 us). */
#define SG2_GCM_STEP 1024
/*
 * Shortest text for this path, and shortest text that is cut for a short
 * piece. Measured with the application stopped, AES-256, us.
 *
 * Encryption, copies -> here: 512 B 20.2 -> 21.0 and 768 B 24.8 -> 24.9 (not
 * taken); 1024 B 28.6 -> 28.3, 1400 B 37.0 -> 34.3, 2048 B 45.0 -> 41.6,
 * 4080 B 74.7 -> 65.5.
 *
 * Decryption in place depends on where the ciphertext is. Still in the cache
 * (the benchmark's own encryption through the copies left it there) the
 * copies take 22.2 / 29.9 / 33.3 / 53.7 us for 1024 / 1400 / 2048 / 4080 B;
 * not in the cache (written by the engine, as by a network card) they take
 * 29.5 / 35.3 / 40.6 / 59.7 and this path 25.1 / 29.6 / 36.7 / 58.3.
 *
 * Cut or not, encrypt / decrypt: 8176 B 115 / 104 uncut, 103 / 94 cut;
 * 16368 B 219 / 196 and 189 / 173; 4080 B cut from 3072: 62 / 59 against
 * 65 / 57; 2048 B cut: 43.7 / 39.0 against 41.7 / 36.3.
 *
 * A caller that may not sleep comes here from SG2_GCM_CUT: its copies (one
 * lock for set and engine) encrypt 1024 / 1400 / 2048 B in 25.9 / 32.5 /
 * 39.4 us, this path in 30.1 / 36.3 / 43.9; 4080 B 67.5 and 67.0, 8176 B
 * 115 and 110.
 */
#define SG2_GCM_MIN 1024
#define SG2_GCM_CUT 4096

/*
 * Hash len bytes at off of sg. While the engine runs (*running), finish its
 * transaction as soon as it is done.
 */
static int sg2_gcm_hash(struct ghash_ctx *g, struct scatterlist *sg, unsigned int off, unsigned int len,
 struct sg2_direct *p, bool *running)
{
 while (len) {
  unsigned int n = *running ? min_t(unsigned int, len, SG2_GCM_STEP) : len;
  sg2_ghash_sg(g, sg, off, n);
  off += n; len -= n;
  if (*running && readl(regs + STATUS)) {
   int err = sg2_direct_finish(p);
   *running = false;
   if (err) return err;
  }
 }
 return 0;
}

/*
 * cryptlen bytes of text skip bytes into src and dst: the engine's AES-CTR
 * from *ctr (advanced), and all of the ciphertext into g. ej0, if not NULL,
 * gets E(*ctr) first, and the text starts at the counter after it.
 */
static int sg2_gcm_direct_core(const struct sg2_dkey *k, struct scatterlist *src, struct scatterlist *dst,
 unsigned int skip, unsigned int cryptlen, bool encrypt, bool inplace, u8 *ctr, struct ghash_ctx *g, u8 *ej0)
{
 struct sg2_walk w = { .s = src, .d = dst, .left = cryptlen };
 /* pos: text done by the engine; hashed: ciphertext in GHASH. */
 unsigned int pos = 0, hashed = 0;
 bool first = true, ahead = !encrypt && inplace;
 int err = 0;

 sg2_walk_skip(&w, skip);
 do {
  struct sg2_direct *p;
  unsigned int room, n, left = cryptlen - pos;
  sg2_own();
  if (poisoned) { sg2_disown(); err = -EIO; break; }
  p = cur->plan;
  p->descs = p->spans = p->fixes = p->bounce = p->bytes = 0;
  p->tail_in = p->lost = false;
  room = min_t(unsigned int, sg2_room(), SG2_GCM_PIECE);
  if (cryptlen >= SG2_GCM_CUT) {
   unsigned int cut = min_t(unsigned int, ALIGN(left / 4, 16), SG2_GCM_SHORT);
   if (encrypt && first) room = min(room, cut);
   /* The short piece is what is left after this one, which ends on a
    * block: only the last piece of a text may end inside one. */
   if (ahead && left > SG2_GCM_SHORT && left <= room + cut) room = (left - cut) & ~15u;
  }
  if (first && ej0) {
   /* E(J0) for the tag: a block of zeroes, to the head of the output buffer. */
   memset(cur->in + SG2_LINE, 0, 16);
   p->tail_in = true;
   sg2_direct_desc(p, k, cur->in_dma + SG2_LINE, cur->out_dma, 16, ctr);
   p->bounce = SG2_LINE;
   sg2_add_counter(ctr, 1);
  }
  while (w.left && p->bytes < room && p->descs + 4 <= SG2_CHAIN_MAX &&
         p->bounce + 3 * SG2_LINE <= cur->out_size) {
   struct page *sp, *dp;
   unsigned int soff, doff, len = sg2_walk_next(&w, room - p->bytes, &sp, &soff, &dp, &doff);
   err = sg2_direct_add(p, k, sp, soff, dp, doff, len, ctr);
   if (err) break;
   p->bytes += len;
  }
  n = p->bytes;
  /* In place, what the engine is about to write over is hashed first. */
  if (!err && ahead && hashed < pos + n) {
   sg2_ghash_sg(g, src, skip + hashed, pos + n - hashed);
   hashed = pos + n;
  }
  if (!err) err = sg2_direct_start(p);
  if (!err) {
   /* While the engine runs, and after it. */
   bool running = true;
   unsigned int upto = encrypt ? pos : ahead ? min(cryptlen, pos + n + room) : pos + n;
   if (upto > hashed) {
    err = sg2_gcm_hash(g, encrypt ? dst : src, skip + hashed, upto - hashed, p, &running);
    hashed = upto;
   }
   if (running) err = sg2_direct_finish(p);
  }
  sg2_direct_done(p, err, first ? ej0 : NULL);
  sg2_disown();
  if (err) break;
  pos += n; first = false;
  sg2_resched();
 } while (pos < cryptlen);
 if (!err && hashed < cryptlen) sg2_ghash_sg(g, encrypt ? dst : src, skip + hashed, cryptlen - hashed);
 return err;
}

/*
 * In place: the engine would write the plaintext over the ciphertext the tag
 * is of. Decided where the text starts: the associated data before it may be
 * in different memory for source and destination while the text is not.
 */
static bool sg2_gcm_inplace(struct aead_request *req)
{
 struct sg2_walk w = { .s = req->src, .d = req->dst, .left = 16 };
 struct page *sp, *dp;
 unsigned int soff, doff;

 if (req->src == req->dst) return true;
 sg2_walk_skip(&w, req->assoclen);
 sg2_walk_next(&w, 16, &sp, &soff, &dp, &doff);
 return sp == dp;
}

static int sg2_gcm_direct(struct aead_request *req, bool encrypt)
{
 struct crypto_aead *tfm = crypto_aead_reqtfm(req);
 struct sg2_gcm_ctx *ctx = crypto_aead_ctx(tfm);
 unsigned int authsize = crypto_aead_authsize(tfm), assoclen = req->assoclen;
 unsigned int cryptlen = req->cryptlen - (encrypt ? 0 : authsize);
 struct sg2_dkey k = { ctx->key, ctx->keylen, HWCRYPTO_MODE_CTR, true };
 __be64 lengths[2] = { cpu_to_be64(8 * (u64)assoclen), cpu_to_be64(8 * (u64)cryptlen) };
 u8 ctr[16], ej0[16], tag[16], expected[16];
 struct ghash_ctx g;
 bool inplace = sg2_gcm_inplace(req);
 int err;

 memcpy(ctr, req->iv, GCM_AES_IV_SIZE);
 put_unaligned_be32(1, ctr + GCM_AES_IV_SIZE);		/* J0 */
 ghash_init(&g, &ctx->ghash);
 sg2_ghash_sg(&g, req->src, 0, assoclen);
 sg2_ghash_pad(&g, assoclen);
 err = sg2_gcm_direct_core(&k, req->src, req->dst, assoclen, cryptlen, encrypt, inplace, ctr, &g, ej0);
 if (err) goto out;
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
 memzero_explicit(&g, sizeof(g)); memzero_explicit(ctr, sizeof(ctr));
 memzero_explicit(ej0, sizeof(ej0)); memzero_explicit(tag, sizeof(tag));
 memzero_explicit(expected, sizeof(expected));
 return err;
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

 if (cryptlen >= (preemptible() ? SG2_GCM_MIN : SG2_GCM_CUT) &&
     sg2_direct_sg_ok(req->src, req->dst, assoclen, cryptlen))
  return sg2_gcm_direct(req, encrypt);
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
 /* The tables of the last two hash keys: a TLS connection has one per
  * direction, and building them takes about as long as hashing 1 KiB. */
 struct {
  u8 h[GHASH_BLOCK_SIZE];	/* the key the tables were built for */
  bool valid;
  struct ghash_key key;
 } keys[2];
 unsigned int recent;	/* the one of keys[] used last */
 u8 text[HWCRYPTO_MAX];
 /* The window of sg2_gcm_user_direct(). */
 struct page *ip[SG2_PIN_PAGES], *op[SG2_PIN_PAGES];
 struct scatterlist ssg[SG2_PIN_PAGES], dsg[SG2_PIN_PAGES];
 /* The window of sg2_gcm_user_copy(): input and output, SG2_GCM_PIECE each. */
 u8 *a, *b;
} gcm_user;

/*
 * Shortest HWCRYPTO_GCM request for the pages of the user's buffers, or for
 * the core on a kernel copy. Beside a 20 Mbit/s stream, AES-256, encrypt /
 * decrypt, us, loop of 4 KiB chunks -> pages: 4 KiB 80 / 52 -> 78 / 62,
 * 8 KiB 119 / 112 -> 123 / 105, 16 KiB 237 / 219 -> 219 / 188, 64 KiB
 * 1037 / 970 -> 719 / 759; -> kernel copy: 4 KiB 73 / 44 -> 75 / 56, 8 KiB
 * 119 / 102 -> 116 / 101, 16 KiB 227 / 209 -> 211 / 190, 64 KiB 1014 / 996 ->
 * 833 / 823.
 */
#define SG2_GCM_USER_MIN 8192
/*
 * HWCRYPTO_GCM on the pages of the user's buffers, in windows of up to
 * SG2_LARGE bytes, *done advanced by each. -EAGAIN: what is left does not
 * qualify, or its pages could not be pinned; the copies take it from there.
 * The conditions are those of sg2_run_direct(), and a counter that does not
 * wrap in the request. Called with gcm_user_lock held.
 */
static int sg2_gcm_user_direct(struct hwcrypto_gcm_request *q, struct ghash_ctx *g, u8 *ctr, unsigned int *done)
{
 struct sg2_dkey k = { q->key, q->key_length, HWCRYPTO_MODE_CTR, true };
 unsigned long in = q->input, out = q->output;
 bool same = in == out;

 if (q->length < SG2_GCM_USER_MIN || ((in | out) & 15) || offset_in_page(in ^ out)) return -EAGAIN;
 if (!same && in < out + q->length && out < in + q->length) return -EAGAIN;
 if (q->length / 16 > (1ULL << 32) - get_unaligned_be32(ctr + 12)) return -EAGAIN;
 while (*done < q->length) {
  unsigned int win = min_t(unsigned int, q->length - *done, SG2_LARGE), left = win, i, n;
  unsigned long a = in + *done, b = out + *done;
  struct ghash_ctx saved = *g;
  u8 ctr0[16];
  int got, err;

  n = DIV_ROUND_UP(offset_in_page(b) + win, PAGE_SIZE);
  got = pin_user_pages_fast(b & PAGE_MASK, n, FOLL_WRITE, gcm_user.op);
  if (got != n) { if (got > 0) unpin_user_pages(gcm_user.op, got); return -EAGAIN; }
  if (!same) {
   got = pin_user_pages_fast(a & PAGE_MASK, n, 0, gcm_user.ip);
   if (got != n) {
    if (got > 0) unpin_user_pages(gcm_user.ip, got);
    unpin_user_pages(gcm_user.op, n);
    return -EAGAIN;
   }
   sg_init_table(gcm_user.ssg, n);
  }
  sg_init_table(gcm_user.dsg, n);
  for (i = 0; i < n; i++) {
   unsigned int off = i ? 0 : offset_in_page(b), len = min_t(unsigned int, PAGE_SIZE - off, left);
   sg_set_page(&gcm_user.dsg[i], gcm_user.op[i], len, off);
   if (!same) sg_set_page(&gcm_user.ssg[i], gcm_user.ip[i], len, off);
   left -= len;
  }
  memcpy(ctr0, ctr, 16);
  err = sg2_gcm_direct_core(&k, same ? gcm_user.dsg : gcm_user.ssg, gcm_user.dsg, 0, win, q->encrypt, same, ctr, g, NULL);
  /* A lost engine holds its own references to the pages (sg2_direct_done()). */
  if (!same) unpin_user_pages(gcm_user.ip, n);
  unpin_user_pages_dirty_lock(gcm_user.op, n, !err);
  if (err) {
   /* The window is not done: its ciphertext out of the hash again. */
   *g = saved; memcpy(ctr, ctr0, 16);
   return err;
  }
  *done += win;
  if (need_resched() || test_thread_flag(TIF_NEED_RESCHED_LAZY)) schedule();
  if (fatal_signal_pending(current)) break;
 }
 return 0;
}

/*
 * HWCRYPTO_GCM for what is left at *done and does not qualify for its own
 * pages: the same core on a kernel copy, a window of up to SG2_GCM_PIECE at a
 * time, for requests of SG2_GCM_USER_MIN bytes and more (shorter ones take the loop
 * of HWCRYPTO_MAX chunks in crypto_gcm_ioctl(): one start each, and at 4 KiB
 * it decrypts in 44 us against 56). Called with gcm_user_lock held.
 */
static int sg2_gcm_user_copy(struct hwcrypto_gcm_request *q, struct ghash_ctx *g, u8 *ctr, unsigned int *done)
{
 struct sg2_dkey k = { q->key, q->key_length, HWCRYPTO_MODE_CTR, true };
 const u8 __user *in = u64_to_user_ptr(q->input);
 u8 __user *out = u64_to_user_ptr(q->output);

 if (!gcm_user.a) {
  u8 *a = kmalloc(SG2_GCM_PIECE, GFP_KERNEL), *b = kmalloc(SG2_GCM_PIECE, GFP_KERNEL);
  if (!a || !b) { kfree(a); kfree(b); return -EAGAIN; }
  gcm_user.a = a; gcm_user.b = b;
 }
 while (*done < q->length) {
  /* GCM's counter is the last 32 bits; a window never wraps it. */
  u64 room = (1ULL << 32) - get_unaligned_be32(ctr + 12);
  unsigned int win = min_t(u64, min_t(u64, SG2_GCM_PIECE, room * 16), q->length - *done);
  struct ghash_ctx saved = *g;
  struct scatterlist ssg, dsg;
  u8 ctr0[16];
  int err;

  if (copy_from_user(gcm_user.a, in + *done, win)) return -EFAULT;
  sg_init_one(&ssg, gcm_user.a, win);
  sg_init_one(&dsg, gcm_user.b, win);
  memcpy(ctr0, ctr, 16);
  err = sg2_gcm_direct_core(&k, &ssg, &dsg, 0, win, q->encrypt, false, ctr, g, NULL);
  if (!err && copy_to_user(out + *done, gcm_user.b, win)) err = -EFAULT;
  if (err) {
   /* The window is not done: its ciphertext out of the hash again. */
   *g = saved; memcpy(ctr, ctr0, 16);
   return err;
  }
  /* The core counts in 128 bits; GCM wraps the last 32. */
  memcpy(ctr, ctr0, 12);
  *done += win;
  if (need_resched() || test_thread_flag(TIF_NEED_RESCHED_LAZY)) schedule();
  if (fatal_signal_pending(current)) break;
 }
 return 0;
}

static long crypto_gcm_ioctl(unsigned long arg)
{
 struct hwcrypto_gcm_request q;
 struct hwcrypto_request r = { .version = 1, .algorithm = HWCRYPTO_ALG_AES,
  .mode = HWCRYPTO_MODE_CTR, .encrypt = 1 };
 const u8 __user *in;
 u8 __user *out;
 struct ghash_ctx g, saved;
 unsigned int pos = 0, pending = 0;	/* pending: encrypted bytes in gcm_user.text not hashed yet */
 unsigned int k;
 bool spoiled = false;
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
 k = gcm_user.recent;
 if (!gcm_user.keys[k].valid || crypto_memneq(gcm_user.keys[k].h, q.h, GHASH_BLOCK_SIZE)) k ^= 1;
 gcm_user.recent = k;
 if (!gcm_user.keys[k].valid || crypto_memneq(gcm_user.keys[k].h, q.h, GHASH_BLOCK_SIZE)) {
  ghash_preparekey(&gcm_user.keys[k].key, q.h);
  memcpy(gcm_user.keys[k].h, q.h, GHASH_BLOCK_SIZE);
  gcm_user.keys[k].valid = true;
 }
 ghash_init(&g, &gcm_user.keys[k].key);
 /* GHASH byte order to the POLYVAL-format accumulator of lib/crypto. */
 g.acc.lo = cpu_to_le64(get_unaligned_be64(&q.xi[8]));
 g.acc.hi = cpu_to_le64(get_unaligned_be64(&q.xi[0]));

 err = sg2_gcm_user_direct(&q, &g, r.iv, &pos);
 /* In place on the caller's pages the engine has written over part of the
  * window that failed (it is lost: nothing else fails there): there is no
  * prefix the caller could continue from in software. */
 spoiled = err && err != -EAGAIN && q.input == q.output;
 if (err == -EAGAIN && q.length >= SG2_GCM_USER_MIN) err = sg2_gcm_user_copy(&q, &g, r.iv, &pos);
 if (err == -EAGAIN) do {
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
 if (pos && !spoiled) {
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
