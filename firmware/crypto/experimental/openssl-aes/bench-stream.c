/* SPDX-License-Identifier: Apache-2.0 */
#define _POSIX_C_SOURCE 200809L
#include <openssl/evp.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
static volatile unsigned sink;
static void check(int ok){if(!ok){fputs("FAIL stream benchmark\n",stderr);exit(2);}}
static uint64_t ns(void){struct timespec t;check(clock_gettime(CLOCK_PROCESS_CPUTIME_ID,&t)==0);return (uint64_t)t.tv_sec*1000000000+t.tv_nsec;}
int main(int argc,char **argv){check(argc==2);EVP_CIPHER *cipher=EVP_CIPHER_fetch(NULL,argv[1],NULL);EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();check(cipher&&ctx);unsigned char key[32]={0},iv[16]={0},in[16400],out[16400];memset(in,0x37,sizeof in);const int sizes[]={16,64,128,256,1024,16384};
 for(unsigned s=0;s<6;s++)for(int enc=0;enc<=1;enc++){int n;check(EVP_CipherInit_ex2(ctx,cipher,key,iv,enc,NULL)==1);check(EVP_CIPHER_CTX_set_padding(ctx,0)==1);uint64_t start=ns(),end,rounds=0;
  do{for(unsigned i=0;i<16;i++){check(EVP_CipherUpdate(ctx,out+5,&n,in+5,sizes[s])==1&&n==sizes[s]);sink^=out[5];rounds++;}end=ns();}while(end-start<25000000);
  check(EVP_CipherFinal_ex(ctx,out,&n)==1&&n==0);
  printf("{\"algorithm\":\"%s\",\"encrypt\":%d,\"bytes\":%d,\"rounds\":%llu,\"cpu_ns\":%llu,\"mib_per_cpu_s\":%.6f}\n",argv[1],enc,sizes[s],(unsigned long long)rounds,(unsigned long long)(end-start),(double)sizes[s]*rounds*1e9/((end-start)*1048576.0));
 }EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_free(cipher);return 0;}
