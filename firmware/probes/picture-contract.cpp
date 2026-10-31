// The OCR picture read (mmf_read_bgr_planar) with the production code and
// fake VPSS/VB calls: a picture VPSS does not take back, and a busy pool.
#include "../../support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp"
// The IVE (the tiled MJPEG change map) is not exercised here; teardown frees it.
CVI_S32 CVI_SYS_FreeI(IVE_HANDLE, IVE_IMAGE_S *) { return 0; }
CVI_S32 CVI_IVE_DestroyHandle(IVE_HANDLE) { return 0; }
#include <cassert>
#include <string>
#include <vector>

static std::vector<std::string> calls;
static int release_failures, detach_failures, destroy_failures, frame_error;
// Pictures handed out and not taken back: VB refuses to destroy their pool.
static int outstanding;
static std::vector<uint8_t> memory;

CVI_S32 SAMPLE_COMM_VI_Bind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { calls.push_back("bind"); return 0; }
CVI_S32 SAMPLE_COMM_VI_UnBind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { calls.push_back("unbind"); return 0; }
CVI_S32 CVI_VPSS_SetChnAttr(VPSS_GRP, VPSS_CHN, const VPSS_CHN_ATTR_S *) { return 0; }
CVI_S32 CVI_VPSS_SetChnCrop(VPSS_GRP, VPSS_CHN, const VPSS_CROP_INFO_S *) { return 0; }
VB_POOL CVI_VB_CreatePool(VB_POOL_CONFIG_S *) { calls.push_back("create"); return 3; }
CVI_S32 CVI_VB_DestroyPool(VB_POOL)
{
    calls.push_back("destroy");
    if (outstanding > 0) return -1;
    return destroy_failures-- > 0 ? -1 : 0;
}
CVI_S32 CVI_VPSS_StopGrp(VPSS_GRP) { calls.push_back("stop-group"); return 0; }
// Destroying the group does not take back a picture the reader holds.
CVI_S32 CVI_VPSS_DestroyGrp(VPSS_GRP) { calls.push_back("destroy-group"); return 0; }
CVI_S32 CVI_VI_ReleaseChnFrame(VI_PIPE, VI_CHN, const VIDEO_FRAME_INFO_S *) { return 0; }
CVI_S32 CVI_VPSS_AttachVbPool(VPSS_GRP, VPSS_CHN, VB_POOL) { calls.push_back("attach"); return 0; }
CVI_S32 CVI_VPSS_DetachVbPool(VPSS_GRP, VPSS_CHN)
{
    calls.push_back("detach");
    return detach_failures-- > 0 ? -1 : 0;
}
CVI_S32 CVI_VPSS_EnableChn(VPSS_GRP, VPSS_CHN) { calls.push_back("enable"); return 0; }
CVI_S32 CVI_VPSS_DisableChn(VPSS_GRP, VPSS_CHN) { calls.push_back("disable"); return 0; }
// A 16x16 picture in contiguous planes with a stride of 32.
CVI_S32 CVI_VPSS_GetChnFrame(VPSS_GRP, VPSS_CHN, VIDEO_FRAME_INFO_S *frame, CVI_S32)
{
    calls.push_back("get");
    if (frame_error) return frame_error;
    ++outstanding;
    *frame = {};
    VIDEO_FRAME_S &v = frame->stVFrame;
    v.u32Width = 16; v.u32Height = 16;
    for (int c = 0; c < 3; ++c) {
        v.u32Stride[c] = 32;
        v.u32Length[c] = 32 * 16;
        v.u64PhyAddr[c] = 0x1000 + c * 512;
    }
    return 0;
}
CVI_S32 CVI_VPSS_ReleaseChnFrame(VPSS_GRP, VPSS_CHN, const VIDEO_FRAME_INFO_S *frame)
{
    calls.push_back("release");
    assert(frame->stVFrame.u64PhyAddr[0] == 0x1000);
    if (release_failures-- > 0) return -1;
    --outstanding;
    return 0;
}
void *CVI_SYS_MmapCache(CVI_U64 addr, CVI_U32 size)
{
    assert(addr == 0x1000 && size == 3 * 512);
    memory.assign(size, 0);
    for (size_t i = 0; i < memory.size(); ++i) memory[i] = uint8_t(i * 7 + i / 256);
    return memory.data();
}
CVI_S32 CVI_SYS_Munmap(void *, CVI_U32) { return 0; }
// The rest of the public teardown (mmf_try_deinit): no encoder or VI
// channel is open in these cases, so only the SYS exit is recorded.
CVI_S32 CVI_VB_ReleaseBlock(VB_BLK) { return 0; }
CVI_S32 CVI_VENC_DestroyChn(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_GetStream(VENC_CHN, VENC_STREAM_S *, CVI_S32) { return -1; }
CVI_S32 CVI_VENC_QueryStatus(VENC_CHN, VENC_CHN_STATUS_S *status) { *status = {}; return 0; }
CVI_S32 CVI_VENC_ReleaseStream(VENC_CHN, VENC_STREAM_S *) { return 0; }
CVI_S32 CVI_VENC_ResetChn(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_StopRecvFrame(VENC_CHN) { return 0; }
CVI_S32 CVI_VI_GetChnAttr(VI_PIPE, VI_CHN, VI_CHN_ATTR_S *attr) { *attr = {}; return 0; }
CVI_S32 CVI_VPSS_GetChnAttr(VPSS_GRP, VPSS_CHN, VPSS_CHN_ATTR_S *attr) { *attr = {}; return 0; }
CVI_S32 SAMPLE_COMM_VI_DestroyIsp(SAMPLE_VI_CONFIG_S *) { return 0; }
CVI_S32 SAMPLE_COMM_VI_DestroyVi(SAMPLE_VI_CONFIG_S *) { return 0; }
CVI_VOID SAMPLE_COMM_SYS_Exit(void) { calls.push_back("sys-exit"); }

static void reset()
{
    priv = {};
    priv.mmf_used_cnt = 1;
    priv.vi_is_inited = true;
    priv.vi_source_bound = true;
    calls.clear();
    release_failures = detach_failures = destroy_failures = frame_error = outstanding = 0;
    picture_pool = -1;
    picture_pool_attached = picture_frame_held = false;
}

static void expect(std::initializer_list<const char *> wanted)
{
    std::vector<std::string> values(wanted.begin(), wanted.end());
    if (calls != values) {
        for (auto &call : calls) fprintf(stderr, "%s ", call.c_str());
        fprintf(stderr, "\n");
    }
    assert(calls == values);
    calls.clear();
}

static int read_picture()
{
    uint8_t out[16 * 16 * 3];
    const int result = mmf_read_bgr_planar(16, 16, out, sizeof(out), 500);
    if (result == 0) {
        // Rows at the stride, planes one after the other.
        for (int c = 0; c < 3; ++c)
            for (int y = 0; y < 16; ++y)
                for (int x = 0; x < 16; ++x)
                    assert(out[(c * 16 + y) * 16 + x] == memory[c * 512 + y * 32 + x]);
    }
    return result;
}

int main()
{
    reset();
    assert(read_picture() == 0);
    expect({"create", "attach", "enable", "get", "disable", "release", "detach", "destroy"});

    // VPSS does not take the picture back once: the read keeps it and
    // returns it in its cleanup, before it detaches and destroys the pool.
    reset();
    release_failures = 1;
    assert(read_picture() == 0);
    expect({"create", "attach", "enable", "get", "disable", "release", "release", "detach", "destroy"});
    assert(!picture_frame_held && picture_pool < 0);

    // Not in the cleanup either: the frame and the pool are kept, and the
    // next read returns the frame first.
    reset();
    release_failures = 2;
    assert(read_picture() == 0);
    expect({"create", "attach", "enable", "get", "disable", "release", "release"});
    assert(picture_frame_held && picture_pool == 3 && picture_pool_attached);
    assert(read_picture() == 0);
    expect({"release", "detach", "destroy", "create", "attach", "enable", "get", "disable", "release", "detach",
            "destroy"});
    assert(!picture_frame_held && picture_pool < 0);

    // Still not taken back: the next read waits as for a picture not there
    // yet (-3, tried again by the server), and leaves the pool alone.
    reset();
    release_failures = 3;
    assert(read_picture() == 0);
    calls.clear();
    assert(read_picture() == -3);
    expect({"release"});
    assert(read_picture() == 0);
    expect({"release", "detach", "destroy", "create", "attach", "enable", "get", "disable", "release", "detach",
            "destroy"});

    // A pool VPSS still uses after the picture: kept, destroyed next time.
    reset();
    destroy_failures = 1;
    assert(read_picture() == 0);
    expect({"create", "attach", "enable", "get", "disable", "release", "detach", "destroy"});
    assert(picture_pool == 3 && !picture_pool_attached);
    assert(read_picture() == 0);
    expect({"destroy", "create", "attach", "enable", "get", "disable", "release", "detach", "destroy"});

    // No picture in time: the pool goes, nothing was taken.
    reset();
    frame_error = -1;
    assert(read_picture() == -3);
    expect({"create", "attach", "enable", "get", "disable", "detach", "destroy"});

    // The capture restarts (mmf_vi_deinit) while a picture is still held:
    // it is returned before the VPSS group goes, then the pool is destroyed.
    reset();
    release_failures = 2;
    assert(read_picture() == 0);
    calls.clear();
    assert(picture_frame_held && outstanding == 1);
    assert(mmf_vi_deinit() == 0);
    expect({"release", "stop-group", "destroy-group", "destroy"});
    assert(!picture_frame_held && picture_pool < 0 && outstanding == 0 && !priv.vi_is_inited);

    // VPSS still refuses it: the restart waits (as for a VI frame it cannot
    // return), keeping the group, the picture and the pool for the next try.
    reset();
    release_failures = 3;
    assert(read_picture() == 0);
    calls.clear();
    assert(mmf_vi_deinit() != 0);
    expect({"release"});
    assert(picture_frame_held && picture_pool == 3 && priv.vi_is_inited);
    assert(mmf_vi_deinit() == 0);
    expect({"release", "stop-group", "destroy-group", "destroy"});
    assert(!picture_frame_held && picture_pool < 0 && outstanding == 0);

    // The same through the public teardown (Capture::shutdown/restart): the
    // media system stays, with its reference, until the picture is back.
    reset();
    release_failures = 3;
    assert(read_picture() == 0);
    calls.clear();
    assert(mmf_try_deinit(true) != 0);
    expect({"release"});
    assert(priv.mmf_used_cnt == 1 && priv.vi_is_inited && picture_frame_held && outstanding == 1);
    assert(mmf_try_deinit(true) == 0);
    expect({"release", "stop-group", "destroy-group", "destroy", "sys-exit"});
    assert(priv.mmf_used_cnt == 0 && !priv.vi_is_inited && !picture_frame_held && outstanding == 0);

    puts("picture contract ok");
    return 0;
}
