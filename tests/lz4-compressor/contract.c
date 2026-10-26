#define _GNU_SOURCE
#include <assert.h>
#include <dlfcn.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define STATE 16416
#define MAXN 262144
#define CAP (MAXN + MAXN / 255 + 32)
struct codec {
    int (*fast)(const char *, char *, int, int, int, void *);
    int (*normal)(const char *, char *, int, int, void *);
    int (*dest)(const char *, char *, int *, int, void *);
    void (*reset)(void *);
    int (*load)(void *, const char *, int);
    int (*save)(void *, char *, int);
    int (*cont)(void *, const char *, char *, int, int, int);
} c;
static int (*decode)(const char *, char *, int, int);
static int (*dictdecode)(const char *, char *, int, int, const char *, int);
static unsigned char src[MAXN + 64], saved[MAXN + 64], out[CAP + 64], plain[MAXN + 64];
static unsigned char history[MAXN], safe[2][65536];
static uint64_t state_words[(STATE + 32) / 8], clone_words[(STATE + 32) / 8];
static uint32_t random_state = 0x9062026;
static unsigned checks;
#define CHECK(x)                                                                                   \
    do {                                                                                           \
        checks++;                                                                                  \
        if (!(x)) {                                                                                \
            fprintf(stderr, "FAIL line %d: %s\n", __LINE__, #x);                                   \
            abort();                                                                               \
        }                                                                                          \
    } while (0)
static uint32_t rnd(void) {
    uint32_t x = random_state;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    return random_state = x;
}
static void fill(unsigned char *p, int n, int mode) {
    for (int i = 0; i < n; i++) {
        uint32_t r = rnd();
        p[i] = mode == 0   ? r
               : mode == 1 ? (i % 31 ? 0 : r)
               : mode == 2 ? (uint32_t)(i % 13)
                           : (i % 128 < 100 ? (uint32_t)(i % 17) : r);
    }
}
static void guards(unsigned char *p, int n) {
    for (int i = 0; i < 16; i++)
        CHECK(p[n + i] == 0xA5);
}
static void state_guard(void) { guards((void *)state_words, STATE); }
static void one(int n, int alignment, int mode, int acceleration) {
    unsigned char *input = src + alignment, *packed = out + alignment, *result = plain + alignment;
    int cap = n + n / 255 + 16;
    fill(input, n, mode);
    memcpy(saved, input, n);
    memset(out, 0xA5, sizeof(out));
    memset(state_words, 0xA5, sizeof(state_words));
    int size = c.fast((char *)input, (char *)packed, n, cap, acceleration, state_words);
    CHECK(size > 0 && size <= cap);
    CHECK(decode((char *)packed, (char *)result, size, n) == n);
    CHECK(!memcmp(input, result, n));
    CHECK(!memcmp(input, saved, n));
    guards(packed, cap);
    state_guard();
    if (n <= 256 || n == 4096) {
        int limits[] = {0, 1, 7, size - 1, size};
        for (unsigned k = 0; k < sizeof(limits) / sizeof(*limits); k++) {
            memset(out, 0xA5, sizeof(out));
            int limit = limits[k];
            int got = c.fast((char *)input, (char *)packed, n, limit, acceleration, state_words);
            CHECK(got >= 0 && got <= limit);
            guards(packed, limit);
            state_guard();
            if (got) {
                CHECK(decode((char *)packed, (char *)result, got, n) == n);
                CHECK(!memcmp(input, result, n));
            }
        }
    }
    if (acceleration == 1) {
        int got = c.normal((char *)input, (char *)packed, n, cap, state_words);
        CHECK(got == size);
        CHECK(decode((char *)packed, (char *)result, got, n) == n);
        CHECK(!memcmp(input, result, n));
    }
}
static void fixed_output(void) {
    const int lengths[] = {0, 1, 7, 15, 255, 4096, 65535, 65536, 65549, 262144};
    const int capacities[] = {0, 1, 2, 7, 16, 31, 128, 512, 4096, 65536};
    for (int mode = 0; mode < 4; mode++)
        for (unsigned l = 0; l < sizeof(lengths) / sizeof(*lengths); l++) {
            int n = lengths[l];
            fill(src, n, mode);
            for (unsigned j = 0; j < sizeof(capacities) / sizeof(*capacities); j++) {
                int cap = capacities[j], consumed = n;
                memset(out, 0xA5, sizeof(out));
                memset(state_words, 0xA5, sizeof(state_words));
                int got = c.dest((char *)src, (char *)out, &consumed, cap, state_words);
                CHECK(got >= 0 && got <= cap);
                guards(out, cap);
                state_guard();
                if (got) {
                    CHECK(consumed >= 0 && consumed <= n);
                    CHECK(dictdecode((char *)out, (char *)plain, got, consumed, NULL, 0) ==
                          consumed);
                    CHECK(!memcmp(src, plain, consumed));
                }
            }
        }
}
static void invalid_workspace(void) {
    for (unsigned offset = 0; offset < 8; offset++) {
        void *workspace = offset ? (unsigned char *)state_words + offset : NULL;
        for (int api = 0; api < 3; api++) {
            memset(state_words, 0xA5, sizeof(state_words));
            memset(out, 0xA5, sizeof(out));
            int consumed = 4096;
            int result = api == 0 ? c.fast((char *)src, (char *)out, consumed, CAP, 1, workspace)
                       : api == 1 ? c.normal((char *)src, (char *)out, consumed, CAP, workspace)
                       : c.dest((char *)src, (char *)out, &consumed, 128, workspace);
            CHECK(result == 0);
            CHECK(consumed == 4096);
            for (unsigned i = 0; i < sizeof(state_words); i++) CHECK(((unsigned char *)state_words)[i] == 0xA5);
            for (unsigned i = 0; i < sizeof(out); i++) CHECK(out[i] == 0xA5);
        }
    }
}
static void streams(void) {
    const int dictionaries[] = {0, 1, 3, 7, 8, 31, 4096, 65535, 65536, 70000};
    const int chunks[] = {0, 1, 3, 7, 4096, 65535, 4096, 65536, 8192};
    for (int mode = 0; mode < 4; mode++)
        for (unsigned d = 0; d < sizeof(dictionaries) / sizeof(*dictionaries); d++)
            for (int saving = 0; saving < 2; saving++) {
                int ds = dictionaries[d], pos = ds;
                fill(history, sizeof(history), mode);
                memset(state_words, 0xA5, sizeof(state_words));
                c.reset(state_words);
                state_guard();
                for (int i = 0; i < STATE; i++)
                    CHECK(((unsigned char *)state_words)[i] == 0);
                int loaded = c.load(state_words, (char *)history, ds);
                CHECK(loaded >= 0 && loaded <= ds && loaded <= 65536);
                state_guard();
                for (unsigned k = 0; k < sizeof(chunks) / sizeof(*chunks); k++) {
                    int n = chunks[k];
                    CHECK(pos + n < MAXN);
                    memset(out, 0xA5, sizeof(out));
                    int got = c.cont(state_words, (char *)history + pos, (char *)out, n, CAP, 1);
                    CHECK(got > 0);
                    state_guard();
                    int old = pos > 65536 ? 65536 : pos;
                    CHECK(dictdecode((char *)out, (char *)plain, got, n,
                                     (char *)history + pos - old, old) == n);
                    CHECK(!memcmp(history + pos, plain, n));
                    pos += n;
                    if (saving) {
                        int limit = k % 2 ? 4096 : 65536;
                        int kept = c.save(state_words, (char *)safe[k % 2], limit);
                        CHECK(kept >= 0 && kept <= limit && kept <= pos);
                        CHECK(!memcmp(safe[k % 2], history + pos - kept, kept));
                        state_guard();
                    }
                }
                /* zram's dictionary mode clones the initialized opaque state per page. */
                c.load(state_words, (char *)history, ds);
                for (int k = 0; k < 16; k++) {
                    memcpy(clone_words, state_words, sizeof(state_words));
                    int pos = ds + k * 4096;
                    int got = c.cont(clone_words, (char *)history + pos, (char *)out, 4096, CAP, 1);
                    CHECK(got > 0);
                    CHECK(dictdecode((char *)out, (char *)plain, got, 4096, (char *)history, ds) ==
                          4096);
                    CHECK(!memcmp(history + pos, plain, 4096));
                    guards((void *)clone_words, STATE);
                }
            }
}
static void long_stream(void) {
    fill(src, 65536, 2);
    c.reset(state_words);
    /* Cross 2GiB of cumulative stream offsets without allocating 2GiB. */
    for (unsigned i = 0; i < 32772; i++) {
        int got = c.cont(state_words, (char *)src, (char *)out, 65536, CAP, 1);
        CHECK(got > 0);
        CHECK(dictdecode((char *)out, (char *)plain, got, 65536, (char *)src, 65536) == 65536);
        CHECK(!memcmp(src, plain, 65536));
    }
    state_guard();
}
static void *symbol(void *h, const char *n) {
    void *p = dlsym(h, n);
    if (!p) {
        fprintf(stderr, "missing %s: %s\n", n, dlerror());
        exit(1);
    }
    return p;
}
int main(int argc, char **argv) {
    CHECK(argc == 3);
    void *lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL),
         *oracle = dlopen(argv[2], RTLD_NOW | RTLD_LOCAL);
    if (!lib || !oracle) {
        fprintf(stderr, "dlopen: %s\n", dlerror());
        return 1;
    }
    c.fast = symbol(lib, "LZ4_compress_fast");
    c.normal = symbol(lib, "LZ4_compress_default");
    c.dest = symbol(lib, "LZ4_compress_destSize");
    c.reset = symbol(lib, "LZ4_resetStream");
    c.load = symbol(lib, "LZ4_loadDict");
    c.save = symbol(lib, "LZ4_saveDict");
    c.cont = symbol(lib, "LZ4_compress_fast_continue");
    decode = symbol(oracle, "LZ4_decompress_safe");
    dictdecode = symbol(oracle, "LZ4_decompress_safe_usingDict");
    for (int n = 0; n <= 256; n++)
        for (int a = 0; a < 8; a++)
            for (int mode = 0; mode < 4; mode++)
                one(n, a, mode, 1);
    const int edges[] = {257,   511,   512,   1023,  4095,  4096,   4097,  16384,
                         65535, 65536, 65547, 65548, 65549, 131072, 262144};
    const int accelerations[] = {-1, 0, 1, 2, 4, 65537, INT_MAX};
    for (unsigned l = 0; l < sizeof(edges) / sizeof(*edges); l++)
        for (int a = 0; a < 8; a++)
            for (int mode = 0; mode < 4; mode++)
                for (unsigned k = 0; k < sizeof(accelerations) / sizeof(*accelerations); k++)
                    one(edges[l], a, mode, accelerations[k]);
    CHECK(c.fast((char *)src, (char *)out, -1, CAP, 1, state_words) == 0);
    CHECK(c.fast((char *)src, (char *)out, INT_MAX, CAP, 1, state_words) == 0);
    invalid_workspace();
    fixed_output();
    streams();
    long_stream();
    printf("PASS: kernel LZ4 API, independent decode, bounds, dictionary clones/save, 2GiB stream; "
           "checks=%u\n",
           checks);
    CHECK(dlclose(lib) == 0);
    CHECK(dlclose(oracle) == 0);
    return 0;
}
