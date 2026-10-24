// SPDX-License-Identifier: GPL-2.0-only
/* One-shot LT6911C/D video policy. Load before starting either video service. */
#include <linux/clk.h>
#include <linux/module.h>
#include <linux/of.h>
#include <dt-bindings/clock/sophgo,cv1800.h>
#include "profile.h"

static char *receiver;
module_param(receiver, charp, 0444);
MODULE_PARM_DESC(receiver, "Must be c or d; other receivers retain their DT clocks");
static bool ready;
module_param(ready, bool, 0444);
static struct video_clock clocks[] = {
    { .rate = 396000000, .parent_rate = 1188000000 },
    { .rate = 360000000, .parent_rate = 1080000000 },
    { .rate = 300000000, .parent_rate = 900000000 },
};
static const unsigned int clock_ids[] = {
    CLK_VC_SRC0, CLK_AXI_VIDEO_CODEC, CLK_SRC_VIP_SYS_1,
};
static const unsigned int parent_ids[] = {
    CLK_DISPPLL, CLK_CAM1PLL, CLK_MIPIMPLL,
};
static unsigned int held_parents;

static int rates_get(char *buffer, const struct kernel_param *kp)
{
    (void)kp;
    return sysfs_emit(buffer, "%lu %lu %lu\n",
                      clk_get_rate(clocks[0].clock),
                      clk_get_rate(clocks[1].clock),
                      clk_get_rate(clocks[2].clock));
}
static const struct kernel_param_ops rates_ops = { .get = rates_get };
module_param_cb(rates, &rates_ops, NULL, 0444);
MODULE_PARM_DESC(rates, "Live vc_src0 axi_video_codec src_vip_sys_1 rates in Hz");

static struct clk *provider_clock(struct device_node *node, unsigned int id)
{
    struct of_phandle_args args = { .np = node, .args_count = 1, .args = { id } };
    return of_clk_get_from_provider(&args);
}

static void release_parents(void)
{
    while (held_parents)
        clk_rate_exclusive_put(clocks[--held_parents].parent);
}

static void release_clocks(void)
{
    unsigned int i;
    release_parents();
    for (i = 0; i < ARRAY_SIZE(clocks); i++) {
        if (!IS_ERR_OR_NULL(clocks[i].clock))
            clk_put(clocks[i].clock);
        if (!IS_ERR_OR_NULL(clocks[i].parent))
            clk_put(clocks[i].parent);
    }
}

static int __init video_clocks_init(void)
{
    struct device_node *node;
    unsigned int i;
    int ret;

    /* No alias/autoload: the userspace model gate loads this explicitly. */
    if (!receiver || (strcmp(receiver, "c") && strcmp(receiver, "d")) ||
        !of_machine_is_compatible("sipeed,nanokvm") ||
        !of_machine_is_compatible("sophgo,sg2002"))
        return -ENODEV;
    node = of_find_compatible_node(NULL, NULL, "sophgo,sg2000-clk");
    if (!node)
        return -ENODEV;
    for (i = 0; i < ARRAY_SIZE(clocks); i++) {
        clocks[i].clock = provider_clock(node, clock_ids[i]);
        clocks[i].parent = provider_clock(node, parent_ids[i]);
    }
    of_node_put(node);
    for (i = 0; i < ARRAY_SIZE(clocks); i++) {
        if (IS_ERR(clocks[i].clock) || IS_ERR(clocks[i].parent)) {
            ret = -ENODEV;
            goto fail;
        }
        ret = clk_rate_exclusive_get(clocks[i].parent);
        if (ret)
            goto fail;
        held_parents++;
    }
    ret = apply_cd_stock(clocks, ARRAY_SIZE(clocks));
    if (ret)
        goto fail;
    /* Guard only the stopped-service transition. Child exclusivity would
     * recursively pin these PLLs too, interfering with unrelated consumers.
     * Native admission verifies the live child rates again before capture.
     */
    release_parents();
    ready = true;
    pr_info("sg2002-video-clocks: LT6911%s stock: 396/360/300 MHz\n", receiver);
    return 0;
fail:
    /* A failed setup may leave lower clocks. Do not claim the profile or let
     * startup use it. A reboot restores the boot DT; no live overclock rollback.
     */
    pr_err("sg2002-video-clocks: C/D profile failed: %d\n", ret);
    release_clocks();
    return ret;
}
module_init(video_clocks_init);

static void __exit video_clocks_exit(void)
{
    release_clocks();
}
module_exit(video_clocks_exit);
MODULE_DESCRIPTION("NanoKVM LT6911C/D stock video clocks");
MODULE_LICENSE("GPL");
