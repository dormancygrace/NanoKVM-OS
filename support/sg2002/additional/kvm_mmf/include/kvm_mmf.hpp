#ifndef __KVM_MMF_HPP__
#define __KVM_MMF_HPP__

#include "stdint.h"

typedef struct {
    uint8_t *data[8];
    int data_size[8];
    int count;
} mmf_stream_t;

typedef struct {
    uint8_t type;           // 0, jpg; 1, h265; 2, h264
    int w;
	int h;
	int fmt;
	uint8_t jpg_quality;	// jpeg
	int intput_fps;			// h264/h265
	int output_fps;			// h264/h265
	int bitrate;			// h264/h265
} mmf_venc_cfg_t;

// init sys
int mmf_init(void);
int mmf_try_deinit(bool force);

// manage vi channels(vi->vpssgroup->vpss->frame)
int mmf_get_vi_unused_channel(void);
int mmf_vi_init(void);
int mmf_del_vi_channel(int ch);
int mmf_reset_vi_channel(int ch, int width, int height, int format);
bool mmf_vi_chn_is_open(int ch);
void mmf_set_vi_hmirror(int ch, bool en);
void mmf_set_vi_vflip(int ch, bool en);

// CPU view of the actual NV21 plane layout; data remains a VI lease.
typedef struct {
    uint8_t *data;
    uint32_t length;
    uint32_t width, height;
    uint32_t y_stride, vu_stride, vu_offset;
} mmf_nv21_view_t;
int mmf_vi_frame_pop_nv21(int ch, mmf_nv21_view_t *view);

// Lease a VI frame without mapping it into CPU address space. The frame can be
// submitted to an H.264 encoder with mmf_venc_push_vi().
int mmf_vi_frame_pop_native(int ch, int *len, int *width, int *height, int *format);
// As mmf_vi_frame_pop_native(), but waits at most timeout_ms for a frame.
int mmf_vi_frame_try_pop_native(int ch, int timeout_ms, int *len, int *width, int *height, int *format);
// For a JPEG reader: in 4:2:0 capture with H.264 straight from capture, its
// own lease of the VI frame (released by mmf_vi_frame_release); otherwise as
// mmf_vi_frame_pop_native().
int mmf_vi_frame_pop_jpeg(int ch, int *len, int *width, int *height, int *format);
// Held by a JPEG reader that encodes outside the capture lock, from the
// JPEG submit to the release of its input frame; teardowns of the JPEG
// encoder and of VI outputs wait for it. Recursive.
// MJPEG's own output, read outside the capture lock under mmf_jpg_hold: its
// frame only, without the direct-path recheck of the other pops.
int mmf_vi_frame_pop_own(int ch, int *len, int *width, int *height, int *format);
// Whether a popped frame is still leased (an output torn down meanwhile
// released it).
// Same thread as pop_own: true only while that exact frame lease survives.
int mmf_vi_frame_leased(int ch);
void mmf_jpg_hold(void);
void mmf_jpg_unhold(void);
void mmf_vi_frame_free(int ch);
// Release the current VI frame immediately. mmf_vi_frame_free() defers
// release so the frame can be sent directly to VENC without a second copy.
void mmf_vi_frame_release(int ch);

// venc
#ifdef __cplusplus
extern "C" {
#endif
// Explicit VB count/depth for independently consumed VPSS outputs.
// mmf_vi_drop_pending returns 0 when the output holds no queued frame or one
// was dropped, and MMF_VI_OUTPUT_LEASED (nothing dropped) while a frame of the
// output is leased to an encoder, e.g. the next video frame that was
// pre-submitted between two reads. Any other value is an error.
#define MMF_VI_OUTPUT_LEASED 1
int mmf_vi_drop_pending(int ch);
// Enables or disables an open output's VPSS channel without touching its
// pool, queue or leases.
int mmf_vi_chn_set_enabled(int ch, int enable);
int mmf_add_vi_channel_configured(int ch, int width, int height, int format, int buffers, int queue_depth);
#ifdef __cplusplus
}
#endif
int mmf_enc_jpg_deinit(int ch);
int mmf_enc_jpg_push_with_quality(int ch, uint8_t *data, int w, int h, int format, int quality);
int mmf_enc_jpg_push_vi_with_quality(int ch, int vi_ch, int quality);
int mmf_enc_jpg_pop(int ch, uint8_t **data, int *size);
int mmf_enc_jpg_free(int ch);
// v2 removes the GOP field from mmf_venc_cfg_t; old libraries must not bind.
extern "C" int mmf_add_venc_channel_v2(int ch, mmf_venc_cfg_t *cfg);
int mmf_del_venc_channel(int ch);
int mmf_venc_push(int ch, uint8_t *data, int w, int h, int format);
int mmf_venc_push_vi(int ch, int vi_ch);
// Capture owner must serialize calls; busy encoder storage is retained.
int mmf_trim_idle_copy_buffers(void);
int mmf_venc_pop(int ch, mmf_stream_t *stream);
int mmf_venc_free(int ch);
// Input rate measured by VI, in frames per second; 0 when unknown.
int mmf_vi_input_fps(void);
// Let VPSS process dst_fps of every src_fps input frames and drop the rest
// before any scaling. dst_fps <= 0, or within 5% (at least 2 fps) of
// src_fps, processes every frame.
int mmf_vpss_set_rate(int src_fps, int dst_fps);
// At most max_fps on one VPSS output (0: no own limit), such as MJPEG's own
// output next to a video stream; applied with the rate of the others.
int mmf_vpss_set_chn_max_rate(int ch, int max_fps);
int mmf_vi_chn_set_jfif(int ch, int jfif);
// 4:2:0 capture (cv181x_vi nk_yuv420, osdrv patch 0013): the VI writes NV21
// and H.264 at the input size reads it directly. active: the running capture
// is 4:2:0; pending: the video encoder wants the other mode, applied by the
// next capture start (restart).
int mmf_vi_yuv420_active(void);
// Whether a VI output is read straight from capture (its VPSS channel off).
int mmf_vi_chn_direct(int ch);
int mmf_vi_yuv420_pending(void);
// VI channel whose frame VENC channel ch is encoding, or -1.
int mmf_venc_pending_vi(int ch);
int mmf_venc_request_idr(int ch);

#endif // __KVM_MMF_HPP__
