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
/* riscvcap.c checks the vector state once at load; the cipher path must not. */
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
static void reset(const char *env) {
    once = (CRYPTO_ONCE)CRYPTO_ONCE_STATIC_INIT; disabled = 0;
    calls = tails = queries = 0; ossl_riscv_has_xtheadvector = 1;
    assert((env ? setenv("NANOKVM_OPENSSL_CHACHA20", env, 1) : unsetenv("NANOKVM_OPENSSL_CHACHA20")) == 0);
}
int main(void) {
    unsigned char in[1024], out[1024]; memset(in, 0x5a, sizeof(in));
    unsigned int key[8] = {7}, ctr[4] = {1, 0, 0, 9};
    reset(NULL);
    for (size_t n = 0; n < 320; ++n)
        assert(!ossl_chacha20_c906(out, in, n, key, ctr));
    assert(!calls);
    assert(ossl_chacha20_c906(out, in, 320, key, ctr));
    assert(calls == 1 && tails == 0 && !memcmp(out, in, 320));
    assert(ossl_chacha20_c906(out, in, 641, key, ctr));
    assert(calls == 2 && tails == 1 && !memcmp(out, in, 641));
    assert(!queries);
    /* No XTheadVector, or vector state off when libcrypto was loaded. */
    reset(NULL); ossl_riscv_has_xtheadvector = 0;
    assert(!ossl_chacha20_c906(out, in, 320, key, ctr)); assert(!calls);
    reset("generic");
    assert(!ossl_chacha20_c906(out, in, 320, key, ctr)); assert(!calls);
    /* Any other value, including the former opt-in, is the default. */
    reset("auto");
    assert(ossl_chacha20_c906(out, in, 320, key, ctr)); assert(calls == 1);
    reset("unknown");
    assert(ossl_chacha20_c906(out, in, 320, key, ctr)); assert(calls == 1);
    assert(!queries);
    puts("PASS real ChaCha dispatch: threshold, load-time capability, no syscall on the cipher path, generic override, tail");
}
