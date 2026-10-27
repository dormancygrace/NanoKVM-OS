/* SPDX-License-Identifier: Apache-2.0 */
/* Reuse the fixed-key TLS record benchmark; synthetic nonces are test-only. */
#define main c906_baseline_main
#include "../openssl-c906/evp-bench.c"
#undef main
static void chacha_bench(int n, uint64_t duration)
{
    EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new(); ck(ctx != NULL, "raw chacha ctx");
    unsigned char key[32] = {0}, iv[16] = {0}, in[16416], out[16416];
    memset(in, 0x37, sizeof(in));
    ck(EVP_EncryptInit_ex2(ctx, EVP_chacha20(), key, iv, NULL) == 1, "raw chacha init");
    struct rusage r0, r1; getrusage(RUSAGE_SELF, &r0);
    uint64_t w0 = ns(CLOCK_MONOTONIC), c0 = ns(CLOCK_PROCESS_CPUTIME_ID), c1, count = 0;
    do {
        for (int j = 0; j < 32; ++j) {
            int got;
            ck(EVP_EncryptInit_ex2(ctx, NULL, NULL, iv, NULL) == 1, "raw record IV");
            ck(EVP_EncryptUpdate(ctx, out + 5, &got, in + 5, n) == 1 && got == n, "raw record");
            sink ^= out[5]; ++count;
        }
        c1 = ns(CLOCK_PROCESS_CPUTIME_ID);
    } while (c1 - c0 < duration);
    uint64_t wall = ns(CLOCK_MONOTONIC) - w0; getrusage(RUSAGE_SELF, &r1);
    output("CHACHA20", "encrypt", n, 5, count, c1 - c0, wall, &r0, &r1);
    EVP_CIPHER_CTX_free(ctx);
}
static void poly1305_bench(int n, uint64_t duration)
{
    EVP_MAC *mac = EVP_MAC_fetch(NULL, "POLY1305", NULL);
    EVP_MAC_CTX *ctx = mac ? EVP_MAC_CTX_new(mac) : NULL; ck(ctx != NULL, "poly ctx");
    unsigned char key[32], in[16416], out[16];
    for (size_t i = 0; i < sizeof(key); ++i) key[i] = (unsigned char)(0x57 + 13 * i);
    memset(in, 0x37, sizeof(in));
    struct rusage r0, r1; getrusage(RUSAGE_SELF, &r0);
    uint64_t w0 = ns(CLOCK_MONOTONIC), c0 = ns(CLOCK_PROCESS_CPUTIME_ID), c1, count = 0;
    do {
        for (int j = 0; j < 32; ++j) {
            size_t got;
            ck(EVP_MAC_init(ctx, key, sizeof(key), NULL) == 1 &&
               EVP_MAC_update(ctx, in + 5, (size_t)n) == 1 &&
               EVP_MAC_final(ctx, out, &got, sizeof(out)) == 1 && got == 16, "poly record");
            sink ^= out[0]; ++count;
        }
        c1 = ns(CLOCK_PROCESS_CPUTIME_ID);
    } while (c1 - c0 < duration);
    uint64_t wall = ns(CLOCK_MONOTONIC) - w0; getrusage(RUSAGE_SELF, &r1);
    output("POLY1305", "mac", n, 5, count, c1 - c0, wall, &r0, &r1);
    EVP_MAC_CTX_free(ctx); EVP_MAC_free(mac);
}
int main(int argc, char **argv)
{
    double seconds = argc > 1 ? strtod(argv[1], NULL) : 0.2;
    ck(seconds > 0 && seconds <= 2, "duration");
    const int sizes[] = {64,128,256,319,320,512,640,768,1024,1400,4096,16384};
    for (size_t i = 0; i < sizeof sizes / sizeof sizes[0]; ++i) {
        chacha_bench(sizes[i], (uint64_t)(seconds * 1e9));
        poly1305_bench(sizes[i], (uint64_t)(seconds * 1e9));
        for (int enc = 0; enc <= 1; ++enc)
            aead_bench("CHACHA20-POLY1305", enc, sizes[i], 5,
                       (uint64_t)(seconds * 1e9));
    }
    struct rusage r;
    ck(getrusage(RUSAGE_SELF, &r) == 0, "rusage");
    printf("{\"kind\":\"memory\",\"maxrss_kib\":%ld}\n", r.ru_maxrss);
    return 0;
}
