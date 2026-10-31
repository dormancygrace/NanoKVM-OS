// Exercise the production shared VPSS input through direct, parked and deleted outputs.
#include "../../support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp"
// The IVE (the tiled MJPEG change map) is not exercised here; teardown frees it.
CVI_S32 CVI_SYS_FreeI(IVE_HANDLE, IVE_IMAGE_S *) { return 0; }
CVI_S32 CVI_IVE_DestroyHandle(IVE_HANDLE) { return 0; }
// Nor are the region JPEG channels; releasing a frame checks them.
CVI_S32 CVI_VENC_StopRecvFrame(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_ResetChn(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_DestroyChn(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_ReleaseStream(VENC_CHN, VENC_STREAM_S *) { return 0; }
#include <cassert>
#include <vector>
#include <string>
static std::vector<std::string> calls;
static bool shared_vi_heap = true;
static uint32_t vi_heap_mib = 144;
static int vi_heap_size_bytes = 4;
static int vi_heap_reads;
extern "C" FILE *__real_fopen(const char *, const char *);
extern "C" FILE *__wrap_fopen(const char *path, const char *mode) {
    if (strcmp(path,"/proc/device-tree/reserved-memory/ion/size")) return __real_fopen(path,mode);
    ++vi_heap_reads;
    if (vi_heap_size_bytes < 0) return nullptr;
    FILE *file=tmpfile(); assert(file);
    const uint32_t bytes=vi_heap_mib*1024*1024;
    const uint8_t value[]={uint8_t(bytes>>24),uint8_t(bytes>>16),uint8_t(bytes>>8),uint8_t(bytes),0,0,0,0};
    assert(fwrite(value,1,vi_heap_size_bytes,file)==size_t(vi_heap_size_bytes));
    rewind(file);return file;
}
extern "C" int __real_access(const char *, int);
extern "C" int __wrap_access(const char *path, int mode) {
    if (!strcmp(path,"/proc/device-tree/reserved-memory/ion/reusable")) return shared_vi_heap ? 0 : -1;
    return __real_access(path,mode);
}
static int bind_error, unbind_error, enable_error, disable_error, destroy_error;
static unsigned bind_successes, unbind_successes;
static VPSS_CHN_ATTR_S output;
static int vi_releases, vpss_releases, attach_error, detach_error, frame_release_error;
static bool pool_trace, create_error;
static std::vector<unsigned> pool_requests;
CVI_S32 SAMPLE_COMM_VI_Bind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { calls.push_back("bind"); if (!bind_error) ++bind_successes; return bind_error; }
CVI_S32 SAMPLE_COMM_VI_UnBind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { calls.push_back("unbind"); if (!unbind_error) ++unbind_successes; return unbind_error; }
CVI_S32 CVI_VPSS_EnableChn(VPSS_GRP, VPSS_CHN ch) { calls.push_back("enable" + std::to_string(ch)); return enable_error; }
CVI_S32 CVI_VPSS_DisableChn(VPSS_GRP, VPSS_CHN ch) { calls.push_back("disable" + std::to_string(ch)); return disable_error; }
CVI_S32 CVI_VI_GetChnAttr(VI_PIPE, VI_CHN, VI_CHN_ATTR_S *a) { *a = {}; a->stSize = {2560,1440}; return 0; }
CVI_S32 CVI_VPSS_GetChnAttr(VPSS_GRP, VPSS_CHN, VPSS_CHN_ATTR_S *a) { *a = output; return 0; }
CVI_S32 CVI_SYS_Munmap(void *, CVI_U32) { abort(); }
CVI_S32 CVI_VI_ReleaseChnFrame(VI_PIPE, VI_CHN, const VIDEO_FRAME_INFO_S *) { ++vi_releases; return 0; }
CVI_S32 CVI_VPSS_ReleaseChnFrame(VPSS_GRP, VPSS_CHN, const VIDEO_FRAME_INFO_S *) { ++vpss_releases; return frame_release_error; }
CVI_S32 CVI_VB_DestroyPool(VB_POOL) { calls.push_back("destroy"); return destroy_error; }
CVI_S32 CVI_VPSS_DetachVbPool(VPSS_GRP, VPSS_CHN ch) { if(pool_trace) calls.push_back("detach" + std::to_string(ch)); return detach_error; }
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
VB_POOL CVI_VB_CreatePool(VB_POOL_CONFIG_S *cfg) {
    if(pool_trace) { calls.push_back("create"); pool_requests.push_back(cfg->u32BlkCnt); assert(cfg->u32BlkSize == 5529600); }
    return create_error ? VB_INVALID_POOLID : 2;
}
CVI_S32 CVI_VPSS_AttachVbPool(VPSS_GRP, VPSS_CHN ch, VB_POOL pool) {
    if(pool_trace) { calls.push_back("attach" + std::to_string(ch)); assert(pool==2 && !priv.vpss_chn_enabled[ch]); }
    return attach_error;
}
static void expect(std::initializer_list<const char *> wanted) {
    std::vector<std::string> values;
    for (auto value : wanted) values.emplace_back(value);
    assert(calls == values); calls.clear();
}
static void reset(bool second = false) {
    priv = {}; calls.clear(); vi_direct_venc = -1;
    pool_trace = create_error = false; detach_error = frame_release_error = 0; pool_requests.clear();
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
static void source_buffer_policy() {
    shared_vi_heap=true;
    struct Case { uint32_t w, h, depth, buffers; };
    for (const auto &c : {Case{1920,1080,1,4}, Case{2560,1440,1,4},
             Case{1440,2560,1,4}, Case{2560,1441,0,2}, Case{3840,2160,0,2},
             Case{0,1440,0,2}, Case{1920,0,0,2}, Case{UINT32_MAX,UINT32_MAX,0,2}}) {
        const auto cfg=nanokvm_vi_configure_buffering(c.w,c.h);
        assert(cfg.ready_depth==c.depth && cfg.source_buffers==c.buffers);
        assert(cfg.cma_bytes==144U*1024*1024 && cfg.cma_read==NANOKVM_CMA_READ_OK);
    }
    shared_vi_heap=false;
    for (const auto &c : {Case{1920,1080,0,3}, Case{2560,1440,0,3}, Case{3840,2160,0,2}}) {
        const auto cfg=nanokvm_vi_configure_buffering(c.w,c.h);
        assert(!cfg.reusable && cfg.ready_depth==c.depth && cfg.source_buffers==c.buffers);
    }
    shared_vi_heap=true;
    for (uint32_t mib : {40U,56U,104U,128U,143U}) {
        vi_heap_mib=mib;
        const auto cfg=nanokvm_vi_configure_buffering(2560,1440);
        assert(cfg.ready_depth==0 && cfg.source_buffers==3 && cfg.cma_bytes==mib*1024*1024);
    }
    vi_heap_mib=144;
    for (int count : {-1,0,3,5,8}) {
        vi_heap_size_bytes=count;
        const auto cfg=nanokvm_vi_configure_buffering(2560,1440);
        assert(cfg.ready_depth==0 && cfg.source_buffers==3 && cfg.cma_bytes==0);
        assert(cfg.cma_read==(count<0 ? NANOKVM_CMA_READ_FAILED : NANOKVM_CMA_READ_INVALID));
    }
    vi_heap_size_bytes=4;
    nanokvm_vi_configure_buffering(2560,1440);
    const int reads=vi_heap_reads;
    shared_vi_heap=false; vi_heap_mib=40;
    uint32_t depth=42;
    assert(nanokvm_vi_startup_depth(2560,1440,&depth)==0 && depth==1);
    assert(vi_heap_reads==reads); // no second DT lookup during channel startup
    for (const auto &c : {Case{3840,2160,0,0}, Case{1440,2560,0,0}, Case{0,0,0,0}}) {
        depth=42;
        assert(nanokvm_vi_startup_depth(c.w,c.h,&depth)!=0 && depth==42);
    }
    assert(nanokvm_vi_startup_depth(2560,1440,nullptr)!=0);
    nanokvm_vi_reset_buffering();
    assert(nanokvm_vi_startup_depth(2560,1440,&depth)!=0);
    shared_vi_heap=true;vi_heap_mib=144;
    const auto uhd=nanokvm_vi_configure_buffering(3840,2160);
    assert(uhd.source_buffers==2);
    assert(nanokvm_vi_startup_depth(3840,2160,&depth)==0 && depth==0);
}
static void owned_pool() {
    reset(); pool_trace=true;
    priv.vi_chn_pool_id[0]=2; priv.vb_pool_used[2]=true; priv.vi_buffers[0]=3;
}
static void reclaim_and_restore() {
    owned_pool();
    assert(mmf_vi_chn_set_enabled(0,0)==0);
    expect({"unbind","disable0","detach0","destroy"});
    assert(priv.vi_pool_reclaimed[0] && !vi_direct[0]);
    assert(mmf_vi_chn_set_enabled(0,1)==0);
    expect({"create","attach0","bind","enable0"});
    owned_pool();
    assert(_mmf_vi_direct_set(0,true)==0);
    expect({"unbind","disable0","detach0","destroy"});
    assert(priv.vi_pool_reclaimed[0] && priv.vi_chn_pool_id[0]==(int)VB_INVALID_POOLID && !priv.vb_pool_used[2]);
    assert(_mmf_vi_direct_set(0,false)==0);
    expect({"create","attach0","bind","enable0"});
    assert(!priv.vi_pool_reclaimed[0] && !priv.vi_pool_detached[0] && priv.vb_pool_used[2]);
    assert(pool_requests==std::vector<unsigned>{3});
    // The encoder can still own the old VPSS output across the switch.
    owned_pool(); priv.vi_frame_valid[0]=true; priv.vi_frame[0].stVFrame.enPixelFormat=PIXEL_FORMAT_NV21;
    priv.venc[1].is_running=true; priv.venc_input_vi_ch[1]=0;
    assert(_mmf_vi_direct_set(0,true)==0); expect({"unbind","disable0"});
    assert(_mmf_release_vi_frame(0)==CVI_ERR_VENC_BUSY); expect({});
    assert(priv.vi_chn_pool_id[0]==2 && priv.vb_pool_used[2]);
    priv.venc[1].is_running=false; frame_release_error=-34;
    assert(_mmf_release_vi_frame(0)==-34);expect({});
    assert(priv.vi_frame_valid[0] && priv.vb_pool_used[2]);
    frame_release_error=0;
    assert(_mmf_release_vi_frame(0)==0 && vpss_releases==2);
    expect({"detach0","destroy"}); assert(priv.vi_pool_reclaimed[0]);
    // A VI lease uses the common source pool, so it does not hold the VPSS pool.
    owned_pool(); priv.vi_frame_valid[0]=vi_frame_from_vi[0]=true;
    assert(_mmf_vi_direct_set(0,true)==0);expect({"unbind","disable0","detach0","destroy"});
    assert(priv.vi_frame_valid[0]);
    // Channel1 is direct while channel0 still needs its own VPSS pool.
    reset(true);pool_trace=true;priv.vi_chn_pool_id[0]=3;priv.vb_pool_used[3]=true;
    priv.vi_chn_pool_id[1]=2;priv.vb_pool_used[2]=true;priv.vi_buffers[1]=3;
    assert(_mmf_vi_direct_set(1,true)==0);expect({"disable1","detach1","destroy"});
    assert(priv.vi_source_bound && priv.vpss_chn_enabled[0] && priv.vb_pool_used[3]);
    assert(priv.vi_chn_pool_id[0]==3 && priv.vi_pool_reclaimed[1]);
    assert(_mmf_vi_direct_set(1,false)==0);expect({"create","attach1","enable1"});
    // An off-path switch while parked must restore the pool before resuming VPSS.
    owned_pool(); assert(_mmf_vi_direct_set(0,true)==0);calls.clear();
    priv.vi_chn_disabled[0]=true;
    assert(_mmf_vi_direct_set(0,false)==0);expect({});
    priv.vi_chn_disabled[0]=false;
    assert(mmf_vi_chn_set_enabled(0,1)==0);expect({"create","attach0","bind","enable0"});
}
static void pool_faults() {
    owned_pool(); detach_error=-31;
    assert(_mmf_vi_direct_set(0,true)==0);expect({"unbind","disable0","detach0"});
    assert(priv.vb_pool_used[2] && !priv.vi_pool_detached[0]);
    detach_error=0;destroy_error=-32;
    assert(_mmf_reclaim_disabled_pool(0)!=0);expect({"detach0","destroy"});
    assert(priv.vb_pool_used[2] && priv.vi_pool_detached[0] && priv.vi_chn_pool_id[0]==2);
    // Resume can reattach a retained pool without allocating a replacement.
    assert(_mmf_vi_direct_set(0,false)==0);expect({"attach0","bind","enable0"});
    assert(pool_requests.empty());
    owned_pool();assert(_mmf_vi_direct_set(0,true)==0);calls.clear();create_error=true;
    assert(_mmf_vi_direct_set(0,false)!=0);expect({"create","create"});
    assert(pool_requests==std::vector<unsigned>({3,2}));
    assert(vi_direct[0] && !priv.vpss_chn_enabled[0] && priv.vi_pool_reclaimed[0]);
    create_error=false;attach_error=-33;
    assert(_mmf_vi_direct_set(0,false)==-33);expect({"create","attach0"});
    assert(vi_direct[0] && priv.vb_pool_used[2] && priv.vi_pool_detached[0]);
    attach_error=0;
    assert(_mmf_vi_direct_set(0,false)==0);expect({"attach0","bind","enable0"});
    assert(!vi_direct[0] && priv.vpss_chn_enabled[0]);
    // Cleanup after an attach failure retains the pool until destroy succeeds.
    owned_pool();assert(_mmf_vi_direct_set(0,true)==0);calls.clear();attach_error=-33;
    assert(_mmf_vi_direct_set(0,false)==-33);calls.clear();destroy_error=-32;
    assert(mmf_del_vi_channel(0)!=0);expect({"destroy"});
    assert(priv.vi_chn_stopping[0] && priv.vb_pool_used[2]);
    destroy_error=0;
    assert(mmf_del_vi_channel(0)==0);expect({"destroy"});
    assert(!priv.vi_chn_is_inited[0] && !priv.vb_pool_used[2]);
}
static void delayed_pool_reclaim() {
    // No consumer/path change: the existing capture tick must reclaim a pool
    // whose last VPSS job finished after DisableChn returned.
    owned_pool(); destroy_error=-32;
    assert(!mmf_vi_has_pending_pool());
    assert(_mmf_vi_direct_set(0,true)==0);
    expect({"unbind","disable0","detach0","destroy"});
    assert(priv.vb_pool_used[2] && priv.vi_pool_detached[0]);
    assert(mmf_vi_has_pending_pool());
    destroy_error=0;
    _mmf_vi_direct_recheck(venc_now_us() + 1000000);
    expect({"destroy"});
    assert(vi_direct[0] && priv.vi_pool_reclaimed[0] && !priv.vb_pool_used[2]);
    assert(!mmf_vi_has_pending_pool());
    // With no readers the timer repeats parking instead of running capture.
    owned_pool(); destroy_error=-32;
    assert(mmf_vi_chn_set_enabled(0,0)==0);
    expect({"unbind","disable0","detach0","destroy"});
    destroy_error=0;
    assert(mmf_vi_chn_set_enabled(0,0)==0);expect({"destroy"});
    assert(!priv.vpss_chn_enabled[0] && !priv.vi_source_bound && priv.vi_pool_reclaimed[0] && !priv.vb_pool_used[2]);
}
// Count real production helper calls while old VPSS leases straddle a switch.
// This validates userspace ownership/idempotency, not device scheduling.
static void repeated_shared_reader_transitions() {
    unsigned total = 0;
    for (bool nv21 : {false, true}) {
        owned_pool(); vi_yuv420_running = nv21; vi_yuv420_refused = false;
        bind_successes = unbind_successes = 0;
        for (unsigned round = 0; round < 500; ++round) {
            priv.vi_frame_valid[0] = true;
            priv.vi_frame[0].stVFrame.enPixelFormat = PIXEL_FORMAT_NV21;
            vi_frame_from_vi[0] = false;
            assert(_mmf_vi_direct_set(0, true) == 0);
            assert(!priv.vi_source_bound && !priv.vpss_chn_enabled[0]);
            assert(!priv.vi_pool_reclaimed[0]); // old VPSS lease owns the pool
            assert(mmf_vi_has_pending_pool());
            assert(mmf_vi_chn_set_enabled(0, 1) == 0); // direct demand is idempotent
            assert(_mmf_release_vi_frame(0) == 0);
            assert(priv.vi_pool_reclaimed[0]);
            assert(!mmf_vi_has_pending_pool());
            // NV21 JPEG keeps its VI lease and must not yield; CPU/packed
            // readers yield, then a later video recheck can restore direct.
            assert(_mmf_jpeg_reader_yields() == !nv21);
            assert(_mmf_vi_direct_yield(0) == 0);
            assert(priv.vi_source_bound && priv.vpss_chn_enabled[0]);
            assert(!priv.vi_pool_reclaimed[0] && !priv.vi_pool_detached[0]);
            assert(mmf_vi_chn_set_enabled(0, 1) == 0);
            assert(bind_successes == round + 1 && unbind_successes == round + 1);
            calls.clear();
        }
        total += bind_successes + unbind_successes;
    }
    printf("PASS %u counted shared-reader bind/unbind transitions\n", total);
}
int main() {
    direct_and_readers(); multiple_and_idle(); new_consumer(); faults();
    source_buffer_policy(); reclaim_and_restore(); pool_faults(); delayed_pool_reclaim(); repeated_shared_reader_transitions();
    puts("PASS source buffering, pool reclaim/restore/lease/fault ownership, shared VPSS demand: H.265/H.264 direct, readers, idle, delete, lease and fault retries");
}
