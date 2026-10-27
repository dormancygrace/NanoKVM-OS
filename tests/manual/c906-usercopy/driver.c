// SPDX-License-Identifier: GPL-2.0-only
#include <linux/module.h>
#include <linux/miscdevice.h>
#include <linux/fs.h>
#include <linux/vmalloc.h>
#include <linux/uaccess.h>
#include <linux/mutex.h>
#include <linux/sched.h>
#include <asm/csr.h>
#include "usercopy_test.h"

/* Exercise the running kernel's raw, SUM-enabled and public usercopy. */
static u8 *area;
static DEFINE_MUTEX(sm_lock);

static unsigned long copy_test(struct sm_req *q, void *kp, void __user *up)
{
	unsigned long residual;

	if (q->variant == 2)
		return q->direction ? copy_to_user(up, kp, q->n) :
			copy_from_user(kp, up, q->n);
	if (q->variant == 1) {
		csr_set(CSR_STATUS, SR_SUM);
		residual = q->direction ?
			__asm_copy_to_user_sum_enabled(up, kp, q->n) :
			__asm_copy_from_user_sum_enabled(kp, up, q->n);
		if (!(csr_read(CSR_STATUS) & SR_SUM))
			q->status = 1;
		csr_clear(CSR_STATUS, SR_SUM);
	} else {
		residual = q->direction ? __asm_copy_to_user(up, kp, q->n) :
			__asm_copy_from_user(kp, up, q->n);
	}
	/* The raw assembly's caller owns zero padding. Variant 2 instead
	 * exercises the actual copy_from_user wrapper's zero padding. */
	if (!q->direction && q->zero && residual <= q->n)
		memset((u8 *)kp + q->n - residual, 0, residual);
	return residual;
}

static long memory_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	struct sm_req q;
	void __user *up;
	unsigned i;
	int err = 0;

	if (cmd != SM_IOCTL)
		return -ENOTTY;
	if (copy_from_user(&q, (void __user *)arg, sizeof(q)))
		return -EFAULT;
	if (q.n > SM_LIMIT || q.offset > 7 || q.variant > 2 ||
	    q.direction > 1 || q.zero > 1)
		return -EINVAL;
	up = u64_to_user_ptr(q.user);
	if (!access_ok(up, q.n))
		return -EFAULT;

	mutex_lock(&sm_lock);
	q.residual = q.status = 0;
	{
		u8 *kp = area + 8 + q.offset;

		for (i = 0; i < q.n + 32; i++)
			area[i] = q.direction ? sm_pattern(i) : 0xa5;
		q.residual = copy_test(&q, kp, up);
		q.status |= csr_read(CSR_STATUS) & SR_SUM;
		if (q.residual > q.n)
			err = -EINVAL;
		else if (q.output && copy_to_user(u64_to_user_ptr(q.output),
						 area, q.n + 32))
			err = -EFAULT;
	}
	mutex_unlock(&sm_lock);
	if (err)
		return err;
	return copy_to_user((void __user *)arg, &q, sizeof(q)) ? -EFAULT : 0;
}

static const struct file_operations memory_ops = {
	.owner = THIS_MODULE,
	.unlocked_ioctl = memory_ioctl,
};
static struct miscdevice memory_dev = {
	.minor = MISC_DYNAMIC_MINOR,
	.name = "c906_usercopy_test",
	.fops = &memory_ops,
	.mode = 0600,
};

static int __init memory_start(void)
{
	int err;

	area = vmalloc(65536);
	if (!area) {
		err = -ENOMEM;
		goto fail;
	}
	err = misc_register(&memory_dev);
	if (err)
		goto fail;
	return 0;
fail:
	vfree(area);
	return err;
}

static void __exit memory_stop(void)
{
	misc_deregister(&memory_dev);
	vfree(area);
}
module_init(memory_start);
module_exit(memory_stop);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Validate live C906 raw usercopy, SUM wrappers and fault recovery");
