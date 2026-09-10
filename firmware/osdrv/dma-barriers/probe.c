
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <errno.h>
typedef int CVI_S32;
typedef uint64_t CVI_U64;
typedef uint32_t CVI_U32;
enum enum_cache_op { enum_cache_op_invalid, enum_cache_op_flush };
enum { DMA_TO_DEVICE=1, DMA_FROM_DEVICE=2 };
typedef uint64_t __u64;
typedef int32_t __s32;
struct sys_cache_op {
	void *addr_v;
	__u64 addr_p;
	__u64 size;
	__s32 dma_fd;
};
#define EXPORT_SYMBOL_GPL(x)
#define __user
#define pr_err(...) ((void)0)
static int event, barriers, copy_fails;
static uint64_t seen_addr;
static size_t seen_size;
static void cvi_ion_sync_for_cpu(uint64_t a,size_t n,int d) {
 assert(!event && d==DMA_FROM_DEVICE); event=1; seen_addr=a; seen_size=n;
}
static void cvi_ion_sync_for_device(uint64_t a,size_t n,int d) {
 assert(!event && d==DMA_TO_DEVICE); event=2; seen_addr=a; seen_size=n;
}
static unsigned long copy_from_user(void *d,const void *s,unsigned long n) {
 if(copy_fails) return n;
 memcpy(d,s,n); return 0;
}
static void actual_mb(void) {
 assert(event);
#ifdef __riscv
 __asm__ __volatile__("fence iorw,iorw" ::: "memory");
#else
 __sync_synchronize();
#endif
 ++barriers;
}
#define mb() actual_mb()
CVI_S32 sys_cache_invalidate(CVI_U64 addr_p, void *addr_v, CVI_U32 u32Len)
{
	cvi_ion_sync_for_cpu(addr_p, u32Len, DMA_FROM_DEVICE);

	/* Order the DMA handoff against memory and MMIO even on UP builds. */
	mb();
	return 0;
}
EXPORT_SYMBOL_GPL(sys_cache_invalidate);

CVI_S32 sys_cache_flush(CVI_U64 addr_p, void *addr_v, CVI_U32 u32Len)
{
	cvi_ion_sync_for_device(addr_p, u32Len, DMA_TO_DEVICE);

	/* Order the DMA handoff against memory and MMIO even on UP builds. */
	mb();
	return 0;
}
EXPORT_SYMBOL_GPL(sys_cache_flush);

static CVI_S32 sys_cache_op_userv(unsigned long arg, enum enum_cache_op op_code)
{
	int ret = 0;
	struct sys_cache_op ioctl_arg;

	ret = copy_from_user(&ioctl_arg, (struct sys_cache_op __user *)arg, sizeof(struct sys_cache_op));
	if (ret) {
		pr_err("copy_from_user failed, sys_cache_op_userv\n");
		return -EFAULT;
	}

	if (op_code == enum_cache_op_invalid)
		cvi_ion_sync_for_cpu(ioctl_arg.addr_p, ioctl_arg.size, DMA_FROM_DEVICE);
	else if (op_code == enum_cache_op_flush)
		cvi_ion_sync_for_device(ioctl_arg.addr_p, ioctl_arg.size, DMA_TO_DEVICE);

	/* Order the DMA handoff against memory and MMIO even on UP builds. */
	mb();
	return 0;
}

static void reset(void) {event=barriers=copy_fails=0;seen_addr=seen_size=0;}
int main(void) {
 reset(); assert(sys_cache_invalidate(0x81234000,0,4096)==0);
 assert(event==1 && barriers==1 && seen_addr==0x81234000 && seen_size==4096);
 reset(); assert(sys_cache_flush(0x81235000,0,8192)==0);
 assert(event==2 && barriers==1 && seen_addr==0x81235000 && seen_size==8192);
 struct sys_cache_op arg={.addr_p=0x81236000,.size=0x100000080ULL};
 reset(); assert(sys_cache_op_userv((unsigned long)&arg,enum_cache_op_invalid)==0);
 assert(event==1 && barriers==1 && seen_addr==arg.addr_p && seen_size==arg.size);
 reset(); assert(sys_cache_op_userv((unsigned long)&arg,enum_cache_op_flush)==0);
 assert(event==2 && barriers==1 && seen_addr==arg.addr_p && seen_size==arg.size);
 reset(); copy_fails=1; assert(sys_cache_op_userv((unsigned long)&arg,enum_cache_op_flush)==-EFAULT);
 assert(event==0 && barriers==0);
 puts("PASS extracted cache entry points: directions, arguments, post-sync barriers, copy fault; no real DMA/cache operations");
 return 0;
}
