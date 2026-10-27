/* SPDX-License-Identifier: Apache-2.0 */
/* Compare real EVP output/IVs with an independently generated host fingerprint. */
#include <openssl/evp.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
static void check(int ok,const char *message){if(!ok){fprintf(stderr,"FAIL: %s\n",message);exit(2);}}
static uint64_t fingerprint=UINT64_C(14695981039346656037);
static void add(const unsigned char *p,size_t n){while(n--){fingerprint^=*p++;fingerprint*=UINT64_C(1099511628211);}}
int main(int argc,char **argv)
{
 unsigned char key[32],iv[16],next[16],input[7200],output[7200];unsigned cases=0;
 const char *modes[]={"ECB","CBC","CTR"};const int offsets[]={0,5,15};
 for(unsigned i=0;i<32;i++)key[i]=(unsigned char)(i*7+3);
 for(unsigned i=0;i<16;i++)iv[i]=(unsigned char)(i*11+3);
 for(unsigned k=128;k<=256;k+=64)for(unsigned mode=0;mode<3;mode++){
  char name[24];snprintf(name,sizeof name,"AES-%u-%s",k,modes[mode]);
  EVP_CIPHER *cipher=EVP_CIPHER_fetch(NULL,name,NULL);EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();check(cipher&&ctx,"fetch/context");
  for(size_t tail=16;tail<=3072;tail+=16)for(int enc=0;enc<=1;enc++)for(unsigned o=0;o<3;o++)for(int inplace=0;inplace<=1;inplace++){
   size_t len=4096+tail;int off=offsets[o],n,m;
   for(size_t i=0;i<sizeof input;i++)input[i]=(unsigned char)(i*13+21);
   memset(output,0xa7,sizeof output);if(inplace)memcpy(output+off,input+off,len);
   check(EVP_CipherInit_ex2(ctx,cipher,key,iv,enc,NULL)==1,"init");check(EVP_CIPHER_CTX_set_padding(ctx,0)==1,"padding");
   check(EVP_CipherUpdate(ctx,output+off,&n,inplace?output+off:input+off,(int)len)==1,"update");
   check(EVP_CipherFinal_ex(ctx,output+off+n,&m)==1&&(size_t)(n+m)==len,"final");
   for(int i=0;i<off;i++)check(output[i]==0xa7,"prefix guard");
   for(size_t i=off+len;i<sizeof output;i++)check(output[i]==0xa7,"suffix guard");
   add(output+off,len);
   if(mode){check(EVP_CIPHER_CTX_get_updated_iv(ctx,next,16)==1,"updated IV");add(next,16);}
   cases++;
  }
  EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_free(cipher);
 }
 printf("{\"cases\":%u,\"fingerprint\":\"%016llx\",\"openssl\":\"%s\"}\n",cases,(unsigned long long)fingerprint,OpenSSL_version(OPENSSL_VERSION));
 check(argc==1||(argc==2&&fingerprint==strtoull(argv[1],NULL,16)),"independent output/IV fingerprint");return 0;
}
