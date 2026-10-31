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
static int holds = 0;
void mmf_jpg_hold() { ++holds; }
void mmf_jpg_unhold() { assert(holds > 0); --holds; }
static int capture_420; // 4:2:0 capture for H.264 straight from capture
int mmf_vi_yuv420_active() { return capture_420; }
static bool has_nv16;
bool nanokvm::Capture::has_format(int format) const { return format==nanokvm::nv16_format() && has_nv16; }
void mmf_vi_frame_release(int ch) {assert(ch==0 && !active);++released;}
nanokvm::JpegBuffer::~JpegBuffer() {free(bytes_);}
int nanokvm::nv21_format() { return PIXEL_FORMAT_NV21; }
int nanokvm::nv16_format() { return PIXEL_FORMAT_NV16; }
nanokvm::I2c::~I2c() {} // Construction/destruction must not access host hardware.
// Tiled MJPEG: a change map and region JPEGs (kvm_mmf).
static int map_result, map_resets, region_errors;
static std::vector<std::pair<int,int>> map_marks; // changed blocks (column, row)
static std::vector<std::vector<int>> encoded_regions, kept_regions;
static uint8_t region_jpeg[] = {0xff,0xd8,0x00,0xff,0xd9};
static bool region_active, frame_supported = true;
bool mmf_tile_frame_supported(int vi) { assert(vi==0); return frame_supported; }
int mmf_tile_map(int vi,uint8_t *map,int map_size,int *cols,int *rows,int *changed) {
    assert(vi==0 && map_size>=120*68);
    if (map_result!=0) return map_result;
    memset(map,0,120*68); *cols=120; *rows=68; *changed=(int)map_marks.size();
    for (auto &mark : map_marks) map[mark.second*120+mark.first]=1;
    return 0;
}
void mmf_tile_map_reset() { ++map_resets; }
int mmf_tile_map_keep(int vi,int x,int y,int w,int h) { assert(vi==0); kept_regions.push_back({x,y,w,h}); return 0; }
int mmf_enc_jpg_region(int ch,int vi,int x,int y,int w,int h,int quality,uint8_t **data,int *size,int *recreated) {
    assert((ch==2 || ch==3) && vi==0 && quality==80 && !region_active && recreated);
    if (region_errors) return region_errors;
    encoded_regions.push_back({ch,x,y,w,h}); region_active=true;
    region_jpeg[2]=(uint8_t)encoded_regions.size(); *data=region_jpeg; *size=sizeof(region_jpeg); return 0;
}
int mmf_enc_jpg_region_free(int ch) { assert((ch==2 || ch==3) && region_active); region_active=false; return 0; }
static int tile_sink(uintptr_t context,const uint8_t *data,uint32_t size,uint32_t offset,uint32_t total) {
    assert(context==123 && offset==0 && size==total);
    copied.assign(data,data+size); return sink_error;
}
static unsigned le16(size_t at) { return copied[at] | copied[at+1]<<8; }
static void reset_tiles() {
    map_result=map_resets=region_errors=0; map_marks.clear(); encoded_regions.clear(); kept_regions.clear(); region_active=false;
}
static int sink(uintptr_t context,const uint8_t *data,uint32_t size,uint32_t offset,uint32_t total) {
    assert(context==123 && data==encoded && active && offset==0 && size==total && total==sizeof(encoded));
    copied.assign(data,data+size); return sink_error;
}
static int owned_sink(uintptr_t context,const uint8_t *data,uint32_t size,uint32_t offset,uint32_t total) {
    assert(context==123 && data && !active && offset==0 && size==total);
    copied.assign(data,data+size);return sink_error;
}
// The production code reads the environment once; read it again after a change.
static void env_changed() { mjpeg_422_env = mjpeg_force_copy_env = -1; }
static void reset() {
    pushes=pops=frees=deinit_count=released=0;
    push_error=pop_error=free_error=sink_error=0;active=false;encoded[2]=0x11;copied.clear();
    video_sink=nullptr;video_sink_context=0;
}
int main() {
    pthread_mutex_init(&vi_mutex, nullptr);
    unsetenv("NANOKVM_MJPEG_422"); unsetenv("NANOKVM_DIAGNOSTIC_FORCE_COPY"); env_changed();
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10000, 2560) == nanokvm::nv16_format());
    setenv("NANOKVM_MJPEG_422", "0", 1); env_changed();
    assert(mjpeg_capture_format(VENC_MJPEG, false, 10000, 2560) == nanokvm::nv21_format());
    setenv("NANOKVM_MJPEG_422", "1", 1); env_changed();
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
    setenv("NANOKVM_DIAGNOSTIC_FORCE_COPY", "1", 1); env_changed();
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12004, 2560) == nanokvm::nv21_format());
    unsetenv("NANOKVM_DIAGNOSTIC_FORCE_COPY"); env_changed();
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12005, 2560) == nanokvm::nv16_format());
    disable_mjpeg_422("fault injection");
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12006, 2560) == nanokvm::nv21_format());
    assert(set_mjpeg_chroma(1) == 0);
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12007, 2560) == nanokvm::nv16_format());
    assert(set_mjpeg_chroma(0) == 0);
    assert(mjpeg_capture_format(VENC_MJPEG, false, 12008, 2560) == nanokvm::nv21_format());
    assert(set_mjpeg_chroma(2) == -1 && mjpeg_chroma_choice == 0);
    // 4:2:0 capture: with the 4:2:2 choice MJPEG keeps its own output (NV16,
    // full-range BT.601, chroma at the capture's 4:2:0 resolution), and the
    // status says the capture is 4:2:0. Next to wide video it is limited to
    // 1920 as with 4:2:2 capture.
    assert(set_mjpeg_chroma(1) == 0);
    mjpeg_last_video_ms = 0; has_nv16 = true;
    assert(get_mjpeg_chroma_status() == MJPEG_CHROMA_ACTIVE_422);
    capture_420 = 1;
    assert(mjpeg_capture_format(VENC_MJPEG, false, 13000, 1920) == nanokvm::nv16_format());
    assert(mjpeg_capture_format(VENC_MJPEG, false, 13001, 2560) == nanokvm::nv16_format());
    assert(!mjpeg_422_disabled && mjpeg_422_frame_failures == 0);
    assert(get_mjpeg_chroma_status() == MJPEG_CHROMA_CAPTURE_420);
    mjpeg_last_video_ms = 12999; mjpeg_last_video_width = 2560;
    assert(mjpeg_parallel_size({2560,1440},VENC_MJPEG,false,13002).width == 1920 && mjpeg_resolution_limited);
    capture_420 = 0;
    assert(mjpeg_parallel_size({2560,1440},VENC_MJPEG,false,13003).width == 1920 && mjpeg_resolution_limited);
    mjpeg_last_video_ms = 0; mjpeg_resolution_limited = false;
    assert(mjpeg_capture_format(VENC_MJPEG, false, 13004, 1920) == nanokvm::nv16_format());
    assert(get_mjpeg_chroma_status() == MJPEG_CHROMA_ACTIVE_422);
    has_nv16 = false; mjpeg_last_jpeg_width = 0;
    assert(set_mjpeg_chroma(0) == 0);
    mjpeg_last_video_ms = 0;
    unsetenv("NANOKVM_MJPEG_422"); env_changed();
    kvmv_data_t frame{};
    reset();video_sink=sink;video_sink_context=123;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_MJPEG_TYPE);
    assert(copied[2]==0x11 && encoded[2]==0x99 && frees==1 && released==1);
    assert(frame.img_data_type==VENC_MJPEG && frame.img_data_size==sizeof(encoded));

    reset();video_sink=sink;video_sink_context=123;sink_error=-1;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_BUFFER_FULL);
    assert(frees==1 && released==1);

    reset();video_sink=sink;video_sink_context=123;free_error=-2;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_VENC_ERROR);
    assert(deinit_count==1 && released==1 && frame.img_data_size==0);

    reset();video_sink=sink;video_sink_context=123;pop_error=-2;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_VENC_ERROR);
    assert(copied.empty() && deinit_count==1 && released==1);

    reset();video_sink=sink;video_sink_context=123;push_error=-2;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_VENC_ERROR);
    assert(pops==0 && frees==0 && released==1);
    assert(holds == 0);

    // MJPEG's own output encodes outside vi_mutex: released after the submit
    // and on return; the JPEG hold covers submit to frame release.
    reset();video_sink=sink;video_sink_context=123;
    pthread_mutex_lock(&vi_mutex);
    assert(frame_to_jpeg(0,1920,1080,&frame,80,true)==IMG_MJPEG_TYPE);
    assert(pthread_mutex_trylock(&vi_mutex) == 0); pthread_mutex_unlock(&vi_mutex);
    assert(holds == 0 && frees==1 && released==1);
    reset();video_sink=sink;video_sink_context=123;push_error=-2;
    pthread_mutex_lock(&vi_mutex);
    assert(frame_to_jpeg(0,1920,1080,&frame,80,true)==IMG_VENC_ERROR);
    assert(pthread_mutex_trylock(&vi_mutex) == 0); pthread_mutex_unlock(&vi_mutex);
    assert(holds == 0 && released==1);

    // Frame-detect/copied capture also bypasses the second native JPEG copy.
    reset();video_sink=owned_sink;video_sink_context=123;
    auto *data=static_cast<uint8_t *>(malloc(sizeof(encoded)));memcpy(data,encoded,sizeof(encoded));
    nanokvm::JpegBuffer jpg(data,sizeof(encoded));
    assert(jpg_dump(&frame,&jpg) && copied[2]==0x11);
    sink_error=-1;assert(!jpg_dump(&frame,&jpg));
    reset();assert(!jpg_dump(&frame,&jpg));
    // Tiled MJPEG: one message of the tiles that cover changed blocks, the
    // last row moved inward; what was sent is kept for the next map.
    tile_request_t request{false, 24};
    tile_request=&request;
    reset();reset_tiles();video_sink=tile_sink;video_sink_context=123;
    map_marks={{9,3},{0,67}}; // x 144, y 48; the strip under the last whole block row
    pthread_mutex_lock(&vi_mutex);
    assert(frame_to_jpeg(0,1920,1080,&frame,80,true)==IMG_MJPEG_TILES);
    assert(pthread_mutex_trylock(&vi_mutex) == 0); pthread_mutex_unlock(&vi_mutex);
    assert(holds==0 && released==1 && pushes==0 && !region_active);
    assert((encoded_regions==std::vector<std::vector<int>>{{2,128,0,128,128},{2,0,952,128,128}}));
    assert((kept_regions==std::vector<std::vector<int>>{{128,0,128,128},{0,952,128,128}}));
    assert(copied.size()==8+2*12+2*sizeof(region_jpeg) && copied[0]==1 && copied[1]==0);
    assert(le16(2)==2 && le16(4)==1920 && le16(6)==1080);
    assert(le16(8)==128 && le16(10)==0 && le16(12)==128 && le16(14)==128 && le16(16)==sizeof(region_jpeg));
    assert(le16(20)==0 && le16(22)==952 && copied[8+24+2]==1 && copied[8+24+sizeof(region_jpeg)+2]==2);

    // Nothing changed: no message.
    reset();reset_tiles();video_sink=tile_sink;video_sink_context=123;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==5 && copied.empty() && released==1);

    // A key, or no kept frame yet: the whole picture on its own channel, kept whole.
    reset();reset_tiles();video_sink=tile_sink;video_sink_context=123;request.key=true;map_result=1;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_MJPEG_TILES);
    assert(map_resets==1 && copied[1]==KVMV_TILE_FLAG_WHOLE && le16(2)==1);
    assert((encoded_regions==std::vector<std::vector<int>>{{3,0,0,1920,1080}}));
    assert((kept_regions==std::vector<std::vector<int>>{{0,0,1920,1080}}));
    request.key=false;

    // More changed tiles than max_tiles: the whole picture.
    reset();reset_tiles();video_sink=tile_sink;video_sink_context=123;request.max_tiles=1;map_marks={{0,0},{20,0}};
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_MJPEG_TILES && copied[1]==KVMV_TILE_FLAG_WHOLE);
    request.max_tiles=24;

    // No change map: a bare JPEG of the whole picture, nothing kept.
    reset();reset_tiles();video_sink=tile_sink;video_sink_context=123;map_result=-1;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_MJPEG_TYPE);
    assert(copied.size()==sizeof(region_jpeg) && kept_regions.empty() && !region_active);

    // An encoder failure forgets the kept frame; the next read is whole.
    reset();reset_tiles();video_sink=tile_sink;video_sink_context=123;map_marks={{0,0}};region_errors=-2;
    assert(frame_to_jpeg(0,1920,1080,&frame,80,false)==IMG_VENC_ERROR && map_resets==1 && copied.empty() && released==1);

    // A frame the map does not take (packed from capture, wider than 1920)
    // goes the classic way, also on MJPEG's own output.
    reset();reset_tiles();video_sink=sink;video_sink_context=123;frame_supported=false;
    assert(frame_to_jpeg(0,2560,1440,&frame,80,false)==IMG_MJPEG_TYPE && pushes==1 && map_resets==1);
    reset();reset_tiles();video_sink=sink;video_sink_context=123;
    pthread_mutex_lock(&vi_mutex);
    assert(frame_to_jpeg(0,2560,1440,&frame,80,true)==IMG_MJPEG_TYPE && pushes==1);
    assert(pthread_mutex_trylock(&vi_mutex) == 0); pthread_mutex_unlock(&vi_mutex);
    frame_supported=true;
    tile_request=nullptr;

    puts("PASS real JPEG sink: callback before release, owned bytes, sink required, callback/encoder failures, encode outside the capture lock, frame-detect path, tiled messages.");
}
