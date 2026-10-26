// Exercise the production shared VPSS input through direct, parked and deleted outputs.
#include "../../support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp"
#include <cassert>
#include <vector>
#include <string>
static std::vector<std::string> calls;
static int bind_error, unbind_error, enable_error, disable_error, destroy_error;
static VPSS_CHN_ATTR_S output;
static int vi_releases, vpss_releases, attach_error;
CVI_S32 SAMPLE_COMM_VI_Bind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { calls.push_back("bind"); return bind_error; }
CVI_S32 SAMPLE_COMM_VI_UnBind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { calls.push_back("unbind"); return unbind_error; }
CVI_S32 CVI_VPSS_EnableChn(VPSS_GRP, VPSS_CHN ch) { calls.push_back("enable" + std::to_string(ch)); return enable_error; }
CVI_S32 CVI_VPSS_DisableChn(VPSS_GRP, VPSS_CHN ch) { calls.push_back("disable" + std::to_string(ch)); return disable_error; }
CVI_S32 CVI_VI_GetChnAttr(VI_PIPE, VI_CHN, VI_CHN_ATTR_S *a) { *a = {}; a->stSize = {2560,1440}; return 0; }
CVI_S32 CVI_VPSS_GetChnAttr(VPSS_GRP, VPSS_CHN, VPSS_CHN_ATTR_S *a) { *a = output; return 0; }
CVI_S32 CVI_SYS_Munmap(void *, CVI_U32) { abort(); }
CVI_S32 CVI_VI_ReleaseChnFrame(VI_PIPE, VI_CHN, const VIDEO_FRAME_INFO_S *) { ++vi_releases; return 0; }
CVI_S32 CVI_VPSS_ReleaseChnFrame(VPSS_GRP, VPSS_CHN, const VIDEO_FRAME_INFO_S *) { ++vpss_releases; return 0; }
CVI_S32 CVI_VB_DestroyPool(VB_POOL) { calls.push_back("destroy"); return destroy_error; }
CVI_S32 CVI_VPSS_DetachVbPool(VPSS_GRP, VPSS_CHN) { return 0; }
CVI_S32 CVI_VPSS_CreateGrp(VPSS_GRP, const VPSS_GRP_ATTR_S *) { return 0; }
CVI_S32 CVI_VPSS_DestroyGrp(VPSS_GRP) { return 0; }
CVI_S32 CVI_VPSS_ResetGrp(VPSS_GRP) { return 0; }
CVI_S32 CVI_VPSS_StartGrp(VPSS_GRP) { return 0; }
CVI_S32 CVI_VPSS_GetChnFd(VPSS_GRP, VPSS_CHN) { return 7; }
extern "C" CVI_S32 vpss_set_grp_csc(CVI_S32, const vpss_grp_csc_cfg *) { return 0; }
CVI_S32 CVI_VPSS_GetGrpAttr(VPSS_GRP, VPSS_GRP_ATTR_S *a) {
    *a = {}; a->u32MaxW = 2560; a->u32MaxH = 1440; return 0;
}
CVI_S32 CVI_VPSS_SetChnCrop(VPSS_GRP, VPSS_CHN, const VPSS_CROP_INFO_S *) { return 0; }
CVI_S32 CVI_VPSS_SetChnAttr(VPSS_GRP, VPSS_CHN, const VPSS_CHN_ATTR_S *) { return 0; }
VB_POOL CVI_VB_CreatePool(VB_POOL_CONFIG_S *) { return 2; }
CVI_S32 CVI_VPSS_AttachVbPool(VPSS_GRP, VPSS_CHN, VB_POOL) { return attach_error; }
static void expect(std::initializer_list<const char *> wanted) {
    std::vector<std::string> values;
    for (auto value : wanted) values.emplace_back(value);
    assert(calls == values); calls.clear();
}
static void reset(bool second = false) {
    priv = {}; calls.clear(); vi_direct_venc = -1;
    for (int i = 0; i < MMF_VI_MAX_CHN; ++i) {
        vi_direct[i] = vi_frame_from_vi[i] = false;
        priv.vi_chn_pool_id[i] = VB_INVALID_POOLID;
    }
    priv.vi_chn_is_inited[0] = priv.vpss_chn_enabled[0] = true;
    priv.vi_chn_is_inited[1] = priv.vpss_chn_enabled[1] = second;
    priv.vi_source_bound = true;
    output = {}; output.enPixelFormat = PIXEL_FORMAT_NV21;
    output.u32Width = 2560; output.u32Height = 1440;
    bind_error = unbind_error = enable_error = disable_error = destroy_error = 0;
    vi_releases = vpss_releases = attach_error = 0;
    priv.mmf_used_cnt = 1; priv.vi_is_inited = true;
}
static void direct_and_readers() {
    for (bool nv21 : {false, true}) {
        reset(); vi_yuv420_running = nv21;
        assert(_mmf_vi_direct_set(0, true) == 0);
        expect({"unbind", "disable0"});
        assert(vi_direct[0] && !priv.vi_source_bound && !priv.vpss_chn_enabled[0]);
        assert(mmf_vi_chn_set_enabled(0, 1) == 0); expect({});
        vi_direct_venc = 1;
        assert(_mmf_vi_direct_yield(0) == 0);
        expect({"bind", "enable0"});
        assert(!vi_direct[0] && vi_direct_venc == -1 && priv.vi_source_bound);
        assert(_mmf_vi_direct_set(0, true) == 0); calls.clear();
        // A lease acquired before a path switch retains its release owner.
        vi_frame_from_vi[0] = priv.vi_frame_valid[0] = true;
        assert(_mmf_vi_direct_set(0, false) == 0);
        assert(_mmf_release_vi_frame(0) == 0 && vi_releases == 1 && vpss_releases == 0);
    }
}
static void multiple_and_idle() {
    reset(true);
    assert(_mmf_vi_direct_set(0, true) == 0); expect({"disable0"});
    assert(priv.vi_source_bound); // MJPEG/scaling/CPU output remains active
    assert(mmf_vi_chn_set_enabled(1, 0) == 0); expect({"unbind", "disable1"});
    assert(!priv.vi_source_bound);
    assert(mmf_vi_chn_set_enabled(1, 0) == 0); expect({});
    assert(mmf_vi_chn_set_enabled(1, 1) == 0); expect({"bind", "enable1"});
    assert(mmf_del_vi_channel(1) == 0); expect({"unbind", "disable1"});
    assert(vi_direct[0] && priv.vi_chn_is_inited[0] && !priv.vi_source_bound);
    assert(mmf_del_vi_channel(0) == 0); expect({});
    assert(!vi_direct[0] && !priv.vi_chn_is_inited[0]);
    reset();
    assert(mmf_vi_chn_set_enabled(0, 0) == 0); expect({"unbind", "disable0"});
    assert(mmf_vi_chn_set_enabled(0, 1) == 0); expect({"bind", "enable0"});
}
static void new_consumer() {
    reset();
    assert(_mmf_vi_direct_set(0, true) == 0); calls.clear();
    // The source already exists but is unbound: adding MJPEG must bind again.
    assert(mmf_add_vi_channel_configured(1, 1280, 720, PIXEL_FORMAT_NV16, 2, 1) == 0);
    expect({"disable1", "enable1", "bind"});
    assert(vi_direct[0] && priv.vi_source_bound && priv.vpss_chn_enabled[1]);
    assert(mmf_del_vi_channel(1) == 0); expect({"unbind", "disable1", "destroy"});
    assert(!priv.vi_source_bound && vi_direct[0]);
    bind_error = -23;
    assert(mmf_add_vi_channel_configured(1, 1280, 720, PIXEL_FORMAT_NV16, 2, 1) != 0);
    expect({"disable1", "enable1", "bind", "disable1"});
    assert(!priv.vi_chn_is_inited[1] && !priv.vpss_chn_enabled[1] && !priv.vi_source_bound);
    bind_error = 0; attach_error = -25;
    assert(mmf_add_vi_channel_configured(1, 1280, 720, PIXEL_FORMAT_NV16, 2, 1) != 0);
    expect({"disable1", "enable1", "bind", "unbind", "disable1", "destroy"});
    assert(!priv.vi_chn_is_inited[1] && !priv.vpss_chn_enabled[1] && !priv.vi_source_bound);
    attach_error = 0;
    assert(mmf_add_vi_channel_configured(1, 1280, 720, PIXEL_FORMAT_NV16, 2, 1) == 0);
    assert(priv.vi_source_bound && vi_direct[0]);
}
static void faults() {
    reset(); unbind_error = -21;
    assert(_mmf_vi_direct_set(0, true) == -21); expect({"unbind"});
    assert(!vi_direct[0] && priv.vi_source_bound && priv.vpss_chn_enabled[0]);
    unbind_error = 0; disable_error = -22;
    assert(_mmf_vi_direct_set(0, true) == -22); expect({"unbind", "disable0", "bind"});
    assert(!vi_direct[0] && priv.vi_source_bound && priv.vpss_chn_enabled[0]);
    disable_error = 0;
    assert(_mmf_vi_direct_set(0, true) == 0); calls.clear();
    bind_error = -23;
    assert(_mmf_vi_direct_set(0, false) == -23); expect({"bind"});
    assert(vi_direct[0] && !priv.vi_source_bound && !priv.vpss_chn_enabled[0]);
    bind_error = 0; enable_error = -24;
    assert(_mmf_vi_direct_set(0, false) == -24); expect({"bind", "enable0", "unbind"});
    assert(vi_direct[0] && !priv.vi_source_bound && !priv.vpss_chn_enabled[0]);
    enable_error = 0;
    assert(_mmf_vi_direct_set(0, false) == 0); expect({"bind", "enable0"});
    // Even a failed rollback records actual state, so a retry repairs it.
    disable_error = -22; bind_error = -23;
    assert(mmf_vi_chn_set_enabled(0, 0) == -22); expect({"unbind", "disable0", "bind"});
    assert(!priv.vi_source_bound && priv.vpss_chn_enabled[0]);
    bind_error = disable_error = 0;
    assert(mmf_vi_chn_set_enabled(0, 1) == 0); expect({"bind"});
    unbind_error = -21;
    assert(mmf_del_vi_channel(0) == -21); expect({"unbind"});
    assert(priv.vi_chn_stopping[0] && priv.vi_chn_is_inited[0] && priv.vi_source_bound);
    unbind_error = 0;
    assert(mmf_del_vi_channel(0) == 0); expect({"unbind", "disable0"});
    assert(!priv.vi_chn_is_inited[0] && !priv.vi_source_bound);
}
int main() {
    direct_and_readers(); multiple_and_idle(); new_consumer(); faults();
    puts("PASS shared VPSS demand: H.265/H.264 direct, readers, idle, delete, lease and fault retries");
}
