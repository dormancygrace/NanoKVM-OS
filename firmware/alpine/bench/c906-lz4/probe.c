// SPDX-License-Identifier: GPL-2.0-only
#include <linux/module.h>
#include <linux/miscdevice.h>
#include <linux/fs.h>
#include <linux/uaccess.h>
#include <linux/slab.h>
#include <linux/vmalloc.h>
#include <linux/lz4.h>
#include <linux/ktime.h>
#include "protocol.h"
#define DECLARE(v) \
extern int c906_v##v##_LZ4_compress_fast(const char *,char *,int,int,int,void *); \
extern int c906_v##v##_LZ4_decompress_safe(const char *,char *,int,int);
DECLARE(1) DECLARE(2) DECLARE(3) DECLARE(4) DECLARE(5) DECLARE(6) DECLARE(7) DECLARE(8) DECLARE(9) DECLARE(10)
typedef int (*compress_fn)(const char *,char *,int,int,int,void *);
typedef int (*decompress_fn)(const char *,char *,int,int);
static compress_fn compressors[]={LZ4_compress_fast,
c906_v1_LZ4_compress_fast,c906_v2_LZ4_compress_fast,c906_v3_LZ4_compress_fast,
c906_v4_LZ4_compress_fast,c906_v5_LZ4_compress_fast,c906_v6_LZ4_compress_fast,c906_v7_LZ4_compress_fast,c906_v8_LZ4_compress_fast,c906_v9_LZ4_compress_fast,c906_v10_LZ4_compress_fast};
static decompress_fn decompressors[]={LZ4_decompress_safe,
c906_v1_LZ4_decompress_safe,c906_v2_LZ4_decompress_safe,c906_v3_LZ4_decompress_safe,
c906_v4_LZ4_decompress_safe,c906_v5_LZ4_decompress_safe,c906_v6_LZ4_decompress_safe,c906_v7_LZ4_decompress_safe,c906_v8_LZ4_decompress_safe,c906_v9_LZ4_decompress_safe,c906_v10_LZ4_decompress_safe};
#define PAD 32
#define CAP LZ4_COMPRESSBOUND(LZ_MAX)
struct context {u8 *input,*reference,*output;void *work;};
static int open_probe(struct inode *i,struct file *f) {
    struct context *c=kzalloc(sizeof(*c),GFP_KERNEL);
    if(!c)return -ENOMEM;
    c->input=kvzalloc(LZ_MAX+2*PAD+8,GFP_KERNEL);
    c->reference=kvzalloc(CAP+2*PAD+8,GFP_KERNEL);
    c->output=kvzalloc(CAP+2*PAD+8,GFP_KERNEL);
    c->work=kvzalloc(LZ4_MEM_COMPRESS,GFP_KERNEL);
    if(!c->input||!c->reference||!c->output||!c->work){
        kvfree(c->input);kvfree(c->reference);kvfree(c->output);kvfree(c->work);kfree(c);return -ENOMEM;
    }
    f->private_data=c;return 0;
}
static int close_probe(struct inode *i,struct file *f) {
    struct context *c=f->private_data;
    kvfree(c->input);kvfree(c->reference);kvfree(c->output);kvfree(c->work);kfree(c);return 0;
}
static u8 pattern(unsigned i,unsigned p,u32 *rng) {
    *rng^=*rng<<13;*rng^=*rng>>17;*rng^=*rng<<5;
    switch(p){case 0:return 0;case 1:return (u8)*rng;case 2:return (u8)(i%16);
    case 3:return (u8)(i%7);case 4:return (i%512<480)?0:(u8)*rng;
    case 5:return " {\"value\":1234,\"enabled\":true} \n"[i%30];
    case 6:return (u8)((i/64)%32);default:return (i%256<128)?(u8)*rng:(u8)(i%31);}
}
static bool lz_guard(const u8 *p,size_t n){while(n--)if(*p++!=0xa7)return false;return true;}
static long run(struct file *f,unsigned int cmd,unsigned long arg) {
    struct context *c=f->private_data;struct lz_request r;u32 rng=0x71542391;
    u8 *src,*ref,*dst;int refbytes,result=0,capacity;unsigned i;
    if(cmd!=LZ_RUN)return -ENOTTY;
    if(copy_from_user(&r,(void __user *)arg,sizeof(r)))return -EFAULT;
    if(r.bytes>LZ_MAX||r.variant>=LZ_VARIANTS||r.pattern>7||r.offset>7||r.operation>1||
       !r.iterations||r.iterations>128||r.reserved)return -EINVAL;
    memset(c->input,0xa7,LZ_MAX+2*PAD+8);memset(c->output,0xa7,CAP+2*PAD+8);
    src=c->input+PAD+r.offset;ref=c->reference+PAD+r.offset;dst=c->output+PAD+r.offset;
    for(i=0;i<r.bytes;i++)src[i]=pattern(i,r.pattern,&rng);
    capacity=LZ4_COMPRESSBOUND(r.bytes);
    refbytes=LZ4_compress_fast(src,ref,r.bytes,capacity,1,c->work);
    if(refbytes<=0)return -EBADMSG;
    r.elapsed_ns=0;
    for(i=0;i<r.iterations;i++){
        u64 start=ktime_get_ns();
        result=r.operation?decompressors[r.variant](ref,dst,refbytes,r.bytes):
            compressors[r.variant](src,dst,r.bytes,capacity,1,c->work);
        r.elapsed_ns+=ktime_get_ns()-start;
        if(result!=(r.operation?r.bytes:refbytes))return -EBADMSG;
        if(!(i&7))cond_resched();
    }
    if(memcmp(dst,r.operation?src:ref,result))return -EBADMSG;
    if(!lz_guard(c->input,PAD+r.offset)||!lz_guard(src+r.bytes,LZ_MAX+PAD+8-r.bytes-r.offset)||
       !lz_guard(c->output,PAD+r.offset)||!lz_guard(dst+(r.operation?r.bytes:capacity),
           CAP+PAD+8-r.offset-(r.operation?r.bytes:capacity)))return -EFAULT;
    r.output=result;
    return copy_to_user((void __user *)arg,&r,sizeof(r))?-EFAULT:0;
}
static const struct file_operations ops={.owner=THIS_MODULE,.open=open_probe,.release=close_probe,.unlocked_ioctl=run};
static struct miscdevice dev={.minor=MISC_DYNAMIC_MINOR,.name="c906-lz4",.fops=&ops,.mode=0600};
static int __init start(void){return misc_register(&dev);}
static void __exit stop(void){misc_deregister(&dev);}
module_init(start);module_exit(stop);
MODULE_LICENSE("GPL");MODULE_DESCRIPTION("Bounded differential LZ4 qualifier; no production dispatch changes");
