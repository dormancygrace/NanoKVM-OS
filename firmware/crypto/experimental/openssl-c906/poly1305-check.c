/* SPDX-License-Identifier: Apache-2.0 */
/* Poly1305 through EVP_MAC (NanoKVM OS C906 qualification).
 *   poly1305-check tags  : print tags for random keys, lengths, offsets and
 *                          update splits (deterministic); run once with
 *                          a Cryptogams-only library build and compare.
 *   poly1305-check bench : MB/s per message size, a fresh key per message.
 * Build with -march=rv64gc so the program itself has no vector code. */
#include <openssl/evp.h>
#include <openssl/core_names.h>
#include <openssl/params.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static uint64_t rng = 0x9e3779b97f4a7c15ULL;
static uint32_t next(void)
{
    rng ^= rng << 13; rng ^= rng >> 7; rng ^= rng << 17;
    return (uint32_t)rng;
}

static EVP_MAC *mac;

static void tag(const unsigned char key[32], const unsigned char *in, size_t len, size_t split,
                unsigned char out[16])
{
    EVP_MAC_CTX *c = EVP_MAC_CTX_new(mac);
    size_t n = 0;
    if (!c || !EVP_MAC_init(c, key, 32, NULL) || !EVP_MAC_update(c, in, split)
        || !EVP_MAC_update(c, in + split, len - split) || !EVP_MAC_final(c, out, &n, 16) || n != 16) {
        fprintf(stderr, "EVP_MAC failure\n");
        exit(2);
    }
    EVP_MAC_CTX_free(c);
}

static double now(void)
{
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec + t.tv_nsec * 1e-9;
}

int main(int argc, char **argv)
{
    static unsigned char buf[70000];
    unsigned char key[32] = {0}, out[16];
    mac = EVP_MAC_fetch(NULL, "POLY1305", NULL);
    if (!mac)
        return 2;
    for (size_t i = 0; i < sizeof(buf); i++)
        buf[i] = (unsigned char)next();
    if (argc > 1 && !strcmp(argv[1], "tags")) {
        for (int i = 0; i < 6000; i++) {
            size_t len = i < 1200 ? (size_t)i : i % 97 == 0 ? 65000 + next() % 500 : next() % 4100;
            size_t off = next() % 16, split = next() % 3 ? 0 : next() % (len + 1);
            for (int k = 0; k < 32; k++)
                key[k] = (unsigned char)next();
            if (i % 50 == 0) {      /* all-ones key and message */
                memset(key, 0xff, 32);
                memset(buf + off, 0xff, len);
            }
            tag(key, buf + off, len, split, out);
            if (i % 50 == 0)
                for (size_t j = 0; j < len; j++)
                    buf[off + j] = (unsigned char)next();
            printf("%d %zu %zu %zu ", i, len, off, split);
            for (int k = 0; k < 16; k++)
                printf("%02x", out[k]);
            putchar('\n');
        }
        return 0;
    }
    static const size_t sizes[] = { 64, 240, 320, 400, 480, 640, 1024, 1400, 4096, 16384 };
    for (size_t s = 0; s < sizeof(sizes) / sizeof(sizes[0]); s++) {
        double best = 1e9;
        for (int r = 0; r < 5; r++) {
            int n = (int)(4000000 / sizes[s]) + 1;
            double t0 = now();
            for (int i = 0; i < n; i++) {
                key[0] = (unsigned char)i;
                tag(key, buf + 5, sizes[s], 0, out);
            }
            double t = (now() - t0) / n;
            if (t < best)
                best = t;
        }
        printf("%6zu %8.1f MB/s\n", sizes[s], sizes[s] / best / 1e6);
    }
    return 0;
}
