/* SPDX-License-Identifier: Apache-2.0 */
#define OPENSSL_SUPPRESS_DEPRECATED
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "sha256_sg2002.c"
static int fail_after, calls, opened, closed;
int ossl_sg2002_begin(unsigned int alg) {assert(alg==6);opened++;return fail_after==-2?-1:37;}
void ossl_sg2002_end(int fd) {assert(fd==37);closed++;}
int ossl_sg2002_run(int fd,struct sg2002_crypto_request *r)
{
    assert(fd==37&&r->algorithm==6&&r->length%64==0&&r->length>=512&&r->length<=4096);
    if(calls++==fail_after)return 0;
    SHA256_CTX temp;memset(&temp,0,sizeof temp);
    for(int i=0;i<8;i++)temp.h[i]=(uint32_t)r->state[4*i]<<24|(uint32_t)r->state[4*i+1]<<16|(uint32_t)r->state[4*i+2]<<8|r->state[4*i+3];
    for(size_t n=0;n<r->length;n+=64)SHA256_Transform(&temp,(const unsigned char*)(uintptr_t)r->input+n);
    for(int i=0;i<8;i++)for(int k=0;k<4;k++)r->state[4*i+k]=(unsigned char)(temp.h[i]>>(24-8*k));
    return 1;
}
int main(void)
{
    unsigned char data[65600];for(size_t i=0;i<sizeof data;i++)data[i]=(unsigned char)(i*29+17);
    const size_t blocks[]={0,1,63,64,65,71,72,73,127,128,129,135,136,200,256,1024};
    int count=0;
    for(int sha224=0;sha224<=1;sha224++)for(int off=0;off<16;off++)
    for(size_t z=0;z<sizeof blocks/sizeof blocks[0];z++)for(fail_after=-2;fail_after<=3;fail_after++) {
        SHA256_CTX got,want;memset(&got,0,sizeof got);
        if(sha224)SHA224_Init(&got);else SHA256_Init(&got);
        got.Nl=0x12345678;got.Nh=7;got.num=13;want=got;calls=opened=closed=0;
        size_t done=ossl_sha256_sg2002(&got,data+off,blocks[z]);
        assert(done<=blocks[z]&&(done==0||done>=64));
        if(fail_after==-1&&blocks[z]>=64)assert(blocks[z]-done<8);
        if(blocks[z]<64)assert(!opened&&!calls&&!closed);
        else if(fail_after==-2)assert(opened==1&&!calls&&!closed);
        else assert(opened==1&&closed==1);
        for(size_t n=done;n<blocks[z];n++)SHA256_Transform(&got,data+off+64*n);
        for(size_t n=0;n<blocks[z];n++)SHA256_Transform(&want,data+off+64*n);
        assert(!memcmp(&got,&want,sizeof got));count++;
    }
    printf("PASS %d SHA-224/256 compression fallback cases: first/later failures, all offsets, unchanged buffering state\n",count);
}
