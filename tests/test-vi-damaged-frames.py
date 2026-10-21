#!/usr/bin/env python3
"""Run the patched VI driver's real buffer-queue, YUV bypass frame-end and
error-wake functions against a model of the device: a frame that failed the
CSI bridge checks is never published, in any interrupt order, and the ready
queue count stays exact. The error handler thread is modelled by the lines
osdrv 0019 adds to it and the vendor's retrigger (checked in the source)."""
import argparse
from pathlib import Path
import re
import subprocess
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('osdrv', type=Path, help='the patched osdrv tree, as platform/build.sh modules prepares it')
a = p.parse_args()
source = (a.osdrv / 'interdrv/vi/chip/cv181x/vi.c').read_text()


def block(marker):
    start = source.index(marker)
    brace = source.index('{', start)
    depth, i = 1, brace + 1
    while depth:
        depth += (source[i] == '{') - (source[i] == '}')
        i += 1
    return source[start:i]


# What the model of the rest of the driver relies on.
handler = block('static void _vi_err_handler(')
assert 'atomic_set(&nk_frame_damaged[err_raw_num], 0);' in handler.split('//step 7')[1].split('//step 8')[0]
retrig = block('static void _vi_err_retrig_preraw(')
assert 'vdev->qbuf_num[buf_chn]++;' in retrig and '_isp_yuv_bypass_trigger(vdev, wait_fe, fe_chn);' in retrig
enq = block('static void _isp_yuv_bypass_buf_enq(')
assert re.search(r'cvi_isp_rdy_buf_pop\(vdev, buf_chn\);\s*b = _cvi_isp_next_buf\(vdev, buf_chn\);', enq)
stop = block('int vi_stop_streaming(') if 'int vi_stop_streaming(' in source else block('static int vi_stop_streaming(')
assert 'atomic_set(&nk_frame_damaged[i], 0);' in stop

code = r'''
#include <assert.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
typedef uint8_t u8; typedef uint32_t u32; typedef uint64_t u64;
#define VI_DBG 1
#define VI_INFO 2
#define VI_WARN 4
#define VI_ERR 8
#define vi_pr(level, ...) ((void)(level))
typedef struct { int counter; } atomic_t;
static void atomic_set(atomic_t *v, int i) { v->counter = i; }
static int atomic_read(const atomic_t *v) { return v->counter; }
static int atomic_xchg(atomic_t *v, int i) { int o = v->counter; v->counter = i; return o; }
static int atomic_cmpxchg(atomic_t *v, int o, int n) { int c = v->counter; if (c == o) v->counter = n; return c; }
typedef int spinlock_t;
static int locked;
#define spin_lock_irqsave(l, f) do { (void)(l); assert(!locked); locked = 1; (f) = 0; } while (0)
#define spin_unlock_irqrestore(l, f) do { (void)(l); (void)(f); assert(locked); locked = 0; } while (0)
struct list_head { struct list_head *next, *prev; };
static void INIT_LIST_HEAD(struct list_head *h) { h->next = h->prev = h; }
static int list_empty(const struct list_head *h) { return h->next == h; }
static void list_add_tail(struct list_head *n, struct list_head *h) { n->prev = h->prev; n->next = h; h->prev->next = n; h->prev = n; }
static void list_del_init(struct list_head *n) { n->prev->next = n->next; n->next->prev = n->prev; INIT_LIST_HEAD(n); }
#define container_of(p, t, m) ((t *)((char *)(p) - offsetof(t, m)))
#define list_first_entry(h, t, m) container_of((h)->next, t, m)
#define GFP_ATOMIC 0
#define kzalloc(n, f) calloc(1, n)
#define kfree free
struct timespec64 { long tv_sec, tv_nsec; };
static long ktime_get(void) { return 0; }
static struct timespec64 ktime_to_timespec64(long t) { struct timespec64 s = { 0, t }; return s; }
static int wakes;
#define wake_up(q) ((void)(q), wakes++)
enum cvi_isp_raw { ISP_PRERAW_A, ISP_PRERAW_B, ISP_PRERAW_C, ISP_PRERAW_MAX,
	ISP_PRERAW_VIRT_A = ISP_PRERAW_MAX, ISP_PRERAW_VIRT_B, ISP_PRERAW_VIRT_MAX };
enum { ISP_FE_CH0, ISP_FE_CH1 };
enum { ISP_PRERAW_IDLE, ISP_PRERAW_RUNNING };
enum { E_VI_TH_EVENT_HANDLER, E_VI_TH_ERR_HANDLER, E_VI_TH_MAX };
#define ISP_FE_CHN_MAX 6
struct cvi_isp_buf { struct { int index; } buf; struct list_head list; };
struct vi_th { u32 flag; int wq; };
struct isp_ctx { u8 rawb_chnstr_num; };
struct cvi_vi_dev {
	struct isp_ctx ctx; spinlock_t qbuf_lock;
	struct list_head qbuf_list[ISP_FE_CHN_MAX]; int qbuf_num[ISP_FE_CHN_MAX];
	atomic_t pre_fe_state[ISP_PRERAW_MAX][2]; u32 pre_fe_frm_num[ISP_PRERAW_MAX][2];
	struct vi_th vi_th[E_VI_TH_MAX]; atomic_t isp_streamoff, isp_err_handle_flag;
};
struct _isp_dqbuf_n { u8 raw_id; u8 chn_id; u32 frm_num; struct timespec64 timestamp; struct list_head list; };
static struct { struct list_head list; } dqbuf_q;
static spinlock_t dq_lock;
/* The DMA address of the armed buffer: the real _isp_yuv_bypass_buf_enq
 * pops the count and takes the list head (checked in the source). */
static struct cvi_isp_buf *armed;
static int triggers, no_buffer;
static void isp_pre_trig(struct isp_ctx *c, int raw, int chn) { (void)c; (void)raw; (void)chn; triggers++; }
struct cvi_isp_buf *_cvi_isp_next_buf(struct cvi_vi_dev *vdev, const u8 chn_num);
static void cvi_isp_rdy_buf_pop(struct cvi_vi_dev *vdev, const u8 chn_num);
static void _isp_yuv_bypass_buf_enq(struct cvi_vi_dev *vdev, const enum cvi_isp_raw raw_num, const u8 buf_chn)
{
	(void)raw_num;
	cvi_isp_rdy_buf_pop(vdev, buf_chn);
	armed = _cvi_isp_next_buf(vdev, buf_chn);
	if (!armed)
		no_buffer++;
}
''' + re.search(r'static atomic_t nk_frame_damaged\[ISP_PRERAW_VIRT_MAX\];', source)[0] + '\n' + '\n'.join(block(m) for m in (
    'static void cvi_isp_buf_queue(', 'struct cvi_isp_buf *_cvi_isp_next_buf(', 'static int cvi_isp_rdy_buf_empty(',
    'static void cvi_isp_rdy_buf_pop(', 'static void cvi_isp_rdy_buf_remove(', 'static void cvi_isp_dqbuf_list(',
    'static int _isp_yuv_bypass_trigger(', 'static void _isp_yuv_bypass_handler(',
    'static inline void vi_err_wake_up_th(')) + r'''

static struct cvi_vi_dev d;
static int published(void)
{
	int n = 0;
	while (!list_empty(&dqbuf_q.list)) {
		struct _isp_dqbuf_n *x = list_first_entry(&dqbuf_q.list, struct _isp_dqbuf_n, list);
		list_del_init(&x->list); free(x); n++;
	}
	return n;
}
static int listed(void) { int n = 0; for (struct list_head *i = d.qbuf_list[0].next; i != &d.qbuf_list[0]; i = i->next) n++; return n; }
/* Ready count = buffers in the queue minus the one the DMA writes. */
static void invariant(void)
{
	int running = atomic_read(&d.pre_fe_state[0][0]) == ISP_PRERAW_RUNNING;
	assert(d.qbuf_num[0] == listed() - running);
	assert(!running || armed == list_first_entry(&d.qbuf_list[0], struct cvi_isp_buf, list));
	assert(!no_buffer && !locked);
}
static void frame_end(void) { d.pre_fe_frm_num[0][0]++; _isp_yuv_bypass_handler(&d, ISP_PRERAW_A, 0); invariant(); }
static void csi_error(void) { vi_err_wake_up_th(&d, ISP_PRERAW_A); invariant(); }
/* The error handler thread: abort (state idle), step 7, the flag, retrigger. */
static void err_thread(void)
{
	if (!atomic_read(&d.isp_err_handle_flag))
		return;
	atomic_set(&d.pre_fe_state[0][0], ISP_PRERAW_IDLE);
	atomic_set(&nk_frame_damaged[0], 0);
	atomic_set(&d.isp_err_handle_flag, 0);
	d.vi_th[E_VI_TH_ERR_HANDLER].flag = 0;
	d.qbuf_num[0]++;
	if (!cvi_isp_rdy_buf_empty(&d, 0))
		_isp_yuv_bypass_trigger(&d, ISP_PRERAW_A, 0);
	invariant();
}
/* The application returns a published buffer. */
static void give_back(void) { struct cvi_isp_buf *b = calloc(1, sizeof(*b)); b->buf.index = 0; cvi_isp_buf_queue(&d, b); invariant(); }
static void start(void)
{
	INIT_LIST_HEAD(&d.qbuf_list[0]); INIT_LIST_HEAD(&dqbuf_q.list);
	for (int i = 0; i < 3; i++) give_back();
	_isp_yuv_bypass_trigger(&d, ISP_PRERAW_A, 0);
	invariant();
}

int main(void)
{
	start();
	frame_end();                    /* a good frame */
	assert(published() == 1); give_back();
	/* Error and frame end in separate interrupts, the thread after both. */
	csi_error(); frame_end();
	assert(published() == 0);
	err_thread();
	frame_end(); assert(published() == 1); give_back();
	/* Coalesced in one interrupt: the error check runs before the frame end. */
	csi_error(); frame_end(); assert(published() == 0);
	err_thread(); frame_end(); assert(published() == 1); give_back();
	/* The thread aborts the frame before its end: nothing to publish, and
	 * the next frame is good. */
	csi_error(); err_thread();
	frame_end(); assert(published() == 1); give_back();
	/* Repeated errors with the thread running behind: no count drifts. */
	for (int i = 0; i < 40; i++) {
		csi_error(); frame_end();
		if (i % 3 == 2)
			err_thread();
	}
	assert(published() == 0);
	err_thread();
	for (int i = 0; i < 10; i++) { frame_end(); assert(published() == 1); give_back(); }
	assert(listed() == 3);
	puts("PASS: damaged frames unpublished in every order; ready count exact");
	return 0;
}
'''
with tempfile.TemporaryDirectory(prefix='vi-damaged-test-') as tmp:
    tmp = Path(tmp)
    (tmp / 'test.c').write_text(code)
    subprocess.run(['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror', '-Wno-unused-function', '-Wno-unused-parameter',
                    '-fsanitize=address,undefined', str(tmp / 'test.c'), '-o', str(tmp / 'test')], check=True)
    subprocess.run([str(tmp / 'test')], check=True)
