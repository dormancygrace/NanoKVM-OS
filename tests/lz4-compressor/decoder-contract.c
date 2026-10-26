#define _GNU_SOURCE
#include <assert.h>
#include <dlfcn.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define LIMIT 16384
struct api {
    void *dl;
    int (*safe)(const char *, char *, int, int);
    int (*partial)(const char *, char *, int, int, int);
    int (*dict)(const char *, char *, int, int, const char *, int);
    int (*fast)(const char *, char *, int);
    int (*fastdict)(const char *, char *, int, const char *, int);
    int (*set)(void *, const char *, int);
    int (*cont)(void *, const char *, char *, int, int);
    int (*fastcont)(void *, const char *, char *, int);
};
static uint64_t checks, cases;
static const char *library;
static int off, ml, dict_mode;
#define CHECK(x)                                                                                   \
    do {                                                                                           \
        checks++;                                                                                  \
        if (!(x)) {                                                                                \
            fprintf(stderr, "FAIL %s:%d %s offset=%d length=%d dictionary=%d\n", __FILE__,         \
                    __LINE__, library, off, ml, dict_mode);                                        \
            exit(2);                                                                               \
        }                                                                                          \
    } while (0)
static unsigned char input[LIMIT], expected[LIMIT], dictionary[4096], output[LIMIT + 32],
    inplace[LIMIT * 2 + 32];
static void extension(int n, int *p) {
    while (n >= 255) {
        input[(*p)++] = 255;
        n -= 255;
    }
    input[(*p)++] = (unsigned char)n;
}
static int block(int prefix, int match, int offset, int *length) {
    int p = 0, code = match - 4;
    input[p++] = (unsigned char)((prefix < 15 ? prefix : 15) << 4) | (code < 15 ? code : 15);
    if (prefix >= 15)
        extension(prefix - 15, &p);
    for (int i = 0; i < prefix; i++)
        input[p++] = expected[i] = (unsigned char)(i * 37 + 11);
    input[p++] = (unsigned char)offset;
    input[p++] = (unsigned char)(offset >> 8);
    if (code >= 15)
        extension(code - 15, &p);
    for (int i = 0; i < match; i++) {
        int pos = prefix + i, from = pos - offset;
        expected[pos] = offset == 0 ? 0 : from < 0 ? dictionary[4096 + from] : expected[from];
    }
    input[p++] = 12 << 4;
    for (int i = 0; i < 12; i++)
        input[p++] = expected[prefix + match + i] = (unsigned char)(0xa0 + i);
    *length = prefix + match + 12;
    return p;
}
static unsigned char *fresh(int capacity) {
    CHECK(capacity >= 0 && capacity <= LIMIT);
    memset(output, 0xcc, sizeof(output));
    return output + 16;
}
static void guards(int capacity) {
    for (int i = 0; i < 16; i++) {
        CHECK(output[i] == 0xcc);
        CHECK(output[16 + capacity + i] == 0xcc);
    }
}
static void one(struct api *a) {
    int length, prefix = dict_mode ? 3 : off ? off : 16;
    int n = block(prefix, ml, off, &length);
    CHECK(n < LIMIT && length < LIMIT);
    unsigned char *out = fresh(length);
    int got = dict_mode ? a->dict((char *)input, (char *)out, n, length, (char *)dictionary, 4096)
                        : a->safe((char *)input, (char *)out, n, length);
    CHECK(got == length);
    CHECK(!memcmp(out, expected, length));
    guards(length);
    cases++;
    out = fresh(length);
    got = dict_mode ? a->fastdict((char *)input, (char *)out, length, (char *)dictionary, 4096)
                    : a->fast((char *)input, (char *)out, length);
    CHECK(got == n);
    CHECK(!memcmp(out, expected, length));
    guards(length);
    if (dict_mode) {
        uint64_t state[32] = {0};
        CHECK(a->set(state, (char *)dictionary, 4096) == 1);
        out = fresh(length);
        CHECK(a->cont(state, (char *)input, (char *)out, n, length) == length);
        CHECK(!memcmp(out, expected, length));
        guards(length);
        memset(state, 0, sizeof(state));
        CHECK(a->set(state, (char *)dictionary, 4096) == 1);
        out = fresh(length);
        CHECK(a->fastcont(state, (char *)input, (char *)out, length) == n);
        CHECK(!memcmp(out, expected, length));
        guards(length);
    } else {
        if (off) {
            int targets[] = {
                0, 1, 3, 4, 7, prefix - 1, prefix, prefix + 3, prefix + ml - 1, length - 1, length};
            for (unsigned i = 0; i < sizeof(targets) / sizeof(targets[0]); i++) {
                int cap = targets[i];
                if (cap < 0)
                    continue;
                out = fresh(cap);
                got = a->partial((char *)input, (char *)out, n, cap, cap);
                CHECK(got == (cap == 0 ? -1 : cap));
                CHECK(!memcmp(out, expected, cap));
                guards(cap);
            }
        }
        /* Sufficient in-place margin, with compressed input at the buffer end. */
        int span = length + length / 255 + 64;
        if (span < n)
            span = n + 64;
        CHECK(span < LIMIT * 2);
        memset(inplace, 0xcc, sizeof(inplace));
        unsigned char *dst = inplace + 16, *src = dst + span - n;
        memcpy(src, input, n);
        CHECK(a->safe((char *)src, (char *)dst, n, length) == length);
        CHECK(!memcmp(dst, expected, length));
        for (int i = 0; i < 16; i++)
            CHECK(inplace[i] == 0xcc && inplace[16 + span + i] == 0xcc);
    }
    /* Truncation and small destinations must remain within their exact bounds. */
    for (int cut = 0; cut < n; cut++) {
        out = fresh(length);
        got = dict_mode ? a->dict((char *)input, (char *)out, cut, length, (char *)dictionary, 4096)
                        : a->safe((char *)input, (char *)out, cut, length);
        CHECK(got < 0 || got <= length);
        guards(length);
    }
    int caps[] = {0, 1, 3, 4, 7, 15, length - 1};
    for (unsigned i = 0; i < sizeof(caps) / sizeof(caps[0]); i++) {
        int cap = caps[i];
        if (cap >= length)
            continue;
        out = fresh(cap);
        got = dict_mode ? a->dict((char *)input, (char *)out, n, cap, (char *)dictionary, 4096)
                        : a->safe((char *)input, (char *)out, n, cap);
        CHECK(got < 0);
        guards(cap);
    }
}
#define LOAD(member, name)                                                                         \
    do {                                                                                           \
        *(void **)(&a.member) = dlsym(a.dl, name);                                                 \
        CHECK(a.member != NULL);                                                                   \
    } while (0)
int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: decoder-contract library...\n");
        return 1;
    }
    for (int i = 0; i < 4096; i++)
        dictionary[i] = (unsigned char)(i * 29 + 3);
    int offsets[] = {0,  1,  2,  3,  4,  5,   6,   7,   8,   9,   15,   16,
                     17, 31, 32, 63, 64, 127, 255, 256, 257, 511, 1024, 4096};
    int lengths[] = {4,  7,  8,  15,  16,  17,  18,  19,  20,  31,
                     32, 63, 64, 255, 256, 257, 510, 511, 512, 4096};
    for (int arg = 1; arg < argc; arg++) {
        library = argv[arg];
        struct api a = {0};
        a.dl = dlopen(library, RTLD_NOW | RTLD_LOCAL);
        if (!a.dl) {
            fprintf(stderr, "%s\n", dlerror());
            return 1;
        }
        LOAD(safe, "LZ4_decompress_safe");
        LOAD(partial, "LZ4_decompress_safe_partial");
        LOAD(dict, "LZ4_decompress_safe_usingDict");
        LOAD(fast, "LZ4_decompress_fast");
        LOAD(fastdict, "LZ4_decompress_fast_usingDict");
        LOAD(set, "LZ4_setStreamDecode");
        LOAD(cont, "LZ4_decompress_safe_continue");
        LOAD(fastcont, "LZ4_decompress_fast_continue");
        for (dict_mode = 0; dict_mode < 2; dict_mode++)
            for (unsigned i = 0; i < sizeof(offsets) / sizeof(offsets[0]); i++)
                for (unsigned j = 0; j < sizeof(lengths) / sizeof(lengths[0]); j++) {
                    off = offsets[i];
                    ml = lengths[j];
                    if (dict_mode && off == 0)
                        continue;
                    one(&a);
                }
        CHECK(dlclose(a.dl) == 0);
        printf("PASS %s cases=%" PRIu64 " checks=%" PRIu64 "\n", library, cases, checks);
        fflush(stdout);
    }
    return 0;
}
