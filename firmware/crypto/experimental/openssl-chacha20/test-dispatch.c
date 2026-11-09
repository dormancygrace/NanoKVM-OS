/* SPDX-License-Identifier: Apache-2.0 */
#define _GNU_SOURCE
#include <assert.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <openssl/crypto.h>
static int queries, calls, tails;
int ossl_riscv_has_xtheadvector;
/* The kernel keeps vectors on for every thread (0078): no query on the path. */
__attribute__((unused)) static int fake_prctl(int op, ...) {
    (void)op; ++queries; return -1;
}
#define prctl fake_prctl
#include "chacha_c906.c"
#undef prctl
void ossl_chacha20_c906_blocks(unsigned int s[16], const unsigned char *in,
                              unsigned char *out, size_t groups, int rounds) {
    assert(ossl_riscv_has_xtheadvector && rounds == 20);
    assert(s[0] == 0x61707865 && s[4] == 7 && s[15] == 9);
    ++calls; memcpy(out, in, groups * 320); s[12] += (unsigned int)(groups * 5);
}
void ChaCha20_ctr32_c(unsigned char *out, const unsigned char *in, size_t len,
                     const unsigned int key[8], const unsigned int counter[4]) {
    assert(key[0] == 7 && counter[0] == 11 && len < 320);
    ++tails; memcpy(out, in, len);
}
int main(void) {
    unsigned char in[1024], out[1024]; memset(in, 0x5a, sizeof(in));
    unsigned int key[8] = {7}, ctr[4] = {1, 0, 0, 9};
    ossl_riscv_has_xtheadvector = 1;
    for (size_t n = 0; n < 320; ++n)
        assert(!ossl_chacha20_c906(out, in, n, key, ctr));
    assert(!calls);
    assert(ossl_chacha20_c906(out, in, 320, key, ctr));
    assert(calls == 1 && tails == 0 && !memcmp(out, in, 320));
    assert(ossl_chacha20_c906(out, in, 641, key, ctr));
    assert(calls == 2 && tails == 1 && !memcmp(out, in, 641));
    ossl_riscv_has_xtheadvector = 0;     /* host, QEMU */
    assert(!ossl_chacha20_c906(out, in, 641, key, ctr)); assert(calls == 2);
    assert(!queries);
    puts("PASS real ChaCha dispatch: threshold, hwprobe capability, no syscall, tail");
}
