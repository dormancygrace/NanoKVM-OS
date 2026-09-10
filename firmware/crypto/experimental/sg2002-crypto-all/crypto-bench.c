/* SPDX-License-Identifier: GPL-2.0-only */
#define _POSIX_C_SOURCE 200809L
#define OPENSSL_SUPPRESS_DEPRECATED
#include <openssl/evp.h>
#include <openssl/des.h>
#include <openssl/crypto.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <time.h>
#include <sys/ioctl.h>
#include "sg2002_crypto.h"
#include "../sg2002-aes-probe/sg2002_aes_probe.h"

struct test_case { const char *name; unsigned alg, mode, keylen; EVP_CIPHER *cipher; };
static struct test_case cases[] = {
 {"SHA256",SG2002_ALG_SHA256,0,0,NULL}, {"SHA1",SG2002_ALG_SHA1,0,0,NULL},
 {"AES-128-ECB",1,0,16,NULL}, {"AES-128-CBC",1,1,16,NULL}, {"AES-128-CTR",1,2,16,NULL},
 {"AES-192-ECB",1,0,24,NULL}, {"AES-192-CBC",1,1,24,NULL}, {"AES-192-CTR",1,2,24,NULL},
 {"AES-256-ECB",1,0,32,NULL}, {"AES-256-CBC",1,1,32,NULL}, {"AES-256-CTR",1,2,32,NULL},
 {"DES-ECB",2,0,8,NULL}, {"DES-CBC",2,1,8,NULL}, {"DES-CTR",2,2,8,NULL},
 {"TDES-2KEY-ECB",3,0,16,NULL}, {"TDES-2KEY-CBC",3,1,16,NULL}, {"TDES-2KEY-CTR",3,2,16,NULL},
 {"TDES-3KEY-ECB",3,0,24,NULL}, {"TDES-3KEY-CBC",3,1,24,NULL}, {"TDES-3KEY-CTR",3,2,24,NULL},
 {"SM4-ECB",4,0,16,NULL}, {"SM4-CBC",4,1,16,NULL}, {"SM4-CTR",4,2,16,NULL},
 {"BASE64",SG2002_ALG_BASE64,0,0,NULL},
};
static int fd;
static EVP_CIPHER_CTX *cipher_ctx;
static unsigned char key[32], iv[16], plain[131072], swout[180000], hwout[180000], encoded[180000];
static uint64_t ns(clockid_t clock)
{
 struct timespec t; if (clock_gettime(clock,&t)) { perror("clock"); exit(1); }
 return (uint64_t)t.tv_sec*1000000000ULL+t.tv_nsec;
}
static void increment(unsigned char *v, unsigned n, unsigned blocks)
{
 while (blocks--) { for (unsigned i=n; i>0; i--) if (++v[i-1]) break; }
}
static void be32(unsigned char *out, uint32_t n)
{
 for (int i=3;i>=0;i--) {out[i]=(unsigned char)n;n>>=8;}
}
static int run(struct sg2002_crypto_request *r)
{
 if (ioctl(fd,SG2002_CRYPTO_RUN,r)<0) { perror("hardware ioctl"); return -1; }
 unsigned expected=(r->algorithm==5||r->algorithm==6)?2:1;
 if (r->status != expected) {fprintf(stderr,"no successful DMA completion\n");return -1;}
 return 0;
}
static int hw_hash(unsigned alg, const unsigned char *src, size_t len, unsigned char *dst)
{
 static const uint32_t sha256_iv[]={0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19};
 static const uint32_t sha1_iv[]={0x67452301,0xefcdab89,0x98badcfe,0x10325476,0xc3d2e1f0};
 unsigned words=alg==SG2002_ALG_SHA256?8:5;
 struct sg2002_crypto_request r={.version=1,.algorithm=alg};
 for (unsigned i=0;i<words;i++) be32(r.state+i*4,(words==8?sha256_iv:sha1_iv)[i]);
 size_t full=len&~(size_t)63, pos=0;
 while (pos<full) {
  r.length=(full-pos>SG2002_CRYPTO_MAX)?SG2002_CRYPTO_MAX:(unsigned)(full-pos);
  r.input=(uintptr_t)(src+pos);
  if (run(&r)) return -1;
  pos+=r.length;
 }
 unsigned char tail[128]={0};
 size_t rem=len-full, tail_len=rem<56?64:128;
 memcpy(tail,src+full,rem);tail[rem]=0x80;
 uint64_t bits=(uint64_t)len*8;
 for (unsigned i=0;i<8;i++) {tail[tail_len-1-i]=(unsigned char)bits;bits>>=8;}
 r.input=(uintptr_t)tail;r.length=(unsigned)tail_len;
 if(run(&r))return -1;
 memcpy(dst,r.state,words*4);return (int)(words*4);
}
static int hw_op(struct test_case *t, int encrypt, const unsigned char *src, size_t len, unsigned char *dst)
{
 if (t->alg==5||t->alg==6) return hw_hash(t->alg,src,len,dst);
 size_t pos=0,out=0;
 unsigned block=(t->alg==2||t->alg==3)?8:16;
 unsigned char next_iv[16];memcpy(next_iv,iv,16);
 while (pos<len) {
  struct sg2002_crypto_request r={.version=1,.algorithm=t->alg,.mode=t->mode,
   .encrypt=(unsigned)encrypt,.key_length=t->keylen,.output_length=SG2002_CRYPTO_OUT_MAX};
  size_t cap=(t->alg==7&&encrypt)?4095:SG2002_CRYPTO_MAX;
  r.length=(unsigned)((len-pos<cap)?len-pos:cap);
  r.input=(uintptr_t)(src+pos);r.output=(uintptr_t)(dst+out);
  memcpy(r.key,key,t->keylen);memcpy(r.iv,next_iv,16);
  if (run(&r)) return -1;
  if (t->alg!=7&&t->mode==SG2002_MODE_CBC)
   memcpy(next_iv,(encrypt?dst+out:src+pos)+r.length-block,block);
  if (t->alg!=7&&t->mode==SG2002_MODE_CTR) increment(next_iv,block,(r.length+block-1)/block);
  pos+=r.length;out+=r.output_length;
 }
 return (int)out;
}
static int sw_des(struct test_case *t,int encrypt,const unsigned char *src,size_t len,unsigned char *dst)
{
 DES_key_schedule k1,k2,k3;DES_cblock vector;
 DES_set_key_unchecked((const_DES_cblock *)key,&k1);
 if (t->alg==3) {
  DES_set_key_unchecked((const_DES_cblock *)(key+8),&k2);
  DES_set_key_unchecked((const_DES_cblock *)(t->keylen==16?key:key+16),&k3);
 }
 memcpy(vector,iv,8);
 if(t->mode==SG2002_MODE_CBC) {
  if(t->alg==3)DES_ede3_cbc_encrypt(src,dst,(long)len,&k1,&k2,&k3,&vector,encrypt);
  else DES_ncbc_encrypt(src,dst,(long)len,&k1,&vector,encrypt);
 }else{
  for(size_t i=0;i<len;i+=8){
   DES_cblock tmp;
   const unsigned char *input=t->mode==SG2002_MODE_CTR?vector:src+i;
   int enc=t->mode==SG2002_MODE_CTR?1:encrypt;
   if(t->alg==3)DES_ecb3_encrypt((const_DES_cblock *)input,&tmp,&k1,&k2,&k3,enc);
   else DES_ecb_encrypt((const_DES_cblock *)input,&tmp,&k1,enc);
   size_t n=len-i<8?len-i:8;
   if(t->mode==SG2002_MODE_CTR){for(size_t j=0;j<n;j++)dst[i+j]=src[i+j]^tmp[j];increment(vector,8,1);}
   else memcpy(dst+i,tmp,n);
  }
 }
 return (int)len;
}
static int sw_op(struct test_case *t,int encrypt,const unsigned char *src,size_t len,unsigned char *dst)
{
 if(t->alg==5||t->alg==6){
  unsigned n=0;if(!EVP_Digest(src,len,dst,&n,t->alg==6?EVP_sha256():EVP_sha1(),NULL))return -1;
  return (int)n;
 }
 if(t->alg==7){
  if(encrypt)return EVP_EncodeBlock(dst,src,(int)len);
  int n=EVP_DecodeBlock(dst,src,(int)len);
  if(n<0)return -1;
  if(len&&src[len-1]=='=')n--;
  if(len>1&&src[len-2]=='=')n--;
  return n;
 }
 if(t->alg==2||t->alg==3)return sw_des(t,encrypt,src,len,dst);
 int n=0,last=0;
 if(!EVP_CipherInit_ex(cipher_ctx,t->cipher,NULL,key,iv,encrypt)||
    !EVP_CIPHER_CTX_set_padding(cipher_ctx,0)||
    !EVP_CipherUpdate(cipher_ctx,dst,&n,src,(int)len)||
    !EVP_CipherFinal_ex(cipher_ctx,dst+n,&last))return -1;
 return n+last;
}
static int same(const char *name,size_t len,int got,int want)
{
 if(got<0||got!=want||memcmp(swout,hwout,(size_t)want)){
  fprintf(stderr,"MISMATCH %s input=%zu hw=%d sw=%d\n",name,len,got,want);
  if(got>0&&want>0){for(int i=0;i<got&&i<32;i++)fprintf(stderr,"%02x",hwout[i]);fprintf(stderr," hw\n");
   for(int i=0;i<want&&i<32;i++)fprintf(stderr,"%02x",swout[i]);
   fprintf(stderr," sw\n");}
  return -1;
 }
 return 0;
}
static int kat(struct test_case *t)
{
 const size_t lengths[]={0,1,2,3,15,16,17,55,56,63,64,65,1200,4095,4096,4097,8192};
 printf("KAT_START %s\n",t->name);fflush(stdout);
 for(size_t j=0;j<sizeof(lengths)/sizeof(lengths[0]);j++){
  size_t len=lengths[j];unsigned block=(t->alg==2||t->alg==3)?8:16;
  if(t->alg<=4&&(!len||(t->mode!=2&&len%block)))continue;
  int n=sw_op(t,1,plain,len,swout),m=hw_op(t,1,plain,len,hwout);
  if(same(t->name,len,m,n))return -1;
  if(t->alg==5||t->alg==6)continue;
  memcpy(encoded,swout,(size_t)n);
  n=sw_op(t,0,encoded,(size_t)n,swout);m=hw_op(t,0,encoded,(size_t)m,hwout);
  if(same(t->name,len,m,n)||n!=(int)len||memcmp(hwout,plain,len))return -1;
 }
 printf("KAT_PASS %s\n",t->name);fflush(stdout);return 0;
}
static int check_invalid(void)
{
 struct sg2002_crypto_info before,after;
 if(ioctl(fd,SG2002_CRYPTO_INFO,&before))return -1;
 for(unsigned variant=0;variant<7;variant++){
  struct sg2002_crypto_request r={.version=1,.algorithm=1,.key_length=16,.length=16,.encrypt=1,.output_length=16,
   .input=(uintptr_t)plain,.output=(uintptr_t)hwout};
  switch(variant){case 0:r.version=99;break;case 1:r.algorithm=8;break;case 2:r.mode=9;break;
   case 3:r.length=4097;break;case 4:r.output_length=1;break;case 5:r.reserved[0]=1;break;case 6:r.input=1;break;}
  if(ioctl(fd,SG2002_CRYPTO_RUN,&r)==0){fprintf(stderr,"invalid request accepted %u\n",variant);return -1;}
 }
 if(ioctl(fd,SG2002_CRYPTO_INFO,&after)||memcmp(before.submitted,after.submitted,sizeof(before.submitted))){
  fprintf(stderr,"invalid request submitted DMA\n");return -1;
 }
 puts("INVALID_REQUESTS_PASS");return 0;
}
static int check_legacy(void)
{
 struct sg2002_aes_request r={.length=64};
 memcpy(r.key,key,16); memcpy(r.iv,iv,16); memcpy(r.data,plain,64);
 struct test_case *t=&cases[2];
 /* Locate CTR independently of table order. */
 for(size_t i=0;i<sizeof(cases)/sizeof(cases[0]);i++)if(!strcmp(cases[i].name,"AES-128-CTR"))t=&cases[i];
 t->cipher=EVP_CIPHER_fetch(NULL,t->name,"provider=default");
 int n=t->cipher?sw_op(t,1,plain,64,swout):-1;
 EVP_CIPHER_free(t->cipher);t->cipher=NULL;
 if(n!=64||ioctl(fd,SG2002_AES_CTR,&r)||r.status!=1||memcmp(r.data,swout,64)){
  fprintf(stderr,"legacy AES128CTR mismatch\n");return -1;
 }
 puts("LEGACY_AES128_CTR_PASS");return 0;
}
static int bench(struct test_case *t,unsigned iterations)
{
 const size_t lengths[]={64,256,1200,4096,65536};
 for(size_t j=0;j<sizeof(lengths)/sizeof(lengths[0]);j++)for(int enc=1;enc>=0;enc--){
  if(!enc&&(t->alg==5||t->alg==6))continue;
  const unsigned char *src=plain;size_t len=lengths[j];
  if(!enc){int n=sw_op(t,1,plain,len,encoded);if(n<0)return -1;src=encoded;len=(size_t)n;}
  unsigned count=j==4?16:iterations;
  for(unsigned round=0;round<3;round++)for(unsigned order=0;order<2;order++){
   int hardware=((order+round)%2)==1;
   struct sg2002_crypto_info before,after;
   if(ioctl(fd,SG2002_CRYPTO_INFO,&before))return -1;
   uint64_t start_wall=ns(CLOCK_MONOTONIC),start_cpu=ns(CLOCK_PROCESS_CPUTIME_ID);
   int result=0;
   for(unsigned k=0;k<count;k++){
    result=hardware?hw_op(t,enc,src,len,hwout):sw_op(t,enc,src,len,swout);
    if(result<0)return -1;
   }
   uint64_t cpu=ns(CLOCK_PROCESS_CPUTIME_ID)-start_cpu,wall=ns(CLOCK_MONOTONIC)-start_wall;
   if(ioctl(fd,SG2002_CRYPTO_INFO,&after))return -1;
   uint64_t calls=after.completed[t->alg]-before.completed[t->alg];
   if(after.poisoned||(hardware&&!calls)||(!hardware&&calls)){fprintf(stderr,"backend counter mismatch\n");return -1;}
   printf("RESULT,%s,%s,%zu,%s,%u,%u,%llu,%llu,%llu\n",t->name,enc?"enc":"dec",len,hardware?"hw":"sw",round,count,
    (unsigned long long)cpu,(unsigned long long)wall,(unsigned long long)calls);
   fflush(stdout);
  }
 }
 return 0;
}
int main(int argc,char **argv)
{
 const char *only=NULL;int timed=0;unsigned iterations=16;
 for(int i=1;i<argc;i++){
  if(!strcmp(argv[i],"--case")&&i+1<argc)only=argv[++i];
  else if(!strcmp(argv[i],"--kat"))timed=0;
  else if(!strcmp(argv[i],"--bench")){timed=1;if(i+1<argc&&argv[i+1][0]!='-')iterations=(unsigned)strtoul(argv[++i],NULL,10);}
  else {fprintf(stderr,"usage: %s [--case NAME] [--kat | --bench [iterations]]\n",argv[0]);return 2;}
 }
 if(!iterations||iterations>256)return 2;
 setvbuf(stdout,NULL,_IOLBF,0);
 fd=open("/dev/sg2002-aes-probe",O_RDWR|O_CLOEXEC);if(fd<0){perror("device");return 1;}
 struct sg2002_crypto_info info;
 if(ioctl(fd,SG2002_CRYPTO_INFO,&info)||info.version!=1||info.poisoned){perror("crypto capabilities");return 1;}
 printf("OPENSSL %s\n",OpenSSL_version(OPENSSL_VERSION));
 printf("ABI %u algorithms=%#x input=%u output=%u\n",info.version,info.algorithms,info.max_input,info.max_output);
 for(unsigned i=0;i<sizeof(key);i++)key[i]=(unsigned char)i;
 for(unsigned i=0;i<sizeof(iv);i++)iv[i]=(unsigned char)(0xa0+i);
 for(unsigned i=0;i<sizeof(plain);i++)plain[i]=(unsigned char)(i*29+17);
 cipher_ctx=EVP_CIPHER_CTX_new();if(!cipher_ctx)return 1;
 if(check_invalid()||check_legacy())return 1;
 int found=0;
 for(size_t i=0;i<sizeof(cases)/sizeof(cases[0]);i++){
  struct test_case *t=&cases[i];if(only&&strcmp(only,t->name))continue;found=1;
  if(t->alg==1||t->alg==4){t->cipher=EVP_CIPHER_fetch(NULL,t->name,"provider=default");if(!t->cipher){fprintf(stderr,"missing software cipher %s\n",t->name);return 1;}}
  if(kat(t)||(timed&&bench(t,iterations)))return 1;
  EVP_CIPHER_free(t->cipher);t->cipher=NULL;
 }
 EVP_CIPHER_CTX_free(cipher_ctx);close(fd);
 if(!found)return 2;
 puts("PASS");return 0;
}
