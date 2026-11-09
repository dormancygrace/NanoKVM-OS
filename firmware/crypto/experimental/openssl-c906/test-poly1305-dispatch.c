/* SPDX-License-Identifier: Apache-2.0 */
/* Host test of the patched crypto/poly1305/poly1305_c906.c. Cryptogams and the
 * vector kernel are replaced by C models with the same interfaces: a radix-2^64
 * context h0 h1 h2 r0 r1 s1 whose h2 is left unreduced (up to 7, as the real
 * code may leave it), and 26-bit limbs in and out. The conversion, the h2
 * fold, the threshold, the tail and the gates are checked against the model
 * Poly1305 alone. */
#define _GNU_SOURCE
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdarg.h>
#include <sys/prctl.h>
#include <openssl/crypto.h>
typedef unsigned __int128 u128;
static int vector_calls, queries;
/* The kernel keeps vectors on for every thread (0078): no query on the path. */
__attribute__((unused)) static int fake_prctl(int op, ...)
{
    (void)op;
    queries++;
    return -1;
}
#define prctl fake_prctl
#include "poly1305_c906.c"
#undef prctl

static uint64_t rng = 88172645463325252ULL;
static uint64_t next(void) { rng ^= rng << 13; rng ^= rng >> 7; rng ^= rng << 17; return rng; }

#define M44 ((1ULL << 44) - 1)
#define M42 ((1ULL << 42) - 1)
/* h * r mod 2^130 - 5 in 44/44/42-bit limbs (poly1305-donna64). */
static void mulmod(uint64_t h[3], const uint64_t r[3])
{
    uint64_t s1 = r[1] * 20, s2 = r[2] * 20;
    u128 d0 = (u128)h[0] * r[0] + (u128)h[1] * s2 + (u128)h[2] * s1;
    u128 d1 = (u128)h[0] * r[1] + (u128)h[1] * r[0] + (u128)h[2] * s2;
    u128 d2 = (u128)h[0] * r[2] + (u128)h[1] * r[1] + (u128)h[2] * r[0];
    uint64_t c = (uint64_t)(d0 >> 44);
    h[0] = (uint64_t)d0 & M44;
    d1 += c; c = (uint64_t)(d1 >> 44); h[1] = (uint64_t)d1 & M44;
    d2 += c; c = (uint64_t)(d2 >> 42); h[2] = (uint64_t)d2 & M42;
    h[0] += c * 5; c = h[0] >> 44; h[0] &= M44; h[1] += c;
}
static void to44(const uint64_t v[3], uint64_t l[3])    /* v: bits 0-63, 64-127, 128+ */
{
    l[0] = v[0] & M44;
    l[1] = ((v[0] >> 44) | (v[1] << 20)) & M44;
    l[2] = (v[1] >> 24) | (v[2] << 40);
}
static void from44(const uint64_t l[3], uint64_t v[3])
{
    u128 t = (u128)l[0] + ((u128)l[1] << 44);
    u128 hi = (u128)(l[2] & ((1ULL << 40) - 1)) << 88;
    u128 s = t + hi;
    v[0] = (uint64_t)s;
    v[1] = (uint64_t)(s >> 64);
    v[2] = (l[2] >> 40) + (s < hi);
}
static void absorb(uint64_t h[3], const uint64_t r[3], const unsigned char *in, size_t blocks, unsigned padbit)
{
    for (; blocks; blocks--, in += 16) {
        uint64_t m[3], ml[3];
        memcpy(&m[0], in, 8);
        memcpy(&m[1], in + 8, 8);
        m[2] = padbit;
        to44(m, ml);
        h[0] += ml[0]; h[1] += ml[1]; h[2] += ml[2];
        mulmod(h, r);
    }
}
/* Cryptogams model; the result may carry an extra multiple of p in h2. */
void poly1305_blocks(void *ctx, const unsigned char *in, size_t len, unsigned int padbit)
{
    uint64_t *st = ctx, h[3], r[3], v[3], rr[3] = { st[3], st[4], 0 };
    to44(st, h);
    to44(rr, r);
    absorb(h, r, in, len / 16, padbit);
    from44(h, v);
    if ((next() & 1) && v[2] <= 3) {            /* + p = + 4 * 2^128 - 5 */
        uint64_t b = v[0] < 5;
        v[0] -= 5;
        uint64_t b2 = v[1] < b;
        v[1] -= b;
        v[2] += 4 - b2;
    }
    st[0] = v[0]; st[1] = v[1]; st[2] = v[2];
}
/* Vector kernel model: 26-bit limbs, groups * 5 blocks with padbit 1. */
void ossl_poly1305_blocks_c906v(const uint64_t r[5], const unsigned char *in, size_t groups, uint64_t h[5])
{
    vector_calls++;
    for (int i = 0; i < 5; i++)
        assert(r[i] < (1ULL << 26) && h[i] < (1ULL << 27));
    uint64_t v[3] = { h[0] | h[1] << 26 | h[2] << 52, h[2] >> 12 | h[3] << 14 | h[4] << 40, h[4] >> 24 };
    uint64_t rv[3] = { r[0] | r[1] << 26 | r[2] << 52, r[2] >> 12 | r[3] << 14 | r[4] << 40, 0 };
    uint64_t hl[3], rl[3];
    to44(v, hl);
    to44(rv, rl);
    absorb(hl, rl, in, groups * 5, 1);
    from44(hl, v);
    h[0] = v[0] & MASK26;
    h[1] = (v[0] >> 26) & MASK26;
    h[2] = ((v[0] >> 52) | (v[1] << 12)) & MASK26;
    h[3] = (v[1] >> 14) & MASK26;
    h[4] = (v[1] >> 40) | (v[2] << 24);
    /* The kernel returns h[1] up to 2^26 + 2^7: move one 2^52 from h[2]. */
    if ((next() & 1) && h[2] != 0) {
        h[2] -= 1;
        h[1] += 1ULL << 26;
    }
}
static void canon(const uint64_t st[3], uint64_t out[3])   /* the value mod p */
{
    uint64_t l[3], v[3], one[3] = { 1, 0, 0 };
    to44(st, l);
    mulmod(l, one);
    from44(l, v);
    u128 t = (u128)v[0] + 5;
    uint64_t g0 = (uint64_t)t;
    t = (u128)v[1] + (uint64_t)(t >> 64);
    uint64_t g1 = (uint64_t)t, g2 = v[2] + (uint64_t)(t >> 64);
    if (g2 >> 2) { v[0] = g0; v[1] = g1; v[2] = g2 & 3; }
    memcpy(out, v, sizeof(v));
}
static unsigned char msg[8192 + 16];
int main(void)
{
    for (size_t i = 0; i < sizeof(msg); i++)
        msg[i] = (unsigned char)next();
    int checks = 0;
    for (int t = 0; t < 4000; t++) {
        uint64_t a[6], b[6], ca[3], cb[3];
        size_t len = t < 1500 ? 16 * (size_t)(t % 140) : 16 * (next() % 500);
        unsigned int padbit = t % 7 ? 1 : 0;
        a[0] = next(); a[1] = next(); a[2] = next() % 8;
        if (t % 50 == 0) { a[0] = a[1] = ~0ULL; a[2] = 7; }
        a[3] = next() & 0x0ffffffc0fffffffULL;
        a[4] = next() & 0x0ffffffc0ffffffcULL;
        if (t % 9 == 0) { a[3] = 0x0ffffffc0fffffffULL; a[4] = 0x0ffffffc0ffffffcULL; }
        a[5] = a[4] + (a[4] >> 2);
        memcpy(b, a, sizeof(a));
        int before = vector_calls;
        poly1305_blocks(a, msg + (t % 16), len, padbit);
        ossl_poly1305_blocks_c906(b, msg + (t % 16), len, padbit);
        assert(vector_calls - before == (padbit && len / 80 >= OSSL_POLY1305_C906_MIN_GROUPS));
        canon(a, ca);
        canon(b, cb);
        assert(!memcmp(ca, cb, sizeof(ca)));
        assert(!memcmp(a + 3, b + 3, 3 * sizeof(uint64_t)));
        checks++;
    }
    assert(queries == 0);
    printf("PASS %d Poly1305 dispatch cases: conversion, h2 fold, threshold, tail, no syscall\n", checks);
    return 0;
}
