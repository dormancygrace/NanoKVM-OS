/* SPDX-License-Identifier: GPL-2.0-only */
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <pthread.h>
#include <linux/kernel.h>
#include <linux/slab.h>
#include <linux/zstd.h>
#include "backend_zstd.h"
static unsigned long checks, calls, fail_at, live, peak;
static pthread_mutex_t alloc_lock = PTHREAD_MUTEX_INITIALIZER;
struct allocation {
    size_t bytes;
    uint64_t magic, pad[6];
};
#define CHECK(x)                                                                                   \
    do {                                                                                           \
        checks++;                                                                                  \
        if (!(x)) {                                                                                \
            fprintf(stderr, "FAIL line %d: %s live=%lu calls=%lu fail=%lu\n", __LINE__, #x, live,  \
                    calls, fail_at);                                                               \
            exit(1);                                                                               \
        }                                                                                          \
    } while (0)
void *host_alloc(size_t n)
{
    pthread_mutex_lock(&alloc_lock);
    unsigned long call = ++calls;
    int fail = fail_at && call == fail_at;
    pthread_mutex_unlock(&alloc_lock);
    if (fail)
        return NULL;
    struct allocation *h = calloc(1, sizeof(*h) + n);
    if (!h)
        return NULL;
    h->bytes = n;
    h->magic = 0x9062026;
    pthread_mutex_lock(&alloc_lock);
    live += n;
    if (live > peak)
        peak = live;
    pthread_mutex_unlock(&alloc_lock);
    return h + 1;
}
void host_free(void *p)
{
    if (!p)
        return;
    struct allocation *h = (struct allocation *)p - 1;
    pthread_mutex_lock(&alloc_lock);
    if (h->magic != 0x9062026 || h->bytes > live)
        abort();
    live -= h->bytes;
    h->magic = 0;
    pthread_mutex_unlock(&alloc_lock);
    free(h);
}
static unsigned char page[4096], dict[4096], packed[8192], decoded[4096];
static unsigned long exercise(int level, size_t dict_size, unsigned fail, int report, int null_dict)
{
    struct zcomp_params params = {
        .level = level, .dict = dict_size && !null_dict ? dict : NULL, .dict_sz = dict_size};
    struct zcomp_ctx ctx = {0};
    struct zcomp_req req = {.src = page, .src_len = 4096, .dst = packed, .dst_len = 8192};
    int ret, created = 0;
    unsigned long total;
    CHECK(live == 0);
    calls = 0;
    peak = 0;
    fail_at = fail;
    ret = backend_zstd.setup_params(&params);
    if (!ret) {
        if (!fail) {
#ifdef EXPECT_EMPTY_DICT_ALLOCATIONS
            CHECK(calls == (dict_size ? 3 : EXPECT_EMPTY_DICT_ALLOCATIONS));
#endif
        }
        if (report)
            printf("level=%d dict=%zu setup_requested=%lu setup_allocations=%lu\n", level,
                   dict_size, live, calls);
        ret = backend_zstd.create_ctx(&params, &ctx);
        created = !ret;
        if (!ret) {
            ret = backend_zstd.compress(&params, &ctx, &req);
            if (!ret) {
                struct zcomp_req d = {
                    .src = packed, .src_len = req.dst_len, .dst = decoded, .dst_len = 4096};
                ret = backend_zstd.decompress(&params, &ctx, &d);
                if (!ret)
                    CHECK(!memcmp(page, decoded, 4096));
            }
        }
    }
    total = calls;
    if (!fail)
        CHECK(!ret);
    if (created)
        backend_zstd.destroy_ctx(&ctx);
    backend_zstd.release_params(&params);
    backend_zstd.release_params(&params);
    CHECK(live == 0);
    return total;
}
struct concurrent {
    struct zcomp_params *p;
    struct zcomp_ctx *c;
    size_t size;
    int error;
    unsigned char last[4096], output[8192];
    size_t out_size;
};
static void *writer(void *opaque)
{
    struct concurrent *x = opaque;
    for (unsigned i = 0; i < 2000; i++) {
        memcpy(x->last, page, 4096);
        x->last[i % 4096] ^= (i + 1);
        struct zcomp_req r = {.src = x->last, .src_len = 4096, .dst = x->output, .dst_len = 8192};
        if (backend_zstd.compress(x->p, x->c, &r)) {
            x->error = 1;
            break;
        }
        x->out_size = r.dst_len;
    }
    return NULL;
}
static void *reader(void *opaque)
{
    struct concurrent *x = opaque;
    unsigned char out[4096];
    for (unsigned i = 0; i < 2000; i++) {
        struct zcomp_req r = {.src = packed, .src_len = x->size, .dst = out, .dst_len = 4096};
        if (backend_zstd.decompress(x->p, x->c, &r) || memcmp(out, page, 4096)) {
            x->error = 2;
            break;
        }
    }
    return NULL;
}
int main(void)
{
    for (unsigned i = 0; i < 4096; i++) {
        page[i] = (i % 127) ^ (i / 512);
        dict[i] = (i * 17 + i / 17) & 255;
    }
    int levels[] = {ZCOMP_PARAM_NOT_SET, 1, 3, 9, 22};
    size_t sizes[] = {0, 64, 4096};
    for (unsigned l = 0; l < 5; l++)
        for (unsigned d = 0; d < 3; d++) {
            unsigned long total = exercise(levels[l], sizes[d], 0, 1, 0);
            for (unsigned f = 1; f <= total; f++)
                exercise(levels[l], sizes[d], f, 0, 0);
        }
    /* A nonzero length must retain dictionary objects even for a NULL pointer. */
    for (unsigned l = 0; l < 5; l++)
        exercise(levels[l], 64, 0, 0, 1);
    for (unsigned d = 0; d < 3; d++) {
        struct zcomp_params p = {.level = 3, .dict = sizes[d] ? dict : NULL, .dict_sz = sizes[d]};
        struct zcomp_ctx c = {0};
        fail_at = 0;
        CHECK(!backend_zstd.setup_params(&p));
        CHECK(!backend_zstd.create_ctx(&p, &c));
        struct zcomp_req r = {.src = page, .src_len = 4096, .dst = packed, .dst_len = 8192};
        CHECK(!backend_zstd.compress(&p, &c, &r));
        struct zcomp_req dr = {
            .src = packed, .src_len = r.dst_len, .dst = decoded, .dst_len = 4096};
        CHECK(!backend_zstd.decompress(&p, &c, &dr));
        struct concurrent rd = {.p = &p, .c = &c, .size = r.dst_len},
                          wr = {.p = &p, .c = &c, .size = r.dst_len};
        pthread_t rt, wt;
        CHECK(!pthread_create(&rt, NULL, reader, &rd));
        CHECK(!pthread_create(&wt, NULL, writer, &wr));
        CHECK(!pthread_join(rt, NULL));
        CHECK(!pthread_join(wt, NULL));
        CHECK(!rd.error && !wr.error);
        struct zcomp_req last = {
            .src = wr.output, .src_len = wr.out_size, .dst = decoded, .dst_len = 4096};
        CHECK(!backend_zstd.decompress(&p, &c, &last));
        CHECK(!memcmp(decoded, wr.last, 4096));
        backend_zstd.destroy_ctx(&c);
        backend_zstd.release_params(&p);
        CHECK(!live);
    }
    for (unsigned i = 0; i < 2; i++) {
        struct zcomp_params p = {.level = i ? zstd_max_clevel() + 1 : zstd_min_clevel() - 1};
        fail_at = 0;
        CHECK(backend_zstd.setup_params(&p) < 0);
        backend_zstd.release_params(&p);
        CHECK(!live);
    }
    printf("PASS checks=%lu concurrent_roundtrips=12000 failure_cleanup=all_allocation_points\n",
           checks);
    return 0;
}
