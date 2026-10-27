// SPDX-License-Identifier: GPL-2.0-only
#include <linux/module.h>
#include <linux/vmalloc.h>
#include <linux/sched.h>
#include <linux/string.h>
#include "memory_test.h"

/* Resolve to the real running kernel implementation, including its return. */
extern void *__memmove(void *, const void *, size_t);
typedef void *(*move_fn)(void *, const void *, size_t);
static const move_fn moves[] = { __memmove };
static u8 *area, *other;
static u64 checks;
static unsigned seed;
module_param(seed, uint, 0400);

static int check_move(unsigned v,unsigned src,unsigned dst,unsigned n)
{
 unsigned total=max(src,dst)+n+16,i;
 void *ret;
 for(i=0;i<total;i++)area[i]=sm_pattern(i+seed);
 ret=moves[v](area+dst,area+src,n);
 if(ret!=area+dst)goto bad;
 for(i=0;i<total;i++) {
  unsigned orig=(i>=dst && i<dst+n) ? src+i-dst : i;
  if(area[i]!=sm_pattern(orig+seed))goto bad;
 }
 checks++;
 return 0;
bad:
 pr_err("sm move failed v=%u src=%u dst=%u n=%u check=%llu\n",v,src,dst,n,checks);
 return -EINVAL;
}
static int validate_moves(void)
{
 unsigned v,n,s,d,j,k;
 const unsigned lens[]={129,130,143,255,256,257,511,512,513,1023,1024,1025,1500,4095,4096,4097,8191,8192,8193,16383,16384};
 const unsigned olens[]={0,1,2,3,4,7,8,9,15,16,17,31,32,33,63,64,65,71,72,127,128,129,255,256,257,511,512,513,4095,4096,4097};
 const int distances[]={0,1,-1,2,-2,3,-3,4,-4,5,-5,6,-6,7,-7,8,-8,9,-9,15,-15,16,-16,17,-17,31,-31,32,-32,33,-33,63,-63,64,-64,65,-65,127,-127,128,-128,129,-129};
 for(v=0;v<ARRAY_SIZE(moves);v++) {
  for(j=0;j<130+ARRAY_SIZE(lens);j++) {
   n=j<130 ? j : lens[j-130];
   for(s=0;s<8;s++)for(d=0;d<8;d++) {
    if(check_move(v,16+s,n+64+d,n))return -EINVAL;
    if(check_move(v,n+64+s,16+d,n))return -EINVAL;
   }
   cond_resched();
  }
  for(j=0;j<ARRAY_SIZE(olens);j++)for(s=0;s<8;s++) {
   n=olens[j];
   for(k=0;k<ARRAY_SIZE(distances);k++)
    if(check_move(v,160+s,160+s+distances[k],n))return -EINVAL;
   cond_resched();
  }
  /* Both address orders: guard source loads and destination stores at the
   * unmapped end, then overlap both ways with the upper range at the guard. */
  for(k=0;k<2;k++)for(n=0;n<=256;n++)for(d=0;d<8;d++) {
   u8 *src=k ? other : area, *dst=k ? area : other;
   unsigned i;
   for(i=0;i<65536;i++) { src[i]=sm_pattern(i+seed);dst[i]=0xa5; }
   if(moves[v](dst+16+d,src+65536-n,n)!=dst+16+d ||
      memcmp(dst+16+d,src+65536-n,n))return -EINVAL;
   if(dst[15+d]!=0xa5 || dst[16+d+n]!=0xa5)return -EINVAL;
   checks++;
   memset(dst+65536-n-16,0xa5,n+16);
   if(moves[v](dst+65536-n,src+16+d,n)!=dst+65536-n ||
      memcmp(dst+65536-n,src+16+d,n))return -EINVAL;
   if(dst[65536-n-1]!=0xa5)return -EINVAL;
   checks++;
   if(!(d&3))cond_resched();
  }
  for(k=0;k<2;k++)for(n=0;n<=256;n++)for(d=1;d<=8;d++)for(j=0;j<2;j++) {
   u8 *buf=k ? other : area;
   unsigned src=65536-n-(j ? d : 0), dst=65536-n-(j ? 0 : d), i;
   for(i=0;i<65536;i++)buf[i]=sm_pattern(i+seed);
   if(moves[v](buf+dst,buf+src,n)!=buf+dst)return -EINVAL;
   for(i=0;i<65536;i++) {
    unsigned orig=(i>=dst && i<dst+n) ? src+i-dst : i;
    if(buf[i]!=sm_pattern(orig+seed))return -EINVAL;
   }
   checks++;
   cond_resched();
  }
 }
 return 0;
}


static int __init memory_start(void)
{
	int err;

	area = vmalloc(65536);
	other = vmalloc(65536);
	if (!area || !other) {
		err = -ENOMEM;
		goto out;
	}
	err = validate_moves();
	if (!err)
		pr_info("c906_memory_test: PASS memmove checks=%llu seed=%u\n",
			checks, seed);
	else
		pr_err("c906_memory_test: FAIL after %llu checks seed=%u\n",
			checks, seed);
out:
	vfree(area);
	vfree(other);
	return err;
}

static void __exit memory_stop(void) { }
module_init(memory_start);
module_exit(memory_stop);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Validate live C906 kernel memmove overlap and page boundaries");
