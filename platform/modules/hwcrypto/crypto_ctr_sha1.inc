/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * HWCRYPTO_CTR_SHA1: for each message of a batch, AES-128-CTR over a range of
 * it and then SHA-1 over the whole of it, in one start of the engine: a chain
 * of two descriptors. This is what SRTP's AES128_CM_HMAC_SHA1 does with every
 * packet (encrypt the payload, authenticate header and ciphertext).
 *
 * The first descriptor (control bits 0x9: valid, continue at the address in
 * words 2-3) encrypts from the input buffer into the output buffer, where the
 * bytes around the range already are; the second (0xf: valid, last, raise
 * the status) hashes the output buffer. The engine runs them one after the
 * other and reports once, with the hash completion bit. On an SG2002 a
 * 1216-byte message takes 8.6 us in the engine, against 4.4 us for the AES
 * alone and about 17 us for HMAC-SHA1 of it on the CPU.
 *
 * The engine does not survive a cipher that writes to an address which is
 * not a multiple of 8 (it never answers; +8 and +16 work, +4 does not), but a
 * hash may read from any word address. An RTP header is 12 bytes or more, so
 * the message lies in the output buffer shifted by up to 12 bytes
 * (ctr_sha1_shift()), which puts the encrypted range at a multiple of 16; its
 * offset in the message must be a whole number of words.
 *
 * A hash result is only in the engine's parameter registers, not in its
 * descriptor, so every message is a start of its own. Two things the engine
 * does not survive and this code never asks for: a cipher and a hash in one
 * descriptor, and a hash descriptor that continues the registers' state.
 *
 * While the engine works on a message the CPU would only wait, so it moves
 * the neighbours: the previous message's result out to the caller and the
 * next message in, each in a set of DMA memory of its own (sets[0] and
 * sets[2] in turn). The engine lock is held then, so these copies must not
 * sleep (copy_*_user_nofault); one that would have to wait for a page is done
 * again after the engine has finished.
 */
struct ctr_sha1_slot {
 struct sg2_set *s;
 struct hwcrypto_ctr_sha1_packet p;
 u32 used;	/* bytes of the buffers to wipe */
};

/* Where the message starts in the output buffer. */
static u32 ctr_sha1_shift(const struct hwcrypto_ctr_sha1_packet *p)
{
 return -p->crypt_offset & 15;
}

static int ctr_sha1_validate(const struct hwcrypto_ctr_sha1_packet *p)
{
 if (!p->data || p->reserved || p->reserved2) return -EINVAL;
 if (p->crypt_offset % 4) return -EINVAL;
 if (!p->length || p->length > HWCRYPTO_MAX || p->length % 64) return -EINVAL;
 if (!p->crypt_length || p->crypt_length % 16) return -EINVAL;
 if (p->crypt_offset > p->length || p->crypt_length > p->length - p->crypt_offset) return -EINVAL;
 return 0;
}

static bool ctr_sha1_from(void *to, const void __user *from, size_t n, bool atomic)
{
 return atomic ? !copy_from_user_nofault(to, from, n) : !copy_from_user(to, from, n);
}
static bool ctr_sha1_to(void __user *to, const void *from, size_t n, bool atomic)
{
 return atomic ? !copy_to_user_nofault(to, from, n) : !copy_to_user(to, from, n);
}

/* A message into a slot: the range to the input buffer, what is around it to
 * where the engine writes the ciphertext between. */
static int ctr_sha1_load(struct ctr_sha1_slot *t, const struct hwcrypto_ctr_sha1_packet __user *from, bool atomic)
{
 u8 __user *data;
 u8 *message;
 u32 after;
 int err;
 if (!ctr_sha1_from(&t->p, from, sizeof(t->p), atomic)) return -EFAULT;
 err = ctr_sha1_validate(&t->p); if (err) return err;
 data = u64_to_user_ptr(t->p.data);
 message = t->s->out + ctr_sha1_shift(&t->p);
 after = t->p.crypt_offset + t->p.crypt_length;
 t->used = max(t->used, t->p.length);
 if (!ctr_sha1_from(message, data, t->p.crypt_offset, atomic) ||
     !ctr_sha1_from(t->s->in, data + t->p.crypt_offset, t->p.crypt_length, atomic) ||
     !ctr_sha1_from(message + after, data + after, t->p.length - after, atomic)) return -EFAULT;
 return 0;
}

/* A slot's result to the caller: the ciphertext and the hash state. */
static bool ctr_sha1_store(const struct ctr_sha1_slot *t, struct hwcrypto_ctr_sha1_packet __user *to, bool atomic)
{
 u8 __user *data = u64_to_user_ptr(t->p.data);
 const u8 *message = t->s->out + ctr_sha1_shift(&t->p);
 return ctr_sha1_to(data + t->p.crypt_offset, message + t->p.crypt_offset, t->p.crypt_length, atomic) &&
  ctr_sha1_to(to->state, t->p.state, sizeof(t->p.state), atomic);
}

/* Start the chain of a loaded slot. On success the engine lock is held, with
 * bottom halves off, until ctr_sha1_finish(). */
static int ctr_sha1_start(const struct hwcrypto_ctr_sha1_request *r, struct ctr_sha1_slot *t)
{
 struct sg2_set *s = t->s;
 const struct hwcrypto_ctr_sha1_packet *p = &t->p;
 u32 shift = ctr_sha1_shift(p);
 dma_addr_t message = s->out_dma + shift;
 memset(s->d, 0, DESC_BYTES);
 s->d[0] = 0x9 | BIT(19) | BIT(9) | BIT(23);
 s->d[1] = BIT(5) | BIT(2) | BIT(0);
 s->d[2] = lower_32_bits(s->d2_dma); s->d[3] = upper_32_bits(s->d2_dma);
 s->d[4] = lower_32_bits(s->in_dma); s->d[5] = upper_32_bits(s->in_dma);
 s->d[6] = lower_32_bits(message + p->crypt_offset); s->d[7] = upper_32_bits(message + p->crypt_offset);
 s->d[8] = p->crypt_length;
 memcpy(&s->d[10], r->key, 16);
 memcpy(&s->d[18], p->iv, 16);
 memset(s->d2, 0, DESC_BYTES);
 s->d2[0] = 0xf | BIT(19) | BIT(12);
 s->d2[1] = 1;
 s->d2[4] = lower_32_bits(message); s->d2[5] = upper_32_bits(message);
 s->d2[8] = p->length;
 memcpy(&s->d2[10], r->state, 20);
 dma_sync_single_for_device(&pdev->dev, s->in_dma, p->crypt_length, DMA_BIDIRECTIONAL);
 dma_sync_single_for_device(&pdev->dev, s->out_dma, shift + p->length, DMA_BIDIRECTIONAL);
 dma_sync_single_for_device(&pdev->dev, s->d_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 dma_sync_single_for_device(&pdev->dev, s->d2_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 dma_wmb();
 spin_lock_bh(&engine_lock);
 if (poisoned) {
  spin_unlock_bh(&engine_lock);
  return -EIO;
 }
 writel(7, regs + MASK); writel(7, regs + STATUS);
 writel(lower_32_bits(s->d_dma), regs + DESC_LO); writel(upper_32_bits(s->d_dma), regs + DESC_HI);
 crypto_submitted[HWCRYPTO_ALG_AES]++; crypto_submitted[HWCRYPTO_ALG_SHA1]++;
 writel(DMA_CTRL_VALUE, regs + CTRL);
 return 0;
}

/* Wait for the chain and release the engine. */
static int ctr_sha1_finish(struct ctr_sha1_slot *t)
{
 struct sg2_set *s = t->s;
 u32 status = 0, i;
 int err = engine_wait(&status);
 /* The chain ends with the hash: its completion bit alone. */
 if (err || status != 2) {
  sg2_poison();
  spin_unlock_bh(&engine_lock);
  pr_err("hwcrypto_crypto: AES-CTR+SHA-1 chain DMA uncertain status=%#x timeout=%d; pinned until reboot\n", status, err);
  return err ? err : -EIO;
 }
 dma_rmb();
 /* The engine wrote the ciphertext only: the descriptors and the input
  * buffer were flushed before the start and are still what the CPU wrote. */
 dma_sync_single_for_cpu(&pdev->dev, s->out_dma, ctr_sha1_shift(&t->p) + t->p.length, DMA_BIDIRECTIONAL);
 crypto_completed[HWCRYPTO_ALG_AES]++; crypto_completed[HWCRYPTO_ALG_SHA1]++;
 for (i = 0; i < 5; i++)
  ((__le32 *)t->p.state)[i] = cpu_to_le32(readl(regs + 0x1c0 + i * 4));
 writel(7, regs + STATUS); writel(initial_mask, regs + MASK);
 spin_unlock_bh(&engine_lock);
 return 0;
}

static long crypto_ctr_sha1_ioctl(unsigned long arg)
{
 struct hwcrypto_ctr_sha1_request r;
 struct hwcrypto_ctr_sha1_packet __user *packets;
 struct ctr_sha1_slot slot[2] = { { .s = &sets[0] }, { .s = &sets[2] } };
 /* loaded: message i is already in its slot. pending: message i - 1 is
  * complete in the other slot and not yet with the caller. */
 bool loaded = false, pending = false;
 u32 i = 0, done = 0;
 int err;
 if (copy_from_user(&r, (void __user *)arg, sizeof(r))) { err = -EFAULT; goto clear; }
 if (r.version != HWCRYPTO_ABI || !r.count || r.count > HWCRYPTO_CTR_SHA1_MAX || r.reserved ||
     !r.packets || memchr_inv(r.reserved2, 0, sizeof(r.reserved2))) { err = -EINVAL; goto clear; }
 packets = u64_to_user_ptr(r.packets);
 err = mutex_lock_interruptible(&lock); if (err) goto clear;
 for (; i < r.count; i++) {
  struct ctr_sha1_slot *t = &slot[i & 1], *other = &slot[~i & 1];
  if (poisoned) { err = -EIO; break; }
  if (!loaded) { err = ctr_sha1_load(t, &packets[i], false); if (err) break; }
  loaded = false;
  err = ctr_sha1_start(&r, t); if (err) break;
  /* The engine is at work: the previous result out, the next message in,
   * as far as that needs no page. */
  if (pending && ctr_sha1_store(other, &packets[i - 1], true)) { pending = false; done = i; }
  if (!pending && i + 1 < r.count) loaded = !ctr_sha1_load(other, &packets[i + 1], true);
  err = ctr_sha1_finish(t);
  if (pending) {
   pending = false;
   if (ctr_sha1_store(other, &packets[i - 1], false)) done = i;
   else if (!err) err = -EFAULT;
  }
  if (err) break;
  pending = true;
  sg2_resched();
 }
 if (pending) {
  if (ctr_sha1_store(&slot[(i - 1) & 1], &packets[i - 1], false)) done = i;
  else if (!err) err = -EFAULT;
 }
 /* An engine that did not answer may still own the memory: leave it. */
 if (!poisoned) {
  for (i = 0; i < 2; i++) {
   memzero_explicit(slot[i].s->in, slot[i].used);
   memzero_explicit(slot[i].s->out, slot[i].used ? slot[i].used + 15 : 0);
   memzero_explicit(slot[i].s->d, DESC_BYTES);
   memzero_explicit(slot[i].s->d2, DESC_BYTES);
  }
 }
 mutex_unlock(&lock);
 /* A batch that stopped after its first message succeeds with the count of
  * completed messages; those after it are untouched. */
 if (done && put_user(done, &((struct hwcrypto_ctr_sha1_request __user *)arg)->count)) err = -EFAULT;
 else if (done) err = 0;
clear:
 memzero_explicit(&r, sizeof(r));
 memzero_explicit(slot, sizeof(slot));
 return err;
}
