/* SPDX-License-Identifier: Apache-2.0 */
#include <openssl/evp.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
static void ck(int ok,const char *s){if(!ok){fprintf(stderr,"FAIL %s\n",s);exit(2);}}
int main(void)
{
    const char *algs[]={"SM3"};
    const int sizes[]={0,1,55,56,63,64,65,255,1023,1024,4095,4096,4097,8191,8192,8193,16384,65536};
    unsigned char data[65568],whole[64],split[64];uint64_t fp=1469598103934665603ULL;
    for(size_t i=0;i<sizeof data;i++)data[i]=(unsigned char)(i*17+11);
    EVP_MD_CTX *ctx=EVP_MD_CTX_new(),*dup=EVP_MD_CTX_new();ck(ctx&&dup,"contexts");
    unsigned cases=0;
    for(size_t a=0;a<1;a++){
        EVP_MD *md=EVP_MD_fetch(NULL,algs[a],NULL);ck(md!=NULL,"fetch");
        for(size_t s=0;s<sizeof sizes/sizeof sizes[0];s++)for(int off=0;off<16;off++){
            unsigned n,m;int len=sizes[s],prefix=(int)(s*313+off)%8193;if(prefix>len)prefix=len;
            ck(EVP_DigestInit_ex2(ctx,md,NULL)==1&&EVP_DigestUpdate(ctx,data+off,len)==1&&EVP_DigestFinal_ex(ctx,whole,&n)==1,"whole digest");
            ck(EVP_DigestInit_ex2(ctx,md,NULL)==1&&EVP_DigestUpdate(ctx,data+off,prefix)==1,"prefix");
            ck(EVP_MD_CTX_copy_ex(dup,ctx)==1,"copy context");
            int pos=prefix;
            while(pos<len){int chunk=(pos%3)?4097:64;if(chunk>len-pos)chunk=len-pos;ck(EVP_DigestUpdate(dup,data+off+pos,chunk)==1,"continuation");pos+=chunk;}
            ck(EVP_DigestFinal_ex(dup,split,&m)==1&&m==n&&!memcmp(split,whole,n),"stream/context-copy mismatch");
            for(unsigned i=0;i<n;i++){fp^=whole[i];fp*=1099511628211ULL;}cases++;
        }
        EVP_MD_free(md);
    }
    ck(fp==UINT64_C(0x1aa2e10b5fb3fe70),"independent SM3 known answers");
    EVP_MD_CTX_free(ctx);EVP_MD_CTX_free(dup);
    printf("{\"cases\":%u,\"fingerprint\":\"%016llx\"}\n",cases,(unsigned long long)fp);
    return 0;
}
