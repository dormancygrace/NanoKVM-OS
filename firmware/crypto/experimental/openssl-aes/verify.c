/* SPDX-License-Identifier: Apache-2.0 */
#include <openssl/evp.h>
#include <openssl/provider.h>
#include <openssl/err.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define LIMIT 65536
static void ck(int ok, const char *what)
{
    if (!ok) { fprintf(stderr, "FAIL %s\n", what); ERR_print_errors_fp(stderr); exit(2); }
}
static void init(EVP_CIPHER_CTX *c, EVP_CIPHER *cipher, const unsigned char *key,
                 const unsigned char *iv, int encrypt)
{
    ck(EVP_CipherInit_ex2(c, cipher, key, iv, encrypt, NULL) == 1, "init");
    ck(EVP_CIPHER_CTX_set_padding(c, 0) == 1, "padding");
}
static void whole(EVP_CIPHER_CTX *c, EVP_CIPHER *cipher, const unsigned char *key,
                  const unsigned char *iv, int enc, unsigned char *out,
                  const unsigned char *in, int len)
{
    int n, m;
    init(c, cipher, key, iv, enc);
    ck(EVP_CipherUpdate(c, out, &n, in, len) == 1, "whole update");
    ck(EVP_CipherFinal_ex(c, out+n, &m) == 1 && n+m == len, "whole final");
}
static void streamed(EVP_CIPHER_CTX *c, EVP_CIPHER_CTX *dup, EVP_CIPHER *cipher,
                     const unsigned char *key, const unsigned char *iv, int enc,
                     unsigned char *out, unsigned char *copy,
                     const unsigned char *in, int len)
{
    int first = len ? 1 : 0, n, m, total, copied, pos;
    init(c, cipher, key, iv, enc);
    ck(EVP_CipherUpdate(c, out, &n, in, first) == 1, "stream prefix");
    ck(EVP_CIPHER_CTX_copy(dup, c) == 1, "context copy");
    memcpy(copy, out, (size_t)n); total = copied = n; pos = first;
    while (pos < len) {
        int step = pos % 3 ? 4097 : 31;
        if (step > len-pos) step = len-pos;
        ck(EVP_CipherUpdate(c, out+total, &n, in+pos, step) == 1, "stream update");
        ck(EVP_CipherUpdate(dup, copy+copied, &m, in+pos, step) == 1, "copied update");
        total += n; copied += m; pos += step;
    }
    ck(EVP_CipherFinal_ex(c, out+total, &n) == 1, "stream final");
    ck(EVP_CipherFinal_ex(dup, copy+copied, &m) == 1, "copied final");
    ck(total+n == len && copied+m == len, "stream lengths");
    ck(!memcmp(out, copy, (size_t)len), "copied context mismatch");
}
int main(void)
{
    static const char *names[] = {
        "AES-128-ECB", "AES-128-CBC", "AES-128-CTR",
        "AES-192-ECB", "AES-192-CBC", "AES-192-CTR",
        "AES-256-ECB", "AES-256-CBC", "AES-256-CTR"
    };
    static const int sizes[] = {0,1,7,8,15,16,17,255,256,1023,1024,1025,4095,4096,4097,8192,16384,65536};
    static const int offsets[] = {0,5,15};
    unsigned char key[32], iv[16], data[LIMIT+32], answer[LIMIT+32];
    unsigned char actual[LIMIT+64], copied[LIMIT+64], inplace[LIMIT+64];
    EVP_CIPHER_CTX *c=EVP_CIPHER_CTX_new(), *dup=EVP_CIPHER_CTX_new();
    OSSL_PROVIDER *def=OSSL_PROVIDER_load(NULL,"default");
    uint64_t fp=UINT64_C(1469598103934665603); unsigned cases=0;
    ck(c && dup && def, "providers/contexts");
    for (size_t i=0; i<sizeof(key); ++i) key[i]=(unsigned char)(i*31+7);
    for (size_t i=0; i<sizeof(iv); ++i) iv[i]=(unsigned char)(i*13+19);
    for (size_t i=0; i<sizeof(data); ++i) data[i]=(unsigned char)(i*17+11);
    for (size_t a=0; a<sizeof(names)/sizeof(*names); ++a) {
        EVP_CIPHER *cipher=EVP_CIPHER_fetch(NULL,names[a],NULL); ck(cipher!=NULL,names[a]);
        int block=EVP_CIPHER_get_block_size(cipher);
        for (size_t s=0; s<sizeof(sizes)/sizeof(*sizes); ++s) for (size_t o=0; o<3; ++o) {
            int len=sizes[s]-sizes[s]%block, off=offsets[o];
            whole(c,cipher,key,iv,1,answer,data+off,len);
            for (int i=0;i<len;++i) { fp^=answer[i]; fp*=UINT64_C(1099511628211); }
            for (int enc=0;enc<2;++enc) {
                const unsigned char *input=enc?data+off:answer, *expected=enc?answer:data+off;
                memset(actual,0xa7,sizeof(actual)); memset(copied,0xa7,sizeof(copied));
                streamed(c,dup,cipher,key,iv,enc,actual+off,copied+off,input,len);
                ck(!memcmp(actual+off,expected,(size_t)len),"stream/reference mismatch");
                memset(inplace,0xa7,sizeof(inplace)); memcpy(inplace+off,input,(size_t)len);
                whole(c,cipher,key,iv,enc,inplace+off,inplace+off,len);
                ck(!memcmp(inplace+off,expected,(size_t)len),"in-place/reference mismatch");
                for (int i=0; i<off; ++i) ck(actual[i]==0xa7 && copied[i]==0xa7 && inplace[i]==0xa7,"prefix guard");
                for (size_t i=(size_t)(off+len); i<sizeof(actual); ++i)
                    ck(actual[i]==0xa7 && copied[i]==0xa7 && inplace[i]==0xa7,"suffix guard");
                ++cases;
            }
        }
        EVP_CIPHER_free(cipher);
    }
    /* Pin the independent host OpenSSL reference before native qualification. */
    ck(fp==UINT64_C(0x019ff5988780130c),"independent ciphertext fingerprint");
    printf("{\"cases\":%u,\"fingerprint\":\"%016llx\"}\n",cases,(unsigned long long)fp);
    EVP_CIPHER_CTX_free(c); EVP_CIPHER_CTX_free(dup);
    OSSL_PROVIDER_unload(def);
    return 0;
}
