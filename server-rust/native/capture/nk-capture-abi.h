#ifndef NANOKVM_CAPTURE_ABI_H
#define NANOKVM_CAPTURE_ABI_H
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
/* C declarations match the retained C++ extern-C ABI. The upstream header uses
 * a C++ default argument on init, so it cannot be included by a C compiler. */
typedef int (*kvmv_video_sink)(uintptr_t,const uint8_t*,uint32_t,uint32_t,uint32_t);
extern void kvmv_init(uint8_t);
extern void kvmv_deinit(void);
extern int kvmv_hdmi_control(uint8_t);
extern uint8_t kvmv_hdmi_signal_active(void);
extern void set_h264_gop(uint8_t);
extern int8_t set_h265_gop_mode(uint8_t);
extern uint8_t get_h265_gop_mode(void);
extern int set_mjpeg_chroma(uint8_t);
extern uint8_t get_mjpeg_chroma_status(void);
extern void kvmv_request_keyframe(void);
extern void set_frame_detact(uint8_t);
extern int kvmv_edid_maintenance(uint8_t);
extern int kvmv_read_mjpeg_sink(uint16_t,uint16_t,uint16_t,kvmv_video_sink,uintptr_t);
extern int kvmv_read_video_sink(uint16_t,uint16_t,uint8_t,uint16_t,uint8_t,uint8_t,kvmv_video_sink,uintptr_t);
#ifdef __cplusplus
}
#endif
#endif
