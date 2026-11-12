/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * Direct access for AES in CTR, ECB and CBC decryption: the engine reads the
 * caller's source and writes the caller's destination, with no copy through
 * the set's buffers. One start of the engine takes a chain of descriptors,
 * one per range of memory (a descriptor costs nothing: 64 KiB as one
 * descriptor or as sixty-four take the same 200 us), so a scatterlist of
 * pages, or the pages of a user buffer, are one start.
 *
 * What the engine does and does not do, measured on the device:
 * - every descriptor has its own key and its own IV; the engine carries no
 *   chaining value from one to the next. CTR (the counter advanced), ECB and
 *   CBC decryption (the ciphertext block before) can be chained, CBC
 *   encryption cannot and keeps the copies;
 * - a hash descriptor after another hash descriptor corrupts the engine until
 *   reboot, so hashes keep the copies too.
 *
 * The device is not cache-coherent: when memory comes back from the engine
 * its cache lines are invalidated whole, and a byte the CPU wrote beside the
 * engine's output meanwhile is gone. So the engine writes only whole cache
 * lines of the destination. The head and the tail of a range that do not
 * fill a line are written to the set's output buffer by descriptors of their
 * own in the same chain and copied from there. The source is only read.
 *
 * A request qualifies when every range of it starts on a 16-byte boundary,
 * in memory and in the stream, and is whole blocks long (the last one of a
 * CTR request may end anywhere): then the lines of the destination fall on
 * blocks. Anything else takes the copies as before.
 */
#define SG2_CHAIN_MAX 64
#define SG2_LINE L1_CACHE_BYTES
/* A user buffer is pinned page by page; below this the copies are cheaper. */
#define SG2_PIN_MIN 16384
#define SG2_PIN_PAGES (SG2_LARGE / PAGE_SIZE + 1)

struct sg2_span { struct page *page; dma_addr_t dma; u32 len; enum dma_data_direction dir; };
struct sg2_fix { u8 *to; u32 off, len; };
struct sg2_direct {
 u32 descs, spans, fixes, bounce, bytes;
 bool tail_in, lost;
 struct sg2_span span[2 * SG2_CHAIN_MAX];
 struct sg2_fix fix[2 * SG2_CHAIN_MAX];
};
struct sg2_dkey { const u8 *key; u32 keylen, mode; bool encrypt; };

/* The ranges of a request: of two scatterlists, or of two pinned user buffers. */
struct sg2_walk {
 struct scatterlist *s, *d;
 unsigned int so, dof;
 struct page **ip, **op;	/* user: the pages of input and output */
 unsigned int ioff, ooff;	/* user: offset in the first page */
 unsigned int left;
};

/* The next range, at most limit bytes: where it is, and its length. */
static unsigned int sg2_walk_next(struct sg2_walk *w, unsigned int limit,
 struct page **sp, unsigned int *soff, struct page **dp, unsigned int *doff)
{
 unsigned int n;
 if (w->ip) {
  *sp = w->ip[w->ioff >> PAGE_SHIFT]; *soff = offset_in_page(w->ioff);
  *dp = w->op[w->ooff >> PAGE_SHIFT]; *doff = offset_in_page(w->ooff);
  n = min3((unsigned int)PAGE_SIZE - *soff, (unsigned int)PAGE_SIZE - *doff, w->left);
  n = min(n, limit);
  w->ioff += n; w->ooff += n;
 } else {
  while (w->so == w->s->length) { w->s = sg_next(w->s); w->so = 0; }
  while (w->dof == w->d->length) { w->d = sg_next(w->d); w->dof = 0; }
  *sp = sg_page(w->s); *soff = w->s->offset + w->so;
  *dp = sg_page(w->d); *doff = w->d->offset + w->dof;
  n = min3(w->s->length - w->so, w->d->length - w->dof, w->left);
  n = min(n, limit);
  w->so += n; w->dof += n;
 }
 w->left -= n;
 return n;
}

static bool sg2_direct_mode(u32 algorithm, u32 mode, bool encrypt)
{
 return algorithm == HWCRYPTO_ALG_AES && (mode != HWCRYPTO_MODE_CBC || !encrypt);
}

/* Do the ranges of len bytes after skip bytes of two scatterlists qualify? */
static bool sg2_direct_sg_ok(struct scatterlist *s, struct scatterlist *d, unsigned int skip, unsigned int len)
{
 unsigned int so = skip, dof = skip, left = len;
 while (left) {
  unsigned int n;
  if (!s || !d) return false;
  if (so >= s->length) { so -= s->length; s = sg_next(s); continue; }
  if (dof >= d->length) { dof -= d->length; d = sg_next(d); continue; }
  n = min3(s->length - so, d->length - dof, left);
  if (((s->offset + so) | (d->offset + dof)) & 15) return false;
  if ((n & 15) && n != left) return false;
  so += n; dof += n; left -= n;
 }
 return true;
}
static bool sg2_direct_ok(struct skcipher_request *req, const struct sg2_cipher_alg *a, bool encrypt)
{
 return sg2_direct_mode(a->hardware, a->mode, encrypt) && sg2_direct_sg_ok(req->src, req->dst, 0, req->cryptlen);
}
/* Start a walk of two scatterlists skip bytes into them. */
static void sg2_walk_skip(struct sg2_walk *w, unsigned int skip)
{
 w->so = w->dof = skip;
 while (w->s && w->so >= w->s->length && sg_next(w->s)) { w->so -= w->s->length; w->s = sg_next(w->s); }
 while (w->d && w->dof >= w->d->length && sg_next(w->d)) { w->dof -= w->d->length; w->d = sg_next(w->d); }
}

/* The counter block of CTR advanced by a number of blocks. */
static void sg2_add_counter(u8 *v, u32 blocks)
{
 u64 lo = get_unaligned_be64(v + 8), sum = lo + blocks;
 put_unaligned_be64(sum, v + 8);
 if (sum < lo) put_unaligned_be64(get_unaligned_be64(v) + 1, v);
}

/* One descriptor of the chain. */
static void sg2_direct_desc(struct sg2_direct *p, const struct sg2_dkey *k,
 dma_addr_t from, dma_addr_t to, u32 len, const u8 *iv)
{
 u32 *q = cur->chain + p->descs * (DESC_BYTES / 4);
 dma_addr_t next = cur->chain_dma + (p->descs + 1) * DESC_BYTES;
 memset(q, 0, DESC_BYTES);
 q[0] = 0x9 | BIT(19) | BIT(9) | (k->mode != HWCRYPTO_MODE_ECB ? BIT(23) : 0);
 q[1] = (k->keylen == 16 ? BIT(5) : k->keylen == 24 ? BIT(4) : BIT(3)) |
  (k->mode == HWCRYPTO_MODE_CBC ? BIT(1) : k->mode == HWCRYPTO_MODE_CTR ? BIT(2) : 0) |
  (k->encrypt || k->mode == HWCRYPTO_MODE_CTR ? BIT(0) : 0);
 q[2] = lower_32_bits(next); q[3] = upper_32_bits(next);
 q[4] = lower_32_bits(from); q[5] = upper_32_bits(from);
 q[6] = lower_32_bits(to); q[7] = upper_32_bits(to);
 q[8] = len;
 memcpy(&q[10], k->key, k->keylen);
 if (k->mode != HWCRYPTO_MODE_ECB) memcpy(&q[18], iv, 16);
 p->descs++;
}

/* The descriptors of one range: n bytes at soff of sp to doff of dp. iv is
 * the chaining value at its start and is advanced past it. */
static int sg2_direct_add(struct sg2_direct *p, const struct sg2_dkey *k,
 struct page *sp, unsigned int soff, struct page *dp, unsigned int doff, unsigned int n, u8 *iv)
{
 unsigned int rem = n & 15, whole = n - rem;
 unsigned int first = ALIGN(doff, SG2_LINE), last = ALIGN_DOWN(doff + whole, SG2_LINE);
 unsigned int part[3] = { whole, 0, 0 }, i, done = 0;
 const u8 *sv = (const u8 *)page_address(sp) + soff;
 u8 *dv = (u8 *)page_address(dp) + doff;
 dma_addr_t src, dst = 0;

 if (first < last) { part[0] = first - doff; part[1] = last - first; part[2] = doff + whole - last; }
 src = dma_map_page(&pdev->dev, sp, soff, n, DMA_TO_DEVICE);
 if (dma_mapping_error(&pdev->dev, src)) return -ENOMEM;
 p->span[p->spans++] = (struct sg2_span){ sp, src, n, DMA_TO_DEVICE };
 if (part[1]) {
  dst = dma_map_page(&pdev->dev, dp, first, part[1], DMA_FROM_DEVICE);
  if (dma_mapping_error(&pdev->dev, dst)) return -ENOMEM;
  p->span[p->spans++] = (struct sg2_span){ dp, dst, part[1], DMA_FROM_DEVICE };
 }
 for (i = 0; i < 3; i++) {
  unsigned int len = part[i];
  dma_addr_t to = dst;
  if (!len) continue;
  if (i != 1) {
   to = cur->out_dma + p->bounce;
   p->fix[p->fixes++] = (struct sg2_fix){ dv + done, p->bounce, len };
   p->bounce += ALIGN(len, SG2_LINE);
  }
  sg2_direct_desc(p, k, src + done, to, len, iv);
  if (k->mode == HWCRYPTO_MODE_CTR) sg2_add_counter(iv, len / 16);
  else if (k->mode == HWCRYPTO_MODE_CBC) memcpy(iv, sv + done + len - 16, 16);
  done += len;
 }
 if (rem) {
  /* The last bytes of a CTR request, less than a block: as a whole block
   * from the set's input buffer to its output buffer. */
  memset(cur->in, 0, 16);
  memcpy(cur->in, sv + whole, rem);
  p->tail_in = true;
  p->fix[p->fixes++] = (struct sg2_fix){ dv + whole, p->bounce, rem };
  sg2_direct_desc(p, k, cur->in_dma, cur->out_dma + p->bounce, 16, iv);
  p->bounce += SG2_LINE;
  sg2_add_counter(iv, 1);
 }
 return 0;
}

/* Start the chain. On success the engine lock is held, with bottom halves
 * off, until sg2_direct_finish(): the caller may do work meanwhile that does
 * not sleep and writes none of the memory the chain reads or writes. */
static int sg2_direct_start(struct sg2_direct *p)
{
 u32 *last = cur->chain + (p->descs - 1) * (DESC_BYTES / 4);
 last[0] |= 0xf; last[2] = 0; last[3] = 0;
 dma_sync_single_for_device(&pdev->dev, cur->chain_dma, p->descs * DESC_BYTES, DMA_BIDIRECTIONAL);
 if (p->bounce) dma_sync_single_for_device(&pdev->dev, cur->out_dma, p->bounce, DMA_BIDIRECTIONAL);
 if (p->tail_in) dma_sync_single_for_device(&pdev->dev, cur->in_dma, 2 * SG2_LINE, DMA_BIDIRECTIONAL);
 dma_wmb();
 sg2_engine_lock();
 if (poisoned) {
  sg2_engine_unlock();
  return -EIO;
 }
 writel(7, regs + MASK); writel(7, regs + STATUS);
 writel(lower_32_bits(cur->chain_dma), regs + DESC_LO); writel(upper_32_bits(cur->chain_dma), regs + DESC_HI);
 crypto_submitted[HWCRYPTO_ALG_AES]++;
 writel(DMA_CTRL_VALUE, regs + CTRL);
 return 0;
}

/* Wait for the chain started by sg2_direct_start(). */
static int sg2_direct_finish(struct sg2_direct *p)
{
 u32 status = 0;
 int err = engine_wait(&status);
 if (err || status != 1) {
  sg2_poison();
  sg2_engine_unlock();
  p->lost = true;
  pr_err("hwcrypto_crypto: direct chain of %u DMA uncertain status=%#x timeout=%d; pinned until reboot\n", p->descs, status, err);
  return err ? err : -EIO;
 }
 writel(7, regs + STATUS); writel(initial_mask, regs + MASK);
 sg2_engine_unlock();
 dma_rmb();
 crypto_completed[HWCRYPTO_ALG_AES]++;
 return 0;
}
static int sg2_direct_run(struct sg2_direct *p)
{
 int err = sg2_direct_start(p);
 return err ? err : sg2_direct_finish(p);
}

/* Give the memory of a start back, whether it ran or not. first16: where to
 * keep the first 16 bytes of the set's output buffer (E(J0) of GCM). */
static void sg2_direct_done(struct sg2_direct *p, int err, u8 *first16)
{
 u32 i;
 if (p->lost) {
  /* The engine may still write to the caller's pages: they are never
   * unmapped and never freed. The set stays with it too (sg2_poison()). */
  for (i = 0; i < p->spans; i++) get_page(p->span[i].page);
  return;
 }
 for (i = 0; i < p->spans; i++)
  dma_unmap_page(&pdev->dev, p->span[i].dma, p->span[i].len, p->span[i].dir);
 if (p->descs) dma_sync_single_for_cpu(&pdev->dev, cur->chain_dma, p->descs * DESC_BYTES, DMA_BIDIRECTIONAL);
 if (p->bounce) dma_sync_single_for_cpu(&pdev->dev, cur->out_dma, p->bounce, DMA_BIDIRECTIONAL);
 if (p->tail_in) dma_sync_single_for_cpu(&pdev->dev, cur->in_dma, 2 * SG2_LINE, DMA_BIDIRECTIONAL);
 if (!err) {
  if (first16) memcpy(first16, cur->out, 16);
  for (i = 0; i < p->fixes; i++) memcpy(p->fix[i].to, cur->out + p->fix[i].off, p->fix[i].len);
 }
 /* Every descriptor holds the key. */
 memzero_explicit(cur->chain, p->descs * DESC_BYTES);
 memzero_explicit(cur->out, p->bounce);
 if (p->tail_in) memzero_explicit(cur->in, 2 * SG2_LINE);
}

/* All of a request, in as many starts as its ranges need. iv is advanced.
 * *lost: the engine did not answer and still owns the caller's memory. */
static int sg2_direct_all(const struct sg2_dkey *k, struct sg2_walk *w, u8 *iv, bool *lost)
{
 int err = 0;
 while (w->left && !err) {
  struct sg2_direct *p;
  unsigned int room;
  sg2_own();
  if (poisoned) { sg2_disown(); return -EIO; }
  p = cur->plan;
  p->descs = p->spans = p->fixes = p->bounce = p->bytes = 0;
  p->tail_in = p->lost = false;
  room = sg2_room();
  /* A range takes at most four descriptors, two mappings and three lines of
   * the output buffer. */
  while (w->left && p->bytes < room && p->descs + 4 <= SG2_CHAIN_MAX &&
         p->bounce + 3 * SG2_LINE <= cur->out_size) {
   struct page *sp, *dp;
   unsigned int soff, doff, n = sg2_walk_next(w, room - p->bytes, &sp, &soff, &dp, &doff);
   err = sg2_direct_add(p, k, sp, soff, dp, doff, n, iv);
   if (err) break;
   p->bytes += n;
  }
  if (!err) err = sg2_direct_run(p);
  sg2_direct_done(p, err, NULL);
  if (p->lost && lost) *lost = true;
  sg2_disown();
  if (!err) sg2_resched();
 }
 return err;
}

static int sg2_cipher_direct(struct skcipher_request *req, bool encrypt)
{
 struct sg2_cipher_ctx *ctx = crypto_skcipher_ctx(crypto_skcipher_reqtfm(req));
 struct sg2_dkey k = { ctx->key, ctx->keylen, ctx->alg->mode, encrypt };
 struct sg2_walk w = { .s = req->src, .d = req->dst, .left = req->cryptlen };
 u8 iv[16] = {};
 int err;

 if (k.mode) memcpy(iv, req->iv, 16);
 err = sg2_direct_all(&k, &w, iv, NULL);
 if (!err && k.mode) memcpy(req->iv, iv, 16);
 memzero_explicit(iv, sizeof(iv));
 return err;
}

/*
 * HWCRYPTO_RUN on the pages of the user's buffers. Returns -EAGAIN when the
 * request does not qualify (the caller then copies as before): another
 * algorithm or mode, a short request, buffers not on a 16-byte boundary,
 * input and output at different offsets in their pages (every page would be
 * two ranges with heads and tails of their own: slower than the copies), or
 * input and output that overlap without being the same.
 */
static int sg2_run_direct(struct hwcrypto_request *r)
{
 struct page *ip[SG2_PIN_PAGES], *op[SG2_PIN_PAGES];
 struct sg2_dkey k = { r->key, r->key_length, r->mode, r->encrypt };
 unsigned long in = r->input, out = r->output;
 unsigned int len = r->length, nin, nout;
 struct sg2_walk w = { .ip = ip, .op = op, .ioff = offset_in_page(in), .ooff = offset_in_page(out), .left = len };
 bool same = in == out, lost = false;
 u8 iv[16];
 int got, err;

 if (!sg2_direct_mode(r->algorithm, r->mode, r->encrypt) || len < SG2_PIN_MIN || ((in | out) & 15) ||
     offset_in_page(in ^ out)) return -EAGAIN;
 if (!same && in < out + len && out < in + len) return -EAGAIN;
 nin = DIV_ROUND_UP(offset_in_page(in) + len, PAGE_SIZE);
 nout = DIV_ROUND_UP(offset_in_page(out) + len, PAGE_SIZE);
 got = pin_user_pages_fast(out & PAGE_MASK, nout, FOLL_WRITE, op);
 if (got != nout) { if (got > 0) unpin_user_pages(op, got); return -EAGAIN; }
 if (same) {
  w.ip = op;
 } else {
  got = pin_user_pages_fast(in & PAGE_MASK, nin, 0, ip);
  if (got != nin) { if (got > 0) unpin_user_pages(ip, got); unpin_user_pages(op, nout); return -EAGAIN; }
 }
 memcpy(iv, r->iv, 16);
 err = sg2_direct_all(&k, &w, iv, &lost);
 memzero_explicit(iv, sizeof(iv));
 /* A lost engine keeps the pages pinned for good. */
 if (!lost) {
  if (!same) unpin_user_pages(ip, nin);
  unpin_user_pages_dirty_lock(op, nout, !err);
 }
 if (!err) r->output_length = len;
 return err;
}
