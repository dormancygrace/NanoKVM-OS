

#ifndef KVM_VISION_H_
#define KVM_VISION_H_


#ifdef __cplusplus
extern "C" {
#endif
#include <fcntl.h> /* low-level i/o */
#include <unistd.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <pthread.h>
#include <time.h>

#define IMG_MJPEG_INPUT_BLOCKED		-8
#define IMG_BUFFER_FULL			-3
#define IMG_VENC_ERROR			-2
#define IMG_NOT_EXIST			-1
#define IMG_MJPEG_TYPE			0
#define IMG_H264_TYPE_SPS		1
#define IMG_H264_TYPE_PPS		2
#define IMG_H264_TYPE_IF		3
#define IMG_H264_TYPE_PF		4
#define IMG_VIDEO_TYPE_KEY		IMG_H264_TYPE_IF
#define IMG_VIDEO_TYPE_DELTA		IMG_H264_TYPE_PF

#define NORMAL_RES                      0
#define NEW_RES                         1
#define UNSUPPORT_RES                   2
#define UNKNOWN_RES                     3
#define ERROR_RES                       4

// Synchronous borrowed-pack callback. The callback must copy before returning;
// neither side retains the other's memory. Nonzero callback result aborts the frame.
typedef int (*kvmv_video_sink)(uintptr_t context, const uint8_t *data,
    uint32_t size, uint32_t offset, uint32_t total);
/*
 * Capture one frame and pass it to sink, which may be called several times
 * for one frame (offset/total). Return values of both readers:
 *     -8: MJPEG refused: the HDMI input is larger than 2560 on its long side
 *         (3840x2160); no JPEG channel is created or submitted to
 *     -7: HDMI input resolution error
 *     -6: unsupported resolution, change it in the host settings
 *     -5: retrieving the image, please wait
 *     -4: changing the image resolution, please wait
 *     -3: the sink refused the frame
 *     -2: encoder error
 *     -1: no image was acquired
 *      0: MJPEG image
 *      3: H.264/H.265 key frame (parameter sets and random-access frame)
 *      4: H.264/H.265 delta frame
 *      5: image not changed
 *
 * Video: one CBR Annex-B H.264/H.265 access unit. codec: 1 H.264, 2 H.265;
 * bitrate: 500..20000 Kbit/s.
 */
// v2 removes the GOP argument; a distinct symbol rejects the old calling convention.
int kvmv_read_video_sink_v2(uint16_t width, uint16_t height, uint8_t codec,
    uint16_t bitrate, uint8_t fps, kvmv_video_sink sink, uintptr_t context);

#ifdef __cplusplus
void kvmv_init(uint8_t _debug_info_en = 0);
#else
void kvmv_init(uint8_t _debug_info_en);
#endif
// Runtime JPEG chroma intent (0 NV21/4:2:0, 1 NV16/4:2:2), serialized with capture.
int set_mjpeg_chroma(uint8_t enable_422);
#define MJPEG_CHROMA_ACTIVE_422 1
#define MJPEG_CHROMA_SHARED_VIDEO 2
#define MJPEG_CHROMA_FRAME_DETECT 4
#define MJPEG_CHROMA_FORCE_COPY 8
#define MJPEG_CHROMA_FALLBACK_ERROR 16
#define MJPEG_CHROMA_LIMIT_WIDTH 32
uint8_t get_mjpeg_chroma_status(void);
// JPEG callback runs before ReleaseStream/VI release; it must copy synchronously.
int kvmv_read_mjpeg_sink(uint16_t width, uint16_t height, uint16_t quality,
    kvmv_video_sink sink, uintptr_t context);
void kvmv_request_keyframe(void);
void set_frame_detact(uint8_t _frame_detact);
void kvmv_deinit();
int kvmv_hdmi_control(uint8_t _en);
int kvmv_edid_maintenance(uint8_t pause);
/* Stream rate the server will request; VPSS drops surplus input frames. */
int kvmv_set_capture_fps(uint8_t fps);
uint8_t kvmv_hdmi_signal_active(void);
/* Parks (1) or resumes (0) the VPSS outputs while nobody reads frames. A
   read resumes them by itself. */
int kvmv_set_capture_parked(uint8_t parked);

#ifdef __cplusplus
}
#endif

#endif // KVM_VISION_H_
