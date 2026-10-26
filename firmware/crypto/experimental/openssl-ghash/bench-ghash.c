#define _POSIX_C_SOURCE 200809L
#define OPENSSL_SUPPRESS_DEPRECATED
#include <openssl/evp.h>
#include <openssl/aes.h>
#include <openssl/modes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
static void ck(int ok){if(!ok){fputs("FAIL GHASH component\n",stderr);exit(1);}}
static uint64_t ns(void){struct timespec t;ck(!clock_gettime(CLOCK_PROCESS_CPUTIME_ID,&t));return (uint64_t)t.tv_sec*1000000000+t.tv_nsec;}
int main(void){
 unsigned char key[16]={0},iv[12]={0},data[16416],actual[16],expected[16];AES_KEY aes;
 memset(data,0x27,sizeof data);ck(!AES_set_encrypt_key(key,128,&aes));
 GCM128_CONTEXT *ctx=CRYPTO_gcm128_new(&aes,(block128_f)AES_encrypt);ck(ctx!=NULL);
 EVP_CIPHER_CTX *ref=EVP_CIPHER_CTX_new();int n;
 ck(ref&&EVP_EncryptInit_ex2(ref,EVP_aes_128_gcm(),key,iv,NULL));ck(EVP_EncryptUpdate(ref,NULL,&n,data+5,1024));
 ck(EVP_EncryptFinal_ex(ref,data,&n));ck(EVP_CIPHER_CTX_ctrl(ref,EVP_CTRL_AEAD_GET_TAG,16,expected));EVP_CIPHER_CTX_free(ref);
 CRYPTO_gcm128_setiv(ctx,iv,12);ck(!CRYPTO_gcm128_aad(ctx,data+5,1024));CRYPTO_gcm128_tag(ctx,actual,16);ck(!memcmp(actual,expected,16));
 const size_t sizes[]={16,64,256,1024,3072,4096,16384};
 for(size_t i=0;i<sizeof sizes/sizeof *sizes;i++){
  size_t size=sizes[i];uint64_t count=0,start=ns(),end;
  CRYPTO_gcm128_setiv(ctx,iv,12);
  do{for(int j=0;j<64;j++){ck(!CRYPTO_gcm128_aad(ctx,data+5,size));count++;}end=ns();}while(end-start<150000000);
  printf("{\"bytes\":%zu,\"iterations\":%llu,\"cpu_ns\":%llu,\"mib_per_cpu_second\":%.6f}\n",size,(unsigned long long)count,(unsigned long long)(end-start),count*(double)size*1e9/1048576/(end-start));
 }
 CRYPTO_gcm128_release(ctx);return 0;
}
