// Host fault injection of the real MMF JPEG setter; no hardware is opened.
#include "../../support/additional/kvm_mmf/src/kvm_mmf.cpp"
// The IVE (the tiled MJPEG change map) is not exercised here; teardown frees it.
CVI_S32 CVI_SYS_FreeI(IVE_HANDLE, IVE_IMAGE_S *) { return 0; }
CVI_S32 CVI_IVE_DestroyHandle(IVE_HANDLE) { return 0; }
// Nor are the region JPEG channels; releasing a frame checks them.
CVI_S32 CVI_VENC_StopRecvFrame(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_ResetChn(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_DestroyChn(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_ReleaseStream(VENC_CHN, VENC_STREAM_S *) { return 0; }
#include <cassert>
static int gets, sets, get_error, set_error;
static CVI_U32 requested;
CVI_S32 CVI_VENC_GetJpegParam(VENC_CHN ch, VENC_JPEG_PARAM_S *p) {
    assert(ch == 0); ++gets; p->u32Qfactor = 80; p->u32MCUPerECS = 17; return get_error;
}
CVI_S32 CVI_VENC_SetJpegParam(VENC_CHN ch, const VENC_JPEG_PARAM_S *p) {
    assert(ch == 0 && p->u32MCUPerECS == 17 && p->u32Qfactor != 50); ++sets; requested=p->u32Qfactor; return set_error;
}
int main() {
    priv={}; priv.enc_jpg_is_init=1; priv.enc_jpg_quality=80;
    assert(_mmf_jpg_update_quality(0,80)==0 && gets==0 && sets==0);
    priv.enc_jpg_running=1;
    assert(_mmf_jpg_update_quality(0,90)!=0 && sets==0);
    priv.enc_jpg_running=0;
    // Accepted: 20..99 (the adaptive quality goes down to 30).
    for (int q : {0,19,100,65535}) assert(_mmf_jpg_update_quality(0,q)!=0 && sets==0);
    assert(_mmf_jpg_update_quality(-1,90)!=0 && _mmf_jpg_update_quality(MMF_VENC_MAX_CHN,90)!=0);
    get_error=-41; assert(_mmf_jpg_update_quality(0,90)==-41 && sets==0 && priv.enc_jpg_quality==80);
    get_error=0; set_error=-42;
    assert(_mmf_jpg_update_quality(0,90)==-42 && sets==1 && priv.enc_jpg_quality==80);
    set_error=0; assert(_mmf_jpg_update_quality(0,90)==0 && sets==2 && requested==90 && priv.enc_jpg_quality==90);
    assert(_mmf_jpg_update_quality(0,90)==0 && sets==2);
    assert(_mmf_jpg_update_quality(0,51)==0 && requested==51);
    const int before50 = sets;
    assert(_mmf_jpg_update_quality(0,50)==0 && sets==before50 && requested==51 && priv.enc_jpg_quality==51);
    assert(_mmf_jpg_update_quality(0,80)==0 && requested==80);
    assert(_mmf_jpg_update_quality(0,50)==0 && requested==51 && priv.enc_jpg_quality==51);
    for (int q=20; q<=99; ++q) assert(_mmf_jpg_qfactor(q)==(q==50 ? 51 : q));
    assert(_mmf_jpg_update_quality(0,30)==0 && requested==30 && priv.enc_jpg_quality==30);
    assert(_mmf_jpg_update_quality(0,20)==0 && requested==20 && priv.enc_jpg_quality==20);
    assert(_mmf_jpg_update_quality(0,99)==0 && requested==99);
    priv.enc_jpg_is_init=0; assert(_mmf_jpg_update_quality(0,80)!=0);
    puts("PASS real JPEG Qfactor: no-op, busy, invalid Q (outside 20..99)/channel, 20/30 accepted, 50 encoded as 51 without repeated writes, retained vendor fields, setter errors and retry.");
}
