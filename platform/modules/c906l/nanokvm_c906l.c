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
 * Users reserve a slot of the job ring (c906l.h) with its staging buffer, put
 * their input there and submit the job; the small core does it and raises the
 * mailbox; the interrupt hands the results to a bottom-half work item, which
 * gives them to the users in the order of the jobs. The interrupt stays masked
 * while the work item runs, so under load one interrupt serves many results.
 *
 * A job not answered within C906L_TIMEOUT_MS gives the small core up: it is
 * held in reset, and every open and later job is handed back to its user
 * (done() with NULL), who then does the work itself. The small core writes
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

#include "c906l.h"
#include "nanokvm_c906l.h"

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
	unsigned long since[C906L_SLOTS];
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

bool c906l_reserve(struct c906l_owner *o)
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
EXPORT_SYMBOL_GPL(c906l_reserve);

/*
 * The job of a reserved slot: job (seq not set) with stage bytes of input
 * in the staging buffer. Never fails; the result arrives through o->done().
 */
static void c906l_submit(struct c906l_owner *o, struct c906l_job *job, u32 stage)
{
	u64 seq = o->seq;
	u32 i = seq % C906L_SLOTS;
	dma_addr_t slot = c906l.ring_dma + i * C906L_SLOT_SIZE;
	struct c906l_job *j = slot_job(seq);
	unsigned long flags;

	if (stage)
		dma_sync_single_for_device(c906l.dev, c906l.stage_dma + i * C906L_STAGE, stage, DMA_TO_DEVICE);
	/* the output buffer is the small core's until the result */
	dma_sync_single_for_device(c906l.dev, c906l.out_dma + i * C906L_OUT, C906L_OUT, DMA_FROM_DEVICE);
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

void c906l_submit_ghash(struct c906l_owner *o, const u8 *h, const u64 *acc, const u8 *lens, u32 len)
{
	struct c906l_job j = { .type = C906L_JOB_GHASH };

	memcpy(j.ghash.h, h, 16);
	j.ghash.acc[0] = acc[0];
	j.ghash.acc[1] = acc[1];
	memcpy(j.ghash.lens, lens, 16);
	j.ghash.addr = c906l.stage_dma + (o->seq % C906L_SLOTS) * C906L_STAGE;
	j.ghash.len = len;
	c906l_submit(o, &j, len);
}
EXPORT_SYMBOL_GPL(c906l_submit_ghash);

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
		u64 seq, acc[2];
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
				writeb(C906L_TO_BIG, c906l.mbox + C906L_MBOX_EN(1));
				unmasked = true;
				continue;
			}
			rmb();
			acc[0] = r->acc[0];
			acc[1] = r->acc[1];
			value = r->value;
			have = true;
			jobs++;
		}
		o = c906l.owner[i];
		spin_unlock_irqrestore(&c906l.lock, flags);
		if (have)
			dma_sync_single_for_cpu(c906l.dev, c906l.out_dma + i * C906L_OUT, C906L_OUT, DMA_FROM_DEVICE);
		if (o)
			o->done(o, have ? acc : NULL, value);
		spin_lock_irqsave(&c906l.lock, flags);
		c906l.owner[i] = NULL;
		c906l.submitted[i] = false;
		c906l.done = seq;
		spin_unlock_irqrestore(&c906l.lock, flags);
	}
	if (!unmasked)
		writeb(C906L_TO_BIG, c906l.mbox + C906L_MBOX_EN(1));
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
	err = devm_request_irq(dev, irq, c906l_irq, 0, "nanokvm-c906l", NULL);
	if (err)
		goto unmap;
	reset_control_assert(c906l.rst);
	err = clk_bulk_prepare_enable(2, c906l.clks);
	if (err)
		goto unmap;
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
		reset_control_assert(c906l.rst);
		err = -ETIMEDOUT;
		goto unmap;
	}
	WRITE_ONCE(c906l.up, true);
	dev_info(dev, "small core up after %d ms, program %zu bytes at %pad\n", i, len, &c906l.mem_dma);
	return 0;
unmap:
	c906l_unmap();
fail:
	dev_err(dev, "small core not started (%d)\n", err);
	memset(&c906l, 0, offsetof(typeof(c906l), lock));
	return err;
}

static void c906l_remove(struct platform_device *pdev)
{
	c906l_give_up("driver removed");
	timer_delete_sync(&c906l.timer);
	flush_work(&c906l.bh);
	reset_control_assert(c906l.rst);
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
	.driver = { .name = "nanokvm-c906l", .of_match_table = c906l_of },
};
module_platform_driver(c906l_driver);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("SG2002 small core (C906L) as a coprocessor");
