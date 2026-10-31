/* SPDX-License-Identifier: GPL-2.0-only */
/* Synthetic benchmark only. Hardware AES via the shared driver, GHASH via
 * OpenSSL's GCM core. No TLS/provider configuration is changed by this program.
 */
#define _POSIX_C_SOURCE 200809L
#include <openssl/evp.h>
#include <openssl/modes.h>
#include <openssl/crypto.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <time.h>
#include <sys/ioctl.h>
#include "sg2002_crypto.h"

static int fd;
struct hw_key { unsigned char bytes[32]; unsigned length; int failed; };
static unsigned char input[65536], expected[65536], actual[65536], recovered[65536];
static const unsigned char nonce[12]={0,1,2,3,4,5,6,7,8,9,10,11};
static const unsigned char aad[17]="synthetic-header";
static uint64_t ns(clockid_t id){struct timespec t;clock_gettime(id,&t);return (uint64_t)t.tv_sec*1000000000ULL+t.tv_nsec;}
static void inc32(unsigned char *v,unsigned n){while(n--){for(unsigned i=16;i>12;i--)if(++v[i-1])break;}}
static int submit(struct hw_key *k,struct sg2002_crypto_request *r)
{
 if(k->failed)return -1;
 if(ioctl(fd,SG2002_CRYPTO_RUN,r)<0||r->status!=1){k->failed=1;perror("AES hardware");return -1;}
 return 0;
}
static void block(const unsigned char in[16],unsigned char out[16],const void *opaque)
{
 struct hw_key *k=(struct hw_key *)opaque;
 struct sg2002_crypto_request r={.version=1,.algorithm=SG2002_ALG_AES,.encrypt=1,
  .key_length=k->length,.length=16,.output_length=16,.input=(uintptr_t)in,.output=(uintptr_t)out};
 memcpy(r.key,k->bytes,k->length);
 if(submit(k,&r))memset(out,0,16);
}
static void ctr(const unsigned char *in,unsigned char *out,size_t blocks,const void *opaque,const unsigned char ivec[16])
{
 struct hw_key *k=(struct hw_key *)opaque;
 unsigned char counter[16];memcpy(counter,ivec,16);
 while(blocks){
  unsigned n=blocks>SG2002_CRYPTO_MAX/16?SG2002_CRYPTO_MAX/16:(unsigned)blocks;
  struct sg2002_crypto_request r={.version=1,.algorithm=SG2002_ALG_AES,.encrypt=1,.mode=SG2002_MODE_CTR,
   .key_length=k->length,.length=n*16,.output_length=n*16,.input=(uintptr_t)in,.output=(uintptr_t)out};
  memcpy(r.key,k->bytes,k->length);memcpy(r.iv,counter,16);
  if(submit(k,&r)){memset(out,0,blocks*16);return;}
  inc32(counter,n);in+=n*16;out+=n*16;blocks-=n;
 }
}
static int hybrid(GCM128_CONTEXT *g,struct hw_key *k,int enc,const unsigned char *src,size_t len,unsigned char *dst,unsigned char tag[16])
{
 if(k->failed)return -1;
 CRYPTO_gcm128_setiv(g,nonce,sizeof(nonce));
 if(CRYPTO_gcm128_aad(g,aad,sizeof(aad)))return -1;
 int err=enc?CRYPTO_gcm128_encrypt_ctr32(g,src,dst,len,ctr):CRYPTO_gcm128_decrypt_ctr32(g,src,dst,len,ctr);
 if(err||k->failed)return -1;
 if(enc)CRYPTO_gcm128_tag(g,tag,16);
 else if(CRYPTO_gcm128_finish(g,tag,16))return -1;
 return k->failed?-1:0;
}
static int software(EVP_CIPHER_CTX *ctx,const unsigned char *src,size_t len,unsigned char *dst,unsigned char tag[16])
{
 int n=0,tail=0;
 if(!EVP_EncryptInit_ex(ctx,NULL,NULL,NULL,nonce)||!EVP_EncryptUpdate(ctx,NULL,&n,aad,sizeof(aad))||
    !EVP_EncryptUpdate(ctx,dst,&n,src,(int)len)||!EVP_EncryptFinal_ex(ctx,dst+n,&tail)||
    !EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_GET_TAG,16,tag))return -1;
 return n+tail==(int)len?0:-1;
}
int main(int argc,char **argv)
{
 int kat_only=argc>1&&!strcmp(argv[1],"--kat");
 setvbuf(stdout,NULL,_IOLBF,0);
 fd=open("/dev/sg2002-aes-probe",O_RDWR|O_CLOEXEC);if(fd<0){perror("device");return 1;}
 struct sg2002_crypto_info info;
 if(ioctl(fd,SG2002_CRYPTO_INFO,&info)||info.poisoned)return 1;
 for(unsigned i=0;i<sizeof(input);i++)input[i]=(unsigned char)(i*31+7);
 printf("OPENSSL %s; fixed-key setup excluded, nonce/AAD/tag and DMA copying included\n",OpenSSL_version(OPENSSL_VERSION));
 const char *names[]={"AES-128-GCM","AES-256-GCM"};
 for(unsigned variant=0;variant<2;variant++){
  struct hw_key k={.length=variant?32:16};for(unsigned i=0;i<k.length;i++)k.bytes[i]=(unsigned char)i;
  EVP_CIPHER *cipher=EVP_CIPHER_fetch(NULL,names[variant],"provider=default");
  EVP_CIPHER_CTX *sw=EVP_CIPHER_CTX_new();
  if(!cipher||!sw||!EVP_EncryptInit_ex(sw,cipher,NULL,k.bytes,nonce))return 1;
  GCM128_CONTEXT *g=CRYPTO_gcm128_new(&k,block);if(!g||k.failed)return 1;
  const size_t checks[]={0,1,15,16,17,1200,4095,4096,4097,16384,65536};
  printf("KAT_START %s-HYBRID\n",names[variant]);
  for(unsigned i=0;i<sizeof(checks)/sizeof(checks[0]);i++){
   size_t len=checks[i];unsigned char expected_tag[16],tag[16];
   if(software(sw,input,len,expected,expected_tag)||hybrid(g,&k,1,input,len,actual,tag))return 1;
   if(memcmp(actual,expected,len)||memcmp(tag,expected_tag,16)){
    fprintf(stderr,"GCM mismatch %s len=%zu\n",names[variant],len);return 1;
   }
   if(hybrid(g,&k,0,actual,len,recovered,tag)||memcmp(recovered,input,len))return 1;
   tag[0]^=1;
   if(hybrid(g,&k,0,actual,len,recovered,tag)==0){fprintf(stderr,"accepted invalid GCM tag\n");return 1;}
  }
  printf("KAT_PASS %s-HYBRID\n",names[variant]);
  const size_t sizes[]={128,512,1200,4096,16384,65536};
  if(!kat_only)for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++)for(unsigned round=0;round<3;round++)for(unsigned order=0;order<2;order++){
   int hw=(round+order)%2;unsigned count=sizes[i]>16384?16:64;unsigned char tag[16];
   struct sg2002_crypto_info before,after;ioctl(fd,SG2002_CRYPTO_INFO,&before);
   uint64_t wall=ns(CLOCK_MONOTONIC),cpu=ns(CLOCK_PROCESS_CPUTIME_ID);
   for(unsigned n=0;n<count;n++)if(hw?hybrid(g,&k,1,input,sizes[i],actual,tag):software(sw,input,sizes[i],expected,tag))return 1;
   cpu=ns(CLOCK_PROCESS_CPUTIME_ID)-cpu;wall=ns(CLOCK_MONOTONIC)-wall;
   ioctl(fd,SG2002_CRYPTO_INFO,&after);
   uint64_t calls=after.completed[SG2002_ALG_AES]-before.completed[SG2002_ALG_AES];
   if(after.poisoned||(hw&&!calls)||(!hw&&calls))return 1;
   printf("RESULT,%s,%zu,%s,%u,%u,%llu,%llu,%llu\n",names[variant],sizes[i],hw?"hw-aes-sw-ghash":"sw",round,count,
    (unsigned long long)cpu,(unsigned long long)wall,(unsigned long long)calls);
  }
  CRYPTO_gcm128_release(g);EVP_CIPHER_CTX_free(sw);EVP_CIPHER_free(cipher);
 }
 // Same nonce/AAD/tag contract and persistent-key setup for ChaCha comparison.
 unsigned char chacha_key[32]={0};EVP_CIPHER *cc=EVP_CIPHER_fetch(NULL,"CHACHA20-POLY1305","provider=default");
 EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();if(!cc||!ctx||!EVP_EncryptInit_ex(ctx,cc,NULL,chacha_key,nonce))return 1;
 const size_t sizes[]={128,512,1200,4096,16384,65536};
 if(!kat_only)for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++)for(unsigned round=0;round<3;round++){
  unsigned count=sizes[i]>16384?16:64;unsigned char tag[16];
  uint64_t wall=ns(CLOCK_MONOTONIC),cpu=ns(CLOCK_PROCESS_CPUTIME_ID);
  for(unsigned n=0;n<count;n++)if(software(ctx,input,sizes[i],expected,tag))return 1;
  cpu=ns(CLOCK_PROCESS_CPUTIME_ID)-cpu;wall=ns(CLOCK_MONOTONIC)-wall;
  printf("RESULT,CHACHA20-POLY1305,%zu,sw,%u,%u,%llu,%llu,0\n",sizes[i],round,count,(unsigned long long)cpu,(unsigned long long)wall);
 }
 EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_free(cc);close(fd);puts("PASS");return 0;
}
