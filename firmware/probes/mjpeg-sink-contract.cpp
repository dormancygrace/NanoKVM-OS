// Real capture JPEG paths with a fault-injected MMF provider. No hardware.
#include "../../support/sg2002/additional/kvm/src/kvm_vision.cpp"
#include <cassert>
#include "linux/cvi_comm_video.h"
#include <vector>
static int pushes, pops, frees, deinit_count, released;
static int push_error, pop_error, free_error, sink_error;
static bool active;
static uint8_t encoded[] = {0xff,0xd8,0x11,0x22,0xff,0xd9};
static std::vector<uint8_t> copied;
int mmf_enc_jpg_push_vi_with_quality(int ch,int vi,int quality) {
    assert(ch==0 && vi==0 && quality==80); ++pushes; active=!push_error; return push_error;
}
int mmf_enc_jpg_pop(int ch,uint8_t **data,int *size) {
    assert(ch==0 && active); ++pops; *data=encoded; *size=sizeof(encoded); return pop_error;
}
int mmf_enc_jpg_free(int ch) {
    assert(ch==0 && active); ++frees; if(!free_error)active=false;
    encoded[2]=0x99; return free_error;
}
int mmf_enc_jpg_deinit(int ch) {assert(ch==0);++deinit_count;active=false;return 0;}
void mmf_vi_frame_release(int ch) {assert(ch==0 && !active);++released;}
nanokvm::JpegBuffer::~JpegBuffer() {free(bytes_);}
int nanokvm::nv21_format() { return PIXEL_FORMAT_NV21; }
int nanokvm::nv16_format() { return PIXEL_FORMAT_NV16; }
nanokvm::I2c::~I2c() {} // Construction/destruction must not access host hardware.
static int sink(uintptr_t context,const uint8_t *data,uint32_t size,uint32_t offset,uint32_t total) {
    assert(context==123 && data==encoded && active && offset==0 && size==total && total==sizeof(encoded));
    copied.assign(data,data+size); return sink_error;
}
static int owned_sink(uintptr_t context,const uint8_t *data,uint32_t size,uint32_t offset,uint32_t total) {
    assert(context==123 && data && !active && offset==0 && size==total);
    copied.assign(data,data+size);return sink_error;
}
static void reset() {
    pushes=pops=frees=deinit_count=released=0;
    push_error=pop_error=free_error=sink_error=0;active=false;encoded[2]=0x11;copied.clear();
    video_sink=nullptr;video_sink_context=0;
}
int main() {
    pthread_mutex_init(&vi_mutex, nullptr);
    unsetenv("NANOKVM_MJPEG_422"); unsetenv("NANOKVM_DIAGNOSTIC_FORCE_COPY");
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10000, 2560) == nanokvm::nv16_format());
    setenv("NANOKVM_MJPEG_422", "0", 1);
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10000, 2560) == nanokvm::nv21_format());
    setenv("NANOKVM_MJPEG_422", "1", 1);
    mjpeg_last_video_ms=9000; mjpeg_last_video_width=2560;
    auto limited=mjpeg_parallel_size({2560,1440},VENC_MJPEG,false,9001);
    assert(limited.width==1920 && limited.height==1080 && mjpeg_resolution_limited);
    auto portrait=mjpeg_parallel_size({1440,2560},VENC_MJPEG,false,9002);
    assert(portrait.width==1440 && portrait.height==2560 && !mjpeg_resolution_limited);
    assert(mjpeg_parallel_size({1980,1114},VENC_MJPEG,false,9002).width==1920);
    auto expired=mjpeg_parallel_size({2560,1440},VENC_MJPEG,false,11000);
    assert(expired.width==2560 && expired.height==1440 && !mjpeg_resolution_limited);
    mjpeg_last_video_ms=0;
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10000, 2560) == nanokvm::nv16_format());
    assert(mjpeg_capture_format(VENC_H265, false, 10001, 2560) == nanokvm::nv21_format());
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10002, 2560) == nanokvm::nv21_format());
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10003, 1920) == nanokvm::nv16_format());
    assert(mjpeg_capture_format(VENC_H264, false, 10004, 1920) == nanokvm::nv21_format());
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10005, 2560) == nanokvm::nv16_format());
    mjpeg_last_video_ms = 10001; mjpeg_last_video_width = 2560;
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12001, 2560) == nanokvm::nv16_format());
    assert(mjpeg_capture_format(VENC_MJPEG, true, 12002, 2560) == nanokvm::nv21_format());
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12003, 2560) == nanokvm::nv16_format());
    setenv("NANOKVM_DIAGNOSTIC_FORCE_COPY", "1", 1);
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12004, 2560) == nanokvm::nv21_format());
    unsetenv("NANOKVM_DIAGNOSTIC_FORCE_COPY");
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12005, 2560) == nanokvm::nv16_format());
    disable_mjpeg_422("fault injection");
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12006, 2560) == nanokvm::nv21_format());
    assert(set_mjpeg_chroma(1) == 0);
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12007, 2560) == nanokvm::nv16_format());
    assert(set_mjpeg_chroma(0) == 0);
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12008, 2560) == nanokvm::nv21_format());
    assert(set_mjpeg_chroma(2) == -1 && mjpeg_chroma_choice == 0);
    mjpeg_last_video_ms = 0;
    unsetenv("NANOKVM_MJPEG_422");
    kvmv_data_t frame{};
    reset();frame.in_use=1;video_sink=sink;video_sink_context=123;
    assert(frame_to_jpeg(0,&frame,80)==IMG_MJPEG_TYPE);
    assert(copied[2]==0x11 && encoded[2]==0x99 && frees==1 && released==1);
    assert(frame.p_img_data==nullptr && frame.img_data_capacity==0);
    release_save_buffer(&frame);

    reset();frame.in_use=1;video_sink=sink;video_sink_context=123;sink_error=-1;
    assert(frame_to_jpeg(0,&frame,80)==IMG_BUFFER_FULL);
    assert(frees==1 && released==1 && !frame.in_use);

    reset();frame.in_use=1;video_sink=sink;video_sink_context=123;free_error=-2;
    assert(frame_to_jpeg(0,&frame,80)==IMG_VENC_ERROR);
    assert(deinit_count==1 && released==1 && !frame.in_use);

    reset();frame.in_use=1;video_sink=sink;video_sink_context=123;pop_error=-2;
    assert(frame_to_jpeg(0,&frame,80)==IMG_VENC_ERROR);
    assert(copied.empty() && deinit_count==1 && released==1 && !frame.in_use);

    reset();frame.in_use=1;video_sink=sink;video_sink_context=123;push_error=-2;
    assert(frame_to_jpeg(0,&frame,80)==IMG_VENC_ERROR);
    assert(pops==0 && frees==0 && released==1 && !frame.in_use);

    // Legacy callers still receive an independent C savebuffer.
    reset();frame.in_use=1;
    assert(frame_to_jpeg(0,&frame,80)==IMG_MJPEG_TYPE);
    assert(frame.p_img_data && frame.p_img_data[2]==0x11 && encoded[2]==0x99);
    release_save_buffer(&frame);free(frame.p_img_data);frame={};

    // Frame-detect/copied capture also bypasses the second native JPEG copy.
    reset();frame.in_use=1;video_sink=owned_sink;video_sink_context=123;
    auto *data=static_cast<uint8_t *>(malloc(sizeof(encoded)));memcpy(data,encoded,sizeof(encoded));
    nanokvm::JpegBuffer jpg(data,sizeof(encoded));
    assert(jpg_dump(&frame,&jpg) && copied[2]==0x11 && frame.p_img_data==nullptr);
    sink_error=-1;assert(!jpg_dump(&frame,&jpg));release_save_buffer(&frame);
    puts("PASS real JPEG sink: callback before release, owned bytes, no savebuffer allocation, legacy path, callback/encoder failures, frame-detect path.");
}
