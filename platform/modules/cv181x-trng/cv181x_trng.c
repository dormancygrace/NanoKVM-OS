// SPDX-License-Identifier: GPL-2.0-only
/* CV181x (SG2002) true random number generator as a hw_random device.
 *
 * The TRNG seeds a DRBG from its noise source and returns 128 bits per
 * GEN_RANDOM command (SG2002 TRM v1.02, 22.2). Each command waits until
 * STAT.BUSY is clear, runs, and is acknowledged by writing its ISTAT bit.
 *
 * Its registers are those of the Synopsys NIST TRNG core (COREKIT_REL 0x200a
 * at 0x1c), which the TRM documents only in part:
 * - ALARMS (0x18) and ZEROIZE (command 0xf, completion ISTAT bit 0) are not
 *   in the TRM;
 * - GEN_NOISE or RENEW_STATE on an instantiated DRBG raises the "illegal
 *   command sequence" alarm (ALARMS bit 4), after which the core ignores
 *   every command until the alarm is cleared and the core zeroized;
 * - ZEROIZE with an alarm still pending hung the bus on the stand.
 * So every seed clears ALARMS and ISTAT, zeroizes, and instantiates from
 * fresh noise, and every command checks ALARMS.
 *
 * The block sits behind the efuse clock gates, which Linux turns off as
 * unused: with them off, any access to its registers hangs the bus. The
 * device tree node names both clocks and they stay on while the driver is
 * bound.
 */
#include <linux/clk.h>
#include <linux/hw_random.h>
#include <linux/io.h>
#include <linux/iopoll.h>
#include <linux/module.h>
#include <linux/mod_devicetable.h>
#include <linux/platform_device.h>

#define TRNG_CTRL 0x00
#define TRNG_STAT 0x0c
#define TRNG_ISTAT 0x14
#define TRNG_ALARMS 0x18
#define TRNG_RAND0 0x24
#define TRNG_RAND_WORDS 4

#define CMD_GEN_NOISE 0x1
#define CMD_CREATE_STATE 0x3
#define CMD_GEN_RANDOM 0x6
#define CMD_ZEROIZE 0xf
#define STAT_BUSY BIT(31)
#define ISTAT_ZEROIZE BIT(0)
#define ISTAT_DONE BIT(4)

/* A fresh seed from the noise source after this many 128-bit outputs. */
#define RESEED_INTERVAL 1024
#define POLL_US 10
#define TIMEOUT_US 100000

struct cv181x_trng {
	struct hwrng rng;
	struct device *dev;
	void __iomem *base;
	unsigned int generated;
	bool seeded;
};

/* Run one command; completion is ISTAT bit done (ZEROIZE has its own). */
static int trng_command(struct cv181x_trng *trng, u32 cmd, u32 done)
{
	u32 val;
	int ret;

	ret = readl_poll_timeout(trng->base + TRNG_STAT, val, !(val & STAT_BUSY),
				 POLL_US, TIMEOUT_US);
	if (ret)
		return ret;
	writel(cmd, trng->base + TRNG_CTRL);
	ret = readl_poll_timeout(trng->base + TRNG_ISTAT, val, val & done,
				 POLL_US, TIMEOUT_US);
	writel(done, trng->base + TRNG_ISTAT);
	if (ret)
		return ret;
	val = readl(trng->base + TRNG_ALARMS);
	if (val) {
		dev_err(trng->dev, "alarm %#x after command %#x\n", val, cmd);
		return -EIO;
	}
	return 0;
}

/* Clear any alarm, then zeroize: the only state every command sequence
 * can start from. */
static int trng_zeroize(struct cv181x_trng *trng)
{
	writel(~0u, trng->base + TRNG_ALARMS);
	writel(~0u, trng->base + TRNG_ISTAT);
	return trng_command(trng, CMD_ZEROIZE, ISTAT_ZEROIZE);
}

static int trng_seed(struct cv181x_trng *trng)
{
	int ret;

	trng->seeded = false;
	ret = trng_zeroize(trng);
	if (!ret)
		ret = trng_command(trng, CMD_GEN_NOISE, ISTAT_DONE);
	if (!ret)
		ret = trng_command(trng, CMD_CREATE_STATE, ISTAT_DONE);
	if (ret)
		return ret;
	trng->generated = 0;
	trng->seeded = true;
	return 0;
}

static int cv181x_trng_init(struct hwrng *rng)
{
	return trng_seed(container_of(rng, struct cv181x_trng, rng));
}

/* Leave no DRBG state behind; the clocks are still on here. */
static void cv181x_trng_cleanup(struct hwrng *rng)
{
	struct cv181x_trng *trng = container_of(rng, struct cv181x_trng, rng);

	trng->seeded = false;
	trng_zeroize(trng);
}

static int cv181x_trng_read(struct hwrng *rng, void *buf, size_t max, bool wait)
{
	struct cv181x_trng *trng = container_of(rng, struct cv181x_trng, rng);
	u32 words[TRNG_RAND_WORDS];
	size_t done = 0, n;
	int ret = 0, i;

	while (done < max) {
		if (!trng->seeded || trng->generated >= RESEED_INTERVAL) {
			ret = trng_seed(trng);
			if (ret)
				break;
		}
		ret = trng_command(trng, CMD_GEN_RANDOM, ISTAT_DONE);
		if (ret) {
			/* Seed again before the next output. */
			trng->seeded = false;
			break;
		}
		trng->generated++;
		for (i = 0; i < TRNG_RAND_WORDS; i++)
			words[i] = readl(trng->base + TRNG_RAND0 + 4 * i);
		n = min(max - done, sizeof(words));
		memcpy(buf + done, words, n);
		done += n;
	}
	memzero_explicit(words, sizeof(words));
	return done ?: ret;
}

static int cv181x_trng_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	struct clk_bulk_data *clks;
	struct cv181x_trng *trng;
	int ret;

	trng = devm_kzalloc(dev, sizeof(*trng), GFP_KERNEL);
	if (!trng)
		return -ENOMEM;
	trng->dev = dev;
	/* Clocks first: the registers hang the bus while they are gated. */
	ret = devm_clk_bulk_get_all_enabled(dev, &clks);
	if (ret < 0)
		return dev_err_probe(dev, ret, "cannot enable the clocks\n");
	if (!ret)
		return dev_err_probe(dev, -EINVAL, "the device tree names no clocks\n");
	trng->base = devm_platform_ioremap_resource(pdev, 0);
	if (IS_ERR(trng->base))
		return PTR_ERR(trng->base);

	trng->rng.name = "cv181x-trng";
	trng->rng.init = cv181x_trng_init;
	trng->rng.cleanup = cv181x_trng_cleanup;
	trng->rng.read = cv181x_trng_read;
	/* Registered after the clocks, so unregistered (and zeroized) first. */
	ret = devm_hwrng_register(dev, &trng->rng);
	if (ret)
		return dev_err_probe(dev, ret, "cannot register\n");
	return 0;
}

static const struct of_device_id cv181x_trng_of_match[] = {
	{ .compatible = "nanokvm,cv181x-trng" },
	{}
};
MODULE_DEVICE_TABLE(of, cv181x_trng_of_match);

static struct platform_driver cv181x_trng_driver = {
	.probe = cv181x_trng_probe,
	.driver = {
		.name = "cv181x-trng",
		.of_match_table = cv181x_trng_of_match,
	},
};
module_platform_driver(cv181x_trng_driver);

MODULE_DESCRIPTION("CV181x true random number generator");
MODULE_LICENSE("GPL");
