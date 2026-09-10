/* SPDX-License-Identifier: GPL-2.0-only */
/* Reuse the independent OpenSSL software oracle and input cases. */
#define main native_benchmark_main
#include "crypto-bench.c"
#undef main
#include <crypto/cryptodev.h>
static int devfd;
static unsigned standard_id(struct test_case *t)
{
 if(t->alg==SG2002_ALG_SHA1)return CRYPTO_SHA1;
 if(t->alg==SG2002_ALG_SHA256)return CRYPTO_SHA2_256;
 if(t->alg==SG2002_ALG_AES)return t->mode==0?CRYPTO_AES_ECB:t->mode==1?CRYPTO_AES_CBC:CRYPTO_AES_CTR;
 if(t->mode==1&&t->alg==SG2002_ALG_DES)return CRYPTO_DES_CBC;
 if(t->mode==1&&t->alg==SG2002_ALG_TDES)return CRYPTO_3DES_CBC;
 return 0;
}
static int dev_op(struct test_case *t,unsigned session,int encrypt,const unsigned char *src,size_t len,unsigned char *dst,unsigned flags)
{
 unsigned char vector[16];memcpy(vector,iv,16);
 struct crypt_op op={.ses=session,.op=encrypt?COP_ENCRYPT:COP_DECRYPT,.flags=flags,.len=(unsigned)len,
  .src=(void *)src,.dst=dst,.mac=dst,.iv=vector};
 if(ioctl(devfd,CIOCCRYPT,&op)){perror("CIOCCRYPT");return -1;}
 return t->alg==5?20:t->alg==6?32:(int)len;
}
/* Keep the default short smoke benchmark, but permit longer matched batches.
 * Parse before opening devices so invalid controls cannot submit DMA work. */
static unsigned benchmark_setting(const char *name,unsigned fallback,unsigned maximum)
{
 const char *value=getenv(name);if(!value)return fallback;
 if(!*value||strspn(value,"0123456789")!=strlen(value)){
  fprintf(stderr,"invalid %s\n",name);exit(2);
 }
 errno=0;char *end;unsigned long n=strtoul(value,&end,10);
 if(errno||*end||n>maximum){fprintf(stderr,"invalid %s\n",name);exit(2);}
 return (unsigned)n;
}
int main(int argc,char **argv)
{
 int timed=argc>1&&!strcmp(argv[1],"--bench");
 unsigned count=benchmark_setting("SG2_BENCH_COUNT",32,4096);
 unsigned selected_bytes=benchmark_setting("SG2_BENCH_BYTES",0,65536);
 if(!count||(selected_bytes&&selected_bytes!=1200&&selected_bytes!=16384&&selected_bytes!=65536)){
  fprintf(stderr,"SG2_BENCH_COUNT must be 1..4096; SG2_BENCH_BYTES must be 0, 1200, 16384 or 65536\n");return 2;
 }
 setvbuf(stdout,NULL,_IOLBF,0);
 fd=open("/dev/sg2002-aes-probe",O_RDWR|O_CLOEXEC);devfd=open("/dev/crypto",O_RDWR|O_CLOEXEC);
 if(fd<0||devfd<0){perror("devices");return 1;}
 unsigned seed=getenv("SG2_BENCH_SEED")?(unsigned)strtoul(getenv("SG2_BENCH_SEED"),NULL,10):0;
 cipher_ctx=EVP_CIPHER_CTX_new();if(!cipher_ctx)return 1;
 for(unsigned i=0;i<sizeof(key);i++)key[i]=(unsigned char)(i+seed);
 for(unsigned i=0;i<sizeof(iv);i++)iv[i]=(unsigned char)(0xa0+i+seed);
 for(unsigned i=0;i<sizeof(plain);i++)plain[i]=(unsigned char)(i*29+17+seed);
 printf("OPENSSL %s; standard CIOCGSESSION/CIOCCRYPT, default zero-copy enabled\n",OpenSSL_version(OPENSSL_VERSION));
 if(timed)printf("BENCH_CONFIG count=%u bytes=%u (0=all sizes)\n",count,selected_bytes);
 for(unsigned index=0;index<sizeof(cases)/sizeof(cases[0]);index++){
  struct test_case *t=&cases[index];unsigned id=standard_id(t);if(!id)continue;
  int hash=t->alg==5||t->alg==6;
  struct session_op session={0};unsigned char session_key[32];memcpy(session_key,key,32);
  if(hash)session.mac=id;else{session.cipher=id;session.key=session_key;session.keylen=t->keylen;
   if(t->alg==3&&t->keylen==16){memcpy(session_key+16,key,8);session.keylen=24;}}
  if(ioctl(devfd,CIOCGSESSION,&session)){perror("session");return 1;}
  struct session_info_op info={.ses=session.ses};
  if(ioctl(devfd,CIOCGSESSINFO,&info)||!(info.flags&SIOP_FLAG_KERNEL_DRIVER_ONLY))return 1;
  const char *driver=hash?info.hash_info.cra_driver_name:info.cipher_info.cra_driver_name;
  if(strncmp(driver,"sg2002-",7)){fprintf(stderr,"unexpected driver %s\n",driver);return 1;}
  printf("DRIVER,%s,%s\n",t->name,driver);
  if(t->alg==1){t->cipher=EVP_CIPHER_fetch(NULL,t->name,"provider=default");if(!t->cipher)return 1;}
  const size_t checks[]={0,1,15,16,17,55,56,63,64,65,1200,4095,4096,4097,8192,65536};
  for(unsigned j=0;j<sizeof(checks)/sizeof(checks[0]);j++){
   size_t len=checks[j];unsigned block=t->alg==2||t->alg==3?8:16;
   if(!hash&&t->mode!=2&&len%block)continue;
   int n=sw_op(t,1,plain,len,swout),m=dev_op(t,session.ses,1,plain,len,hwout,0);
   if(same(t->name,len,m,n))return 1;
   if(!hash){
    m=dev_op(t,session.ses,0,hwout,len,encoded,0);
    if(m!=(int)len||memcmp(encoded,plain,len)){fprintf(stderr,"decrypt mismatch %s %zu\n",t->name,len);return 1;}
    memcpy(encoded,plain,len);m=dev_op(t,session.ses,1,encoded,len,encoded,0);
    if(m!=(int)len||memcmp(encoded,swout,len)){fprintf(stderr,"in-place mismatch\n");return 1;}
   }
  }
  if(hash){
   struct crypt_op part={.ses=session.ses,.op=COP_ENCRYPT,.flags=COP_FLAG_UPDATE|COP_FLAG_RESET,.len=13,.src=plain};
   if(ioctl(devfd,CIOCCRYPT,&part))return 1;
   struct session_op clone={.mac=id};if(ioctl(devfd,CIOCGSESSION,&clone))return 1;
   struct cphash_op cp={.src_ses=session.ses,.dst_ses=clone.ses};
   if(ioctl(devfd,CIOCCPHASH,&cp))return 1;
   int n=sw_op(t,1,plain,1200,swout);
   int m=dev_op(t,session.ses,1,plain+13,1187,hwout,COP_FLAG_FINAL);
   if(same(t->name,1200,m,n))return 1;
   m=dev_op(t,clone.ses,1,plain+13,1187,hwout,COP_FLAG_FINAL);
   if(same(t->name,1200,m,n)||ioctl(devfd,CIOCFSESSION,&clone.ses))return 1;
  }
  printf("KAT_PASS %s standard API\n",t->name);
  if(timed&&(t->alg==5||t->alg==6||(t->alg==1&&t->mode==2))){
   const size_t sizes[]={1200,16384,65536};
   for(unsigned j=0;j<3;j++)for(unsigned round=0;round<3;round++)for(unsigned order=0;order<2;order++){
    if(selected_bytes&&sizes[j]!=selected_bytes)continue;
    int hw=(round+order)%2;struct sg2002_crypto_info before,after;
    if(ioctl(fd,SG2002_CRYPTO_INFO,&before))return 1;
    uint64_t wall=ns(CLOCK_MONOTONIC),cpu=ns(CLOCK_PROCESS_CPUTIME_ID);
    for(unsigned k=0;k<count;k++)if((hw?dev_op(t,session.ses,1,plain,sizes[j],hwout,0):sw_op(t,1,plain,sizes[j],swout))<0)return 1;
    cpu=ns(CLOCK_PROCESS_CPUTIME_ID)-cpu;wall=ns(CLOCK_MONOTONIC)-wall;
    if(ioctl(fd,SG2002_CRYPTO_INFO,&after))return 1;
    uint64_t calls=after.completed[t->alg]-before.completed[t->alg];
    if(after.poisoned||(hw&&!calls)||(!hw&&calls))return 1;
    printf("RESULT,%s,enc,%zu,%s,%u,%u,%llu,%llu,%llu\n",t->name,sizes[j],hw?"devcrypto":"sw",round,count,(unsigned long long)cpu,(unsigned long long)wall,(unsigned long long)calls);
   }
  }
  EVP_CIPHER_free(t->cipher);t->cipher=NULL;
  if(ioctl(devfd,CIOCFSESSION,&session.ses))return 1;
 }
 EVP_CIPHER_CTX_free(cipher_ctx);close(devfd);close(fd);puts("PASS");return 0;
}
