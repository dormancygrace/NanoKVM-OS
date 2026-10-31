/* SPDX-License-Identifier: GPL-2.0-only */
/* Standard cryptodev AEAD against OpenSSL default-provider software.
 * Synthetic fixed nonce/key are for repeatable measurement, never real data. */
#define main private_gcm_benchmark_main
#include "gcm-bench.c"
#undef main
#include <crypto/cryptodev.h>
#include <errno.h>
static int cfd;
static int standard_gcm(unsigned session, int enc, unsigned char *src, size_t len,
                        unsigned char *dst, unsigned char *auth, size_t authlen, unsigned taglen)
{
 unsigned char iv[12]; memcpy(iv, nonce, sizeof(iv));
 struct crypt_auth_op op = {.ses=session,.op=enc?COP_ENCRYPT:COP_DECRYPT,
  .len=(unsigned)len,.auth_len=(unsigned)authlen,.auth_src=auth,.src=src,.dst=dst,
  .tag_len=taglen,.iv=iv,.iv_len=sizeof(iv)};
 return ioctl(cfd,CIOCAUTHCRYPT,&op);
}
static int sw_gcm(EVP_CIPHER_CTX *ctx, int enc, unsigned char *src, size_t len,
                  unsigned char *dst, unsigned char *auth, size_t authlen, unsigned taglen)
{
 int n=0,total=0;
 if(!EVP_CipherInit_ex(ctx,NULL,NULL,NULL,nonce,enc))return -1;
 if(!enc && !EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_SET_TAG,(int)taglen,src+len))return -1;
 if(authlen && !EVP_CipherUpdate(ctx,NULL,&n,auth,(int)authlen))return -1;
 if(!EVP_CipherUpdate(ctx,dst,&n,src,(int)len))return -1;
 total=n;
 if(!EVP_CipherFinal_ex(ctx,dst+total,&n))return -1;
 if(enc && !EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_GET_TAG,(int)taglen,dst+len))return -1;
 return total+n==(int)len?0:-1;
}
int main(int argc,char **argv)
{
 int kat_only=argc>1 && !strcmp(argv[1],"--kat");
 static unsigned char in[65536+16],out[65536+16],ref[65536+16],plain[65536+16],auth[17];
 const size_t checks[]={0,1,15,16,17,1200,4095,4096,4097,16384,65536};
 const size_t sizes[]={128,512,1200,4096,16384,65536};
 const unsigned tags[]={16,12,8};
 setvbuf(stdout,NULL,_IOLBF,0);
 cfd=open("/dev/crypto",O_RDWR|O_CLOEXEC);fd=open("/dev/sg2002-aes-probe",O_RDWR|O_CLOEXEC);
 if(cfd<0||fd<0){perror("open");return 1;}
 for(unsigned i=0;i<65536;i++)in[i]=(unsigned char)(i*31+7);
 memcpy(auth,aad,sizeof(auth));
 puts("CIOCAUTHCRYPT plain AEAD; reusable session; 12-byte nonce; setup excluded; per-operation IV/AAD/tag included");
 for(unsigned bits=128;bits<=256;bits+=64){
  char name[32];snprintf(name,sizeof(name),"AES-%u-GCM",bits);
  unsigned char key[32];for(unsigned i=0;i<sizeof(key);i++)key[i]=(unsigned char)i;
  struct session_op session={.cipher=CRYPTO_AES_GCM,.keylen=bits/8,.key=key};
  if(ioctl(cfd,CIOCGSESSION,&session)){perror("CIOCGSESSION AES-GCM");return 1;}
  struct session_info_op info={.ses=session.ses};
  if(ioctl(cfd,CIOCGSESSINFO,&info)){perror("CIOCGSESSINFO");return 1;}
  printf("DRIVER,%s,%s,flags=%u\n",name,info.cipher_info.cra_driver_name,info.flags);
  if(!strstr(info.cipher_info.cra_driver_name,"sg2002-aes-ctr")){fputs("Not hardware AES\n",stderr);return 1;}
  EVP_CIPHER *cipher=EVP_CIPHER_fetch(NULL,name,"provider=default");
  EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();
  if(!cipher||!ctx||!EVP_CipherInit_ex(ctx,cipher,NULL,key,nonce,1))return 1;
  for(unsigned t=0;t<sizeof(tags)/sizeof(tags[0]);t++)for(unsigned ai=0;ai<2;ai++)for(unsigned i=0;i<sizeof(checks)/sizeof(checks[0]);i++){
   size_t len=checks[i],alen=ai?sizeof(auth):0;unsigned tag=tags[t];
   printf("CHECK,%s,%zu,aad=%zu,tag=%u\n",name,len,alen,tag);
   if(sw_gcm(ctx,1,in,len,ref,auth,alen,tag)||standard_gcm(session.ses,1,in,len,out,auth,alen,tag)){perror("encrypt");return 1;}
   if(memcmp(out,ref,len+tag)){fputs("ciphertext/tag mismatch\n",stderr);return 1;}
   if(standard_gcm(session.ses,0,out,len+tag,plain,auth,alen,tag)||memcmp(in,plain,len)){perror("decrypt");return 1;}
   memcpy(plain,in,len);
   if(standard_gcm(session.ses,1,plain,len,plain,auth,alen,tag)||memcmp(ref,plain,len+tag))return 1;
   if(standard_gcm(session.ses,0,plain,len+tag,plain,auth,alen,tag)||memcmp(in,plain,len))return 1;
   out[len]^=1;errno=0;
   if(standard_gcm(session.ses,0,out,len+tag,plain,auth,alen,tag)!=-1 || errno!=EBADMSG){fputs("Bad tag not rejected as EBADMSG\n",stderr);return 1;}
   out[len]^=1;
   if(alen){auth[0]^=1;errno=0;if(standard_gcm(session.ses,0,out,len+tag,plain,auth,alen,tag)!=-1||errno!=EBADMSG)return 1;auth[0]^=1;}
  }
  printf("KAT_PASS %s standard AEAD; both directions, in-place, boundaries, truncated tags, tampered tag/AAD\n",name);
  /* cryptodev 1.14 compares a new tag length to the current authsize; use a fresh session after short-tag tests. */
  if(ioctl(cfd,CIOCFSESSION,&session.ses)||ioctl(cfd,CIOCGSESSION,&session))return 1;
  if(!kat_only)for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++)for(unsigned enc=0;enc<2;enc++){
   size_t len=sizes[i];
   if(sw_gcm(ctx,1,in,len,ref,auth,sizeof(auth),16))return 1;
   for(unsigned round=0;round<3;round++)for(unsigned order=0;order<2;order++){
    unsigned hw=(round+order)%2,count=32;
    struct sg2002_crypto_info before,after;
    if(ioctl(fd,SG2002_CRYPTO_INFO,&before))return 1;
    uint64_t wall=ns(CLOCK_MONOTONIC),cpu=ns(CLOCK_PROCESS_CPUTIME_ID);
    for(unsigned n=0;n<count;n++)if(hw?standard_gcm(session.ses,enc,enc?in:ref,len+(enc?0:16),out,auth,sizeof(auth),16):sw_gcm(ctx,enc,enc?in:ref,len,out,auth,sizeof(auth),16)){perror("bench");return 1;}
    cpu=ns(CLOCK_PROCESS_CPUTIME_ID)-cpu;wall=ns(CLOCK_MONOTONIC)-wall;
    if(ioctl(fd,SG2002_CRYPTO_INFO,&after))return 1;
    uint64_t calls=after.completed[SG2002_ALG_AES]-before.completed[SG2002_ALG_AES];
    if(after.poisoned||(hw&&!calls)||(!hw&&calls))return 1;
    if(memcmp(out,enc?ref:in,len+(enc?16:0)))return 1;
    printf("RESULT,%s,%s,%zu,%s,%u,%u,%llu,%llu,%llu\n",name,enc?"enc":"dec",len,hw?"devcrypto":"sw",round,count,(unsigned long long)cpu,(unsigned long long)wall,(unsigned long long)calls);
   }
  }
  EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_free(cipher);
  if(ioctl(cfd,CIOCFSESSION,&session.ses))return 1;
 }
 close(cfd);close(fd);puts("PASS");return 0;
}
