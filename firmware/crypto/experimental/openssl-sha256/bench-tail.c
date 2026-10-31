/* SPDX-License-Identifier: Apache-2.0 */
/* Native compression comparison; EVP setup is intentionally excluded. */
#define _GNU_SOURCE
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <time.h>
#include <pthread.h>
#include <openssl/crypto.h>
#include <openssl/sha.h>
#include "crypto/hwcrypto_transport.h"
size_t base_sha256(SHA256_CTX *,const unsigned char *,size_t);
size_t tail_sha256(SHA256_CTX *,const unsigned char *,size_t);
void ossl_sha256_c906_blocks(void *,const void *,size_t);
int CRYPTO_THREAD_run_once(CRYPTO_ONCE *o,void (*fn)(void)){return pthread_once(o,fn)==0;}
void OPENSSL_cleanse(void *p,size_t n){volatile unsigned char *v=p;while(n--)*v++=0;}
static double now(clockid_t c){struct timespec t;assert(!clock_gettime(c,&t));return t.tv_sec+t.tv_nsec*1e-9;}
static unsigned char input[16384];
static const unsigned initial[8]={0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19};
int main(void){
 for(unsigned i=0;i<sizeof input;i++)input[i]=(unsigned char)(i*29+3);
 int fd=ossl_hwcrypto_begin(HWCRYPTO_ALG_SHA256);assert(fd>=0);
 for(unsigned n=512;n<=4096;n+=64){
  struct hwcrypto_request r={.version=1,.algorithm=HWCRYPTO_ALG_SHA256,.length=n,.input=(uintptr_t)input};
  unsigned want[8];memcpy(want,initial,32);
  for(int i=0;i<8;i++)for(int j=0;j<4;j++)r.state[4*i+j]=want[i]>>(24-8*j);
  ossl_sha256_c906_blocks(want,input,n/64);assert(ossl_hwcrypto_run(fd,&r));
  for(int i=0;i<8;i++)for(int j=0;j<4;j++)assert(r.state[4*i+j]==(unsigned char)(want[i]>>(24-8*j)));
 }
 ossl_hwcrypto_end(fd);puts("{\"driver_cases\":57,\"pass\":true}");
 size_t sizes[]={4096,4608,5120,6144,8192,8704,4160,4224,4352,4480};
 for(unsigned trial=0;trial<5;trial++)for(unsigned q=0;q<sizeof sizes/sizeof *sizes;q++)for(unsigned order=0;order<2;order++){
  unsigned candidate=(trial+order)&1;size_t n=sizes[q],loops=0;SHA256_CTX c,want;memset(&want,0,sizeof want);memcpy(want.h,initial,32);
  ossl_sha256_c906_blocks(&want,input,n/64);double start=now(CLOCK_PROCESS_CPUTIME_ID),wall=now(CLOCK_MONOTONIC),elapsed;
  do {
   memset(&c,0,sizeof c);memcpy(c.h,initial,32);
   size_t done=(candidate?tail_sha256:base_sha256)(&c,input,n/64);
   size_t expected=(n/4096)*64;
   if(candidate && n%4096>=512)expected=n/64;
   assert(done==expected); /* Fail instead of measuring a silent DMA fallback. */
   if(done!=n/64)ossl_sha256_c906_blocks(&c,input+done*64,n/64-done);
   assert(!memcmp(&c,&want,sizeof c));loops++;
   elapsed=now(CLOCK_PROCESS_CPUTIME_ID)-start;
  }while(elapsed<0.18);
  printf("{\"trial\":%u,\"candidate\":%u,\"bytes\":%zu,\"loops\":%zu,\"cpu_ns\":%.3f,\"wall_s\":%.6f}\n",trial,candidate,n,loops,elapsed*1e9/loops,now(CLOCK_MONOTONIC)-wall);fflush(stdout);
 }
 return 0;
}
