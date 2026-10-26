// Host fault injection of the real VENC teardown (mmf_del_venc_channel) of a
// wedged channel; no hardware is opened. The encoder keeps a submitted frame
// that never completes; teardown must keep the input lease and its bookkeeping
// until the channel is destroyed.
#include "../../support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp"
#include <cassert>
static int stops, resets, destroys, releases, vendor_error;
CVI_S32 CVI_VENC_QueryStatus(VENC_CHN, VENC_CHN_STATUS_S *status) {
    memset(status, 0, sizeof(*status));
    status->u32CurPacks = 1;
    return CVI_SUCCESS;
}
CVI_S32 CVI_VENC_GetStream(VENC_CHN, VENC_STREAM_S *, CVI_S32) { return CVI_ERR_VENC_BUSY; }
CVI_S32 CVI_VENC_StopRecvFrame(VENC_CHN) { ++stops; return vendor_error; }
CVI_S32 CVI_VENC_ResetChn(VENC_CHN) { ++resets; return vendor_error; }
CVI_S32 CVI_VENC_DestroyChn(VENC_CHN) { ++destroys; return vendor_error; }
CVI_S32 CVI_VPSS_ReleaseChnFrame(VPSS_GRP, VPSS_CHN, const VIDEO_FRAME_INFO_S *) {
    ++releases;
    return CVI_SUCCESS;
}
// Linked but not reached: no stream is held, nothing is mapped, no copy buffer.
CVI_S32 CVI_VENC_ReleaseStream(VENC_CHN, VENC_STREAM_S *) { abort(); }
CVI_S32 CVI_SYS_Munmap(void *, CVI_U32) { abort(); }
CVI_S32 CVI_VB_ReleaseBlock(VB_BLK) { abort(); }
CVI_S32 CVI_VB_DestroyPool(VB_POOL) { abort(); }
CVI_S32 CVI_VI_ReleaseChnFrame(VI_PIPE, VI_CHN, const VIDEO_FRAME_INFO_S *) {
    ++releases; return CVI_SUCCESS;
}
CVI_S32 SAMPLE_COMM_VI_Bind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { abort(); }
CVI_S32 SAMPLE_COMM_VI_UnBind_VPSS(VI_PIPE, VI_CHN, VPSS_GRP) { abort(); }
CVI_S32 CVI_VI_GetChnAttr(VI_PIPE, VI_CHN, VI_CHN_ATTR_S *) { abort(); }
CVI_S32 CVI_VPSS_GetChnAttr(VPSS_GRP, VPSS_CHN, VPSS_CHN_ATTR_S *) { abort(); }
CVI_S32 CVI_VPSS_DisableChn(VPSS_GRP, VPSS_CHN) { abort(); }
CVI_S32 CVI_VPSS_EnableChn(VPSS_GRP, VPSS_CHN) { abort(); }
int main() {
  for (bool direct : {false, true}) {
    stops = resets = destroys = releases = 0;
    vi_frame_from_vi[0] = direct;
    priv = {};
    priv.venc[1].is_inited = 1;
    priv.venc[1].is_running = 1;
    priv.venc_input_vi_ch[1] = 0;
    priv.vi_frame_valid[0] = true;
    priv.vi_frame_deferred[0] = true;

    // Stop, every reset and every destroy fail: nothing is released.
    vendor_error = CVI_FAILURE;
    assert(mmf_del_venc_channel(1) != 0);
    assert(stops == 1 && resets == 3 && destroys == 3);
    assert(releases == 0 && priv.vi_frame_valid[0]);
    assert(priv.venc[1].is_inited && priv.venc[1].is_running && priv.venc_input_vi_ch[1] == 0);

    // The retry destroys the channel: then the input is released.
    vendor_error = CVI_SUCCESS;
    assert(mmf_del_venc_channel(1) == 0);
    assert(releases == 1 && !priv.vi_frame_valid[0]);
    assert(!priv.venc[1].is_inited && !priv.venc[1].is_running && priv.venc_input_vi_ch[1] == -1);
  }
    puts("PASS real VENC teardown: a wedged channel keeps its input lease until it is destroyed.");
}
