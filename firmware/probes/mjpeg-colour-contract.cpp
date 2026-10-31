// Fault injection of the production VPSS setup; no hardware.
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
#include <cstring>
static bool modern;
static bool get_attr_error;
static int csc_calls, csc_fail_at, attr_calls, attr_fail_at, enables, starts, destroys;
static vpss_grp_csc_cfg last_csc;
static VPSS_CHN_ATTR_S last_attr;
extern "C" FILE *__wrap_fopen(const char *path, const char *) {
    assert(strcmp(path, "/proc/cvitek/vpss")==0);
    FILE *f=tmpfile(); assert(f);
    fputs(modern ? "NV21 BT709\n" : "NV21 UNKNOWN\n",f); rewind(f); return f;
}
CVI_S32 CVI_VPSS_GetChnFd(VPSS_GRP, VPSS_CHN) { return 7; }
extern "C" CVI_S32 vpss_set_grp_csc(CVI_S32 fd, const vpss_grp_csc_cfg *cfg) {
    assert(fd==7); ++csc_calls;
    if(csc_calls==csc_fail_at) return -42;
    last_csc=*cfg; return CVI_SUCCESS;
}
CVI_S32 CVI_VPSS_CreateGrp(VPSS_GRP, const VPSS_GRP_ATTR_S *) { return CVI_SUCCESS; }
CVI_S32 CVI_VPSS_ResetGrp(VPSS_GRP) { return CVI_SUCCESS; }
CVI_S32 CVI_VPSS_DestroyGrp(VPSS_GRP) { ++destroys; return CVI_SUCCESS; }
CVI_S32 CVI_VPSS_StartGrp(VPSS_GRP) { ++starts; return CVI_SUCCESS; }
CVI_S32 CVI_VPSS_GetGrpAttr(VPSS_GRP, VPSS_GRP_ATTR_S *a) {
    *a={};a->u32MaxW=1920;a->u32MaxH=1080;return CVI_SUCCESS;
}
CVI_S32 CVI_VPSS_SetChnCrop(VPSS_GRP, VPSS_CHN, const VPSS_CROP_INFO_S *) { return CVI_SUCCESS; }
CVI_S32 CVI_VPSS_SetChnAttr(VPSS_GRP, VPSS_CHN, const VPSS_CHN_ATTR_S *a) {
    ++attr_calls;if(attr_calls==attr_fail_at)return -43;last_attr=*a;return CVI_SUCCESS;
}
CVI_S32 CVI_VPSS_GetChnAttr(VPSS_GRP, VPSS_CHN, VPSS_CHN_ATTR_S *a) {
    if (get_attr_error) return -1;
    *a=last_attr; return CVI_SUCCESS;
}
CVI_S32 CVI_VPSS_EnableChn(VPSS_GRP, VPSS_CHN) { ++enables;return CVI_SUCCESS; }
static int channel(PIXEL_FORMAT_E format) {
    return _mmf_vpss_chn_init(0,0,1920,1080,format,60,2,false,false);
}
int main(int argc,char **argv) {
    assert(argc==2);modern=strcmp(argv[1],"new")==0;
    if(strcmp(argv[1],"input-failure")==0)csc_fail_at=1;
    int result=_mmf_vpss_init_new(0,1920,1080,PIXEL_FORMAT_UYVY);
    if(csc_fail_at==1) { assert(result!=0 && starts==0 && destroys==1);return 0; }
    assert(result==0 && starts==1 && last_csc.coef[0][0]==1192 && last_csc.sub[0]==16);
    if(strcmp(argv[1],"restore-failure")==0)csc_fail_at=2;
    if(strcmp(argv[1],"attr-failure")==0)attr_fail_at=2;
    result=channel(PIXEL_FORMAT_NV21);
    if(csc_fail_at==2 || attr_fail_at==2) { assert(result!=0 && enables==0);return 0; }
    assert(result==0 && enables==1);
    assert(last_attr.enVideoFormat==(modern ? VIDEO_FORMAT_LINEAR_BT709 : VIDEO_FORMAT_LINEAR));
    assert(last_csc.sub[0]==(modern ? 16 : 0));
    assert(last_csc.coef[0][0]==(modern ? 1192 : 1024));
    assert(channel(PIXEL_FORMAT_NV16)==0 && last_attr.enVideoFormat==VIDEO_FORMAT_LINEAR);
    // A source restart must preserve the chosen input/output colour pairing.
    assert(_mmf_vpss_init_new(0,1280,720,PIXEL_FORMAT_UYVY)==0);
    assert(last_csc.sub[0]==(modern ? 16 : 0));
    assert(channel(PIXEL_FORMAT_NV21)==0);
    assert(last_attr.enVideoFormat==(modern ? VIDEO_FORMAT_LINEAR_BT709 : VIDEO_FORMAT_LINEAR));
    priv.vi_chn_is_inited[0]=true;
    int before=attr_calls;
    assert(mmf_vi_chn_set_jfif(0,1)==(modern ? 1 : 0));
    assert(last_attr.enVideoFormat==VIDEO_FORMAT_LINEAR);
    assert(last_attr.u32Width==1920 && last_attr.u32Height==1080 && last_attr.enPixelFormat==PIXEL_FORMAT_NV21);
    assert(attr_calls==before+(modern ? 1 : 0));
    assert(mmf_vi_chn_set_jfif(0,1)==0);
    if (modern) {
        get_attr_error=true;assert(mmf_vi_chn_set_jfif(0,0)<0);get_attr_error=false;
        attr_fail_at=attr_calls+1;assert(mmf_vi_chn_set_jfif(0,0)<0);
        assert(last_attr.enVideoFormat==VIDEO_FORMAT_LINEAR);attr_fail_at=0;
        assert(mmf_vi_chn_set_jfif(0,0)==1 && last_attr.enVideoFormat==VIDEO_FORMAT_LINEAR_BT709);
    }
    puts("PASS VPSS input/output colour pairing and restart");
}
