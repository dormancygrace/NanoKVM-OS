/* SPDX-License-Identifier: GPL-2.0-only */
#include <stdlib.h>
#include <linux/zstd.h>
#include "common/bitstream.h"
struct probe {
    void *cm, *dm;
    zstd_cctx *c;
    zstd_dctx *d;
    zstd_parameters p;
};
void *probe_create(int level)
{
    struct probe *p = calloc(1, sizeof(*p));
    if (!p)
        return 0;
    p->p = zstd_get_params(level, 4096);
    size_t cs = zstd_cctx_workspace_bound(&p->p.cParams), ds = zstd_dctx_workspace_bound();
    if (posix_memalign(&p->cm, 64, cs) || posix_memalign(&p->dm, 64, ds))
        goto fail;
    p->c = zstd_init_cctx(p->cm, cs);
    p->d = zstd_init_dctx(p->dm, ds);
    if (p->c && p->d)
        return p;
fail:
    free(p->cm);
    free(p->dm);
    free(p);
    return 0;
}
void probe_free(struct probe *p)
{
    free(p->cm);
    free(p->dm);
    free(p);
}
size_t probe_comp(struct probe *p, const void *s, size_t n, void *d, size_t cap)
{
    return zstd_compress_cctx(p->c, d, cap, s, n, &p->p);
}
size_t probe_decomp(struct probe *p, const void *s, size_t n, void *d, size_t cap)
{
    return zstd_decompress_dctx(p->d, d, cap, s, n);
}
unsigned probe_error(size_t n) { return zstd_is_error(n); }
unsigned long probe_bits(unsigned long x, unsigned consumed, unsigned n)
{
    BIT_DStream_t b = {.bitContainer = x, .bitsConsumed = consumed};
    return BIT_lookBits(&b, n);
}
