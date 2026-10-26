/* SPDX-License-Identifier: Apache-2.0 */
#include <assert.h>
#include <openssl/evp.h>
#include <openssl/provider.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "sg2002_cipher.c"
struct cipher_case {const char *name;unsigned alg,mode,keylen,block;};
static const struct cipher_case cases[]={
 {"AES-128-ECB",1,0,16,16},{"AES-128-CBC",1,1,16,16},{"AES-128-CTR",1,2,16,16},
 {"AES-192-ECB",1,0,24,16},{"AES-192-CBC",1,1,24,16},{"AES-192-CTR",1,2,24,16},
 {"AES-256-ECB",1,0,32,16},{"AES-256-CBC",1,1,32,16},{"AES-256-CTR",1,2,32,16},
 {"DES-ECB",2,0,8,8},{"DES-CBC",2,1,8,8},
 {"DES-EDE",3,0,16,8},{"DES-EDE-CBC",3,1,16,8},
 {"DES-EDE3",3,0,24,8},{"DES-EDE3-CBC",3,1,24,8},
 {"SM4-ECB",4,0,16,16},{"SM4-CBC",4,1,16,16},{"SM4-CTR",4,2,16,16}};
static const struct cipher_case *current;
static EVP_CIPHER *cipher;
static unsigned char key[32];
static int fail_after,calls,opens,closes;
static void software(int enc,const unsigned char *in,unsigned char *out,size_t len,unsigned char *iv)
{
 EVP_CIPHER_CTX *c=EVP_CIPHER_CTX_new();int n,k;assert(c);
 assert(EVP_CipherInit_ex2(c,cipher,key,iv,enc,NULL));assert(EVP_CIPHER_CTX_set_padding(c,0));
 assert(EVP_CipherUpdate(c,out,&n,in,(int)len));assert(EVP_CipherFinal_ex(c,out+n,&k));assert((size_t)(n+k)==len);
 if(current->mode!=0) { assert(EVP_CIPHER_CTX_get_updated_iv(c,iv,current->block)); }
 EVP_CIPHER_CTX_free(c);
}
int ossl_sg2002_begin(unsigned alg){assert(alg==current->alg);opens++;return fail_after==-2?-1:37;}
void ossl_sg2002_end(int fd){assert(fd==37);closes++;}
int ossl_sg2002_run(int fd,struct sg2002_crypto_request *r)
{
 assert(fd==37&&r->algorithm==current->alg&&r->mode==current->mode);
 assert(r->key_length==current->keylen&&!memcmp(r->key,key,current->keylen));
 if(calls++==fail_after)return 0;
 unsigned char iv[16];memcpy(iv,r->iv,16);
 software(r->encrypt,(const unsigned char*)(uintptr_t)r->input,(unsigned char*)(uintptr_t)r->output,r->length,iv);
 return 1;
}
static void ctr32_reference(const unsigned char *in,unsigned char *out,size_t len,unsigned char iv[16])
{
 EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();assert(ctx);
 assert(EVP_EncryptInit_ex2(ctx,EVP_aes_128_ecb(),key,NULL,NULL));assert(EVP_CIPHER_CTX_set_padding(ctx,0));
 uint32_t counter=(uint32_t)iv[12]<<24|(uint32_t)iv[13]<<16|(uint32_t)iv[14]<<8|iv[15];
 for(size_t p=0;p<len;p+=16){
  unsigned char c[16],stream[16];int n;memcpy(c,iv,16);
  for(unsigned k=0;k<4;k++)c[12+k]=(unsigned char)(counter>>(24-8*k));
  assert(EVP_EncryptUpdate(ctx,stream,&n,c,16)&&n==16);
  for(unsigned k=0;k<16;k++)out[p+k]=in[p+k]^stream[k];
  counter++;
 }
 for(unsigned k=0;k<4;k++)iv[12+k]=(unsigned char)(counter>>(24-8*k));
 EVP_CIPHER_CTX_free(ctx);
}
static void check_gcm_counter(void)
{
 unsigned char input[16384],want[16384],buf[16384],iv[16],next[16],expected[16];
 memset(input,0x4b,sizeof input);current=&cases[2];cipher=EVP_CIPHER_fetch(NULL,current->name,NULL);assert(cipher);
 for(size_t len=1024;len<=16384;len*=2)for(fail_after=-2;fail_after<=2;fail_after++){
  memset(iv,0xff,16);iv[0]=0xa5;iv[15]=0xfe;memcpy(expected,iv,16);
  ctr32_reference(input,want,len,expected);memcpy(next,iv,16);memcpy(buf,input,len);calls=opens=closes=0;
  size_t n=ossl_sg2002_cipher(1,2,1,key,16,next,4,buf,buf,len,1024);
  ctr32_reference(buf+n,buf+n,len-n,next);
  assert(!memcmp(buf,want,len)&&!memcmp(next,expected,16));
 }
 EVP_CIPHER_free(cipher);
 puts("PASS 25 GCM CTR32 rollover/fault cases: nonce prefix never carries");
}

int main(void)
{
 OSSL_PROVIDER *def=OSSL_PROVIDER_load(NULL,"default"),*legacy=OSSL_PROVIDER_load(NULL,"legacy");assert(def&&legacy);
 unsigned char input[16432],original[16432],want[16432],buf[16432],iv[16],next[16],expected_iv[16];unsigned count=0;
 const size_t sizes[]={0,16,512,1008,1024,1025,4095,4096,4112,8192,10000,16384};const int offsets[]={0,5,15};
 for(unsigned k=0;k<32;k++)key[k]=(unsigned char)(k*7+3);
 for(size_t i=0;i<sizeof original;i++)original[i]=(unsigned char)(i*13+21);
 for(size_t a=0;a<sizeof cases/sizeof cases[0];a++){
  current=&cases[a];cipher=EVP_CIPHER_fetch(NULL,current->name,NULL);assert(cipher);
  for(size_t z=0;z<sizeof sizes/sizeof sizes[0];z++)for(int enc=0;enc<=1;enc++)for(size_t o=0;o<3;o++)for(fail_after=-2;fail_after<=2;fail_after++){
   size_t len=sizes[z];int off=offsets[o];if(current->mode!=2&&len%current->block)continue;
   memset(iv,0xff,16);iv[0]=0xa5;iv[15]=0xfe; /* full CTR carry across low-word boundary */
   memcpy(input,original,sizeof input);memcpy(next,iv,16);
   if(!enc)software(1,original+off,input+off,len,next);
   memcpy(expected_iv,iv,16);software(enc,input+off,want,len,expected_iv);
   memset(buf,0xa7,sizeof buf);memcpy(buf+off,input+off,len);memcpy(next,iv,16);calls=opens=closes=0;
   size_t n=ossl_sg2002_cipher(current->alg,current->mode,enc,key,current->keylen,next,current->block,buf+off,buf+off,len,1024);
   assert(n<=len&&n%current->block==0);
   if(len<1024)assert(!opens&&!calls&&!closes);
   else if(fail_after==-2)assert(opens==1&&!calls&&!closes);else assert(opens==1&&closes==1);
   software(enc,buf+off+n,buf+off+n,len-n,next);
   assert(!memcmp(buf+off,want,len));if(current->mode)assert(!memcmp(next,expected_iv,current->block));
   for(int k=0;k<off;k++) { assert(buf[k]==0xa7); }
   for(size_t k=off+len;k<off+len+16;k++) { assert(buf[k]==0xa7); }
   count++;
  }EVP_CIPHER_free(cipher);
 }
 check_gcm_counter();
 OSSL_PROVIDER_unload(legacy);OSSL_PROVIDER_unload(def);
 printf("PASS %u block-cipher continuation cases across 18 AES/DES/TDES/SM4 modes\n",count);
}
