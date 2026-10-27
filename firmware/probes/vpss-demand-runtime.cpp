// Exclusive device qualification; run with the server stopped and output in RAM.
#include "kvm_vision.h"
#include "kvm_mmf.hpp"
#include <vector>
#include <string>
static double now() { timespec t{}; clock_gettime(CLOCK_MONOTONIC,&t); return t.tv_sec+t.tv_nsec/1e9; }
static int collect(uintptr_t context,const uint8_t *data,uint32_t size,uint32_t offset,uint32_t total) {
    auto &bytes=*reinterpret_cast<std::vector<uint8_t> *>(context);
    if(!data || !size || total>16*1024*1024 || offset>total || size>total-offset) return -1;
    if(!offset) bytes.clear();
    if(bytes.size()!=offset) return -1;
    try { bytes.insert(bytes.end(),data,data+size); } catch(...) { return -1; }
    return 0;
}
static bool bound() {
    FILE *f=fopen("/proc/cvitek/sys","r"); if(!f) return true;
    char line[512]; bool found=false;
    while(fgets(line,sizeof(line),f)) if(strstr(line,"VI ") && strstr(line,"VPSS")) found=true;
    fclose(f); return found;
}
static void counters(const char *tag) {
    printf("COUNTERS tag=%s bound=%d direct=%d,%d nv21=%d\n",tag,bound(),mmf_vi_chn_direct(0),mmf_vi_chn_direct(1),mmf_vi_yuv420_active());
    for(const char *path : {"/proc/cvitek/vi","/proc/cvitek/vpss"}) {
        FILE *f=fopen(path,"r"); char line[512]; bool row=false;
        while(f && fgets(line,sizeof(line),f)) {
            if(row) { printf("%s %s",path,line); break; }
            row=strstr(line,"LostFrame") || strstr(line,"StartFailCnt");
        }
        if(f) fclose(f);
    }
}
struct Trial { const char *name; int codec,w,h,fps,jw,jh; bool bind; int chroma=1; };
static int trial(const Trial &t,const char *directory) {
    printf("BEGIN %s\n",t.name);
    kvmv_set_capture_fps(t.fps);
    set_mjpeg_chroma(t.chroma);
    std::vector<uint8_t> video,jpeg;
    double start=now(),last_jpeg=0,ready=0; int good=0,errors=0,jpegs=0;
    auto read=[&]() {
        if(t.jw && now()-last_jpeg>0.1) {
            jpeg.clear(); int result=kvmv_read_mjpeg_sink(t.jw,t.jh,80,collect,reinterpret_cast<uintptr_t>(&jpeg));
            if(result==0 && jpeg.size()>4 && jpeg[0]==0xff && jpeg[1]==0xd8) ++jpegs;
            else ++errors;
            last_jpeg=now();
        }
        video.clear(); int result=kvmv_read_video_sink_v2(t.w,t.h,t.codec,10000,t.fps,collect,reinterpret_cast<uintptr_t>(&video));
        if(result>=0 && !video.empty()) ++good; else { ++errors; usleep(20000); }
        return result;
    };
    // Warm through geometry/codec changes and the existing two-second reader grace.
    while(now()-start<30 && (!ready || now()-ready<3)) { read(); if(good>=10 && !ready) ready=now(); }
    if(!ready) { printf("FAIL startup %s errors=%d\n",t.name,errors); return 1; }
    counters(t.name); good=errors=jpegs=0; start=now();
    std::string path=std::string(directory)+"/"+t.name+(t.codec==1?".h264":".h265");
    FILE *file=fopen(path.c_str(),"wb"); if(!file)return 1;
    kvmv_request_keyframe(); bool writing=false; int saved=0;
    while(now()-start<6) {
        const int result=read();
        if(result==IMG_VIDEO_TYPE_KEY) writing=true;
        if(writing && saved<20 && result>=0 && !video.empty()) {
            if(fwrite(video.data(),1,video.size(),file)!=video.size()) { fclose(file); return 1; }
            ++saved;
        }
    }
    fclose(file);
    counters(t.name);
    const bool direct=mmf_vi_chn_direct(0)||mmf_vi_chn_direct(1);
    const int failed=good<30 || errors!=0 || bound()!=t.bind || (!t.bind && !direct) || (t.jw && !jpegs) || !saved;
    printf("TRIAL name=%s frames=%d errors=%d jpegs=%d seconds=%.3f bound=%d direct=%d nv21=%d saved=%d status=%d\n",
        t.name,good,errors,jpegs,now()-start,bound(),direct,mmf_vi_yuv420_active(),saved,failed);
    return failed;
}
int main(int argc,char **argv) {
    if(argc!=2)return 2;
    setvbuf(stdout,nullptr,_IONBF,0);
    kvmv_init(0); set_frame_detact(0); set_mjpeg_chroma(1); kvmv_hdmi_control(1);
    const Trial cases[]={
        {"hevc-direct",2,2560,1440,60,0,0,false},
        {"hevc-mjpeg",2,2560,1440,60,1280,720,true},
        {"hevc-return",2,2560,1440,60,0,0,false},
        {"avc-direct",1,2560,1440,60,0,0,false},
        // Colour-corrected JPEG keeps its own VPSS output even on NV21 capture.
        {"avc-mjpeg",1,2560,1440,60,2560,1440,true},
        // The legacy NV21 reader instead shares VI directly with H.264.
        {"avc-jpeg-direct",1,2560,1440,60,2560,1440,false,0},
        {"avc-scaled",1,1280,720,60,0,0,true},
        {"hevc-thinned",2,2560,1440,30,0,0,true}};
    int failed=0;
    for(const auto &t:cases) if((failed=trial(t,argv[1]))) break;
    if(!failed) {
        int parked=kvmv_set_capture_parked(1); usleep(200000);
        counters("parked"); failed=parked!=0 || bound();
        printf("PARK result=%d bound=%d status=%d\n",parked,bound(),failed);
        if(!failed)failed=trial({"resume-hevc-direct",2,2560,1440,60,0,0,false},argv[1]);
    }
    kvmv_deinit(); printf("RESULT status=%d\n",failed); return failed;
}
