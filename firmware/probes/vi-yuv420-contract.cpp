// Fault injection of the production 4:2:0 capture mode (osdrv patch 0013) and
// the H.264 VI-direct path built on it; no hardware.
#include "../../support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp"
#include <cassert>
#include <string>

// A fake /sys and /etc: the VI parameter is a real temporary file.
static bool param_present = true, param_ignores_writes, packed_src = true;
static std::string setting; // "" absent
static char param_path[] = "/tmp/nk_yuv420-XXXXXX";
extern "C" FILE *__real_fopen(const char *, const char *);
static FILE *text(const char *content) {
    FILE *f = tmpfile(); assert(f); fputs(content, f); rewind(f); return f;
}
extern "C" FILE *__wrap_fopen(const char *path, const char *mode) {
    if (strcmp(path, vi_yuv420_param) == 0) {
        if (!param_present) return nullptr;
        if (param_ignores_writes) return mode[0] == 'w' ? tmpfile() : text("N\n");
        return __real_fopen(param_path, mode);
    }
    if (strcmp(path, "/etc/kvm/hevc_vi_direct") == 0) return setting.empty() ? nullptr : text(setting.c_str());
    if (strcmp(path, "/sys/module/cvi_vc_driver/parameters/nk_packed_src") == 0) return packed_src ? text("1\n") : nullptr;
    if (strcmp(path, "/proc/cvitek/vpss") == 0) return text("NV21 BT709\n");
    if (strcmp(path, "/run/nanokvm-venc-tune") == 0) return nullptr;
    fprintf(stderr, "unexpected fopen %s\n", path); abort();
}
static char param_value() {
    FILE *f = __real_fopen(param_path, "r"); assert(f); const int c = fgetc(f); fclose(f); return (char)c;
}

// The vendor side.
static VPSS_CHN_ATTR_S output;
static VIDEO_FRAME_INFO_S vi_input, vpss_input;
static PIXEL_FORMAT_E group_format;
static int vi_gets, vpss_gets, vi_releases, vpss_releases, sends, enables, disables;
static const VIDEO_FRAME_INFO_S *sent;
CVI_S32 CVI_VI_GetChnAttr(VI_PIPE, VI_CHN, VI_CHN_ATTR_S *a) { *a = {}; a->stSize = priv.vi_size; return 0; }
CVI_S32 CVI_VPSS_GetChnAttr(VPSS_GRP, VPSS_CHN, VPSS_CHN_ATTR_S *a) { *a = output; return 0; }
CVI_S32 CVI_VPSS_SetChnAttr(VPSS_GRP, VPSS_CHN, const VPSS_CHN_ATTR_S *a) { output = *a; return 0; }
CVI_S32 CVI_VPSS_DisableChn(VPSS_GRP, VPSS_CHN) { ++disables; return 0; }
CVI_S32 CVI_VPSS_EnableChn(VPSS_GRP, VPSS_CHN) { ++enables; return 0; }
CVI_S32 CVI_VI_GetChnFrame(VI_PIPE, VI_CHN, VIDEO_FRAME_INFO_S *f, CVI_S32) { ++vi_gets; *f = vi_input; return 0; }
CVI_S32 CVI_VPSS_GetChnFrame(VPSS_GRP, VPSS_CHN, VIDEO_FRAME_INFO_S *f, CVI_S32) { ++vpss_gets; *f = vpss_input; return 0; }
CVI_S32 CVI_VI_ReleaseChnFrame(VI_PIPE, VI_CHN, const VIDEO_FRAME_INFO_S *) { ++vi_releases; return 0; }
CVI_S32 CVI_VPSS_ReleaseChnFrame(VPSS_GRP, VPSS_CHN, const VIDEO_FRAME_INFO_S *) { ++vpss_releases; return 0; }
CVI_S32 CVI_VENC_SendFrame(VENC_CHN, const VIDEO_FRAME_INFO_S *f, CVI_S32) { ++sends; sent = f; return 0; }
CVI_S32 CVI_VPSS_CreateGrp(VPSS_GRP, const VPSS_GRP_ATTR_S *a) { group_format = a->enPixelFormat; return 0; }
CVI_S32 CVI_VPSS_DestroyGrp(VPSS_GRP) { return 0; }
CVI_S32 CVI_VPSS_ResetGrp(VPSS_GRP) { return 0; }
CVI_S32 CVI_VPSS_StartGrp(VPSS_GRP) { return 0; }
CVI_S32 CVI_VPSS_GetChnFd(VPSS_GRP, VPSS_CHN) { return 7; }
extern "C" CVI_S32 vpss_set_grp_csc(CVI_S32, const vpss_grp_csc_cfg *) { return 0; }
// Linked but not reached: no copy fallback, no mapping, no pool.
CVI_S32 CVI_SYS_Munmap(void *, CVI_U32) { abort(); }
void *CVI_SYS_MmapCache(CVI_U64, CVI_U32) { abort(); }
CVI_S32 CVI_SYS_IonFlushCache(CVI_U64, void *, CVI_U32) { abort(); }
VB_POOL CVI_VB_CreatePool(VB_POOL_CONFIG_S *) { abort(); }
CVI_S32 CVI_VB_DestroyPool(VB_POOL) { abort(); }
VB_BLK CVI_VB_GetBlock(VB_POOL, CVI_U32) { abort(); }
CVI_S32 CVI_VB_ReleaseBlock(VB_BLK) { abort(); }
CVI_U64 CVI_VB_Handle2PhysAddr(VB_BLK) { abort(); }
VB_POOL CVI_VB_Handle2PoolId(VB_BLK) { abort(); }

static mmf_venc_cfg_t video(int type, int w = 1920, int h = 1080) {
    mmf_venc_cfg_t cfg{}; cfg.type = (uint8_t)type; cfg.w = w; cfg.h = h; cfg.fmt = PIXEL_FORMAT_NV21;
    cfg.intput_fps = cfg.output_fps = 60; cfg.bitrate = 3000; return cfg;
}
// The NV21 frame as base_get_frame_info lays out a 1920x1080 VI channel.
static VIDEO_FRAME_INFO_S nv21_frame(unsigned w = 1920, unsigned h = 1080, unsigned pitch = 1920) {
    VIDEO_FRAME_INFO_S f{}; VIDEO_FRAME_S &v = f.stVFrame;
    v.enPixelFormat = PIXEL_FORMAT_NV21; v.u32Width = w; v.u32Height = h;
    v.u32Stride[0] = v.u32Stride[1] = pitch;
    v.u32Length[0] = pitch * h; v.u32Length[1] = pitch * h / 2;
    v.u64PhyAddr[0] = 0x8000000; v.u64PhyAddr[1] = v.u64PhyAddr[0] + v.u32Length[0];
    return f;
}
// A capture start as mmf_init and mmf_vi_init run it.
static void start(unsigned w = 1920, unsigned h = 1080) {
    priv.vi_size = {w, h};
    _mmf_vi_yuv420_start(priv.vi_size);
    assert(mmf_vi_init() == 0);
    priv.vi_is_inited = false;
}
static void reset() {
    priv = {}; vi_direct[0] = vi_direct[1] = false; vi_frame_from_vi[0] = vi_frame_from_vi[1] = false;
    vi_direct_venc = -1; vi_shared_reader_us[0] = vi_shared_reader_us[1] = 0;
    vi_yuv420_wanted = vi_yuv420_running = vi_yuv420_broken = vi_yuv420_refused = vpss_rate_thins = false;
    param_present = true; param_ignores_writes = false; packed_src = true; setting.clear();
    vi_gets = vpss_gets = vi_releases = vpss_releases = sends = enables = disables = 0; sent = nullptr;
    output = {}; output.enPixelFormat = PIXEL_FORMAT_NV21; output.u32Width = 1920; output.u32Height = 1080;
    output.stFrameRate = {60, 60};
    priv.vi_size = {1920, 1080}; priv.vi_chn_is_inited[0] = true;
    vi_input = nv21_frame(); vpss_input = nv21_frame(); vpss_input.stVFrame.u64PhyAddr[0] = 0x9000000;
    vpss_input.stVFrame.u64PhyAddr[1] = 0x9000000 + vpss_input.stVFrame.u32Length[0];
    FILE *f = __real_fopen(param_path, "w"); fputs("N\n", f); fclose(f);
}
static void mode_rule() {
    reset();
    mmf_venc_cfg_t h264 = video(2), h265 = video(1);
    assert(_mmf_vi_yuv420_rule(&h264) && !_mmf_vi_yuv420_rule(&h265));
    // Each condition alone keeps 4:2:2.
    { mmf_venc_cfg_t scaled = video(2, 1280, 720); assert(!_mmf_vi_yuv420_rule(&scaled)); }
    output.bMirror = CVI_TRUE; assert(!_mmf_vi_yuv420_rule(&h264)); output.bMirror = CVI_FALSE;
    output.bFlip = CVI_TRUE; assert(!_mmf_vi_yuv420_rule(&h264)); output.bFlip = CVI_FALSE;
    output.enPixelFormat = PIXEL_FORMAT_NV16; assert(!_mmf_vi_yuv420_rule(&h264)); output.enPixelFormat = PIXEL_FORMAT_NV21;
    setting = "0"; assert(!_mmf_vi_yuv420_rule(&h264)); setting = "1"; assert(_mmf_vi_yuv420_rule(&h264)); setting.clear();
    param_present = false; assert(!_mmf_vi_yuv420_rule(&h264)); param_present = true;
    priv.vi_chn_is_inited[0] = false; assert(!_mmf_vi_yuv420_rule(&h264)); priv.vi_chn_is_inited[0] = true;
    // Widths that are not a multiple of 64: the writer uses the padded pitch.
    priv.vi_size = {1440, 900}; output.u32Width = 1440; output.u32Height = 900;
    { mmf_venc_cfg_t narrow = video(2, 1440, 900); assert(_mmf_vi_yuv420_rule(&narrow)); }
    priv.vi_size = {1080, 1920}; output.u32Width = 1080; output.u32Height = 1920;
    { mmf_venc_cfg_t portrait = video(2, 1080, 1920); assert(_mmf_vi_yuv420_rule(&portrait)); }
    priv.vi_size = {2560, 1440}; output.u32Width = 2560; output.u32Height = 1440;
    { mmf_venc_cfg_t qhd = video(2, 2560, 1440); assert(_mmf_vi_yuv420_rule(&qhd)); }
    priv.vi_size = {1920, 1080}; output.u32Width = 1920; output.u32Height = 1080;
    // The stream rate: only a known rate changes the verdict.
    assert(mmf_vpss_set_rate(60, 30) == 0 && vpss_rate_thins && !_mmf_vi_yuv420_rule(&h264));
    assert(mmf_vpss_set_rate(0, 30) == 0 && vpss_rate_thins); // VI not measured yet after a restart
    assert(mmf_vpss_set_rate(60, 60) == 0 && !vpss_rate_thins && _mmf_vi_yuv420_rule(&h264));
    assert(mmf_vpss_set_rate(51, 50) == 0 && !vpss_rate_thins); // within the tolerance
    puts("rule ok");
}

static void restart_and_codec_switch() {
    reset();
    start();
    assert(!mmf_vi_yuv420_active() && param_value() == 'N' && group_format == PIXEL_FORMAT_UYVY);
    // An H.264 encoder at the input size asks for 4:2:0; applied by a restart only.
    mmf_venc_cfg_t h264 = video(2), h265 = video(1);
    _mmf_vi_yuv420_follow(&h264);
    assert(mmf_vi_yuv420_pending() && !mmf_vi_yuv420_active());
    start();
    assert(mmf_vi_yuv420_active() && !mmf_vi_yuv420_pending());
    assert(param_value() == 'Y' && group_format == PIXEL_FORMAT_NV21);
    // A new H.264 encoder in the same conditions: no further restart.
    _mmf_vi_yuv420_follow(&h264); assert(!mmf_vi_yuv420_pending());
    // H.265: back to 4:2:2 and the packed path.
    _mmf_vi_yuv420_follow(&h265); assert(mmf_vi_yuv420_pending());
    start();
    assert(!mmf_vi_yuv420_active() && param_value() == 'N' && group_format == PIXEL_FORMAT_UYVY);
    assert(_mmf_vi_direct_type() == 1);
    // And H.264 again.
    _mmf_vi_yuv420_follow(&h264); start(); assert(mmf_vi_yuv420_active() && _mmf_vi_direct_type() == 2);
    // The once-a-second recheck follows the setting through the video encoder ...
    priv.venc[1].is_inited = 1; priv.venc[1].type = 2; priv.venc[1].cfg = h264;
    setting = "0"; usleep(1100000); _mmf_vi_direct_recheck();
    assert(mmf_vi_yuv420_pending());
    setting.clear(); usleep(1100000); _mmf_vi_direct_recheck();
    assert(!mmf_vi_yuv420_pending());
    // ... but no encoder (MJPEG only) keeps the mode.
    priv.venc[1] = {}; setting = "0"; usleep(1100000); _mmf_vi_direct_recheck();
    assert(!mmf_vi_yuv420_pending() && mmf_vi_yuv420_active());
    // A capture start checks the setting itself; any input width keeps the mode.
    start(); assert(!mmf_vi_yuv420_active() && !mmf_vi_yuv420_pending());
    setting.clear(); _mmf_vi_yuv420_follow(&h264); start(1440, 900);
    assert(mmf_vi_yuv420_active() && param_value() == 'Y' && !mmf_vi_yuv420_pending());
    // A driver that does not take the value: no restart loop, the readback rules.
    start(); _mmf_vi_yuv420_follow(&h264); param_ignores_writes = true; start();
    assert(!mmf_vi_yuv420_active() && vi_yuv420_broken && !mmf_vi_yuv420_pending());
    _mmf_vi_yuv420_follow(&h264); assert(!mmf_vi_yuv420_pending());
    // Without the parameter (older VI driver) the capture stays 4:2:2.
    reset(); param_present = false; _mmf_vi_yuv420_follow(&h264); start();
    assert(!mmf_vi_yuv420_active() && !mmf_vi_yuv420_pending() && group_format == PIXEL_FORMAT_UYVY);
    puts("restart and codec switch ok");
}

// In 4:2:0 capture H.264 gets the VI frame as the VI describes it.
static void h264_direct() {
    reset(); mmf_venc_cfg_t h264 = video(2), h265 = video(1);
    _mmf_vi_yuv420_follow(&h264); start();
    priv.venc[1].is_inited = 1; priv.venc[1].type = 2; priv.venc[1].cfg = h264;
    _mmf_vi_direct_for_venc(1, &h264);
    assert(vi_direct[0] && vi_direct_venc == 1 && disables == 1);
    int len = 0, w = 0, h = 0, fmt = 0;
    assert(_mmf_acquire_vi_frame(0, &len, &w, &h, &fmt, 0) == 0);
    const VIDEO_FRAME_S &v = priv.vi_frame[0].stVFrame;
    // No relabel: NV21, two planes, the VI's own pitch and lengths.
    assert(v.enPixelFormat == PIXEL_FORMAT_NV21 && v.u32Stride[0] == 1920 && v.u32Stride[1] == 1920);
    assert(v.u64PhyAddr[1] == v.u64PhyAddr[0] + 1920 * 1080 && fmt == PIXEL_FORMAT_NV21);
    assert(len == 1920 * 1080 * 3 / 2 && w == 1920 && h == 1080);
    assert(vi_frame_from_vi[0] && vi_gets == 1 && vpss_gets == 0);
    priv.vi_frame_deferred[0] = true;
    assert(mmf_venc_push_vi(1, 0) == 0 && sends == 1 && sent == &priv.vi_frame[0]);
    assert(priv.venc_input_vi_ch[1] == 0);
    priv.venc[1].is_running = 0; assert(_mmf_release_vi_frame(0) == 0 && vi_releases == 1);
    // The H.264 encoder goes (mmf_del_venc_channel turns direct off); an H.265
    // encoder in 4:2:0 capture, until its restart, is not direct: a VI lease
    // taken before goes back and it gets the VPSS frame.
    assert(_mmf_acquire_vi_frame(0, &len, &w, &h, &fmt, 0) == 0); priv.vi_frame_deferred[0] = true;
    priv.venc[1] = {}; _mmf_vi_direct_off_all(); assert(!vi_direct[0] && vi_direct_venc == -1);
    priv.venc[2].is_inited = 1; priv.venc[2].type = 1; priv.venc[2].cfg = h265;
    _mmf_vi_direct_for_venc(2, &h265); assert(!vi_direct[0]);
    assert(mmf_venc_push_vi(2, 0) == 0 && sent->stVFrame.u64PhyAddr[0] == 0x9000000 && !vi_frame_from_vi[0]);
    assert(vi_releases == 2 && vpss_gets == 1);
    puts("h264 direct ok");
}

// In 4:2:2 capture H.265 behaves as before: packed, relabelled UYVY; H.264 stays on VPSS.
static void hevc_unchanged() {
    reset(); start(); mmf_venc_cfg_t h264 = video(2), h265 = video(1);
    _mmf_vi_direct_for_venc(1, &h264); assert(!vi_direct[0]);
    _mmf_vi_direct_for_venc(1, &h265); assert(vi_direct[0] && vi_direct_venc == 1);
    vi_input.stVFrame.u32Stride[0] = 1920; vi_input.stVFrame.u32Length[0] = 1920 * 1080;
    int len = 0, w = 0, h = 0, fmt = 0;
    assert(_mmf_acquire_vi_frame(0, &len, &w, &h, &fmt, 0) == 0);
    const VIDEO_FRAME_S &v = priv.vi_frame[0].stVFrame;
    assert(v.enPixelFormat == PIXEL_FORMAT_UYVY && v.u32Stride[0] == 3840 && v.u64PhyAddr[1] == 0);
    assert(fmt == PIXEL_FORMAT_NV21 && len == 3840 * 1080);
    puts("hevc unchanged ok");
}

// A 4:2:0 frame with another layout: logged once, released, VPSS from then on.
static void stride_guard() {
    for (int fault = 0; fault < 4; ++fault) {
        reset(); mmf_venc_cfg_t h264 = video(2);
        _mmf_vi_yuv420_follow(&h264); start();
        _mmf_vi_direct_for_venc(1, &h264); assert(vi_direct[0]);
        if (fault == 0) vi_input = nv21_frame(1920, 1080, 1984);              // pitch padded twice
        if (fault == 1) vi_input.stVFrame.u64PhyAddr[1] += 64;              // VU elsewhere
        if (fault == 2) vi_input.stVFrame.u32Stride[1] = 960;               // other VU pitch
        if (fault == 3) vi_input.stVFrame.enPixelFormat = PIXEL_FORMAT_UYVY; // not NV21
        int len = 0, w = 0, h = 0, fmt = 0;
        assert(_mmf_acquire_vi_frame(0, &len, &w, &h, &fmt, 0) == 0);
        assert(vi_releases == 1 && vpss_gets == 1 && !vi_frame_from_vi[0]);
        assert(!vi_direct[0] && vi_direct_venc == -1 && enables == 1 && vi_yuv420_refused);
        assert(priv.vi_frame[0].stVFrame.u64PhyAddr[0] == 0x9000000 && fmt == PIXEL_FORMAT_NV21);
        // The mode stays; direct does not come back until the next capture start.
        assert(mmf_vi_yuv420_active() && !mmf_vi_yuv420_pending());
        _mmf_vi_direct_for_venc(1, &h264); assert(!vi_direct[0]);
        start(); assert(!vi_yuv420_refused);
        _mmf_vi_direct_for_venc(1, &h264); assert(vi_direct[0]);
    }
    // A 1080 wide portrait at the 1088 pitch the VI reports is taken directly.
    reset(); priv.vi_size = {1080, 1920}; output.u32Width = 1080; output.u32Height = 1920;
    { mmf_venc_cfg_t portrait = video(2, 1080, 1920);
      _mmf_vi_yuv420_follow(&portrait); start(1080, 1920);
      _mmf_vi_direct_for_venc(1, &portrait); assert(vi_direct[0]);
      vi_input = nv21_frame(1080, 1920, 1088);
      int len = 0, w = 0, h = 0, fmt = 0;
      assert(_mmf_acquire_vi_frame(0, &len, &w, &h, &fmt, 0) == 0);
      assert(vi_frame_from_vi[0] && !vi_yuv420_refused && w == 1080 && h == 1920); }
    puts("stride guard ok");
}

// A JPEG or CPU reader yields the output to VPSS exactly as with H.265; the mode stays.
static void yield_unchanged() {
    reset(); mmf_venc_cfg_t h264 = video(2);
    _mmf_vi_yuv420_follow(&h264); start();
    _mmf_vi_direct_for_venc(1, &h264); assert(vi_direct[0]);
    assert(_mmf_vi_direct_yield(0) == 0 && !vi_direct[0] && vi_direct_venc == -1 && vi_shared_reader_us[0]);
    _mmf_vi_direct_for_venc(1, &h264); assert(!vi_direct[0]); // within 2 s of the reader
    assert(mmf_vi_yuv420_active() && !mmf_vi_yuv420_pending());
    vi_shared_reader_us[0] -= 2000001;
    _mmf_vi_direct_for_venc(1, &h264); assert(vi_direct[0]);
    puts("yield unchanged ok");
}

int main() {
    const int fd = mkstemp(param_path); assert(fd >= 0); close(fd);
    mode_rule();
    restart_and_codec_switch();
    h264_direct();
    hevc_unchanged();
    stride_guard();
    yield_unchanged();
    unlink(param_path);
    puts("PASS real 4:2:0 capture mode: rule, restart, codec switch, no relabel, stride guard, yield");
}
