/* SPDX-License-Identifier: Apache-2.0 */
/* Link to the same production libcrypto.a. Synthetic keys/nonces only. */
#define _GNU_SOURCE
#include <openssl/crypto.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <time.h>
#include "crypto/chacha.h"
#include "crypto/riscv_arch.h"
#include "crypto/chacha/chacha_c906.h"
extern void ossl_chacha20_c906_blocks(unsigned int *, const unsigned char *, unsigned char *, size_t, int);
static unsigned char input[16416], output[16416];
static unsigned int key[8] = {7}, counter[4] = {1};
static volatile unsigned int sink;
static void check(int ok) { if (!ok) abort(); }
static uint64_t now(void) {
    struct timespec t; check(clock_gettime(CLOCK_THREAD_CPUTIME_ID, &t) == 0);
    return (uint64_t)t.tv_sec * 1000000000 + t.tv_nsec;
}
static uint64_t cycles(void) {
    uint64_t x; __asm__ volatile("rdcycle %0" : "=r"(x) : : "memory"); return x;
}
static void prepare(unsigned int s[16]) {
    s[0]=0x61707865; s[1]=0x3320646e; s[2]=0x79622d32; s[3]=0x6b206574;
    memcpy(s+4,key,32); memcpy(s+12,counter,16);
}
static void kernel(unsigned int s[16], size_t n) {
    size_t blocks=n/320, bytes=blocks*320;
    if(blocks) ossl_chacha20_c906_blocks(s,input+5,output+5,blocks,20);
    if(bytes!=n) ChaCha20_ctr32_c(output+5+bytes,input+5+bytes,n-bytes,key,s+12);
}
static void bench(const char *kind, int mode, size_t n, uint64_t duration) {
    unsigned int s[16]; prepare(s);
    uint64_t start=now(), end, count=0;
    do {
        for(int i=0;i<64;++i) {
            if(mode==0) { check(prctl(70,0L,0L,0L,0L)>=0); }
            else if(mode==1) { prepare(s); OPENSSL_cleanse(s,sizeof(s)); }
            else if(mode==2) { ChaCha20_ctr32_c(output+5,input+5,n,key,counter); }
            else if(mode==3) { kernel(s,n); }
            else if(mode==4 || mode==5) {
                if(mode==5) check((prctl(70,0L,0L,0L,0L)&3)==2);
                prepare(s); kernel(s,n); OPENSSL_cleanse(s,sizeof(s));
            } else {
                if(!ossl_chacha20_c906(output+5,input+5,n,key,counter))
                    ChaCha20_ctr32_c(output+5,input+5,n,key,counter);
            }
            sink^=output[5]; ++count;
        }
        end=now();
    } while(end-start<duration);
    printf("{\"kind\":\"overhead\",\"path\":\"%s\",\"bytes\":%zu,\"iterations\":%llu,\"cpu_ns_per_call\":%.3f}\n",
           kind,n,(unsigned long long)count,(double)(end-start)/count); fflush(stdout);
}
static void *first_vector(void *arg) {
    unsigned int s[16]; prepare(s);
    check((prctl(70,0L,0L,0L,0L)&3)==2);
    uint64_t a=cycles(); ossl_chacha20_c906_blocks(s,input+5,output+5,1,20); uint64_t b=cycles();
    ossl_chacha20_c906_blocks(s,input+5,output+5,1,20); uint64_t c=cycles();
    OPENSSL_cleanse(s,sizeof(s));
    printf("{\"kind\":\"first-vector\",\"thread\":%lu,\"first_cycles\":%llu,\"second_cycles\":%llu}\n",
           (unsigned long)arg,(unsigned long long)(b-a),(unsigned long long)(c-b));
    return NULL;
}
int main(int argc,char **argv) {
    double seconds=argc>1?strtod(argv[1],NULL):0.15; check(seconds>0 && seconds<=2);
    check(ossl_riscv_has_xtheadvector && (prctl(70,0L,0L,0L,0L)&3)==2);
    memset(input,0x37,sizeof(input));
    for(unsigned long i=0;i<20;++i) {
        pthread_t t; check(pthread_create(&t,NULL,first_vector,(void *)i)==0); check(pthread_join(t,NULL)==0);
    }
    uint64_t duration=(uint64_t)(seconds*1e9);
    bench("prctl",0,0,duration); bench("prepare-cleanse",1,0,duration);
    const size_t sizes[]={319,320,512,640,768,1024,1400,16384};
    const char *names[]={"scalar","kernel-and-tail","prepare-kernel-cleanse","prctl-prepare-kernel-cleanse","production-dispatch"};
    for(size_t i=0;i<sizeof(sizes)/sizeof(sizes[0]);++i)
        for(int mode=2;mode<=6;++mode) bench(names[mode-2],mode,sizes[i],duration);
}
