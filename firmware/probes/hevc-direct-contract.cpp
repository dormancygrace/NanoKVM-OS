// Fault injection of the production VI-direct state and leases; no hardware.
#include "../../support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp"
#include <cassert>
static int disable_error, enable_error, release_error, vi_releases, vpss_releases;
static VIDEO_FRAME_INFO_S input;
static VPSS_CHN_ATTR_S output;
CVI_S32 CVI_VI_GetChnAttr(VI_PIPE, VI_CHN, VI_CHN_ATTR_S *a) {
    *a = {}; a->stSize = {1080,1920}; return 0;
}
CVI_S32 CVI_VPSS_GetChnAttr(VPSS_GRP, VPSS_CHN, VPSS_CHN_ATTR_S *a) { *a=output; return 0; }
CVI_S32 CVI_VPSS_DisableChn(VPSS_GRP, VPSS_CHN) { return disable_error; }
CVI_S32 CVI_VPSS_EnableChn(VPSS_GRP, VPSS_CHN) { return enable_error; }
CVI_S32 CVI_VI_GetChnFrame(VI_PIPE, VI_CHN, VIDEO_FRAME_INFO_S *f, CVI_S32) { *f=input; return 0; }
CVI_S32 CVI_VPSS_GetChnFrame(VPSS_GRP, VPSS_CHN, VIDEO_FRAME_INFO_S *, CVI_S32) { abort(); }
CVI_S32 CVI_VI_ReleaseChnFrame(VI_PIPE, VI_CHN, const VIDEO_FRAME_INFO_S *) {
    ++vi_releases; return release_error;
}
CVI_S32 CVI_VPSS_ReleaseChnFrame(VPSS_GRP, VPSS_CHN, const VIDEO_FRAME_INFO_S *) {
    ++vpss_releases; return release_error;
}
CVI_S32 CVI_SYS_Munmap(void *, CVI_U32) { abort(); }
extern "C" FILE *__wrap_fopen(const char *path, const char *) {
    if (strcmp(path,"/etc/kvm/hevc_vi_direct")==0) return nullptr;
    assert(strcmp(path,"/sys/module/cvi_vc_driver/parameters/nk_packed_src")==0);
    FILE *f=tmpfile(); assert(f); fputs("1",f); rewind(f); return f;
}
int main(int argc, char **argv) {
    assert(argc==2);
    priv={}; priv.vi_chn_is_inited[0]=true;
    output={}; output.enPixelFormat=PIXEL_FORMAT_NV21;
    output.u32Width=1080; output.u32Height=1920;
    if (strcmp(argv[1],"switch")==0) {
        disable_error=-41;
        assert(_mmf_vi_direct_set(0,true)==-41 && !vi_direct[0]);
        disable_error=0;
        assert(_mmf_vi_direct_set(0,true)==0 && vi_direct[0]);
        vi_direct_venc=1; enable_error=-42;
        _mmf_vi_direct_off_all();
        assert(vi_direct[0] && vi_direct_venc==1);
        _mmf_vi_direct_yield(0);
        assert(vi_direct[0] && vi_direct_venc==1);
        enable_error=0;
        _mmf_vi_direct_yield(0);
        assert(!vi_direct[0] && vi_direct_venc==-1);
        output.bMirror=CVI_TRUE; assert(_mmf_vi_direct_set(0,true)!=0);
        output.bMirror=CVI_FALSE; output.stFrameRate={60,30};
        assert(_mmf_vi_direct_set(0,true)!=0);
    } else {
        vi_direct[0]=true; vi_direct_venc=1;
        input={}; input.stVFrame.u32Width=1080; input.stVFrame.u32Height=1920;
        input.stVFrame.u32Stride[0]=1088; // vendor reports NV21, DMA actually packs UYVY
        input.stVFrame.u32Length[0]=1088*1920; input.stVFrame.u64PhyAddr[0]=0x123400;
        int len=0,w=0,h=0,fmt=0;
        if (strcmp(argv[1],"stride")==0) {
            assert(_mmf_acquire_vi_frame(0,&len,&w,&h,&fmt,0)==0);
            assert(priv.vi_frame[0].stVFrame.u32Stride[0]==2176);
            assert(len==2176*1920 && w==1080 && h==1920 && fmt==PIXEL_FORMAT_NV21);
            assert(vi_frame_from_vi[0] && priv.vi_frame_valid[0]);
            // Changing the active path must not change a frame's release owner.
            assert(_mmf_vi_direct_set(0,false)==0);
            assert(_mmf_release_vi_frame(0)==0);
            assert(vi_releases==1 && vpss_releases==0 && !priv.vi_frame_valid[0]);
        } else {
            assert(strcmp(argv[1],"invalid")==0);
            input.stVFrame.u64PhyAddr[0]=0; release_error=-43;
            assert(_mmf_acquire_vi_frame(0,&len,&w,&h,&fmt,0)!=0);
            assert(priv.vi_frame_valid[0] && vi_frame_from_vi[0]);
            release_error=0;
            assert(_mmf_release_vi_frame(0)==0 && vi_releases==2 && !priv.vi_frame_valid[0]);
        }
    }
    puts("PASS real HEVC direct path: aligned pitch, switch failures and lease ownership");
}
