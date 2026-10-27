/* SPDX-License-Identifier: Apache-2.0 */
/* Freestanding test: no host libc instructions can hide a C906 ISA mismatch. */
#include <stdint.h>
#include <stddef.h>
#include <openssl/sha.h>
#define OPENSSL_RISCVCAP_IMPL
#include "crypto/riscv_arch.h"
#include "crypto/sg2002.h"

void ossl_sha256_c906_blocks(void *, const void *, size_t);
void sha256_block_data_order(void *, const void *, size_t);
int checked_sha256(void *, const void *, size_t);
static void quit(int status) __attribute__((noreturn));
static void quit(int status) {
    register long a0 __asm__("a0") = status;
    register long a7 __asm__("a7") = 93;
    __asm__ volatile("ecall" : : "r"(a0), "r"(a7) : "memory");
    __builtin_unreachable();
}
#define REQUIRE(x) do { if (!(x)) quit(1); } while (0)
void *memcpy(void *dst, const void *src, size_t n) {
    unsigned char *d = dst; const unsigned char *s = src;
    for (size_t i = 0; i < n; i++) d[i] = s[i];
    return dst;
}
void *memset(void *dst, int c, size_t n) {
    unsigned char *d = dst; for (size_t i = 0; i < n; i++) d[i] = c;
    return dst;
}
static int equal(const void *a, const void *b, size_t n) {
    const unsigned char *x = a, *y = b;
    for (size_t i = 0; i < n; i++) if (x[i] != y[i]) return 0;
    return 1;
}
void OPENSSL_cleanse(void *p, size_t n) {
    volatile unsigned char *v = p; while (n--) *v++ = 0;
}
size_t riscv_vlen(void) { return 0; }
void sha256_block_data_order_zvkb_zvknha_or_zvknhb(void *c, const void *p, size_t n) {
    (void)c; (void)p; (void)n; quit(2);
}
void sha256_block_data_order_c(void *c, const void *p, size_t n) {
    (void)c; (void)p; (void)n; quit(3); /* Prove the C906 path is used. */
}
static const uint32_t k[64] = {
0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2};
static uint32_t rotr(uint32_t x, unsigned n) { return (x >> n) | (x << (32-n)); }
static uint32_t be32(const unsigned char *p) {
    return (uint32_t)p[0]<<24 | (uint32_t)p[1]<<16 | (uint32_t)p[2]<<8 | p[3];
}
/* Independent FIPS 180-4 word-array compressor, not the assembly generator. */
static void reference(uint32_t h[8], const unsigned char *p, size_t blocks) {
    while (blocks--) {
        uint32_t w[64], a=h[0],b=h[1],c=h[2],d=h[3],e=h[4],f=h[5],g=h[6],z=h[7];
        for (int i=0;i<16;i++) w[i]=be32(p+4*i);
        for (int i=16;i<64;i++) w[i]=w[i-16]+w[i-7]
            +(rotr(w[i-15],7)^rotr(w[i-15],18)^(w[i-15]>>3))
            +(rotr(w[i-2],17)^rotr(w[i-2],19)^(w[i-2]>>10));
        for (int i=0;i<64;i++) {
            uint32_t t=z+(rotr(e,6)^rotr(e,11)^rotr(e,25))+((e&f)^(~e&g))+k[i]+w[i];
            uint32_t u=(rotr(a,2)^rotr(a,13)^rotr(a,22))+((a&b)^(a&c)^(b&c));
            z=g;g=f;f=e;e=d+t;d=c;c=b;b=a;a=t+u;
        }
        h[0]+=a;h[1]+=b;h[2]+=c;h[3]+=d;h[4]+=e;h[5]+=f;h[6]+=g;h[7]+=z;
        p+=64;
    }
}
static uint32_t rng=0x3bc29871;
static uint32_t random_word(void) { rng^=rng<<13;rng^=rng>>17;rng^=rng<<5;return rng; }
static int fail_after, calls, opens, closes;
int ossl_sg2002_begin(unsigned algorithm) {
    REQUIRE(algorithm==SG2002_ALG_SHA256);opens++;return fail_after==-2?-1:37;
}
void ossl_sg2002_end(int fd) { REQUIRE(fd==37);closes++; }
int ossl_sg2002_run(int fd, struct sg2002_crypto_request *r) {
    uint32_t h[8];
    REQUIRE(fd==37 && r->algorithm==SG2002_ALG_SHA256);
    REQUIRE(r->length>=512 && r->length<=4096 && r->length%64==0);
    if (calls++==fail_after) return 0;
    for(int i=0;i<8;i++)h[i]=be32(r->state+4*i);
    reference(h,(const unsigned char *)(uintptr_t)r->input,r->length/64);
    for(int i=0;i<8;i++)for(int j=0;j<4;j++)r->state[4*i+j]=h[i]>>(24-8*j);
    return 1;
}
static unsigned char data[200*64+16], saved[sizeof data];
static void one_case(size_t blocks, unsigned offset, int wrapper) {
    struct { uint64_t before[2]; SHA256_CTX ctx; uint64_t after[2]; } got,want;
    unsigned char *bytes=(unsigned char *)&got;
    for(size_t i=0;i<sizeof got;i++)bytes[i]=(unsigned char)random_word();
    memcpy(&want,&got,sizeof got);reference(want.ctx.h,data+offset,blocks);
    calls=opens=closes=0;
    if (wrapper) {
        sha256_block_data_order(&got.ctx,data+offset,blocks);
        REQUIRE(opens==(blocks>=64));
        REQUIRE(closes==(blocks>=64 && fail_after!=-2));
    } else REQUIRE(checked_sha256(&got.ctx,data+offset,blocks)==0);
#ifdef NEGATIVE_DIGEST
    want.ctx.h[0]^=1;
#endif
    REQUIRE(equal(&got,&want,sizeof got));
    REQUIRE(equal(data,saved,sizeof data));
}
int main(void) {
    /* Anchor the independent reference with SHA-256("abc"). */
    uint32_t h[8]={0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19};
    const uint32_t expected[8]={0xba7816bf,0x8f01cfea,0x414140de,0x5dae2223,0xb00361a3,0x96177a9c,0xb410ff61,0xf20015ad};
    unsigned char abc[64]={0x61,0x62,0x63,0x80};abc[63]=24;reference(h,abc,1);
    REQUIRE(equal(h,expected,sizeof h));
    for(size_t i=0;i<sizeof data;i++)data[i]=(unsigned char)random_word();
    memcpy(saved,data,sizeof data);
    for(size_t blocks=0;blocks<=130;blocks++)for(unsigned off=0;off<16;off++)one_case(blocks,off,0);
    const size_t lengths[]={0,1,63,64,65,71,72,73,127,128,129,135,136,200};
    for(size_t i=0;i<sizeof lengths/sizeof *lengths;i++)for(unsigned off=0;off<16;off++)
        for(fail_after=-2;fail_after<=3;fail_after++)one_case(lengths[i],off,1);
    return 0;
}
