/* SPDX-License-Identifier: Apache-2.0 */
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <openssl/crypto.h>
#include "internal/sm3.h"
#include "crypto/sg2002.h"
static uint32_t rol(uint32_t x,unsigned n){return (x<<n)|(x>>(32-n));}
static void ref(SM3_CTX *c,const unsigned char *p,size_t blocks)
{
 uint32_t h[8]={c->A,c->B,c->C,c->D,c->E,c->F,c->G,c->H};
 while(blocks--){
  uint32_t w[68],a=h[0],b=h[1],cc=h[2],d=h[3],e=h[4],f=h[5],g=h[6],hh=h[7];
  for(unsigned j=0;j<16;j++)w[j]=(uint32_t)p[j*4]<<24|(uint32_t)p[j*4+1]<<16|(uint32_t)p[j*4+2]<<8|p[j*4+3];
  for(unsigned j=16;j<68;j++){
   uint32_t x=w[j-16]^w[j-9]^rol(w[j-3],15);
   w[j]=x^rol(x,15)^rol(x,23)^rol(w[j-13],7)^w[j-6];
  }
  for(unsigned j=0;j<64;j++){
   uint32_t tj=j<16?0x79cc4519:0x7a879d8a;
   unsigned k=j&31; if(k)tj=rol(tj,k);
   uint32_t ar=rol(a,12),s1=rol(ar+e+tj,7),s2=s1^ar;
   uint32_t ff=j<16?a^b^cc:(a&b)|(a&cc)|(b&cc);
   uint32_t gg=j<16?e^f^g:(e&f)|(~e&g);
   uint32_t t1=ff+d+s2+(w[j]^w[j+4]),t2=gg+hh+s1+w[j];
   d=cc;cc=rol(b,9);b=a;a=t1;hh=g;g=rol(f,19);f=e;e=t2^rol(t2,9)^rol(t2,17);
  }
  h[0]^=a;h[1]^=b;h[2]^=cc;h[3]^=d;h[4]^=e;h[5]^=f;h[6]^=g;h[7]^=hh;p+=64;
 }
 c->A=h[0];c->B=h[1];c->C=h[2];c->D=h[3];c->E=h[4];c->F=h[5];c->G=h[6];c->H=h[7];
}
static int absent,fail_at,calls,closed;
int ossl_sg2002_begin(unsigned algorithm){assert(algorithm==SG2002_ALG_SM3);return absent?-1:17;}
void ossl_sg2002_end(int fd){assert(fd==17);closed++;}
int ossl_sg2002_run(int fd,struct sg2002_crypto_request *r){
 assert(fd==17&&r->algorithm==SG2002_ALG_SM3&&r->length==4096&&r->output_length==0);
 if(++calls==fail_at)return 0;
 uint32_t h[8];for(unsigned i=0;i<8;i++)h[i]=(uint32_t)r->state[4*i]<<24|(uint32_t)r->state[4*i+1]<<16|(uint32_t)r->state[4*i+2]<<8|r->state[4*i+3];
 SM3_CTX c={.A=h[0],.B=h[1],.C=h[2],.D=h[3],.E=h[4],.F=h[5],.G=h[6],.H=h[7]};
 ref(&c,(const unsigned char *)(uintptr_t)r->input,r->length/64);
 h[0]=c.A;h[1]=c.B;h[2]=c.C;h[3]=c.D;h[4]=c.E;h[5]=c.F;h[6]=c.G;h[7]=c.H;
 for(unsigned i=0;i<8;i++)for(unsigned j=0;j<4;j++)r->state[4*i+j]=(unsigned char)(h[i]>>(24-8*j));
 r->status=4;return 1;
}
#include "sm3_sg2002.c"
static SM3_CTX initial(void){SM3_CTX c;memset(&c,0x5c,sizeof(c));c.A=0x7380166f;c.B=0x4914b2b9;c.C=0x172442d7;c.D=0xda8a0600;c.E=0xa96f30bc;c.F=0x163138aa;c.G=0xe38dee4d;c.H=0xb0fb0e4e;return c;}
int main(void){
 unsigned char data[65568];for(size_t i=0;i<sizeof(data);i++)data[i]=(unsigned char)(i*13+9);
 /* Published SM3 abc known answer validates the independent compression core. */
 unsigned char abc[64]={0x61,0x62,0x63,0x80};abc[63]=24;SM3_CTX kat=initial();ref(&kat,abc,1);
 assert(kat.A==0x66c7f0f4&&kat.B==0x62eeedd9&&kat.C==0xd1f2d46b&&kat.D==0xdc10e4e2&&kat.E==0x4167c487&&kat.F==0x5cf2f7a2&&kat.G==0x297da02b&&kat.H==0x8f4ba8e0);
 const size_t counts[]={0,1,63,64,65,127,128,129,256,1024};unsigned cases=0;
 for(unsigned off=0;off<16;off++)for(unsigned n=0;n<10;n++)for(int mode=0;mode<6;mode++){
  SM3_CTX got=initial(),want=got;size_t count=counts[n];ref(&want,data+off,count);
  absent=mode==1;fail_at=mode>=2?mode-1:0;calls=closed=0;
  size_t done=ossl_sm3_sg2002(&got,data+off,count);
  size_t expected=absent?0:count/64*64;
  if(fail_at&&expected>=(size_t)fail_at*64)expected=(size_t)(fail_at-1)*64;
  assert(done==expected&&closed==(count>=64&&!absent));
  ref(&got,data+off+done*64,count-done);assert(!memcmp(&got,&want,sizeof(got)));cases++;
 }
 printf("PASS %u SM3 prefix/fallback cases and independent abc vector\n",cases);
}
