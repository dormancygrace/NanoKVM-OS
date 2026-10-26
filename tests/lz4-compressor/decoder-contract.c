#define _GNU_SOURCE
#include <assert.h>
#include <dlfcn.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
#define LIMIT 16384
struct api {
    void *dl;
    int (*safe)(const char *, char *, int, int);
    int (*reference)(const char *, char *, int, int);
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
static int off, ml, dict_mode, tail = 12;
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
    input[p++] = (tail < 15 ? tail : 15) << 4;
    if (tail >= 15)
        extension(tail - 15, &p);
    for (int i = 0; i < tail; i++)
        input[p++] = expected[prefix + match + i] = (unsigned char)(0xa0 + i);
    *length = prefix + match + tail;
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
static int literal_block(int length) {
    int p = 0;
    input[p++] = (length < 15 ? length : 15) << 4;
    if (length >= 15)
        extension(length - 15, &p);
    for (int i = 0; i < length; i++)
        input[p++] = expected[i] = (unsigned char)(length * 19 + i * 29);
    return p;
}

/* The terminal copy must retain memmove semantics in either direction. */
static void short_literals(struct api *a) {
    unsigned char original[256], want[256], got[256];
    off = ml = dict_mode = 0;
    for (tail = 0; tail <= 64; tail++) {
        int n = literal_block(tail);
        for (int shift = -32; shift <= 32; shift++) {
            memset(original, 0xcc, sizeof(original));
            memcpy(original + 96, input, n);
            memcpy(want, original, sizeof(want));
            memcpy(want + 96 + shift, expected, tail);
            memcpy(got, original, sizeof(got));
            CHECK(a->safe((char *)got + 96, (char *)got + 96 + shift, n, tail) == tail);
            CHECK(!memcmp(got, want, sizeof(got)));
        }
    }
}

/* Successful full decodes must match the retained generic full decoder.
 * The partial API is not an oracle for malformed offset-zero blocks: even
 * zeroed destinations can contain speculative literal copies at the match
 * position, and partial mode deliberately skips offset-zero initialization.
 * Negative error codes need not be identical between decoder versions. */
static int differential(struct api *a, const unsigned char *src,
                        unsigned char *dst, int n, int capacity) {
    unsigned char reference[LIMIT];
    CHECK(capacity >= 0 && capacity <= LIMIT);
    memset(dst, 0, capacity);
    memset(reference, 0, capacity);
    int got = a->safe((const char *)src, (char *)dst, n, capacity);
    CHECK(got < 0 || got <= capacity);
    if (got < 0)
        return 0;
    int other = a->reference((const char *)src, (char *)reference, n, capacity);
    CHECK(other == got);
    CHECK(!memcmp(dst, reference, got));
    return 1;
}

/* Read and write limits sit directly against inaccessible pages. */
static void guarded(struct api *a) {
    size_t page = (size_t)sysconf(_SC_PAGESIZE), span = LIMIT;
    unsigned char *src = mmap(NULL, span + 2 * page, PROT_NONE,
                              MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    unsigned char *dst = mmap(NULL, span + 2 * page, PROT_NONE,
                              MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    CHECK(src != MAP_FAILED && dst != MAP_FAILED);
    CHECK(mprotect(src + page, span, PROT_READ | PROT_WRITE) == 0);
    CHECK(mprotect(dst + page, span, PROT_READ | PROT_WRITE) == 0);
    dict_mode = 0;
    int matches[] = {4, 7, 8, 18, 19, 31, 32, 63, 64, 255, 256, 4096};
    for (off = 0; off <= 32; off++)
        for (unsigned j = 0; j < sizeof(matches) / sizeof(matches[0]); j++)
            for (tail = 5; tail <= 96; tail++) {
                ml = matches[j];
                if (ml + tail < 12)
                    continue;
                int length, n = block(off ? off : 16, ml, off, &length);
                unsigned char *in = src + page + span - n;
                unsigned char *out = dst + page + span - length;
                memcpy(in, input, n);
                CHECK(a->safe((char *)in, (char *)out, n, length) == length);
                CHECK(!memcmp(out, expected, length));
            }
    for (int length = 0; length <= 64; length++) {
        int n = literal_block(length);
        unsigned char *in = src + page + span - n;
        unsigned char *out = dst + page + span - length;
        memcpy(in, input, n);
        CHECK(a->safe((char *)in, (char *)out, n, length) == length);
        CHECK(!memcmp(out, expected, length));
    }
    uint32_t random = 0x9062026;
    for (int i = 0; i < 10000; i++) {
        random = random * 1664525U + 1013904223U;
        int n = (int)(random % 512), cap = (int)((random >> 9) % 4097);
        unsigned char *in = src + page + span - n;
        unsigned char *out = dst + page + span - cap;
        for (int j = 0; j < n; j++) {
            random = random * 1664525U + 1013904223U;
            in[j] = (unsigned char)(random >> 24);
        }
        differential(a, in, out, n, cap);
    }
    unsigned accepted = 0;
    /* Mutating valid blocks reaches substantially more successful paths than
     * arbitrary random bytes: literal/match/extension changes and truncation. */
    for (int i = 0; i < 20000; i++) {
        random = random * 1664525U + 1013904223U;
        off = (int)(random % 33);
        ml = 7 + (int)((random >> 6) % 2048);
        tail = 5 + (int)((random >> 18) % 92);
        int length, n = block(off ? off : 16, ml, off, &length);
        int mode = i % 5;
        if (mode == 1 || mode == 2) {
            int at = (int)((random >> 4) % (unsigned)n);
            input[at] ^= mode == 1 ? (1U << ((random >> 24) % 8)) : 0xff;
        } else if (mode == 3) {
            n -= (int)((random >> 5) % (unsigned)n);
        }
        int cap = mode == 4 ? (int)(random % (unsigned)(length + 32)) : length;
        unsigned char *in = src + page + span - n;
        unsigned char *out = dst + page + span - cap;
        memcpy(in, input, n);
        accepted += differential(a, in, out, n, cap);
    }
    CHECK(accepted >= 4000);
    printf("Differential accepted mutated/valid blocks=%u\n", accepted);
    CHECK(a->safe(NULL, (char *)dst + page, 1, 4096) < 0);
    CHECK(a->safe((char *)src + page, (char *)dst + page, -1, 4096) < 0);
    CHECK(a->safe((char *)src + page, (char *)dst + page, 1, -1) < 0);
    CHECK(munmap(src, span + 2 * page) == 0);
    CHECK(munmap(dst, span + 2 * page) == 0);
}

static void malformed(struct api *a) {
    unsigned char bad[600];
    int capacity = 128;
    /* Offset exceeds both the produced prefix and an external dictionary. */
    memset(bad, 0, sizeof(bad));
    bad[0] = 0x10; bad[1] = 'A'; bad[2] = 0xff; bad[3] = 0xff;
    bad[4] = 0xc0;
    unsigned char *out = fresh(capacity);
    CHECK(a->safe((char *)bad, (char *)out, 17, capacity) < 0);
    guards(capacity);
    out = fresh(capacity);
    CHECK(a->dict((char *)bad, (char *)out, 17, capacity,
                  (char *)dictionary, sizeof(dictionary)) < 0);
    guards(capacity);
    /* Literal and match extensions that overflow the output capacity,
     * including unterminated extension chains. */
    for (int match = 0; match < 2; match++) {
        int p = 0;
        bad[p++] = match ? 0x1f : 0xf0;
        if (match) { bad[p++] = 'A'; bad[p++] = 1; bad[p++] = 0; }
        memset(bad + p, 255, 512); p += 512;
        for (int terminated = 0; terminated < 2; terminated++) {
            int n = p;
            if (terminated) { bad[n++] = 0; bad[n++] = 0xc0;
                memset(bad + n, 0, 12); n += 12; }
            out = fresh(capacity);
            CHECK(a->safe((char *)bad, (char *)out, n, capacity) < 0);
            guards(capacity);
        }
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
        LOAD(reference, "nanokvm_lz4_generic_full");
        LOAD(dict, "LZ4_decompress_safe_usingDict");
        LOAD(fast, "LZ4_decompress_fast");
        LOAD(fastdict, "LZ4_decompress_fast_usingDict");
        LOAD(set, "LZ4_setStreamDecode");
        LOAD(cont, "LZ4_decompress_safe_continue");
        LOAD(fastcont, "LZ4_decompress_fast_continue");
        int tails[] = {5, 6, 7, 8, 9, 10, 11, 12, 64, 96, 255};
        for (unsigned k = 0; k < sizeof(tails) / sizeof(tails[0]); k++) {
            tail = tails[k];
            for (dict_mode = 0; dict_mode < 2; dict_mode++)
                for (unsigned i = 0; i < sizeof(offsets) / sizeof(offsets[0]); i++)
                    for (unsigned j = 0; j < sizeof(lengths) / sizeof(lengths[0]); j++) {
                        off = offsets[i];
                        ml = lengths[j];
                        if (ml + tail < 12 || (dict_mode && off == 0))
                            continue;
                        one(&a);
                    }
        }
        malformed(&a);
        short_literals(&a);
        guarded(&a);
        CHECK(dlclose(a.dl) == 0);
        printf("PASS %s cases=%" PRIu64 " checks=%" PRIu64 "\n", library, cases, checks);
        fflush(stdout);
    }
    return 0;
}
