// Exclusive, bounded qualification through the public capture API.
#include "kvm_vision.h"
static constexpr int VENC_H264=1,VENC_H265=2;
#include <cstdio>
#include <cstring>
#include <vector>
#include <time.h>
#include <unistd.h>
static double now() { timespec t{}; clock_gettime(CLOCK_MONOTONIC,&t); return t.tv_sec+t.tv_nsec/1e9; }
static int collect(uintptr_t ctx,const uint8_t *data,uint32_t size,uint32_t offset,uint32_t total) {
    auto &bytes=*reinterpret_cast<std::vector<uint8_t>*>(ctx);
    if (!data || !size || total>64*1024*1024 || offset>total || size>total-offset) return -1;
    if (!offset) bytes.clear();
    if (bytes.size()!=offset) return -1;
    try { bytes.insert(bytes.end(),data,data+size); } catch (...) { return -1; }
    return 0;
}
static int sampling(const std::vector<uint8_t> &data,int width,int height) {
    if(data.size()<4 || data[0]!=0xff || data[1]!=0xd8 || data[data.size()-2]!=0xff || data.back()!=0xd9) return -1;
    for(size_t p=2;p+4<=data.size();) {
        if(data[p++]!=0xff) return -1;
        while(p<data.size() && data[p]==0xff) ++p;
        if(p+3>data.size()) return -1;
        int marker=data[p++]; if(marker==0xda) break;
        int len=(data[p]<<8)|data[p+1]; if(len<2 || p+len>data.size()) return -1;
        if(marker==0xc0) {
            if(len<17 || ((data[p+3]<<8)|data[p+4])!=height || ((data[p+5]<<8)|data[p+6])!=width) return -1;
            return data[p+9];
        }
        p+=len;
    }
    return -1;
}
static void memory(const char *tag) {
    FILE *f=fopen("/sys/kernel/debug/ion/cvi_carveout_heap_dump/alloc_mem","r");
    unsigned long used=0; if(f){fscanf(f,"%lu",&used);fclose(f);} printf("MEM tag=%s bytes=%lu\n",tag,used);
}
int main(int argc,char **argv) {
    const bool smart=argc==2 && !strcmp(argv[1],"smart");
    setvbuf(stdout,nullptr,_IONBF,0);
    kvmv_init(0);set_frame_detact(0);set_mjpeg_chroma(1);kvmv_hdmi_control(1);
    if(set_h265_gop_mode(smart?1:0)!=0 || get_h265_gop_mode()!=(smart?1:0)) return 2;
    printf("GOP selected=%s\n",smart?"SmartP":"NormalP");
    const struct Trial {const char *name;int jw,jh,vw,vh,codec,sample;} cases[]={
        {"fhd-avc",1920,1080,1920,1080,VENC_H264,0x21},
        {"fhd-hevc",1920,1080,1920,1080,VENC_H265,0x21},
        {"qhd-jpeg-fhd-hevc",2560,1440,1920,1080,VENC_H265,0x21},
        {"fhd-jpeg-qhd-avc",1920,1080,2560,1440,VENC_H264,0x21},
        {"fhd-jpeg-qhd-hevc",1920,1080,2560,1440,VENC_H265,0x21},
        {"qhd-both-hevc",2560,1440,2560,1440,VENC_H265,0x21},
        {"qhd-both-avc",2560,1440,2560,1440,VENC_H264,0x21},
        {"qhd-jpeg-fhd-repeat",2560,1440,1920,1080,VENC_H265,0x21}};
    int failed=0,total=0;std::vector<uint8_t> jpeg,video;
    for(const auto &t:cases) {
        int pairs=0,errors=0,status=0;double start=now();
        char path[192];snprintf(path,sizeof(path),"/kvmapp/.mjpeg-dual-stage/runtime-evidence/%s-%s.%s",smart?"smart":"normal",t.name,t.codec==VENC_H265?"h265":"h264");
        FILE *file=fopen(path,"wb");if(!file){failed=1;break;}
        while(pairs<90 && now()-start<25) {
            jpeg.clear();int jr=kvmv_read_mjpeg_sink(t.jw,t.jh,80,collect,reinterpret_cast<uintptr_t>(&jpeg));
            if(jr || jpeg.empty()){++errors;if(errors<6)printf("RETRY jpeg=%d bytes=%zu\n",jr,jpeg.size());usleep(50000);continue;}
            const int actual_jw = pairs>2 && t.jw>1920 && t.vw>1920 ? 1920 : t.jw;
            const int actual_jh = actual_jw==t.jw ? t.jh : t.jh*actual_jw/t.jw;
            int sample=sampling(jpeg,actual_jw,actual_jh);
            if(pairs>2 && sample!=t.sample){printf("BAD JPEG case=%s pair=%d sample=%#x expected=%#x\n",t.name,pairs,sample,t.sample);status=1;break;}
            if(pairs==0) kvmv_request_keyframe();
            video.clear();int vr=kvmv_read_video_sink(t.vw,t.vh,t.codec,10000,30,50,collect,reinterpret_cast<uintptr_t>(&video));
            if(vr<0 || video.empty()){++errors;if(errors<6)printf("RETRY video=%d bytes=%zu\n",vr,video.size());usleep(50000);continue;}
            if(fwrite(video.data(),1,video.size(),file)!=video.size()){status=1;break;}
            if(pairs==10) {
                snprintf(path,sizeof(path),"/kvmapp/.mjpeg-dual-stage/runtime-evidence/%s-%s.jpg",smart?"smart":"normal",t.name);
                FILE *f=fopen(path,"wb");if(!f || fwrite(jpeg.data(),1,jpeg.size(),f)!=jpeg.size())status=1;if(f)fclose(f);
                memory(t.name);
            }
            if(pairs>2 && (((get_mjpeg_chroma_status()&MJPEG_CHROMA_ACTIVE_422)!=0)!=(t.sample==0x21))){status=1;break;}
            if(pairs>2 && t.jw>1920 && t.vw>1920 && !(get_mjpeg_chroma_status()&MJPEG_CHROMA_LIMIT_WIDTH)){status=1;break;}
            ++pairs;++total;
        }
        fclose(file);if(pairs!=90)status=1;
        printf("TRIAL name=%s pairs=%d errors=%d seconds=%.3f status=%d\n",t.name,pairs,errors,now()-start,status);
        failed+=status;if(status)break;
    }
    // Stop JPEG requests while video keeps reading: stale peer queues must not
    // block it; then let the native idle grace retire that output.
    if(!failed) {
        double start=now();int frames=0;
        while(frames<150 && now()-start<12) {
            video.clear();int r=kvmv_read_video_sink(1920,1080,VENC_H265,10000,30,50,collect,reinterpret_cast<uintptr_t>(&video));
            if(r>=0 && !video.empty())++frames;else usleep(20000);
        }
        printf("IDLE jpeg-stopped video=%d seconds=%.3f status=%d\n",frames,now()-start,frames==150?0:1);
        failed+=frames!=150;
        printf("RETIRED jpeg-state=%u\n",get_mjpeg_chroma_status());
        failed+=(get_mjpeg_chroma_status()&MJPEG_CHROMA_ACTIVE_422)!=0;
        usleep(2200000);
        jpeg.clear();int r=kvmv_read_mjpeg_sink(2560,1440,80,collect,reinterpret_cast<uintptr_t>(&jpeg));
        int sample=sampling(jpeg,2560,1440);
        printf("RECOVER single-qhd-jpeg result=%d sample=%#x state=%u\n",r,sample,get_mjpeg_chroma_status());
        failed+=r!=0 || sample!=0x21;
    }
    kvmv_hdmi_control(0);kvmv_deinit();memory("shutdown");
    printf("RESULT failed=%d pairs=%d\n",failed,total);return failed?1:0;
}
