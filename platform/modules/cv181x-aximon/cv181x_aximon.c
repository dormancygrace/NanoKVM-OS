// SPDX-License-Identifier: GPL-2.0-only
/* CV181x DDR bandwidth by AXI port, from the monitors in front of the DDR
 * controller.
 *
 * Six ports (video capture, video offline processing, CPU, TPU, video codec,
 * high-speed peripherals) each have a write and a read monitor, 0x80 apart.
 * Writing 0x40004 then 0x40000 to a monitor's control register latches its
 * byte counter and restarts it (register layout: Sophgo osdrv
 * interdrv/mon/hal/cv181x/mon_platform.c). The counters are 32 bits wide and
 * DDR moves a few GB/s, so they are latched every 500 ms into 64-bit totals.
 * Sampling starts with the first read of /proc/ddr_bandwidth:
 *
 *   elapsed_ns N
 *   PORT READ_BYTES WRITE_BYTES
 *
 * Two reads give MB/s per port.
 */
#include <linux/io.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/of.h>
#include <linux/of_address.h>
#include <linux/platform_device.h>
#include <linux/proc_fs.h>
#include <linux/seq_file.h>
#include <linux/workqueue.h>

#define PORTS 6
#define MONITOR_WRITE(port) ((port) * 0x100)
#define MONITOR_READ(port) ((port) * 0x100 + 0x80)
#define BYTE_COUNT 0x2c
#define INPUT_SELECT 0x01000100
#define SNAPSHOT_LATCH 0x40004
#define SNAPSHOT_RESTART 0x40000
/* ddr_top: clock gate enable of the AXI monitors (bit 8). */
#define AXI_CLOCK_GATE 0x14
#define AXIMON_CLOCK BIT(8)
#define PERIOD_MS 500

static const char *const port_names[PORTS] = {
	"vip_rt", "vip_off", "cpu", "tpu", "vc", "hsperi",
};

struct aximon {
	void __iomem *regs;
	struct delayed_work work;
	struct mutex lock;
	bool sampling;
	ktime_t last;
	u64 elapsed_ns;
	u64 read_bytes[PORTS];
	u64 write_bytes[PORTS];
};

static struct aximon *aximon;

static void aximon_latch(struct aximon *m, unsigned int offset)
{
	writel(SNAPSHOT_LATCH, m->regs + offset);
	writel(SNAPSHOT_RESTART, m->regs + offset);
}

/* Latch every monitor and add the counts since the previous latch. */
static void aximon_sample(struct aximon *m, bool count)
{
	ktime_t now;
	int port;

	for (port = 0; port < PORTS; port++) {
		aximon_latch(m, MONITOR_WRITE(port));
		aximon_latch(m, MONITOR_READ(port));
	}
	now = ktime_get();
	if (count) {
		m->elapsed_ns += ktime_to_ns(ktime_sub(now, m->last));
		for (port = 0; port < PORTS; port++) {
			m->write_bytes[port] += readl(m->regs + MONITOR_WRITE(port) + BYTE_COUNT);
			m->read_bytes[port] += readl(m->regs + MONITOR_READ(port) + BYTE_COUNT);
		}
	}
	m->last = now;
}

static void aximon_work(struct work_struct *work)
{
	struct aximon *m = container_of(to_delayed_work(work), struct aximon, work);

	mutex_lock(&m->lock);
	aximon_sample(m, true);
	mutex_unlock(&m->lock);
	schedule_delayed_work(&m->work, msecs_to_jiffies(PERIOD_MS));
}

static int aximon_show(struct seq_file *s, void *unused)
{
	struct aximon *m = aximon;
	int port;

	mutex_lock(&m->lock);
	if (!m->sampling) {
		m->sampling = true;
		aximon_sample(m, false);
		schedule_delayed_work(&m->work, msecs_to_jiffies(PERIOD_MS));
	} else {
		aximon_sample(m, true);
	}
	seq_printf(s, "elapsed_ns %llu\n", m->elapsed_ns);
	for (port = 0; port < PORTS; port++)
		seq_printf(s, "%s %llu %llu\n", port_names[port], m->read_bytes[port], m->write_bytes[port]);
	mutex_unlock(&m->lock);
	return 0;
}

static int aximon_probe(struct platform_device *pdev)
{
	struct aximon *m;
	void __iomem *top;
	int port;

	if (aximon)
		return -EBUSY;
	m = devm_kzalloc(&pdev->dev, sizeof(*m), GFP_KERNEL);
	if (!m)
		return -ENOMEM;
	m->regs = devm_platform_ioremap_resource(pdev, 0);
	if (IS_ERR(m->regs))
		return PTR_ERR(m->regs);
	/* ddr_top is shared with the DDR setup; map it without claiming it. */
	top = of_iomap(pdev->dev.of_node, 1);
	if (!top)
		return -ENOMEM;
	writel(readl(top + AXI_CLOCK_GATE) | AXIMON_CLOCK, top + AXI_CLOCK_GATE);
	iounmap(top);
	for (port = 0; port < PORTS; port++) {
		writel(INPUT_SELECT, m->regs + MONITOR_WRITE(port));
		writel(INPUT_SELECT, m->regs + MONITOR_READ(port));
	}
	mutex_init(&m->lock);
	INIT_DELAYED_WORK(&m->work, aximon_work);
	platform_set_drvdata(pdev, m);
	aximon = m;
	if (!proc_create_single("ddr_bandwidth", 0444, NULL, aximon_show)) {
		aximon = NULL;
		return -ENOMEM;
	}
	return 0;
}

static void aximon_remove(struct platform_device *pdev)
{
	struct aximon *m = platform_get_drvdata(pdev);

	remove_proc_entry("ddr_bandwidth", NULL);
	cancel_delayed_work_sync(&m->work);
	aximon = NULL;
}

static const struct of_device_id aximon_of_match[] = {
	{ .compatible = "nanokvm,cv181x-aximon" },
	{ }
};
MODULE_DEVICE_TABLE(of, aximon_of_match);

static struct platform_driver aximon_driver = {
	.probe = aximon_probe,
	.remove = aximon_remove,
	.driver = {
		.name = "cv181x-aximon",
		.of_match_table = aximon_of_match,
	},
};
module_platform_driver(aximon_driver);

MODULE_DESCRIPTION("CV181x DDR bandwidth by AXI port");
MODULE_LICENSE("GPL");
