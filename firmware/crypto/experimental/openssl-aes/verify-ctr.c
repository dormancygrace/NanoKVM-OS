/* SPDX-License-Identifier: Apache-2.0 */
#define _POSIX_C_SOURCE 200809L
#include <openssl/evp.h>
#include <openssl/err.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef __linux__
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include "hwcrypto.h"
#endif
static void check(int ok, const char *what)
{
    if (!ok) { fprintf(stderr,"FAIL %s\n",what); ERR_print_errors_fp(stderr); exit(1); }
}
static void reference(const EVP_CIPHER *ecb, const unsigned char *key,
    const unsigned char initial[16], const unsigned char *in, unsigned char *out,
    size_t len, unsigned int width)
{
    EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();
    unsigned char counter[16], stream[16]; int n;
    check(ctx!=NULL,"reference context"); memcpy(counter,initial,16);
    check(EVP_EncryptInit_ex2(ctx,ecb,key,NULL,NULL),"reference key");
    check(EVP_CIPHER_CTX_set_padding(ctx,0),"reference padding");
    for (size_t pos=0;pos<len;pos+=16) {
        check(EVP_EncryptUpdate(ctx,stream,&n,counter,16)&&n==16,"reference ECB");
        for(size_t i=0;i<16&&pos+i<len;i++) out[pos+i]=in[pos+i]^stream[i];
        for(unsigned int i=16;i>16-width;i--) if(++counter[i-1]) break;
    }
    EVP_CIPHER_CTX_free(ctx);
}
static void provider(const EVP_CIPHER *ctr, const unsigned char *key,
    const unsigned char *iv,const unsigned char *in,unsigned char *out,
    size_t len,int split,int enc)
{
    EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();int n,total=0,last;
    const size_t steps[]={1,2047,3073,4095};size_t pos=0,step=0;
    check(ctx&&EVP_CipherInit_ex2(ctx,ctr,key,iv,enc,NULL),"provider init");
    while(pos<len) {
        size_t count=split?steps[step++%4]:len;
        if(count>len-pos)count=len-pos;
        check(EVP_CipherUpdate(ctx,out+total,&n,in+pos,(int)count),"provider update");
        total+=n;pos+=count;
    }
    check(EVP_CipherFinal_ex(ctx,out+total,&last)&&total+last==(int)len,"provider final");
    EVP_CIPHER_CTX_free(ctx);
}
int main(int argc,char **argv)
{
    static const size_t lengths[]={64,2048,4096,8193};
    unsigned char key[32],iv[16],in[8224],want[8224],got[8224],c32[4096],c64[4096];
    unsigned int cases=0;int raw_only=argc==2&&!strcmp(argv[1],"--raw-only");
    int native=raw_only||(argc==2&&!strcmp(argv[1],"--native"));
    check(argc==1||native,"argument");
    for(unsigned i=0;i<sizeof key;i++)key[i]=(unsigned char)(i*7+3);
    for(unsigned i=0;i<sizeof in;i++)in[i]=(unsigned char)(i*13+11);
#ifdef __linux__
    int fd=native?open("/dev/nanokvm-hwcrypto",O_RDWR|O_CLOEXEC):-1;
    if(native)check(fd>=0,"native device");
#else
    check(!native,"native requires Linux");
#endif
    for(unsigned int bits=128;bits<=256;bits+=64) {
        char name[32];snprintf(name,sizeof name,"AES-%u-ECB",bits);
        EVP_CIPHER *ecb=EVP_CIPHER_fetch(NULL,name,NULL);
        snprintf(name,sizeof name,"AES-%u-CTR",bits);
        EVP_CIPHER *ctr=EVP_CIPHER_fetch(NULL,name,NULL);check(ecb&&ctr,"cipher fetch");
        for(unsigned int carry=0;carry<3;carry++) {
            for(unsigned i=0;i<16;i++)iv[i]=(unsigned char)(0x13+i);
            memset(iv+(carry==0?12:carry==1?8:0),0xff,carry==0?4:carry==1?8:16);
            iv[15]=0xfd; /* 32-bit carry, 64-bit carry, then testmgr's ff..fd. */
#ifdef __linux__
            if(native) {
                struct hwcrypto_request req={.version=HWCRYPTO_ABI,
                    .algorithm=HWCRYPTO_ALG_AES,.mode=HWCRYPTO_MODE_CTR,.encrypt=1,
                    .key_length=bits/8,.length=4096,.output_length=4096,
                    .input=(uintptr_t)in,.output=(uintptr_t)got};
                memcpy(req.key,key,bits/8);memcpy(req.iv,iv,16);
                reference(ecb,key,iv,in,want,4096,16);
                reference(ecb,key,iv,in,c64,4096,8);
                reference(ecb,key,iv,in,c32,4096,4);
                check(ioctl(fd,HWCRYPTO_RUN,&req)==0&&req.status==1&&req.output_length==4096,"raw DMA");
                int full=!memcmp(got,want,4096),low64=!memcmp(got,c64,4096),low32=!memcmp(got,c32,4096);
                check(full||low64||low32,"unrecognized hardware counter behavior");
                printf("RAW AES-%u carry=%u matches128=%d matches64=%d matches32=%d\n",bits,carry,full,low64,low32);
                fflush(stdout);
            }
#endif
            if(raw_only)continue;
            for(size_t l=0;l<sizeof lengths/sizeof *lengths;l++)for(int split=0;split<2;split++)for(int enc=0;enc<2;enc++)for(int inplace=0;inplace<2;inplace++) {
                size_t len=lengths[l];reference(ecb,key,iv,in,want,len,16);
                const unsigned char *src=enc?in:want,*expected=enc?want:in;
                memset(got,0xa5,sizeof got);
                if(inplace)memcpy(got+5,src,len);
                provider(ctr,key,iv,inplace?got+5:src,got+5,len,split,enc);
                check(!memcmp(got+5,expected,len),"full-width CTR carry");
                for(unsigned i=0;i<5;i++)check(got[i]==0xa5,"prefix guard");
                for(size_t i=5+len;i<sizeof got;i++)check(got[i]==0xa5,"suffix guard");
                cases++;
            }
        }
        EVP_CIPHER_free(ecb);EVP_CIPHER_free(ctr);
    }
#ifdef __linux__
    if(fd>=0)close(fd);
#endif
    printf("PASS %u EVP CTR carry cases\n",cases);
    return 0;
}
