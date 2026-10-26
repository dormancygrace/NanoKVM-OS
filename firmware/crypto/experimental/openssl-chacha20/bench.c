/* SPDX-License-Identifier: Apache-2.0 */
/* Reuse the fixed-key TLS record benchmark; synthetic nonces are test-only. */
#define main c906_baseline_main
#include "../openssl-c906/evp-bench.c"
#undef main
int main(int argc, char **argv)
{
    double seconds = argc > 1 ? strtod(argv[1], NULL) : 0.2;
    ck(seconds > 0 && seconds <= 2, "duration");
    const int sizes[] = {64,128,256,319,320,512,640,768,1024,1400,4096,16384};
    for (size_t i = 0; i < sizeof sizes / sizeof sizes[0]; ++i)
        for (int enc = 0; enc <= 1; ++enc)
            aead_bench("CHACHA20-POLY1305", enc, sizes[i], 5,
                       (uint64_t)(seconds * 1e9));
    struct rusage r;
    ck(getrusage(RUSAGE_SELF, &r) == 0, "rusage");
    printf("{\"kind\":\"memory\",\"maxrss_kib\":%ld}\n", r.ru_maxrss);
    return 0;
}
