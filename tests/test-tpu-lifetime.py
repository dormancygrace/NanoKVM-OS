#!/usr/bin/env python3
'''Exercise the TPU driver's actual mapping-lifetime and clock-start helpers.'''
from pathlib import Path
import os
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def block(source, marker):
    start = source.index(marker)
    brace = source.index('{', start)
    depth, end = 1, brace + 1
    while depth:
        depth += (source[end] == '{') - (source[end] == '}')
        end += 1
    return source[start:end]

with tempfile.TemporaryDirectory(prefix='tpu-lifetime-') as tmp:
    tmp = Path(tmp)
    # Extract the new driver files from the unchanged upstream import.
    patch = (ROOT / 'platform/kernel/0047-soc-sophgo-cv181x-tpu-driver.patch').read_text()
    for part in patch.split('diff --git ')[1:]:
        name = part.splitlines()[0].split(' b/')[1]
        if '/tpu/' not in name:
            continue
        p = tmp / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text('\n'.join(line[1:] for line in part.splitlines()
                                if line.startswith('+') and not line.startswith('+++')) + '\n')
    subprocess.run(['git', 'apply', str(ROOT / 'platform/kernel/0049-tpu-retain-job-mappings-and-check-initialization.patch')],
                   cwd=tmp, env=dict(os.environ, GIT_CEILING_DIRECTORIES=str(tmp.parent)), check=True)
    source = (tmp / 'drivers/soc/sophgo/tpu/cvi_tpu_interface.c').read_text()
    platform = (tmp / 'drivers/soc/sophgo/tpu/tpu_platform.c').read_text()
    # The tested retention helpers must be used by the real submit/cleanup paths.
    assert 'cvi_tpu_pin_registered(ctx, node)' in block(source, 'static int cvi_tpu_submit(')
    assert 'cvi_tpu_unpin_registered(node)' in block(source, 'static void cvi_tpu_cleanup_buffer(')
    assert 'if (ret < 0)\n\t\tplatform_tpu_reset(ndev)' in block(source, 'static int cvi_tpu_run_dmabuf(')
    assert 'cvi_tpu_buf_put(node->pio_src_buf)' in block(source, 'static void cvi_tpu_cleanup_buffer(')
    probe = block(source, 'static int cvi_tpu_probe(')
    assert probe.index('work_thread_init(ndev)') < probe.index('cvi_tpu_register_cdev(ndev)')
    worker = block(source, 'static void work_thread_run(')
    assert worker.index('if (ret)') < worker.index('cvi_tpu_run_dmabuf(ndev, first_node)')
    code = r'''
#include <assert.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <stdio.h>
typedef uint64_t u64;
typedef uint64_t dma_addr_t;
typedef unsigned refcount_t;
struct mutex { int unused; };
struct list_head { struct list_head *next, *prev; };
#define GFP_KERNEL 0
#define ENOMEM 12
#define DMA_BIDIRECTIONAL 0
#define container_of(p,t,m) ((t *)((char *)(p)-offsetof(t,m)))
#define list_for_each_entry(p,h,m) for(p=container_of((h)->next,__typeof__(*p),m); &(p)->m!=(h); p=container_of((p)->m.next,__typeof__(*p),m))
static void init_list(struct list_head *h) { h->next=h->prev=h; }
static void list_add(struct list_head *n,struct list_head *h) {
 n->next=h->next;n->prev=h;h->next->prev=n;h->next=n;
}
static void list_del(struct list_head *n) { n->next->prev=n->prev;n->prev->next=n->next; }
static void mutex_lock(struct mutex *m) { (void)m; }
static void mutex_unlock(struct mutex *m) { (void)m; }
static void refcount_inc(refcount_t *r) { assert(*r); ++*r; }
static bool refcount_dec_and_test(refcount_t *r) { assert(*r); return --*r==0; }
static int oom;
static void *kcalloc(size_t n,size_t s,int g) { (void)g;return oom?NULL:calloc(n,s); }
#define kfree free
struct dma_buf { size_t size; int refs; };
struct dma_buf_attachment { int mapped; };
struct sg_table { int unused; };
static int unmaps, detaches, buf_puts;
static void dma_buf_unmap_attachment_unlocked(struct dma_buf_attachment *a,struct sg_table *s,int d) {
 (void)s;(void)d;assert(a->mapped);a->mapped=0;unmaps++;
}
static void dma_buf_detach(struct dma_buf *b,struct dma_buf_attachment *a) {
 (void)b;assert(!a->mapped);detaches++;free(a);
}
static void dma_buf_put(struct dma_buf *b) { assert(b->refs==1);buf_puts++;free(b); }
'''
    code += block(source, 'struct cvi_tpu_buf {') + ';\n'
    code += r'''
struct cvi_list_node { struct cvi_tpu_buf **registered_bufs; unsigned registered_count; };
struct cvi_tpu_ctx { struct list_head bufs; struct mutex bufs_lock; };
'''
    for marker in ['static void cvi_tpu_buf_put(', 'static void cvi_tpu_unpin_registered(',
                   'static int cvi_tpu_pin_registered(', 'static void cvi_tpu_release_buf(',
                   'static struct cvi_tpu_buf *cvi_tpu_pin_pio_range(']:
        code += block(source, marker) + '\n'
    code += r'''
struct clk { int enabled, error; };
struct cvi_tpu_device { struct clk *clk_tpu_axi, *clk_tpu_fab; };
static int resets;
static int clk_prepare_enable(struct clk *c) { if(c->error)return c->error;c->enabled++;return 0; }
static void clk_disable_unprepare(struct clk *c) { assert(c->enabled);c->enabled--; }
static int platform_tpu_reset(struct cvi_tpu_device *d) { (void)d;resets++;return 0; }
'''
    code += block(platform, 'int platform_tpu_init(') + '\n'
    code += r'''
static struct cvi_tpu_buf *new_buffer(struct cvi_tpu_ctx *ctx,u64 base) {
 struct cvi_tpu_buf *b=calloc(1,sizeof(*b));
 b->refs=1;b->daddr=base;b->dmabuf=calloc(1,sizeof(*b->dmabuf));
 b->dmabuf->size=4096;b->dmabuf->refs=1;
 b->attach=calloc(1,sizeof(*b->attach));b->attach->mapped=1;
 list_add(&b->list,&ctx->bufs);return b;
}
int main(void) {
 struct cvi_tpu_ctx ctx={0};init_list(&ctx.bufs);
 struct cvi_list_node one={0},two={0};
 struct cvi_tpu_buf *a=new_buffer(&ctx,0x1000),*b=new_buffer(&ctx,0x2000);
 assert(!cvi_tpu_pin_registered(&ctx,&one));
 assert(!cvi_tpu_pin_registered(&ctx,&two));
 cvi_tpu_release_buf(a);cvi_tpu_release_buf(b); /* RELEASE or close */
 assert(!unmaps && !buf_puts && a->attach->mapped && b->attach->mapped);
 cvi_tpu_unpin_registered(&one);assert(!unmaps);
 cvi_tpu_unpin_registered(&two);assert(unmaps==2 && buf_puts==2 && detaches==2);
 cvi_tpu_unpin_registered(&two);assert(unmaps==2); /* done-node cleanup */
 a=new_buffer(&ctx,0x1000);
 struct cvi_tpu_buf *src=cvi_tpu_pin_pio_range(&ctx,0x1000,4096);
 struct cvi_tpu_buf *dst=cvi_tpu_pin_pio_range(&ctx,0x1001,4095);
 assert(src==a && dst==a);
 assert(!cvi_tpu_pin_pio_range(&ctx,0x1001,4096));
 assert(!cvi_tpu_pin_pio_range(&ctx,UINT64_MAX,2));
 cvi_tpu_release_buf(a);
 cvi_tpu_buf_put(src);assert(unmaps==2 && dst->attach->mapped);
 cvi_tpu_buf_put(dst);assert(unmaps==3);
 a=new_buffer(&ctx,0x1000);oom=1;
 assert(cvi_tpu_pin_registered(&ctx,&one)==-ENOMEM && a->refs==1);
 oom=0;cvi_tpu_release_buf(a);assert(unmaps==4);
 assert(!cvi_tpu_pin_registered(&ctx,&one));cvi_tpu_unpin_registered(&one);
 struct clk axi={.error=-5},fab={0};struct cvi_tpu_device dev={&axi,&fab};
 assert(platform_tpu_init(&dev)==-5 && !axi.enabled && !fab.enabled && !resets);
 axi.error=0;fab.error=-6;
 assert(platform_tpu_init(&dev)==-6 && !axi.enabled && !fab.enabled && !resets);
 fab.error=0;
 assert(!platform_tpu_init(&dev) && axi.enabled==1 && fab.enabled==1 && resets==1);
 printf("PASS: descriptor/PIO mappings survive release and close; startup failures unwind\n");
 return 0;
}
'''
    (tmp / 'test.c').write_text(code)
    subprocess.run(['cc','-std=gnu11','-Wall','-Wextra','-Werror',
                    '-fsanitize=address,undefined',str(tmp/'test.c'),'-o',str(tmp/'test')],check=True)
    subprocess.run([str(tmp/'test')],check=True)
