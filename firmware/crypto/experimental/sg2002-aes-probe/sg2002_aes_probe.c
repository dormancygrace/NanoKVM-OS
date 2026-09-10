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
#include <linux/iopoll.h>
#include <linux/miscdevice.h>
#include <linux/mutex.h>
#include <linux/uaccess.h>
#include <linux/slab.h>
#include <linux/of.h>
#include "sg2002_aes_probe.h"

#define BASE 0x02060000
#define CTRL 0x00
#define MASK 0x04
#define DESC_LO 0x08
#define DESC_HI 0x0c
#define STATUS 0x10
#define DESC_BYTES 128
static struct platform_device *pdev;
static void __iomem *regs;
static u32 *desc;
static u8 *buffer;
static dma_addr_t desc_dma, buffer_dma;
static DEFINE_MUTEX(lock);
static bool poisoned;
static u32 initial_mask;

/* Keep the public fixed-size ioctl ABI, but allocate only the submitted data.
 * This object is CPU scratch storage; DMA mappings and completion are unchanged.
 */
struct aes_scratch {
 u32 length, reserved;
 u8 key[16], iv[16];
 u32 status;
 u8 data[];
};
static_assert(sizeof(struct aes_scratch) == offsetof(struct sg2002_aes_request, data));
static_assert(offsetof(struct aes_scratch, key) == offsetof(struct sg2002_aes_request, key));
static_assert(offsetof(struct aes_scratch, iv) == offsetof(struct sg2002_aes_request, iv));
static_assert(offsetof(struct aes_scratch, status) == offsetof(struct sg2002_aes_request, status));

static long aes_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
 struct aes_scratch header = {}, *r;
 u32 status, size;
 int err;
 if (cmd != SG2002_AES_CTR) return -ENOTTY;
 /* Snapshot once: allocation and payload copy must use the same length even
  * if userspace changes its header concurrently. Never reread that header. */
 if (copy_from_user(&header, (void __user *)arg, sizeof(header))) {
  err = -EFAULT; goto clear_header;
 }
 if (!header.length || header.length > SG2002_AES_MAX || header.reserved) {
  err = -EINVAL; goto clear_header;
 }
 r = kmalloc(sizeof(*r) + header.length, GFP_KERNEL);
 if (!r) { err = -ENOMEM; goto clear_header; }
 memcpy(r, &header, sizeof(header));
 memzero_explicit(&header, sizeof(header));
 if (copy_from_user(r->data, (u8 __user *)arg + offsetof(struct sg2002_aes_request, data), r->length)) {
  err = -EFAULT; goto free_request;
 }
 err = mutex_lock_interruptible(&lock);
 if (err) goto free_request;
 if (poisoned) { err = -EIO; goto unlock; }
 size = ALIGN(r->length, 16);
 memcpy(buffer, r->data, r->length);
 memset(buffer + r->length, 0, size - r->length);
 memset(desc, 0, DESC_BYTES);
 desc[0] = BIT(23) | BIT(19) | BIT(9) | 0xf;
 desc[1] = BIT(5) | BIT(2) | BIT(0); /* AES128,CTR,encrypt */
 desc[4] = lower_32_bits(buffer_dma);
 desc[5] = upper_32_bits(buffer_dma);
 desc[6] = lower_32_bits(buffer_dma);
 desc[7] = upper_32_bits(buffer_dma);
 desc[8] = size;
 memcpy(&desc[10], r->key, 16);
 memcpy(&desc[18], r->iv, 16);
 dma_sync_single_for_device(&pdev->dev, buffer_dma, size, DMA_BIDIRECTIONAL);
 dma_wmb();
 /* Match vendor interrupt/status enable. The first MASK=0 experiment
  * timed out; this change tests that difference without assuming its cause. */
 writel(7, regs + MASK);
 writel(7, regs + STATUS);
 writel(lower_32_bits(desc_dma), regs + DESC_LO);
 writel(upper_32_bits(desc_dma), regs + DESC_HI);
 writel((6 << 24) | (16 << 16) | 3, regs + CTRL);
 err = readl_poll_timeout(regs + STATUS, status, status != 0, 1, 20000);
 r->status = status;
 if (err || status != 1) {
  /* Completion/error bits beyond bit0 are not assumed to prove DMA idle.
   * Retain mappings, buffers and module code until reset on any uncertainty. */
  poisoned = true;
  __module_get(THIS_MODULE);
  pr_err("sg2002_aes_probe: DMA uncertain status=%#x timeout=%d; pinned until reboot\n", status, err);
  err = err ? err : -EIO;
  goto unlock;
 }
 dma_rmb();
 dma_sync_single_for_cpu(&pdev->dev, buffer_dma, size, DMA_BIDIRECTIONAL);
 memcpy(r->data, buffer, r->length);
 memzero_explicit(buffer, size);
 memzero_explicit(desc, DESC_BYTES);
 writel(7, regs + STATUS);
 writel(initial_mask, regs + MASK);
 if (copy_to_user((void __user *)arg, r, offsetof(struct sg2002_aes_request, data) + r->length)) err = -EFAULT;
unlock:
 mutex_unlock(&lock);
free_request:
 kfree_sensitive(r);
clear_header:
 memzero_explicit(&header, sizeof(header));
 return err;
}

static const struct file_operations ops = {
 .owner = THIS_MODULE, .unlocked_ioctl = aes_ioctl, .open = nonseekable_open,
};
static struct miscdevice misc = {
 .minor = MISC_DYNAMIC_MINOR, .name = "sg2002-aes-probe", .fops = &ops, .mode = 0600,
};
static const struct resource resources[] = {
 DEFINE_RES_MEM(BASE, 0x1000),
};

static int __init aes_init(void)
{
 struct platform_device_info info = {
  .name = "sg2002-aes-probe", .id = PLATFORM_DEVID_NONE,
  .res = resources, .num_res = ARRAY_SIZE(resources), .dma_mask = DMA_BIT_MASK(32),
 };
 int err;
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
 desc = dma_alloc_coherent(&pdev->dev, DESC_BYTES, &desc_dma, GFP_KERNEL);
 if (!desc) {err = -ENOMEM; goto device;}
 buffer = kzalloc(SG2002_AES_MAX, GFP_KERNEL);
 if (!buffer) {err = -ENOMEM; goto descriptor;}
 buffer_dma = dma_map_single(&pdev->dev, buffer, SG2002_AES_MAX, DMA_BIDIRECTIONAL);
 if (dma_mapping_error(&pdev->dev, buffer_dma)) {err = -EIO; goto free_buffer;}
 dma_sync_single_for_cpu(&pdev->dev, buffer_dma, SG2002_AES_MAX, DMA_BIDIRECTIONAL);
 err = misc_register(&misc);
 if (err) goto data;
 pr_info("sg2002_aes_probe: ready coherent=%d, status=%#x mask=%#x; no DMA submitted\n", dev_is_dma_coherent(&pdev->dev), readl(regs + STATUS), initial_mask);
 return 0;
data:
 dma_unmap_single(&pdev->dev, buffer_dma, SG2002_AES_MAX, DMA_BIDIRECTIONAL);
free_buffer:
 kfree_sensitive(buffer);
descriptor:
 dma_free_coherent(&pdev->dev, DESC_BYTES, desc, desc_dma);
device:
 platform_device_unregister(pdev);
 return err;
}

static void __exit aes_exit(void)
{
 misc_deregister(&misc);
 /* A failed submission pins the module, so normal unload is only reachable
  * after all opens close and each submitted descriptor has completed. */
 writel(0, regs + CTRL);
 writel(initial_mask, regs + MASK);
 memzero_explicit(buffer, SG2002_AES_MAX);
 memzero_explicit(desc, DESC_BYTES);
 dma_unmap_single(&pdev->dev, buffer_dma, SG2002_AES_MAX, DMA_BIDIRECTIONAL);
 kfree_sensitive(buffer);
 dma_free_coherent(&pdev->dev, DESC_BYTES, desc, desc_dma);
 platform_device_unregister(pdev);
}
module_init(aes_init);
module_exit(aes_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Experimental SG2002 AES128 CTR diagnostic DMA backend");
