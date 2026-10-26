/* SPDX-License-Identifier: GPL-2.0-only */
/* Included by the existing serialized AES owner, using its DMA resources and
 * failure latch. SHA/cipher fields follow SG2002 TRM 22.1. SM3 follows
 * Sophgo linux_5.10 sg200x-dev, commit 6ef521238996 (2025-06-23):
 * descriptor bit14 and result registers 0x1e0 supersede the older TRM.
 */
#include "sg2002_crypto.h"
#include <linux/unaligned.h>
static_assert(sizeof(struct sg2002_crypto_request) == 144);
static_assert(sizeof(struct sg2002_crypto_info) == 160);
static_assert(sizeof(struct sg2002_crypto_info2) == 176);
/* The vendor SM3 protocol timed out on the qualification SG2002. Keep it
 * opt-in until its hardware completion and chaining have been established.
 */
static bool experimental_sm3;
module_param(experimental_sm3, bool, 0444);
MODULE_PARM_DESC(experimental_sm3, "Enable unqualified SM3 DMA protocol (may require reboot)");
static u8 *crypto_output;
static dma_addr_t crypto_output_dma;
static u64 crypto_submitted[SG2002_ALG_COUNT], crypto_completed[SG2002_ALG_COUNT];

static int crypto_alloc_output(void)
{
 crypto_output = kzalloc(SG2002_CRYPTO_OUT_MAX, GFP_KERNEL);
 if (!crypto_output) return -ENOMEM;
 crypto_output_dma = dma_map_single(&pdev->dev, crypto_output,
  SG2002_CRYPTO_OUT_MAX, DMA_BIDIRECTIONAL);
 if (dma_mapping_error(&pdev->dev, crypto_output_dma)) {
  kfree_sensitive(crypto_output); crypto_output = NULL; return -EIO;
 }
 dma_sync_single_for_cpu(&pdev->dev, crypto_output_dma,
  SG2002_CRYPTO_OUT_MAX, DMA_BIDIRECTIONAL);
 return 0;
}
static void crypto_free_output(void)
{
 if (!crypto_output) return;
 memzero_explicit(crypto_output, SG2002_CRYPTO_OUT_MAX);
 dma_unmap_single(&pdev->dev, crypto_output_dma,
  SG2002_CRYPTO_OUT_MAX, DMA_BIDIRECTIONAL);
 kfree_sensitive(crypto_output); crypto_output = NULL;
}

static bool crypto_is_hash(u32 alg)
{
 return alg == SG2002_ALG_SHA1 || alg == SG2002_ALG_SHA256 || alg == SG2002_ALG_SM3;
}
static bool crypto_b64_char(u8 c)
{
 return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
  (c >= '0' && c <= '9') || c == '+' || c == '/';
}

static int crypto_validate(struct sg2002_crypto_request *r)
{
 u32 block = 16;
 if (r->version != SG2002_CRYPTO_ABI || r->algorithm < 1 ||
     r->algorithm >= SG2002_ALG_COUNT || r->encrypt > 1 ||
     memchr_inv(r->reserved, 0, sizeof(r->reserved))) return -EINVAL;
 if (r->algorithm == SG2002_ALG_SM3 && !experimental_sm3) return -EOPNOTSUPP;
 if (!r->length || r->length > SG2002_CRYPTO_MAX || !r->input) return -EINVAL;
 if (r->output_length > SG2002_CRYPTO_OUT_MAX) return -EINVAL;
 if (crypto_is_hash(r->algorithm)) {
  if (r->length % 64 || r->key_length || r->mode || r->encrypt ||
      r->output_length) return -EINVAL;
  return 0;
 }
 if (!r->output) return -EINVAL;
 if (r->algorithm == SG2002_ALG_BASE64) {
  u32 needed = r->encrypt ? DIV_ROUND_UP(r->length, 3) * 4 : r->length / 4 * 3;
  if (r->key_length || r->mode || (!r->encrypt && r->length % 4)) return -EINVAL;
  if (r->output_length < needed) return -EMSGSIZE;
  return 0;
 }
 if (r->mode > SG2002_MODE_CTR || r->output_length < r->length) return -EINVAL;
 switch (r->algorithm) {
 case SG2002_ALG_AES:
  if (r->key_length != 16 && r->key_length != 24 && r->key_length != 32) return -EINVAL;
  break;
 case SG2002_ALG_DES:
  block = 8; if (r->key_length != 8) return -EINVAL; break;
 case SG2002_ALG_TDES:
  block = 8; if (r->key_length != 16 && r->key_length != 24) return -EINVAL; break;
 case SG2002_ALG_SM4:
  if (r->key_length != 16) return -EINVAL;
  break;
 default: return -EINVAL;
 }
 if (r->mode != SG2002_MODE_CTR && r->length % block) return -EINVAL;
 return 0;
}

static int crypto_prepare_descriptor(struct sg2002_crypto_request *r,
 u32 *input_size, u32 *output_size, u32 *result_size)
{
 bool hash = crypto_is_hash(r->algorithm);
 u32 block = (r->algorithm == SG2002_ALG_DES || r->algorithm == SG2002_ALG_TDES) ? 8 : 16;
 u32 i, pad = 0;
 *input_size = (hash || r->algorithm == SG2002_ALG_BASE64) ? r->length : ALIGN(r->length, block);
 *output_size = 0;
 *result_size = hash ? 0 : r->length;
 memset(buffer + r->length, 0, *input_size - r->length);
 memset(desc, 0, DESC_BYTES);
 desc[0] = 0xf;
 desc[4] = lower_32_bits(buffer_dma); desc[5] = upper_32_bits(buffer_dma);
 desc[8] = *input_size;
 if (r->algorithm == SG2002_ALG_SM3) {
  /* Unlike SHA, SM3's descriptor carries native little-endian words.
   * Keep the userspace chaining state in canonical big-endian order.
   * Give the vendor's destination field a separate bounded output mapping.
   */
  desc[0] |= BIT(19) | BIT(23) | BIT(14);
  desc[1] = 1;
  for (i = 0; i < 8; i++) desc[10+i] = get_unaligned_be32(r->state + i*4);
  desc[6] = lower_32_bits(crypto_output_dma);
  desc[7] = upper_32_bits(crypto_output_dma);
  *output_size = *input_size;
  return 0;
 }
 if (hash) {
  desc[0] |= BIT(19) | BIT(12);
  desc[1] = r->algorithm == SG2002_ALG_SHA256 ? 3 : 1;
  memcpy(&desc[10], r->state, r->algorithm == SG2002_ALG_SHA256 ? 32 : 20);
  return 0;
 }
 if (r->algorithm == SG2002_ALG_BASE64) {
  if (!r->encrypt) {
   if (buffer[r->length-1] == '=') pad++;
   if (r->length >= 2 && buffer[r->length-2] == '=') pad++;
   for (i = 0; i < r->length-pad; i++) if (!crypto_b64_char(buffer[i])) return -EINVAL;
  }
  *output_size = r->encrypt ? DIV_ROUND_UP(r->length, 3) * 4 : r->length / 4 * 3;
  *result_size = *output_size - pad;
  desc[0] |= BIT(13); desc[1] = r->encrypt;
  desc[6] = lower_32_bits(crypto_output_dma); desc[7] = upper_32_bits(crypto_output_dma);
  desc[10] = *output_size;
  memset(crypto_output, 0, *output_size);
  return 0;
 }
 desc[0] |= BIT(19);
 /* Ciphers write to a separate buffer. In-place requests (source ==
  * destination, as in the vendor driver) hung the SoC within minutes while
  * the H.265 encoder was active; the same requests out of place did not. */
 desc[6] = lower_32_bits(crypto_output_dma); desc[7] = upper_32_bits(crypto_output_dma);
 *output_size = *input_size;
 memcpy(&desc[10], r->key, r->key_length);
 switch (r->algorithm) {
 case SG2002_ALG_AES:
  desc[0] |= BIT(9);
  desc[1] = r->key_length == 16 ? BIT(5) : r->key_length == 24 ? BIT(4) : BIT(3);
  break;
 case SG2002_ALG_SM4: desc[0] |= BIT(11); desc[1] = BIT(5); break;
 case SG2002_ALG_DES: desc[0] |= BIT(10); break;
 case SG2002_ALG_TDES:
  desc[0] |= BIT(10); desc[1] = BIT(3);
  if (r->key_length == 16) memcpy((u8 *)&desc[10] + 16, r->key, 8);
  break;
 }
 if (r->mode != SG2002_MODE_ECB) {
  desc[0] |= BIT(23);
  desc[1] |= r->mode == SG2002_MODE_CBC ? BIT(1) : BIT(2);
  memcpy(&desc[18], r->iv, block);
 }
 // CTR uses the encryption primitive in both directions.
 if (r->encrypt || r->mode == SG2002_MODE_CTR) desc[1] |= BIT(0);
 return 0;
}

/* Sizes of a transaction between crypto_start_locked() and crypto_finish_locked(). */
struct crypto_sizes { u32 input, output, result; };

/* Start one bounded DMA transaction. Caller owns lock and CPU-owned buffer;
 * until crypto_finish_locked() the engine owns buffer, crypto_output and desc,
 * and the caller may only do work that touches none of them.
 * No userspace pointers are dereferenced here; Crypto API and ioctl share this.
 */
static int crypto_start_locked(struct sg2002_crypto_request *r, struct crypto_sizes *s)
{
 int err;
 if (poisoned) return -EIO;
 err = crypto_prepare_descriptor(r, &s->input, &s->output, &s->result);
 if (err) return err;
 dma_sync_single_for_device(&pdev->dev, buffer_dma, s->input, DMA_BIDIRECTIONAL);
 if (s->output) dma_sync_single_for_device(&pdev->dev, crypto_output_dma, s->output, DMA_BIDIRECTIONAL);
 dma_sync_single_for_device(&pdev->dev, desc_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 dma_wmb();
 writel(7, regs + MASK); writel(7, regs + STATUS);
 writel(lower_32_bits(desc_dma), regs + DESC_LO); writel(upper_32_bits(desc_dma), regs + DESC_HI);
 crypto_submitted[r->algorithm]++;
 writel(DMA_CTRL_VALUE, regs + CTRL);
 return 0;
}

/* Wait for the transaction started by crypto_start_locked() and take the
 * buffers back for the CPU. */
static int crypto_finish_locked(struct sg2002_crypto_request *r, const struct crypto_sizes *s)
{
 u32 input_size = s->input, output_size = s->output, result_size = s->result, status = 0, i;
 int err;
 err = engine_wait(&status);
 r->status = status;
 // Completion: cipher bit0, SHA bit1, SM3 bit2. Never accept mixed status.
 if (err || status != (r->algorithm == SG2002_ALG_SM3 ? 4 : crypto_is_hash(r->algorithm) ? 2 : 1)) {
  poisoned = true; __module_get(THIS_MODULE);
  pr_err("sg2002_crypto: algorithm=%u DMA uncertain status=%#x timeout=%d; pinned until reboot\n", r->algorithm, status, err);
  err = err ? err : -EIO;
  // Engine ownership is uncertain: retain every mapping and its memory.
  return err;
 }
 dma_rmb();
 dma_sync_single_for_cpu(&pdev->dev, desc_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 dma_sync_single_for_cpu(&pdev->dev, buffer_dma, input_size, DMA_BIDIRECTIONAL);
 if (output_size) dma_sync_single_for_cpu(&pdev->dev, crypto_output_dma, output_size, DMA_BIDIRECTIONAL);
 crypto_completed[r->algorithm]++;
 if (r->algorithm == SG2002_ALG_SM3) {
  for (i = 0; i < 8; i++)
   put_unaligned_be32(readl(regs + 0x1e0 + i*4), r->state + i*4);
 } else if (crypto_is_hash(r->algorithm)) {
  for (i = 0; i < (r->algorithm == SG2002_ALG_SHA256 ? 8 : 5); i++)
   ((__le32 *)r->state)[i] = cpu_to_le32(readl(regs + 0x1c0 + i*4));
 }
 r->output_length = result_size;
 writel(7, regs + STATUS); writel(initial_mask, regs + MASK);
 return 0;
}

/* One bounded DMA transaction. Caller owns lock and CPU-owned buffer. */
static int crypto_run_locked(struct sg2002_crypto_request *r)
{
 struct crypto_sizes s = {};
 int err = crypto_start_locked(r, &s);
 return err ? err : crypto_finish_locked(r, &s);
}
static void crypto_clean_locked(const struct sg2002_crypto_request *r)
{
 /* Never touch a mapping whose DMA ownership is uncertain. */
 if (poisoned) return;
 memzero_explicit(buffer, ALIGN(r->length, 16));
 if (r->algorithm == SG2002_ALG_BASE64)
  memzero_explicit(crypto_output, r->encrypt ? DIV_ROUND_UP(r->length, 3)*4 : r->length/4*3);
 else if (!crypto_is_hash(r->algorithm) || r->algorithm == SG2002_ALG_SM3)
  memzero_explicit(crypto_output, ALIGN(r->length, 16));
 memzero_explicit(desc, DESC_BYTES);
}
static long crypto_ioctl(unsigned int cmd, unsigned long arg)
{
 struct sg2002_crypto_request r = {};
 int err;
 if (cmd == SG2002_CRYPTO_INFO || cmd == SG2002_CRYPTO_INFO2) {
  struct sg2002_crypto_info2 info = { .legacy = { .version = SG2002_CRYPTO_ABI,
   .algorithms = GENMASK(SG2002_ALG_COUNT-1, 1),
   .max_input = SG2002_CRYPTO_MAX, .max_output = SG2002_CRYPTO_OUT_MAX } };
  size_t size = cmd == SG2002_CRYPTO_INFO ? sizeof(info.legacy) : sizeof(info);
  if (!experimental_sm3) info.legacy.algorithms &= ~BIT(SG2002_ALG_SM3);
  err = mutex_lock_interruptible(&lock); if (err) return err;
  info.legacy.poisoned = poisoned;
  memcpy(info.legacy.submitted, crypto_submitted, sizeof(info.legacy.submitted));
  memcpy(info.legacy.completed, crypto_completed, sizeof(info.legacy.completed));
  info.sm3_submitted = crypto_submitted[SG2002_ALG_SM3];
  info.sm3_completed = crypto_completed[SG2002_ALG_SM3];
  mutex_unlock(&lock);
  return copy_to_user((void __user *)arg, &info, size) ? -EFAULT : 0;
 }
 if (cmd != SG2002_CRYPTO_RUN) return -ENOTTY;
 if (copy_from_user(&r, (void __user *)arg, sizeof(r))) { err = -EFAULT; goto clear; }
 err = crypto_validate(&r); if (err) goto clear;
 err = mutex_lock_interruptible(&lock); if (err) goto clear;
 if (poisoned) { err = -EIO; goto unlock; }
 if (copy_from_user(buffer, u64_to_user_ptr(r.input), r.length)) { err = -EFAULT; goto clean; }
 err = crypto_run_locked(&r);
 if (err) goto clean;
 if (!crypto_is_hash(r.algorithm) && copy_to_user(u64_to_user_ptr(r.output),
     crypto_output, r.output_length)) err = -EFAULT;
 if (!err && copy_to_user((void __user *)arg, &r, sizeof(r))) err = -EFAULT;
clean:
 crypto_clean_locked(&r);
unlock:
 mutex_unlock(&lock);
clear:
 memzero_explicit(&r, sizeof(r));
 return err;
}
