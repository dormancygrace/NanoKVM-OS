#define _POSIX_C_SOURCE 200809L
#include <openssl/evp.h>
#include <openssl/err.h>
#include <openssl/crypto.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <time.h>
#include <sys/resource.h>
#include "openssl-kats.h"
static volatile unsigned sink;
static void fail(const char *s) { fprintf(stderr,"FAIL %s\n",s); ERR_print_errors_fp(stderr); exit(2); }
static void ck(int ok,const char *s) { if(!ok) fail(s); }
static uint64_t ns(clockid_t clock) { struct timespec t; ck(clock_gettime(clock,&t)==0,"clock"); return (uint64_t)t.tv_sec*1000000000+t.tv_nsec; }
static double tv(struct timeval a) { return a.tv_sec+a.tv_usec/1e6; }
static size_t unhex(const char *s,unsigned char *p) { size_t n=strlen(s)/2; for(size_t i=0;i<n;i++){ unsigned x;ck(sscanf(s+2*i,"%2x",&x)==1,"hex");p[i]=(unsigned char)x;}return n; }
static int crypt(EVP_CIPHER_CTX *ctx,const EVP_CIPHER *cipher,int enc,const unsigned char *key,const unsigned char *iv,const unsigned char *aad,int alen,const unsigned char *in,int n,unsigned char *out,unsigned char tag[16],int split) {
 int got=0,total=0,k;
 ck(EVP_CipherInit_ex2(ctx,cipher,NULL,NULL,enc,NULL)==1,"cipher init");
 ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_SET_IVLEN,12,NULL)==1,"ivlen");
 ck(EVP_CipherInit_ex2(ctx,NULL,key,iv,enc,NULL)==1,"keyiv");
 ck(EVP_CipherUpdate(ctx,NULL,&got,aad,alen)==1,"aad");
 if(split>0&&split<n){ck(EVP_CipherUpdate(ctx,out,&got,in,split)==1,"update1");total=got;ck(EVP_CipherUpdate(ctx,out+total,&got,in+split,n-split)==1,"update2");total+=got;}
 else { ck(EVP_CipherUpdate(ctx,out,&got,in,n)==1,"update");total=got; }
 if(!enc)ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_SET_TAG,16,tag)==1,"set tag");
 k=EVP_CipherFinal_ex(ctx,out+total,&got);
 if(k!=1)return 0;
 ck(total+got==n,"output length");
 if(enc)ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_GET_TAG,16,tag)==1,"get tag");
 return 1;
}
static void correctness(void) {
 EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();ck(ctx!=NULL,"ctx");
 unsigned char key[32],iv[12],aad[256],plain[66000],out[66000],back[66000],expect[66000],tag[16],wanted[16];
 unsigned long checks=0;uint64_t fingerprint=1469598103934665603ULL;
 for(size_t i=0;i<sizeof(kats)/sizeof(kats[0]);i++){
  const struct kat *k=&kats[i];EVP_CIPHER *c=EVP_CIPHER_fetch(NULL,k->cipher,NULL);ck(c!=NULL,"kat cipher");
  unhex(k->key,key);ck(unhex(k->iv,iv)==12,"kat iv");int an=(int)unhex(k->aad,aad);int n=(int)unhex(k->plain,plain);ck(unhex(k->ciphertext,expect)==(size_t)n,"kat n");ck(unhex(k->tag,wanted)==16,"kat tag");
  #ifdef CORRUPT_KAT
  wanted[0]^=1;
#endif
  ck(crypt(ctx,c,1,key,iv,aad,an,plain,n,out,tag,0),"KAT encrypt");ck(memcmp(out,expect,(size_t)n)==0&&memcmp(tag,wanted,16)==0,"KAT mismatch");
  ck(crypt(ctx,c,0,key,iv,aad,an,out,n,back,tag,0)&&memcmp(back,plain,(size_t)n)==0,"KAT decrypt");checks++;EVP_CIPHER_free(c);
 }
 const char *algs[]={"CHACHA20-POLY1305","AES-128-GCM","AES-256-GCM"};
 int lengths[]={0,1,15,16,17,31,32,63,64,65,127,128,129,255,256,257,319,320,321,1400,4095,4096,4097,16383,16384,16385,65536};int offsets[]={0,1,5,7,15};
 for(size_t a=0;a<3;a++){
  EVP_CIPHER *c=EVP_CIPHER_fetch(NULL,algs[a],NULL);ck(c!=NULL,"cipher fetch");
  for(size_t z=0;z<sizeof(lengths)/sizeof(lengths[0]);z++)for(size_t o=0;o<5;o++){
   int n=lengths[z],off=offsets[o];for(int j=0;j<32;j++)key[j]=(unsigned char)(j*13+z);for(int j=0;j<12;j++)iv[j]=(unsigned char)(j*7+o);for(int j=0;j<23;j++)aad[j]=(unsigned char)(j+11);
   memset(plain,0x87,sizeof plain);memset(out,0xa5,sizeof out);memset(back,0x5a,sizeof back);for(int j=0;j<n;j++)plain[off+j]=(unsigned char)(j*17+z);
   ck(crypt(ctx,c,1,key,iv,aad,23,plain+off,n,out+off,tag,(int)(z%65)),"random encrypt");
   ck(crypt(ctx,c,0,key,iv,aad,23,out+off,n,back+off,tag,(int)(z%31)),"random decrypt");ck(memcmp(plain+off,back+off,(size_t)n)==0,"roundtrip mismatch");
   for(int j=0;j<off;j++)ck(out[j]==0xa5&&back[j]==0x5a,"prefix guard");
   for(int j=n+off;j<n+off+32;j++)ck(out[j]==0xa5&&back[j]==0x5a,"suffix guard");
   for(int j=0;j<n;j++){fingerprint^=out[off+j];fingerprint*=1099511628211ULL;}for(int j=0;j<16;j++){fingerprint^=tag[j];fingerprint*=1099511628211ULL;}
   tag[3]^=1;ck(!crypt(ctx,c,0,key,iv,aad,23,out+off,n,back+off,tag,0),"bad tag accepted");tag[3]^=1;
   if(n){out[off+n/2]^=1;ck(!crypt(ctx,c,0,key,iv,aad,23,out+off,n,back+off,tag,0),"bad ciphertext accepted");}
   checks++;
  }EVP_CIPHER_free(c);
 }
 EVP_CIPHER_CTX_free(ctx);
 printf("{\"kind\":\"correctness\",\"version\":\"%s\",\"cases\":%lu,\"fingerprint\":\"%016llx\"}\n",OpenSSL_version(OPENSSL_VERSION),checks,(unsigned long long)fingerprint);fflush(stdout);
}
static void output(const char *alg,const char *op,int n,int off,uint64_t count,uint64_t cpu,uint64_t wall,struct rusage *a,struct rusage *b){
 printf("{\"kind\":\"bench\",\"algorithm\":\"%s\",\"operation\":\"%s\",\"bytes\":%d,\"offset\":%d,\"iterations\":%llu,\"cpu_seconds\":%.9f,\"wall_seconds\":%.9f,\"user_seconds\":%.6f,\"kernel_seconds\":%.6f,\"mib_per_cpu_second\":%.6f}\n",alg,op,n,off,(unsigned long long)count,cpu/1e9,wall/1e9,tv(b->ru_utime)-tv(a->ru_utime),tv(b->ru_stime)-tv(a->ru_stime),count*(double)n*1e9/(1048576.0*cpu));fflush(stdout);
}
static void aead_bench(const char *name,int enc,int n,int off,uint64_t duration){
 EVP_CIPHER *cipher=EVP_CIPHER_fetch(NULL,name,NULL);EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();ck(cipher&&ctx,"bench ctx");
 unsigned char key[32]={0},iv[12]={0},aad[5]={23,3,3,0,0},plain[16432],encrypted[16432],out[16432],tag[16];for(int i=0;i<n;i++)plain[i+off]=(unsigned char)(i*13+7);aad[3]=(unsigned char)((n+16)>>8);aad[4]=(unsigned char)(n+16);
 ck(crypt(ctx,cipher,1,key,iv,aad,5,plain+off,n,encrypted+off,tag,0),"bench seed");
 ck(EVP_CipherInit_ex2(ctx,cipher,key,iv,enc,NULL)==1,"bench init");
 struct rusage r0,r1;getrusage(RUSAGE_SELF,&r0);uint64_t w0=ns(CLOCK_MONOTONIC),c0=ns(CLOCK_PROCESS_CPUTIME_ID),c1,count=0;
 do{for(int j=0;j<32;j++){
  int got,total;ck(EVP_CipherInit_ex2(ctx,NULL,NULL,iv,enc,NULL)==1,"record iv");ck(EVP_CipherUpdate(ctx,NULL,&got,aad,5)==1,"record aad");
  ck(EVP_CipherUpdate(ctx,out+off,&got,enc?plain+off:encrypted+off,n)==1,"record data");total=got;
  if(!enc)ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_SET_TAG,16,tag)==1,"record settag");
  ck(EVP_CipherFinal_ex(ctx,out+off+total,&got)==1,"record final");ck(total+got==n,"record length");
  if(enc)ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_GET_TAG,16,tag)==1,"record gettag");
  sink^=out[off];count++;
 }c1=ns(CLOCK_PROCESS_CPUTIME_ID);}while(c1-c0<duration);
 uint64_t wall=ns(CLOCK_MONOTONIC)-w0;getrusage(RUSAGE_SELF,&r1);output(name,enc?"seal":"open",n,off,count,c1-c0,wall,&r0,&r1);EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_free(cipher);
}
static void digest_bench(const char *name,int n,uint64_t duration){
 EVP_MD *md=EVP_MD_fetch(NULL,name,NULL);EVP_MD_CTX *ctx=EVP_MD_CTX_new();ck(md&&ctx,"digest ctx");unsigned char data[16384],out[64];memset(data,0x37,sizeof data);unsigned int len;
 struct rusage r0,r1;getrusage(RUSAGE_SELF,&r0);uint64_t w0=ns(CLOCK_MONOTONIC),c0=ns(CLOCK_PROCESS_CPUTIME_ID),c1,count=0;
 do{for(int j=0;j<64;j++){ck(EVP_DigestInit_ex2(ctx,md,NULL)==1&&EVP_DigestUpdate(ctx,data,(size_t)n)==1&&EVP_DigestFinal_ex(ctx,out,&len)==1,"digest");sink^=out[0];count++;}c1=ns(CLOCK_PROCESS_CPUTIME_ID);}while(c1-c0<duration);
 uint64_t wall=ns(CLOCK_MONOTONIC)-w0;getrusage(RUSAGE_SELF,&r1);output(name,"digest",n,0,count,c1-c0,wall,&r0,&r1);EVP_MD_CTX_free(ctx);EVP_MD_free(md);
}
int main(int argc,char **argv){
 uint64_t duration=argc>1?(uint64_t)(strtod(argv[1],NULL)*1e9):400000000ULL;
 correctness();if(duration==0)return 0;
 const char *algs[]={"CHACHA20-POLY1305","AES-128-GCM","AES-256-GCM"};int sizes[]={1400,16384};
 for(size_t a=0;a<3;a++)for(size_t n=0;n<2;n++)for(int enc=0;enc<=1;enc++)aead_bench(algs[a],enc,sizes[n],5,duration);
 const char *mds[]={"SHA1","SHA256","SHA512"};for(size_t a=0;a<3;a++)for(size_t n=0;n<2;n++)digest_bench(mds[a],sizes[n],duration);
 return 0;
}
