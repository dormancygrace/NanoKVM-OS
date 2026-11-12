// SPDX-License-Identifier: GPL-2.0-only
/* Diagnostic SG2002 AES-CTR DMA backend. Not a production Crypto API driver.
 * Descriptor layout: Sophgo linux_5.10 382af279, cvitek-spacc-regs.h.
 * No user-provided DMA addresses; exclusive root-only synchronous operations.
 */
#include <linux/module.h>
#include <linux/platform_device.h>
#include <linux/dma-mapping.h>
#include <linux/dma-map-ops.h>
#include <linux/io.h>
#include <linux/ktime.h>
#include <linux/miscdevice.h>
#include <linux/mutex.h>
#include <linux/spinlock.h>
#include <linux/preempt.h>
#include <linux/uaccess.h>
#include <linux/slab.h>
#include <linux/of.h>
#include "nanokvm_hwcrypto.h"
#include "hwcrypto.h"

#define BASE 0x02060000
#define CTRL 0x00
#define MASK 0x04
#define DESC_LO 0x08
#define DESC_HI 0x0c
#define STATUS 0x10
/*
 * DMA_CTRL: write max burst (31:24), read max burst (23:16), descriptor mode,
 * enable. The vendor Linux driver writes bursts of 6 (its secure boot uses
 * 16). Concurrent HDMI capture, scaling and H.265 encoding with CryptoDMA
 * writes still hung the SoC with out-of-place requests: mean time to hang
 * about 48 s with bursts of 16 and 78 s with 6, at 1250 requests of 1216
 * bytes per second and 1920x1080@100. Bursts of 4 ran without a hang there
 * (15 min) and with 3840x2160 H.265 over WebRTC with hardware SRTP (13 min),
 * for 2.6 % fewer 1216-byte requests per second. Reads (hashes) never hung.
 *
 * Bursts of 4 hung too once the load grew: 2560x1440@60 H.265 over WebRTC
 * with every packet on the engine (2100 a second) beside an MJPEG viewer
 * (scaler and JPEG encoder working as well) stopped the SoC twice in about
 * 2.5 hours, without a word in the log, until the watchdog. Bursts of 2 ran
 * that load plus 2800 4 KiB AES requests a second for 2 h 56 min, and those
 * requests alone at 26800 a second (110 MB/s) for 2.5 hours more. The stream
 * rates and the busy share did not change against bursts of 4.
 */
#define DMA_CTRL_VALUE ((2 << 24) | (16 << 16) | 3)
#define DESC_BYTES 128

static struct platform_device *pdev;
static void __iomem *regs;
/*
 * Two sets of DMA memory (descriptor, input, output). Set 0 serves callers
 * that may sleep, serialised by `lock`: ioctls copy to and from userspace
 * while they own it. Set 1 serves callers in atomic context, such as ovpn
 * encrypting a packet in softirq: they hold engine_lock for the whole request
 * (sg2_own()). The engine registers are owned under engine_lock with bottom
 * halves off: a sleeper takes it for each transaction only, from
 * crypto_start_locked() to crypto_finish_locked(), so a softirq never finds
 * the engine busy. `cur` is the set of the present owner; an atomic owner
 * restores it before it lets the interrupted sleeper continue. Set 2 is a
 * second one for sleepers, also under `lock`: HWCRYPTO_CTR_SHA1 fills it
 * while the engine works on set 0, and the other way round.
 */
struct sg2_direct;
struct sg2_set {
 u32 *d, *d2;	/* d2: the second descriptor of a chain (HWCRYPTO_CTR_SHA1) */
 u8 *in, *out;
 u32 in_size, out_size;
 u8 *blk; dma_addr_t blk_dma;	/* small sets: the block in and out are in */
 dma_addr_t d_dma, d2_dma, in_dma, out_dma;
 /* Direct access (crypto_direct.inc): the descriptors of a chain and its plan. */
 u32 *chain;
 dma_addr_t chain_dma;
 struct sg2_direct *plan;
};
static struct sg2_set sets[3], *cur = &sets[0];
/*
 * A request of a caller that may sleep is cut into pieces of SG2_LARGE bytes,
 * the room of set 0; one in atomic context into pieces of HWCRYPTO_MAX, the
 * room of set 1. The engine takes a descriptor of a megabyte at the rate of
 * 4 KiB (AES 333 MB/s, SHA-256 367 MB/s), and each piece costs 15 to 44 us
 * around its transaction (the copies, the cache maintenance, the locks): at
 * 4 KiB that was two thirds of the time of a megabyte of AES-CTR. A piece of
 * 64 KiB keeps the engine, with bottom halves off, for 200 us; SRTP's
 * HWCRYPTO_CTR_SHA1 waits for `lock` that long plus the copies of the piece.
 * GCM keeps HWCRYPTO_MAX in either context: the CPU computes GHASH while the
 * engine holds the piece, and for 64 KiB that is over a millisecond with
 * bottom halves off.
 */
#define SG2_LARGE 65536
static unsigned int sg2_room(void) { return preemptible() ? SG2_LARGE : HWCRYPTO_MAX; }
static DEFINE_MUTEX(lock);
static DEFINE_SPINLOCK(engine_lock);

/* Own a set of DMA memory: by `lock` in a preemptible context, else the atomic
 * set together with the engine. (A caller that may not sleep is not
 * preemptible here: with TINY_RCU even rcu_read_lock() disables preemption.) */
static void sg2_own(void)
{
 if (preemptible()) {
  mutex_lock(&lock);
 } else {
  spin_lock_bh(&engine_lock);
  cur = &sets[1];
 }
}
static void sg2_disown(void)
{
 if (cur == &sets[1]) {
  cur = &sets[0];
  spin_unlock_bh(&engine_lock);
 } else {
  mutex_unlock(&lock);
 }
}
static void sg2_resched(void)
{
 if (preemptible()) cond_resched();
}
/* The engine for one transaction of a sleeper; an atomic owner has it already. */
static void sg2_engine_lock(void) { if (cur == &sets[0]) spin_lock_bh(&engine_lock); }
static void sg2_engine_unlock(void) { if (cur == &sets[0]) spin_unlock_bh(&engine_lock); }
static bool poisoned;
static u32 initial_mask;

/* Keep the public fixed-size ioctl ABI. The header is stack scratch and the
 * submitted payload is copied directly into the existing locked DMA buffer.
 */
struct aes_scratch {
 u32 length, reserved;
 u8 key[16], iv[16];
 u32 status;
 u8 data[];
};
static_assert(sizeof(struct aes_scratch) == offsetof(struct hwcrypto_aes_request, data));
static_assert(offsetof(struct aes_scratch, key) == offsetof(struct hwcrypto_aes_request, key));
static_assert(offsetof(struct aes_scratch, iv) == offsetof(struct hwcrypto_aes_request, iv));
static_assert(offsetof(struct aes_scratch, status) == offsetof(struct hwcrypto_aes_request, status));

/* The set the engine may still read or write after the poison latch. */
static struct sg2_set *lost;

/*
 * The engine did not complete: never use it again. Called with engine_lock
 * held, so that no request starts on it in between; crypto_start_locked() and
 * the ioctl test the latch again under that lock. The module stays loaded,
 * and the set the engine was given is never touched or freed again; the other
 * set is cleaned as usual (crypto_clean_locked()).
 */
static void sg2_poison(void)
{
 if (poisoned) return;
 poisoned = true;
 lost = cur;
 __module_get(THIS_MODULE);
}

/*
 * Wait for the completion status of the engine, spinning: the caller holds
 * engine_lock. A 4 KiB request takes about 35 us; with the 1 us sleep of
 * readl_poll_timeout() a loop of 1216-byte ioctls took 46.6 us each, 31 us of
 * it in the poll, and 19.0 us spinning. An engine that has not answered after
 * ENGINE_TIMEOUT_US is given up (the caller sets the poison latch). The clock
 * is read every 64th poll.
 */
#define ENGINE_TIMEOUT_US 20000
static int engine_wait(u32 *status)
{
 ktime_t deadline = ktime_add_us(ktime_get(), ENGINE_TIMEOUT_US);
 unsigned int polls = 0;
 for (;;) {
  *status = readl(regs + STATUS);
  if (*status) return 0;
  if (!(++polls & 63) && ktime_after(ktime_get(), deadline)) {
   *status = readl(regs + STATUS);
   return *status ? 0 : -ETIMEDOUT;
  }
  cpu_relax();
 }
}

#include "crypto_algorithms.inc"
#include "crypto_linux_api.inc"
#include "crypto_gcm.inc"
#include "crypto_ccm.inc"
#include "crypto_cmac.inc"
#include "crypto_ctr_sha1.inc"

static long aes_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
 struct aes_scratch header = {};
 u32 status = 0, size = 0;
 int err;
 if (cmd == HWCRYPTO_GCM) return crypto_gcm_ioctl(arg);
 if (cmd == HWCRYPTO_CTR_SHA1) return crypto_ctr_sha1_ioctl(arg);
 if (cmd != HWCRYPTO_AES_CTR) return crypto_ioctl(cmd, arg);
 /* Snapshot the fixed header once. The payload copy below uses this length and
  * never rereads userspace metadata, even if the caller races with the ioctl. */
 if (copy_from_user(&header, (void __user *)arg, sizeof(header))) {
  err = -EFAULT; goto clear_header;
 }
 if (!header.length || header.length > HWCRYPTO_AES_MAX || header.reserved) {
  err = -EINVAL; goto clear_header;
 }
 err = mutex_lock_interruptible(&lock);
 if (err) goto clear_header;
 if (poisoned) { err = -EIO; goto unlock; }
 size = ALIGN(header.length, 16);
 /* The shared DMA buffer is protected by lock. Keeping the user copy inside
  * the lock removes the per-request allocation and the two intermediate copies. */
 if (copy_from_user(cur->in,
      (u8 __user *)arg + offsetof(struct hwcrypto_aes_request, data),
      header.length)) {
  err = -EFAULT;
  goto clear_input;
 }
 memset(cur->in + header.length, 0, size - header.length);
 memset(cur->d, 0, DESC_BYTES);
 cur->d[0] = BIT(23) | BIT(19) | BIT(9) | 0xf;
 cur->d[1] = BIT(5) | BIT(2) | BIT(0); /* AES128,CTR,encrypt */
 cur->d[4] = lower_32_bits(cur->in_dma);
 cur->d[5] = upper_32_bits(cur->in_dma);
 /* Never in place: see crypto_prepare_descriptor(). */
 cur->d[6] = lower_32_bits(cur->out_dma);
 cur->d[7] = upper_32_bits(cur->out_dma);
 cur->d[8] = size;
 memcpy(&cur->d[10], header.key, 16);
 memcpy(&cur->d[18], header.iv, 16);
 dma_sync_single_for_device(&pdev->dev, cur->in_dma, size, DMA_BIDIRECTIONAL);
 dma_sync_single_for_device(&pdev->dev, cur->out_dma, size, DMA_BIDIRECTIONAL);
 dma_sync_single_for_device(&pdev->dev, cur->d_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 dma_wmb();
 /* Match vendor interrupt/status enable. The first MASK=0 experiment
  * timed out; this change tests that difference without assuming its cause. */
 sg2_engine_lock();
 if (poisoned) {	/* latched by an atomic request since the test above */
  sg2_engine_unlock();
  err = -EIO;
  if (cur != lost) goto clear_input;
  goto unlock;
 }
 writel(7, regs + MASK);
 writel(7, regs + STATUS);
 writel(lower_32_bits(cur->d_dma), regs + DESC_LO);
 writel(upper_32_bits(cur->d_dma), regs + DESC_HI);
 writel(DMA_CTRL_VALUE, regs + CTRL);
 err = engine_wait(&status);
 if (!err && status == 1) {
  writel(7, regs + STATUS);
  writel(initial_mask, regs + MASK);
 } else {
  /* Completion/error bits beyond bit0 are not assumed to prove DMA idle.
   * Retain mappings, buffers and module code until reset on any uncertainty. */
  sg2_poison();
 }
 sg2_engine_unlock();
 if (err || status != 1) {
  pr_err("nanokvm_hwcrypto: DMA uncertain status=%#x timeout=%d; pinned until reboot\n", status, err);
  err = err ? err : -EIO;
  goto unlock;
 }
 dma_rmb();
 dma_sync_single_for_cpu(&pdev->dev, cur->d_dma, DESC_BYTES, DMA_BIDIRECTIONAL);
 dma_sync_single_for_cpu(&pdev->dev, cur->in_dma, size, DMA_BIDIRECTIONAL);
 dma_sync_single_for_cpu(&pdev->dev, cur->out_dma, size, DMA_BIDIRECTIONAL);
 header.status = status;
 /* Preserve the old contiguous copy_to_user ordering: return the header first,
  * then the ciphertext. A failed copy still runs the normal scratch cleanup. */
 if (copy_to_user((void __user *)arg, &header, sizeof(header))) {
  err = -EFAULT;
  goto clear_scratch;
 }
 if (copy_to_user((u8 __user *)arg + offsetof(struct hwcrypto_aes_request, data),
      cur->out, header.length)) {
  err = -EFAULT;
  goto clear_scratch;
 }
 err = 0;
clear_scratch:
 memzero_explicit(cur->in, size);
 memzero_explicit(cur->out, size);
 memzero_explicit(cur->d, DESC_BYTES);
 goto unlock;
clear_input:
 memzero_explicit(cur->in, size);
 memzero_explicit(cur->d, DESC_BYTES);
unlock:
 mutex_unlock(&lock);
clear_header:
 memzero_explicit(&header, sizeof(header));
 return err;
}

/* Input (HWCRYPTO_MAX) and output (HWCRYPTO_OUT_MAX) of a small set, see sg2_set_alloc(). */
#define SG2_SMALL_BLOCK 16384
/* One zeroed, CPU-owned block of DMA memory; NULL on failure. */
static void *sg2_dma_alloc(size_t size, dma_addr_t *dma)
{
 void *p = kzalloc(size, GFP_KERNEL);
 if (!p) return NULL;
 *dma = dma_map_single(&pdev->dev, p, size, DMA_BIDIRECTIONAL);
 if (dma_mapping_error(&pdev->dev, *dma)) { kfree(p); return NULL; }
 dma_sync_single_for_cpu(&pdev->dev, *dma, size, DMA_BIDIRECTIONAL);
 return p;
}
static void sg2_dma_free(void *p, size_t size, dma_addr_t dma)
{
 if (!p) return;
 memzero_explicit(p, size);
 dma_unmap_single(&pdev->dev, dma, size, DMA_BIDIRECTIONAL);
 kfree_sensitive(p);
}
static void sg2_set_free(struct sg2_set *s)
{
 kfree(s->plan);
 sg2_dma_free(s->chain, SG2_CHAIN_MAX * DESC_BYTES, s->chain_dma);
 if (s->blk) {
  sg2_dma_free(s->blk, SG2_SMALL_BLOCK, s->blk_dma);
 } else {
  sg2_dma_free(s->out, s->out_size, s->out_dma);
  sg2_dma_free(s->in, s->in_size, s->in_dma);
 }
 sg2_dma_free(s->d2, DESC_BYTES, s->d2_dma);
 sg2_dma_free(s->d, DESC_BYTES, s->d_dma);
 memset(s, 0, sizeof(*s));
}
/* On failure the caller frees the set. */
static int sg2_set_alloc(struct sg2_set *s)
{
 /* Set 0 is the sleepers' (sg2_room()); the others hold one HWCRYPTO_MAX request. */
 s->in_size = s == &sets[0] ? SG2_LARGE : HWCRYPTO_MAX;
 s->out_size = s == &sets[0] ? SG2_LARGE : HWCRYPTO_OUT_MAX;
 s->d = sg2_dma_alloc(DESC_BYTES, &s->d_dma);
 s->d2 = sg2_dma_alloc(DESC_BYTES, &s->d2_dma);
 if (s == &sets[0]) {
  s->in = sg2_dma_alloc(s->in_size, &s->in_dma);
  s->out = sg2_dma_alloc(s->out_size, &s->out_dma);
 } else {
  /*
   * One block of 16 KiB, a way of the L1 data cache: the output in its
   * second and third quarter, the input in its last. Where a buffer
   * falls in a way is which sets of the cache it uses, and input, output
   * and the caller's buffer are all hot during a request. As two
   * allocations the output came out a different quarter on every other
   * load of the module, the input always the last. Measured from an
   * atomic caller whose buffer starts a way (the first quarter), gcm,
   * AES-256, encrypt / decrypt us: output in the third quarter 1 KiB
   * 24.3 / 20.6, 2 KiB 39.8 / 30.5, 4 KiB 70.5 / 52.1; output in the
   * first quarter, with the caller's buffer, 28.4 / 23.1, 46.7 / 35.4,
   * 79.6 / 57.8. With the offsets set by hand, 2 KiB: all three in
   * different quarters 39.2 / 32.1, input with the caller's buffer
   * 42.2 / 34.7, all three together 47.0 / 37.5. A caller's buffer may be
   * anywhere, so one of the two can still share its sets; this layout
   * keeps them off each other and no longer changes from load to load.
   */
  s->blk = sg2_dma_alloc(SG2_SMALL_BLOCK, &s->blk_dma);
  if (s->blk) {
   s->out = s->blk + SG2_SMALL_BLOCK / 4; s->out_dma = s->blk_dma + SG2_SMALL_BLOCK / 4;
   s->in = s->blk + 3 * SG2_SMALL_BLOCK / 4; s->in_dma = s->blk_dma + 3 * SG2_SMALL_BLOCK / 4;
  }
 }
 s->chain = sg2_dma_alloc(SG2_CHAIN_MAX * DESC_BYTES, &s->chain_dma);
 s->plan = kzalloc(sizeof(*s->plan), GFP_KERNEL);
 return s->d && s->d2 && s->in && s->out && s->chain && s->plan ? 0 : -ENOMEM;
}

static const struct file_operations ops = {
 .owner = THIS_MODULE, .unlocked_ioctl = aes_ioctl, .open = nonseekable_open,
};
static struct miscdevice misc = {
 .minor = MISC_DYNAMIC_MINOR, .name = "nanokvm-hwcrypto", .fops = &ops, .mode = 0600,
};
static const struct resource resources[] = {
 DEFINE_RES_MEM(BASE, 0x1000),
};

static int __init aes_init(void)
{
 struct platform_device_info info = {
  .name = "nanokvm-hwcrypto", .id = PLATFORM_DEVID_NONE,
  .res = resources, .num_res = ARRAY_SIZE(resources), .dma_mask = DMA_BIT_MASK(32),
 };
 int err;
 /* An atomic owner switches `cur` under a sleeper that is merely
  * interrupted: correct on the single CPU of the SG2002 only. */
 BUILD_BUG_ON(IS_ENABLED(CONFIG_SMP));
 /* sg2_own() tells a caller that may not sleep by preemptible(): inside an
  * RCU read section that needs TINY_RCU. */
 BUILD_BUG_ON(IS_ENABLED(CONFIG_PREEMPT_RCU));
 if (!of_machine_is_compatible("sophgo,sg2002")) return -ENODEV;
 pdev = platform_device_register_full(&info);
 if (IS_ERR(pdev)) return PTR_ERR(pdev);
 /* Synthetic devices do not pass through OF DMA setup. This kernel defaults
  * them to coherent; SG2002 DMA is noncoherent. Set it before allocating. */
 dev_assign_dma_coherent(&pdev->dev, false);
 regs = devm_platform_ioremap_resource(pdev, 0);
 if (IS_ERR(regs)) {err = PTR_ERR(regs); goto device;}
 err = dma_set_mask_and_coherent(&pdev->dev, DMA_BIT_MASK(32));
 if (err) goto device;
 /* Never take over an engine already enabled by another owner. */
 if (readl(regs + CTRL) & 1) {err = -EBUSY; goto device;}
 initial_mask = readl(regs + MASK);
 /* Experimental: vendor-style cached descriptor with explicit DMA ownership.
  * Payload, descriptor bytes, register programming and timeout policy stay R8.
  * BIDIRECTIONAL conservatively allows device descriptor writeback.
  */
 err = sg2_set_alloc(&sets[0]);
 if (!err) err = sg2_set_alloc(&sets[1]);
 if (!err) err = sg2_set_alloc(&sets[2]);
 if (err) goto data;
 err = crypto_linux_register();
 if (err) goto crypto_data;
 err = crypto_register_ahash(&sg2_cmac_alg);
 if (err) goto crypto_linux;
 err = crypto_register_aead(&sg2_gcm_alg);
 if (err) goto crypto_cmac;
 err = crypto_register_aead(&sg2_ccm_alg);
 if (err) goto crypto_gcm;
 err = misc_register(&misc);
 if (err) goto crypto_ccm;
 pr_info("nanokvm_hwcrypto: ready crypto-api=1 descriptor=streaming coherent=%d, status=%#x mask=%#x; Linux Crypto API registered\n", dev_is_dma_coherent(&pdev->dev), readl(regs + STATUS), initial_mask);
 return 0;
crypto_ccm:
 crypto_unregister_aead(&sg2_ccm_alg);
crypto_gcm:
 crypto_unregister_aead(&sg2_gcm_alg);
crypto_cmac:
 crypto_unregister_ahash(&sg2_cmac_alg);
crypto_linux:
 crypto_linux_unregister();
crypto_data:
data:
 sg2_set_free(&sets[2]);
 sg2_set_free(&sets[1]);
 sg2_set_free(&sets[0]);
device:
 platform_device_unregister(pdev);
 return err;
}

static void __exit aes_exit(void)
{
 misc_deregister(&misc);
 kfree_sensitive(gcm_user.a); kfree_sensitive(gcm_user.b);
 memzero_explicit(&gcm_user, sizeof(gcm_user));
 crypto_unregister_aead(&sg2_ccm_alg);
 crypto_unregister_aead(&sg2_gcm_alg);
 crypto_unregister_ahash(&sg2_cmac_alg);
 crypto_linux_unregister();
 /* A failed submission pins the module, so normal unload is only reachable
  * after all opens close and each submitted descriptor has completed. */
 writel(0, regs + CTRL);
 writel(initial_mask, regs + MASK);
 sg2_set_free(&sets[2]);
 sg2_set_free(&sets[1]);
 sg2_set_free(&sets[0]);
 platform_device_unregister(pdev);
}
module_init(aes_init);
module_exit(aes_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Experimental SG2002 AES128 CTR diagnostic DMA backend");
