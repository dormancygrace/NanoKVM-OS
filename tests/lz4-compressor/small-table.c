#define _GNU_SOURCE
#include <dlfcn.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#define STATE 4128
static unsigned checks;
#define CHECK(x)                                                                                   \
    do {                                                                                           \
        checks++;                                                                                  \
        if (!(x)) {                                                                                \
            fprintf(stderr, "FAIL line %d: %s\n", __LINE__, #x);                                   \
            abort();                                                                               \
        }                                                                                          \
    } while (0)
static int (*compress4k)(const char *, char *, int, int, int, void *);
static int (*decode)(const char *, char *, int, int);
static unsigned char *input_end, *output_end, *state, plain[4096], original[4096];
static uint32_t seed = 0x9062026;
static uint32_t random32(void) {
    uint32_t x = seed;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    return seed = x;
}

static void *guarded_end(size_t bytes) {
    size_t page = sysconf(_SC_PAGESIZE);
    size_t accessible = (bytes + page - 1) / page * page;
    unsigned char *p =
        mmap(NULL, accessible + 2 * page, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    CHECK(p != MAP_FAILED);
    CHECK(mprotect(p + page, accessible, PROT_READ | PROT_WRITE) == 0);
    return p + page + accessible;
}

static void check_block(int n, int mode, int acceleration) {
    unsigned char *src = input_end - n;
    for (int i = 0; i < n; i++) {
        uint32_t r = random32();
        src[i] = mode == 0   ? r
                 : mode == 1 ? (i % 31 ? 0 : r)
                 : mode == 2 ? (uint32_t)(i % 13)
                             : (i % 128 < 100 ? (uint32_t)(i % 17) : r);
    }
    memcpy(original, src, n);
    memset(state - 16, 0xA5, STATE + 16);
    int cap = n + n / 255 + 16;
    unsigned char *dst = output_end - cap;
    int size = compress4k((char *)src, (char *)dst, n, cap, acceleration, state);
    CHECK(size > 0 && size <= cap);
    CHECK(decode((char *)dst, (char *)plain, size, n) == n);
    CHECK(!memcmp(plain, original, n));
    CHECK(!memcmp(src, original, n));
    for (int i = 1; i <= 16; i++)
        CHECK(state[-i] == 0xA5);
    if (n <= 64 || n == 4096 || n == 255) {
        int limits[] = {0, 1, 7, size - 1, size};
        for (unsigned j = 0; j < sizeof(limits) / sizeof(*limits); j++) {
            int limit = limits[j];
            dst = output_end - limit;
            int got = compress4k((char *)src, (char *)dst, n, limit, acceleration, state);
            CHECK(got >= 0 && got <= limit);
            if (got) {
                CHECK(decode((char *)dst, (char *)plain, got, n) == n);
                CHECK(!memcmp(plain, original, n));
            }
            for (int i = 1; i <= 16; i++)
                CHECK(state[-i] == 0xA5);
        }
    }
}

int main(int argc, char **argv) {
    CHECK(argc == 3);
    void *lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    void *oracle = dlopen(argv[2], RTLD_NOW | RTLD_LOCAL);
    if (!lib || !oracle) {
        fprintf(stderr, "dlopen: %s\n", dlerror());
        return 1;
    }
    compress4k = dlsym(lib, "LZ4_compress_fast_4k");
    decode = dlsym(oracle, "LZ4_decompress_safe");
    CHECK(compress4k && decode);
    input_end = guarded_end(4096);
    output_end = guarded_end(8192);
    state = (unsigned char *)guarded_end(STATE + 16) - STATE;
    for (int n = 0; n <= 4096; n++)
        for (int mode = 0; mode < 4; mode++)
            check_block(n, mode, 1);
    int lengths[] = {0, 1, 7, 31, 255, 1023, 4095, 4096};
    int accelerations[] = {-1, 0, 2, 8, 65537, INT_MAX};
    for (unsigned l = 0; l < sizeof(lengths) / sizeof(*lengths); l++)
        for (unsigned a = 0; a < sizeof(accelerations) / sizeof(*accelerations); a++)
            for (int mode = 0; mode < 4; mode++)
                check_block(lengths[l], mode, accelerations[a]);
    unsigned char oversized[4097] = {0};
    CHECK(compress4k((char *)oversized, (char *)output_end - 8192, sizeof(oversized), 8192, 1,
                     state) == 0);
    /* Rejection must happen without accessing any supplied buffers. */
    CHECK(compress4k(NULL, NULL, -1, 8192, 1, NULL) == 0);
    CHECK(compress4k(NULL, NULL, 4097, 8192, 1, NULL) == 0);
    CHECK(compress4k(NULL, NULL, INT_MAX, 8192, 1, NULL) == 0);
    CHECK(compress4k(NULL, NULL, 4096, -1, 1, NULL) == 0);
    printf("PASS: 4KiB table, all input lengths/alignments, guard pages, bounded output, "
           "accelerations; checks=%u\n",
           checks);
    CHECK(dlclose(lib) == 0);
    CHECK(dlclose(oracle) == 0);
    return 0;
}
