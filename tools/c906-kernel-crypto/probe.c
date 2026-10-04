// SPDX-License-Identifier: GPL-2.0-only
/* Bounded, namespaced qualification; does not hook production functions. */
#include <linux/completion.h>
#include <linux/crc32.h>
#include <linux/fs.h>
#include <linux/hrtimer.h>
#include <linux/ktime.h>
#include <linux/miscdevice.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/scatterlist.h>
#include <linux/slab.h>
#include <linux/uaccess.h>
#include <linux/vmalloc.h>
#include <asm/simd.h>
#include <asm/vector.h>
#include <crypto/utils.h>
#include <net/checksum.h>
#include "candidates.h"

struct kc_context {
	u8 *src, *dst, *expected;
	struct chacha_state state, base;
	struct scatterlist sg[32];
	u8 key[32], iv[16];
};
static DEFINE_MUTEX(run_lock);
static struct kc_request *active_request;
static int kc_open(struct inode *inode, struct file *file)
{
	struct kc_context *c = kzalloc(sizeof(*c), GFP_KERNEL);
	if (!c) return -ENOMEM;
	c->src = kvmalloc(KC_MAX + 4*KC_PAD, GFP_KERNEL);
	c->dst = kvmalloc(KC_MAX + 4*KC_PAD, GFP_KERNEL);
	c->expected = kvmalloc(KC_MAX + 4*KC_PAD, GFP_KERNEL);
	if (!c->src || !c->dst || !c->expected) {
		kvfree(c->src); kvfree(c->dst); kvfree(c->expected); kfree(c);
		return -ENOMEM;
	}
	file->private_data = c;
	return nonseekable_open(inode, file);
}
static int kc_release(struct inode *inode, struct file *file)
{
	struct kc_context *c = file->private_data;
	kvfree(c->src); kvfree(c->dst); kvfree(c->expected); kfree(c);
	return 0;
}
void kc_candidate_chacha_crypt(struct chacha_state *state, u8 *dst,
	const u8 *src, unsigned int bytes, int rounds)
{
	struct kc_request *r = active_request;
	u32 stream[64] __aligned(16);
	bool simd = r && (r->variant == 2 || r->variant == 3 || r->variant == 4)
		&& bytes && (r->variant != 4 || bytes >= 256)
		&& has_xtheadvector() && may_use_simd();
	if (!simd) { chacha_crypt(state, dst, src, bytes, rounds); return; }
	kernel_vector_begin();
	r->vector_calls++;
	while (bytes >= 64) {
		unsigned int blocks = min(bytes / 64, 4U), n = blocks * 64;
		kc_chacha_blocks(state, (u8 *)stream, blocks, rounds);
		kc_xor_bytes(dst, src, (u8 *)stream, n);
		dst += n; src += n; bytes -= n;
	}
	if (bytes) {
		kc_chacha_blocks(state, (u8 *)stream, 1, rounds);
		kc_xor_bytes(dst, src, (u8 *)stream, bytes);
	}
	kernel_vector_end();
	memzero_explicit(stream, sizeof(stream));
}
static void make_sg(struct kc_context *c, u8 *p, size_t n, bool split)
{
	unsigned int i = 0;
	sg_init_table(c->sg, ARRAY_SIZE(c->sg));
	while (n) {
		size_t chunk = min(n, PAGE_SIZE - offset_in_page(p));
		struct page *page = is_vmalloc_addr(p) ? vmalloc_to_page(p) : virt_to_page(p);
		if (split && i < 3) chunk = min(chunk, (size_t)(29 + i*38));
		sg_set_page(&c->sg[i], page, chunk, offset_in_page(p));
		p += chunk; n -= chunk; i++;
	}
	sg_mark_end(&c->sg[i-1]);
}
static u64 compute_one(struct kc_context *c, struct kc_request *r)
{
	u8 *src = c->src + KC_PAD + r->offset;
	u8 *dst = c->dst + KC_PAD + r->dst_offset;
	bool simd;
	u32 result;
	if (r->operation < KC_AEAD_SG) {
		kc_candidate_chacha_crypt(&c->state, dst, r->flags & 1 ? dst : src,
			r->bytes, r->operation == KC_CHACHA20 ? 20 : 12);
		return c->state.x[12];
	}
	if (r->operation == KC_AEAD_SG)
		return kc_chacha20poly1305_encrypt_sg_inplace(c->sg, r->bytes,
			NULL, 0, ((u64)r->seed << 32) | r->seed, c->key);
	simd = (r->variant == 2 || r->variant == 3) && r->bytes &&
		(r->operation == KC_COPY_CSUM || r->bytes >= 512) && may_use_simd();
	if (simd) { kernel_vector_begin(); r->vector_calls++; }
	if (r->operation == KC_COPY_CSUM)
		result = simd ? kc_copy_csum(dst, src, r->bytes) :
			(__force u32)csum_partial_copy_nocheck(src, dst, r->bytes);
	else if (simd)
		result = kc_crc_vector(r->seed, src, r->bytes, r->operation == KC_CRC32C);
	else if (r->variant == 1)
		result = kc_crc_scalar(r->seed, src, r->bytes, r->operation == KC_CRC32C);
	else
		result = r->operation == KC_CRC32C ? crc32c(r->seed, src, r->bytes) :
			crc32_le(r->seed, src, r->bytes);
	if (simd) kernel_vector_end();
	return result;
}
struct irq_request {
	struct hrtimer timer;
	struct completion done;
	struct kc_context *c;
	struct kc_request *r;
	u64 result;
};
static enum hrtimer_restart run_irq(struct hrtimer *timer)
{
	struct irq_request *q = container_of(timer, struct irq_request, timer);
	q->result = compute_one(q->c, q->r);
	complete(&q->done);
	return HRTIMER_NORESTART;
}
static u64 run_one(struct kc_context *c, struct kc_request *r)
{
	struct irq_request q;
	if (r->variant != 3) return compute_one(c, r);
	q.c = c; q.r = r;
	init_completion(&q.done);
	hrtimer_setup_on_stack(&q.timer, run_irq, CLOCK_MONOTONIC, HRTIMER_MODE_REL_HARD);
	hrtimer_start(&q.timer, ns_to_ktime(1), HRTIMER_MODE_REL_HARD);
	wait_for_completion(&q.done);
	hrtimer_cancel(&q.timer);
	destroy_hrtimer_on_stack(&q.timer);
	return q.result;
}
static u32 crc_oracle(u32 crc, const u8 *p, size_t n, u32 poly)
{
	while (n--) {
		unsigned int bit;
		crc ^= *p++;
		for (bit = 0; bit < 8; bit++) crc = (crc >> 1) ^ ((0U - (crc & 1)) & poly);
	}
	return crc;
}
static long kc_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	struct kc_context *c = file->private_data;
	struct kc_request r;
	struct chacha_state expected_state;
	u8 *src, *dst, *expected;
	u64 expected_result = 0, start, entries;
	u32 rng = 0x31415926;
	size_t i, output;
	int ret = 0;
	if (cmd != KC_RUN) return -ENOTTY;
	if (copy_from_user(&r, (void __user *)arg, sizeof(r))) return -EFAULT;
	if (r.bytes > KC_MAX || r.offset > 7 || r.dst_offset > 7 || r.pattern > 3 ||
		r.operation >= KC_OPERATIONS || r.variant > 4 || r.flags > 15 ||
		!r.iterations || r.iterations > 4096 ||
		(r.bytes && r.iterations > 64ULL*1024*1024/r.bytes) ||
		(r.variant == 3 && (r.bytes > 4096 || r.operation == KC_AEAD_SG)) ||
		((r.flags & 8) && !((r.operation == KC_CHACHA20 && r.bytes == 64) ||
			((r.operation == KC_CRC32 || r.operation == KC_CRC32C) && r.bytes == 9))))
		return -EINVAL;
	if (r.variant >= 2 && !has_xtheadvector()) return -EOPNOTSUPP;
	if (mutex_lock_interruptible(&run_lock)) return -ERESTARTSYS;
	active_request = &r;
	src = c->src + KC_PAD + r.offset;
	dst = c->dst + KC_PAD + r.dst_offset;
	expected = c->expected + KC_PAD;
	output = r.bytes + (r.operation == KC_AEAD_SG ? 16 : 0);
	memset(c->src, 0xa5, KC_MAX + 4*KC_PAD);
	memset(c->dst, 0xa5, KC_MAX + 4*KC_PAD);
	memset(c->expected, 0xa5, KC_MAX + 4*KC_PAD);
	for (i = 0; i < r.bytes; i++) {
		rng ^= rng << 13; rng ^= rng >> 17; rng ^= rng << 5;
		src[i] = r.pattern == 0 ? (u8)rng : r.pattern == 1 ? (u8)(i*37+11) :
			r.pattern == 2 ? 0 : 255;
	}
	for (i = 0; i < 32; i++) c->key[i] = i;
	memset(c->iv, 0, sizeof(c->iv));
	put_unaligned_le32(r.seed, c->iv);
	c->iv[7] = 9; c->iv[11] = 0x4a;
	if (r.flags & 8) {
		if (r.operation == KC_CHACHA20) { memset(src, 0, 64); put_unaligned_le32(1, c->iv); }
		else memcpy(src, "123456789", 9);
	}
	chacha_init(&c->base, (const u32 *)c->key, c->iv);
	expected_state = c->base;
	if (r.operation < KC_AEAD_SG) {
		chacha_crypt(&expected_state, expected, src, r.bytes,
			r.operation == KC_CHACHA20 ? 20 : 12);
		expected_result = expected_state.x[12];
	} else if (r.operation == KC_AEAD_SG) {
		chacha20poly1305_encrypt(expected, src, r.bytes, NULL, 0,
			((u64)r.seed << 32) | r.seed, c->key);
		expected_result = 1;
	} else if (r.operation == KC_COPY_CSUM) {
		memcpy(expected, src, r.bytes);
		expected_result = (__force u32)csum_partial(src, r.bytes, 0);
	} else expected_result = crc_oracle(r.seed, src, r.bytes,
		r.operation == KC_CRC32 ? 0xedb88320 : 0x82f63b78);
	if (r.flags & 8) {
		static const u8 rfc_block[64] = {
			0x10,0xf1,0xe7,0xe4,0xd1,0x3b,0x59,0x15,0x50,0x0f,0xdd,0x1f,0xa3,0x20,0x71,0xc4,
			0xc7,0xd1,0xf4,0xc7,0x33,0xc0,0x68,0x03,0x04,0x22,0xaa,0x9a,0xc3,0xd4,0x6c,0x4e,
			0xd2,0x82,0x64,0x46,0x07,0x9f,0xaa,0x09,0x14,0xc2,0xd7,0x05,0xd9,0x8b,0x02,0xa2,
			0xb5,0x12,0x9c,0xd1,0xde,0x16,0x4e,0xb9,0xcb,0xd0,0x83,0xe8,0xa2,0x50,0x3c,0x4e };
		if ((r.operation == KC_CHACHA20 && memcmp(expected, rfc_block, 64)) ||
			(r.operation == KC_CRC32 && expected_result != 0x340bc6d9) ||
			(r.operation == KC_CRC32C && expected_result != 0x1cf96d7c)) { ret = -EBADMSG; goto out; }
	}
	r.elapsed_ns = r.vector_calls = r.completed = r.result = 0;
	for (i = 0; i < r.iterations; i++) {
		c->state = c->base;
		if ((r.flags & 1) || r.operation == KC_AEAD_SG) memcpy(dst, src, r.bytes);
		if (r.operation == KC_AEAD_SG) make_sg(c, dst, output, r.flags & 4);
		start = ktime_get_ns();
		r.result = run_one(c, &r);
		r.elapsed_ns += ktime_get_ns() - start;
		r.completed++;
		if (r.result != expected_result) { ret = -EBADMSG; break; }
		if (!(i & 63)) cond_resched();
	}
	if (!ret && (r.operation <= KC_AEAD_SG || r.operation == KC_COPY_CSUM) &&
		memcmp(dst, expected, output)) ret = -EBADMSG;
	if (!ret && r.operation < KC_AEAD_SG && memcmp(&c->state, &expected_state, sizeof(c->state)))
		ret = -EBADMSG;
	entries = r.vector_calls;
	if (!ret && r.operation == KC_AEAD_SG) {
		if (r.flags & 2) dst[r.bytes + 15] ^= 1;
		if (kc_chacha20poly1305_decrypt_sg_inplace(c->sg, output, NULL, 0,
			((u64)r.seed << 32) | r.seed, c->key)
			!= !(r.flags & 2)) ret = -EBADMSG;
		if (!(r.flags & 2) && memcmp(dst, src, r.bytes)) ret = -EBADMSG;
	}
	r.vector_calls = entries;
	for (i = 0; i < KC_PAD; i++)
		if (dst[output+i] != 0xa5 || src[r.bytes+i] != 0xa5) ret = -EOVERFLOW;
	for (i = 0; i < KC_PAD + r.dst_offset; i++)
		if (c->dst[i] != 0xa5) ret = -EOVERFLOW;
	for (i = 0; i < KC_PAD + r.offset; i++)
		if (c->src[i] != 0xa5) ret = -EOVERFLOW;
out:
	active_request = NULL;
	if (copy_to_user((void __user *)arg, &r, sizeof(r))) ret = -EFAULT;
	mutex_unlock(&run_lock);
	return ret;
}
static const struct file_operations kc_fops = {
	.owner = THIS_MODULE, .open = kc_open, .release = kc_release, .unlocked_ioctl = kc_ioctl,
};
static struct miscdevice kc_device = {
	.minor = MISC_DYNAMIC_MINOR, .name = "c906-crypto-probe", .fops = &kc_fops, .mode = 0600,
};
static int __init kc_init(void) { kc_tables_init(); return misc_register(&kc_device); }
static void __exit kc_exit(void) { misc_deregister(&kc_device); }
module_init(kc_init);
module_exit(kc_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("C906 ChaCha, AEAD, CRC and fused copy-checksum qualifier");
