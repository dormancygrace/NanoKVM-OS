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
static int mode = 2, queries, calls, tails;
int ossl_riscv_has_xtheadvector;
static int fake_prctl(int op, ...) {
    assert(op == 70); ++queries; return mode;
}
char *ossl_safe_getenv(const char *name) { return getenv(name); }
#define prctl fake_prctl
#include "chacha_c906.c"
#undef prctl
void ossl_chacha20_c906_blocks(unsigned int s[16], const unsigned char *in,
                              unsigned char *out, size_t groups, int rounds) {
    assert((mode & 3) == 2 && ossl_riscv_has_xtheadvector && rounds == 20);
    assert(s[0] == 0x61707865 && s[4] == 7 && s[15] == 9);
    ++calls; memcpy(out, in, groups * 320); s[12] += (unsigned int)(groups * 5);
}
void ChaCha20_ctr32_c(unsigned char *out, const unsigned char *in, size_t len,
                     const unsigned int key[8], const unsigned int counter[4]) {
    assert(key[0] == 7 && counter[0] == 11 && len < 320);
    ++tails; memcpy(out, in, len);
}
static void reset(void) {
    once = (CRYPTO_ONCE)CRYPTO_ONCE_STATIC_INIT; disabled = 0;
    calls = tails = queries = 0; ossl_riscv_has_xtheadvector = 1; mode = 2;
    assert(setenv("NANOKVM_OPENSSL_CHACHA20", "auto", 1) == 0);
}
int main(void) {
    unsigned char in[1024], out[1024]; memset(in, 0x5a, sizeof(in));
    unsigned int key[8] = {7}, ctr[4] = {1, 0, 0, 9};
    reset();
    for (size_t n = 0; n < 640; ++n)
        assert(!ossl_chacha20_c906(out, in, n, key, ctr));
    assert(!queries && !calls);
    assert(ossl_chacha20_c906(out, in, 640, key, ctr));
    assert(calls == 1 && queries == 1 && tails == 0 && !memcmp(out, in, 640));
    assert(ossl_chacha20_c906(out, in, 641, key, ctr));
    assert(calls == 2 && queries == 2 && tails == 1 && !memcmp(out, in, 641));
    for (int m = -1; m <= 7; ++m) {
        mode = m; int before = calls;
        assert(ossl_chacha20_c906(out, in, 640, key, ctr) == (m >= 0 && (m & 3) == 2));
        assert(calls == before + (m >= 0 && (m & 3) == 2));
    }
    reset(); ossl_riscv_has_xtheadvector = 0;
    assert(!ossl_chacha20_c906(out, in, 640, key, ctr)); assert(!calls && !queries);
    reset(); assert(setenv("NANOKVM_OPENSSL_CHACHA20", "generic", 1) == 0);
    assert(!ossl_chacha20_c906(out, in, 640, key, ctr)); assert(!calls && !queries);
    reset(); assert(unsetenv("NANOKVM_OPENSSL_CHACHA20") == 0);
    assert(!ossl_chacha20_c906(out, in, 640, key, ctr)); assert(!calls && !queries);
    reset(); assert(setenv("NANOKVM_OPENSSL_CHACHA20", "unknown", 1) == 0);
    assert(!ossl_chacha20_c906(out, in, 640, key, ctr)); assert(!calls && !queries);
    puts("PASS real ChaCha dispatch: threshold, hwprobe capability, thread modes, override, tail");
}
