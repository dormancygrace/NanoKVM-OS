// SPDX-License-Identifier: GPL-2.0-only
/* Explicit probes only; no replacement of production kernel functions. */
#include <linux/fs.h>
#include <linux/completion.h>
#include <linux/hrtimer.h>
#include <linux/ktime.h>
#include <linux/lz4.h>
#include <linux/miscdevice.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/sched.h>
#include <linux/slab.h>
#include <linux/uaccess.h>
#include <linux/vmalloc.h>
#include <asm/simd.h>
#include <asm/vector.h>
#include <net/checksum.h>
#include "screen_protocol.h"

extern unsigned int screen_vector_csum(const void *, size_t);
extern size_t screen_vector_prefix(const void *, const void *, size_t);
extern unsigned int screen_scalar_prefix(const unsigned char *, const unsigned char *, size_t);
extern unsigned int screen_th_prefix(const unsigned char *, const unsigned char *, size_t);
extern int screen_th_LZ4_compress_fast(const char *, char *, int, int, int, void *);
extern int screen_lazy_compress(const char *, char *, int, int, int, void *, u64 *);
extern int screen_vec_LZ4_compress_fast(const char *, char *, int, int, int, void *);

struct screen_context {
	struct mutex lock;
	unsigned char *src, *match, *compressed, *expected, *decoded;
	void *work;
};
static int screen_open(struct inode *inode, struct file *file)
{
	struct screen_context *c = kzalloc(sizeof(*c), GFP_KERNEL);
	if (!c)
		return -ENOMEM;
	c->src = kvmalloc(SCREEN_MAX + 4 * SCREEN_PAD, GFP_KERNEL);
	c->match = kvmalloc(SCREEN_MAX + 4 * SCREEN_PAD, GFP_KERNEL);
	c->compressed = kvmalloc(LZ4_COMPRESSBOUND(SCREEN_MAX) + 4 * SCREEN_PAD, GFP_KERNEL);
	c->expected = kvmalloc(LZ4_COMPRESSBOUND(SCREEN_MAX) + 4 * SCREEN_PAD, GFP_KERNEL);
	c->decoded = kvmalloc(SCREEN_MAX + 4 * SCREEN_PAD, GFP_KERNEL);
	c->work = kvmalloc(LZ4_MEM_COMPRESS, GFP_KERNEL);
	if (!c->src || !c->match || !c->compressed || !c->expected || !c->decoded || !c->work) {
		kvfree(c->src); kvfree(c->match); kvfree(c->compressed);
		kvfree(c->expected); kvfree(c->decoded); kvfree(c->work); kfree(c);
		return -ENOMEM;
	}
	mutex_init(&c->lock);
	file->private_data = c;
	return nonseekable_open(inode, file);
}
static int screen_release(struct inode *inode, struct file *file)
{
	struct screen_context *c = file->private_data;
	kvfree(c->src); kvfree(c->match); kvfree(c->compressed);
	kvfree(c->expected); kvfree(c->decoded); kvfree(c->work); kfree(c);
	return 0;
}
static u64 compute_one(struct screen_context *c, struct screen_request *r)
{
	const unsigned char *src = c->src + SCREEN_PAD + r->offset;
	const unsigned char *match = c->match + SCREEN_PAD;
	unsigned char *dst = c->compressed + SCREEN_PAD;
	bool requested = r->variant == 2 || r->variant == 3, simd;
	u64 result;
	int capacity = r->operation == SCREEN_LZ4_LIMITED ? r->difference : LZ4_COMPRESSBOUND(r->bytes);
	simd = requested && r->bytes && may_use_simd();
	if (simd) {
		kernel_vector_begin();
		r->vector_calls++;
	}
	if (r->operation == SCREEN_CSUM)
		result = simd ? screen_vector_csum(src, r->bytes) :
			(__force u32)csum_partial(src, r->bytes, 0);
	else if (r->operation == SCREEN_PREFIX)
		result = simd ? screen_vector_prefix(src, match, r->bytes) :
			(r->variant == 1 ? screen_th_prefix(src, match, r->bytes) :
			 screen_scalar_prefix(src, match, r->bytes));
	else if (r->variant == 4)
		result = screen_lazy_compress(src, dst, r->bytes, capacity, 1, c->work, &r->vector_calls);
	else if (simd)
		result = screen_vec_LZ4_compress_fast(src, dst, r->bytes,
			capacity, 1, c->work);
	else if (r->variant == 1)
		result = screen_th_LZ4_compress_fast(src, dst, r->bytes,
			capacity, 1, c->work);
	else
		result = LZ4_compress_fast(src, dst, r->bytes,
			capacity, 1, c->work);
	if (simd)
		kernel_vector_end();
	return result;
}

struct irq_request {
    struct hrtimer timer;
    struct completion done;
    struct screen_context *context;
    struct screen_request *request;
    u64 result;
};
static enum hrtimer_restart screen_irq(struct hrtimer *timer)
{
    struct irq_request *q = container_of(timer, struct irq_request, timer);
    q->result = compute_one(q->context, q->request);
    complete(&q->done);
    return HRTIMER_NORESTART;
}
static u64 one(struct screen_context *c, struct screen_request *r)
{
    struct irq_request q;
    if (r->variant != 3)
        return compute_one(c, r);
    q.context = c;
    q.request = r;
    init_completion(&q.done);
    hrtimer_setup_on_stack(&q.timer, screen_irq, CLOCK_MONOTONIC, HRTIMER_MODE_REL_HARD);
    hrtimer_start(&q.timer, ns_to_ktime(1), HRTIMER_MODE_REL_HARD);
    wait_for_completion(&q.done);
    hrtimer_cancel(&q.timer);
    destroy_hrtimer_on_stack(&q.timer);
    return q.result;
}

static long screen_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	struct screen_context *c = file->private_data;
	struct screen_request r;
	unsigned char *src;
	u64 expected, start;
	u32 state = 0x31415926;
	size_t i, cap;
	int ret = 0, decoded;
	if (cmd != SCREEN_RUN)
		return -ENOTTY;
	if (copy_from_user(&r, (void __user *)arg, sizeof(r)))
		return -EFAULT;
	if (r.bytes > SCREEN_MAX || r.offset > 7 || r.pattern > 7 ||
	    r.operation > SCREEN_LZ4_LIMITED || r.variant > 4 || !r.iterations ||
	    (r.variant == 3 && r.bytes > (r.operation == SCREEN_CSUM ? 16384 : 4096)) ||
	    (r.variant == 4 && r.operation != SCREEN_LZ4 && r.operation != SCREEN_LZ4_LIMITED) ||
	    (r.operation == SCREEN_LZ4_LIMITED && r.difference > LZ4_COMPRESSBOUND(r.bytes)) ||
	    r.iterations > 20000 || (r.bytes && r.iterations > 128ULL * 1024 * 1024 / r.bytes))
		return -EINVAL;
	if (r.variant >= 2 && !has_xtheadvector())
		return -EOPNOTSUPP;
	if (mutex_lock_interruptible(&c->lock))
		return -ERESTARTSYS;
	src = c->src + SCREEN_PAD + r.offset;
	cap = r.operation == SCREEN_LZ4_LIMITED ? r.difference : LZ4_COMPRESSBOUND(r.bytes);
	memset(c->src, 0xa5, r.bytes + 4 * SCREEN_PAD);
	memset(c->match, 0xa5, r.bytes + 4 * SCREEN_PAD);
	memset(c->compressed, 0xa5, cap + 2 * SCREEN_PAD);
	memset(c->decoded, 0xa5, r.bytes + 2 * SCREEN_PAD);
	for (i = 0; i < r.bytes; i++) {
		state ^= state << 13; state ^= state >> 17; state ^= state << 5;
		switch (r.pattern) {
		case 0: src[i] = (u8)state; break;
		case 1: src[i] = (u8)(i * 37 + 11); break;
		case 2: src[i] = 0; break;
		case 3: src[i] = 0xff; break;
		case 4: src[i] = (u8)(i % 1009); break;
		case 5: src[i] = i % 256 < 64 ? (u8)state : (u8)(i & 7); break;
		case 6: src[i] = 1U << (i & 7); break;
		default: src[i] = (u8)(i * 3 + r.pattern); break;
		}
		c->match[SCREEN_PAD + i] = src[i];
	}
	if (r.difference < r.bytes)
		c->match[SCREEN_PAD + r.difference] ^= 1U << r.pattern;
	if (r.operation == SCREEN_CSUM)
		expected = (__force u32)csum_partial(src, r.bytes, 0);
	else if (r.operation == SCREEN_PREFIX)
		expected = min_t(u64, r.difference, r.bytes);
	else
		expected = LZ4_compress_fast(src, c->expected + SCREEN_PAD,
			r.bytes, cap, 1, c->work);
	r.elapsed_ns = r.vector_calls = r.completed = r.result = 0;
	start = ktime_get_ns();
	for (i = 0; i < r.iterations; i++) {
		r.result = one(c, &r);
		r.completed++;
		if (r.result != expected) {
			ret = -EBADMSG;
			break;
		}
		if (!(i & 63))
			cond_resched();
	}
	r.elapsed_ns = ktime_get_ns() - start;
	if (!ret && (r.operation == SCREEN_LZ4 || r.operation == SCREEN_LZ4_LIMITED)) {
		if (r.operation == SCREEN_LZ4 && !expected)
			ret = -EBADMSG;
		if (expected) {
			if (memcmp(c->compressed + SCREEN_PAD, c->expected + SCREEN_PAD, expected))
				ret = -EBADMSG;
			decoded = LZ4_decompress_safe(c->compressed + SCREEN_PAD,
				c->decoded + SCREEN_PAD, expected, r.bytes);
			if (decoded != r.bytes || memcmp(src, c->decoded + SCREEN_PAD, r.bytes))
				ret = -EBADMSG;
		}
		for (i = 0; i < SCREEN_PAD; i++)
			if (c->compressed[i] != 0xa5 || c->compressed[SCREEN_PAD + cap + i] != 0xa5 ||
			    c->decoded[i] != 0xa5 || c->decoded[SCREEN_PAD + r.bytes + i] != 0xa5)
				ret = -EOVERFLOW;
	}
	if (copy_to_user((void __user *)arg, &r, sizeof(r)))
		ret = -EFAULT;
	mutex_unlock(&c->lock);
	return ret;
}
static const struct file_operations screen_fops = {
	.owner = THIS_MODULE, .open = screen_open, .release = screen_release,
	.unlocked_ioctl = screen_ioctl,
};
static struct miscdevice screen_device = {
	.minor = MISC_DYNAMIC_MINOR, .name = "c906-screen-probe",
	.fops = &screen_fops, .mode = 0600,
};
static int __init screen_init(void) { return misc_register(&screen_device); }
static void __exit screen_exit(void) { misc_deregister(&screen_device); }
module_init(screen_init);
module_exit(screen_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Bounded checksum/LZ4 legacy vector qualification without kernel hooks");
