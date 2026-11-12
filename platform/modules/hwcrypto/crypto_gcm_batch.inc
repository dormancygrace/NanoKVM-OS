/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * HWCRYPTO_GCM_BATCH: AES-GCM encryption of up to HWCRYPTO_GCM_BATCH_MAX short
 * messages with one key, each whole (text and tag), in two starts of the
 * engine. This is a burst of DTLS records of a data channel: 16 records of
 * 1.2 KiB. One by one they cost a system call each and never reach the size
 * from which the engine pays (HWCRYPTO_GCM takes whole blocks of one message
 * and leaves the tag to the caller).
 *
 * A message is one descriptor of a chain (crypto_direct.inc: every descriptor
 * has its own IV): counter mode from J0 over a zero block and the text behind
 * it, in the set's own buffers. The zero block comes back as E(J0), which the
 * tag needs, and the text gets the counters from J0 + 1, as GCM wants. Then
 * the CPU computes each tag (GHASH over the associated data and the
 * ciphertext, lib/crypto's tables) and the results go to the caller. The hash
 * key's tables are those HWCRYPTO_GCM keeps (the last two keys).
 *
 * GHASH is the slower of the two, and a tag needs its ciphertext. So a batch
 * of four records or more is two starts: the first quarter of the records,
 * then the rest, and while the engine has the rest the CPU computes the tags
 * of the first quarter.
 *
 * The engine only ever has the driver's buffers here, so an engine that does
 * not answer is given up as in the other paths through the copies
 * (sg2_poison()), and nothing was written to the caller's memory: the results
 * are copied out after the engine has finished all of them.
 */
struct gcm_batch { struct hwcrypto_gcm_batch_record r[HWCRYPTO_GCM_BATCH_MAX]; u32 off[HWCRYPTO_GCM_BATCH_MAX]; };

/* Start the chain of records [first, end), bytes of the set's buffers: on
 * success the engine lock is held, with bottom halves off, until
 * gcm_batch_finish(). */
static int gcm_batch_start(const struct sg2_dkey *k, const struct gcm_batch *b, u32 first, u32 end, u32 bytes)
{
 struct sg2_direct *p = cur->plan;
 u32 from = b->off[first], i;
 p->descs = p->spans = p->fixes = p->bounce = p->bytes = 0;
 p->tail_in = false;
 for (i = first; i < end; i++)
  sg2_direct_desc(p, k, cur->in_dma + b->off[i], cur->out_dma + b->off[i],
                  16 + ALIGN(b->r[i].length, 16), b->r[i].j0);
 dma_sync_single_for_device(&pdev->dev, cur->in_dma + from, bytes, DMA_BIDIRECTIONAL);
 dma_sync_single_for_device(&pdev->dev, cur->out_dma + from, bytes, DMA_BIDIRECTIONAL);
 return sg2_direct_start(p);
}

/* Wait for the chain over bytes at from of the set's buffers and release the engine. */
static int gcm_batch_finish(u32 from, u32 bytes)
{
 u32 status = 0;
 int err = engine_wait(&status);
 if (err || status != 1) {
  sg2_poison();
  sg2_engine_unlock();
  pr_err("hwcrypto_crypto: GCM batch DMA uncertain status=%#x timeout=%d; pinned until reboot\n", status, err);
  return err ? err : -EIO;
 }
 writel(7, regs + STATUS); writel(initial_mask, regs + MASK);
 sg2_engine_unlock();
 dma_rmb();
 crypto_completed[HWCRYPTO_ALG_AES]++;
 dma_sync_single_for_cpu(&pdev->dev, cur->out_dma + from, bytes, DMA_BIDIRECTIONAL);
 return 0;
}

/* The tag of record i, whose ciphertext is in the set's output buffer. */
static void gcm_batch_tag(const struct gcm_batch *b, u32 i, const struct ghash_key *key, u8 *tag)
{
 const struct hwcrypto_gcm_batch_record *r = &b->r[i];
 const u8 *out = cur->out + b->off[i];
 __be64 lengths[2] = { cpu_to_be64(8 * (u64)r->aad_length), cpu_to_be64(8 * (u64)r->length) };
 struct ghash_ctx g;
 ghash_init(&g, key);
 ghash_update(&g, r->aad, r->aad_length);
 sg2_ghash_pad(&g, r->aad_length);
 ghash_update(&g, out + 16, r->length);
 sg2_ghash_pad(&g, r->length);
 ghash_update(&g, (const u8 *)lengths, sizeof(lengths));
 ghash_final(&g, tag);
 crypto_xor(tag, out, 16);
 memzero_explicit(&g, sizeof(g));
}

static long crypto_gcm_batch_ioctl(unsigned long arg)
{
 struct hwcrypto_gcm_batch_request q;
 struct gcm_batch *b = NULL;
 struct sg2_dkey k = {};
 u32 i, total = 0, head, descs = 0;
 unsigned int key;
 int err = -EINVAL;

 if (copy_from_user(&q, (void __user *)arg, sizeof(q))) { err = -EFAULT; goto clear; }
 if (q.version != HWCRYPTO_ABI || !q.count || q.count > HWCRYPTO_GCM_BATCH_MAX || q.reserved || !q.records ||
     (q.key_length != 16 && q.key_length != 24 && q.key_length != 32) ||
     memchr_inv(q.reserved2, 0, sizeof(q.reserved2))) goto clear;
 b = kmalloc(sizeof(*b), GFP_KERNEL);
 if (!b) { err = -ENOMEM; goto clear; }
 if (copy_from_user(b->r, u64_to_user_ptr(q.records), q.count * sizeof(b->r[0]))) { err = -EFAULT; goto clear; }
 for (i = 0; i < q.count; i++) {
  const struct hwcrypto_gcm_batch_record *r = &b->r[i];
  if (!r->data || !r->length || r->length > HWCRYPTO_MAX || r->aad_length > sizeof(r->aad)) goto clear;
  b->off[i] = total;
  total += 16 + ALIGN(r->length, 16);
 }
 /* The records lie one after another in the large set's input and output. */
 BUILD_BUG_ON(HWCRYPTO_GCM_BATCH_BYTES > SG2_LARGE);
 if (total > HWCRYPTO_GCM_BATCH_BYTES) goto clear;
 k = (struct sg2_dkey){ q.key, q.key_length, HWCRYPTO_MODE_CTR, true };

 err = mutex_lock_interruptible(&gcm_user_lock);
 if (err) goto clear;
 key = gcm_user.recent;
 if (!gcm_user.keys[key].valid || crypto_memneq(gcm_user.keys[key].h, q.h, GHASH_BLOCK_SIZE)) key ^= 1;
 gcm_user.recent = key;
 if (!gcm_user.keys[key].valid || crypto_memneq(gcm_user.keys[key].h, q.h, GHASH_BLOCK_SIZE)) {
  ghash_preparekey(&gcm_user.keys[key].key, q.h);
  memcpy(gcm_user.keys[key].h, q.h, GHASH_BLOCK_SIZE);
  gcm_user.keys[key].valid = true;
 }

 sg2_own();
 if (poisoned) { err = -EIO; goto disown; }
 for (i = 0; i < q.count; i++) {
  const struct hwcrypto_gcm_batch_record *r = &b->r[i];
  u8 *in = cur->in + b->off[i];
  u32 n = 16 + ALIGN(r->length, 16);
  memset(in, 0, 16);
  if (copy_from_user(in + 16, u64_to_user_ptr(r->data), r->length)) { err = -EFAULT; goto clean; }
  memset(in + 16 + r->length, 0, n - 16 - r->length);
 }
 head = q.count >= 4 ? q.count / 4 : 0;
 if (head) {
  descs = head;
  err = gcm_batch_start(&k, b, 0, head, b->off[head]);
  if (!err) err = gcm_batch_finish(0, b->off[head]);
  if (err) goto disown;	/* engine state uncertain: leave its buffers alone */
 }
 descs = max(descs, q.count - head);
 err = gcm_batch_start(&k, b, head, q.count, total - b->off[head]);
 if (err) goto disown;
 /* While the engine has the rest: kernel memory only. The tags are kept in
  * the input buffer, where each record's zero block was. */
 for (i = 0; i < head; i++) gcm_batch_tag(b, i, &gcm_user.keys[key].key, cur->in + b->off[i]);
 err = gcm_batch_finish(b->off[head], total - b->off[head]);
 if (err) goto disown;
 for (i = head; i < q.count; i++) gcm_batch_tag(b, i, &gcm_user.keys[key].key, cur->in + b->off[i]);
 for (i = 0; i < q.count; i++) {
  const struct hwcrypto_gcm_batch_record *r = &b->r[i];
  if (copy_to_user(u64_to_user_ptr(r->data), cur->out + b->off[i] + 16, r->length) ||
      copy_to_user(u64_to_user_ptr(r->data) + r->length, cur->in + b->off[i], 16)) { err = -EFAULT; break; }
 }
clean:
 /* Every descriptor holds the key. */
 memzero_explicit(cur->chain, descs * DESC_BYTES);
 memzero_explicit(cur->in, total);
 memzero_explicit(cur->out, total);
disown:
 sg2_disown();
 mutex_unlock(&gcm_user_lock);
clear:
 kfree_sensitive(b);
 memzero_explicit(&q, sizeof(q)); memzero_explicit(&k, sizeof(k));
 return err;
}
