#define _DEFAULT_SOURCE
#include <unistd.h>
#include "nk-capture-protocol.h"
#include "nk-capture-abi.h"
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef int(*sink_fn)(uintptr_t,const uint8_t*,uint32_t,uint32_t,uint32_t);
static uint32_t calls[16];static uint8_t mode,chroma,keyframe;
static void trace(const char*operation,unsigned value){
    const char*path=getenv("NK_FIXTURE_TRACE_FILE");
    if(path){FILE*f=fopen(path,"a");if(f){fprintf(f,"%s:%u\n",operation,value);fclose(f);}}
}
static int fault(const char*name,unsigned value){const char*s=getenv(name);return s&&strtoul(s,NULL,10)==value;}

uint32_t nk_fixture_calls(uint8_t operation){return operation<16?calls[operation]:0;}
void kvmv_init(uint8_t debug){trace("init",debug);calls[NK_INIT]++;}
void kvmv_deinit(void){
    trace("close",0);calls[NK_CLOSE]++;
    /* Test-only local marker proves graceful deinit rather than SIGKILL. */
    const char*path=getenv("NK_FIXTURE_CLOSE_FILE");
    if(path){FILE*f=fopen(path,"w");if(f){fputs("deinitialized\n",f);fclose(f);}}
}
int kvmv_hdmi_control(uint8_t enabled){trace("hdmi",enabled);calls[NK_HDMI]++;if(fault("NK_FIXTURE_HDMI_STALL",enabled))usleep(5000000);return fault("NK_FIXTURE_HDMI_FAIL",enabled)?-5:0;}
uint8_t kvmv_hdmi_signal_active(void){trace("signal",1);calls[NK_SIGNAL]++;return 1;}
void set_h264_gop(uint8_t value){trace("gop",value);calls[NK_GOP]++;}
int8_t set_h265_gop_mode(uint8_t value){trace("mode",value);mode=value;return 0;}
uint8_t get_h265_gop_mode(void){trace("get-mode",mode);calls[NK_GOP_MODE]++;return mode;}
int set_mjpeg_chroma(uint8_t value){trace("chroma",value);calls[NK_CHROMA]++;chroma=value;return 0;}
uint8_t get_mjpeg_chroma_status(void){trace("get-chroma",chroma);calls[NK_CHROMA_STATUS]++;const char*s=getenv("NK_FIXTURE_CHROMA_FLAGS");return s?(uint8_t)strtoul(s,NULL,10):chroma;}
void set_frame_detact(uint8_t value){trace("detect",value);calls[NK_FRAME_DETECT]++;}
void kvmv_request_keyframe(void){trace("keyframe",1);calls[NK_KEYFRAME]++;keyframe=1;}
int kvmv_edid_maintenance(uint8_t value){trace("edid",value);calls[NK_EDID]++;return fault("NK_FIXTURE_EDID_FAIL",value)?-5:0;}
static int frame(uint16_t variant,sink_fn sink,uintptr_t context,int success){
    trace("frame",variant);const uint8_t bytes[]="synthetic-frame";uint32_t total=(uint32_t)strlen((const char*)bytes);
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
static int fixture_jpeg(const char*path,sink_fn sink,uintptr_t context){
    const char*hold=getenv("NK_FIXTURE_MJPEG_HOLD");if(hold&&access(hold,F_OK)==0)trace("jpeg-hold",calls[NK_MJPEG]);while(hold&&access(hold,F_OK)==0)usleep(5000);
    const char*status_path=getenv("NK_FIXTURE_MJPEG_STATUS_FILE");
    if(status_path){FILE*s=fopen(status_path,"r");if(s){int status=0;int scanned=fscanf(s,"%d",&status);fclose(s);if(scanned==1&&status!=0)return status;}}
    FILE*f=fopen(path,"rb");if(!f)return -2;
    if(fseek(f,0,SEEK_END)){fclose(f);return -2;}long length=ftell(f);
    if(length<=0||(unsigned long)length>NK_FRAME_MAX||fseek(f,0,SEEK_SET)){fclose(f);return -2;}
    uint8_t*data=malloc((size_t)length);if(!data){fclose(f);return -2;}
    size_t read=fread(data,1,(size_t)length,f);int closed=fclose(f);
    if(read!=(size_t)length||closed){free(data);return -2;}
    const uint8_t prefix[]={0xff,0xd8,0xff,0xe9,0,4};
    if(getenv("NK_FIXTURE_MJPEG_COUNTER")&&length>8&&!memcmp(data,prefix,sizeof(prefix))){data[6]=(uint8_t)(calls[NK_MJPEG]>>8);data[7]=(uint8_t)calls[NK_MJPEG];}
    int result=sink(context,data,(uint32_t)length,0,(uint32_t)length);free(data);return result?-2:0;
}
int kvmv_read_mjpeg_sink(uint16_t width,uint16_t height,uint16_t quality,sink_fn sink,uintptr_t context){
    calls[NK_MJPEG]++;const char*path=getenv("NK_FIXTURE_MJPEG_DATA");
    if(path){trace("mjpeg",quality);trace("jpeg-width",width);trace("jpeg-height",height);return fixture_jpeg(path,sink,context);}
    return frame(quality,sink,context,0);
}
int kvmv_read_video_sink(uint16_t width,uint16_t height,uint8_t codec,uint16_t bitrate,uint8_t gop,uint8_t fps,sink_fn sink,uintptr_t context){(void)width;(void)height;(void)gop;calls[NK_VIDEO]++;trace("codec",codec);trace("bitrate",bitrate);trace("fps",fps);const char*hold=getenv("NK_FIXTURE_VIDEO_HOLD");while(hold&&access(hold,F_OK)==0)usleep(5000);const char*override=getenv("NK_FIXTURE_VIDEO_STATUS");int status=override?atoi(override):codec==1?3:4;if(getenv("NK_FIXTURE_KEYFRAMES")){status=keyframe?3:4;keyframe=0;}return frame(0,sink,context,status);}
