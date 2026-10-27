/* SPDX-License-Identifier: GPL-2.0-only */
#define _GNU_SOURCE
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <sys/mman.h>
#include <limits.h>
#define N 2
static const char *names[N] = {"reference", "candidate"};
struct api {
    void *h, *ctx;
    void *(*create)(int);
    void (*destroy)(void *);
    size_t (*comp)(void *, const void *, size_t, void *, size_t);
    size_t (*decomp)(void *, const void *, size_t, void *, size_t);
    unsigned (*error)(size_t);
    unsigned long (*bits)(unsigned long, unsigned, unsigned);
};
static struct api a[N];
static uint64_t checks;
static unsigned rng = 0x9062026;
static unsigned rnd(void)
{
    unsigned x = rng;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    return rng = x;
}
#define REQUIRE(c)                                                                                 \
    do {                                                                                           \
        checks++;                                                                                  \
        if (!(c)) {                                                                                \
            fprintf(stderr, "FAIL line=%d check=%llu expression=%s\n", __LINE__,                   \
                    (unsigned long long)checks, #c);                                               \
            exit(1);                                                                               \
        }                                                                                          \
    } while (0)
static unsigned char *guard(unsigned pages)
{
    size_t n = (pages + 2) * 4096;
    unsigned char *p = mmap(0, n, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    REQUIRE(p != MAP_FAILED);
    REQUIRE(!mprotect(p + 4096, pages * 4096, PROT_READ | PROT_WRITE));
    return p + (pages + 1) * 4096;
}
static unsigned char *iend, *oend, *cend;
static unsigned char packed[N][8192];
static void decoder_case(const unsigned char *data, size_t n, size_t cap, int must_error)
{
    unsigned char reference[4096];
    size_t reference_n = 0;
    unsigned reference_error = 0;
    unsigned char *in = iend - n, *out = oend - cap;
    memcpy(in, data, n);
    for (unsigned v = 0; v < N; v++) {
        memset(out - 16, 0xa5, cap + 16);
        size_t z = a[v].decomp(a[v].ctx, in, n, out, cap);
        unsigned err = a[v].error(z);
        for (unsigned k = 1; k <= 16; k++)
            REQUIRE(out[-(int)k] == 0xa5);
        if (must_error) {
            if (!err)
                fprintf(stderr,
                        "unexpected accepted: variant=%s input=%zu capacity=%zu output=%zu\n",
                        names[v], n, cap, z);
            REQUIRE(err);
        }
        if (!n)
            REQUIRE(!err && z == 0);
        if (!err)
            REQUIRE(z <= cap);
        if (!v) {
            reference_n = z;
            reference_error = err;
            if (!err)
                memcpy(reference, out, z);
        } else {
            REQUIRE(err == reference_error);
            if (!err) {
                REQUIRE(z == reference_n);
                REQUIRE(!memcmp(reference, out, z));
            }
        }
    }
}
static void roundtrip(const unsigned char *page, size_t n, unsigned extra)
{
    size_t sizes[N];
    for (unsigned v = 0; v < N; v++) {
        sizes[v] = a[v].comp(a[v].ctx, page, n, packed[v], sizeof(packed[v]));
        REQUIRE(!a[v].error(sizes[v]));
        if (v) {
            REQUIRE(sizes[v] == sizes[0]);
            REQUIRE(!memcmp(packed[v], packed[0], sizes[0]));
        }
        for (unsigned d = 0; d < N; d++) {
            unsigned char *out = oend - n;
            size_t z = a[d].decomp(a[d].ctx, packed[v], sizes[v], out, n);
            REQUIRE(!a[d].error(z) && z == n);
            REQUIRE(!memcmp(out, page, n));
        }
    }
    decoder_case(packed[0], sizes[0], n, 0);
    if (extra) {
        for (size_t len = 0; len < sizes[0]; len++)
            decoder_case(packed[0], len, n, len != 0);
        if (n)
            decoder_case(packed[0], sizes[0], n - 1, 1);
        for (unsigned v = 0; v < N; v++)
            for (unsigned j = 0; j < 3; j++) {
                size_t cap = j == 0 ? 0 : j == 1 ? 1 : sizes[0] - 1;
                unsigned char *out = cend - cap;
                memset(out - 16, 0xa5, cap + 16);
                size_t z = a[v].comp(a[v].ctx, page, n, out, cap);
                REQUIRE(a[v].error(z));
                for (unsigned k = 1; k <= 16; k++)
                    REQUIRE(out[-(int)k] == 0xa5);
            }
    }
}
int main(int argc, char **argv)
{
    REQUIRE(argc == 3);
    char path[4096];
    for (unsigned v = 0; v < N; v++) {
        snprintf(path, sizeof(path), "%s/%s.so", argv[1], names[v]);
        a[v].h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
        if (!a[v].h) {
            fprintf(stderr, "%s\n", dlerror());
            return 1;
        }
#define LOAD(field, symbol)                                                                        \
    do {                                                                                           \
        *(void **)(&a[v].field) = dlsym(a[v].h, symbol);                                           \
        REQUIRE(a[v].field);                                                                       \
    } while (0)
        LOAD(create, "probe_create");
        LOAD(destroy, "probe_free");
        LOAD(comp, "probe_comp");
        LOAD(decomp, "probe_decomp");
        LOAD(error, "probe_error");
        LOAD(bits, "probe_bits");
    }
    iend = guard(2);
    oend = guard(2);
    cend = guard(2);
    for (unsigned n = 0; n <= 31; n++)
        for (unsigned consumed = 0; consumed + n <= 64; consumed++)
            for (unsigned k = 0; k < 128; k++) {
                unsigned long x = ((unsigned long)rnd() << 32) | rnd();
                unsigned long expected = n ? ((x >> (64 - consumed - n)) & ((1UL << n) - 1)) : 0;
                for (unsigned v = 0; v < N; v++)
                    REQUIRE(a[v].bits(x, consumed, n) == expected);
            }
    FILE *f = fopen(argv[2], "rb");
    REQUIRE(f);
    REQUIRE(!fseek(f, 0, SEEK_END));
    long sz = ftell(f);
    REQUIRE(sz > 0 && sz % 4096 == 0);
    rewind(f);
    unsigned char *corpus = malloc(sz);
    REQUIRE(corpus);
    REQUIRE(fread(corpus, 1, sz, f) == (size_t)sz);
    fclose(f);
    unsigned levels[] = {1, 3, 9};
    unsigned char raw[8192];
    for (unsigned level = 0; level < 3; level++) {
        for (unsigned v = 0; v < N; v++) {
            a[v].ctx = a[v].create(levels[level]);
            REQUIRE(a[v].ctx);
        }
        for (unsigned p = 0; p < (unsigned)sz / 4096; p++)
            roundtrip(corpus + p * 4096, 4096, p % 97 == 0);
        unsigned sizes[] = {0,   1,   2,   3,   4,    7,    8,    9,    15,   16,   17,
                            31,  32,  33,  63,  64,   65,   127,  128,  129,  255,  256,
                            257, 511, 512, 513, 1023, 1024, 1025, 2047, 2048, 2049, 4095};
        for (unsigned p = 0; p < 16; p++)
            for (unsigned n = 0; n < sizeof(sizes) / sizeof(sizes[0]); n++)
                roundtrip(corpus + ((p * 89) % ((unsigned)sz / 4096)) * 4096, sizes[n], p % 7 == 0);
        for (unsigned k = 0; k < 100000; k++) {
            size_t n;
            if (k & 1) {
                unsigned p = rnd() % ((unsigned)sz / 4096);
                n = a[0].comp(a[0].ctx, corpus + p * 4096, 4096, raw, sizeof(raw));
                REQUIRE(!a[0].error(n));
                unsigned mutations = 1 + rnd() % 4;
                while (mutations--)
                    raw[rnd() % n] ^= 1U << (rnd() % 8);
            } else {
                n = rnd() % 512;
                for (unsigned j = 0; j < n; j++)
                    raw[j] = rnd();
            }
            decoder_case(raw, n, rnd() % 4097, 0);
        }
        for (unsigned v = 0; v < N; v++)
            a[v].destroy(a[v].ctx);
        printf("level=%u PASS checks=%llu\n", levels[level], (unsigned long long)checks);
        fflush(stdout);
    }
    free(corpus);
    munmap(iend - 3 * 4096, 4 * 4096);
    munmap(oend - 3 * 4096, 4 * 4096);
    munmap(cend - 3 * 4096, 4 * 4096);
    for (unsigned v = 0; v < N; v++)
        dlclose(a[v].h);
    printf("PASS checks=%llu corpus_pages=%ld random_or_mutated=300000\n", (unsigned long long)checks,
           sz / 4096);
    return 0;
}
