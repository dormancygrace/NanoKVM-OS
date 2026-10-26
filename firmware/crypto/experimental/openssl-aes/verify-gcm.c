/* SPDX-License-Identifier: Apache-2.0 */
#define main baseline_benchmark_main
#include "../openssl-c906/evp-bench.c"
#undef main
/* Test provider state, not just the standalone DMA helper. A copied context
 * must remain valid after freeing and cleansing the original key schedule. */
static int copy_stream(const EVP_CIPHER *cipher,const unsigned char *key,
    const unsigned char *iv,int ivlen,const unsigned char *aad,int aadlen,
    const unsigned char *input,unsigned char *out,int len,int cut,int enc,
    unsigned char tag[16])
{
    EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new(),*copy=EVP_CIPHER_CTX_new();int n,total,last;
    ck(ctx&&copy,"GCM copy contexts");
    ck(EVP_CipherInit_ex2(ctx,cipher,NULL,NULL,enc,NULL),"GCM init");
    ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_SET_IVLEN,ivlen,NULL),"GCM IV length");
    ck(EVP_CipherInit_ex2(ctx,NULL,key,iv,enc,NULL),"GCM key/IV");
    if(!enc)ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_SET_TAG,16,tag),"GCM expected tag");
    ck(EVP_CipherUpdate(ctx,NULL,&n,aad,7),"GCM first AAD");
    ck(EVP_CipherUpdate(ctx,NULL,&n,aad+7,aadlen-7),"GCM remaining AAD");
    if(cut>len)cut=len;
    ck(EVP_CipherUpdate(ctx,out,&n,input,cut)&&n==cut,"GCM first input");total=n;
    ck(EVP_CIPHER_CTX_copy(copy,ctx),"GCM context copy");
    EVP_CIPHER_CTX_free(ctx);
    ck(EVP_CipherUpdate(copy,out+total,&n,input+cut,len-cut),"GCM copied update");total+=n;
    int ok=EVP_CipherFinal_ex(copy,out+total,&last);
    if(ok) {
        ck(total+last==len,"GCM output length");
        if(enc)ck(EVP_CIPHER_CTX_ctrl(copy,EVP_CTRL_AEAD_GET_TAG,16,tag),"GCM copied tag");
    }
    EVP_CIPHER_CTX_free(copy);return ok;
}
static void seed(const EVP_CIPHER *cipher,const unsigned char *key,
    const unsigned char *iv,int ivlen,const unsigned char *aad,int aadlen,
    const unsigned char *in,unsigned char *out,int len,unsigned char tag[16])
{
    EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();int n,last;
    ck(ctx&&EVP_EncryptInit_ex2(ctx,cipher,NULL,NULL,NULL),"seed init");
    ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_SET_IVLEN,ivlen,NULL),"seed IV length");
    ck(EVP_EncryptInit_ex2(ctx,NULL,key,iv,NULL),"seed key");
    ck(EVP_EncryptUpdate(ctx,NULL,&n,aad,aadlen),"seed AAD");
    ck(EVP_EncryptUpdate(ctx,out,&n,in,len)&&n==len,"seed input");
    ck(EVP_EncryptFinal_ex(ctx,out+n,&last)&&last==0,"seed final");
    ck(EVP_CIPHER_CTX_ctrl(ctx,EVP_CTRL_AEAD_GET_TAG,16,tag),"seed tag");
    EVP_CIPHER_CTX_free(ctx);
}
int main(void)
{
    static const int lengths[]={1025,2049,3073,4097,16384,16385};
    static const int cuts[]={1,1024,2048,3072,4096};
    unsigned char key[32],iv[16],aad[23],plain[16432],expected[16432],out[16432],tag[16],actual_tag[16];
    uint64_t fp=UINT64_C(1469598103934665603);unsigned cases=0;
    correctness(); /* Independent published known-answer vectors and bad tags. */
    for(unsigned i=0;i<sizeof key;i++)key[i]=(unsigned char)(i*7+3);
    for(unsigned i=0;i<sizeof iv;i++)iv[i]=(unsigned char)(0x13+i);
    for(unsigned i=0;i<sizeof aad;i++)aad[i]=(unsigned char)(i*19+5);
    for(unsigned i=0;i<sizeof plain;i++)plain[i]=(unsigned char)(i*13+11);
    for(int bits=128;bits<=256;bits+=64) {
        char name[32];snprintf(name,sizeof name,"AES-%d-GCM",bits);
        EVP_CIPHER *cipher=EVP_CIPHER_fetch(NULL,name,NULL);ck(cipher!=NULL,"GCM fetch");
        for(int ivlen=8;ivlen<=12;ivlen+=4)for(size_t l=0;l<sizeof lengths/sizeof *lengths;l++) {
            int len=lengths[l];seed(cipher,key,iv,ivlen,aad,sizeof aad,plain,expected,len,tag);
            for(int i=0;i<len;i++){fp^=expected[i];fp*=UINT64_C(1099511628211);}
            for(unsigned i=0;i<16;i++){fp^=tag[i];fp*=UINT64_C(1099511628211);}
            for(int off=0;off<=5;off+=5)for(size_t s=0;s<sizeof cuts/sizeof *cuts;s++) {
                for(int enc=0;enc<2;enc++)for(int inplace=0;inplace<2;inplace++) {
                    const unsigned char *in=enc?plain:expected,*want=enc?expected:plain;
                    memset(out,0xa5,sizeof out);memcpy(actual_tag,tag,16);
                    if(inplace)memcpy(out+off,in,(size_t)len);
                    ck(copy_stream(cipher,key,iv,ivlen,aad,sizeof aad,inplace?out+off:in,out+off,len,cuts[s],enc,actual_tag),"copied GCM final");
                    ck(!memcmp(out+off,want,(size_t)len)&&!memcmp(tag,actual_tag,16),"copied GCM reference");
                    for(int i=0;i<off;i++)ck(out[i]==0xa5,"GCM prefix guard");
                    for(size_t i=(size_t)(off+len);i<sizeof out;i++)ck(out[i]==0xa5,"GCM suffix guard");
                    cases++;
                }
                memcpy(actual_tag,tag,16);actual_tag[3]^=1;
                ck(!copy_stream(cipher,key,iv,ivlen,aad,sizeof aad,expected,out,len,cuts[s],0,actual_tag),"GCM bad tag accepted");
                memcpy(out,expected,(size_t)len);out[len/2]^=1;memcpy(actual_tag,tag,16);
                ck(!copy_stream(cipher,key,iv,ivlen,aad,sizeof aad,out,out,len,cuts[s],0,actual_tag),"GCM bad ciphertext accepted");
                cases+=2;
            }
        }
        EVP_CIPHER_free(cipher);
    }
    ck(fp==UINT64_C(0xa2d7088437b53472),"independent GCM ciphertext/tag fingerprint");
    printf("PASS %u GCM split/copy/in-place/tag cases fingerprint=%016llx\n",cases,(unsigned long long)fp);
    return 0;
}
