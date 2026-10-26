/* SPDX-License-Identifier: GPL-2.0-only */
#include <assert.h>
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <errno.h>
#include "sg2002_crypto.h"
typedef uint32_t u32;
typedef uint8_t u8;
typedef uint32_t __le32;
#define BIT(n) (1u<<(n))
#define ALIGN(x,n) (((x)+(n)-1)&~((n)-1))
#define DIV_ROUND_UP(x,n) (((x)+(n)-1)/(n))
#define lower_32_bits(x) ((u32)(x))
#define upper_32_bits(x) ((u32)((uint64_t)(x)>>32))
#define cpu_to_le32(x) (x)
#define DESC_BYTES 128
#define STATUS 0x10
#define MASK 4
#define DMA_BIDIRECTIONAL 0
#define THIS_MODULE 0
static unsigned char buffer[4096],crypto_output[8192],regs[1024];
static uint32_t desc[32];
static uint64_t buffer_dma=0x10200000,crypto_output_dma=0x20400000,desc_dma=0x30600000;
static uint64_t crypto_completed[SG2002_ALG_COUNT];
static bool poisoned;
static unsigned initial_mask=7,pins;
static struct {int dev;} device,*pdev=&device;
static int wait_error;static u32 wait_status;
struct crypto_sizes {u32 input,output,result;};
static void *memchr_inv(const void *v,int c,size_t n){const unsigned char *p=v;for(size_t i=0;i<n;i++)if(p[i]!=(unsigned char)c)return (void *)(p+i);return NULL;}
static uint32_t get_unaligned_be32(const void *v){const unsigned char *p=v;return (uint32_t)p[0]<<24|(uint32_t)p[1]<<16|(uint32_t)p[2]<<8|p[3];}
static void put_unaligned_be32(uint32_t x,void *v){unsigned char *p=v;for(unsigned j=0;j<4;j++)p[j]=(unsigned char)(x>>(24-8*j));}
static u32 readl(const void *p){u32 v;memcpy(&v,p,4);return v;}
static void writel(u32 v,void *p){memcpy(p,&v,4);}
static int engine_wait(u32 *status){*status=wait_status;return wait_error;}
static void __module_get(int module){(void)module;pins++;}
#define pr_err(...) ((void)0)
#define dma_rmb() ((void)0)
static void dma_sync_single_for_cpu(void *dev,uint64_t dma,size_t len,int dir){(void)dev;(void)dma;(void)len;(void)dir;}
static void memzero_explicit(void *p,size_t n){memset(p,0,n);}
#include "driver.inc"
_Static_assert(sizeof(struct sg2002_crypto_request)==144,"RUN ABI changed");
_Static_assert(sizeof(struct sg2002_crypto_info)==160,"INFO ABI changed");
_Static_assert(sizeof(struct sg2002_crypto_info2)==176,"INFO2 ABI");
_Static_assert(SG2002_CRYPTO_RUN==0xc0904bc2UL,"RUN ioctl changed");
_Static_assert(SG2002_CRYPTO_INFO==0x80a04bc3UL,"INFO ioctl changed");
_Static_assert(offsetof(struct sg2002_crypto_info,completed)==96,"legacy counters moved");
int main(void)
{
 struct sg2002_crypto_request r={.version=1,.algorithm=SG2002_ALG_SM3,.length=64,.input=1};
 const u32 iv[8]={0x7380166f,0x4914b2b9,0x172442d7,0xda8a0600,0xa96f30bc,0x163138aa,0xe38dee4d,0xb0fb0e4e};
 for(unsigned i=0;i<8;i++)put_unaligned_be32(iv[i],r.state+4*i);
 assert(!crypto_validate(&r));
 struct crypto_sizes s;assert(!crypto_prepare_descriptor(&r,&s.input,&s.output,&s.result));
 assert(desc[0]==(BIT(19)|BIT(23)|BIT(14)|15)&&desc[1]==1);
 assert(desc[4]==buffer_dma&&desc[6]==crypto_output_dma&&desc[4]!=desc[6]);
 assert(s.input==64&&s.output==64&&s.result==0&&desc[8]==64);
 for(unsigned i=0;i<8;i++)assert(desc[10+i]==iv[i]);
 for(unsigned i=0;i<8;i++)writel(iv[i]^0x12345678,regs+0x1e0+4*i);
 wait_status=4;assert(!crypto_finish_locked(&r,&s));assert(r.status==4&&r.output_length==0&&!poisoned);
 for(unsigned i=0;i<8;i++)assert(get_unaligned_be32(r.state+4*i)==(iv[i]^0x12345678));
 assert(crypto_completed[SG2002_ALG_SM3]==1);
 memset(buffer,0x77,64);memset(crypto_output,0x77,64);crypto_clean_locked(&r);
 assert(!memchr_inv(buffer,0,64)&&!memchr_inv(crypto_output,0,64)&&!memchr_inv(desc,0,DESC_BYTES));
 for(unsigned status=0;status<8;status++)if(status!=4){
  unsigned before=pins;poisoned=false;wait_status=status;assert(crypto_finish_locked(&r,&s)==-EIO&&poisoned&&pins==before+1);
 }
 poisoned=false;wait_status=4;wait_error=-ETIMEDOUT;assert(crypto_finish_locked(&r,&s)==-ETIMEDOUT&&poisoned);
 memset(buffer,0x77,64);crypto_clean_locked(&r);assert(buffer[0]==0x77);
 for(unsigned n=0;n<4098;n++){
  r.length=n;int valid=n!=0&&n<=4096&&n%64==0;assert((crypto_validate(&r)==0)==valid);
 }
 r.length=64;r.encrypt=1;assert(crypto_validate(&r)==-EINVAL);r.encrypt=0;
 r.key_length=1;assert(crypto_validate(&r)==-EINVAL);r.key_length=0;
 r.output_length=1;assert(crypto_validate(&r)==-EINVAL);r.output_length=0;
 r.mode=1;assert(crypto_validate(&r)==-EINVAL);r.mode=0;
 r.reserved[3]=1;assert(crypto_validate(&r)==-EINVAL);r.reserved[3]=0;
 /* SHA256 remains byte-for-byte compatible with the prior descriptor. */
 r.algorithm=SG2002_ALG_SHA256;for(unsigned i=0;i<8;i++)put_unaligned_be32(iv[i],r.state+4*i);
 assert(!crypto_prepare_descriptor(&r,&s.input,&s.output,&s.result));
 assert(desc[0]==(BIT(19)|BIT(12)|15)&&desc[1]==3&&s.output==0&&s.result==0);
 assert(!memcmp(&desc[10],r.state,32));
 puts("PASS SM3 descriptor/endian/status/poison and legacy ioctl ABI; 4098 length cases");
}
