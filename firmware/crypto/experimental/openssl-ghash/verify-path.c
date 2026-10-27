/* SPDX-License-Identifier: Apache-2.0 */
/* Run against an NKOS_GHASH_TEST library; pair with verify-gcm.c for KATs. */
#define _GNU_SOURCE
#define OPENSSL_SUPPRESS_DEPRECATED
#include <openssl/aes.h>
#include <openssl/modes.h>
#include <dlfcn.h>
#include <sys/prctl.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
static void ck(int ok,const char *what){if(!ok){fprintf(stderr,"FAIL %s\n",what);exit(2);}}
int main(void){
 unsigned long (*hits)(void)=(unsigned long (*)(void))dlsym(RTLD_DEFAULT,"nkos_ghash_test_hits");
 ck(hits!=NULL,"instrumented assembly counter");
 int control=prctl(70,0L,0L,0L,0L);ck(control>=0,"vector control");
 const char *opt=getenv("NANOKVM_OPENSSL_GHASH");int enabled=(control&3)==2&&(!opt||strcmp(opt,"generic"));
 unsigned char key[16]={0},iv[12]={0},data[16416],tag[16];AES_KEY aes;memset(data,0x71,sizeof(data));
 ck(AES_set_encrypt_key(key,128,&aes)==0,"AES key");
 GCM128_CONTEXT *ctx=CRYPTO_gcm128_new(&aes,(block128_f)AES_encrypt);ck(ctx!=NULL,"GCM context");
 const size_t sizes[]={0,16,64,1024,3071,3072,4096,16384};unsigned checks=0;
 for(size_t off=0;off<16;off+=5)for(size_t i=0;i<sizeof(sizes)/sizeof(*sizes);++i){
  CRYPTO_gcm128_setiv(ctx,iv,sizeof(iv));unsigned long before=hits();
  ck(CRYPTO_gcm128_aad(ctx,data+off,sizes[i])==0,"AAD");CRYPTO_gcm128_tag(ctx,tag,sizeof(tag));
  ck((hits()>before)==(enabled&&sizes[i]>=3072),"actual assembly-entry count");checks++;
 }
 CRYPTO_gcm128_release(ctx);
 printf("PASS %u GHASH entry proofs; vector_enabled=%d actual_asm_hits=%lu\n",checks,enabled,hits());return 0;
}
