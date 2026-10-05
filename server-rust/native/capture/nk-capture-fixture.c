#define _DEFAULT_SOURCE
#include <unistd.h>
#include "nk-capture-protocol.h"
#include "nk-capture-abi.h"
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef int(*sink_fn)(uintptr_t,const uint8_t*,uint32_t,uint32_t,uint32_t);
static uint32_t calls[16];static uint8_t mode,chroma;
uint32_t nk_fixture_calls(uint8_t operation){return operation<16?calls[operation]:0;}
void kvmv_init(uint8_t debug){(void)debug;calls[NK_INIT]++;}
void kvmv_deinit(void){
    calls[NK_CLOSE]++;
    /* Test-only local marker proves graceful deinit rather than SIGKILL. */
    const char*path=getenv("NK_FIXTURE_CLOSE_FILE");
    if(path){FILE*f=fopen(path,"w");if(f){fputs("deinitialized\n",f);fclose(f);}}
}
int kvmv_hdmi_control(uint8_t enabled){calls[NK_HDMI]++;return enabled?0:0;}
uint8_t kvmv_hdmi_signal_active(void){calls[NK_SIGNAL]++;return 1;}
void set_h264_gop(uint8_t value){(void)value;calls[NK_GOP]++;}
int8_t set_h265_gop_mode(uint8_t value){mode=value;return 0;}
uint8_t get_h265_gop_mode(void){calls[NK_GOP_MODE]++;return mode;}
int set_mjpeg_chroma(uint8_t value){calls[NK_CHROMA]++;chroma=value;return 0;}
uint8_t get_mjpeg_chroma_status(void){calls[NK_CHROMA_STATUS]++;return chroma;}
void set_frame_detact(uint8_t value){(void)value;calls[NK_FRAME_DETECT]++;}
void kvmv_request_keyframe(void){calls[NK_KEYFRAME]++;}
int kvmv_edid_maintenance(uint8_t value){(void)value;calls[NK_EDID]++;return 0;}
static int frame(uint16_t variant,sink_fn sink,uintptr_t context,int success){
    const uint8_t bytes[]="synthetic-frame";uint32_t total=(uint32_t)strlen((const char*)bytes);
    if(variant==87)usleep(5000000);
    if(variant==85)return -5;
    if(variant==84){(void)sink(context,bytes,1,0,NK_FRAME_MAX+1);return success;}
    if(variant==83){(void)sink(context,bytes,0,0,total);return success;}
    if(sink(context,bytes,4,0,total))return -1;
    if(variant==88)usleep(5000000);
    if(variant==81)return success;
    uint32_t offset=variant==80?3:4;uint32_t claimed=variant==82?total+1:total;
    if(sink(context,bytes+4,total-4,offset,claimed))return -1;
    return variant==86?-2:success;
}
int kvmv_read_mjpeg_sink(uint16_t width,uint16_t height,uint16_t quality,sink_fn sink,uintptr_t context){(void)width;(void)height;calls[NK_MJPEG]++;return frame(quality,sink,context,0);}
int kvmv_read_video_sink(uint16_t width,uint16_t height,uint8_t codec,uint16_t bitrate,uint8_t gop,uint8_t fps,sink_fn sink,uintptr_t context){(void)width;(void)height;(void)bitrate;(void)gop;(void)fps;calls[NK_VIDEO]++;return frame(0,sink,context,codec==1?3:4);}
