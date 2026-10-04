// SPDX-License-Identifier: GPL-2.0-only
/* Explicit, opt-in probes: no hooks into normal kernel copy paths. */
#include <linux/fs.h>
#include <linux/ktime.h>
#include <linux/miscdevice.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/sched.h>
#include <linux/slab.h>
#include <linux/uaccess.h>
#include <linux/vmalloc.h>
#include <asm/asm-prototypes.h>
#include <asm/simd.h>
#include <asm/vector.h>
#include "protocol.h"

extern size_t probe_vector_usercopy(void *dst, const void *src, size_t n);
extern size_t probe_vector_copy(void *dst, const void *src, size_t n);
extern void probe_vector_memset(void *dst, int value, size_t n);
extern void *__memcpy(void *dst, const void *src, size_t n);
extern void *__memset(void *dst, int value, size_t n);

struct probe_context {
	struct mutex mutex;
	unsigned char *src;
	unsigned char *dst;
	size_t snapshot_size;
};

static int probe_open(struct inode *inode, struct file *file)
{
	struct probe_context *ctx;
	ctx = kzalloc(sizeof(*ctx), GFP_KERNEL);
	if (!ctx)
		return -ENOMEM;
	ctx->src = kvmalloc(C906_MAX_BYTES + 4 * C906_PAD, GFP_KERNEL);
	ctx->dst = kvmalloc(C906_MAX_BYTES + 4 * C906_PAD, GFP_KERNEL);
	if (!ctx->src || !ctx->dst) {
		kvfree(ctx->src);
		kvfree(ctx->dst);
		kfree(ctx);
		return -ENOMEM;
	}
	mutex_init(&ctx->mutex);
	file->private_data = ctx;
	return nonseekable_open(inode, file);
}

static int probe_release(struct inode *inode, struct file *file)
{
	struct probe_context *ctx = file->private_data;
	kvfree(ctx->src);
	kvfree(ctx->dst);
	kfree(ctx);
	return 0;
}

static unsigned long scalar_usercopy(void *dst, const void *src, size_t n,
				    bool from_user)
{
	return from_user ? __asm_copy_from_user(dst, src, n) :
			   __asm_copy_to_user(dst, src, n);
}

static unsigned long one_copy(struct probe_context *ctx,
			      struct c906_request *r)
{
	void *dst = ctx->dst + C906_PAD + r->destination_offset;
	const void *src = ctx->src + C906_PAD + r->source_offset;
	void __user *user = u64_to_user_ptr(r->user_pointer);
	unsigned long remain = 0;
	bool userspace = r->operation <= C906_TO_USER;
	bool from_user = r->operation == C906_FROM_USER;
	bool simd;

	if (!r->bytes)
		return 0;
	if (userspace) {
		might_fault();
		if (!access_ok(user, r->bytes)) {
			if (from_user)
				__memset(dst, 0, r->bytes);
			return r->bytes;
		}
		if (from_user)
			src = user;
		else
			dst = (void __force *)user;
	}
	simd = r->vector && may_use_simd();
	if (simd) {
		kernel_vector_begin();
		if (r->operation == C906_FILL)
			probe_vector_memset(dst, r->fill, r->bytes);
		else if (userspace)
			remain = probe_vector_usercopy(dst, src, r->bytes);
		else
			remain = probe_vector_copy(dst, src, r->bytes);
		kernel_vector_end();
		r->vector_calls++;
	}
	if (!simd || remain) {
		size_t n = simd ? remain : r->bytes;
		size_t copied = r->bytes - n;
		if (userspace)
			remain = scalar_usercopy(dst + copied, src + copied, n, from_user);
		else if (r->operation == C906_FILL)
			__memset(dst, r->fill, n);
		else
			__memcpy(dst + copied, src + copied, n);
	}
	/* Match copy_from_user()'s public zero-padding contract. */
	if (from_user && remain)
		__memset(dst + r->bytes - remain, 0, remain);
	return remain;
}

static long probe_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	struct probe_context *ctx = file->private_data;
	struct c906_request r;
	size_t source_size, destination_size, i;
	u64 start;
	long ret = 0;

	if (cmd != C906_RUN)
		return -ENOTTY;
	if (copy_from_user(&r, (void __user *)arg, sizeof(r)))
		return -EFAULT;
	if (r.bytes > C906_MAX_BYTES || r.source_offset >= C906_PAD ||
	    r.destination_offset >= C906_PAD || r.operation > C906_FILL ||
	    r.vector > 1 || r.fill > 255 || !r.iterations ||
	    r.iterations > 100000 ||
	    (r.bytes && r.iterations > (256ULL * 1024 * 1024) / r.bytes))
		return -EINVAL;
	if (r.vector && !has_xtheadvector())
		return -EOPNOTSUPP;
	if (mutex_lock_interruptible(&ctx->mutex))
		return -ERESTARTSYS;
	source_size = 2 * C906_PAD + r.source_offset + r.bytes;
	destination_size = 2 * C906_PAD + r.destination_offset + r.bytes;
	for (i = 0; i < source_size; i++)
		ctx->src[i] = (unsigned char)(i * 37 + 11);
	__memset(ctx->dst, 0xa5, destination_size);
	ctx->snapshot_size = destination_size;
	file->f_pos = 0;
	r.elapsed_ns = r.not_copied = r.vector_calls = r.completed = 0;
	start = ktime_get_ns();
	for (i = 0; i < r.iterations; i++) {
		r.not_copied = one_copy(ctx, &r);
		r.completed++;
		if (r.not_copied)
			break;
		/* Include this same scheduling point in both variants. */
		if (!(i & 63))
			cond_resched();
	}
	r.elapsed_ns = ktime_get_ns() - start;
	if (copy_to_user((void __user *)arg, &r, sizeof(r)))
		ret = -EFAULT;
	mutex_unlock(&ctx->mutex);
	return ret;
}

static ssize_t probe_read(struct file *file, char __user *dst,
			  size_t n, loff_t *position)
{
	struct probe_context *ctx = file->private_data;
	ssize_t ret;
	if (mutex_lock_interruptible(&ctx->mutex))
		return -ERESTARTSYS;
	ret = simple_read_from_buffer(dst, n, position, ctx->dst, ctx->snapshot_size);
	mutex_unlock(&ctx->mutex);
	return ret;
}

static const struct file_operations probe_ops = {
	.owner = THIS_MODULE,
	.open = probe_open,
	.release = probe_release,
	.unlocked_ioctl = probe_ioctl,
	.read = probe_read,
};

static struct miscdevice probe_device = {
	.minor = MISC_DYNAMIC_MINOR,
	.name = "c906-vector-probe",
	.fops = &probe_ops,
	.mode = 0600,
};

static int __init probe_init(void)
{
	if (!has_xtheadvector())
		return -ENODEV;
	return misc_register(&probe_device);
}
static void __exit probe_exit(void)
{
	misc_deregister(&probe_device);
}
module_init(probe_init);
module_exit(probe_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Bounded legacy RVV0.7.1 copy/fill qualification without kernel hooks");
