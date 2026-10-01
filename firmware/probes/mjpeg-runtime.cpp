// Bounded device qualification of the real borrowed JPEG path and live Qfactor.
#include "kvm_vision.h"
extern "C" {
#include "cvi_venc.h"
}
#include <cerrno>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <vector>
#include <time.h>
static double now() { timespec t{}; clock_gettime(CLOCK_MONOTONIC,&t); return t.tv_sec+t.tv_nsec/1e9; }
static int copy_frame(uintptr_t context,const uint8_t *bytes,uint32_t size,uint32_t offset,uint32_t total) {
    if(!bytes || !size || size!=total || offset || total>64*1024*1024) return -1;
    auto *owned=reinterpret_cast<std::vector<uint8_t> *>(context);
    try { owned->assign(bytes,bytes+size); } catch(...) { return -1; }
    return 0;
}
static bool inspect(const std::vector<uint8_t> &data,unsigned *width,unsigned *height,unsigned *sampling,uint64_t *tables) {
    if(data.size()<4 || data[0]!=0xff || data[1]!=0xd8 || data[data.size()-2]!=0xff || data.back()!=0xd9) return false;
    bool sof=false,dqt=false;*tables=1469598103934665603ULL;
    for(size_t pos=2;pos+4<=data.size();) {
        if(data[pos++]!=0xff) return false;
        while(pos<data.size() && data[pos]==0xff)++pos;
        if(pos+3>data.size())return false;
        unsigned marker=data[pos++]; if(marker==0xda)break;
        if(marker==0x01 || (marker>=0xd0 && marker<=0xd9))continue;
        unsigned length=(data[pos]<<8)|data[pos+1];if(length<2 || pos+length>data.size())return false;
        if(marker==0xdb) {
            dqt=true;for(size_t i=pos+2;i<pos+length;++i){*tables^=data[i];*tables*=1099511628211ULL;}
        }
        if(marker==0xc0 || marker==0xc1 || marker==0xc2) {
            if(length<17 || data[pos+7]!=3)return false;
            *height=(data[pos+3]<<8)|data[pos+4];*width=(data[pos+5]<<8)|data[pos+6];
            *sampling=data[pos+9];if(data[pos+12]!=0x11 || data[pos+15]!=0x11)return false;sof=true;
        }
        pos+=length;
    }
    return sof && dqt;
}
int main(int argc,char **argv) {
    if(argc!=4 || (strcmp(argv[1],"nv21") && strcmp(argv[1],"nv16")))return 2;
    unsigned width=static_cast<unsigned>(strtoul(argv[2],nullptr,10));
    unsigned height=static_cast<unsigned>(strtoul(argv[3],nullptr,10));
    if(!width || !height || width>2560 || height>2560 || width%2 || height%2)return 2;
    bool nv16=!strcmp(argv[1],"nv16");setenv("NANOKVM_MJPEG_422",nv16?"1":"0",1);
    unsetenv("NANOKVM_DIAGNOSTIC_FORCE_COPY");setvbuf(stdout,nullptr,_IONBF,0);
    kvmv_init(0);set_frame_detact(0);kvmv_hdmi_control(1);
    const int qualities[]={51,60,80,99};const double start=now();int errors=0,status=0,total_frames=0;
    struct Trial { bool nv16; unsigned width,height; };
    const Trial trials[]={{false,1920,1080},{true,1920,1080},{false,1280,720},{true,1280,720},{false,2560,1440},{true,2560,1440},{false,1920,1080}};
    std::vector<uint8_t> jpeg;
    for(const auto &trial:trials) {
      nv16=trial.nv16;width=trial.width;height=trial.height;
      if(set_mjpeg_chroma(nv16?1:0)) {status=1;break;}
      const char *mode=nv16?"nv16":"nv21";
      int frames=0;const double trial_start=now();
      while(frames<48 && now()-trial_start<20) {
        jpeg.clear();int q=qualities[frames/12];double before=now();
        int result=kvmv_read_mjpeg_sink(width,height,q,copy_frame,reinterpret_cast<uintptr_t>(&jpeg));
        if(result) {++errors;usleep(20000);continue;}
        unsigned actual_w=0,actual_h=0,sampling=0;uint64_t tables=0;
        VENC_JPEG_PARAM_S params{};int queried=CVI_VENC_GetJpegParam(0,&params);
        if(!inspect(jpeg,&actual_w,&actual_h,&sampling,&tables) || sampling!=(nv16?0x21U:0x22U)
            || actual_w!=width || actual_h!=height || queried || params.u32Qfactor!=static_cast<unsigned>(q)) {
            printf("FAIL mode=%s frame=%d sampling=%#x q=%d actual_q=%u query=%#x\n",mode,frames,sampling,q,params.u32Qfactor,queried);status=1;break;
        }
        if(((get_mjpeg_chroma_status() & MJPEG_CHROMA_ACTIVE_422)!=0)!=nv16) {status=1;break;}
        printf("JPEG mode=%s frame=%d size=%zu width=%u height=%u sampling=%#x q=%d actual_q=%u dqt=%016llx ms=%.3f\n",
            mode,frames,jpeg.size(),actual_w,actual_h,sampling,q,params.u32Qfactor,static_cast<unsigned long long>(tables),(now()-before)*1000);
        if(frames%12==0) {
            char name[160];snprintf(name,sizeof(name),"/run/nanokvm-mjpeg-qualification/%s-%ux%u-q%d.jpg",mode,width,height,q);
            FILE *file=fopen(name,"wb");if(!file || fwrite(jpeg.data(),1,jpeg.size(),file)!=jpeg.size())status=1;
            if(file && fclose(file))status=1;
            if(status)break;
        }
        ++frames;++total_frames;
      }
      if(frames!=48)status=1;
      printf("TRIAL mode=%s width=%u height=%u frames=%d status=%d\n",mode,width,height,frames,status);
      if(status)break;
    }
    kvmv_hdmi_control(0);kvmv_deinit();
    printf("RESULT mode=%s frames=%d startup_or_capture_errors=%d elapsed=%.3f status=%d\n",argv[1],total_frames,errors,now()-start,status);
    return status;
}
