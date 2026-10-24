/* SPDX-License-Identifier: GPL-2.0-only */
/* Shared with the host fault-injection test; only CCF calls touch hardware. */
struct video_clock {
    struct clk *clock;
    struct clk *parent;
    unsigned long rate;
    unsigned long parent_rate;
};

/* The fixed-point CAM1 synthesizer is reported as 1079999999 Hz by CCF.
 * Accept its one-Hz representation error; dividers must still read exactly.
 */
static int parent_rate_matches(struct video_clock *c)
{
    unsigned long actual = clk_get_rate(c->parent);
    return actual >= c->parent_rate - 1 && actual <= c->parent_rate + 1;
}

static int apply_cd_stock(struct video_clock *clocks, unsigned int count)
{
    unsigned int i;
    int ret;

    /* Validate the fixed PLLs before touching any divider. Never retune one. */
    for (i = 0; i < count; i++)
        if (!parent_rate_matches(&clocks[i]))
            return -EINVAL;

    for (i = 0; i < count; i++) {
        struct video_clock *c = &clocks[i];
        struct clk *old_parent = clk_get_parent(c->clock);
        unsigned long bridge = (clk_get_rate(old_parent) + 5) / 6;

        /* Changing AXI from 900/2 to 1080/3 must not pass through 1080/2.
         * First choose /6 on the old PLL, then switch, then set the target.
         */
        if (!bridge || bridge > c->rate ||
            clk_round_rate(c->clock, bridge) != (long)bridge)
            return -EINVAL;
        ret = clk_set_rate(c->clock, bridge);
        if (ret)
            return ret;
        if (clk_get_rate(c->clock) != bridge)
            return -EIO;
        ret = clk_set_parent(c->clock, c->parent);
        if (ret)
            return ret;
        if (!clk_is_match(clk_get_parent(c->clock), c->parent) ||
            clk_get_rate(c->clock) > c->rate)
            return -EIO;
        if (clk_round_rate(c->clock, c->rate) != (long)c->rate)
            return -EINVAL;
        ret = clk_set_rate(c->clock, c->rate);
        if (ret)
            return ret;
        if (clk_get_rate(c->clock) != c->rate ||
            !parent_rate_matches(c))
            return -EIO;
    }
    return 0;
}
