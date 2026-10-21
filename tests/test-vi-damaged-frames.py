#!/usr/bin/env python3
"""Run the patched VI driver's real buffer-queue, YUV bypass frame-end,
error-wake, error-handler and retrigger functions against a model of the
device: a frame that failed the CSI bridge checks is never published, a frame
end during error recovery does not race the handler's restart, and the ready
queue count stays exact. The hardware steps of the handler
(isp_frm_err_handler) are a hook that can deliver a frame end or fail."""
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
enq = block('static void _isp_yuv_bypass_buf_enq(')
assert re.search(r'cvi_isp_rdy_buf_pop\(vdev, buf_chn\);\s*b = _cvi_isp_next_buf\(vdev, buf_chn\);\s*'
                 r'if \(b == NULL\) \{[^}]*\}\s*atomic_set\(&nk_buf_out\[raw_num\]\[hw_chn_num\], 1\);', enq)
stop = block('int vi_stop_streaming(') if 'int vi_stop_streaming(' in source else block('static int vi_stop_streaming(')
assert all(f'atomic_set(&{v}, 0);' in stop for v in ('nk_frame_damaged[i]', 'nk_err_recovering[i]', 'nk_eof_held[i][j]', 'nk_buf_out[i][j]'))
assert '_vi_err_handler(vdev, err_raw_num);' in block('static int _vi_err_handler_thread(')
declarations = '\n'.join(re.findall(r'static atomic_t nk_\w+(?:\[\w+\])+;', source))
assert declarations.count('static atomic_t') == 4, declarations

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
#define unlikely(x) (x)
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
enum cvi_isp_pre_chn_num { ISP_FE_CH0, ISP_FE_CH1, ISP_FE_CH2, ISP_FE_CH3 };
enum { ISP_PRERAW_IDLE, ISP_PRERAW_RUNNING };
enum { ISP_POSTRAW_IDLE, ISP_POSTRAW_RUNNING };
enum { ISP_PRE_BE_IDLE, ISP_PRE_BE_RUNNING };
enum { ISP_BE_CH0, ISP_BE_CH1 };
enum { E_VI_TH_EVENT_HANDLER, E_VI_TH_ERR_HANDLER, E_VI_TH_MAX };
#define ISP_FE_CHN_MAX 4
/* The register accesses of the handler outside isp_frm_err_handler. */
enum { ISP_BLK_ID_TNR, ISP_BLK_ID_DMA_CTL41, ISP_BLK_ID_DMA_CTL42, ISP_BLK_ID_MAX };
#define ISP_RD_BITS(base, ...) ((void)(base), 0)
#define ISP_WR_BITS(base, ...) ((void)(base))
enum { E_MODULE_VPSS, VPSS_CB_ONLINE_ERR_HANDLE };
struct sc_err_handle_cb { int snr_num; };
static int _vi_call_cb(int m, int c, void *a) { (void)m; (void)c; (void)a; return 0; }
static int ctrl_flow;
struct cvi_isp_buf { struct { int index; } buf; struct list_head list; };
struct vi_th { u32 flag; int wq; };
struct isp_pipe_cfg { bool is_offline_scaler, is_yuv_bypass_path, is_mux, is_hdr_on; u8 muxMode; u32 first_frm_cnt; };
struct isp_ctx {
	uintptr_t phys_regs[ISP_BLK_ID_MAX]; u8 rawb_chnstr_num;
	bool is_offline_be, is_offline_postraw, is_slice_buf_on, is_multi_sensor, is_fbc_on;
	bool isp_pipe_enable[ISP_PRERAW_MAX]; struct isp_pipe_cfg isp_pipe_cfg[ISP_PRERAW_VIRT_MAX];
};
struct cvi_vi_dev {
	struct isp_ctx ctx; spinlock_t qbuf_lock;
	struct list_head qbuf_list[ISP_FE_CHN_MAX]; int qbuf_num[ISP_FE_CHN_MAX];
	atomic_t pre_fe_state[ISP_PRERAW_VIRT_MAX][ISP_FE_CHN_MAX]; u32 pre_fe_frm_num[ISP_PRERAW_VIRT_MAX][ISP_FE_CHN_MAX];
	atomic_t postraw_state, pre_be_state[2]; u32 isp_err_times[ISP_PRERAW_VIRT_MAX];
	struct vi_th vi_th[E_VI_TH_MAX]; atomic_t isp_streamoff, isp_err_handle_flag;
};
static u32 _is_fe_be_online(struct isp_ctx *ctx) { return !ctx->is_offline_be && ctx->is_offline_postraw; }
static u32 _is_be_post_online(struct isp_ctx *ctx) { return ctx->is_offline_be && !ctx->is_offline_postraw; }
static void ispblk_dma_set_sw_mode(struct isp_ctx *c, int id, bool on) { (void)c; (void)id; (void)on; }
static void _vi_show_debug_info(void);
static void _postraw_clear_inbuf(struct cvi_vi_dev *vdev) { (void)vdev; }
static void _pre_hw_enque(struct cvi_vi_dev *vdev, int raw, int chn) { (void)vdev; (void)raw; (void)chn; assert(!"RGB path"); }
struct _isp_dqbuf_n { u8 raw_id; u8 chn_id; u32 frm_num; struct timespec64 timestamp; struct list_head list; };
static struct { struct list_head list; } dqbuf_q;
static spinlock_t dq_lock;
''' + declarations + r'''
/* The device: whether the DMA holds an armed buffer, and triggers since the
 * last reset. The real _isp_yuv_bypass_buf_enq pops the count, takes the
 * list head and, with a buffer, marks it given out (checked in the source). */
static struct cvi_isp_buf *armed;
static int hw_armed, triggers_since_reset, no_buffer;
static void isp_pre_trig(struct isp_ctx *c, int raw, int chn)
{
	(void)c; (void)raw; (void)chn;
	assert(!hw_armed); /* a trigger over a running DMA */
	assert(armed); /* a DMA into whatever buffer it last had */
	hw_armed = 1; triggers_since_reset++;
}
struct cvi_isp_buf *_cvi_isp_next_buf(struct cvi_vi_dev *vdev, const u8 chn_num);
static void cvi_isp_rdy_buf_pop(struct cvi_vi_dev *vdev, const u8 chn_num);
static void _isp_yuv_bypass_buf_enq(struct cvi_vi_dev *vdev, const enum cvi_isp_raw raw_num, const u8 buf_chn)
{
	(void)raw_num;
	cvi_isp_rdy_buf_pop(vdev, buf_chn);
	armed = _cvi_isp_next_buf(vdev, buf_chn);
	if (!armed) {
		no_buffer++;
		return;
	}
	atomic_set(&nk_buf_out[raw_num][buf_chn], 1);
}
static int isp_frm_err_handler(struct isp_ctx *ctx, const enum cvi_isp_raw err_raw_num, const u8 step);
static void usleep_range(long a, long b);
''' + '\n'.join(block(m) for m in (
    'static void cvi_isp_buf_queue(', 'struct cvi_isp_buf *_cvi_isp_next_buf(', 'static int cvi_isp_rdy_buf_empty(',
    'static void cvi_isp_rdy_buf_pop(', 'static void cvi_isp_rdy_buf_remove(', 'static void cvi_isp_dqbuf_list(',
    'static int _isp_yuv_bypass_trigger(', 'static void nk_err_recovery_clear(', 'static void nk_err_recovery_abandon(',
    'static void _vi_err_retrig_preraw(', 'static void _vi_err_handler(', 'static void _isp_yuv_bypass_handler(',
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
/* Between interrupts and outside the handler: the state matches the device,
 * and the ready count = buffers in the queue minus the one the DMA writes. */
static int line; /* the scenario (line in main) of a failing check */
static void invariant(void)
{
	int running = atomic_read(&d.pre_fe_state[0][0]) == ISP_PRERAW_RUNNING;
	if (running != hw_armed || d.qbuf_num[0] != listed() - running)
		fprintf(stderr, "scenario at line %d: running %d device %d ready %d listed %d\n",
			line, running, hw_armed, d.qbuf_num[0], listed());
	assert(running == hw_armed);
	assert(d.qbuf_num[0] == listed() - running);
	assert(!running || armed == list_first_entry(&d.qbuf_list[0], struct cvi_isp_buf, list));
	assert(!no_buffer && !locked);
}
/* The frame end interrupt of the armed DMA (the vendor code before the
 * handler counts the frame and resets the error count). */
static void eof_irq(void)
{
	assert(hw_armed);
	hw_armed = 0;
	d.pre_fe_frm_num[0][0]++;
	d.isp_err_times[0] = 0;
	_isp_yuv_bypass_handler(&d, ISP_PRERAW_A, 0);
}
static int eof_step = -1, fail_step = -1, wait_mode, late_eof, latched;
/* The handler's hardware steps: a frame end can arrive during any of them
 * while the DMA runs; step 4 resets the ISP, which drops the armed DMA. */
static int isp_frm_err_handler(struct isp_ctx *ctx, const enum cvi_isp_raw err_raw_num, const u8 step)
{
	(void)ctx; (void)err_raw_num;
	if (step == eof_step && hw_armed)
		eof_irq();
	if (step == fail_step)
		return -1;
	if (step == 4) {
		/* The DMA's frame end raised just before the reset, serviced late. */
		if (late_eof && hw_armed)
			latched = 1;
		hw_armed = 0;
		triggers_since_reset = 0;
	}
	return 0;
}
/* The handler's step 2 wait for the post stage. wait_mode 1: a frame end
 * arrives while it sleeps, then the post stage goes idle; 2: a frame end
 * arrives and the stage never goes idle, so the handler gives up; 3: no
 * frame end and it gives up. */
static void usleep_range(long a, long b)
{
	(void)a; (void)b;
	if ((wait_mode == 1 || wait_mode == 2) && hw_armed)
		eof_irq();
	if (wait_mode == 1)
		atomic_set(&d.postraw_state, ISP_POSTRAW_IDLE);
}
/* Step 10, after step 7: the CPU services a frame end latched before the
 * reset. */
static void _vi_show_debug_info(void)
{
	if (!latched)
		return;
	latched = late_eof = 0;
	d.pre_fe_frm_num[0][0]++;
	_isp_yuv_bypass_handler(&d, ISP_PRERAW_A, 0);
}
static void frame_end(void) { eof_irq(); invariant(); }
#define csi_error() (line = __LINE__ - LINE0, vi_err_wake_up_th(&d, ISP_PRERAW_A))
/* The error handler thread, as _vi_err_handler_thread runs it. */
static void err_thread(void)
{
	if (!d.vi_th[E_VI_TH_ERR_HANDLER].flag)
		return;
	d.vi_th[E_VI_TH_ERR_HANDLER].flag = 0;
	_vi_err_handler(&d, ISP_PRERAW_A);
}
/* A completed recovery: one restart after the reset, the state consistent. */
static void recovered(void)
{
	invariant();
	assert(hw_armed && triggers_since_reset == 1);
	assert(!atomic_read(&d.isp_err_handle_flag) && !atomic_read(&nk_err_recovering[0]));
}
/* The application returns a published buffer. */
static void give_back(void) { struct cvi_isp_buf *b = calloc(1, sizeof(*b)); b->buf.index = 0; cvi_isp_buf_queue(&d, b); invariant(); }
static void good(void) { frame_end(); assert(published() == 1); give_back(); }
static void start(void)
{
	INIT_LIST_HEAD(&d.qbuf_list[0]); INIT_LIST_HEAD(&dqbuf_q.list);
	/* NanoKVM: one YUV bypass channel to the scaler, fe->be->dram->post. */
	d.ctx.is_offline_be = false; d.ctx.is_offline_postraw = true;
	d.ctx.isp_pipe_enable[0] = true;
	d.ctx.isp_pipe_cfg[0].is_yuv_bypass_path = true; d.ctx.isp_pipe_cfg[0].is_offline_scaler = true;
	for (int i = 0; i < 3; i++) give_back();
	_isp_yuv_bypass_trigger(&d, ISP_PRERAW_A, 0);
	invariant();
}

enum { LINE0 = __LINE__ };
int main(void)
{
	start();
	good();
	/* Error and frame end in separate interrupts, the thread after both:
	 * the frame end is held back, the handler restarts once. */
	csi_error(); eof_irq(); assert(published() == 0);
	err_thread(); recovered();
	good();
	/* Coalesced in one interrupt: the error check runs before the frame end. */
	csi_error(); eof_irq(); assert(published() == 0);
	err_thread(); recovered(); good();
	/* The thread aborts the frame before its end. */
	csi_error(); err_thread(); recovered(); good();
	/* A frame end during each step of the handler before its reset, or
	 * during its step 2 wait: held back, one restart after the reset. */
	for (int s = 1; s <= 4; s++) {
		csi_error(); eof_step = s; err_thread(); eof_step = -1;
		assert(published() == 0);
		recovered(); good();
	}
	csi_error(); wait_mode = 1; atomic_set(&d.postraw_state, ISP_POSTRAW_RUNNING);
	err_thread(); wait_mode = 0; recovered(); good();
	/* A frame end the device raised before the reset and the CPU serviced
	 * after step 7: still held back, one restart. */
	csi_error(); late_eof = 1; err_thread(); assert(!late_eof && published() == 0);
	recovered(); good();
	/* The idle wait after the reset fails: the reset dropped the DMA, so the
	 * handler restarts anyway, with or without a held frame end. */
	for (int s = 0; s <= 4; s++) {
		csi_error(); eof_step = s; fail_step = 6; err_thread(); eof_step = fail_step = -1;
		assert(published() == 0);
		recovered(); good();
	}
	/* The handler gives up before its reset: the step 2 wait times out
	 * (after a frame end, or without one) or the abort fails (after a frame
	 * end at step 1, or without one). A held frame end restarts its channel;
	 * a channel still running ends its frame, dropped by the mark. The
	 * handler flag is cleared, so the next error runs the handler again. */
	for (int i = 0; i < 4; i++) {
		int held = i == 0 || i == 2;

		csi_error();
		if (i < 2) {
			wait_mode = i == 0 ? 2 : 3;
			atomic_set(&d.postraw_state, ISP_POSTRAW_RUNNING);
		} else {
			eof_step = i == 2 ? 1 : -1;
			fail_step = 3;
		}
		err_thread();
		eof_step = fail_step = -1; wait_mode = 0; atomic_set(&d.postraw_state, ISP_POSTRAW_IDLE);
		assert(published() == 0 && !atomic_read(&d.isp_err_handle_flag) && !atomic_read(&nk_err_recovering[0]));
		if (held) {
			invariant(); assert(hw_armed);
		} else {
			frame_end(); assert(published() == 0);
		}
		good();
		csi_error(); assert(d.vi_th[E_VI_TH_ERR_HANDLER].flag);
		err_thread(); recovered(); good();
	}
	/* A handler flag set with no handler woken (VI_CB_RESET_ISP): an error
	 * only drops its frame and capture continues. */
	atomic_set(&d.isp_err_handle_flag, 1);
	csi_error(); assert(!d.vi_th[E_VI_TH_ERR_HANDLER].flag && !atomic_read(&nk_err_recovering[0]));
	frame_end(); assert(published() == 0);
	good();
	atomic_set(&d.isp_err_handle_flag, 0);
	/* An error while every buffer is with the application: the handler
	 * counts nothing back and triggers nothing; capture restarts when a
	 * buffer returns (the queue path triggers then). */
	frame_end(); frame_end(); frame_end();
	assert(published() == 3 && listed() == 0 && !hw_armed);
	csi_error(); err_thread();
	invariant(); assert(!hw_armed && d.qbuf_num[0] == 0);
	/* A trigger on the drained queue (a retried tasklet trigger) is refused
	 * and leaves the state idle. */
	assert(_isp_yuv_bypass_trigger(&d, ISP_PRERAW_A, 0) == -1);
	invariant(); assert(!hw_armed && d.qbuf_num[0] == 0);
	for (int i = 0; i < 3; i++)
		give_back();
	assert(_isp_yuv_bypass_trigger(&d, ISP_PRERAW_A, 0) == 0);
	invariant(); good();
	/* Repeated errors with the thread running behind: no count drifts. */
	for (int i = 0; i < 40; i++) {
		csi_error();
		if (hw_armed)
			eof_irq();
		if (i % 3 == 2) {
			err_thread();
			invariant();
		}
	}
	assert(published() == 0);
	err_thread(); recovered();
	for (int i = 0; i < 10; i++)
		good();
	assert(listed() == 3);
	puts("PASS: damaged frames unpublished, every recovery restarts once, ready count exact");
	return 0;
}
'''
with tempfile.TemporaryDirectory(prefix='vi-damaged-test-') as tmp:
    tmp = Path(tmp)
    (tmp / 'test.c').write_text(code)
    subprocess.run(['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror', '-Wno-unused-function', '-Wno-unused-parameter',
                    '-fsanitize=address,undefined', str(tmp / 'test.c'), '-o', str(tmp / 'test')], check=True)
    subprocess.run([str(tmp / 'test')], check=True)
