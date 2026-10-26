/* SPDX-License-Identifier: Apache-2.0 */
#define _GNU_SOURCE
#include <openssl/evp.h>
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include "sg2002_crypto.h"
static void ck(int ok,const char *why){if(!ok){fprintf(stderr,"FAIL %s errno=%d\n",why,errno);exit(2);}}
static void init(unsigned char *p){const uint32_t h[]={0x7380166f,0x4914b2b9,0x172442d7,0xda8a0600,0xa96f30bc,0x163138aa,0xe38dee4d,0xb0fb0e4e};for(unsigned i=0;i<8;i++)for(unsigned j=0;j<4;j++)p[4*i+j]=(unsigned char)(h[i]>>(24-8*j));}
int main(void)
{
 /* Keep the reference in software even with the candidate libcrypto loaded. */
 ck(setenv("NANOKVM_OPENSSL_DMA","off",1)==0,"reference switch");
 int fd=open("/dev/sg2002-aes-probe",O_RDWR|O_CLOEXEC);ck(fd>=0,"open");
 struct sg2002_crypto_info2 before,after;ck(ioctl(fd,SG2002_CRYPTO_INFO2,&before)==0,"INFO2");
 ck(!before.legacy.poisoned&&(before.legacy.algorithms&(1u<<SG2002_ALG_SM3)),"SM3 capability");
 unsigned char buf[65728],expected[32];unsigned cases=0;uint64_t submits=0;
 const size_t sizes[]={0,1,3,55,56,63,64,65,1023,1024,4095,4096,4097,16384,65536};
 for(size_t s=0;s<sizeof(sizes)/sizeof(*sizes);s++)for(unsigned off=0;off<16;off+=5)for(unsigned split=0;split<2;split++){
  size_t n=sizes[s],padded=(n+9+63)&~(size_t)63;unsigned char *data=buf+off;unsigned digest_len;
  memset(buf,0xa7,sizeof(buf));for(size_t j=0;j<n;j++)data[j]=(unsigned char)(j*17+11);
  if(n==3)memcpy(data,"abc",3);
  ck(EVP_Digest(data,n,expected,&digest_len,EVP_sm3(),NULL)==1&&digest_len==32,"software reference");
  memset(data+n,0,padded-n);data[n]=0x80;uint64_t bits=(uint64_t)n*8;
  for(unsigned j=0;j<8;j++)data[padded-1-j]=(unsigned char)(bits>>(8*j));
  struct sg2002_crypto_request req={.version=1,.algorithm=SG2002_ALG_SM3};init(req.state);
  for(size_t pos=0;pos<padded;){size_t chunk=split?64:4096;if(chunk>padded-pos)chunk=padded-pos;
   req.input=(uintptr_t)(data+pos);req.length=(uint32_t)chunk;
   ck(ioctl(fd,SG2002_CRYPTO_RUN,&req)==0,"SM3 RUN");ck(req.status==4&&req.output_length==0,"completion");submits++;pos+=chunk;
  }
  if(memcmp(req.state,expected,32)){
   fprintf(stderr,"SM3 mismatch len=%zu offset=%u split=%u got=",n,off,split);
   for(unsigned j=0;j<32;j++)fprintf(stderr,"%02x",req.state[j]);
   fputs(" expected=",stderr);
   for(unsigned j=0;j<32;j++)fprintf(stderr,"%02x",expected[j]);
   fputc('\n',stderr);return 3;
  }
  for(unsigned j=0;j<off;j++)ck(buf[j]==0xa7,"input prefix");
  for(size_t j=off+padded;j<sizeof(buf);j++)ck(buf[j]==0xa7,"input suffix");
  cases++;
 }
 ck(ioctl(fd,SG2002_CRYPTO_INFO2,&after)==0&&!after.legacy.poisoned,"completion info");
 ck(after.sm3_submitted-before.sm3_submitted==submits&&after.sm3_completed-before.sm3_completed==submits,"SM3 counter proof");
 close(fd);printf("{\"cases\":%u,\"sm3_dma_submits\":%llu}\n",cases,(unsigned long long)submits);return 0;
}
