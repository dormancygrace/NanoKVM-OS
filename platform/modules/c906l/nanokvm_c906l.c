// SPDX-License-Identifier: GPL-2.0-only
/*
 * The SG2002's second C906 (C906L: 700 MHz, FPU, no vector unit, 16 KiB 4-way
 * L1 data cache, 8 KiB 2-way instruction cache) as a coprocessor of Linux.
 *
 * The driver binds the device-tree node "nanokvm,sg2002-c906l": SEC_SYS, which
 * holds the core's boot address (any access to it hangs the bus while the
 * efuse clocks are gated: they are enabled around it), the mailbox and its
 * interrupt, the efuse clocks and the core's reset line. It loads the program
 * of firmware/ (built by scripts/build-c906l-firmware.py, embedded here) into
 * memory of its own, points the boot address at it and releases the reset.
 *
 * Its job: the Opus encoder of /dev/nanokvm-opus (nanokvm_opus.h). A job
 * reserves a slot of the ring (c906l.h) with its staging and output buffers;
 * the small core does it and raises the mailbox; the interrupt hands the
 * results to a bottom-half work item, which gives them back in the order of
 * the jobs. The interrupt stays masked while the work item runs, so under
 * load one interrupt serves many results.
 *
 * A job not answered within C906L_TIMEOUT_MS gives the small core up: it is
 * held in reset, and every open and later job fails. The small core writes
 * only its own memory and the slots' output buffers, none of which is reused
 * while the module is loaded.
 */
#include <linux/clk.h>
#include <linux/delay.h>
#include <linux/dma-mapping.h>
#include <linux/interrupt.h>
#include <linux/io.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/platform_device.h>
#include <linux/reset.h>
#include <linux/timer.h>
#include <linux/workqueue.h>

#include <linux/completion.h>
#include <linux/miscdevice.h>
#include <linux/mutex.h>
#include <linux/slab.h>
#include <linux/uaccess.h>

#include "c906l.h"
#include "nanokvm_opus.h"

/* A user of a slot. */
struct c906l_owner {
	/*
	 * The job's result value, in bottom-half context; ok false: the small
	 * core was given up. The slot is the owner's until this returns.
	 */
	void (*done)(struct c906l_owner *o, bool ok, s64 value);
	u8 *stage;			/* C906L_STAGE bytes, the slot's input */
	const u8 *out;			/* the slot's output, valid in done() */
	u64 seq;
};

asm(".section .rodata\n.balign 64\nc906l_program:\n.incbin \"" C906L_FW "\"\nc906l_program_end:\n.previous\n");
extern const u8 c906l_program[], c906l_program_end[];

#define C906L_TIMEOUT_MS	50

static struct {
	struct device *dev;
	bool up;
	u8 *mem;			/* the program's memory, uncached here */
	dma_addr_t mem_dma;
	u8 *ring, *stage, *out;		/* cached here, synced around the small core */
	dma_addr_t ring_dma, stage_dma, out_dma;
	void __iomem *sec, *mbox;
	struct clk_bulk_data clks[2];
	struct reset_control *rst;
	spinlock_t lock;
	u64 next, done;			/* seq of the next reservation; last seq completed */
	struct c906l_owner *owner[C906L_SLOTS];
	bool submitted[C906L_SLOTS];
	u32 out_len[C906L_SLOTS];	/* output bytes the slot's job may write */
	unsigned long since[C906L_SLOTS];
	int irq;
	struct work_struct bh;
	struct timer_list timer;
} c906l = { .lock = __SPIN_LOCK_UNLOCKED(c906l.lock) };

static unsigned long jobs, given_up;
module_param(jobs, ulong, 0444);
MODULE_PARM_DESC(jobs, "jobs the small core finished");
module_param(given_up, ulong, 0444);
MODULE_PARM_DESC(given_up, "1 once the small core has been given up");

static struct c906l_job *slot_job(u64 seq)
{
	return (struct c906l_job *)(c906l.ring + (seq % C906L_SLOTS) * C906L_SLOT_SIZE);
}
static struct c906l_result *slot_result(u64 seq)
{
	return (struct c906l_result *)(c906l.mem + C906L_RESULTS + (seq % C906L_SLOTS) * C906L_RESULT_SIZE);
}

/* A slot and its buffers, or false: no small core, or the ring full. */
static bool c906l_reserve(struct c906l_owner *o)
{
	unsigned long flags;
	bool ok = false;

	spin_lock_irqsave(&c906l.lock, flags);
	if (READ_ONCE(c906l.up) && c906l.next - c906l.done <= C906L_SLOTS) {
		u64 seq = c906l.next++;
		u32 i = seq % C906L_SLOTS;

		c906l.owner[i] = o;
		c906l.submitted[i] = false;
		o->seq = seq;
		o->stage = c906l.stage + i * C906L_STAGE;
		o->out = c906l.out + i * C906L_OUT;
		ok = true;
	}
	spin_unlock_irqrestore(&c906l.lock, flags);
	return ok;
}

/*
 * The job of a reserved slot: job (seq not set) with stage bytes of input
 * in the staging buffer and up to out bytes of output in the output buffer.
 * Never fails; the result arrives through o->done(). Only what the job uses
 * is synced: a line costs a cache operation.
 */
static void c906l_submit(struct c906l_owner *o, struct c906l_job *job, u32 stage, u32 out)
{
	u64 seq = o->seq;
	u32 i = seq % C906L_SLOTS;
	dma_addr_t slot = c906l.ring_dma + i * C906L_SLOT_SIZE;
	struct c906l_job *j = slot_job(seq);
	unsigned long flags;

	if (stage)
		dma_sync_single_for_device(c906l.dev, c906l.stage_dma + i * C906L_STAGE, stage, DMA_TO_DEVICE);
	/* the output buffer is the small core's until the result */
	c906l.out_len[i] = out;
	if (out)
		dma_sync_single_for_device(c906l.dev, c906l.out_dma + i * C906L_OUT, out, DMA_FROM_DEVICE);
	/* The job in memory with the slot's old seq, then the seq: the small core
	 * never sees this seq with an older part of the job. */
	job->seq = j->seq;
	memcpy(j, job, sizeof(*j));
	dma_sync_single_for_device(c906l.dev, slot, sizeof(*j), DMA_TO_DEVICE);
	WRITE_ONCE(j->seq, seq);
	dma_sync_single_for_device(c906l.dev, slot, sizeof(j->seq), DMA_TO_DEVICE);
	spin_lock_irqsave(&c906l.lock, flags);
	if (c906l.up) {
		writeb(C906L_TO_SMALL, c906l.mbox + C906L_MBOX_SET);
		if (!timer_pending(&c906l.timer))
			mod_timer(&c906l.timer, jiffies + msecs_to_jiffies(C906L_TIMEOUT_MS));
	}
	c906l.submitted[i] = true;
	c906l.since[i] = jiffies;
	spin_unlock_irqrestore(&c906l.lock, flags);
	if (!READ_ONCE(c906l.up))
		queue_work(system_bh_wq, &c906l.bh);	/* given up meanwhile: hands it back */
}


/* The mailbox interrupt on again, unless the small core is given up. */
static void c906l_unmask(void)
{
	unsigned long flags;

	spin_lock_irqsave(&c906l.lock, flags);
	if (c906l.up)
		writeb(C906L_TO_BIG, c906l.mbox + C906L_MBOX_EN(1));
	spin_unlock_irqrestore(&c906l.lock, flags);
}

/*
 * Hand back what is done, in order; with the small core given up, everything
 * submitted. Only this work item advances c906l.done, and only after the
 * owner's callback: until then the slot and its buffers stay the owner's.
 */
static void c906l_complete(struct work_struct *w)
{
	bool unmasked = false;

	for (;;) {
		struct c906l_owner *o;
		unsigned long flags;
		u64 seq;
		s64 value = 0;
		bool have = false;
		u32 i;

		spin_lock_irqsave(&c906l.lock, flags);
		seq = c906l.done + 1;
		i = seq % C906L_SLOTS;
		if (seq >= c906l.next || !c906l.submitted[i]) {
			spin_unlock_irqrestore(&c906l.lock, flags);
			break;
		}
		if (c906l.up) {
			struct c906l_result *r = slot_result(seq);

			if (READ_ONCE(r->seq) != seq) {
				spin_unlock_irqrestore(&c906l.lock, flags);
				/* Unmask the interrupt (c906l_irq()) and look once more: a
				 * result written meanwhile raises it again or is seen here. */
				if (unmasked)
					return;
				c906l_unmask();
				unmasked = true;
				continue;
			}
			rmb();
			value = r->value;
			have = true;
			jobs++;
		}
		o = c906l.owner[i];
		spin_unlock_irqrestore(&c906l.lock, flags);
		if (have && c906l.out_len[i])
			dma_sync_single_for_cpu(c906l.dev, c906l.out_dma + i * C906L_OUT, c906l.out_len[i], DMA_FROM_DEVICE);
		if (o)
			o->done(o, have, value);
		spin_lock_irqsave(&c906l.lock, flags);
		c906l.owner[i] = NULL;
		c906l.submitted[i] = false;
		c906l.done = seq;
		spin_unlock_irqrestore(&c906l.lock, flags);
	}
	if (!unmasked)
		c906l_unmask();
}

static void c906l_give_up(const char *why)
{
	unsigned long flags;

	spin_lock_irqsave(&c906l.lock, flags);
	if (!c906l.up) {
		spin_unlock_irqrestore(&c906l.lock, flags);
		return;
	}
	WRITE_ONCE(c906l.up, false);
	writeb(0, c906l.mbox + C906L_MBOX_EN(1));
	spin_unlock_irqrestore(&c906l.lock, flags);
	reset_control_assert(c906l.rst);
	given_up = 1;
	dev_err(c906l.dev, "small core given up (%s)\n", why);
	queue_work(system_bh_wq, &c906l.bh);
}

static void c906l_timeout(struct timer_list *t)
{
	unsigned long flags;
	bool late = false, open;
	u64 seq;

	spin_lock_irqsave(&c906l.lock, flags);
	seq = c906l.done + 1;
	open = seq < c906l.next;
	if (open && c906l.submitted[seq % C906L_SLOTS] &&
	    time_after(jiffies, c906l.since[seq % C906L_SLOTS] + msecs_to_jiffies(C906L_TIMEOUT_MS)))
		late = true;
	if (open && !late && c906l.up)
		mod_timer(&c906l.timer, jiffies + msecs_to_jiffies(C906L_TIMEOUT_MS));
	spin_unlock_irqrestore(&c906l.lock, flags);
	if (late)
		c906l_give_up("a job not answered");
}

static irqreturn_t c906l_irq(int irq, void *dev)
{
	if (!(readb(c906l.mbox + C906L_MBOX_INT(1)) & C906L_TO_BIG))
		return IRQ_NONE;
	/* Masked until the bottom half has taken the results; only this core
	 * writes its enable register. */
	writeb(0, c906l.mbox + C906L_MBOX_EN(1));
	writeb(C906L_TO_BIG, c906l.mbox + C906L_MBOX_CLR(1));
	queue_work(system_bh_wq, &c906l.bh);
	return IRQ_HANDLED;
}

/*
 * /dev/nanokvm-opus (nanokvm_opus.h): an encoder of the small core per open
 * file. A frame is a job like any other: its PCM goes to the slot's staging
 * buffer, the packet comes back in the slot's output buffer. There is no
 * encoder on the big core: if the small core is given up, encoding fails.
 */
struct opus_file {
	struct c906l_owner o;
	struct mutex lock;		/* one job of the file at a time */
	struct completion done;
	s64 value;
	int encoder;
	bool ready;
	u8 packet[1275];		/* the largest Opus packet */
};
static unsigned long opus_encoders;	/* bit per encoder of the small core in use */
static DEFINE_SPINLOCK(opus_lock);

static void opus_done(struct c906l_owner *o, bool ok, s64 value)
{
	struct opus_file *f = container_of(o, struct opus_file, o);

	f->value = ok ? value : -EIO;
	if (ok && value > 0)
		memcpy(f->packet, o->out, min_t(s64, value, sizeof(f->packet)));
	complete(&f->done);
}

/* One job of the file's encoder; its result value, or -EIO without the small core. */
static s64 opus_job(struct opus_file *f, struct c906l_job *j, const void __user *pcm)
{
	int tries = 0;

	while (!c906l_reserve(&f->o)) {
		/* the ring full, or no small core */
		if (!READ_ONCE(c906l.up) || ++tries > 1000)
			return -EIO;
		usleep_range(50, 100);
	}
	if (pcm && copy_from_user(f->o.stage, pcm, C906L_OPUS_FRAME * 4)) {
		c906l_submit(&f->o, &(struct c906l_job){ .type = 0 }, 0, 0);	/* gives the slot back */
		wait_for_completion(&f->done);
		reinit_completion(&f->done);
		return -EFAULT;
	}
	if (pcm) {
		j->opus.pcm = c906l.stage_dma + (f->o.seq % C906L_SLOTS) * C906L_STAGE;
		j->opus.out = c906l.out_dma + (f->o.seq % C906L_SLOTS) * C906L_OUT;
		j->opus.out_size = sizeof(f->packet);
	}
	c906l_submit(&f->o, j, pcm ? C906L_OPUS_FRAME * 4 : 0, pcm ? sizeof(f->packet) : 0);
	/* bounded: a job not answered gives the small core up within C906L_TIMEOUT_MS */
	wait_for_completion(&f->done);
	reinit_completion(&f->done);
	return f->value;
}

static int opus_open(struct inode *inode, struct file *file)
{
	struct opus_file *f;
	int e;

	f = kzalloc(sizeof(*f), GFP_KERNEL);
	if (!f)
		return -ENOMEM;
	spin_lock(&opus_lock);
	e = find_first_zero_bit(&opus_encoders, C906L_OPUS_ENCODERS);
	if (e < C906L_OPUS_ENCODERS)
		set_bit(e, &opus_encoders);
	spin_unlock(&opus_lock);
	if (e >= C906L_OPUS_ENCODERS) {
		kfree(f);
		return -EBUSY;
	}
	f->encoder = e;
	f->o.done = opus_done;
	mutex_init(&f->lock);
	init_completion(&f->done);
	file->private_data = f;
	return 0;
}

static int opus_release(struct inode *inode, struct file *file)
{
	struct opus_file *f = file->private_data;

	clear_bit(f->encoder, &opus_encoders);
	kfree(f);
	return 0;
}

static long opus_ioctl_locked(struct opus_file *f, unsigned int cmd, unsigned long arg);
static long opus_ioctl(struct file *file, unsigned int cmd, unsigned long arg)
{
	struct opus_file *f = file->private_data;
	long ret;

	mutex_lock(&f->lock);
	ret = opus_ioctl_locked(f, cmd, arg);
	mutex_unlock(&f->lock);
	return ret;
}

static long opus_ioctl_locked(struct opus_file *f, unsigned int cmd, unsigned long arg)
{
	struct c906l_job j = {};
	s64 n;

	if (cmd == NANOKVM_OPUS_SETUP) {
		struct nanokvm_opus_setup s;

		if (copy_from_user(&s, (void __user *)arg, sizeof(s)))
			return -EFAULT;
		j.type = C906L_JOB_OPUS_INIT;
		j.opus_init.encoder = f->encoder;
		j.opus_init.bitrate = s.bitrate;
		j.opus_init.complexity = s.complexity;
		j.opus_init.vbr_constraint = s.vbr_constraint;
		n = opus_job(f, &j, NULL);
		f->ready = n == 0;
		return n == 0 ? 0 : n == -EIO ? -EIO : -EINVAL;
	}
	if (cmd == NANOKVM_OPUS_ENCODE) {
		struct nanokvm_opus_encode e;

		if (copy_from_user(&e, (void __user *)arg, sizeof(e)))
			return -EFAULT;
		if (!f->ready)
			return -EINVAL;
		j.type = C906L_JOB_OPUS_ENCODE;
		j.opus.encoder = f->encoder;
		n = opus_job(f, &j, u64_to_user_ptr(e.pcm));
		if (n <= 0)
			return n == -EIO || n == -EFAULT ? n : -EIO;
		if (n > e.packet_size)
			return -ENOSPC;
		return copy_to_user(u64_to_user_ptr(e.packet), f->packet, n) ? -EFAULT : n;
	}
	return -ENOTTY;
}

static const struct file_operations opus_fops = {
	.owner = THIS_MODULE,
	.open = opus_open,
	.release = opus_release,
	.unlocked_ioctl = opus_ioctl,
};
static struct miscdevice opus_misc = { .minor = MISC_DYNAMIC_MINOR, .name = "nanokvm-opus", .fops = &opus_fops };

/* Memory written through the cache here and synced for the small core. */
static void *alloc_mapped(size_t size, dma_addr_t *dma)
{
	void *p = devm_kzalloc(c906l.dev, size, GFP_KERNEL);

	if (!p)
		return NULL;
	*dma = dma_map_single(c906l.dev, p, size, DMA_BIDIRECTIONAL);
	return dma_mapping_error(c906l.dev, *dma) ? NULL : p;
}

static void c906l_unmap(void)
{
	if (c906l.ring)
		dma_unmap_single(c906l.dev, c906l.ring_dma, C906L_SLOTS * C906L_SLOT_SIZE, DMA_BIDIRECTIONAL);
	if (c906l.stage)
		dma_unmap_single(c906l.dev, c906l.stage_dma, C906L_SLOTS * C906L_STAGE, DMA_BIDIRECTIONAL);
	if (c906l.out)
		dma_unmap_single(c906l.dev, c906l.out_dma, C906L_SLOTS * C906L_OUT, DMA_BIDIRECTIONAL);
}

/*
 * The small core held in reset and the mailbox interrupt gone: masked under
 * the lock (the bottom half unmasks only while up), freed (which waits for a
 * running handler), the bottom half drained and the mailbox cleared. No job
 * is submitted any more.
 */
static void c906l_stop(void)
{
	unsigned long flags;

	spin_lock_irqsave(&c906l.lock, flags);
	WRITE_ONCE(c906l.up, false);
	writeb(0, c906l.mbox + C906L_MBOX_EN(1));
	spin_unlock_irqrestore(&c906l.lock, flags);
	reset_control_assert(c906l.rst);
	free_irq(c906l.irq, &c906l);
	timer_delete_sync(&c906l.timer);
	flush_work(&c906l.bh);
	writeb(C906L_TO_SMALL | C906L_TO_BIG, c906l.mbox + C906L_MBOX_CLR(1));
	writeb(C906L_TO_SMALL | C906L_TO_BIG, c906l.mbox + C906L_MBOX_CLR(2));
}

static int c906l_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	size_t len = c906l_program_end - c906l_program;
	const struct c906l_status *st;
	int irq, i, err;

	if (c906l.dev)
		return -EBUSY;
	c906l.dev = dev;
	c906l.next = 1;
	c906l.done = 0;
	INIT_WORK(&c906l.bh, c906l_complete);
	timer_setup(&c906l.timer, c906l_timeout, 0);
	c906l.sec = devm_platform_ioremap_resource(pdev, 0);
	c906l.mbox = devm_platform_ioremap_resource(pdev, 1);
	irq = platform_get_irq(pdev, 0);
	c906l.clks[0].id = "efuse";
	c906l.clks[1].id = "apb-efuse";
	err = IS_ERR(c906l.sec) ? PTR_ERR(c906l.sec) : IS_ERR(c906l.mbox) ? PTR_ERR(c906l.mbox) : irq < 0 ? irq :
	      devm_clk_bulk_get(dev, 2, c906l.clks);
	if (err)
		goto fail;
	c906l.rst = devm_reset_control_get_exclusive(dev, NULL);
	if (IS_ERR(c906l.rst)) {
		err = PTR_ERR(c906l.rst);
		goto fail;
	}
	if (len > C906L_PROGRAM_MAX) {
		err = -E2BIG;
		goto fail;
	}
	err = -ENOMEM;
	c906l.mem = dmam_alloc_coherent(dev, C906L_MEM_SIZE, &c906l.mem_dma, GFP_KERNEL);
	c906l.ring = alloc_mapped(C906L_SLOTS * C906L_SLOT_SIZE, &c906l.ring_dma);
	c906l.stage = alloc_mapped(C906L_SLOTS * C906L_STAGE, &c906l.stage_dma);
	c906l.out = alloc_mapped(C906L_SLOTS * C906L_OUT, &c906l.out_dma);
	if (!c906l.mem || !c906l.ring || !c906l.stage || !c906l.out)
		goto unmap;
	memcpy(c906l.mem, c906l_program, len);
	((struct c906l_config *)(c906l.mem + C906L_CONFIG))->ring = c906l.ring_dma;
	/* Channel 0 to the small core, channel 1 to the big core; nothing else uses the mailbox. */
	writeb(C906L_TO_SMALL | C906L_TO_BIG, c906l.mbox + C906L_MBOX_CLR(2));
	writeb(C906L_TO_SMALL | C906L_TO_BIG, c906l.mbox + C906L_MBOX_CLR(1));
	writeb(C906L_TO_SMALL, c906l.mbox + C906L_MBOX_EN(2));
	writeb(C906L_TO_BIG, c906l.mbox + C906L_MBOX_EN(1));
	err = request_irq(irq, c906l_irq, 0, "nanokvm-c906l", &c906l);
	if (err)
		goto unmap;
	c906l.irq = irq;
	reset_control_assert(c906l.rst);
	err = clk_bulk_prepare_enable(2, c906l.clks);
	if (err)
		goto stop;
	writel(readl(c906l.sec + 4) | BIT(13), c906l.sec + 4);
	writel(lower_32_bits(c906l.mem_dma), c906l.sec + 0x20);
	writel(upper_32_bits(c906l.mem_dma), c906l.sec + 0x24);
	readl(c906l.sec + 0x24);
	clk_bulk_disable_unprepare(2, c906l.clks);
	wmb();
	reset_control_deassert(c906l.rst);
	st = (const struct c906l_status *)(c906l.mem + C906L_STATUS);
	for (i = 0; i < 100 && READ_ONCE(st->magic) != C906L_MAGIC; i++)
		msleep(1);
	if (READ_ONCE(st->magic) != C906L_MAGIC) {
		err = -ETIMEDOUT;
		goto stop;
	}
	WRITE_ONCE(c906l.up, true);
	err = misc_register(&opus_misc);
	if (err)
		goto stop;
	dev_info(dev, "small core up after %d ms, program %zu bytes at %pad\n", i, len, &c906l.mem_dma);
	return 0;
stop:
	c906l_stop();
unmap:
	c906l_unmap();
fail:
	dev_err(dev, "small core not started (%d)\n", err);
	memset(&c906l, 0, offsetof(typeof(c906l), lock));
	return err;
}

static void c906l_remove(struct platform_device *pdev)
{
	/* Removal only with the module unloaded (no bind/unbind in sysfs), so no file is open. */
	misc_deregister(&opus_misc);
	c906l_stop();
	c906l_unmap();
	memset(&c906l, 0, offsetof(typeof(c906l), lock));
}

static const struct of_device_id c906l_of[] = {
	{ .compatible = "nanokvm,sg2002-c906l" },
	{ }
};
MODULE_DEVICE_TABLE(of, c906l_of);

static struct platform_driver c906l_driver = {
	.probe = c906l_probe,
	.remove = c906l_remove,
	/* No bind/unbind in sysfs: the buffers of a running ioctl must outlive it. */
	.driver = { .name = "nanokvm-c906l", .of_match_table = c906l_of, .suppress_bind_attrs = true },
};
module_platform_driver(c906l_driver);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("SG2002 small core (C906L) as a coprocessor");
