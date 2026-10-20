#include "internal/capture_rate.hpp"
#include <stdint.h>
#include <string.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <unistd.h>
#include <sys/time.h>
#include <sys/param.h>
#include "math.h"
#include <inttypes.h>
#include <pthread.h>
#include <mutex>
#include <time.h>

#include <fcntl.h>		/* low-level i/o */
#include "cvi_buffer.h"
#include "cvi_ae_comm.h"
#include "cvi_awb_comm.h"
#include "cvi_comm_isp.h"
#include "cvi_comm_sns.h"
#include "cvi_ae.h"
#include "cvi_awb.h"
#include "cvi_isp.h"
#include "cvi_sns_ctrl.h"
#include "cvi_sys.h"
#include <linux/vpss_uapi.h>
#include "sample_comm.h"
#include "kvm_mmf.hpp"
#include "internal/frame_buffer.hpp"

#define MMF_VI_MAX_CHN            2       // Qualified VPSS outputs; VI still has one source.
#define MMF_VENC_MAX_CHN		4

#define MMF_VB_VI_ID			0

#if VPSS_MAX_PHY_CHN_NUM < MMF_VI_MAX_CHN
#error "VPSS_MAX_PHY_CHN_NUM < MMF_VI_MAX_CHN"
#endif

typedef struct {
	uint8_t type;	// 0, jpg; 1, h265; 2, h264
	uint8_t is_inited;
	uint8_t is_used;
	uint8_t is_running;
	VIDEO_FRAME_INFO_S *capture_frame;
	VENC_STREAM_S capture_stream;
	mmf_venc_cfg_t cfg;
	uint32_t pool_id;
} venc_info_t;

typedef struct {
	int mmf_used_cnt;
	bool vi_is_inited;
	bool vi_chn_is_inited[MMF_VI_MAX_CHN];
	bool vi_chn_stopping[MMF_VI_MAX_CHN];
	bool vi_chn_disabled[MMF_VI_MAX_CHN];
	bool vi_source_bound;  // the one VI source is bound to VPSS group 0
	int vi_chn_pool_id[MMF_VI_MAX_CHN];
    int vi_buffers[MMF_VI_MAX_CHN], vi_queue_depth[MMF_VI_MAX_CHN];
	SIZE_S vi_size;
	VIDEO_FRAME_INFO_S vi_frame[MMF_VI_MAX_CHN];

	int enc_jpg_is_init;
	VENC_STREAM_S enc_jpeg_frame;
	int enc_jpg_frame_w;
	int enc_jpg_frame_h;
	int enc_jpg_frame_fmt;
	int enc_jpg_running;
	int enc_jpg_quality;
	VIDEO_FRAME_INFO_S *enc_jpg_frame;
	int enc_jpg_input_pool_id;
	// The JPEG channel whose CVI_VENC_DestroyChn failed, retried in teardown.
	bool enc_jpg_destroy_failed;
	int enc_jpg_destroy_ch;

	venc_info_t venc[MMF_VENC_MAX_CHN];

	bool vb_pool_used[VB_MAX_COMM_POOLS];  // created by _create_vb_pool

	// Zero-copy state: VI frame leases and the VENC streams and inputs.
	bool vi_frame_valid[MMF_VI_MAX_CHN];
	bool vi_frame_mapped[MMF_VI_MAX_CHN];
	bool vi_frame_deferred[MMF_VI_MAX_CHN];
	bool venc_stream_acquired[MMF_VENC_MAX_CHN];
	int venc_input_vi_ch[MMF_VENC_MAX_CHN];
} priv_t;

typedef struct {
	bool vi_hmirror[MMF_VI_MAX_CHN];
	bool vi_vflip[MMF_VI_MAX_CHN];
} g_priv_t;

static priv_t priv;
static g_priv_t g_priv;
static VENC_PACK_S *venc_pack_storage[MMF_VENC_MAX_CHN];
static CVI_U32 venc_pack_capacity[MMF_VENC_MAX_CHN];

/*
 * H.26x encoder tuning. /run/nanokvm-venc-tune, a volatile key=value file,
 * overrides the defaults below for A/B measurements and is read when a channel
 * is created.
 *
 * Every viewer asks for an IDR when it needs one (a new subscriber, WebRTC
 * PLI, Direct and RustDesk recovery), so periodic IDRs only cost bits and
 * delay: at 2560x1440 an IDR is a few hundred KB in one frame period. One
 * every idr_seconds is kept as a safety net; a requested IDR restarts the
 * count (the driver resets its GOP counter).
 *
 * QP floor 20: with a lower floor CBR keeps refining a static picture on
 * every frame (static 1440p desktop: 0.51 Mbit/s at 20, 0.77 at 14/12, 9.7 at
 * 2/0), and 20 looks no worse than 16/14 or 14/12.
 *
 * The GOP is NormalP, with no further encoder features. SmartP (GOP 30,
 * 60 fps) made a virtual I frame every 0.5 s, a ~250 KB background IDR every
 * 2.5 s at 1440p, and kept an extra reference frame in ION. VBR does not lower
 * the static bitrate; AVBR needs a VI motion level that frames sent with
 * SendFrame do not carry; super-frame re-encoding doubles the encode work; SAO
 * and the H.265 fast preset need a driver parameter, and 1440p60 leaves no
 * hardware headroom (about 15 of 16.7 ms per frame); the driver keeps intra
 * refresh off.
 */
struct venc_tuning_t {
	int idr_seconds = 10;
	// High (8x8 transform) only on request: the WebRTC offer and the Direct
	// decoder configuration declare Main (profile-level-id 4d402a, avc1.4D402A),
	// and a decoder that accepts Main need not decode High.
	int h264_high = 0;
	int min_qp = 20;      // quality ceiling of P frames on static content
	int min_iqp = 20;
	int max_qp = 51;
	int max_iqp = 51;
	int max_iprop = 10;   // I-frame bits at most this many average frames
	int ip_qp_delta = 2;
	int row_qp_delta = -1; // -1: driver default
	int thrd_lv = -1;
	int stats = 0;        // 1: per-second statistics in /run/nanokvm-venc-stats
	int buf_kb = 0;       // stream buffer; 0: sized from the picture (venc_stream_buffer)
	int jpeg_buf_kb = 0;  // JPEG bitstream buffer; 0: sized from the picture (jpeg_stream_buffer)
};

static venc_tuning_t venc_tuning;
static constexpr uint64_t venc_pending_limit_ms = 3000;

static venc_tuning_t load_venc_tuning(void)
{
	venc_tuning_t t;
	FILE *f = fopen("/run/nanokvm-venc-tune", "r");
	if (!f) return t;
	char key[64];
	int value;
	while (fscanf(f, " %63[a-z0-9_] = %d", key, &value) == 2) {
		if (!strcmp(key, "idr_seconds")) t.idr_seconds = value;
		else if (!strcmp(key, "h264_high")) t.h264_high = value;
		else if (!strcmp(key, "min_qp")) t.min_qp = value;
		else if (!strcmp(key, "min_iqp")) t.min_iqp = value;
		else if (!strcmp(key, "max_qp")) t.max_qp = value;
		else if (!strcmp(key, "max_iqp")) t.max_iqp = value;
		else if (!strcmp(key, "max_iprop")) t.max_iprop = value;
		else if (!strcmp(key, "ip_qp_delta")) t.ip_qp_delta = value;
		else if (!strcmp(key, "row_qp_delta")) t.row_qp_delta = value;
		else if (!strcmp(key, "thrd_lv")) t.thrd_lv = value;
		else if (!strcmp(key, "stats")) t.stats = value;
		else if (!strcmp(key, "buf_kb")) t.buf_kb = value;
		else if (!strcmp(key, "jpeg_buf_kb")) t.jpeg_buf_kb = value;
	}
	fclose(f);
	fprintf(stderr, "[kvm_mmf] VENC tuning override: idr_seconds=%d h264_high=%d qp=%d..%d iqp=%d..%d "
		"max_iprop=%d ip_qp_delta=%d row_qp_delta=%d thrd_lv=%d stats=%d buf_kb=%d jpeg_buf_kb=%d\n",
		t.idr_seconds, t.h264_high, t.min_qp, t.max_qp, t.min_iqp, t.max_iqp,
		t.max_iprop, t.ip_qp_delta, t.row_qp_delta, t.thrd_lv, t.stats, t.buf_kb, t.jpeg_buf_kb);
	fflush(stderr);
	return t;
}

/*
 * Video memory shortage. The pools are sized to the measured peak, so their
 * use is near 100 % in normal operation and says nothing about a fault. A
 * shortage shows as failed allocations instead: a VPSS pool that cannot be
 * created or an encoder without its buffers (CVI_ERR_VENC_NOBUF from
 * SendFrame, see platform/modules/osdrv/0004). It is logged once; a gap of
 * 2 s without failures, or one encoded frame, ends it. The capture owner
 * serializes all callers.
 */
static uint64_t video_memory_short_last_ms; // 0: no shortage

static void note_video_memory(bool short_now)
{
	if (!short_now) {
		video_memory_short_last_ms = 0;
		return;
	}
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	const uint64_t now = (uint64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
	if (!video_memory_short_last_ms || now - video_memory_short_last_ms > 2000) {
		fprintf(stderr, "[kvm_mmf] video memory exhausted: a buffer of the video pipeline cannot be allocated\n");
		fflush(stderr);
	}
	video_memory_short_last_ms = now;
}

// Per-second encoder statistics for measurements (venc_tuning.stats).
struct venc_stats_t {
	uint64_t start_us;
	unsigned frames, keyframes;
	uint64_t bytes, key_bytes, max_bytes;
	uint64_t qp_sum, key_qp_sum, mse_sum;
};

static venc_stats_t venc_stats;

static uint64_t venc_now_us(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000 + ts.tv_nsec / 1000;
}

static void venc_stats_frame(const VENC_STREAM_S *stream, const VENC_CHN_STATUS_S *status, int type)
{
	uint64_t bytes = 0;
	bool key = false;
	for (CVI_U32 i = 0; i < stream->u32PackCount; i++) {
		const VENC_PACK_S *pack = &stream->pstPack[i];
		bytes += pack->u32Len - pack->u32Offset;
		if (type == 1 ? (pack->DataType.enH265EType == H265E_NALU_IDRSLICE ||
				pack->DataType.enH265EType == H265E_NALU_ISLICE)
			: (pack->DataType.enH264EType == H264E_NALU_IDRSLICE ||
				pack->DataType.enH264EType == H264E_NALU_ISLICE))
			key = true;
	}
	venc_stats_t &s = venc_stats;
	const uint64_t now = venc_now_us();
	if (s.start_us == 0) s.start_us = now;
	s.frames++;
	s.bytes += bytes;
	s.max_bytes = MAX(s.max_bytes, bytes);
	s.qp_sum += status->stVencStrmInfo.u32MeanQp;
	s.mse_sum += status->stVencStrmInfo.u32MseSum;
	if (key) {
		s.keyframes++;
		s.key_bytes += bytes;
		s.key_qp_sum += status->stVencStrmInfo.u32MeanQp;
	}
	if (now - s.start_us < 1000000) return;
	FILE *f = fopen("/run/nanokvm-venc-stats.tmp", "w");
	if (f) {
		const unsigned deltas = s.frames - s.keyframes;
		fprintf(f, "frames=%u keyframes=%u kbps=%llu key_kb=%llu max_kb=%llu "
			"delta_kb=%.2f qp=%.2f delta_qp=%.2f key_qp=%.2f mse_sum=%llu\n",
			s.frames, s.keyframes,
			(unsigned long long)(s.bytes * 8 * 1000 / (now - s.start_us)),
			(unsigned long long)(s.keyframes ? s.key_bytes / s.keyframes / 1024 : 0),
			(unsigned long long)(s.max_bytes / 1024),
			deltas ? (double)(s.bytes - s.key_bytes) / deltas / 1024 : 0.0,
			(double)s.qp_sum / s.frames,
			deltas ? (double)(s.qp_sum - s.key_qp_sum) / deltas : 0.0,
			s.keyframes ? (double)s.key_qp_sum / s.keyframes : 0.0,
			(unsigned long long)(s.mse_sum / s.frames));
		fclose(f);
		rename("/run/nanokvm-venc-stats.tmp", "/run/nanokvm-venc-stats");
	}
	s = venc_stats_t{};
}

/*
 * Video straight from the capture buffer. The VI writes the HDMI receiver's
 * packed UYVY 4:2:2 frame to DDR, and VPSS only converts it to NV21 for the
 * encoder. The H.265 core reads packed 4:2:2 itself (cvi_vc_driver with
 * nk_packed_src), so a video channel at the input size can take its frames
 * from the VI and skip VPSS: 6 ms less per 1080p frame, 25 ms at 2160p.
 * H.264 takes only 4:2:0: it gets the VI frame when the VI itself writes NV21
 * (4:2:0 capture below). Set while an encoder of the codec that the capture
 * mode serves exists; "0" in /etc/kvm/hevc_vi_direct turns it off. Callers
 * still see NV21: a packed frame is UYVY only inside VIDEO_FRAME_INFO_S, and
 * CPU mappings of VI frames are refused.
 */
static bool vi_direct[MMF_VI_MAX_CHN];        // channel acquires from the VI
static bool vi_frame_from_vi[MMF_VI_MAX_CHN]; // current lease is a VI frame
static int vi_direct_venc = -1;               // the encoder that set it

/*
 * 4:2:0 capture for H.264 (osdrv patch 0013). The CSI bridge has a 4:2:2 to
 * 4:2:0 DMA writer; cv181x_vi.ko enables it for the receiver when its
 * parameter nk_yuv420 is Y as the VI starts. The VI frame is then genuine
 * NV21, so H.264 at the input size takes it directly (1080p30: 8.3 instead of
 * 14.6 ms from the end of a captured frame to the encoded frame, 414 instead
 * of 668 MB/s of DDR traffic), the VPSS group reads NV21 and MJPEG gets 4:2:0
 * chroma. The VI cannot change the mode while it runs: the wanted mode follows
 * the video encoder (_mmf_vi_yuv420_rule), and libkvm restarts the capture
 * when it differs from the running one (mmf_vi_yuv420_pending).
 */
static const char vi_yuv420_param[] = "/sys/module/cv181x_vi/parameters/nk_yuv420";
static bool vi_yuv420_wanted;  // mode for the next capture start
static bool vi_yuv420_running; // mode of the running capture
static bool vi_yuv420_broken;  // the driver did not take the parameter: no restarts for it
static bool vi_yuv420_refused; // a VI frame had another layout: VPSS until the next start

/*
 * A JPEG reader's own lease of a 4:2:0 VI frame. On an output that H.264
 * reads straight from capture, the JPEG encoder takes the same NV21 frame the
 * VI wrote, in a lease of its own: it never waits for the frame the video
 * encoder holds, and the VPSS stays off. The lease lasts one JPEG encode
 * (pop, push, release in one kvmv_read_img call under vi_mutex).
 */
/*
 * MJPEG on its own output encodes outside the capture lock (kvm_vision
 * frame_to_jpeg) and holds this from the JPEG submit to the release of its
 * input frame. Every teardown of the JPEG encoder or of a VI output, which
 * runs under the capture lock, waits for it; the JPEG reader never takes the
 * capture lock while it holds this.
 */
static std::recursive_mutex jpg_hold_mutex;
#define MMF_JPG_TEARDOWN std::lock_guard<std::recursive_mutex> jpg_teardown_guard(jpg_hold_mutex)
void mmf_jpg_hold(void) { jpg_hold_mutex.lock(); }
void mmf_jpg_unhold(void) { jpg_hold_mutex.unlock(); }

static VIDEO_FRAME_INFO_S vi_jpeg_frame;
static int vi_jpeg_ch = -1; // the output whose JPEG lease is vi_jpeg_frame
// A channel number may be recycled during the JPEG hold -> vi_mutex handoff.
// Generations belong to their frame slots and never reset on capture restart.
static uint64_t vi_frame_generation[MMF_VI_MAX_CHN];
static thread_local uint64_t own_jpeg_generation;
static thread_local int own_jpeg_channel = -1;

static int _mmf_release_jpeg_lease(void)
{
	if (vi_jpeg_ch < 0) return CVI_SUCCESS;
	const int released = CVI_VI_ReleaseChnFrame(0, 0, &vi_jpeg_frame);
	if (released != CVI_SUCCESS) return released;
	memset(&vi_jpeg_frame, 0, sizeof(vi_jpeg_frame));
	vi_jpeg_ch = -1;
	return CVI_SUCCESS;
}
static bool vpss_rate_thins;   // the last known stream rate is below the input rate
// A lower rate for one output only (0: none), such as MJPEG's own output
// next to a video stream. It never counts as thinning the video stream.
static int vpss_chn_max_fps[MMF_VI_MAX_CHN];

// The setting: absent or anything but "0" is on.
static bool _vi_direct_setting_on(void)
{
	FILE *setting = fopen("/etc/kvm/hevc_vi_direct", "r");
	if (!setting) return true;
	const bool off = fgetc(setting) == '0';
	fclose(setting);
	return !off;
}

static bool _mmf_vi_yuv420_supported(void)
{
	FILE *f = fopen(vi_yuv420_param, "r");
	if (!f) return false;
	fclose(f);
	return true;
}

// The codec that takes VI frames in the running mode: H.264 (2) in 4:2:0
// capture, H.265 (1) otherwise.
static int _mmf_vi_direct_type(void)
{
	return vi_yuv420_running ? 2 : 1;
}

static bool _vi_direct_supported(void)
{
	if (!_vi_direct_setting_on()) return false;
	if (vi_yuv420_running) return !vi_yuv420_refused;
	FILE *f = fopen("/sys/module/cvi_vc_driver/parameters/nk_packed_src", "r");
	if (!f) return false;
	int value = 0;
	const bool ok = fscanf(f, "%d", &value) == 1 && value == 1;
	fclose(f);
	return ok;
}

/*
 * The mode rule, the only place that decides it. 4:2:0 capture while an
 * H.264 encoder takes the whole input at the input rate from an NV21 output
 * without mirror or flip and the setting allows direct capture. Evaluated when a video encoder is created and
 * once a second while one exists (setting, rate), never for a JPEG or CPU
 * reader: with no video encoder the mode stays. Applied at the next capture
 * start (_mmf_vi_yuv420_start), which also checks the new input width.
 */
static bool _mmf_vi_yuv420_rule(const mmf_venc_cfg_t *cfg)
{
	if (cfg->type != 2 || vi_yuv420_broken || vpss_rate_thins) return false;
	const SIZE_S vi = priv.vi_size;
	if ((int)vi.u32Width != cfg->w || (int)vi.u32Height != cfg->h) return false;
	bool output = false;
	for (int ch = 0; ch < MMF_VI_MAX_CHN && !output; ++ch) {
		VPSS_CHN_ATTR_S attr;
		output = priv.vi_chn_is_inited[ch] && CVI_VPSS_GetChnAttr(0, ch, &attr) == CVI_SUCCESS
			&& attr.enPixelFormat == PIXEL_FORMAT_NV21 && !attr.bMirror && !attr.bFlip
			&& attr.u32Width == vi.u32Width && attr.u32Height == vi.u32Height;
	}
	return output && _vi_direct_setting_on() && _mmf_vi_yuv420_supported();
}

static void _mmf_vi_yuv420_follow(const mmf_venc_cfg_t *cfg)
{
	const bool want = _mmf_vi_yuv420_rule(cfg);
	if (want != vi_yuv420_wanted) {
		fprintf(stderr, "[kvm_mmf] VI capture mode for codec %d at %dx%d: %s\n",
			cfg->type, cfg->w, cfg->h, want ? "4:2:0" : "4:2:2");
		fflush(stderr);
	}
	vi_yuv420_wanted = want;
}

// At every capture start, before the VI starts streaming: write the wanted
// mode to the driver and read back what the VI will do.
static void _mmf_vi_yuv420_start(SIZE_S size)
{
	vi_yuv420_running = false;
	vi_yuv420_refused = false;
	if (!_mmf_vi_yuv420_supported()) {
		vi_yuv420_wanted = false;
		return;
	}
	const bool on = vi_yuv420_wanted && !vi_yuv420_broken && _vi_direct_setting_on();
	FILE *f = fopen(vi_yuv420_param, "w");
	bool written = f && fputs(on ? "Y" : "N", f) >= 0;
	if (f && fclose(f) != 0) written = false;
	int value = EOF;
	f = fopen(vi_yuv420_param, "r");
	if (f) {
		value = fgetc(f);
		fclose(f);
	}
	vi_yuv420_running = value == 'Y';
	if (!written || vi_yuv420_running != on) {
		fprintf(stderr, "[kvm_mmf] VI 4:2:0 parameter not applied (wanted %s, reads %c): "
			"capture mode stays as the driver has it\n", on ? "Y" : "N", value == EOF ? '?' : value);
		vi_yuv420_broken = true;
	}
	vi_yuv420_wanted = vi_yuv420_running;
	fprintf(stderr, "[kvm_mmf] VI capture %s (%ux%u)\n",
		vi_yuv420_running ? "4:2:0 (NV21)" : "4:2:2 (packed UYVY)", size.u32Width, size.u32Height);
	fflush(stderr);
}

/*
 * The 4:2:0 frame as the VI reports it: base_get_frame_info lays out its NV21
 * channel with DEFAULT_ALIGN (64): the Y plane at a pitch of the width rounded
 * up to 64, the VU plane at the same pitch right after the Y plane's length.
 * VI patch 0013 makes the writer use that pitch too (1088 for a 1080 wide
 * portrait). Anything else is refused (_mmf_vi_yuv420_refuse).
 */
static bool _mmf_vi_nv21_layout_ok(const VIDEO_FRAME_S *v)
{
	return v->enPixelFormat == PIXEL_FORMAT_NV21 && v->u64PhyAddr[0]
		&& v->u32Width && v->u32Height && v->u32Width % 2 == 0 && v->u32Height % 2 == 0
		&& v->u32Stride[0] == (v->u32Width + DEFAULT_ALIGN - 1) / DEFAULT_ALIGN * DEFAULT_ALIGN
		&& v->u32Stride[1] == v->u32Stride[0]
		&& (uint64_t)v->u32Stride[0] * v->u32Height <= v->u32Length[0]
		&& (uint64_t)v->u32Stride[1] * (v->u32Height / 2) <= v->u32Length[1]
		&& v->u64PhyAddr[1] == v->u64PhyAddr[0] + v->u32Length[0]
		&& (uint64_t)v->u32Length[0] + v->u32Length[1] <= INT32_MAX;
}

int mmf_vi_yuv420_active(void)
{
	return vi_yuv420_running;
}

int mmf_vi_yuv420_pending(void)
{
	return !vi_yuv420_broken && vi_yuv420_wanted != vi_yuv420_running;
}

/*
 * Keep the former zero-copy hook's frame lifetime in the MMF implementation.
 * A frame released by the image path stays leased until VENC has consumed it.
 */
static int _mmf_release_vi_frame(int ch)
{
 if (ch < 0 || ch >= MMF_VI_MAX_CHN) return CVI_FAILURE;
 if (!priv.vi_frame_valid[ch]) return CVI_SUCCESS;
 // NV16 is exclusively JPEG-owned. Its reader may release outside vi_mutex
 // under jpg_hold_mutex; video encoder state belongs to vi_mutex, so do not
 // inspect it here. NV21/packed leases retain the encoder ownership guard.
 if (priv.vi_frame[ch].stVFrame.enPixelFormat != PIXEL_FORMAT_NV16) {
  for (int encoder = 0; encoder < MMF_VENC_MAX_CHN; ++encoder) {
   if (priv.venc[encoder].is_running && priv.venc_input_vi_ch[encoder] == ch)
    return CVI_ERR_VENC_BUSY;
  }
 }
 VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
 if (priv.vi_frame_mapped[ch] && frame->stVFrame.pu8VirAddr[0]) {
  uint32_t image_size = frame->stVFrame.u32Length[0]
   + frame->stVFrame.u32Length[1] + frame->stVFrame.u32Length[2];
  const int unmapped = CVI_SYS_Munmap(frame->stVFrame.pu8VirAddr[0], image_size);
  if (unmapped != CVI_SUCCESS) return unmapped;
  frame->stVFrame.pu8VirAddr[0] = NULL;
  priv.vi_frame_mapped[ch] = false;
 }
 const int released = vi_frame_from_vi[ch] ? CVI_VI_ReleaseChnFrame(0, 0, frame)
  : CVI_VPSS_ReleaseChnFrame(0, ch, frame);
 if (released != CVI_SUCCESS) {
  SAMPLE_PRT("%s failed for ch %d: %#x\n", vi_frame_from_vi[ch]
   ? "CVI_VI_ReleaseChnFrame" : "CVI_VPSS_ReleaseChnFrame", ch, released);
  return released;
 }
 memset(frame, 0, sizeof(*frame));
 vi_frame_from_vi[ch] = false;
 priv.vi_frame_valid[ch] = false;
 priv.vi_frame_mapped[ch] = false;
 priv.vi_frame_deferred[ch] = false;
 return CVI_SUCCESS;
}

// A lower stream rate is VPSS frame-rate control on the output
// (mmf_vpss_set_rate); the VI delivers every frame, so such an output stays
// on VPSS.
static bool _vpss_thins(const VPSS_CHN_ATTR_S *attr)
{
	return attr->stFrameRate.s32DstFrameRate > 0
		&& attr->stFrameRate.s32DstFrameRate < attr->stFrameRate.s32SrcFrameRate;
}

// Turn VI-direct capture on or off for one VPSS output. On: the output must be
// NV21 at the VI size without mirror or flip (VPSS would have applied them);
// its VPSS channel is disabled so a full queue there cannot stall the group.
// The VI channel attributes stay untouched: CVI_VI_SetChnAttr on a running VI
// bound to VPSS stops its frames for good (stand, 2026-10-20). With depth 0,
// CVI_VI_GetChnFrame still returns the next finished frame (base snap job).
static int _mmf_vi_direct_set(int ch, bool on)
{
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || vi_direct[ch] == on) return CVI_SUCCESS;
	VI_CHN_ATTR_S vi = {};
	if (on) {
		if (CVI_VI_GetChnAttr(0, 0, &vi) != CVI_SUCCESS) return CVI_FAILURE;
		VPSS_CHN_ATTR_S attr;
		// A VPSS frame leased now stays tagged as such and goes back to VPSS.
		if (!priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch] || priv.vi_chn_disabled[ch]
			|| CVI_VPSS_GetChnAttr(0, ch, &attr) != CVI_SUCCESS
			|| attr.enPixelFormat != PIXEL_FORMAT_NV21 || attr.bMirror || attr.bFlip
			|| _vpss_thins(&attr)
			|| attr.u32Width != vi.stSize.u32Width || attr.u32Height != vi.stSize.u32Height)
			return CVI_FAILURE;
		const int result = CVI_VPSS_DisableChn(0, ch);
		if (result != CVI_SUCCESS) return result;
	} else {
		// A VI lease still held by an encoder is released by its source tag.
		if (priv.vi_chn_is_inited[ch] && !priv.vi_chn_stopping[ch] && !priv.vi_chn_disabled[ch]) {
			const int result = CVI_VPSS_EnableChn(0, ch);
			if (result != CVI_SUCCESS) return result;
		}
	}
	vi_direct[ch] = on;
	fprintf(stderr, "[kvm_mmf] %s VI-direct capture %s on output %d (%ux%u)\n",
		vi_yuv420_running ? "H.264" : "H.265", on ? "enabled" : "disabled", ch,
		vi.stSize.u32Width, vi.stSize.u32Height);
	fflush(stderr);
	return CVI_SUCCESS;
}

// The last time a JPEG or CPU reader used an output. Above 1920 pixels MJPEG
// and video share one output, and those readers need its NV21 frames; the
// output stays on VPSS until they have been gone for 2 s.
static uint64_t vi_shared_reader_us[MMF_VI_MAX_CHN];

static int _mmf_vi_direct_yield(int ch)
{
	vi_shared_reader_us[ch] = venc_now_us();
	if (vi_direct[ch]) {
		const int result = _mmf_vi_direct_set(ch, false);
		if (result != CVI_SUCCESS) return result;
		vi_direct_venc = -1;
	}
	return CVI_SUCCESS;
}

// Whether a JPEG reader yields a VI-direct output to VPSS: only in packed
// capture. In 4:2:0 capture every frame is NV21, from the VI (the JPEG
// reader's own lease, mmf_vi_frame_pop_jpeg) or from the VPSS while the
// output is not direct (a video encoder being recreated). A yield then would
// hold the output off the direct path for as long as MJPEG runs.
static bool _mmf_jpeg_reader_yields(void)
{
	return !vi_yuv420_running || vi_yuv420_refused;
}

static void _mmf_vi_direct_for_venc(int venc, const mmf_venc_cfg_t *cfg)
{
	if (cfg->type != _mmf_vi_direct_type() || !_vi_direct_supported()) return;
	const uint64_t now = venc_now_us();
	for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) {
		if (!priv.vi_chn_is_inited[ch]) continue;
		if (vi_shared_reader_us[ch] && now - vi_shared_reader_us[ch] < 2000000) continue;
		VPSS_CHN_ATTR_S attr;
		if (CVI_VPSS_GetChnAttr(0, ch, &attr) != CVI_SUCCESS
			|| (int)attr.u32Width != cfg->w || (int)attr.u32Height != cfg->h) continue;
		if (_mmf_vi_direct_set(ch, true) == CVI_SUCCESS && vi_direct[ch]) {
			vi_direct_venc = venc;
			return;
		}
	}
}

static void _mmf_vi_direct_off_all(void)
{
	bool active = false;
	for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) {
		_mmf_vi_direct_set(ch, false);
		active |= vi_direct[ch];
	}
	if (!active) vi_direct_venc = -1;
}

// Follow a changed setting at most once a second. An encoder keeps the input
// format of its first frame, so its next frame fails and the caller creates
// a new encoder, which starts with the other path (one key frame). The
// capture mode follows the video encoder too (setting, stream rate).
static void _mmf_vi_direct_recheck(void)
{
	static uint64_t last_us;
	const uint64_t now = venc_now_us();
	if (now - last_us < 1000000) return;
	last_us = now;
	for (int ch = 0; ch < MMF_VENC_MAX_CHN; ++ch) {
		if (priv.venc[ch].is_inited && (priv.venc[ch].type == 1 || priv.venc[ch].type == 2)) {
			_mmf_vi_yuv420_follow(&priv.venc[ch].cfg);
			break;
		}
	}
	bool want = _vi_direct_supported();
	for (int ch = 0; want && ch < MMF_VI_MAX_CHN; ++ch) {
		VPSS_CHN_ATTR_S attr;
		if (vi_direct[ch] && CVI_VPSS_GetChnAttr(0, ch, &attr) == CVI_SUCCESS && _vpss_thins(&attr))
			want = false; // a lower stream rate was set meanwhile
	}
	if (!want && vi_direct_venc >= 0) {
		_mmf_vi_direct_off_all();
		return;
	}
	if (!want || vi_direct_venc >= 0) return;
	for (int ch = 0; ch < MMF_VENC_MAX_CHN; ++ch) {
		if (priv.venc[ch].is_inited && priv.venc[ch].type == _mmf_vi_direct_type()) {
			_mmf_vi_direct_for_venc(ch, &priv.venc[ch].cfg);
			return;
		}
	}
}

// A 4:2:0 frame with another layout than expected: log it once per capture
// start, give the frame back and keep the output on the VPSS (which reads the
// same NV21) until the capture restarts.
static void _mmf_vi_yuv420_refuse(int ch, const VIDEO_FRAME_S *v)
{
	if (!vi_yuv420_refused) {
		fprintf(stderr, "[kvm_mmf] VI 4:2:0 frame layout not usable (format %d, %ux%u, pitch %u/%u, "
			"planes %#llx/%#llx, lengths %u/%u): H.264 reads the VPSS output\n",
			v->enPixelFormat, v->u32Width, v->u32Height, v->u32Stride[0], v->u32Stride[1],
			(unsigned long long)v->u64PhyAddr[0], (unsigned long long)v->u64PhyAddr[1],
			v->u32Length[0], v->u32Length[1]);
		fflush(stderr);
	}
	vi_yuv420_refused = true;
	_mmf_release_vi_frame(ch);
	if (_mmf_vi_direct_set(ch, false) == CVI_SUCCESS) vi_direct_venc = -1;
}

static int _mmf_release_all_vi_frames(void)
{
 MMF_JPG_TEARDOWN;
 const int jpeg = _mmf_release_jpeg_lease();
 if (jpeg != CVI_SUCCESS) return jpeg;
 for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) {
  const int result = _mmf_release_vi_frame(ch);
  if (result != CVI_SUCCESS) return result;
 }
 return CVI_SUCCESS;
}

static int _mmf_find_deferred_vi_frame(int width, int height, int format,
	const uint8_t *data, VIDEO_FRAME_INFO_S **frame_out)
{
	for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) {
		if (!priv.vi_frame_valid[ch] || !priv.vi_frame_deferred[ch]) {
			continue;
		}
		VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
		if ((int)frame->stVFrame.u32Width == width
			&& (int)frame->stVFrame.u32Height == height
			&& (int)frame->stVFrame.enPixelFormat == format && frame->stVFrame.pu8VirAddr[0] == data) {
			*frame_out = frame;
			return ch;
		}
	}
	return -1;
}

static int _mmf_acquire_vi_frame(int ch, int *len, int *width, int *height,
	int *format, int timeout_ms = 1000, bool recheck = true)
{
	VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
	_mmf_release_vi_frame(ch);
	if (priv.vi_frame_valid[ch]) return CVI_ERR_VENC_BUSY;
	if (recheck) _mmf_vi_direct_recheck();
	memset(frame, 0, sizeof(*frame));
	if (vi_direct[ch] && vi_yuv420_running) {
		if (CVI_VI_GetChnFrame(0, 0, frame, timeout_ms) != CVI_SUCCESS) return -1;
		// 4:2:0 capture: genuine NV21, passed on as the VI describes it.
		VIDEO_FRAME_S *v = &frame->stVFrame;
		priv.vi_frame_valid[ch] = true;
		++vi_frame_generation[ch];
		priv.vi_frame_mapped[ch] = false;
		priv.vi_frame_deferred[ch] = false;
		vi_frame_from_vi[ch] = true;
		if (_mmf_vi_nv21_layout_ok(v)) {
			*len = (int)(v->u32Length[0] + v->u32Length[1]);
			*width = v->u32Width;
			*height = v->u32Height;
			*format = PIXEL_FORMAT_NV21;
			return 0;
		}
		_mmf_vi_yuv420_refuse(ch, v);
		// Keep a failed release owned, so the next read retries it; the same
		// for a VPSS channel that could not be enabled again.
		if (priv.vi_frame_valid[ch] || vi_direct[ch]) return -1;
		memset(frame, 0, sizeof(*frame));
	}
	if (vi_direct[ch]) {
		if (CVI_VI_GetChnFrame(0, 0, frame, timeout_ms) != CVI_SUCCESS) return -1;
		// The yuv-bypass buffer holds packed UYVY but is labelled NV21.
		VIDEO_FRAME_S *v = &frame->stVFrame;
		v->enPixelFormat = PIXEL_FORMAT_UYVY;
		// The vendor metadata (pitch/length too) describes NV21, not the
		// packed DMA payload. Match ispblk_dma_yuv_bypass_config's
		// DEFAULT_ALIGN pitch rather than treating each row as width*2.
		priv.vi_frame_valid[ch] = true;
		++vi_frame_generation[ch];
		vi_frame_from_vi[ch] = true;
		const uint64_t stride = ((uint64_t)v->u32Width * 2 + DEFAULT_ALIGN - 1)
			/ DEFAULT_ALIGN * DEFAULT_ALIGN;
		const uint64_t bytes = stride * v->u32Height;
		if (!v->u64PhyAddr[0] || !v->u32Width || !v->u32Height || bytes > INT32_MAX) {
			// Keep a failed release owned, so the next read retries it.
			_mmf_release_vi_frame(ch);
			return -1;
		}
		v->u32Stride[0] = (CVI_U32)stride;
		v->u32Stride[1] = v->u32Stride[2] = 0;
		v->u64PhyAddr[1] = v->u64PhyAddr[2] = 0;
		v->u32Length[0] = v->u32Stride[0] * v->u32Height;
		v->u32Length[1] = v->u32Length[2] = 0;
		priv.vi_frame_valid[ch] = true;
		++vi_frame_generation[ch];
		priv.vi_frame_mapped[ch] = false;
		priv.vi_frame_deferred[ch] = false;
		vi_frame_from_vi[ch] = true;
		*len = (int)v->u32Length[0];
		*width = v->u32Width;
		*height = v->u32Height;
		*format = PIXEL_FORMAT_NV21; // what the channel delivers to its callers
		return 0;
	}
	if (CVI_VPSS_GetChnFrame(0, ch, frame, timeout_ms) != CVI_SUCCESS) {
		return -1;
	}

	const uint64_t image_bytes = (uint64_t)frame->stVFrame.u32Length[0]
		+ frame->stVFrame.u32Length[1] + frame->stVFrame.u32Length[2];
	if (frame->stVFrame.u64PhyAddr[0] == 0 || image_bytes == 0 || image_bytes > INT32_MAX) {
		SAMPLE_PRT("invalid VI frame address or size\n");
		CVI_VPSS_ReleaseChnFrame(0, ch, frame);
		memset(frame, 0, sizeof(*frame));
		return -1;
	}

	priv.vi_frame_valid[ch] = true;
	++vi_frame_generation[ch];
	priv.vi_frame_mapped[ch] = false;
	priv.vi_frame_deferred[ch] = false;
	*len = (int)image_bytes;
	*width = frame->stVFrame.u32Width;
	*height = frame->stVFrame.u32Height;
	*format = frame->stVFrame.enPixelFormat;
	return 0;
}

static void *_mmf_map_vi_frame(int ch)
{
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_frame_valid[ch]) {
		return NULL;
	}

	// A VI-direct frame is packed UYVY, or NV21 that the VI owns; CPU paths
	// read the VPSS output (they yield VI-direct first).
	if (vi_frame_from_vi[ch]) return NULL;
	VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
	if (priv.vi_frame_mapped[ch]) {
		return frame->stVFrame.pu8VirAddr[0];
	}

	int image_size = frame->stVFrame.u32Length[0]
		+ frame->stVFrame.u32Length[1]
		+ frame->stVFrame.u32Length[2];
	CVI_VOID *vir_addr = CVI_SYS_MmapCache(frame->stVFrame.u64PhyAddr[0], image_size);
	if (vir_addr == NULL) {
		SAMPLE_PRT("CVI_SYS_MmapCache failed for VI frame\n");
		return NULL;
	}
	// CVI_SYS_MmapCache already invalidates this newly mapped range.
	frame->stVFrame.pu8VirAddr[0] = (CVI_U8 *)vir_addr;
	priv.vi_frame_mapped[ch] = true;
	return vir_addr;
}

static int _create_vb_pool(uint32_t size, uint32_t max_num)
{
	uint32_t pool_id = -1;
	VB_POOL_CONFIG_S stVbPoolCfg;
	stVbPoolCfg.u32BlkCnt = max_num;
	stVbPoolCfg.u32BlkSize = size;
	stVbPoolCfg.enRemapMode = VB_REMAP_MODE_CACHED;

	if (max_num == 0 || size == 0) {
		return -1;
	}

	pool_id = CVI_VB_CreatePool(&stVbPoolCfg);
	if (pool_id == VB_INVALID_POOLID || pool_id >= VB_MAX_COMM_POOLS) {
		return -2;
	}

	priv.vb_pool_used[pool_id] = true;
	return pool_id;
}

static int _destroy_vb_pool(uint32_t pool_id)
{
	if (pool_id == VB_INVALID_POOLID) return CVI_SUCCESS;
	if (pool_id >= VB_MAX_COMM_POOLS) return CVI_FAILURE;
	if (priv.vb_pool_used[pool_id]) {
		if (CVI_VB_DestroyPool(pool_id) != CVI_SUCCESS) {
			printf("CVI_VB_DestroyPool : %d fail!\n", pool_id);
			return -1;
		}
		priv.vb_pool_used[pool_id] = false;
	}

	return 0;
}

static SAMPLE_VI_CONFIG_S g_stViConfig;
static int mmf_vi_deinit(void);
static int mmf_del_vi_channel_all(void);
static int mmf_del_venc_channel_all(void);

static inline CVI_VOID VENC_GetPicBufferConfig2(CVI_U32 u32Width, CVI_U32 u32Height,
	PIXEL_FORMAT_E enPixelFormat, DATA_BITWIDTH_E enBitWidth, COMPRESS_MODE_E enCmpMode,
	VB_CAL_CONFIG_S *pstVbCfg)
{
	CVI_U32 u32AlignWidth = ALIGN(u32Width, VENC_ALIGN_W);
	CVI_U32 u32AlignHeight = u32Height;
	CVI_U32 u32Align = VENC_ALIGN_W;

	COMMON_GetPicBufferConfig(u32AlignWidth, u32AlignHeight, enPixelFormat,
		enBitWidth, enCmpMode, u32Align, pstVbCfg);
}

static VIDEO_FRAME_INFO_S *_mmf_alloc_frame(int id, SIZE_S size, PIXEL_FORMAT_E format)
{
    if (!size.u32Width || !size.u32Height || size.u32Width > UINT16_MAX || size.u32Height > UINT16_MAX)
        return NULL;
    VB_CAL_CONFIG_S layout{};
    VENC_GetPicBufferConfig2(size.u32Width, size.u32Height, format,
        DATA_BITWIDTH_8, COMPRESS_MODE_NONE, &layout);
    return nanokvm::media::allocate(id, size, format, layout);
}

static CVI_S32 _mmf_free_frame(VIDEO_FRAME_INFO_S *frame)
{
    return nanokvm::media::release(frame);
}

static void _mmf_sys_exit(void)
{
	if (g_stViConfig.s32WorkingViNum != 0) {
		SAMPLE_COMM_VI_DestroyIsp(&g_stViConfig);
		SAMPLE_COMM_VI_DestroyVi(&g_stViConfig);
	}
	SAMPLE_COMM_SYS_Exit();
}

static CVI_S32 _mmf_sys_init(SIZE_S stSize)
{
	VB_CONFIG_S	   stVbConf;
	CVI_U32        u32BlkSize, u32BlkRotSize;
	CVI_S32 s32Ret = CVI_SUCCESS;
	COMPRESS_MODE_E    enCompressMode   = COMPRESS_MODE_NONE;

	memset(&stVbConf, 0, sizeof(VB_CONFIG_S));

	// vi
	u32BlkSize = COMMON_GetPicBufferSize(stSize.u32Width, stSize.u32Height, PIXEL_FORMAT_UYVY,
		DATA_BITWIDTH_8, enCompressMode, DEFAULT_ALIGN);
	u32BlkRotSize = COMMON_GetPicBufferSize(stSize.u32Height, stSize.u32Width, PIXEL_FORMAT_UYVY,
		DATA_BITWIDTH_8, enCompressMode, DEFAULT_ALIGN);
	u32BlkSize = MAX(u32BlkSize, u32BlkRotSize);
	stVbConf.astCommPool[MMF_VB_VI_ID].u32BlkSize	= u32BlkSize;
	// The pipeline serializes encoder leases. Two source surfaces
	// retain producer/VPSS overlap.
	stVbConf.astCommPool[MMF_VB_VI_ID].u32BlkCnt = 2;
	stVbConf.astCommPool[MMF_VB_VI_ID].enRemapMode	= VB_REMAP_MODE_CACHED;
	stVbConf.u32MaxPoolCnt = 1;

	s32Ret = SAMPLE_COMM_SYS_Init(&stVbConf);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("system init failed with %#x\n", s32Ret);
		goto error;
	}

	return s32Ret;
error:
	_mmf_sys_exit();
	return s32Ret;
}

static CVI_S32 _mmf_vpss_deinit_new(VPSS_GRP VpssGrp)
{
	CVI_S32 s32Ret = CVI_SUCCESS;

	s32Ret = CVI_VPSS_StopGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("Vpss Stop Grp %d failed! Please check param\n", VpssGrp);
		return CVI_FAILURE;
	}

	s32Ret = CVI_VPSS_DestroyGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("Vpss Destroy Grp %d failed! Please check\n", VpssGrp);
		return CVI_FAILURE;
	}

	return s32Ret;
}

static CVI_S32 _mmf_init(void)
{
	MMF_VERSION_S stVersion;
	SAMPLE_INI_CFG_S	   stIniCfg;
	SAMPLE_VI_CONFIG_S stViConfig;

	PIC_SIZE_E enPicSize;
	SIZE_S stSize;
	CVI_S32 s32Ret = CVI_SUCCESS;

	// Kernel modules are managed by board startup, not by a media client.
	CVI_SYS_GetVersion(&stVersion);
	SAMPLE_PRT("maix multi-media version:%s\n", stVersion.version);

	s32Ret = SAMPLE_COMM_VI_ParseIni(&stIniCfg);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_VI_ParseIni failed with %#x\n", s32Ret);
		return s32Ret;
	}

	//Set sensor number
	CVI_VI_SetDevNum(stIniCfg.devNum);

	/************************************************
	 * step1:  Config VI
	 ************************************************/
	s32Ret = SAMPLE_COMM_VI_IniToViCfg(&stIniCfg, &stViConfig);
	if (s32Ret != CVI_SUCCESS)
		return s32Ret;

	memcpy(&g_stViConfig, &stViConfig, sizeof(SAMPLE_VI_CONFIG_S));

	/************************************************
	 * step2:  Get input size
	 ************************************************/
	s32Ret = SAMPLE_COMM_VI_GetSizeBySensor(stIniCfg.enSnsType[0], &enPicSize);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_VI_GetSizeBySensor failed with %#x "
			"(parsed sensor=%d, expected LT6911=%d)\n", s32Ret,
			(int)stIniCfg.enSnsType[0], (int)LONTIUM_LT6911_2M_60FPS_8BIT);
		return s32Ret;
	}

	s32Ret = SAMPLE_COMM_SYS_GetPicSize(enPicSize, &stSize);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_SYS_GetPicSize failed with %#x\n", s32Ret);
		return s32Ret;
	}

	/************************************************
	 * step3:  Init modules
	 ************************************************/
	// Use the SYS/VB lifecycle APIs; debug dumps do not prove buffer ownership.
	s32Ret = _mmf_sys_init(stSize);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("sys init failed. s32Ret: 0x%x !\n", s32Ret);
		goto _need_exit_sys_and_deinit_vi;
	}

	// The VI reads its capture mode when it starts streaming.
	_mmf_vi_yuv420_start(stSize);
	s32Ret = SAMPLE_PLAT_VI_INIT(&stViConfig);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("vi init failed. s32Ret: 0x%x !\n", s32Ret);
		SAMPLE_PRT("Please try to check if the camera is working.\n");
		goto _need_exit_sys_and_deinit_vi;
	}

	priv.vi_size.u32Width = stSize.u32Width;
	priv.vi_size.u32Height = stSize.u32Height;

	return s32Ret;

_need_exit_sys_and_deinit_vi:
	_mmf_sys_exit();

	return s32Ret;
}

static int _mmf_deinit(void)
{
	/*
	 * Encoders can still own input frames from VI. Tear them down before VI so
	 * those frames are returned while their producer is still alive. JPEG also
	 * owns a separate MMF reference; forced process teardown has already reset
	 * the reference count, so its public deinit can safely release its pools
	 * here without recursively entering this function.
	 */
	const int video_stopped = mmf_del_venc_channel_all();
	if (video_stopped != CVI_SUCCESS) return video_stopped;
	mmf_enc_jpg_deinit(0);
	const int vi_stopped = mmf_del_vi_channel_all();
	if (vi_stopped != CVI_SUCCESS) return vi_stopped;
	if (priv.enc_jpg_destroy_failed) {
		// A JPEG channel whose CVI_VENC_DestroyChn failed: its state was
		// cleared (mmf_enc_jpg_deinit), retry once the encoders are gone.
		const int ch = priv.enc_jpg_destroy_ch;
		CVI_VENC_StopRecvFrame(ch);
		CVI_VENC_ResetChn(ch);
		if (CVI_VENC_DestroyChn(ch) == CVI_SUCCESS) priv.enc_jpg_destroy_failed = false;
	}
	mmf_vi_deinit();
	_mmf_sys_exit();
	return CVI_SUCCESS;
}

static void _mmf_jpg_destroy(int ch)
{
	const CVI_S32 result = CVI_VENC_DestroyChn(ch);
	if (result == CVI_SUCCESS) return;
	printf("CVI_VENC_DestroyChn [%d] failed with %#x\n", ch, result);
	priv.enc_jpg_destroy_failed = true;
	priv.enc_jpg_destroy_ch = ch;
}

static int _vi_get_unused_ch() {
	for (int i = 0; i < MMF_VI_MAX_CHN; i++) {
		if (priv.vi_chn_is_inited[i] == false) {
			return i;
		}
	}
	return -1;
}

/*
 * The VB driver counts inits and frees its pools only when the count returns
 * to zero, but CVI_VB_Exit acts only in the process that initialised it. A
 * process that ended without exiting (stopped or crashed mid-capture) leaves
 * the count up: every later process then gets that process's pools, its own
 * configuration is ignored ("vb has already inited, set_config cmd has no
 * effect"), and a larger capture finds no pool for its frames (2560x1440
 * after a 1920x1080 start: "No valid pool for size(5529600)") until a
 * reboot. As the only media process, bring the count to zero before init.
 */
extern "C" int get_base_fd(void);
extern "C" int vb_ioctl_exit(int fd);
static void _mmf_vb_reset_stale(void)
{
	const int fd = get_base_fd();
	int exits = 0;
	while (fd >= 0 && exits < 16 && CVI_VB_IsInited() != CVI_FALSE && vb_ioctl_exit(fd) == 0) ++exits;
	if (exits) {
		fprintf(stderr, "[kvm_mmf] released %d stale VB reference%s\n", exits, exits == 1 ? "" : "s");
		fflush(stderr);
	}
}

int mmf_init(void)
{
    if (priv.mmf_used_cnt) {
		priv.mmf_used_cnt ++;
        return 0;
    }

	/*
	 * SYS/VB state left by a previous process. SAMPLE_COMM_SYS_Init repeats
	 * these two exits; the explicit call is kept as the documented reset.
	 * VENC and VPSS need no sweep here: a new process has no VENC device
	 * open, so the vendor calls would not reach the driver, and VPSS group 0
	 * is recreated by _mmf_vpss_init_new.
	 */
	SAMPLE_COMM_SYS_Exit();
	_mmf_vb_reset_stale();

    if (_mmf_init() != CVI_SUCCESS) {
        printf("maix multi-media init failed\n");
        return -1;
    } else {
		printf("maix multi-media init ok\n");
	}

	priv.mmf_used_cnt = 1;
    return 0;
}

int mmf_try_deinit(bool force) {
	MMF_JPG_TEARDOWN;
	if (!priv.mmf_used_cnt) {
		return 0;
	}
	const int previous_count = priv.mmf_used_cnt;
	if (!force && --priv.mmf_used_cnt) return 0;
	// JPEG teardown can drop its extra reference recursively. Keep the count
	// zero during teardown, but restore ownership if video could not drain.
	priv.mmf_used_cnt = 0;
	const int result = _mmf_deinit();
	if (result != CVI_SUCCESS) {
		priv.mmf_used_cnt = previous_count;
		return result;
	}
	printf("maix multi-media driver destroyed.\n");
	return CVI_SUCCESS;
}

int mmf_get_vi_unused_channel(void) {
	return _vi_get_unused_ch();
}

/*
 * Colour. The HDMI receivers send limited-range BT.709 YCbCr. VPSS converts
 * its input to RGB with the group matrix and every YUV channel back with the
 * channel's: by default full-range BT.601 both ways, so YUV passed through
 * unchanged, but JPEG, which decoders read as full-range BT.601 (JFIF), came
 * out pale: black at 16, white at 235. With the receiver's matrix at the
 * input the group works in true RGB; the video channels convert back to
 * limited-range BT.709 (VIDEO_FORMAT_LINEAR_BT709, osdrv patch 0010) and
 * NV16, which only JPEG takes, to JFIF. A kernel without that patch keeps
 * the default matrices.
 */
extern "C" CVI_S32 vpss_set_grp_csc(CVI_S32 fd, const struct vpss_grp_csc_cfg *cfg);
static bool _mmf_vpss_true_rgb = true;

static CVI_S32 _mmf_vpss_set_source_colour(VPSS_GRP grp, bool bt709)
{
	// 10-bit fractions, bit 13 the sign. BT.709 limited: 1.1644 (Y - 16)
	// + 1.7927 (Cr - 128); - 0.2132 (Cb - 128) - 0.5329 (Cr - 128);
	// + 2.1124 (Cb - 128). The default, full-range BT.601, is the matrix
	// the kernel sets when it creates the group.
	const uint16_t neg = 1 << 13;
	const uint16_t limited709[3][3] = {{1192, 0, 1836}, {1192, (uint16_t)(neg | 218), (uint16_t)(neg | 546)}, {1192, 2163, 0}};
	const uint16_t full601[3][3] = {{1024, 0, 1436}, {1024, (uint16_t)(neg | 352), (uint16_t)(neg | 731)}, {1024, 1815, 0}};
	struct vpss_grp_csc_cfg cfg = {};
	cfg.VpssGrp = grp;
	for (int i = 0; i < PROC_AMP_MAX; ++i) cfg.proc_amp[i] = 50; // neutral
	memcpy(cfg.coef, bt709 ? limited709 : full601, sizeof(cfg.coef));
	cfg.sub[0] = bt709 ? 16 : 0;
	cfg.sub[1] = cfg.sub[2] = 128;
	const CVI_S32 fd = CVI_VPSS_GetChnFd(grp, 0);
	const CVI_S32 result = fd < 0 ? fd : vpss_set_grp_csc(fd, &cfg);
	if (result != CVI_SUCCESS)
		SAMPLE_PRT("VPSS group %d input colour matrix not set\n", grp);
	return result;
}

// Whether the kernel knows VIDEO_FORMAT_LINEAR_BT709: its /proc/cvitek/vpss
// names a channel in it "BT709" (an older one says "UNKNOWN"). Asked once,
// after a channel has been set to it.
static bool _mmf_vpss_bt709_supported(void)
{
	static int supported = -1;
	if (supported < 0) {
		supported = 0;
		FILE *proc = fopen("/proc/cvitek/vpss", "r");
		char line[256];
		while (proc && fgets(line, sizeof(line), proc))
			if (strstr(line, "BT709")) { supported = 1; break; }
		if (proc) fclose(proc);
		if (!supported) {
			SAMPLE_PRT("kernel without BT.709 VPSS output: default colour matrices\n");
			_mmf_vpss_true_rgb = false;
		}
	}
	return supported;
}

// Keeps the aspect ratio and crops the group input to the output's ratio.
static CVI_S32 _mmf_vpss_chn_init(VPSS_GRP VpssGrp, VPSS_CHN VpssChn, int width, int height, PIXEL_FORMAT_E format, int fps, int depth, bool mirror, bool flip)
{
	VPSS_GRP_ATTR_S stGrpAttr;
	VPSS_CROP_INFO_S   stChnCropInfo;
	VPSS_CHN_ATTR_S chn_attr = {};
	CVI_S32 s32Ret = CVI_SUCCESS;

	s32Ret = CVI_VPSS_GetGrpAttr(VpssGrp, &stGrpAttr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_GetGrpAttr failed. s32Ret: 0x%x !\n", s32Ret);
		return s32Ret;
	}
	CVI_FLOAT corp_scale_w = (CVI_FLOAT)stGrpAttr.u32MaxW / width;
	CVI_FLOAT corp_scale_h = (CVI_FLOAT)stGrpAttr.u32MaxH / height;
	chn_attr.u32Width                    = width;
	chn_attr.u32Height                   = height;
	// Only JPEG takes NV16, and JPEG decoders read full-range BT.601
	// (LINEAR); the video encoders keep the receiver's limited BT.709
	// (mmf_vi_chn_set_jfif switches NV21 while only MJPEG reads it).
	chn_attr.enVideoFormat               = format == PIXEL_FORMAT_NV16 || !_mmf_vpss_true_rgb
		? VIDEO_FORMAT_LINEAR : VIDEO_FORMAT_LINEAR_BT709;
	chn_attr.enPixelFormat               = format;
	chn_attr.stFrameRate.s32SrcFrameRate = fps;
	chn_attr.stFrameRate.s32DstFrameRate = fps;
	chn_attr.u32Depth                    = depth;
	chn_attr.bMirror                     = mirror;
	chn_attr.bFlip                       = flip;
	chn_attr.stAspectRatio.enMode        = ASPECT_RATIO_AUTO;
	chn_attr.stAspectRatio.bEnableBgColor = CVI_TRUE;
	chn_attr.stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
	chn_attr.stNormalize.bEnable         = CVI_FALSE;

	CVI_U32 crop_w = corp_scale_w < corp_scale_h ? width * corp_scale_w: width * corp_scale_h;
	CVI_U32 crop_h = corp_scale_w < corp_scale_h ? height * corp_scale_w: height * corp_scale_h;
	if (corp_scale_h < 0 || corp_scale_w < 0) {
		SAMPLE_PRT("crop scale error. corp_scale_w: %f, corp_scale_h: %f\n", corp_scale_w, corp_scale_h);
		return -1;
	}

	stChnCropInfo.bEnable = true;
	stChnCropInfo.stCropRect.s32X = (stGrpAttr.u32MaxW - crop_w) / 2;
	stChnCropInfo.stCropRect.s32Y = (stGrpAttr.u32MaxH - crop_h) / 2;
	stChnCropInfo.stCropRect.u32Width = crop_w;
	stChnCropInfo.stCropRect.u32Height = crop_h;

	if (crop_w != 0 && crop_h != 0) {
		s32Ret = CVI_VPSS_SetChnCrop(VpssGrp, VpssChn, &stChnCropInfo);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("set vpss group crop failed. s32Ret: 0x%x !\n", s32Ret);
			return -1;
		}
	}

	s32Ret = CVI_VPSS_SetChnAttr(VpssGrp, VpssChn, &chn_attr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_SetChnAttr failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}
	if (chn_attr.enVideoFormat == VIDEO_FORMAT_LINEAR_BT709 && !_mmf_vpss_bt709_supported()) {
		// A kernel without the BT.709 channel output: back to the default
		// matrices, so the video keeps its colours.
		s32Ret = _mmf_vpss_set_source_colour(VpssGrp, false);
		if (s32Ret != CVI_SUCCESS)
			return s32Ret;
		chn_attr.enVideoFormat = VIDEO_FORMAT_LINEAR;
		s32Ret = CVI_VPSS_SetChnAttr(VpssGrp, VpssChn, &chn_attr);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("CVI_VPSS_SetChnAttr failed with %#x\n", s32Ret);
			return s32Ret;
		}
	}

	s32Ret = CVI_VPSS_EnableChn(VpssGrp, VpssChn);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_EnableChn failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}

	return s32Ret;
}

static CVI_S32 _mmf_vpss_chn_deinit(VPSS_GRP VpssGrp, VPSS_CHN VpssChn)
{
	return CVI_VPSS_DisableChn(VpssGrp, VpssChn);
}

static CVI_S32 _mmf_vpss_init_new(VPSS_GRP VpssGrp, CVI_U32 width, CVI_U32 height, PIXEL_FORMAT_E format)
{
	VPSS_GRP_ATTR_S    stVpssGrpAttr;
	CVI_S32 s32Ret = CVI_SUCCESS;

	memset(&stVpssGrpAttr, 0, sizeof(VPSS_GRP_ATTR_S));
	stVpssGrpAttr.stFrameRate.s32SrcFrameRate    = -1;
	stVpssGrpAttr.stFrameRate.s32DstFrameRate    = -1;
	stVpssGrpAttr.enPixelFormat                  = format;
	stVpssGrpAttr.u32MaxW                        = width;
	stVpssGrpAttr.u32MaxH                        = height;
	stVpssGrpAttr.u8VpssDev                      = 0;

	s32Ret = CVI_VPSS_CreateGrp(VpssGrp, &stVpssGrpAttr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_CreateGrp(grp:%d) retry(%#x)!\n", VpssGrp, s32Ret);
		CVI_VPSS_DestroyGrp(VpssGrp);

		s32Ret = CVI_VPSS_CreateGrp(VpssGrp, &stVpssGrpAttr);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("CVI_VPSS_CreateGrp(grp:%d) failed with %#x!\n", VpssGrp, s32Ret);
			return CVI_FAILURE;
		}
	}

	s32Ret = CVI_VPSS_ResetGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_ResetGrp(grp:%d) failed with %#x!%d\n", VpssGrp, s32Ret, CVI_ERR_VPSS_ILLEGAL_PARAM);
		CVI_VPSS_DestroyGrp(VpssGrp);
		return CVI_FAILURE;
	}
	s32Ret = _mmf_vpss_set_source_colour(VpssGrp, _mmf_vpss_true_rgb);
	if (s32Ret != CVI_SUCCESS) {
		CVI_VPSS_DestroyGrp(VpssGrp);
		return s32Ret;
	}

	s32Ret = CVI_VPSS_StartGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_StartGrp failed with %#x\n", s32Ret);
		// The caller leaves vi_is_inited false, so teardown would not see it.
		CVI_VPSS_DestroyGrp(VpssGrp);
		return CVI_FAILURE;
	}
	return s32Ret;
}

int mmf_vi_init(void)
{
	if (priv.vi_is_inited) {
		return 0;
	}

	CVI_S32 s32Ret = CVI_SUCCESS;
	// The group reads what the VI writes: packed UYVY, or NV21 in 4:2:0 capture.
	s32Ret = _mmf_vpss_init_new(0, priv.vi_size.u32Width, priv.vi_size.u32Height,
		vi_yuv420_running ? PIXEL_FORMAT_NV21 : PIXEL_FORMAT_UYVY);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_init_new failed. s32Ret: 0x%x !\n", s32Ret);
		priv.vi_is_inited = false;
		return s32Ret;
	}

	priv.vi_is_inited = true;

	return s32Ret;
}

static int mmf_vi_deinit(void)
{
	if (!priv.vi_is_inited) {
		return 0;
	}

	const int released = _mmf_release_all_vi_frames();
	if (released != CVI_SUCCESS) return released;
	CVI_S32 s32Ret = CVI_SUCCESS;
	s32Ret = _mmf_vpss_deinit_new(0);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_deinit_new failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}

	priv.vi_is_inited = false;

	return s32Ret;
}

static int _mmf_add_vi_channel(int ch, int width, int height, int format, int buffers = 4, int queue_depth = 2) {
 if (buffers < 2 || buffers > 4 || queue_depth < 1 || queue_depth >= buffers) return CVI_FAILURE;
 if (ch < 0 || ch >= MMF_VI_MAX_CHN) return CVI_FAILURE;
 // Do not add a producer while a previous channel still owns cleanup stages.
 for (int i = 0; i < MMF_VI_MAX_CHN; ++i)
  if (priv.vi_chn_stopping[i]) return CVI_ERR_VENC_BUSY;
	uint32_t pool_size_out = 0;
	int pool_id = -1;
	// VI has one source channel. The index here selects a VPSS output, not VI.
	bool another_output = false;
	for (int i = 0; i < MMF_VI_MAX_CHN; ++i) another_output |= priv.vi_chn_is_inited[i];

	if (!priv.mmf_used_cnt) {
		printf("%s: maix multi-media or vi not inited\n", __func__);
		return -1;
	}

	if (!priv.vi_is_inited) {
		if (0 != mmf_vi_init()) {
			printf("mmf_vi_init failed!\r\n");
			return -1;
		}
	}

	if (width <= 0 || height <= 0) {
		printf("invalid width or height\n");
		return -1;
	}

	if (format != PIXEL_FORMAT_NV21 && format != PIXEL_FORMAT_NV16) {
		printf("invalid format\n");
		return -1;
	}

	if (priv.vi_chn_is_inited[ch]) {
		printf("vi ch %d already open\n", ch);
		return -1;
	}

	CVI_S32 s32Ret = CVI_SUCCESS;
	// Keep active geometry exact; VB allocation and VIDEO_FRAME strides own padding.
	const int width_out = width;
	const int height_out = height;
	const int fps = nanokvm::capture_rate_limit(width_out, height_out);
	int depth = queue_depth;
	PIXEL_FORMAT_E format_out = (PIXEL_FORMAT_E)format;
	bool mirror = !g_priv.vi_hmirror[ch];
	bool flip = !g_priv.vi_vflip[ch];
	s32Ret = _mmf_vpss_chn_deinit(0, ch);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_chn_deinit failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}

	s32Ret = _mmf_vpss_chn_init(0, ch, width_out, height_out, format_out, fps, depth, mirror, flip);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_chn_init failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}

 // Channel initialization succeeded: retain ownership even if binding,
 // pool creation or attachment later fails. The delete path owns rollback.
 priv.vi_chn_is_inited[ch] = true;
 priv.vi_chn_stopping[ch] = true;
 priv.vi_chn_disabled[ch] = false;
 priv.vi_chn_pool_id[ch] = VB_INVALID_POOLID;
 s32Ret = another_output ? CVI_SUCCESS : SAMPLE_COMM_VI_Bind_VPSS(0, 0, 0);
 if (s32Ret == CVI_SUCCESS) priv.vi_source_bound = true;
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("vi bind vpss failed. s32Ret: 0x%x !\n", s32Ret);
		goto _need_deinit_vpss_chn;
	}

	pool_size_out = COMMON_GetPicBufferSize(width_out, height_out, format_out, DATA_BITWIDTH_8, COMPRESS_MODE_NONE, DEFAULT_ALIGN);
	// Four surfaces cover two queued outputs, the encoder lease and the producer.
	pool_id = _create_vb_pool(pool_size_out, buffers);
	if (pool_id < 0 && buffers > 3) {
		pool_id = _create_vb_pool(pool_size_out, 3);
	}
	if (pool_id < 0 && buffers > 2) {
		// Keep capture usable on profiles with a smaller reserved video heap.
		pool_id = _create_vb_pool(pool_size_out, 2);
	}
	if (pool_id < 0) {
		printf("[%s][%d]_create_vb_pool failed, id %d\n", __func__, __LINE__, pool_id);
		note_video_memory(true);
		goto _need_deinit_vpss_chn;
	}

	priv.vi_chn_pool_id[ch] = pool_id;
	s32Ret = CVI_VPSS_AttachVbPool(0, ch, pool_id);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_AttachVbPool failed. s32Ret: 0x%x !\n", s32Ret);
		goto _need_deinit_vpss_chn;
	}

 priv.vi_chn_stopping[ch] = false;
 return CVI_SUCCESS;
_need_deinit_vpss_chn:
 // Failed rollback remains visible through the channel owner and is retried
 // by full cleanup. Never make that slot reusable before all stages succeed.
 mmf_del_vi_channel(ch);
 return CVI_FAILURE;
}

int mmf_add_vi_channel_configured(int ch, int width, int height, int format, int buffers, int queue_depth) {
    const int result = _mmf_add_vi_channel(ch, width, height, format, buffers, queue_depth);
    if (!result) { priv.vi_buffers[ch] = buffers; priv.vi_queue_depth[ch] = queue_depth; }
    return result;
}

int mmf_del_vi_channel(int ch) {
 MMF_JPG_TEARDOWN;
 if (ch < 0 || ch >= MMF_VI_MAX_CHN) return CVI_FAILURE;
 if (!priv.vi_chn_is_inited[ch]) return CVI_SUCCESS;
 priv.vi_chn_stopping[ch] = true;
 int result = _mmf_release_vi_frame(ch);
 if (result != CVI_SUCCESS) return result;
 if (vi_direct[ch]) {
  vi_direct[ch] = false; // the VPSS channel is torn down below anyway
  vi_direct_venc = -1;
 }
 vpss_chn_max_fps[ch] = 0; // a reopened channel may feed video
 // VI has one source; it stays bound while another VPSS output is open.
 bool another_output = false;
 for (int i = 0; i < MMF_VI_MAX_CHN; ++i)
  if (i != ch) another_output |= priv.vi_chn_is_inited[i];
 if (!another_output && priv.vi_source_bound) {
  result = SAMPLE_COMM_VI_UnBind_VPSS(0, 0, 0);
  if (result != CVI_SUCCESS) return result;
  priv.vi_source_bound = false;
 }
 if (!priv.vi_chn_disabled[ch]) {
  result = _mmf_vpss_chn_deinit(0, ch);
  if (result != CVI_SUCCESS) return result;
  priv.vi_chn_disabled[ch] = true;
 }
 result = _destroy_vb_pool(priv.vi_chn_pool_id[ch]);
 if (result != CVI_SUCCESS) return result;
 priv.vi_chn_pool_id[ch] = VB_INVALID_POOLID;
 priv.vi_chn_is_inited[ch] = false;
 priv.vi_chn_stopping[ch] = false;
 priv.vi_chn_disabled[ch] = false;
 return CVI_SUCCESS;
}

static int mmf_del_vi_channel_all(void) {
	for (int i = 0; i < MMF_VI_MAX_CHN; i++) {
		if (priv.vi_chn_is_inited[i] == true) {
			const int result = mmf_del_vi_channel(i);
			if (result != CVI_SUCCESS) return result;
		}
	}
	return 0;
}

bool mmf_vi_chn_is_open(int ch) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		return false;
	}

	return priv.vi_chn_is_inited[ch] && !priv.vi_chn_stopping[ch];
}

int mmf_reset_vi_channel(int ch, int width, int height, int format)
{
	MMF_JPG_TEARDOWN;
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) return CVI_FAILURE;
    const int buffers = priv.vi_buffers[ch], depth = priv.vi_queue_depth[ch];
	const int result = mmf_del_vi_channel(ch);
	if (result != CVI_SUCCESS) return result;
	return mmf_add_vi_channel_configured(ch, width, height, format, buffers, depth);
}

int mmf_vi_frame_pop_nv21(int ch, mmf_nv21_view_t *view)
{
    if (!view || ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]) return -1;
    if (_mmf_vi_direct_yield(ch) != CVI_SUCCESS) return -1; // CPU readers need NV21
    int len = 0, width = 0, height = 0, format = 0;
    if (_mmf_acquire_vi_frame(ch, &len, &width, &height, &format) != 0) return -1;
    const VIDEO_FRAME_S *frame = &priv.vi_frame[ch].stVFrame;
    // _mmf_map_vi_frame maps contiguous VB storage. Check that contract before
    // exposing its chroma plane, and retain the actual strides/plane offset.
    const bool valid = format == PIXEL_FORMAT_NV21 && width > 0 && height > 0
        && width % 2 == 0 && height % 2 == 0
        && frame->u32Stride[0] >= (CVI_U32)width && frame->u32Stride[1] >= (CVI_U32)width
        && (CVI_U64)frame->u32Stride[0] * height <= frame->u32Length[0]
        && (CVI_U64)frame->u32Stride[1] * (height / 2) <= frame->u32Length[1]
        && frame->u64PhyAddr[0] <= UINT64_MAX - frame->u32Length[0]
        && frame->u64PhyAddr[1] == frame->u64PhyAddr[0] + frame->u32Length[0]
        && frame->u32Length[2] == 0;
    if (!valid) {
        _mmf_release_vi_frame(ch);
        return -1;
    }
    uint8_t *data = (uint8_t *)_mmf_map_vi_frame(ch);
    if (!data) {
        _mmf_release_vi_frame(ch);
        return -1;
    }
    *view = {data, (uint32_t)len, (uint32_t)width, (uint32_t)height,
             frame->u32Stride[0], frame->u32Stride[1], frame->u32Length[0]};
    return 0;
}

int mmf_vi_chn_set_enabled(int ch, int enable) {
    if (!mmf_vi_chn_is_open(ch)) return CVI_FAILURE;
    if (vi_direct[ch]) return CVI_SUCCESS; // its VPSS channel stays off meanwhile
    return enable ? CVI_VPSS_EnableChn(0, ch) : CVI_VPSS_DisableChn(0, ch);
}

int mmf_vi_drop_pending(int ch) {
    // A JPEG reader can own a frame slot outside vi_mutex. Do not inspect or
    // drain that slot concurrently, and do not stall video waiting for JPEG.
    std::unique_lock<std::recursive_mutex> jpeg(jpg_hold_mutex, std::try_to_lock);
    if (!jpeg.owns_lock()) return MMF_VI_OUTPUT_LEASED;
    if (ch < 0 || ch >= MMF_VI_MAX_CHN || !mmf_vi_chn_is_open(ch)) return CVI_FAILURE;
    // Never replace a DMA lease still referenced by an encoder. That is not a
    // failure: the leasing consumer is mid-frame and releases it itself.
    if (priv.vi_frame_valid[ch]) return MMF_VI_OUTPUT_LEASED;
    VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
    // An output straight from capture queues nothing (VI depth 0: a read
    // takes the next finished frame). Taking one here only threw away the
    // video stream's next frame, once per MJPEG read, and an empty read logs
    // a VI error each time.
    if (vi_direct[ch]) return CVI_SUCCESS;
    const int result = CVI_VPSS_GetChnFrame(0, ch, frame, 0);
    if (result == CVI_ERR_VPSS_BUF_EMPTY) return CVI_SUCCESS;
    if (result != CVI_SUCCESS) return result;
    priv.vi_frame_valid[ch] = true;
    ++vi_frame_generation[ch];
    priv.vi_frame_mapped[ch] = false;
    priv.vi_frame_deferred[ch] = false;
    return _mmf_release_vi_frame(ch);
}

int mmf_vi_frame_pop_native(int ch, int *len, int *width, int *height, int *format) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]) {
		return -1;
	}
	if (len == NULL || width == NULL || height == NULL || format == NULL) {
		printf("invalid param\n");
		return -1;
	}
	if (_mmf_acquire_vi_frame(ch, len, width, height, format) != 0) {
		return -1;
	}
	priv.vi_frame_deferred[ch] = true;
	return 0;
}

int mmf_vi_frame_pop_jpeg(int ch, int *len, int *width, int *height, int *format) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]
		|| len == NULL || width == NULL || height == NULL || format == NULL) {
		return -1;
	}
	_mmf_vi_direct_recheck();
	if (!vi_direct[ch] || !vi_yuv420_running || vi_yuv420_refused)
		return mmf_vi_frame_pop_native(ch, len, width, height, format);
	if (_mmf_release_jpeg_lease() != CVI_SUCCESS) return -1;
	if (CVI_VI_GetChnFrame(0, 0, &vi_jpeg_frame, 1000) != CVI_SUCCESS) {
		memset(&vi_jpeg_frame, 0, sizeof(vi_jpeg_frame));
		return -1;
	}
	vi_jpeg_ch = ch;
	const VIDEO_FRAME_S *v = &vi_jpeg_frame.stVFrame;
	if (!_mmf_vi_nv21_layout_ok(v)) {
		_mmf_release_jpeg_lease();
		return -1;
	}
	*len = (int)(v->u32Length[0] + v->u32Length[1]);
	*width = v->u32Width;
	*height = v->u32Height;
	*format = PIXEL_FORMAT_NV21;
	return 0;
}

int mmf_vi_frame_pop_own(int ch, int *len, int *width, int *height, int *format) {
	// Outside the capture lock: only this output's frame slot, no shared
	// direct-path state (the recheck runs on the capture lock's reads).
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]
		|| vi_direct[ch] || len == NULL || width == NULL || height == NULL || format == NULL) {
		return -1;
	}
	if (_mmf_acquire_vi_frame(ch, len, width, height, format, 1000, false) != 0) return -1;
	priv.vi_frame_deferred[ch] = true;
	own_jpeg_channel = ch;
	own_jpeg_generation = vi_frame_generation[ch];
	return 0;
}

int mmf_vi_frame_leased(int ch) {
	return ch >= 0 && ch < MMF_VI_MAX_CHN && ch == own_jpeg_channel
		&& vi_frame_generation[ch] == own_jpeg_generation
		&& priv.vi_frame_valid[ch] && priv.vi_frame_deferred[ch];
}

int mmf_vi_frame_try_pop_native(int ch, int timeout_ms, int *len, int *width, int *height, int *format) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]
		|| len == NULL || width == NULL || height == NULL || format == NULL) {
		return -1;
	}
	if (_mmf_acquire_vi_frame(ch, len, width, height, format, timeout_ms) != 0) {
		return -1;
	}
	priv.vi_frame_deferred[ch] = true;
	return 0;
}

void mmf_vi_frame_free(int ch) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_frame_valid[ch]) {
		return;
	}
	priv.vi_frame_deferred[ch] = true;
}

void mmf_vi_frame_release(int ch) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		return;
	}
	if (vi_jpeg_ch == ch) {
		_mmf_release_jpeg_lease();
		return;
	}
	_mmf_release_vi_frame(ch);
}

/*
 * The JPEG bitstream buffer. Without a size the driver allocates 9 MiB of
 * the video heap for any picture (LARGE_STREAM_BUF_SIZE, 4096x2304), while
 * a 2560x1440 frame of a desktop is well under 1 MiB. Half a byte per pixel
 * (1.76 MiB at 2560x1440, 1 MiB at 1920x1080) holds every frame seen; a
 * larger one is not lost: on INT_JPU_BIT_BUF_FULL the driver saves the
 * filled part and continues (cviJpgEncBufFullSaveStream).
 */
static CVI_U32 jpeg_stream_buffer(int w, int h)
{
	const venc_tuning_t t = load_venc_tuning();
	CVI_U32 bytes = t.jpeg_buf_kb > 0 ? (CVI_U32)t.jpeg_buf_kb << 10 : (CVI_U32)w * (CVI_U32)h / 2;
	if (t.jpeg_buf_kb <= 0) bytes = MAX(bytes, 512u << 10);
	return (bytes + 0x3ff) & ~0x3ffu; // the driver needs a multiple of 1 KiB
}

// The encoder takes Qfactor 50 as the mark of a custom quantization table
// and refuses it (Q_TABLE_CUSTOM in cvi_venc.c): a failed frame, which the
// MJPEG reader took for a broken 4:2:2 path and fell back to 4:2:0 until a
// restart. The adaptive MJPEG quality passes 50 on its way down; 51 looks
// the same.
static int _mmf_jpg_qfactor(int quality)
{
	return quality == 50 ? 51 : quality;
}

static int mmf_enc_jpg_init(int ch, int w, int h, int format, int quality)
{
	if (priv.enc_jpg_is_init)
		return 0;

    if (ch < 0 || ch >= MMF_VENC_MAX_CHN || w <= 0 || h <= 0
        || w > UINT16_MAX || h > UINT16_MAX || quality < 20 || quality > 99
        || (format != PIXEL_FORMAT_NV21 && format != PIXEL_FORMAT_NV16) || w % 2 || h % 2) {
        printf("Invalid JPEG channel, size, format or quality\n");
		return -1;
	}

	if (mmf_init()) {
		return -1;
	}

	CVI_S32 s32Ret = CVI_SUCCESS;

	VENC_CHN_ATTR_S stVencChnAttr;
	memset(&stVencChnAttr, 0, sizeof(VENC_CHN_ATTR_S));
	stVencChnAttr.stVencAttr.enType = PT_JPEG;
	stVencChnAttr.stVencAttr.u32BufSize = jpeg_stream_buffer(w, h);
	stVencChnAttr.stVencAttr.u32MaxPicWidth = w;
	stVencChnAttr.stVencAttr.u32MaxPicHeight = h;
	stVencChnAttr.stVencAttr.u32PicWidth = w;
	stVencChnAttr.stVencAttr.u32PicHeight = h;
	stVencChnAttr.stVencAttr.bEsBufQueueEn = CVI_FALSE;
	stVencChnAttr.stVencAttr.bIsoSendFrmEn = CVI_FALSE;
	stVencChnAttr.stVencAttr.bByFrame = 1;
	stVencChnAttr.stRcAttr.enRcMode = VENC_RC_MODE_MJPEGFIXQP;

	s32Ret = CVI_VENC_CreateChn(ch, &stVencChnAttr);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_CreateChn [%d] failed with %#x\n", ch, s32Ret);
		return s32Ret;
	}

	VENC_JPEG_PARAM_S stJpegParam;
	memset(&stJpegParam, 0, sizeof(VENC_JPEG_PARAM_S));
	s32Ret = CVI_VENC_GetJpegParam(ch, &stJpegParam);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_GetJpegParam failed with %#x\n", s32Ret);
		_mmf_jpg_destroy(ch);
		return s32Ret;
	}
	stJpegParam.u32Qfactor = _mmf_jpg_qfactor(quality);
	s32Ret = CVI_VENC_SetJpegParam(ch, &stJpegParam);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_SetJpegParam failed with %#x\n", s32Ret);
		_mmf_jpg_destroy(ch);
		return s32Ret;
	}

	// Native semi-planar frames already own their VPSS buffer; a copy
	// buffer is allocated only by the copied fallback (_mmf_jpg_push_copy).
	priv.enc_jpg_input_pool_id = VB_INVALID_POOLID;

	VENC_RECV_PIC_PARAM_S stRecvParam;
	stRecvParam.s32RecvPicNum = -1;
	s32Ret = CVI_VENC_StartRecvFrame(ch, &stRecvParam);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_StartRecvPic failed with %#x\n", s32Ret);
		// Not yet owned by enc_jpg_is_init: destroy it here, so that no
		// vendor channel outlives its bookkeeping.
		_mmf_jpg_destroy(ch);
		return CVI_FAILURE;
	}

	if (priv.enc_jpg_frame) {
		_mmf_free_frame(priv.enc_jpg_frame);
		priv.enc_jpg_frame = NULL;
	}

	priv.enc_jpg_frame_w = w;
	priv.enc_jpg_frame_h = h;
	priv.enc_jpg_frame_fmt = format;
	priv.enc_jpg_quality = _mmf_jpg_qfactor(quality);
	priv.enc_jpg_is_init = 1;
	priv.enc_jpg_running = 0;

	return s32Ret;
}

int mmf_enc_jpg_deinit(int ch)
{
	MMF_JPG_TEARDOWN;
	if (!priv.enc_jpg_is_init)
		return 0;

	CVI_S32 s32Ret = CVI_SUCCESS;

	if (!mmf_enc_jpg_pop(ch, NULL, NULL)) {
		mmf_enc_jpg_free(ch);
	}

	s32Ret = CVI_VENC_StopRecvFrame(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_StopRecvPic failed with %d\n", s32Ret);
	}

	s32Ret = CVI_VENC_ResetChn(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_ResetChn vechn[%d] failed with %#x!\n",
				ch, s32Ret);
	}

	s32Ret = CVI_VENC_DestroyChn(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_DestroyChn [%d] failed with %d\n", ch, s32Ret);
		priv.enc_jpg_destroy_failed = true;
		priv.enc_jpg_destroy_ch = ch;
	}

	if (priv.enc_jpg_frame) {
		_mmf_free_frame(priv.enc_jpg_frame);
		priv.enc_jpg_frame = NULL;
	}

	_destroy_vb_pool(priv.enc_jpg_input_pool_id);
	priv.enc_jpg_input_pool_id = VB_INVALID_POOLID;

	if (mmf_try_deinit(false)) {
		return -1;
	}

	priv.enc_jpg_frame_w = 0;
	priv.enc_jpg_frame_h = 0;
	priv.enc_jpg_frame_fmt = 0;
	priv.enc_jpg_quality = -1;
	priv.enc_jpg_is_init = 0;
	priv.enc_jpg_running = 0;

	return s32Ret;
}

static int _mmf_jpg_update_quality(int ch, int quality)
{
    if (!priv.enc_jpg_is_init || priv.enc_jpg_running || ch < 0 || ch >= MMF_VENC_MAX_CHN
        || quality < 20 || quality > 99) return CVI_FAILURE;
    quality = _mmf_jpg_qfactor(quality);
    if (priv.enc_jpg_quality == quality) return CVI_SUCCESS;
    VENC_JPEG_PARAM_S params{};
    int ret = CVI_VENC_GetJpegParam(ch, &params);
    if (ret != CVI_SUCCESS) return ret;
    params.u32Qfactor = quality;
    ret = CVI_VENC_SetJpegParam(ch, &params);
    // Keep the previous value after failure, so the next frame retries.
    if (ret == CVI_SUCCESS) priv.enc_jpg_quality = quality;
    return ret;
}

static int _mmf_jpg_push_copy(int ch, uint8_t *data, int w, int h, int format, int quality,
                              const VIDEO_FRAME_INFO_S *source)
{
    if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !data || w <= 0 || h <= 0 || format != PIXEL_FORMAT_NV21)
        return CVI_FAILURE;
    if (priv.enc_jpg_running) return CVI_FAILURE;
    if (!priv.enc_jpg_is_init || priv.enc_jpg_frame_w != w || priv.enc_jpg_frame_h != h
        || priv.enc_jpg_frame_fmt != format) {
        const int stopped = mmf_enc_jpg_deinit(ch);
        if (stopped != CVI_SUCCESS) return stopped;
        const int started = mmf_enc_jpg_init(ch, w, h, format, quality);
        if (started != CVI_SUCCESS) return started;
    }
    const int updated = _mmf_jpg_update_quality(ch, quality);
    if (updated != CVI_SUCCESS) return updated;
    if (!priv.enc_jpg_frame) {
        bool created = false;
        if (priv.enc_jpg_input_pool_id == (int)VB_INVALID_POOLID) {
            const uint32_t size = COMMON_GetPicBufferSize(w, h, (PIXEL_FORMAT_E)format,
                DATA_BITWIDTH_8, COMPRESS_MODE_NONE, DEFAULT_ALIGN);
            const int pool = _create_vb_pool(size, 1);
            if (pool < 0) return CVI_FAILURE;
            priv.enc_jpg_input_pool_id = pool;
            created = true;
        }
        priv.enc_jpg_frame = _mmf_alloc_frame(priv.enc_jpg_input_pool_id, {(CVI_U32)w, (CVI_U32)h},
                                             (PIXEL_FORMAT_E)format);
        if (!priv.enc_jpg_frame) {
            if (created) {
                _destroy_vb_pool(priv.enc_jpg_input_pool_id);
                priv.enc_jpg_input_pool_id = VB_INVALID_POOLID;
            }
            return CVI_FAILURE;
        }
    }
    const int copied = source
        ? nanokvm::media::copy_mapped_nv21_and_flush(priv.enc_jpg_frame, source, data)
        : nanokvm::media::copy_packed_and_flush(priv.enc_jpg_frame, data, w, h, (PIXEL_FORMAT_E)format);
    if (copied != CVI_SUCCESS) return copied;
    const int sent = CVI_VENC_SendFrame(ch, priv.enc_jpg_frame, 1000);
    if (sent == CVI_SUCCESS) priv.enc_jpg_running = 1;
    return sent;
}

int mmf_enc_jpg_push_with_quality(int ch, uint8_t *data, int w, int h, int format, int quality)
{
    return _mmf_jpg_push_copy(ch, data, w, h, format, quality, NULL);
}

// Read once: called for every frame, and the environment does not change.
static bool diagnostic_force_copy()
{
    static const bool force = [] {
        const char *value = getenv("NANOKVM_DIAGNOSTIC_FORCE_COPY");
        return value && strcmp(value, "1") == 0;
    }();
    return force;
}

/*
 * The copied-frame fallbacks (_mmf_venc_push_copy, _mmf_jpg_push_copy) take a
 * whole picture of the video pool: 5.3 MiB at 2560x1440, 11.9 MiB at
 * 3840x2160. The pools are sized for the native path, so a copy buffer is
 * released as soon as the native path sends a frame again; one that stayed
 * would leave no room for MJPEG next to the video encoder. Both helpers run
 * only while the encoder does not hold the copied frame: the JPEG one is
 * called when the encoder is idle or has just taken a native frame.
 */
static int _mmf_release_venc_copy(int ch) {
    venc_info_t *info = &priv.venc[ch];
    if (!info->is_inited || info->is_running || priv.venc_stream_acquired[ch]) return CVI_SUCCESS;
    if (info->capture_frame) {
        const int released = _mmf_free_frame(info->capture_frame);
        info->capture_frame = NULL;
        if (released != CVI_SUCCESS) return released;
    }
    if (info->pool_id != VB_INVALID_POOLID) {
        const int released = _destroy_vb_pool(info->pool_id);
        if (released != CVI_SUCCESS) return released;
        info->pool_id = VB_INVALID_POOLID;
    }
    return CVI_SUCCESS;
}

static int _mmf_release_jpg_copy(void) {
    if (!priv.enc_jpg_is_init || priv.enc_jpg_frame_fmt != PIXEL_FORMAT_NV21) return CVI_SUCCESS;
    if (priv.enc_jpg_frame) {
        const int released = _mmf_free_frame(priv.enc_jpg_frame);
        priv.enc_jpg_frame = NULL;
        if (released != CVI_SUCCESS) return released;
    }
    if (priv.enc_jpg_input_pool_id != (int)VB_INVALID_POOLID) {
        const int released = _destroy_vb_pool(priv.enc_jpg_input_pool_id);
        if (released != CVI_SUCCESS) return released;
        priv.enc_jpg_input_pool_id = VB_INVALID_POOLID;
    }
    return CVI_SUCCESS;
}

int mmf_enc_jpg_push_vi_with_quality(int ch, int vi_ch, int quality)
{
	const bool jpeg_lease = vi_ch >= 0 && vi_jpeg_ch == vi_ch;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || vi_ch < 0 || vi_ch >= MMF_VI_MAX_CHN
		|| (!jpeg_lease && (!priv.vi_frame_valid[vi_ch] || !priv.vi_frame_deferred[vi_ch]))) {
		printf("Invalid native JPEG frame. venc:%d vi:%d\r\n", ch, vi_ch);
		return -1;
	}
    if (priv.enc_jpg_running) return CVI_FAILURE;
	// A JPEG reader on a packed VI-direct output: hand it back to VPSS. A
	// packed frame fails below; the next one is a VPSS frame again.
	if (!jpeg_lease && _mmf_jpeg_reader_yields() && _mmf_vi_direct_yield(vi_ch) != CVI_SUCCESS)
		return -1;

	VIDEO_FRAME_INFO_S *frame = jpeg_lease ? &vi_jpeg_frame : &priv.vi_frame[vi_ch];
	int width = frame->stVFrame.u32Width;
	int height = frame->stVFrame.u32Height;
	int format = frame->stVFrame.enPixelFormat;
	if (format != PIXEL_FORMAT_NV21 && format != PIXEL_FORMAT_NV16) {
		printf("Unsupported native JPEG format:%d\r\n", format);
		return -1;
	}

	if (!priv.enc_jpg_is_init || priv.enc_jpg_frame_w != width
		|| priv.enc_jpg_frame_h != height || priv.enc_jpg_frame_fmt != format) {
        const int stopped = mmf_enc_jpg_deinit(ch);
        if (stopped != CVI_SUCCESS) return stopped;
		int ret = mmf_enc_jpg_init(ch, width, height, format, quality);
		if (ret != CVI_SUCCESS) {
			return ret;
		}
	}

    const int updated = _mmf_jpg_update_quality(ch, quality);
    if (updated != CVI_SUCCESS) return updated;
	CVI_S32 s32Ret = diagnostic_force_copy() ? CVI_FAILURE : CVI_VENC_SendFrame(ch, frame, 1000);
	if (s32Ret != CVI_SUCCESS) {
        // The stride-aware copied fallback accepts NV21 only. Never treat
        // full-height NV16 chroma as subsampled VU; the caller reverts to NV21.
        if (format == PIXEL_FORMAT_NV16 || jpeg_lease) return s32Ret;
		printf("CVI_VENC_SendFrame native JPEG failed with %#x, fallback to copy\n", s32Ret);
		uint8_t *data = (uint8_t *)_mmf_map_vi_frame(vi_ch);
		if (data == NULL) {
			return s32Ret;
		}
		return _mmf_jpg_push_copy(ch, data, width, height, format, quality, frame);
	}

	priv.enc_jpg_running = 1;
	// The native frame is with the encoder: a copy buffer left from a
	// fallback is idle now (see _mmf_release_jpg_copy).
	if (priv.enc_jpg_frame) _mmf_release_jpg_copy();
	return s32Ret;
}

// Keep JPEG-specific storage beside the JPEG stream path. Besides making the
// ownership clearer, this avoids colliding with unrelated global VENC storage.
static VENC_PACK_S jpeg_pack_storage;

int mmf_enc_jpg_pop(int ch, uint8_t **data, int *size)
{
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN) {
		printf("Invalid JPEG channel:%d\r\n", ch);
		return -1;
	}
	if (!priv.enc_jpg_running) {
		return s32Ret;
	}

	memset(&jpeg_pack_storage, 0, sizeof(jpeg_pack_storage));
	priv.enc_jpeg_frame.pstPack = &jpeg_pack_storage;
	// JPEG produces one pack per frame. Go straight to the blocking call:
	// QueryStatus can still report zero immediately after SendFrame and turn
	// normal encoder latency into a spurious failure.
	s32Ret = CVI_VENC_GetStream(ch, &priv.enc_jpeg_frame, 1000);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_GetStream failed with %#x\n", s32Ret);
		priv.enc_jpeg_frame.pstPack = NULL;
		return s32Ret;
	}

	VENC_PACK_S *pack = priv.enc_jpeg_frame.pstPack;
	if (priv.enc_jpeg_frame.u32PackCount != 1 || pack->pu8Addr == NULL
		|| pack->u32Offset > pack->u32Len) {
		printf("Invalid JPEG stream pack\r\n");
		CVI_VENC_ReleaseStream(ch, &priv.enc_jpeg_frame);
		priv.enc_jpeg_frame.pstPack = NULL;
		priv.enc_jpg_running = 0;
		return -1;
	}

	if (data)
		*data = pack->pu8Addr + pack->u32Offset;
	if (size)
		*size = pack->u32Len - pack->u32Offset;

	return s32Ret;
}

int mmf_enc_jpg_free(int ch)
{
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (!priv.enc_jpg_running) {
		return s32Ret;
	}

	s32Ret = CVI_VENC_ReleaseStream(ch, &priv.enc_jpeg_frame);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_ReleaseStream failed with %#x\n", s32Ret);
		return s32Ret;
	}

	if (priv.enc_jpeg_frame.pstPack) {
		priv.enc_jpeg_frame.pstPack = NULL;
	}

	priv.enc_jpg_running = 0;
	return s32Ret;
}

void mmf_set_vi_hmirror(int ch, bool en)
{
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		printf("invalid ch %d, must be [0, %d)\r\n", ch, MMF_VI_MAX_CHN);
		return;
	}

	g_priv.vi_hmirror[ch] = en;
}

void mmf_set_vi_vflip(int ch, bool en)
{
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		printf("invalid ch %d, must be [0, %d)\r\n", ch, MMF_VI_MAX_CHN);
		return;
	}

	g_priv.vi_vflip[ch] = en;
}

/*
 * The stream buffer holds the encoded frames until they are fetched. A frame
 * that does not fit is dropped by the driver, which then halves the bitrate
 * and forces an IDR; 1 MiB was too small for 2560x1440 at 20 Mbit/s (IDRs of
 * 650 KB plus queued deltas). One byte per pixel (3.5 MiB at 2560x1440, 8 MiB
 * at 3840x2160) holds the largest IDR seen with room for the queue.
 */
static CVI_U32 venc_stream_buffer(const mmf_venc_cfg_t *cfg)
{
	CVI_U32 bytes = venc_tuning.buf_kb > 0 ? (CVI_U32)venc_tuning.buf_kb << 10
		: (CVI_U32)cfg->w * (CVI_U32)cfg->h;
	bytes = MAX(bytes, 1u << 20);
	bytes = MIN(bytes, 8u << 20);
	return (bytes + 0xffff) & ~0xffffu;
}

// The IDR interval in frames: venc_tuning.idr_seconds at the output rate.
static CVI_U32 venc_idr_interval(const mmf_venc_cfg_t *cfg)
{
	const int fps = cfg->output_fps > 0 ? cfg->output_fps : 30;
	const int seconds = venc_tuning.idr_seconds > 0 ? venc_tuning.idr_seconds : 10;
	return (CVI_U32)MIN(fps * seconds, 65535);
}

template <typename T>
static void _set_h26x_rate_timing(T &rate, const mmf_venc_cfg_t *cfg)
{
	rate.u32Gop = venc_idr_interval(cfg);
	rate.u32StatTime = 2;
	rate.u32SrcFrameRate = cfg->intput_fps;
	rate.fr32DstFrameRate = cfg->output_fps;
	rate.bVariFpsEn = CVI_FALSE;
}

static void _set_h26x_rc_attr(VENC_CHN_ATTR_S *attr, const mmf_venc_cfg_t *cfg)
{
	if (cfg->type == 2) {
		attr->stRcAttr.enRcMode = VENC_RC_MODE_H264CBR;
		_set_h26x_rate_timing(attr->stRcAttr.stH264Cbr, cfg);
		attr->stRcAttr.stH264Cbr.u32BitRate = cfg->bitrate;
	} else {
		attr->stRcAttr.enRcMode = VENC_RC_MODE_H265CBR;
		_set_h26x_rate_timing(attr->stRcAttr.stH265Cbr, cfg);
		attr->stRcAttr.stH265Cbr.u32BitRate = cfg->bitrate;
	}
}

template <typename T>
static void _set_h26x_rc_limits(T &param)
{
	const venc_tuning_t &t = venc_tuning;
	param.u32MinIprop = 1;
	param.u32MaxIprop = t.max_iprop;
	param.u32MaxQp = t.max_qp;
	param.u32MinQp = t.min_qp;
	param.u32MaxIQp = t.max_iqp;
	param.u32MinIQp = t.min_iqp;
	param.bQpMapEn = CVI_FALSE;
}

static void _set_h26x_cbr_limits(VENC_RC_PARAM_S *param, const mmf_venc_cfg_t *cfg)
{
	if (cfg->type == 2) {
		_set_h26x_rc_limits(param->stParamH264Cbr);
	} else {
		_set_h26x_rc_limits(param->stParamH265Cbr);
	}
}

static int _venc_init_failed(int ch, const char *stage, CVI_S32 error)
{
	fprintf(stderr, "[kvm_mmf] %s failed on VENC channel %d with %#x\n", stage, ch, error);
	fflush(stderr);
	int cleanup = mmf_del_venc_channel(ch);
	if (cleanup != 0) {
		fprintf(stderr, "[kvm_mmf] failed to unwind VENC channel %d after %s: %#x\n",
			ch, stage, cleanup);
		fflush(stderr);
	}
	return error == CVI_SUCCESS ? CVI_FAILURE : error;
}

int mmf_add_venc_channel_v2(int ch, mmf_venc_cfg_t *cfg) {
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || priv.venc[ch].is_used) {
		fprintf(stderr, "[kvm_mmf] invalid or busy VENC channel %d\n", ch);
		fflush(stderr);
		return -1;
	}
	if (cfg->type != 1 && cfg->type != 2) {
		fprintf(stderr, "[kvm_mmf] unsupported VENC codec type=%d\n", cfg->type);
		fflush(stderr);
		return -1;
	}
	VENC_CHN_ATTR_S stVencChnAttr;
	memset(&stVencChnAttr, 0, sizeof(VENC_CHN_ATTR_S));
	stVencChnAttr.stVencAttr.enType = cfg->type == 1 ? PT_H265 : PT_H264;
	stVencChnAttr.stVencAttr.u32MaxPicWidth = cfg->w;
	stVencChnAttr.stVencAttr.u32MaxPicHeight = cfg->h;
	venc_tuning = load_venc_tuning();
	stVencChnAttr.stVencAttr.u32BufSize = venc_stream_buffer(cfg);
	stVencChnAttr.stVencAttr.bByFrame = CVI_TRUE;
	stVencChnAttr.stVencAttr.u32PicWidth = cfg->w;
	stVencChnAttr.stVencAttr.u32PicHeight = cfg->h;
	stVencChnAttr.stVencAttr.bEsBufQueueEn = CVI_TRUE;
	stVencChnAttr.stVencAttr.bIsoSendFrmEn = CVI_TRUE;
	if (cfg->type == 2 && venc_tuning.h264_high) {
		// The stream is Main with CABAC today; High adds the 8x8 transform.
		stVencChnAttr.stVencAttr.u32Profile = H264E_PROFILE_HIGH;
	}
	stVencChnAttr.stGopAttr.enGopMode = VENC_GOPMODE_NORMALP;
	stVencChnAttr.stGopAttr.stNormalP.s32IPQpDelta = venc_tuning.ip_qp_delta;
	_set_h26x_rc_attr(&stVencChnAttr, cfg);
	s32Ret = CVI_VENC_CreateChn(ch, &stVencChnAttr);
	if (s32Ret != CVI_SUCCESS) {
		fprintf(stderr,
			"[kvm_mmf] CVI_VENC_CreateChn failed: channel=%d codec=%d "
			"size=%ux%u bitrate=%u error=%#x\n",
			ch, cfg->type, cfg->w, cfg->h, cfg->bitrate, s32Ret);
		fflush(stderr);
		return s32Ret;
	}

	// From this point on, make every failed initialization visible to teardown.
	// Otherwise a successfully created vendor channel can be orphaned while the
	// local bookkeeping still says that the channel is free.
	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	memset(info, 0, sizeof(venc_info_t));
	info->type = cfg->type;
	info->pool_id = VB_INVALID_POOLID;
	priv.venc_stream_acquired[ch] = false;
	priv.venc_input_vi_ch[ch] = -1;
	memcpy(&info->cfg, cfg, sizeof(mmf_venc_cfg_t));
	info->is_used = 1;
	info->is_inited = 1;

	// The encoder is opened by StartRecvFrame and reads the QP limits, the
	// I-frame proportion and the start QP only then; set later, they were
	// stored but never used (the stream ran on the driver defaults: QP down
	// to 1, I frames unbounded).
	VENC_RC_PARAM_S stRcParam;
	s32Ret = CVI_VENC_GetRcParam(ch, &stRcParam);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_GetRcParam", s32Ret);
	}
	stRcParam.s32FirstFrameStartQp = 35;
	stRcParam.s32InitialDelay = 1000;
	if (venc_tuning.row_qp_delta >= 0) stRcParam.u32RowQpDelta = venc_tuning.row_qp_delta;
	if (venc_tuning.thrd_lv >= 0) stRcParam.u32ThrdLv = venc_tuning.thrd_lv;
	_set_h26x_cbr_limits(&stRcParam, cfg);
	s32Ret = CVI_VENC_SetRcParam(ch, &stRcParam);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_SetRcParam", s32Ret);
	}

	VENC_RECV_PIC_PARAM_S stRecvParam = {0};
	stRecvParam.s32RecvPicNum = -1;
	s32Ret = CVI_VENC_StartRecvFrame(ch, &stRecvParam);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_StartRecvFrame", s32Ret);
	}

	VENC_FRAMELOST_S stFL;
	s32Ret = CVI_VENC_GetFrameLostStrategy(ch, &stFL);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_GetFrameLostStrategy", s32Ret);
	}
	fprintf(stderr,
		"[kvm_mmf] VENC channel %d frame-loss defaults: codec=%d open=%d threshold=%u mode=%d gap=%u\n",
		ch, cfg->type, stFL.bFrmLostOpen, stFL.u32FrmLostBpsThr,
		stFL.enFrmLostMode, stFL.u32EncFrmGaps);
	fflush(stderr);
	/* A KVM stream must preserve its reference chain. The generic vendor
	 * frame-loss strategy can discard a reference frame when instantaneous
	 * bitrate spikes, after which Direct/WebCodecs cannot decode later deltas. */
	stFL.bFrmLostOpen = CVI_FALSE;
	stFL.enFrmLostMode = FRMLOST_NORMAL;
	stFL.u32EncFrmGaps = 0;
	s32Ret = CVI_VENC_SetFrameLostStrategy(ch, &stFL);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_SetFrameLostStrategy", s32Ret);
	}

	// Native VPSS leases can go straight to VENC. Allocate copy storage only
	// when a caller actually needs that path; idle reserves can be trimmed
	// on a codec change, and teardown releases any remaining allocation.
	fprintf(stderr,
		"[kvm_mmf] VENC channel %d initialized: codec=%d rc=cbr size=%ux%u "
		"bitrate=%u idr_interval=%u stream_buffer=%uKiB src_fps=%d dst_fps=%d "
		"(rate metadata; wall pacing unmeasured)\n",
		ch, cfg->type, cfg->w, cfg->h, cfg->bitrate, venc_idr_interval(cfg),
		stVencChnAttr.stVencAttr.u32BufSize >> 10,
		cfg->intput_fps, cfg->output_fps);
	fflush(stderr);
	// A different capture mode is applied by the caller's capture restart.
	_mmf_vi_yuv420_follow(cfg);
	_mmf_vi_direct_for_venc(ch, cfg);

	return 0;
}

int mmf_del_venc_channel(int ch) {
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN) {
		return -1;
	}
	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	if (!info->is_inited) {
		return 0;
	}
	fprintf(stderr, "[kvm_mmf] tearing down VENC channel %d: codec=%d rc=cbr running=%d\n",
		ch, info->cfg.type, info->is_running);
	fflush(stderr);

	// A submitted frame that never completes leaves the input lease held:
	// with both VPSS buffers taken, capture stops for good. Reset the channel
	// and release the lease instead (wedged below), but only once the channel
	// is destroyed: until then the encoder may still read that buffer, and the
	// lease and its bookkeeping stay for the next attempt.
	bool wedged = false;
	if (info->is_running || priv.venc_stream_acquired[ch]) {
		mmf_stream_t stream = {};
		int drained = CVI_SUCCESS;
		if (!priv.venc_stream_acquired[ch]) {
			drained = mmf_venc_pop(ch, &stream);
			if (drained != CVI_SUCCESS && drained != CVI_ERR_VENC_BUSY) return drained;
		}
		if (drained == CVI_ERR_VENC_BUSY) {
			fprintf(stderr, "[kvm_mmf] VENC channel %d is wedged; resetting it and releasing its input\n", ch);
			fflush(stderr);
			wedged = true;
		} else {
			const int released = mmf_venc_free(ch);
			if (released != CVI_SUCCESS) return released;
		}
	}

	CVI_S32 s32Ret = CVI_SUCCESS;
	s32Ret = CVI_VENC_StopRecvFrame(ch);
	if (s32Ret != CVI_SUCCESS) {
		fprintf(stderr, "[kvm_mmf] CVI_VENC_StopRecvFrame(%d) failed with %#x\n", ch, s32Ret);
		fflush(stderr);
	}

	CVI_S32 destroyRet = CVI_FAILURE;
	for (int attempt = 0; attempt < 3; attempt++) {
		if (attempt != 0) {
			usleep(5000 * attempt);
		}
		s32Ret = CVI_VENC_ResetChn(ch);
		if (s32Ret != CVI_SUCCESS) {
			fprintf(stderr, "[kvm_mmf] CVI_VENC_ResetChn(%d) attempt %d failed with %#x\n",
				ch, attempt + 1, s32Ret);
			fflush(stderr);
		}
		destroyRet = CVI_VENC_DestroyChn(ch);
		if (destroyRet == CVI_SUCCESS) {
			break;
		}
		fprintf(stderr, "[kvm_mmf] CVI_VENC_DestroyChn(%d) attempt %d failed with %#x\n",
			ch, attempt + 1, destroyRet);
		fflush(stderr);
	}
	if (destroyRet != CVI_SUCCESS) {
		// Preserve the bookkeeping so the next frame can retry teardown.  In
		// particular, never claim that a still-existing vendor channel is free,
		// and keep a wedged channel's input lease: it may still read it.
		return destroyRet;
	}
	if (wedged) {
		const int wedged_vi = priv.venc_input_vi_ch[ch];
		info->is_running = 0;
		priv.venc_input_vi_ch[ch] = -1;
		if (wedged_vi >= 0) {
			const int released = _mmf_release_vi_frame(wedged_vi);
			fprintf(stderr, "[kvm_mmf] released input %d of wedged VENC channel %d: %#x\n",
				wedged_vi, ch, released);
			fflush(stderr);
		}
	}

	if (info->capture_frame) {
		_mmf_free_frame(info->capture_frame);
		info->capture_frame = NULL;
	}
	/*
	 * Do not release deferred VI frames here. Profile reconfiguration happens
	 * after CameraCviMmf::read() has leased the next zero-copy frame, and that
	 * exact frame must remain valid until it has been sent to the replacement
	 * encoder. An outstanding frame owned by the old encoder is released by
	 * mmf_venc_free() above when info->is_running is true.
	 */
	if (info->pool_id != VB_INVALID_POOLID && info->pool_id < VB_MAX_COMM_POOLS) {
		_destroy_vb_pool(info->pool_id);
	}
	free(venc_pack_storage[ch]);
	venc_pack_storage[ch] = NULL;
	venc_pack_capacity[ch] = 0;

	memset(info, 0, sizeof(venc_info_t));
	priv.venc_stream_acquired[ch] = false;
	priv.venc_input_vi_ch[ch] = -1;
	if (vi_direct_venc == ch) _mmf_vi_direct_off_all();
	fprintf(stderr, "[kvm_mmf] VENC channel %d destroyed\n", ch);
	fflush(stderr);

	return 0;
}

static int mmf_del_venc_channel_all(void) {
	for (int i = 0; i < MMF_VENC_MAX_CHN; i ++) {
		const int result = mmf_del_venc_channel(i);
		if (result != CVI_SUCCESS) return result;
	}
	return 0;
}

static int _mmf_venc_push_copy(int ch, uint8_t *data, int w, int h, int format,
                               const VIDEO_FRAME_INFO_S *source = NULL) {
    if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !data || !priv.venc[ch].is_inited) return CVI_FAILURE;
    venc_info_t *info = &priv.venc[ch];
    if (info->is_running || w != info->cfg.w || h != info->cfg.h || format != info->cfg.fmt)
        return CVI_FAILURE;
    if (!info->capture_frame) {
        const uint32_t size = VDEC_GetPicBufferSize((PAYLOAD_TYPE_E)info->cfg.type,
            w, h, (PIXEL_FORMAT_E)format, DATA_BITWIDTH_8, COMPRESS_MODE_NONE);
        const int pool = _create_vb_pool(size, 1);
        if (pool < 0) {
            note_video_memory(true);
            return CVI_FAILURE;
        }
        VIDEO_FRAME_INFO_S *frame = _mmf_alloc_frame(pool,
            (SIZE_S){(CVI_U32)w, (CVI_U32)h}, (PIXEL_FORMAT_E)format);
        if (!frame) {
            _destroy_vb_pool(pool);
            return CVI_FAILURE;
        }
        // Publish ownership only after both allocations succeeded. A failed
        // attempt must not orphan a pool or prevent a later native send.
        info->pool_id = pool;
        info->capture_frame = frame;
        fprintf(stderr, "[kvm_mmf] VENC channel %d allocated copy buffer: %u bytes\n", ch, size);
    }
    const int copied = source
        ? nanokvm::media::copy_mapped_nv21_and_flush(info->capture_frame, source, data)
        : nanokvm::media::copy_packed_and_flush(info->capture_frame, data, w, h, (PIXEL_FORMAT_E)format);
    if (copied != CVI_SUCCESS) return copied;
    const int sent = CVI_VENC_SendFrame(ch, info->capture_frame, 1000);
    if (sent == CVI_SUCCESS) info->is_running = 1;
    if (sent == CVI_SUCCESS || sent == CVI_ERR_VENC_NOBUF) note_video_memory(sent != CVI_SUCCESS);
    return sent;
}

int mmf_venc_push(int ch, uint8_t *data, int w, int h, int format) {
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || data == NULL || format != PIXEL_FORMAT_NV21) {
		printf("Invalid param. ch:%d data:%p format:%d\r\n", ch, data, format);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
    if (!info->is_inited || info->is_running) return CVI_FAILURE;
	if (info->type == 1 || info->type == 2) {
		VIDEO_FRAME_INFO_S *vi_frame = NULL;
        const int vi_ch = _mmf_find_deferred_vi_frame(w, h, format, data, &vi_frame);
        if (vi_ch >= 0) {
            const int sent = CVI_VENC_SendFrame(ch, vi_frame, 1000);
            if (sent == CVI_SUCCESS) {
                info->is_running = 1;
                priv.venc_input_vi_ch[ch] = vi_ch;
                return 0;
            }
            const int copied = _mmf_venc_push_copy(ch, data, w, h, format, vi_frame);
            if (copied == CVI_SUCCESS) priv.venc_input_vi_ch[ch] = vi_ch;
            return copied;
        }
	}

	return _mmf_venc_push_copy(ch, data, w, h, format);
}

int mmf_trim_idle_copy_buffers(void) {
    MMF_JPG_TEARDOWN;
    // Called under the capture owner's serialization when changing codec.
    // Keep encoders alive for mixed clients, but not unused copy reserves.
    for (int ch = 0; ch < MMF_VENC_MAX_CHN; ++ch) {
        const int released = _mmf_release_venc_copy(ch);
        if (released != CVI_SUCCESS) return released;
    }
    return priv.enc_jpg_running ? CVI_SUCCESS : _mmf_release_jpg_copy();
}

int mmf_venc_push_vi(int ch, int vi_ch) {
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || vi_ch < 0 || vi_ch >= MMF_VI_MAX_CHN
		|| !priv.vi_frame_valid[vi_ch] || !priv.vi_frame_deferred[vi_ch]) {
		printf("Invalid native frame. venc:%d vi:%d\r\n", ch, vi_ch);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
    if (!info->is_inited || info->is_running) return CVI_FAILURE;
	const int direct_type = _mmf_vi_direct_type();
	if (vi_direct[vi_ch] && !vi_frame_from_vi[vi_ch] && info->type == direct_type) {
		// The VPSS frame leased before VI-direct started. The encoder keeps the
		// input format of its first frame, so give it the VI frame instead.
		const int released = _mmf_release_vi_frame(vi_ch);
		if (released != CVI_SUCCESS) return released;
		int len = 0, w = 0, h = 0, format = 0;
		if (_mmf_acquire_vi_frame(vi_ch, &len, &w, &h, &format, 200) != 0) return -1;
		priv.vi_frame_deferred[vi_ch] = true;
	} else if (vi_frame_from_vi[vi_ch] && (!vi_direct[vi_ch] || info->type != direct_type)) {
		// The reverse: a VI frame leased before VI-direct ended, or for an
		// encoder of the other codec. Take the VPSS frame instead.
		const int released = _mmf_release_vi_frame(vi_ch);
		if (released != CVI_SUCCESS) return released;
		int len = 0, w = 0, h = 0, format = 0;
		if (_mmf_acquire_vi_frame(vi_ch, &len, &w, &h, &format, 200) != 0) return -1;
		priv.vi_frame_deferred[vi_ch] = true;
	}
	VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[vi_ch];
	// A packed VI-direct frame (UYVY) goes to H.265 only; H.264 has no packed
	// input. A 4:2:0 VI frame is NV21.
	const bool packed = frame->stVFrame.enPixelFormat == PIXEL_FORMAT_UYVY && info->type == 1;
	if ((info->type != 1 && info->type != 2) ||
		(frame->stVFrame.enPixelFormat != PIXEL_FORMAT_NV21 && !packed)) {
		printf("Unsupported native frame. venc_type:%d format:%d\r\n",
			info->type, frame->stVFrame.enPixelFormat);
		return -1;
	}

	CVI_S32 ret = diagnostic_force_copy() ? CVI_FAILURE : CVI_VENC_SendFrame(ch, frame, 1000);
	if (ret == CVI_SUCCESS || ret == CVI_ERR_VENC_NOBUF) note_video_memory(ret != CVI_SUCCESS);
	if (ret == CVI_ERR_VENC_NOBUF) {
		// Without its buffers the channel cannot encode; a copy would not
		// help and needs more of the same memory. The caller recreates it.
		return ret;
	}
	if (ret == CVI_SUCCESS) {
		// A copy buffer left from a fallback is idle: the encoder finished
		// with it before this frame (see _mmf_release_venc_copy).
		if (info->capture_frame) _mmf_release_venc_copy(ch);
		info->is_running = 1;
		priv.venc_input_vi_ch[ch] = vi_ch;
		return 0;
	}

	printf("CVI_VENC_SendFrame native failed with %#x, fallback to mapped copy\n", ret);
	uint8_t *data = (uint8_t *)_mmf_map_vi_frame(vi_ch);
	if (data == NULL) {
		return -1;
	}
    const int copied = _mmf_venc_push_copy(ch, data, frame->stVFrame.u32Width,
        frame->stVFrame.u32Height, frame->stVFrame.enPixelFormat, frame);
    if (copied == CVI_SUCCESS) priv.venc_input_vi_ch[ch] = vi_ch;
    return copied;
}

int mmf_vi_input_fps(void) {
	VI_CHN_STATUS_S status = {};
	if (CVI_VI_QueryChnStatus(0, 0, &status) != CVI_SUCCESS || !status.bEnable) return 0;
	return (int)status.u32FrameRate;
}

static int vpss_last_src_fps, vpss_last_dst_fps;

int mmf_vpss_set_rate(int src_fps, int dst_fps) {
	vpss_last_src_fps = src_fps;
	vpss_last_dst_fps = dst_fps;
	// The VI measurement reads a 50 Hz input as 50 or 51; never thin a stream
	// that already matches the input.
	const int tolerance = src_fps / 20 > 2 ? src_fps / 20 : 2;
	// The capture mode rule uses the last known verdict: right after a
	// capture start the VI has not measured the input rate yet.
	if (src_fps > 0 && dst_fps > 0) vpss_rate_thins = dst_fps < src_fps - tolerance;
	// Group frame-rate control is not applied to a started group; the
	// channel control is, and skips the conversion of dropped frames.
	int result = 0;
	for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) {
		if (!priv.vi_chn_is_inited[ch]) continue;
		int src = src_fps, dst = dst_fps;
		if (src > 0 && vpss_chn_max_fps[ch] > 0 && (dst <= 0 || dst > vpss_chn_max_fps[ch]))
			dst = vpss_chn_max_fps[ch];
		if (src <= 0 || dst <= 0 || dst >= src - tolerance) src = dst = -1;
		VPSS_CHN_ATTR_S attr;
		if (CVI_VPSS_GetChnAttr(0, ch, &attr) != CVI_SUCCESS) { result = -1; continue; }
		if (attr.stFrameRate.s32SrcFrameRate == src && attr.stFrameRate.s32DstFrameRate == dst) continue;
		attr.stFrameRate.s32SrcFrameRate = src;
		attr.stFrameRate.s32DstFrameRate = dst;
		if (CVI_VPSS_SetChnAttr(0, ch, &attr) != CVI_SUCCESS) result = -1;
	}
	return result;
}

int mmf_vpss_set_chn_max_rate(int ch, int max_fps) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || max_fps < 0) return CVI_FAILURE;
	if (vpss_chn_max_fps[ch] == max_fps) return CVI_SUCCESS;
	vpss_chn_max_fps[ch] = max_fps;
	return mmf_vpss_set_rate(vpss_last_src_fps, vpss_last_dst_fps);
}

// The NV21 output feeds the video encoders (limited-range BT.709) and, with
// 4:2:0 sampling, frame detection or after the 4:2:2 fallback, MJPEG, which
// JPEG decoders read as full-range BT.601. libkvm switches it to JFIF while
// only MJPEG reads it. A new matrix makes the driver drop the frames the
// channel has queued. 1: switched; 0: nothing to do (already so, NV16, a
// kernel without the BT.709 output); -1: error.
int mmf_vi_chn_set_jfif(int ch, int jfif) {
	if (!_mmf_vpss_true_rgb) return 0;
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch]) return -1;
	if (vi_direct[ch]) return 0; // VPSS is bypassed: the encoder gets limited BT.709 as captured
	VPSS_CHN_ATTR_S attr;
	if (CVI_VPSS_GetChnAttr(0, ch, &attr) != CVI_SUCCESS) return -1;
	if (attr.enPixelFormat == PIXEL_FORMAT_NV16) return 0;
	const VIDEO_FORMAT_E want = jfif ? VIDEO_FORMAT_LINEAR : VIDEO_FORMAT_LINEAR_BT709;
	if (attr.enVideoFormat == want) return 0;
	attr.enVideoFormat = want;
	return CVI_VPSS_SetChnAttr(0, ch, &attr) == CVI_SUCCESS ? 1 : -1;
}

int mmf_venc_pending_vi(int ch) {
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !priv.venc[ch].is_inited
		|| !priv.venc[ch].is_running || priv.venc_stream_acquired[ch]) return -1;
	return priv.venc_input_vi_ch[ch];
}

int mmf_venc_request_idr(int ch) {
 if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !priv.venc[ch].is_inited) return -1;
 return CVI_VENC_RequestIDR(ch, CVI_TRUE);
}

int mmf_venc_pop(int ch, mmf_stream_t *stream) {
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !priv.venc[ch].is_inited) {
		printf("Invalid venc ch:%d\r\n", ch);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	VENC_STREAM_S *venc_stream = (VENC_STREAM_S *)&priv.venc[ch].capture_stream;
	if (!info->is_running) {
		/* No frame was submitted, so reporting success would expose an empty
		 * stream to kvm_vision and turn an ordinary profile transition into the
		 * misleading IMG_VENC_ERROR (-2). */
		return -1;
	}
	if (priv.venc_stream_acquired[ch]) {
		fprintf(stderr, "[kvm_mmf] VENC channel %d stream was not released before next pop\n", ch);
		fflush(stderr);
		return -1;
	}
	// One frame was already submitted. GetStream waits for that frame itself;
	// an extra 80 ms select deadline can abandon it while the codec lock is
	// still held. StopRecvFrame/ResetChn do not cancel H.26x encoding in this SDK.

	VENC_CHN_STATUS_S stStatus;
	s32Ret = CVI_VENC_QueryStatus(ch, &stStatus);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_QueryStatus failed with %#x\n", s32Ret);
		return s32Ret;
	}

	/*
	 * QueryStatus reports how many VENC_PACK_S entries GetStream may fill.
	 * Allocating a fixed eight entries and checking u32PackCount afterwards is
	 * too late: a vendor frame with more packs has already overrun the heap.
	 */
	if (stStatus.u32CurPacks == 0 || stStatus.u32CurPacks > 64) {
		fprintf(stderr, "[kvm_mmf] invalid VENC channel %d queried pack count: %u\n",
			ch, stStatus.u32CurPacks);
		fflush(stderr);
		return -1;
	}
	if (venc_pack_capacity[ch] < stStatus.u32CurPacks) {
		VENC_PACK_S *storage = (VENC_PACK_S *)realloc(venc_pack_storage[ch],
			stStatus.u32CurPacks * sizeof(VENC_PACK_S));
		if (storage == NULL) {
			printf("Realloc failed!\r\n");
			return -1;
		}
		venc_pack_storage[ch] = storage;
		venc_pack_capacity[ch] = stStatus.u32CurPacks;
	}
	memset(venc_pack_storage[ch], 0, stStatus.u32CurPacks * sizeof(VENC_PACK_S));
	venc_stream->pstPack = venc_pack_storage[ch];
	if (!venc_stream->pstPack) {
		printf("Malloc failed!\r\n");
		return -1;
	}

	if (stStatus.u32CurPacks > 0) {
		// A frame takes at most one frame period in the hardware. One that is
		// still pending after venc_pending_limit_ms never completes (the
		// channel is wedged); report it so teardown can reset the channel
		// instead of every read waiting forever.
		const uint64_t started = venc_now_us();
		unsigned pending_waits = 0;
		do {
			s32Ret = CVI_VENC_GetStream(ch, venc_stream, 1000);
			if (s32Ret != CVI_ERR_VENC_BUSY) break;
			const uint64_t waited_ms = (venc_now_us() - started) / 1000;
			if (++pending_waits == 1 || pending_waits % 1000 == 0) {
				fprintf(stderr, "[kvm_mmf] VENC channel %d frame still pending after %llu ms; retaining input\n",
					ch, (unsigned long long)waited_ms);
				fflush(stderr);
			}
			if (waited_ms >= venc_pending_limit_ms) {
				fprintf(stderr, "[kvm_mmf] VENC channel %d frame did not complete in %llu ms\n",
					ch, (unsigned long long)waited_ms);
				fflush(stderr);
				return CVI_ERR_VENC_BUSY;
			}
			usleep(1000);
		} while (true);
		if (s32Ret != CVI_SUCCESS) {
			printf("CVI_VENC_GetStream failed with %#x\n", s32Ret);
			return s32Ret;
		}
		priv.venc_stream_acquired[ch] = true;
		if (venc_tuning.stats && info->type != 0) {
			venc_stats_frame(venc_stream, &stStatus, info->type);
		}
	} else {
		printf("CVI_VENC_QueryStatus find not pack\r\n");
		return -1;
	}

	if (stream) {
		stream->count = venc_stream->u32PackCount;
		if (stream->count > 8) {
			fprintf(stderr, "[kvm_mmf] VENC channel %d returned too many packs: queried=%u returned=%d\n",
				ch, stStatus.u32CurPacks, stream->count);
			fflush(stderr);
			const int released = mmf_venc_free(ch);
			if (released != CVI_SUCCESS) return released;
			return -1;
		}
		for (int i = 0; i < stream->count; i++) {
			VENC_PACK_S *pack = &venc_stream->pstPack[i];
			stream->data[i] = pack->pu8Addr == NULL ? NULL : pack->pu8Addr + pack->u32Offset;
			stream->data_size[i] = pack->u32Offset > pack->u32Len ? -1 :
				(int)(pack->u32Len - pack->u32Offset);
			if (stream->data[i] == NULL || stream->data_size[i] <= 0) {
				static unsigned int invalid_pack_count = 0;
				invalid_pack_count++;
				if (invalid_pack_count <= 16 ||
					(invalid_pack_count & (invalid_pack_count - 1)) == 0) {
					fprintf(stderr,
						"[kvm_mmf] invalid VENC pack #%u: ch=%d pack=%d/%d queried=%u addr=%p len=%u offset=%u data_num=%u frame_end=%d\n",
						invalid_pack_count, ch, i, stream->count, stStatus.u32CurPacks,
						pack->pu8Addr, pack->u32Len, pack->u32Offset,
						pack->u32DataNum, pack->bFrameEnd);
					fflush(stderr);
				}
			}
		}
	}

	return 0;
}

int mmf_venc_free(int ch) {
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !priv.venc[ch].is_inited) {
		printf("Invalid venc ch:%d\r\n", ch);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	VENC_STREAM_S *venc_stream = (VENC_STREAM_S *)&priv.venc[ch].capture_stream;
	if (!info->is_running && !priv.venc_stream_acquired[ch]) {
		// Encoder reinitialization happens after CameraCviMmf has already
		// acquired the next VI frame.  With no submitted VENC frame there is
		// nothing to release here; releasing all VI frames would invalidate
		// the Image that is about to be sent to the new encoder.
		return s32Ret;
	}
	if (!priv.venc_stream_acquired[ch]) {
		// A wait error is not a completed frame. Keep both the pending state and
		// VI lease so deletion must drain it before closing the codec channel.
		return CVI_ERR_VENC_BUSY;
	}

	if (priv.venc_stream_acquired[ch]) {
		s32Ret = CVI_VENC_ReleaseStream(ch, venc_stream);
		if (s32Ret != CVI_SUCCESS) {
			printf("CVI_VENC_ReleaseStream failed with %#x\n", s32Ret);
		}
		if (s32Ret != CVI_SUCCESS) return s32Ret;
		priv.venc_stream_acquired[ch] = false;
	}

	info->is_running = 0;
	if (priv.venc_input_vi_ch[ch] >= 0) {
		_mmf_release_vi_frame(priv.venc_input_vi_ch[ch]);
		priv.venc_input_vi_ch[ch] = -1;
	}
	return s32Ret;
}
