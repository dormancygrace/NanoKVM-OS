/* SPDX-License-Identifier: Apache-2.0 */
/* Link to the matching static libcrypto; these transport symbols are private. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>
#include <openssl/evp.h>
#include "crypto/hwcrypto_transport.h"
static volatile unsigned sink;
static void ck(int ok,const char *s){if(!ok){perror(s);exit(2);}}
static uint64_t ns(clockid_t c){struct timespec v;ck(clock_gettime(c,&v)==0,"clock");return (uint64_t)v.tv_sec*1000000000+v.tv_nsec;}
static const unsigned char initial[32]={0x6a,0x09,0xe6,0x67,0xbb,0x67,0xae,0x85,0x3c,0x6e,0xf3,0x72,0xa5,0x4f,0xf5,0x3a,0x51,0x0e,0x52,0x7f,0x9b,0x05,0x68,0x8c,0x1f,0x83,0xd9,0xab,0x5b,0xe0,0xcd,0x19};
static unsigned char input[4096],digest[32];
static EVP_MD_CTX *ctx;
static EVP_MD *fetched;
static void hash(int reuse){unsigned n;ck(EVP_DigestInit_ex2(ctx,reuse?fetched:EVP_sha256(),NULL)==1,"init");ck(EVP_DigestUpdate(ctx,input,sizeof input)==1,"update");ck(EVP_DigestFinal_ex(ctx,digest,&n)==1&&n==32,"final");sink^=digest[0];}
int main(int argc,char **argv){
 memset(input,0x27,sizeof input);ctx=EVP_MD_CTX_new();ck(ctx!=NULL,"context");
 fetched=EVP_MD_fetch(NULL,"SHA256",NULL);ck(fetched!=NULL,"fetch");
 if(argc==2&&!strcmp(argv[1],"--hash-once")){hash(1);for(unsigned i=0;i<32;i++)printf("%02x",digest[i]);puts("");EVP_MD_CTX_free(ctx);EVP_MD_free(fetched);return 0;}
 ck(argc==1,"arguments");
 uint64_t c=ns(CLOCK_PROCESS_CPUTIME_ID),w=ns(CLOCK_MONOTONIC);int fd=ossl_hwcrypto_begin(HWCRYPTO_ALG_SHA256);ck(fd>=0,"first begin");ossl_hwcrypto_end(fd);
 printf("{\"operation\":\"cold-begin-end\",\"iterations\":1,\"cpu_ns\":%llu,\"wall_ns\":%llu}\n",(unsigned long long)(ns(CLOCK_PROCESS_CPUTIME_ID)-c),(unsigned long long)(ns(CLOCK_MONOTONIC)-w));
 hash(1); /* Load provider and warm the full-EVP path outside repeated timings. */
 static const char *names[]={"empty","open-close","open-fstat-close","warm-begin-end","sha256-run-held-fd","sha256-full-evp-legacy","sha256-full-evp-fetched"};
 for(unsigned mode=0;mode<7;mode++){
  fd=mode==4?ossl_hwcrypto_begin(HWCRYPTO_ALG_SHA256):-1;
  if(mode==4)ck(fd>=0,"held fd");
  w=ns(CLOCK_MONOTONIC);c=ns(CLOCK_PROCESS_CPUTIME_ID);
  for(unsigned i=0;i<10000;i++){
   if(mode==0){sink^=i;continue;}
   if(mode==1||mode==2){int f=open("/dev/sg2002-aes-probe",O_RDWR|O_CLOEXEC);ck(f>=0,"open");if(mode==2){struct stat st;ck(fstat(f,&st)==0&&S_ISCHR(st.st_mode),"fstat");}ck(close(f)==0,"close");}
   else if(mode==3){int f=ossl_hwcrypto_begin(HWCRYPTO_ALG_SHA256);ck(f>=0,"warm begin");ossl_hwcrypto_end(f);}
   else if(mode==4){struct hwcrypto_request request={.version=HWCRYPTO_ABI,.algorithm=HWCRYPTO_ALG_SHA256,.length=sizeof input,.input=(uintptr_t)input};memcpy(request.state,initial,32);ck(ossl_hwcrypto_run(fd,&request),"run");sink^=request.state[0];}
   else hash(mode==6);
  }
  uint64_t cpu=ns(CLOCK_PROCESS_CPUTIME_ID)-c,wall=ns(CLOCK_MONOTONIC)-w;
  if(mode==4)ossl_hwcrypto_end(fd);
  printf("{\"operation\":\"%s\",\"iterations\":10000,\"cpu_ns\":%llu,\"wall_ns\":%llu,\"cpu_ns_per_call\":%.3f}\n",names[mode],(unsigned long long)cpu,(unsigned long long)wall,cpu/10000.0);fflush(stdout);
 }
 EVP_MD_CTX_free(ctx);EVP_MD_free(fetched);return 0;
}
