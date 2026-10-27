/* SPDX-License-Identifier: Apache-2.0 */
/* The real OpenSSL key expansion and assembler versus SP800-38D bit-serial GHASH. */
#include <stdint.h>
#include <stddef.h>
#include <string.h>
#include <openssl/crypto.h>
#include "crypto/modes.h"
#ifdef GHASH_NATIVE
#include <stdio.h>
#include <stdlib.h>
#include <pthread.h>
#include <sys/prctl.h>
#endif

size_t ossl_gcm_c906_prefix(u64 xi[2], const u128 *table, const u8 *in, size_t len);
int ossl_riscv_has_xtheadvector=1;
#ifdef GHASH_NATIVE
int CRYPTO_THREAD_run_once(CRYPTO_ONCE *once, void (*fn)(void)) {return pthread_once(once,fn)==0;}
#else
/* Only hardware/policy edges are mocked under QEMU; arithmetic is unchanged. */
int CRYPTO_THREAD_run_once(CRYPTO_ONCE *once, void (*fn)(void)) {
    if (*once==CRYPTO_ONCE_STATIC_INIT) {fn();*once=1;}return 1;
}
char *secure_getenv(const char *name) {(void)name;return "auto";}
int prctl(int op, ...) {(void)op;return 2;}
void *memcpy(void *dst,const void *src,size_t n) {
    unsigned char *d=dst;const unsigned char *s=src;
    for(size_t i=0;i<n;i++)d[i]=s[i];return dst;
}
void *memset(void *dst,int c,size_t n) {
    unsigned char *d=dst;for(size_t i=0;i<n;i++)d[i]=c;return dst;
}
void *__memcpy_chk(void *dst,const void *src,size_t n,size_t size) {
    if(n>size)__builtin_trap();return memcpy(dst,src,n);
}
int strcmp(const char *a,const char *b) {
    while(*a && *a==*b){a++;b++;}return (unsigned char)*a-(unsigned char)*b;
}
#endif
void OPENSSL_cleanse(void *p,size_t n) {volatile unsigned char *v=p;while(n--)*v++=0;}
static int equal(const void *a,const void *b,size_t n) {
    const u8 *x=a,*y=b;for(size_t i=0;i<n;i++)if(x[i]!=y[i])return 0;return 1;
}
/* Direct bit/byte implementation, independent of the shared assembly generator. */
static void multiply(u8 x[16],const u8 h[16]) {
    u8 z[16]={0},v[16];memcpy(v,h,16);
    for(int i=0;i<128;i++) {
        if(x[i/8] & (0x80>>(i%8)))for(int k=0;k<16;k++)z[k]^=v[k];
        int lsb=v[15]&1;
        for(int k=15;k>0;k--)v[k]=(v[k]>>1)|(v[k-1]<<7);
        v[0]>>=1;if(lsb)v[0]^=0xe1;
    }
    memcpy(x,z,16);
}
static void reference(u8 xi[16],const u8 h[16],const u8 *in,size_t len) {
    for(size_t i=0;i<len;i+=16) {
        for(int k=0;k<16;k++)xi[k]^=in[i+k];multiply(xi,h);
    }
}
static u64 big_endian(const u8 *p) {u64 n=0;for(int i=0;i<8;i++)n=n<<8|p[i];return n;}
static u64 rng=UINT64_C(88172645463325252);
static u64 random_word(void){rng^=rng<<13;rng^=rng>>7;rng^=rng<<17;return rng;}
static u8 input[16400+16],original[sizeof input];
int main(void) {
    static const size_t lengths[]={3072,3088,3104,3120,3136,3200,3216,4096,6144,16384,16400};
    /* NIST GCM zero-key test: first ciphertext block times its hash subkey. */
    u8 kat[16]={0x03,0x88,0xda,0xce,0x60,0xb6,0xa3,0x92,0xf3,0x28,0xc2,0xb9,0x71,0xb2,0xfe,0x78};
    const u8 hkat[16]={0x66,0xe9,0x4b,0xd4,0xef,0x8a,0x2c,0x3b,0x88,0x4c,0xfa,0x59,0xca,0x34,0x2b,0x2e};
    const u8 expected[16]={0x5e,0x2e,0xc7,0x46,0x91,0x70,0x62,0x88,0x2c,0x85,0xb0,0x68,0x53,0x53,0xde,0xb7};
    multiply(kat,hkat);if(!equal(kat,expected,16))return 2;
    int cases=0;
    for(int key=0;key<40;key++) {
        u8 h[16];for(int k=0;k<16;k++)h[k]=(u8)random_word();
        if(key==0)memset(h,0xff,16);
        if(key==1){memset(h,0,16);h[15]=1;}
        if(key==2){memset(h,0,16);h[0]=0x80;}
        if(key==3)memset(h,0,16);
        u128 table[16],saved_table[16];
        for(size_t i=0;i<sizeof table;i++)((u8 *)table)[i]=(u8)random_word();
        table[8].hi=big_endian(h);table[8].lo=big_endian(h+8);memcpy(saved_table,table,sizeof table);
        for(size_t li=0;li<sizeof lengths/sizeof *lengths;li++)for(int off=0;off<8;off++) {
            size_t len=lengths[li];
            for(size_t i=0;i<sizeof input;i++)input[i]=(u8)random_word();
            if(key==4)memset(input+off,0xff,len);
            memcpy(original,input,sizeof input);
            struct {u64 guard0[2],xi[2],guard1[2];} got,want;
            for(size_t i=0;i<sizeof got;i++)((u8 *)&got)[i]=(u8)random_word();
            memcpy(&want,&got,sizeof got);reference((u8 *)want.xi,h,input+off,len);
#ifdef NEGATIVE_DIGEST
            want.xi[0]^=1;
#endif
            size_t done=ossl_gcm_c906_prefix(got.xi,table,input+off,len);
            if(done!=len || !equal(&got,&want,sizeof got) || !equal(table,saved_table,sizeof table)
               || !equal(input,original,sizeof input)) {
#ifdef GHASH_NATIVE
                fprintf(stderr,"FAIL key=%d bytes=%zu offset=%d done=%zu\n",key,len,off,done);
#endif
                return 1;
            }
            cases++;
        }
    }
#ifdef GHASH_NATIVE
    printf("PASS: %d actual XTheadVector GHASH cases; keys, Xi, 0-4 scalar tails, offsets and guards\n",cases);
#endif
    return cases==3520?0:3;
}
