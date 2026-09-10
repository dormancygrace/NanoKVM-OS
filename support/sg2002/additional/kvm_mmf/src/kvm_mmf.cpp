#include <stdint.h>
#include <string.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <unistd.h>
#include <sys/time.h>
#include <sys/param.h>
#include "math.h"
#include <inttypes.h>

#include <fcntl.h>		/* low-level i/o */
#include "cvi_buffer.h"
#include "cvi_ae_comm.h"
#include "cvi_awb_comm.h"
#include "cvi_comm_isp.h"
#include "cvi_comm_sns.h"
#include "cvi_ae.h"
#include "cvi_awb.h"
#include "cvi_isp.h"
#include "cvi_sns_ctrl.h"
#include "cvi_ive.h"
#include "cvi_sys.h"
#include "sample_comm.h"
#include "kvm_mmf.hpp"
#include "internal/frame_buffer.hpp"

#define MMF_VI_MAX_CHN 			2		// manually limit the max channel number of vi
#define MMF_RGN_MAX_NUM			16
#define MMF_VENC_MAX_CHN		4

#define MMF_VB_VI_ID			0

#if VPSS_MAX_PHY_CHN_NUM < MMF_VI_MAX_CHN
#error "VPSS_MAX_PHY_CHN_NUM < MMF_VI_MAX_CHN"
#endif

typedef struct {
	uint8_t ch;
	SIZE_S input;
	SIZE_S output;
	int fps;
	uint8_t depth;
	uint8_t fit; 	// fit = 0, width to new width, height to new height, may be stretch
					// fit = 1, keep aspect ratio, fill blank area with black color
					// fit = 2, keep aspect ratio, crop image to fit new size
	int input_fmt;
	int output_fmt;
} vpss_info_t;

typedef struct {
	uint8_t ch;
	uint8_t type;	// 0, jpg; 1, h265; 2, h264
	uint8_t is_inited;
	uint8_t is_used;
	uint8_t is_running;
	uint8_t use_vpss;
	VIDEO_FRAME_INFO_S *capture_frame;
	VENC_STREAM_S capture_stream;
	mmf_venc_cfg_t cfg;
	vpss_info_t vpss;
	uint32_t pool_id;
} venc_info_t;

typedef enum {
	MMF_MOD_VO,
	MMF_MOD_VI,
	MMF_MOD_VPSS,
	MMF_MOD_VENC,
	MMF_MOD_VDEC,
	MMF_MOD_REGION,
} mmf_mod_type_t;

typedef struct {
	char name[15];
	uint8_t is_used;
	mmf_mod_type_t mod;
	uint32_t pool_id;
	uint32_t size;
	uint32_t max_num;
} mmf_vb_pool_t;

typedef struct {
	int mmf_used_cnt;
	bool vi_is_inited;
	bool vi_chn_is_inited[MMF_VI_MAX_CHN];
	bool vi_chn_stopping[MMF_VI_MAX_CHN];
	bool vi_chn_disabled[MMF_VI_MAX_CHN];
	bool vi_source_bound[MMF_VI_MAX_CHN];
	int vi_chn_pool_id[MMF_VI_MAX_CHN];
	SIZE_S vi_size;
	VIDEO_FRAME_INFO_S vi_frame[MMF_VI_MAX_CHN];
	VB_CONFIG_S vb_conf;

	int ive_is_init;
	IVE_HANDLE ive_handle;
	IVE_IMAGE_S ive_rgb2yuv_rgb_img;
	IVE_IMAGE_S ive_rgb2yuv_yuv_img;
	int ive_rgb2yuv_w;
	int ive_rgb2yuv_h;

	bool rgn_is_init[MMF_RGN_MAX_NUM];
	bool rgn_is_bind[MMF_RGN_MAX_NUM];
	RGN_TYPE_E rgn_type[MMF_RGN_MAX_NUM];
	int rgn_id[MMF_RGN_MAX_NUM];
	MOD_ID_E rgn_mod_id[MMF_RGN_MAX_NUM];
	CVI_S32 rgn_dev_id[MMF_RGN_MAX_NUM];
	CVI_S32 rgn_chn_id[MMF_RGN_MAX_NUM];
	uint8_t* rgn_canvas_data[MMF_RGN_MAX_NUM];
	int rgn_canvas_w[MMF_RGN_MAX_NUM];
	int rgn_canvas_h[MMF_RGN_MAX_NUM];
	int rgn_canvas_format[MMF_RGN_MAX_NUM];

	int enc_jpg_is_init;
	VENC_STREAM_S enc_jpeg_frame;
	int enc_jpg_frame_w;
	int enc_jpg_frame_h;
	int enc_jpg_frame_fmt;
	int enc_jpg_running;
	int enc_jpg_quality;
	VIDEO_FRAME_INFO_S *enc_jpg_frame;
	int enc_jpg_input_pool_id;
	int enc_jpg_output_pool_id;

	int vb_of_vi_is_config : 1;
	int vb_of_private_is_config : 1;
	int vb_size_of_vi;
	int vb_count_of_vi;
	int vb_size_of_private;
	int vb_count_of_private;


	SAMPLE_SNS_TYPE_E sensor_type;

	venc_info_t venc[MMF_VENC_MAX_CHN];
	uint8_t h265_or_h264_is_used;

	mmf_vb_pool_t vb_pool[VB_MAX_COMM_POOLS];

	// Keep all existing member offsets stable for compatibility with the
	// vendor middleware; zero-copy state is appended at the end.
	bool vi_frame_valid[MMF_VI_MAX_CHN];
	bool vi_frame_mapped[MMF_VI_MAX_CHN];
	bool vi_frame_deferred[MMF_VI_MAX_CHN];
	bool venc_stream_acquired[MMF_VENC_MAX_CHN];
	int venc_input_vi_ch[MMF_VENC_MAX_CHN];
} priv_t;

typedef struct {
	int enc_jpg_enable : 1;
	bool vi_hmirror[MMF_VI_MAX_CHN];
	bool vi_vflip[MMF_VI_MAX_CHN];
} g_priv_t;

static priv_t priv;
static g_priv_t g_priv;
static VENC_PACK_S *venc_pack_storage[MMF_VENC_MAX_CHN];
static CVI_U32 venc_pack_capacity[MMF_VENC_MAX_CHN];

#define MODULE_NAME "soph_vi"

/*
 * Keep the former zero-copy hook's frame lifetime in the MMF implementation.
 * A frame released by the image path stays leased until VENC has consumed it.
 */
static int _mmf_release_vi_frame(int ch)
{
 if (ch < 0 || ch >= MMF_VI_MAX_CHN) return CVI_FAILURE;
 if (!priv.vi_frame_valid[ch]) return CVI_SUCCESS;
 for (int encoder = 0; encoder < MMF_VENC_MAX_CHN; ++encoder) {
  if (priv.venc[encoder].is_running && priv.venc_input_vi_ch[encoder] == ch)
   return CVI_ERR_VENC_BUSY;
 }
 VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
 if (priv.vi_frame_mapped[ch] && frame->stVFrame.pu8VirAddr[0]) {
  uint32_t image_size = frame->stVFrame.u32Length[0]
   + frame->stVFrame.u32Length[1] + frame->stVFrame.u32Length[2];
  const int unmapped = CVI_SYS_Munmap(frame->stVFrame.pu8VirAddr[0], image_size);
  if (unmapped != CVI_SUCCESS) return unmapped;
  frame->stVFrame.pu8VirAddr[0] = NULL;
  priv.vi_frame_mapped[ch] = false;
 }
 const int released = CVI_VPSS_ReleaseChnFrame(0, ch, frame);
 if (released != CVI_SUCCESS) {
  SAMPLE_PRT("CVI_VPSS_ReleaseChnFrame failed for ch %d: %#x\n", ch, released);
  return released;
 }
 memset(frame, 0, sizeof(*frame));
 priv.vi_frame_valid[ch] = false;
 priv.vi_frame_mapped[ch] = false;
 priv.vi_frame_deferred[ch] = false;
 return CVI_SUCCESS;
}

static int _mmf_release_all_vi_frames(void)
{
 for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) {
  const int result = _mmf_release_vi_frame(ch);
  if (result != CVI_SUCCESS) return result;
 }
 return CVI_SUCCESS;
}

static int _mmf_find_deferred_vi_frame(int width, int height, int format,
	const uint8_t *data, VIDEO_FRAME_INFO_S **frame_out)
{
	for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) {
		if (!priv.vi_frame_valid[ch] || !priv.vi_frame_deferred[ch]) {
			continue;
		}
		VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
		if ((int)frame->stVFrame.u32Width == width
			&& (int)frame->stVFrame.u32Height == height
			&& (int)frame->stVFrame.enPixelFormat == format && frame->stVFrame.pu8VirAddr[0] == data) {
			*frame_out = frame;
			return ch;
		}
	}
	return -1;
}

static int _mmf_acquire_vi_frame(int ch, int *len, int *width, int *height,
	int *format)
{
	VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
	_mmf_release_vi_frame(ch);
	if (priv.vi_frame_valid[ch]) return CVI_ERR_VENC_BUSY;
	memset(frame, 0, sizeof(*frame));
	if (CVI_VPSS_GetChnFrame(0, ch, frame, 1000) != CVI_SUCCESS) {
		return -1;
	}

	const uint64_t image_bytes = (uint64_t)frame->stVFrame.u32Length[0]
		+ frame->stVFrame.u32Length[1] + frame->stVFrame.u32Length[2];
	if (frame->stVFrame.u64PhyAddr[0] == 0 || image_bytes == 0 || image_bytes > INT32_MAX) {
		SAMPLE_PRT("invalid VI frame address or size\n");
		CVI_VPSS_ReleaseChnFrame(0, ch, frame);
		memset(frame, 0, sizeof(*frame));
		return -1;
	}

	priv.vi_frame_valid[ch] = true;
	priv.vi_frame_mapped[ch] = false;
	priv.vi_frame_deferred[ch] = false;
	*len = (int)image_bytes;
	*width = frame->stVFrame.u32Width;
	*height = frame->stVFrame.u32Height;
	*format = frame->stVFrame.enPixelFormat;
	return 0;
}

static void *_mmf_map_vi_frame(int ch)
{
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_frame_valid[ch]) {
		return NULL;
	}

	VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[ch];
	if (priv.vi_frame_mapped[ch]) {
		return frame->stVFrame.pu8VirAddr[0];
	}

	int image_size = frame->stVFrame.u32Length[0]
		+ frame->stVFrame.u32Length[1]
		+ frame->stVFrame.u32Length[2];
	CVI_VOID *vir_addr = CVI_SYS_MmapCache(frame->stVFrame.u64PhyAddr[0], image_size);
	if (vir_addr == NULL) {
		SAMPLE_PRT("CVI_SYS_MmapCache failed for VI frame\n");
		return NULL;
	}
	CVI_SYS_IonInvalidateCache(frame->stVFrame.u64PhyAddr[0], vir_addr, image_size);
	frame->stVFrame.pu8VirAddr[0] = (CVI_U8 *)vir_addr;
	priv.vi_frame_mapped[ch] = true;
	return vir_addr;
}

static int _create_vb_pool(char *name, mmf_mod_type_t mod, uint32_t size, uint32_t max_num)
{
	uint32_t pool_id = -1;
	VB_POOL_CONFIG_S stVbPoolCfg;
	stVbPoolCfg.u32BlkCnt = max_num;
	stVbPoolCfg.u32BlkSize = size;
	stVbPoolCfg.enRemapMode = VB_REMAP_MODE_CACHED;

	if (max_num == 0 || size == 0) {
		return -1;
	}

	pool_id = CVI_VB_CreatePool(&stVbPoolCfg);
	if (pool_id == VB_INVALID_POOLID || pool_id >= VB_MAX_COMM_POOLS) {
		return -2;
	}

	mmf_vb_pool_t *info = (mmf_vb_pool_t *)&priv.vb_pool[pool_id];
	info->pool_id = pool_id;
	strncpy(info->name, name, sizeof(info->name));
	info->mod = mod;
	info->size = size;
	info->max_num = max_num;
	info->is_used = 1;

	return pool_id;
}

static int _destroy_vb_pool(uint32_t pool_id)
{
	if (pool_id == VB_INVALID_POOLID) return CVI_SUCCESS;
	if (pool_id >= VB_MAX_COMM_POOLS) return CVI_FAILURE;
	CVI_S32 s32Ret;
	mmf_vb_pool_t *info = (mmf_vb_pool_t *)&priv.vb_pool[pool_id];
	if (info->is_used) {
		s32Ret =  CVI_VB_DestroyPool(pool_id);
		if (s32Ret != CVI_SUCCESS) {
			printf("CVI_VB_DestroyPool : %d fail!\n", pool_id);
			return -1;
		}
		memset(info, 0, sizeof(mmf_vb_pool_t));
	}

	return 0;
}

__attribute__((unused)) static void _list_vb_pool(void)
{
	printf("====== VB POOL =======\r\n");
	for (int pool_id = 0; pool_id < VB_MAX_COMM_POOLS; pool_id ++) {
		mmf_vb_pool_t *info = (mmf_vb_pool_t *)&priv.vb_pool[pool_id];
		if (info->is_used) {
			printf("[%d] name:%s size:%d num:%d\r\n", pool_id, info->name, info->size, info->max_num);
		}
	}
	printf("\r\n");
}

static SAMPLE_VI_CONFIG_S g_stViConfig;
static SAMPLE_INI_CFG_S g_stIniCfg;
static CVI_S32 _mmf_vpss_deinit_new(VPSS_GRP VpssGrp);

static inline CVI_VOID VENC_GetPicBufferConfig2(CVI_U32 u32Width, CVI_U32 u32Height,
	PIXEL_FORMAT_E enPixelFormat, DATA_BITWIDTH_E enBitWidth, COMPRESS_MODE_E enCmpMode,
	VB_CAL_CONFIG_S *pstVbCfg)
{
	CVI_U32 u32AlignWidth = ALIGN(u32Width, VENC_ALIGN_W);
	CVI_U32 u32AlignHeight = u32Height;
	CVI_U32 u32Align = VENC_ALIGN_W;

	COMMON_GetPicBufferConfig(u32AlignWidth, u32AlignHeight, enPixelFormat,
		enBitWidth, enCmpMode, u32Align, pstVbCfg);
}

static VIDEO_FRAME_INFO_S *_mmf_alloc_frame(int id, SIZE_S size, PIXEL_FORMAT_E format)
{
    if (!size.u32Width || !size.u32Height || size.u32Width > UINT16_MAX || size.u32Height > UINT16_MAX)
        return NULL;
    VB_CAL_CONFIG_S layout{};
    VENC_GetPicBufferConfig2(size.u32Width, size.u32Height, format,
        DATA_BITWIDTH_8, COMPRESS_MODE_NONE, &layout);
    return nanokvm::media::allocate(id, size, format, layout);
}

static CVI_S32 _mmf_free_frame(VIDEO_FRAME_INFO_S *frame)
{
    return nanokvm::media::release(frame);
}

static int cvi_ive_init(void)
{
	CVI_S32 s32Ret;
	if (priv.ive_is_init)
		return 0;

	priv.ive_rgb2yuv_w = 640;
	priv.ive_rgb2yuv_h = 480;
	priv.ive_handle = CVI_IVE_CreateHandle();
	if (priv.ive_handle == NULL) {
		printf("CVI_IVE_CreateHandle failed!\n");
		return -1;
	}

	s32Ret = CVI_IVE_CreateImage_Cached(priv.ive_handle, &priv.ive_rgb2yuv_rgb_img, IVE_IMAGE_TYPE_U8C3_PACKAGE, priv.ive_rgb2yuv_w, priv.ive_rgb2yuv_h);
	if (s32Ret != CVI_SUCCESS) {
		printf("Create src image failed!\n");
		CVI_IVE_DestroyHandle(priv.ive_handle);
		return -1;
	}

	s32Ret = CVI_IVE_CreateImage_Cached(priv.ive_handle, &priv.ive_rgb2yuv_yuv_img, IVE_IMAGE_TYPE_YUV420SP, priv.ive_rgb2yuv_w, priv.ive_rgb2yuv_h);
	if (s32Ret != CVI_SUCCESS) {
		printf("Create src image failed!\n");
		CVI_IVE_DestroyHandle(priv.ive_handle);
		return -1;
	}

	priv.ive_is_init = 1;
	return 0;
}

static int cvi_ive_deinit(void)
{
	if (priv.ive_is_init == 0)
		return 0;

	CVI_SYS_FreeI(priv.ive_handle, &priv.ive_rgb2yuv_rgb_img);
	CVI_SYS_FreeI(priv.ive_handle, &priv.ive_rgb2yuv_yuv_img);
	CVI_IVE_DestroyHandle(priv.ive_handle);

	priv.ive_is_init = 0;
	return 0;
}

[[maybe_unused]] static int cvi_rgb2nv21(uint8_t *src, int input_w, int input_h)
{
	CVI_S32 s32Ret;

	int width = ALIGN(input_w, DEFAULT_ALIGN);
	int height = input_h;

	if (width != priv.ive_rgb2yuv_w || height != priv.ive_rgb2yuv_h) {
		CVI_SYS_FreeI(priv.ive_handle, &priv.ive_rgb2yuv_rgb_img);
		CVI_SYS_FreeI(priv.ive_handle, &priv.ive_rgb2yuv_yuv_img);
		priv.ive_rgb2yuv_w = width;
		priv.ive_rgb2yuv_h = height;
		printf("reinit rgb2nv21 buffer, buff w:%d h:%d\n", priv.ive_rgb2yuv_w, priv.ive_rgb2yuv_h);
		s32Ret = CVI_IVE_CreateImage_Cached(priv.ive_handle, &priv.ive_rgb2yuv_rgb_img, IVE_IMAGE_TYPE_U8C3_PACKAGE, priv.ive_rgb2yuv_w, priv.ive_rgb2yuv_h);
		if (s32Ret != CVI_SUCCESS) {
			printf("Create src image failed!\n");
			return -1;
		}

		s32Ret = CVI_IVE_CreateImage_Cached(priv.ive_handle, &priv.ive_rgb2yuv_yuv_img, IVE_IMAGE_TYPE_YUV420SP, priv.ive_rgb2yuv_w, priv.ive_rgb2yuv_h);
		if (s32Ret != CVI_SUCCESS) {
			printf("Create src image failed!\n");
			return -1;
		}
	}

	if (width != input_w) {
		for (int h = 0; h < height; h++) {
			memcpy((uint8_t *)priv.ive_rgb2yuv_rgb_img.u64VirAddr[0] + width * h * 3, (uint8_t *)src + input_w * h * 3, input_w * 3);
		}
	} else {
		memcpy((uint8_t *)priv.ive_rgb2yuv_rgb_img.u64VirAddr[0], (uint8_t *)src, width * height * 3);
	}

	IVE_CSC_CTRL_S stCtrl;
	stCtrl.enMode = IVE_CSC_MODE_VIDEO_BT601_RGB2YUV;
	s32Ret = CVI_IVE_CSC(priv.ive_handle, &priv.ive_rgb2yuv_rgb_img, &priv.ive_rgb2yuv_yuv_img, &stCtrl, 1);
	if (s32Ret != CVI_SUCCESS) {
		printf("Run HW IVE CSC YUV2RGB failed!\n");
		return -1;
	}
	return 0;
}

static int _try_release_sys(void)
{
	CVI_S32 s32Ret = CVI_FAILURE;
	SAMPLE_INI_CFG_S	   	stIniCfg;
	SAMPLE_VI_CONFIG_S 		stViConfig;
	if (SAMPLE_COMM_VI_ParseIni(&stIniCfg)) {
		SAMPLE_PRT("Parse complete\n");
		return s32Ret;
	}

	priv.sensor_type = stIniCfg.enSnsType[0];

	s32Ret = CVI_VI_SetDevNum(stIniCfg.devNum);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("VI_SetDevNum failed with %#x\n", s32Ret);
		return s32Ret;
	}

	s32Ret = SAMPLE_COMM_VI_IniToViCfg(&stIniCfg, &stViConfig);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_VI_IniToViCfg failed with %#x\n", s32Ret);
		return s32Ret;
	}

	s32Ret = SAMPLE_COMM_VI_DestroyIsp(&stViConfig);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_VI_DestroyIsp failed with %#x\n", s32Ret);
		return s32Ret;
	}

	s32Ret = SAMPLE_COMM_VI_DestroyVi(&stViConfig);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_VI_DestroyVi failed with %#x\n", s32Ret);
		return s32Ret;
	}

	SAMPLE_COMM_SYS_Exit();
	return s32Ret;
}

int _try_release_vi(void)
{
	CVI_S32 s32Ret = CVI_FAILURE;
	s32Ret = mmf_del_vi_channel_all();
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("mmf_del_vi_channel_all failed with %#x\n", s32Ret);
		return s32Ret;
	}
	return s32Ret;
}

int _try_release_venc_all(void)
{
	for (int ch = 0; ch < VENC_MAX_CHN_NUM; ch ++) {
		CVI_VENC_StopRecvFrame(ch);
		CVI_VENC_ResetChn(ch);
		CVI_VENC_DestroyChn(ch);
	}
	return 0;
}

int _try_release_vpss_all(void)
{
	for (int ch = 0; ch < 4; ch ++) {
		_mmf_vpss_deinit_new(ch);
	}
	return 0;
}

static void _mmf_sys_exit(void)
{
	if (g_stViConfig.s32WorkingViNum != 0) {
		SAMPLE_COMM_VI_DestroyIsp(&g_stViConfig);
		SAMPLE_COMM_VI_DestroyVi(&g_stViConfig);
	}
	SAMPLE_COMM_SYS_Exit();
}

static CVI_S32 _mmf_sys_init(SIZE_S stSize)
{
	VB_CONFIG_S	   stVbConf;
	CVI_U32        u32BlkSize, u32BlkRotSize;
	CVI_S32 s32Ret = CVI_SUCCESS;
	COMPRESS_MODE_E    enCompressMode   = COMPRESS_MODE_NONE;

	memset(&stVbConf, 0, sizeof(VB_CONFIG_S));
	memcpy(&stVbConf, &priv.vb_conf, sizeof(VB_CONFIG_S));

	// vi
	u32BlkSize = COMMON_GetPicBufferSize(stSize.u32Width, stSize.u32Height, PIXEL_FORMAT_UYVY,
		DATA_BITWIDTH_8, enCompressMode, DEFAULT_ALIGN);
	u32BlkRotSize = COMMON_GetPicBufferSize(stSize.u32Height, stSize.u32Width, PIXEL_FORMAT_UYVY,
		DATA_BITWIDTH_8, enCompressMode, DEFAULT_ALIGN);
	u32BlkSize = MAX(u32BlkSize, u32BlkRotSize);
	stVbConf.astCommPool[MMF_VB_VI_ID].u32BlkSize	= u32BlkSize;
	stVbConf.astCommPool[MMF_VB_VI_ID].u32BlkCnt	= 3;
	stVbConf.astCommPool[MMF_VB_VI_ID].enRemapMode	= VB_REMAP_MODE_CACHED;
	stVbConf.u32MaxPoolCnt = 1;

	s32Ret = SAMPLE_COMM_SYS_Init(&stVbConf);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("system init failed with %#x\n", s32Ret);
		goto error;
	}

	return s32Ret;
error:
	_mmf_sys_exit();
	return s32Ret;
}

static CVI_S32 _mmf_vpss_deinit(VPSS_GRP VpssGrp, VPSS_CHN VpssChn)
{
	CVI_BOOL           abChnEnable[VPSS_MAX_PHY_CHN_NUM] = {0};
	CVI_S32 s32Ret = CVI_SUCCESS;

	/*start vpss*/
	abChnEnable[VpssChn] = CVI_TRUE;
	s32Ret = SAMPLE_COMM_VPSS_Stop(VpssGrp, abChnEnable);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("init vpss group failed. s32Ret: 0x%x !\n", s32Ret);
	}

	return s32Ret;
}

static CVI_S32 _mmf_vpss_deinit_new(VPSS_GRP VpssGrp)
{
	CVI_S32 s32Ret = CVI_SUCCESS;

	s32Ret = CVI_VPSS_StopGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("Vpss Stop Grp %d failed! Please check param\n", VpssGrp);
		return CVI_FAILURE;
	}

	s32Ret = CVI_VPSS_DestroyGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("Vpss Destroy Grp %d failed! Please check\n", VpssGrp);
		return CVI_FAILURE;
	}

	return s32Ret;
}

// fit = 0, width to new width, height to new height, may be stretch
// fit = 1, keep aspect ratio, fill blank area with black color
// fit = other, keep aspect ratio, crop image to fit new size
static CVI_S32 _mmf_vpss_init(VPSS_GRP VpssGrp, VPSS_CHN VpssChn, SIZE_S stSizeIn, SIZE_S stSizeOut, PIXEL_FORMAT_E formatIn, PIXEL_FORMAT_E formatOut,
int fps, int depth, bool mirror, bool flip, int fit)
{
	VPSS_GRP_ATTR_S    stVpssGrpAttr;
	VPSS_CROP_INFO_S   stGrpCropInfo;
	CVI_BOOL           abChnEnable[VPSS_MAX_PHY_CHN_NUM] = {0};
	VPSS_CHN_ATTR_S    astVpssChnAttr[VPSS_MAX_PHY_CHN_NUM];
	CVI_S32 s32Ret = CVI_SUCCESS;

	memset(&stVpssGrpAttr, 0, sizeof(VPSS_GRP_ATTR_S));
	stVpssGrpAttr.stFrameRate.s32SrcFrameRate    = -1;
	stVpssGrpAttr.stFrameRate.s32DstFrameRate    = -1;
	stVpssGrpAttr.enPixelFormat                  = formatIn;
	stVpssGrpAttr.u32MaxW                        = stSizeIn.u32Width;
	stVpssGrpAttr.u32MaxH                        = stSizeIn.u32Height;
	stVpssGrpAttr.u8VpssDev                      = 0;

	CVI_FLOAT corp_scale_w = (CVI_FLOAT)stSizeIn.u32Width / stSizeOut.u32Width;
	CVI_FLOAT corp_scale_h = (CVI_FLOAT)stSizeIn.u32Height / stSizeOut.u32Height;
	CVI_U32 crop_w = -1, crop_h = -1;
	if (fit == 0) {
		memset(astVpssChnAttr, 0, sizeof(VPSS_CHN_ATTR_S) * VPSS_MAX_PHY_CHN_NUM);
		astVpssChnAttr[VpssChn].u32Width                    = stSizeOut.u32Width;
		astVpssChnAttr[VpssChn].u32Height                   = stSizeOut.u32Height;
		astVpssChnAttr[VpssChn].enVideoFormat               = VIDEO_FORMAT_LINEAR;
		astVpssChnAttr[VpssChn].enPixelFormat               = formatOut;
		astVpssChnAttr[VpssChn].stFrameRate.s32SrcFrameRate = fps;
		astVpssChnAttr[VpssChn].stFrameRate.s32DstFrameRate = fps;
		astVpssChnAttr[VpssChn].u32Depth                    = depth;
		astVpssChnAttr[VpssChn].bMirror                     = mirror;
		astVpssChnAttr[VpssChn].bFlip                       = flip;
		astVpssChnAttr[VpssChn].stAspectRatio.enMode        = ASPECT_RATIO_MANUAL;
		astVpssChnAttr[VpssChn].stAspectRatio.stVideoRect.s32X       = 0;
		astVpssChnAttr[VpssChn].stAspectRatio.stVideoRect.s32Y       = 0;
		astVpssChnAttr[VpssChn].stAspectRatio.stVideoRect.u32Width   = stSizeOut.u32Width;
		astVpssChnAttr[VpssChn].stAspectRatio.stVideoRect.u32Height  = stSizeOut.u32Height;
		astVpssChnAttr[VpssChn].stAspectRatio.bEnableBgColor = CVI_TRUE;
		astVpssChnAttr[VpssChn].stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
		astVpssChnAttr[VpssChn].stNormalize.bEnable         = CVI_FALSE;

		stGrpCropInfo.bEnable = false;
	} else if (fit == 1) {
		memset(astVpssChnAttr, 0, sizeof(VPSS_CHN_ATTR_S) * VPSS_MAX_PHY_CHN_NUM);
		astVpssChnAttr[VpssChn].u32Width                    = stSizeOut.u32Width;
		astVpssChnAttr[VpssChn].u32Height                   = stSizeOut.u32Height;
		astVpssChnAttr[VpssChn].enVideoFormat               = VIDEO_FORMAT_LINEAR;
		astVpssChnAttr[VpssChn].enPixelFormat               = formatOut;
		astVpssChnAttr[VpssChn].stFrameRate.s32SrcFrameRate = fps;
		astVpssChnAttr[VpssChn].stFrameRate.s32DstFrameRate = fps;
		astVpssChnAttr[VpssChn].u32Depth                    = depth;
		astVpssChnAttr[VpssChn].bMirror                     = mirror;
		astVpssChnAttr[VpssChn].bFlip                       = flip;
		astVpssChnAttr[VpssChn].stAspectRatio.enMode        = ASPECT_RATIO_AUTO;
		astVpssChnAttr[VpssChn].stAspectRatio.bEnableBgColor = CVI_TRUE;
		astVpssChnAttr[VpssChn].stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
		astVpssChnAttr[VpssChn].stNormalize.bEnable         = CVI_FALSE;

		stGrpCropInfo.bEnable = false;
	} else {
		memset(astVpssChnAttr, 0, sizeof(VPSS_CHN_ATTR_S) * VPSS_MAX_PHY_CHN_NUM);
		astVpssChnAttr[VpssChn].u32Width                    = stSizeOut.u32Width;
		astVpssChnAttr[VpssChn].u32Height                   = stSizeOut.u32Height;
		astVpssChnAttr[VpssChn].enVideoFormat               = VIDEO_FORMAT_LINEAR;
		astVpssChnAttr[VpssChn].enPixelFormat               = formatOut;
		astVpssChnAttr[VpssChn].stFrameRate.s32SrcFrameRate = fps;
		astVpssChnAttr[VpssChn].stFrameRate.s32DstFrameRate = fps;
		astVpssChnAttr[VpssChn].u32Depth                    = depth;
		astVpssChnAttr[VpssChn].bMirror                     = mirror;
		astVpssChnAttr[VpssChn].bFlip                       = flip;
		astVpssChnAttr[VpssChn].stAspectRatio.enMode        = ASPECT_RATIO_AUTO;
		astVpssChnAttr[VpssChn].stAspectRatio.bEnableBgColor = CVI_TRUE;
		astVpssChnAttr[VpssChn].stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
		astVpssChnAttr[VpssChn].stNormalize.bEnable         = CVI_FALSE;

		crop_w = corp_scale_w < corp_scale_h ? stSizeOut.u32Width * corp_scale_w: stSizeOut.u32Width * corp_scale_h;
		crop_h = corp_scale_w < corp_scale_h ? stSizeOut.u32Height * corp_scale_w: stSizeOut.u32Height * corp_scale_h;
		if (corp_scale_h < 0 || corp_scale_w < 0) {
			SAMPLE_PRT("crop scale error. corp_scale_w: %f, corp_scale_h: %f\n", corp_scale_w, corp_scale_h);
			goto error;
		}

		stGrpCropInfo.bEnable = true;
		stGrpCropInfo.stCropRect.s32X = (stSizeIn.u32Width - crop_w) / 2;
		stGrpCropInfo.stCropRect.s32Y = (stSizeIn.u32Height - crop_h) / 2;
		stGrpCropInfo.stCropRect.u32Width = crop_w;
		stGrpCropInfo.stCropRect.u32Height = crop_h;
	}

	/*start vpss*/
	abChnEnable[0] = CVI_TRUE;
	s32Ret = SAMPLE_COMM_VPSS_Init(VpssGrp, abChnEnable, &stVpssGrpAttr, astVpssChnAttr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("init vpss group failed. s32Ret: 0x%x ! retry!!!\n", s32Ret);
		s32Ret = SAMPLE_COMM_VPSS_Stop(VpssGrp, abChnEnable);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("stop vpss group failed. s32Ret: 0x%x !\n", s32Ret);
		}
		s32Ret = SAMPLE_COMM_VPSS_Init(VpssGrp, abChnEnable, &stVpssGrpAttr, astVpssChnAttr);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("retry to init vpss group failed. s32Ret: 0x%x !\n", s32Ret);
			return s32Ret;
		} else {
			SAMPLE_PRT("retry to init vpss group ok!\n");
		}
	}

	if (crop_w != 0 && crop_h != 0) {
		s32Ret = CVI_VPSS_SetChnCrop(VpssGrp, VpssChn, &stGrpCropInfo);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("set vpss group crop failed. s32Ret: 0x%x !\n", s32Ret);
			goto error;
		}
	}

	s32Ret = SAMPLE_COMM_VPSS_Start(VpssGrp, abChnEnable, &stVpssGrpAttr, astVpssChnAttr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("start vpss group failed. s32Ret: 0x%x !\n", s32Ret);
		goto error;
	}

	return s32Ret;
error:
	_mmf_vpss_deinit(VpssGrp, VpssChn);
	return s32Ret;
}

static CVI_S32 _mmf_init(void)
{
	MMF_VERSION_S stVersion;
	SAMPLE_INI_CFG_S	   stIniCfg;
	SAMPLE_VI_CONFIG_S stViConfig;

	PIC_SIZE_E enPicSize;
	SIZE_S stSize;
	CVI_S32 s32Ret = CVI_SUCCESS;
	LOG_LEVEL_CONF_S log_conf;

	// Kernel modules are managed by board startup, not by a media client.
	CVI_SYS_GetVersion(&stVersion);
	SAMPLE_PRT("maix multi-media version:%s\n", stVersion.version);

	log_conf.enModId = CVI_ID_LOG;
	log_conf.s32Level = CVI_DBG_DEBUG;
	CVI_LOG_SetLevelConf(&log_conf);

	// Get config from ini if found.
	if (SAMPLE_COMM_VI_ParseIni(&stIniCfg)) {
		SAMPLE_PRT("Parse complete\n");
	}

	//Set sensor number
	CVI_VI_SetDevNum(stIniCfg.devNum);

	/************************************************
	 * step1:  Config VI
	 ************************************************/
	s32Ret = SAMPLE_COMM_VI_IniToViCfg(&stIniCfg, &stViConfig);
	if (s32Ret != CVI_SUCCESS)
		return s32Ret;

	memcpy(&g_stViConfig, &stViConfig, sizeof(SAMPLE_VI_CONFIG_S));
	memcpy(&g_stIniCfg, &stIniCfg, sizeof(SAMPLE_INI_CFG_S));

	/************************************************
	 * step2:  Get input size
	 ************************************************/
	s32Ret = SAMPLE_COMM_VI_GetSizeBySensor(stIniCfg.enSnsType[0], &enPicSize);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_VI_GetSizeBySensor failed with %#x "
			"(parsed sensor=%d, expected LT6911=%d)\n", s32Ret,
			(int)stIniCfg.enSnsType[0], (int)LONTIUM_LT6911_2M_60FPS_8BIT);
		return s32Ret;
	}

	s32Ret = SAMPLE_COMM_SYS_GetPicSize(enPicSize, &stSize);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("SAMPLE_COMM_SYS_GetPicSize failed with %#x\n", s32Ret);
		return s32Ret;
	}

	/************************************************
	 * step3:  Init modules
	 ************************************************/
	// Use the SYS/VB lifecycle APIs; debug dumps do not prove buffer ownership.
	s32Ret = _mmf_sys_init(stSize);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("sys init failed. s32Ret: 0x%x !\n", s32Ret);
		goto _need_exit_sys_and_deinit_vi;
	}

	s32Ret = SAMPLE_PLAT_VI_INIT(&stViConfig);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("vi init failed. s32Ret: 0x%x !\n", s32Ret);
		SAMPLE_PRT("Please try to check if the camera is working.\n");
		goto _need_exit_sys_and_deinit_vi;
	}

	priv.vi_size.u32Width = stSize.u32Width;
	priv.vi_size.u32Height = stSize.u32Height;

	return s32Ret;

_need_exit_sys_and_deinit_vi:
	_mmf_sys_exit();

	return s32Ret;
}

static int _mmf_deinit(void)
{
	UNUSED(cvi_ive_deinit);
	/*
	 * Encoders can still own input frames from VI. Tear them down before VI so
	 * those frames are returned while their producer is still alive. JPEG also
	 * owns a separate MMF reference; forced process teardown has already reset
	 * the reference count, so its public deinit can safely release its pools
	 * here without recursively entering this function.
	 */
	const int video_stopped = mmf_del_venc_channel_all();
	if (video_stopped != CVI_SUCCESS) return video_stopped;
	mmf_enc_jpg_deinit(0);
	const int vi_stopped = mmf_del_vi_channel_all();
	if (vi_stopped != CVI_SUCCESS) return vi_stopped;
	_try_release_venc_all();
	_try_release_vpss_all();
	mmf_vi_deinit();
	// mmf_del_region_channel_all();		// need not release
	_mmf_sys_exit();
	return CVI_SUCCESS;
}

static int _vi_get_unused_ch() {
	for (int i = 0; i < MMF_VI_MAX_CHN; i++) {
		if (priv.vi_chn_is_inited[i] == false) {
			return i;
		}
	}
	return -1;
}

int mmf_init(void)
{
    if (priv.mmf_used_cnt) {
		priv.mmf_used_cnt ++;
        // printf("maix multi-media already inited(cnt:%d)\n", priv.mmf_used_cnt);
        return 0;
    }

	if (_try_release_sys() != CVI_SUCCESS) {
		printf("try release sys failed\n");
		return -1;
	} else {
		printf("try release sys ok\n");
	}

    if (_mmf_init() != CVI_SUCCESS) {
        printf("maix multi-media init failed\n");
        return -1;
    } else {
		printf("maix multi-media init ok\n");
	}

	UNUSED(cvi_ive_init);
	priv.mmf_used_cnt = 1;

	if (_try_release_vi() != CVI_SUCCESS) {
		printf("try release vio failed\n");
		return -1;
	} else {
		printf("try release vio ok\n");
	}

	if (_try_release_venc_all() != CVI_SUCCESS) {
		printf("try release venc failed\n");
		return -1;
	} else {
		printf("try release venc ok\n");
	}

    return 0;
}

bool mmf_is_init(void)
{
    return priv.mmf_used_cnt > 0 ? true : false;
}

int mmf_try_deinit(bool force) {
	if (!priv.mmf_used_cnt) {
		return 0;
	}
	const int previous_count = priv.mmf_used_cnt;
	if (!force && --priv.mmf_used_cnt) return 0;
	// JPEG teardown can drop its extra reference recursively. Keep the count
	// zero during teardown, but restore ownership if video could not drain.
	priv.mmf_used_cnt = 0;
	const int result = _mmf_deinit();
	if (result != CVI_SUCCESS) {
		priv.mmf_used_cnt = previous_count;
		return result;
	}
	printf("maix multi-media driver destroyed.\n");
	return CVI_SUCCESS;
}

int mmf_deinit(void) {
    return mmf_try_deinit(false);
}

int mmf_get_vi_unused_channel(void) {
	return _vi_get_unused_ch();
}

static CVI_S32 _mmf_vpss_chn_init(VPSS_GRP VpssGrp, VPSS_CHN VpssChn, int width, int height, PIXEL_FORMAT_E format, int fps, int depth, bool mirror, bool flip, int fit)
{
#if 1
	VPSS_GRP_ATTR_S stGrpAttr;
	VPSS_CROP_INFO_S   stChnCropInfo;
	VPSS_CHN_ATTR_S chn_attr = {};
	CVI_S32 s32Ret = CVI_SUCCESS;

	s32Ret = CVI_VPSS_GetGrpAttr(VpssGrp, &stGrpAttr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_GetGrpAttr failed. s32Ret: 0x%x !\n", s32Ret);
		return s32Ret;
	}
	CVI_FLOAT corp_scale_w = (CVI_FLOAT)stGrpAttr.u32MaxW / width;
	CVI_FLOAT corp_scale_h = (CVI_FLOAT)stGrpAttr.u32MaxH / height;
	CVI_U32 crop_w = -1, crop_h = -1;
	if (fit == 0) {
		chn_attr.u32Width                    = width;
		chn_attr.u32Height                   = height;
		chn_attr.enVideoFormat               = VIDEO_FORMAT_LINEAR;
		chn_attr.enPixelFormat               = format;
		chn_attr.stFrameRate.s32SrcFrameRate = fps;
		chn_attr.stFrameRate.s32DstFrameRate = fps;
		chn_attr.u32Depth                    = depth;
		chn_attr.bMirror                     = mirror;
		chn_attr.bFlip                       = flip;
		chn_attr.stAspectRatio.enMode        = ASPECT_RATIO_MANUAL;
		chn_attr.stAspectRatio.stVideoRect.s32X       = 0;
		chn_attr.stAspectRatio.stVideoRect.s32Y       = 0;
		chn_attr.stAspectRatio.stVideoRect.u32Width   = width;
		chn_attr.stAspectRatio.stVideoRect.u32Height  = height;
		chn_attr.stAspectRatio.bEnableBgColor = CVI_TRUE;
		chn_attr.stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
		chn_attr.stNormalize.bEnable         = CVI_FALSE;

		stChnCropInfo.bEnable = false;
	} else if (fit == 1) {
		chn_attr.u32Width                    = width;
		chn_attr.u32Height                   = height;
		chn_attr.enVideoFormat               = VIDEO_FORMAT_LINEAR;
		chn_attr.enPixelFormat               = format;
		chn_attr.stFrameRate.s32SrcFrameRate = fps;
		chn_attr.stFrameRate.s32DstFrameRate = fps;
		chn_attr.u32Depth                    = depth;
		chn_attr.bMirror                     = mirror;
		chn_attr.bFlip                       = flip;
		chn_attr.stAspectRatio.enMode        = ASPECT_RATIO_AUTO;
		chn_attr.stAspectRatio.bEnableBgColor = CVI_TRUE;
		chn_attr.stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
		chn_attr.stNormalize.bEnable         = CVI_FALSE;

		stChnCropInfo.bEnable = false;
	} else {
		chn_attr.u32Width                    = width;
		chn_attr.u32Height                   = height;
		chn_attr.enVideoFormat               = VIDEO_FORMAT_LINEAR;
		chn_attr.enPixelFormat               = format;
		chn_attr.stFrameRate.s32SrcFrameRate = fps;
		chn_attr.stFrameRate.s32DstFrameRate = fps;
		chn_attr.u32Depth                    = depth;
		chn_attr.bMirror                     = mirror;
		chn_attr.bFlip                       = flip;
		chn_attr.stAspectRatio.enMode        = ASPECT_RATIO_AUTO;
		chn_attr.stAspectRatio.bEnableBgColor = CVI_TRUE;
		chn_attr.stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
		chn_attr.stNormalize.bEnable         = CVI_FALSE;

		crop_w = corp_scale_w < corp_scale_h ? width * corp_scale_w: width * corp_scale_h;
		crop_h = corp_scale_w < corp_scale_h ? height * corp_scale_w: height * corp_scale_h;
		if (corp_scale_h < 0 || corp_scale_w < 0) {
			SAMPLE_PRT("crop scale error. corp_scale_w: %f, corp_scale_h: %f\n", corp_scale_w, corp_scale_h);
			return -1;
		}

		stChnCropInfo.bEnable = true;
		stChnCropInfo.stCropRect.s32X = (stGrpAttr.u32MaxW - crop_w) / 2;
		stChnCropInfo.stCropRect.s32Y = (stGrpAttr.u32MaxH - crop_h) / 2;
		stChnCropInfo.stCropRect.u32Width = crop_w;
		stChnCropInfo.stCropRect.u32Height = crop_h;
	}

	if (crop_w != 0 && crop_h != 0) {
		s32Ret = CVI_VPSS_SetChnCrop(VpssGrp, VpssChn, &stChnCropInfo);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("set vpss group crop failed. s32Ret: 0x%x !\n", s32Ret);
			return -1;
		}
	}

	s32Ret = CVI_VPSS_SetChnAttr(VpssGrp, VpssChn, &chn_attr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_SetChnAttr failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}

	s32Ret = CVI_VPSS_EnableChn(VpssGrp, VpssChn);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_EnableChn failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}

	return s32Ret;
#else
	CVI_S32 s32Ret;
	VPSS_CHN_ATTR_S chn_attr = {};
	chn_attr.u32Width                    = width;
	chn_attr.u32Height                   = height;
	chn_attr.enVideoFormat               = VIDEO_FORMAT_LINEAR;
	chn_attr.enPixelFormat               = format;
	chn_attr.stFrameRate.s32SrcFrameRate = fps;
	chn_attr.stFrameRate.s32DstFrameRate = fps;
	chn_attr.u32Depth                    = depth;
	chn_attr.bMirror                     = mirror;
	chn_attr.bFlip                       = flip;
	chn_attr.stAspectRatio.enMode        = ASPECT_RATIO_MANUAL;
	chn_attr.stAspectRatio.stVideoRect.s32X       = 0;
	chn_attr.stAspectRatio.stVideoRect.s32Y       = 0;
	chn_attr.stAspectRatio.stVideoRect.u32Width   = width;
	chn_attr.stAspectRatio.stVideoRect.u32Height  = height;
	chn_attr.stAspectRatio.bEnableBgColor = CVI_TRUE;
	chn_attr.stAspectRatio.u32BgColor    = COLOR_RGB_BLACK;
	chn_attr.stNormalize.bEnable         = CVI_FALSE;

	s32Ret = CVI_VPSS_SetChnAttr(VpssGrp, VpssChn, &chn_attr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_SetChnAttr failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}

	s32Ret = CVI_VPSS_EnableChn(VpssGrp, VpssChn);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_EnableChn failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}

	return CVI_SUCCESS;
#endif
}

static CVI_S32 _mmf_vpss_chn_deinit(VPSS_GRP VpssGrp, VPSS_CHN VpssChn)
{
	return CVI_VPSS_DisableChn(VpssGrp, VpssChn);
}

static CVI_S32 _mmf_vpss_init_new(VPSS_GRP VpssGrp, CVI_U32 width, CVI_U32 height, PIXEL_FORMAT_E format)
{
	VPSS_GRP_ATTR_S    stVpssGrpAttr;
	CVI_S32 s32Ret = CVI_SUCCESS;

	memset(&stVpssGrpAttr, 0, sizeof(VPSS_GRP_ATTR_S));
	stVpssGrpAttr.stFrameRate.s32SrcFrameRate    = -1;
	stVpssGrpAttr.stFrameRate.s32DstFrameRate    = -1;
	stVpssGrpAttr.enPixelFormat                  = format;
	stVpssGrpAttr.u32MaxW                        = width;
	stVpssGrpAttr.u32MaxH                        = height;
	stVpssGrpAttr.u8VpssDev                      = 0;

	s32Ret = CVI_VPSS_CreateGrp(VpssGrp, &stVpssGrpAttr);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_CreateGrp(grp:%d) retry(%#x)!\n", VpssGrp, s32Ret);
		CVI_VPSS_DestroyGrp(VpssGrp);

		s32Ret = CVI_VPSS_CreateGrp(VpssGrp, &stVpssGrpAttr);
		if (s32Ret != CVI_SUCCESS) {
			SAMPLE_PRT("CVI_VPSS_CreateGrp(grp:%d) failed with %#x!\n", VpssGrp, s32Ret);
			return CVI_FAILURE;
		}
	}

	s32Ret = CVI_VPSS_ResetGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_ResetGrp(grp:%d) failed with %#x!%d\n", VpssGrp, s32Ret, CVI_ERR_VPSS_ILLEGAL_PARAM);
		return CVI_FAILURE;
	}

	s32Ret = CVI_VPSS_StartGrp(VpssGrp);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_StartGrp failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}
	return s32Ret;
}

int mmf_vi_init(void)
{
	if (priv.vi_is_inited) {
		return 0;
	}

	CVI_S32 s32Ret = CVI_SUCCESS;
	s32Ret = _mmf_vpss_init_new(0, priv.vi_size.u32Width, priv.vi_size.u32Height, PIXEL_FORMAT_UYVY);		// PIXEL_FORMAT_UYVY  PIXEL_FORMAT_NV21
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_init_new failed. s32Ret: 0x%x !\n", s32Ret);
		priv.vi_is_inited = false;
		return s32Ret;
	}

	priv.vi_is_inited = true;

	return s32Ret;
}

int mmf_vi_deinit(void)
{
	if (!priv.vi_is_inited) {
		return 0;
	}

	const int released = _mmf_release_all_vi_frames();
	if (released != CVI_SUCCESS) return released;
	CVI_S32 s32Ret = CVI_SUCCESS;
	s32Ret = _mmf_vpss_deinit_new(0);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_deinit_new failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}

	priv.vi_is_inited = false;

	return s32Ret;
}

static int _mmf_add_vi_channel(int ch, int width, int height, int format) {
 if (ch < 0 || ch >= MMF_VI_MAX_CHN) return CVI_FAILURE;
 // Do not add a producer while a previous channel still owns cleanup stages.
 for (int i = 0; i < MMF_VI_MAX_CHN; ++i)
  if (priv.vi_chn_stopping[i]) return CVI_ERR_VENC_BUSY;
	uint32_t pool_size_out = 0;
	int pool_id = -1;
#ifdef NANOKVM_ENHANCED
	// VI has one source channel. The index here selects a VPSS output, not VI.
	bool another_output = false;
	for (int i = 0; i < MMF_VI_MAX_CHN; ++i) another_output |= priv.vi_chn_is_inited[i];
#endif

	if (!priv.mmf_used_cnt) {
		printf("%s: maix multi-media or vi not inited\n", __func__);
		return -1;
	}

	if (!priv.vi_is_inited) {
		if (0 != mmf_vi_init()) {
			printf("mmf_vi_init failed!\r\n");
			return -1;
		}
	}

	if (width <= 0 || height <= 0) {
		printf("invalid width or height\n");
		return -1;
	}

	if (format != PIXEL_FORMAT_NV21
		&& format != PIXEL_FORMAT_RGB_888) {
		printf("invalid format\n");
		return -1;
	}

	// if ((format == PIXEL_FORMAT_RGB_888 && width * height * 3 > 640 * 640 * 3)
	// 	|| (format == PIXEL_FORMAT_RGB_888 && width * height * 3 / 2 > 2560 * 1440 * 3 / 2)) {
	// 	printf("camera size is too large, for NV21, maximum resolution 2560x1440, for RGB888, maximum resolution 640x640!\n");
	// 	return -1;
	// }

	if (priv.vi_chn_is_inited[ch]) {
		printf("vi ch %d already open\n", ch);
		return -1;
	}

	CVI_S32 s32Ret = CVI_SUCCESS;
	int fps = 60;
	int depth = 2;
	int width_out = ALIGN(width, DEFAULT_ALIGN);
	int height_out = height;
	PIXEL_FORMAT_E format_out = (PIXEL_FORMAT_E)format;
	bool mirror = !g_priv.vi_hmirror[ch];
	bool flip = !g_priv.vi_vflip[ch];
	s32Ret = _mmf_vpss_chn_deinit(0, ch);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_chn_deinit failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}

	s32Ret = _mmf_vpss_chn_init(0, ch, width_out, height_out, format_out, fps, depth, mirror, flip, 2);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("_mmf_vpss_chn_init failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}

 // Channel initialization succeeded: retain ownership even if binding,
 // pool creation or attachment later fails. The delete path owns rollback.
 priv.vi_chn_is_inited[ch] = true;
 priv.vi_chn_stopping[ch] = true;
 priv.vi_chn_disabled[ch] = false;
 priv.vi_chn_pool_id[ch] = VB_INVALID_POOLID;
#ifdef NANOKVM_ENHANCED
 s32Ret = another_output ? CVI_SUCCESS : SAMPLE_COMM_VI_Bind_VPSS(0, 0, 0);
 if (s32Ret == CVI_SUCCESS) priv.vi_source_bound[0] = true;
#else
 s32Ret = SAMPLE_COMM_VI_Bind_VPSS(0, ch, 0);
 if (s32Ret == CVI_SUCCESS) priv.vi_source_bound[ch] = true;
#endif
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("vi bind vpss failed. s32Ret: 0x%x !\n", s32Ret);
		goto _need_deinit_vpss_chn;
	}

	char name[20];
	snprintf(name, 20, "vi_vpss%.1d", ch);
	pool_size_out = COMMON_GetPicBufferSize(width_out, height_out, format_out, DATA_BITWIDTH_8, COMPRESS_MODE_NONE, DEFAULT_ALIGN);
	pool_id = _create_vb_pool(name, MMF_MOD_VI, pool_size_out, 2);
	if (pool_id < 0) {
		printf("[%s][%d]_create_vb_pool failed, id %d\n", __func__, __LINE__, pool_id);
		goto _need_deinit_vpss_chn;
	}

	priv.vi_chn_pool_id[ch] = pool_id;
	s32Ret = CVI_VPSS_AttachVbPool(0, ch, pool_id);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_VPSS_AttachVbPool failed. s32Ret: 0x%x !\n", s32Ret);
		goto _need_deinit_vpss_chn;
	}

	// VIDEO_FRAME_INFO_S frame;
	// if ((s32Ret = CVI_VPSS_GetChnFrame(0, ch, &frame, 3000)) != CVI_SUCCESS) {
	// 	SAMPLE_PRT("vi get frame failed: 0x%x !\n", s32Ret);
	// 	if ((s32Ret = SAMPLE_COMM_VI_UnBind_VPSS(0, ch, 0)) != CVI_SUCCESS) {
	// 		SAMPLE_PRT("vi unbind vpss failed. s32Ret: 0x%x !\n", s32Ret);
	// 	}
	// 	goto _need_deinit_vpss_chn;
	// }
	// CVI_VPSS_ReleaseChnFrame(0, ch, &frame);

 priv.vi_chn_stopping[ch] = false;
 return CVI_SUCCESS;
_need_deinit_vpss_chn:
 // Failed rollback remains visible through the channel owner and is retried
 // by full cleanup. Never make that slot reusable before all stages succeed.
 mmf_del_vi_channel(ch);
 return CVI_FAILURE;
}

int mmf_add_vi_channel(int ch, int width, int height, int format) {
	printf("mmf_add_vi_channel..\r\n");
	return _mmf_add_vi_channel(ch, width, height, format);
}

int mmf_del_vi_channel(int ch) {
 if (ch < 0 || ch >= MMF_VI_MAX_CHN) return CVI_FAILURE;
 if (!priv.vi_chn_is_inited[ch]) return CVI_SUCCESS;
 priv.vi_chn_stopping[ch] = true;
 int result = _mmf_release_vi_frame(ch);
 if (result != CVI_SUCCESS) return result;
#ifdef NANOKVM_ENHANCED
 bool another_output = false;
 for (int i = 0; i < MMF_VI_MAX_CHN; ++i)
  if (i != ch) another_output |= priv.vi_chn_is_inited[i];
 const int source = 0;
 const bool unbind = !another_output;
#else
 const int source = ch;
 const bool unbind = true;
#endif
 if (unbind && priv.vi_source_bound[source]) {
  result = SAMPLE_COMM_VI_UnBind_VPSS(0, source, 0);
  if (result != CVI_SUCCESS) return result;
  priv.vi_source_bound[source] = false;
 }
 if (!priv.vi_chn_disabled[ch]) {
  result = _mmf_vpss_chn_deinit(0, ch);
  if (result != CVI_SUCCESS) return result;
  priv.vi_chn_disabled[ch] = true;
 }
 result = _destroy_vb_pool(priv.vi_chn_pool_id[ch]);
 if (result != CVI_SUCCESS) return result;
 priv.vi_chn_pool_id[ch] = VB_INVALID_POOLID;
 priv.vi_chn_is_inited[ch] = false;
 priv.vi_chn_stopping[ch] = false;
 priv.vi_chn_disabled[ch] = false;
 return CVI_SUCCESS;
}

int mmf_del_vi_channel_all() {
	for (int i = 0; i < MMF_VI_MAX_CHN; i++) {
		if (priv.vi_chn_is_inited[i] == true) {
			const int result = mmf_del_vi_channel(i);
			if (result != CVI_SUCCESS) return result;
		}
	}
	return 0;
}

bool mmf_vi_chn_is_open(int ch) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		return false;
	}

	return priv.vi_chn_is_inited[ch] && !priv.vi_chn_stopping[ch];
}

int mmf_reset_vi_channel(int ch, int width, int height, int format)
{
	const int result = mmf_del_vi_channel(ch);
	if (result != CVI_SUCCESS) return result;
	return mmf_add_vi_channel(ch, width, height, format);
}

int mmf_vi_aligned_width(int ch) {
	UNUSED(ch);
	return DEFAULT_ALIGN;
}

int mmf_vi_frame_pop(int ch, void **data, int *len, int *width, int *height, int *format) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		printf("[%d] invalid ch %d\n", __LINE__, ch);
		return -1;
	}
	if (!priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]) {
        // printf("vi ch %d not open\n", ch);
        return -1;
    }
    if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
        printf("[%d] invalid ch %d\n", __LINE__, ch);
        return -1;
    }
    if (data == NULL || len == NULL || width == NULL || height == NULL || format == NULL) {
        printf("invalid param\n");
        return -1;
    }

	if (_mmf_acquire_vi_frame(ch, len, width, height, format) != 0) {
		return -1;
	}
	*data = _mmf_map_vi_frame(ch);
	if (*data == NULL) {
		_mmf_release_vi_frame(ch);
		return -1;
	}
	return 0;
}

int mmf_vi_frame_pop_nv21(int ch, mmf_nv21_view_t *view)
{
    if (!view || ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]) return -1;
    int len = 0, width = 0, height = 0, format = 0;
    if (_mmf_acquire_vi_frame(ch, &len, &width, &height, &format) != 0) return -1;
    const VIDEO_FRAME_S *frame = &priv.vi_frame[ch].stVFrame;
    // _mmf_map_vi_frame maps contiguous VB storage. Check that contract before
    // exposing its chroma plane, and retain the actual strides/plane offset.
    const bool valid = format == PIXEL_FORMAT_NV21 && width > 0 && height > 0
        && width % 2 == 0 && height % 2 == 0
        && frame->u32Stride[0] >= (CVI_U32)width && frame->u32Stride[1] >= (CVI_U32)width
        && (CVI_U64)frame->u32Stride[0] * height <= frame->u32Length[0]
        && (CVI_U64)frame->u32Stride[1] * (height / 2) <= frame->u32Length[1]
        && frame->u64PhyAddr[0] <= UINT64_MAX - frame->u32Length[0]
        && frame->u64PhyAddr[1] == frame->u64PhyAddr[0] + frame->u32Length[0]
        && frame->u32Length[2] == 0;
    if (!valid) {
        _mmf_release_vi_frame(ch);
        return -1;
    }
    uint8_t *data = (uint8_t *)_mmf_map_vi_frame(ch);
    if (!data) {
        _mmf_release_vi_frame(ch);
        return -1;
    }
    *view = {data, (uint32_t)len, (uint32_t)width, (uint32_t)height,
             frame->u32Stride[0], frame->u32Stride[1], frame->u32Length[0]};
    return 0;
}

int mmf_vi_frame_pop_native(int ch, int *len, int *width, int *height, int *format) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_chn_is_inited[ch] || priv.vi_chn_stopping[ch]) {
		return -1;
	}
	if (len == NULL || width == NULL || height == NULL || format == NULL) {
		printf("invalid param\n");
		return -1;
	}
	if (_mmf_acquire_vi_frame(ch, len, width, height, format) != 0) {
		return -1;
	}
	priv.vi_frame_deferred[ch] = true;
	return 0;
}

void mmf_vi_frame_free(int ch) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN || !priv.vi_frame_valid[ch]) {
		return;
	}
	priv.vi_frame_deferred[ch] = true;
}

void mmf_vi_frame_release(int ch) {
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		return;
	}
	_mmf_release_vi_frame(ch);
}

int mmf_region_frame_push(int ch, void *data, int len)
{
	CVI_S32 s32Ret;
	RGN_CANVAS_INFO_S stCanvasInfo;

	if (ch < 0 || ch >= MMF_RGN_MAX_NUM) {
		SAMPLE_PRT("Handle ch is illegal %d!\n", ch);
		return CVI_FAILURE;
	}

	if (!priv.rgn_is_init[ch]) {
		return 0;
	}

	if (!priv.rgn_is_bind[ch]) {
		return 0;
	}

	s32Ret = CVI_RGN_GetCanvasInfo(ch, &stCanvasInfo);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_RGN_GetCanvasInfo failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}

	if (stCanvasInfo.enPixelFormat == PIXEL_FORMAT_ARGB_8888) {
		if (!data || (CVI_U32)len != stCanvasInfo.stSize.u32Width * stCanvasInfo.stSize.u32Height * 4) {
			printf("Param is error!\r\n");
			return CVI_FAILURE;
		}
		memcpy(stCanvasInfo.pu8VirtAddr, data, len);
	} else {
		printf("Not support format!\r\n");
		return CVI_FAILURE;
	}

	s32Ret = CVI_RGN_UpdateCanvas(ch);
	if (s32Ret != CVI_SUCCESS) {
		SAMPLE_PRT("CVI_RGN_UpdateCanvas failed with %#x!\n", s32Ret);
		return CVI_FAILURE;
	}
	return s32Ret;
}

int mmf_enc_jpg_init(int ch, int w, int h, int format, int quality)
{
	if (priv.enc_jpg_is_init)
		return 0;

    if (ch < 0 || ch >= MMF_VENC_MAX_CHN || w <= 0 || h <= 0
        || w > UINT16_MAX || h > UINT16_MAX || quality <= 50 || quality > 100
        || (format != PIXEL_FORMAT_NV21 && format != PIXEL_FORMAT_RGB_888)
        || (format == PIXEL_FORMAT_NV21 && (w % 2 || h % 2))) {
        printf("Invalid JPEG channel, size, format or quality\n");
		return -1;
	}

	if (mmf_init()) {
		return -1;
	}

	// if ((format == PIXEL_FORMAT_RGB_888 && w * h * 3 > 640 * 640 * 3)
	// 	|| (format == PIXEL_FORMAT_RGB_888 && w * h * 3 / 2 > 2560 * 1440 * 3 / 2)) {
	// 	printf("image size is too large, for NV21, maximum resolution 2560x1440, for RGB888, maximum resolution 640x640!\n");
	// 	return -1;
	// }

	CVI_S32 s32Ret = CVI_SUCCESS;

	VENC_CHN_ATTR_S stVencChnAttr;
	memset(&stVencChnAttr, 0, sizeof(VENC_CHN_ATTR_S));
	stVencChnAttr.stVencAttr.enType = PT_JPEG;
	stVencChnAttr.stVencAttr.u32MaxPicWidth = w;
	stVencChnAttr.stVencAttr.u32MaxPicHeight = h;
	stVencChnAttr.stVencAttr.u32PicWidth = w;
	stVencChnAttr.stVencAttr.u32PicHeight = h;
	stVencChnAttr.stVencAttr.bEsBufQueueEn = CVI_FALSE;
	stVencChnAttr.stVencAttr.bIsoSendFrmEn = CVI_FALSE;
	stVencChnAttr.stVencAttr.bByFrame = 1;
	stVencChnAttr.stRcAttr.enRcMode = VENC_RC_MODE_MJPEGFIXQP;

	s32Ret = CVI_VENC_CreateChn(ch, &stVencChnAttr);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_CreateChn [%d] failed with %#x\n", ch, s32Ret);
		return s32Ret;
	}

	s32Ret = CVI_VENC_ResetChn(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_CreateChn [%d] failed with %#x\n", ch, s32Ret);
		return s32Ret;
	}

	s32Ret = CVI_VENC_DestroyChn(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_destroyChn [%d] failed with %#x\n", ch, s32Ret);
	}

	s32Ret = CVI_VENC_CreateChn(ch, &stVencChnAttr);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_CreateChn [%d] failed with %#x\n", ch, s32Ret);
		return s32Ret;
	}

	VENC_JPEG_PARAM_S stJpegParam;
	memset(&stJpegParam, 0, sizeof(VENC_JPEG_PARAM_S));
	s32Ret = CVI_VENC_GetJpegParam(ch, &stJpegParam);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_GetJpegParam failed with %#x\n", s32Ret);
		CVI_VENC_DestroyChn(ch);
		return s32Ret;
	}
	stJpegParam.u32Qfactor = quality;
	s32Ret = CVI_VENC_SetJpegParam(ch, &stJpegParam);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_SetJpegParam failed with %#x\n", s32Ret);
		CVI_VENC_DestroyChn(ch);
		return s32Ret;
	}

	switch (format) {
	case PIXEL_FORMAT_RGB_888:
		{
			s32Ret = _mmf_vpss_init(2, ch, (SIZE_S){(CVI_U32)w, (CVI_U32)h}, (SIZE_S){(CVI_U32)w, (CVI_U32)h}, PIXEL_FORMAT_RGB_888, PIXEL_FORMAT_YUV_PLANAR_420, 30, 0, CVI_FALSE, CVI_FALSE, 0);
			if (s32Ret != CVI_SUCCESS) {
				printf("VPSS init failed with %#x\n", s32Ret);
				CVI_VENC_StopRecvFrame(ch);
				CVI_VENC_DestroyChn(ch);
				return s32Ret;
			}

			s32Ret = SAMPLE_COMM_VPSS_Bind_VENC(2, ch, ch);
			if (s32Ret != CVI_SUCCESS) {
				printf("VPSS bind VENC failed with %#x\n", s32Ret);
				_mmf_vpss_deinit(2, ch);
				CVI_VENC_StopRecvFrame(ch);
				CVI_VENC_DestroyChn(ch);
				return s32Ret;
			}

			uint32_t input_size = 0, output_size = 0;
			input_size = COMMON_GetPicBufferSize(w, h, (PIXEL_FORMAT_E)format, DATA_BITWIDTH_8, COMPRESS_MODE_NONE, DEFAULT_ALIGN);
			output_size = COMMON_GetPicBufferSize(w, h, (PIXEL_FORMAT_E)PIXEL_FORMAT_YUV_PLANAR_420, DATA_BITWIDTH_8, COMPRESS_MODE_NONE, DEFAULT_ALIGN);
			int pool_id = _create_vb_pool((char *)"enc_jpeg_in", MMF_MOD_VENC, input_size, 1);
			if (pool_id < 0) {
				printf("[%s][%d]_create_vb_pool failed, id %d\n", __func__, __LINE__, pool_id);
				return -1;
			}
			priv.enc_jpg_input_pool_id = pool_id;

			pool_id = _create_vb_pool((char *)"enc_jpeg_out", MMF_MOD_VENC, output_size, 1);
			if (pool_id < 0) {
				printf("[%s][%d]_create_vb_pool failed, id %d\n", __func__, __LINE__, pool_id);
				return -1;
			}
			priv.enc_jpg_output_pool_id = pool_id;

			s32Ret = CVI_VPSS_AttachVbPool(2, ch, priv.enc_jpg_output_pool_id);
			if (s32Ret != CVI_SUCCESS) {
				SAMPLE_PRT("CVI_VPSS_AttachVbPool failed. s32Ret: 0x%x !\n", s32Ret);
				_destroy_vb_pool(priv.enc_jpg_output_pool_id);
				_destroy_vb_pool(priv.enc_jpg_input_pool_id);
				return CVI_FAILURE;
			}
		}
		break;
	case PIXEL_FORMAT_NV21:
		{
			// Native NV21 frames already own their VPSS buffer.
			priv.enc_jpg_input_pool_id = VB_INVALID_POOLID;
		}
		break;
	default:
		printf("unknown format!\n");
		CVI_VENC_StopRecvFrame(ch);
		CVI_VENC_DestroyChn(ch);
		return -1;
	}

	VENC_RECV_PIC_PARAM_S stRecvParam;
	stRecvParam.s32RecvPicNum = -1;
	s32Ret = CVI_VENC_StartRecvFrame(ch, &stRecvParam);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_StartRecvPic failed with %#x\n", s32Ret);
		return CVI_FAILURE;
	}

	if (priv.enc_jpg_frame) {
		_mmf_free_frame(priv.enc_jpg_frame);
		priv.enc_jpg_frame = NULL;
	}

	priv.enc_jpg_frame_w = w;
	priv.enc_jpg_frame_h = h;
	priv.enc_jpg_frame_fmt = format;
	priv.enc_jpg_quality = quality;
	priv.enc_jpg_is_init = 1;
	priv.enc_jpg_running = 0;

	return s32Ret;
}

int mmf_enc_jpg_deinit(int ch)
{
	if (!priv.enc_jpg_is_init)
		return 0;

	CVI_S32 s32Ret = CVI_SUCCESS;

	if (!mmf_enc_jpg_pop(ch, NULL, NULL)) {
		mmf_enc_jpg_free(ch);
	}

	switch (priv.enc_jpg_frame_fmt) {
	case PIXEL_FORMAT_RGB_888:
		s32Ret = SAMPLE_COMM_VPSS_UnBind_VENC(2, ch, ch);
		if (s32Ret != CVI_SUCCESS) {
			printf("VPSS unbind VENC failed with %d\n", s32Ret);
		}

		s32Ret = _mmf_vpss_deinit(2, ch);
		if (s32Ret != CVI_SUCCESS) {
			printf("VPSS deinit failed with %d\n", s32Ret);
		}
		break;
	case PIXEL_FORMAT_NV21:
		break;
	default:
		break;
	}

	s32Ret = CVI_VENC_StopRecvFrame(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_StopRecvPic failed with %d\n", s32Ret);
	}

	s32Ret = CVI_VENC_ResetChn(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_ResetChn vechn[%d] failed with %#x!\n",
				ch, s32Ret);
	}

	s32Ret = CVI_VENC_DestroyChn(ch);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_DestroyChn [%d] failed with %d\n", ch, s32Ret);
	}

	if (priv.enc_jpg_frame) {
		_mmf_free_frame(priv.enc_jpg_frame);
		priv.enc_jpg_frame = NULL;
	}

	switch (priv.enc_jpg_frame_fmt) {
	case PIXEL_FORMAT_RGB_888:
		_destroy_vb_pool(priv.enc_jpg_output_pool_id);
		priv.enc_jpg_output_pool_id = -1;
		_destroy_vb_pool(priv.enc_jpg_input_pool_id);
		priv.enc_jpg_input_pool_id = -1;
		break;
	case PIXEL_FORMAT_NV21:
		_destroy_vb_pool(priv.enc_jpg_input_pool_id);
		priv.enc_jpg_input_pool_id = -1;
		break;
	default:
		break;
	}

	if (mmf_deinit()) {
		return -1;
	}

	priv.enc_jpg_frame_w = 0;
	priv.enc_jpg_frame_h = 0;
	priv.enc_jpg_frame_fmt = 0;
	priv.enc_jpg_quality = -1;
	priv.enc_jpg_is_init = 0;
	priv.enc_jpg_running = 0;

	return s32Ret;
}

static int _mmf_jpg_push_copy(int ch, uint8_t *data, int w, int h, int format, int quality,
                              const VIDEO_FRAME_INFO_S *source)
{
    if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !data || w <= 0 || h <= 0
        || (format != PIXEL_FORMAT_NV21 && format != PIXEL_FORMAT_RGB_888 && format != PIXEL_FORMAT_UINT8_C1))
        return CVI_FAILURE;
    if (priv.enc_jpg_running) return CVI_FAILURE;
    const int encoder_format = format == PIXEL_FORMAT_UINT8_C1 ? PIXEL_FORMAT_NV21 : format;
    if (!priv.enc_jpg_is_init || priv.enc_jpg_frame_w != w || priv.enc_jpg_frame_h != h
        || priv.enc_jpg_frame_fmt != encoder_format || priv.enc_jpg_quality != quality) {
        const int stopped = mmf_enc_jpg_deinit(ch);
        if (stopped != CVI_SUCCESS) return stopped;
        const int started = mmf_enc_jpg_init(ch, w, h, encoder_format, quality);
        if (started != CVI_SUCCESS) return started;
    }
    if (!priv.enc_jpg_frame) {
        bool created = false;
        if (priv.enc_jpg_input_pool_id == (int)VB_INVALID_POOLID) {
            const uint32_t size = COMMON_GetPicBufferSize(w, h, (PIXEL_FORMAT_E)encoder_format,
                DATA_BITWIDTH_8, COMPRESS_MODE_NONE, DEFAULT_ALIGN);
            const int pool = _create_vb_pool((char *)"enc_jpeg_in", MMF_MOD_VENC, size, 1);
            if (pool < 0) return CVI_FAILURE;
            priv.enc_jpg_input_pool_id = pool;
            created = true;
        }
        priv.enc_jpg_frame = _mmf_alloc_frame(priv.enc_jpg_input_pool_id, {(CVI_U32)w, (CVI_U32)h},
                                             (PIXEL_FORMAT_E)encoder_format);
        if (!priv.enc_jpg_frame) {
            if (created) {
                _destroy_vb_pool(priv.enc_jpg_input_pool_id);
                priv.enc_jpg_input_pool_id = VB_INVALID_POOLID;
            }
            return CVI_FAILURE;
        }
    }
    const int copied = source
        ? nanokvm::media::copy_mapped_nv21_and_flush(priv.enc_jpg_frame, source, data)
        : nanokvm::media::copy_packed_and_flush(priv.enc_jpg_frame, data, w, h, (PIXEL_FORMAT_E)format);
    if (copied != CVI_SUCCESS) return copied;
    const int sent = format == PIXEL_FORMAT_RGB_888
        ? CVI_VPSS_SendFrame(2, priv.enc_jpg_frame, 1000)
        : CVI_VENC_SendFrame(ch, priv.enc_jpg_frame, 1000);
    if (sent == CVI_SUCCESS) priv.enc_jpg_running = 1;
    return sent;
}

int mmf_enc_jpg_push_with_quality(int ch, uint8_t *data, int w, int h, int format, int quality)
{
    return _mmf_jpg_push_copy(ch, data, w, h, format, quality, NULL);
}

static bool diagnostic_force_copy()
{
    const char *value = getenv("NANOKVM_DIAGNOSTIC_FORCE_COPY");
    return value && strcmp(value, "1") == 0;
}

int mmf_enc_jpg_push_vi_with_quality(int ch, int vi_ch, int quality)
{
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || vi_ch < 0 || vi_ch >= MMF_VI_MAX_CHN
		|| !priv.vi_frame_valid[vi_ch] || !priv.vi_frame_deferred[vi_ch]) {
		printf("Invalid native JPEG frame. venc:%d vi:%d\r\n", ch, vi_ch);
		return -1;
	}
    if (priv.enc_jpg_running) return CVI_FAILURE;

	VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[vi_ch];
	int width = frame->stVFrame.u32Width;
	int height = frame->stVFrame.u32Height;
	int format = frame->stVFrame.enPixelFormat;
	if (format != PIXEL_FORMAT_NV21) {
		printf("Unsupported native JPEG format:%d\r\n", format);
		return -1;
	}

	if (!priv.enc_jpg_is_init || priv.enc_jpg_frame_w != width
		|| priv.enc_jpg_frame_h != height || priv.enc_jpg_frame_fmt != format
		|| priv.enc_jpg_quality != quality) {
        const int stopped = mmf_enc_jpg_deinit(ch);
        if (stopped != CVI_SUCCESS) return stopped;
		int ret = mmf_enc_jpg_init(ch, width, height, format, quality);
		if (ret != CVI_SUCCESS) {
			return ret;
		}
	}

	CVI_S32 s32Ret = diagnostic_force_copy() ? CVI_FAILURE : CVI_VENC_SendFrame(ch, frame, 1000);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_SendFrame native JPEG failed with %#x, fallback to copy\n", s32Ret);
		uint8_t *data = (uint8_t *)_mmf_map_vi_frame(vi_ch);
		if (data == NULL) {
			return s32Ret;
		}
		return _mmf_jpg_push_copy(ch, data, width, height, format, quality, frame);
	}

	priv.enc_jpg_running = 1;
	return s32Ret;
}

int mmf_enc_jpg_push(int ch, uint8_t *data, int w, int h, int format)
{
    return mmf_enc_jpg_push_with_quality(ch, data, w, h, format, 80);
}

// Keep JPEG-specific storage beside the JPEG stream path. Besides making the
// ownership clearer, this avoids colliding with unrelated global VENC storage.
static VENC_PACK_S jpeg_pack_storage;

int mmf_enc_jpg_pop(int ch, uint8_t **data, int *size)
{
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN) {
		printf("Invalid JPEG channel:%d\r\n", ch);
		return -1;
	}
	if (!priv.enc_jpg_running) {
		return s32Ret;
	}

	memset(&jpeg_pack_storage, 0, sizeof(jpeg_pack_storage));
	priv.enc_jpeg_frame.pstPack = &jpeg_pack_storage;
	// JPEG produces one pack per frame. Go straight to the blocking call:
	// QueryStatus can still report zero immediately after SendFrame and turn
	// normal encoder latency into a spurious failure.
	s32Ret = CVI_VENC_GetStream(ch, &priv.enc_jpeg_frame, 1000);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_GetStream failed with %#x\n", s32Ret);
		priv.enc_jpeg_frame.pstPack = NULL;
		return s32Ret;
	}

	VENC_PACK_S *pack = priv.enc_jpeg_frame.pstPack;
	if (priv.enc_jpeg_frame.u32PackCount != 1 || pack->pu8Addr == NULL
		|| pack->u32Offset > pack->u32Len) {
		printf("Invalid JPEG stream pack\r\n");
		CVI_VENC_ReleaseStream(ch, &priv.enc_jpeg_frame);
		priv.enc_jpeg_frame.pstPack = NULL;
		priv.enc_jpg_running = 0;
		return -1;
	}

	if (data)
		*data = pack->pu8Addr + pack->u32Offset;
	if (size)
		*size = pack->u32Len - pack->u32Offset;

	return s32Ret;
}

int mmf_enc_jpg_free(int ch)
{
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (!priv.enc_jpg_running) {
		return s32Ret;
	}

	s32Ret = CVI_VENC_ReleaseStream(ch, &priv.enc_jpeg_frame);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_ReleaseStream failed with %#x\n", s32Ret);
		return s32Ret;
	}

	if (priv.enc_jpeg_frame.pstPack) {
		priv.enc_jpeg_frame.pstPack = NULL;
	}

	priv.enc_jpg_running = 0;
	return s32Ret;
}

int mmf_invert_format_to_mmf(int maix_format) {
	switch (maix_format) {
		case 0:
			return PIXEL_FORMAT_RGB_888;
		case 1:
			return PIXEL_FORMAT_BGR_888;
		case 3:
			return PIXEL_FORMAT_ARGB_8888;
		case 8:
			return PIXEL_FORMAT_NV21;
		case 12:
			return PIXEL_FORMAT_UINT8_C1;
		default:
			return -1;
	}
}

void mmf_set_vi_hmirror(int ch, bool en)
{
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		printf("invalid ch %d, must be [0, %d)\r\n", ch, MMF_VI_MAX_CHN);
		return;
	}

	g_priv.vi_hmirror[ch] = en;
}

void mmf_get_vi_hmirror(int ch, bool *en)
{
	if (en == NULL) return;
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		printf("invalid ch %d, must be [0, %d)\r\n", ch, MMF_VI_MAX_CHN);
		return;
	}

	*en = (bool)g_priv.vi_hmirror[ch];
}

void mmf_set_vi_vflip(int ch, bool en)
{
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		printf("invalid ch %d, must be [0, %d)\r\n", ch, MMF_VI_MAX_CHN);
		return;
	}

	g_priv.vi_vflip[ch] = en;
}

void mmf_get_vi_vflip(int ch, bool *en)
{
	if (en == NULL) return;
	if (ch < 0 || ch >= MMF_VI_MAX_CHN) {
		printf("invalid ch %d, must be [0, %d)\r\n", ch, MMF_VI_MAX_CHN);
		return;
	}

	*en = (bool)g_priv.vi_vflip[ch];
}

template <typename T>
static void _set_h26x_rate_timing(T &rate, const mmf_venc_cfg_t *cfg)
{
	rate.u32Gop = cfg->gop;
	rate.u32StatTime = 2;
	rate.u32SrcFrameRate = cfg->intput_fps;
	rate.fr32DstFrameRate = cfg->output_fps;
	rate.bVariFpsEn = CVI_FALSE;
}

static void _set_h26x_rc_attr(VENC_CHN_ATTR_S *attr, const mmf_venc_cfg_t *cfg)
{
	if (cfg->type == 2) {
		attr->stRcAttr.enRcMode = VENC_RC_MODE_H264CBR;
		_set_h26x_rate_timing(attr->stRcAttr.stH264Cbr, cfg);
		attr->stRcAttr.stH264Cbr.u32BitRate = cfg->bitrate;
	} else {
		attr->stRcAttr.enRcMode = VENC_RC_MODE_H265CBR;
		_set_h26x_rate_timing(attr->stRcAttr.stH265Cbr, cfg);
		attr->stRcAttr.stH265Cbr.u32BitRate = cfg->bitrate;
	}
}

template <typename T>
static void _set_h26x_rc_limits(T &param)
{
	param.u32MinIprop = 1;
	param.u32MaxIprop = 10;
	param.u32MaxQp = 51;
	param.u32MinQp = 20;
	param.u32MaxIQp = 51;
	param.u32MinIQp = 20;
	param.bQpMapEn = CVI_FALSE;
}

static void _set_h26x_cbr_limits(VENC_RC_PARAM_S *param, const mmf_venc_cfg_t *cfg)
{
	if (cfg->type == 2) {
		_set_h26x_rc_limits(param->stParamH264Cbr);
	} else {
		_set_h26x_rc_limits(param->stParamH265Cbr);
	}
}

static int _venc_init_failed(int ch, const char *stage, CVI_S32 error)
{
	fprintf(stderr, "[kvm_mmf] %s failed on VENC channel %d with %#x\n", stage, ch, error);
	fflush(stderr);
	int cleanup = mmf_del_venc_channel(ch);
	if (cleanup != 0) {
		fprintf(stderr, "[kvm_mmf] failed to unwind VENC channel %d after %s: %#x\n",
			ch, stage, cleanup);
		fflush(stderr);
	}
	return error == CVI_SUCCESS ? CVI_FAILURE : error;
}

int mmf_add_venc_channel(int ch, mmf_venc_cfg_t *cfg) {
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || priv.venc[ch].is_used) {
		fprintf(stderr, "[kvm_mmf] invalid or busy VENC channel %d\n", ch);
		fflush(stderr);
		return -1;
	}
	if (cfg->type != 1 && cfg->type != 2) {
		fprintf(stderr, "[kvm_mmf] unsupported VENC codec type=%d\n", cfg->type);
		fflush(stderr);
		return -1;
	}


	VENC_CHN_ATTR_S stVencChnAttr;
	memset(&stVencChnAttr, 0, sizeof(VENC_CHN_ATTR_S));
	stVencChnAttr.stVencAttr.enType = cfg->type == 1 ? PT_H265 : PT_H264;
	stVencChnAttr.stVencAttr.u32MaxPicWidth = cfg->w;
	stVencChnAttr.stVencAttr.u32MaxPicHeight = cfg->h;
	stVencChnAttr.stVencAttr.u32BufSize = 1024 * 1024;	// 1024Kb
	stVencChnAttr.stVencAttr.bByFrame = CVI_TRUE;
	stVencChnAttr.stVencAttr.u32PicWidth = cfg->w;
	stVencChnAttr.stVencAttr.u32PicHeight = cfg->h;
	stVencChnAttr.stVencAttr.bEsBufQueueEn = CVI_TRUE;
	stVencChnAttr.stVencAttr.bIsoSendFrmEn = CVI_TRUE;
	stVencChnAttr.stGopAttr.enGopMode = VENC_GOPMODE_NORMALP;
	stVencChnAttr.stGopAttr.stNormalP.s32IPQpDelta = 2;
	_set_h26x_rc_attr(&stVencChnAttr, cfg);
	s32Ret = CVI_VENC_CreateChn(ch, &stVencChnAttr);
	if (s32Ret != CVI_SUCCESS) {
		fprintf(stderr,
			"[kvm_mmf] CVI_VENC_CreateChn failed: channel=%d codec=%d "
			"size=%ux%u bitrate=%u gop=%u error=%#x\n",
			ch, cfg->type, cfg->w, cfg->h, cfg->bitrate, cfg->gop, s32Ret);
		fflush(stderr);
		return s32Ret;
	}

	// From this point on, make every failed initialization visible to teardown.
	// Otherwise a successfully created vendor channel can be orphaned while the
	// local bookkeeping still says that the channel is free.
	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	memset(info, 0, sizeof(venc_info_t));
	info->ch = ch;
	info->type = cfg->type;
	info->pool_id = VB_INVALID_POOLID;
	priv.venc_stream_acquired[ch] = false;
	priv.venc_input_vi_ch[ch] = -1;
	memcpy(&info->cfg, cfg, sizeof(mmf_venc_cfg_t));
	info->is_used = 1;
	info->is_inited = 1;
	priv.h265_or_h264_is_used = 1;

	VENC_RECV_PIC_PARAM_S stRecvParam = {0};
	stRecvParam.s32RecvPicNum = -1;
	s32Ret = CVI_VENC_StartRecvFrame(ch, &stRecvParam);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_StartRecvFrame", s32Ret);
	}

	VENC_RC_PARAM_S stRcParam;
	s32Ret = CVI_VENC_GetRcParam(ch, &stRcParam);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_GetRcParam", s32Ret);
	}
	stRcParam.s32FirstFrameStartQp = 35;
	stRcParam.s32InitialDelay = 1000;
	_set_h26x_cbr_limits(&stRcParam, cfg);
	s32Ret = CVI_VENC_SetRcParam(ch, &stRcParam);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_SetRcParam", s32Ret);
	}

	VENC_FRAMELOST_S stFL;
	s32Ret = CVI_VENC_GetFrameLostStrategy(ch, &stFL);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_GetFrameLostStrategy", s32Ret);
	}
	fprintf(stderr,
		"[kvm_mmf] VENC channel %d frame-loss defaults: codec=%d open=%d threshold=%u mode=%d gap=%u\n",
		ch, cfg->type, stFL.bFrmLostOpen, stFL.u32FrmLostBpsThr,
		stFL.enFrmLostMode, stFL.u32EncFrmGaps);
	fflush(stderr);
	/* A KVM stream must preserve its reference chain. The generic vendor
	 * frame-loss strategy can discard a reference frame when instantaneous
	 * bitrate spikes, after which Direct/WebCodecs cannot decode later deltas. */
	stFL.bFrmLostOpen = CVI_FALSE;
	stFL.enFrmLostMode = FRMLOST_NORMAL;
	stFL.u32EncFrmGaps = 0;
	s32Ret = CVI_VENC_SetFrameLostStrategy(ch, &stFL);
	if (s32Ret != CVI_SUCCESS) {
		return _venc_init_failed(ch, "CVI_VENC_SetFrameLostStrategy", s32Ret);
	}

	// Native VPSS leases can go straight to VENC. Allocate copy storage only
	// when a caller actually needs that path; idle reserves can be trimmed
	// on a codec change, and teardown releases any remaining allocation.
	fprintf(stderr,
		"[kvm_mmf] VENC channel %d initialized: codec=%d rc=cbr size=%ux%u bitrate=%u gop=%u\n",
		ch, cfg->type, cfg->w, cfg->h, cfg->bitrate, cfg->gop);
	fflush(stderr);

	return 0;
}

int mmf_del_venc_channel(int ch) {
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN) {
		return -1;
	}
	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	if (!info->is_inited) {
		return 0;
	}
	fprintf(stderr, "[kvm_mmf] tearing down VENC channel %d: codec=%d rc=cbr running=%d\n",
		ch, info->cfg.type, info->is_running);
	fflush(stderr);

	if (info->is_running || priv.venc_stream_acquired[ch]) {
		mmf_stream_t stream = {};
		if (!priv.venc_stream_acquired[ch]) {
			const int drained = mmf_venc_pop(ch, &stream);
			if (drained != CVI_SUCCESS) return drained;
		}
		const int released = mmf_venc_free(ch);
		if (released != CVI_SUCCESS) return released;
	}

	CVI_S32 s32Ret = CVI_SUCCESS;
	s32Ret = CVI_VENC_StopRecvFrame(ch);
	if (s32Ret != CVI_SUCCESS) {
		fprintf(stderr, "[kvm_mmf] CVI_VENC_StopRecvFrame(%d) failed with %#x\n", ch, s32Ret);
		fflush(stderr);
	}

	CVI_S32 destroyRet = CVI_FAILURE;
	for (int attempt = 0; attempt < 3; attempt++) {
		if (attempt != 0) {
			usleep(5000 * attempt);
		}
		s32Ret = CVI_VENC_ResetChn(ch);
		if (s32Ret != CVI_SUCCESS) {
			fprintf(stderr, "[kvm_mmf] CVI_VENC_ResetChn(%d) attempt %d failed with %#x\n",
				ch, attempt + 1, s32Ret);
			fflush(stderr);
		}
		destroyRet = CVI_VENC_DestroyChn(ch);
		if (destroyRet == CVI_SUCCESS) {
			break;
		}
		fprintf(stderr, "[kvm_mmf] CVI_VENC_DestroyChn(%d) attempt %d failed with %#x\n",
			ch, attempt + 1, destroyRet);
		fflush(stderr);
	}
	if (destroyRet != CVI_SUCCESS) {
		// Preserve the bookkeeping so the next frame can retry teardown.  In
		// particular, never claim that a still-existing vendor channel is free.
		return destroyRet;
	}

	if (info->capture_frame) {
		_mmf_free_frame(info->capture_frame);
		info->capture_frame = NULL;
	}
	/*
	 * Do not release deferred VI frames here. Profile reconfiguration happens
	 * after CameraCviMmf::read() has leased the next zero-copy frame, and that
	 * exact frame must remain valid until it has been sent to the replacement
	 * encoder. An outstanding frame owned by the old encoder is released by
	 * mmf_venc_free() above when info->is_running is true.
	 */
	if (info->pool_id != VB_INVALID_POOLID && info->pool_id < VB_MAX_COMM_POOLS) {
		_destroy_vb_pool(info->pool_id);
	}
	free(venc_pack_storage[ch]);
	venc_pack_storage[ch] = NULL;
	venc_pack_capacity[ch] = 0;

	if (info->type == 2 || info->type == 1) {
		priv.h265_or_h264_is_used = 0;
	}
	memset(info, 0, sizeof(venc_info_t));
	priv.venc_stream_acquired[ch] = false;
	priv.venc_input_vi_ch[ch] = -1;
	fprintf(stderr, "[kvm_mmf] VENC channel %d destroyed\n", ch);
	fflush(stderr);

	return 0;
}

int mmf_del_venc_channel_all() {
	for (int i = 0; i < MMF_VENC_MAX_CHN; i ++) {
		const int result = mmf_del_venc_channel(i);
		if (result != CVI_SUCCESS) return result;
	}
	return 0;
}

static int _mmf_venc_push_copy(int ch, uint8_t *data, int w, int h, int format,
                               const VIDEO_FRAME_INFO_S *source = NULL) {
    if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !data || !priv.venc[ch].is_inited) return CVI_FAILURE;
    venc_info_t *info = &priv.venc[ch];
    if (info->is_running || w != info->cfg.w || h != info->cfg.h || format != info->cfg.fmt)
        return CVI_FAILURE;
    if (!info->capture_frame) {
        char name[20];
        snprintf(name, sizeof(name), "venc%.1d", ch);
        const uint32_t size = VDEC_GetPicBufferSize((PAYLOAD_TYPE_E)info->cfg.type,
            w, h, (PIXEL_FORMAT_E)format, DATA_BITWIDTH_8, COMPRESS_MODE_NONE);
        const int pool = _create_vb_pool(name, MMF_MOD_VENC, size, 1);
        if (pool < 0) return CVI_FAILURE;
        VIDEO_FRAME_INFO_S *frame = _mmf_alloc_frame(pool,
            (SIZE_S){(CVI_U32)w, (CVI_U32)h}, (PIXEL_FORMAT_E)format);
        if (!frame) {
            _destroy_vb_pool(pool);
            return CVI_FAILURE;
        }
        // Publish ownership only after both allocations succeeded. A failed
        // attempt must not orphan a pool or prevent a later native send.
        info->pool_id = pool;
        info->capture_frame = frame;
        fprintf(stderr, "[kvm_mmf] VENC channel %d allocated copy buffer: %u bytes\n", ch, size);
    }
    const int copied = source
        ? nanokvm::media::copy_mapped_nv21_and_flush(info->capture_frame, source, data)
        : nanokvm::media::copy_packed_and_flush(info->capture_frame, data, w, h, (PIXEL_FORMAT_E)format);
    if (copied != CVI_SUCCESS) return copied;
    const int sent = CVI_VENC_SendFrame(ch, info->capture_frame, 1000);
    if (sent == CVI_SUCCESS) info->is_running = 1;
    return sent;
}

int mmf_venc_push(int ch, uint8_t *data, int w, int h, int format) {
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || data == NULL
		|| (format != PIXEL_FORMAT_NV21 && format != PIXEL_FORMAT_RGB_888)) {
		printf("Invalid param. ch:%d data:%p format:%d\r\n", ch, data, format);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
    if (!info->is_inited || info->is_running) return CVI_FAILURE;
	if (info->type == 1 || info->type == 2) {
		VIDEO_FRAME_INFO_S *vi_frame = NULL;
        const int vi_ch = _mmf_find_deferred_vi_frame(w, h, format, data, &vi_frame);
        if (vi_ch >= 0) {
            const int sent = CVI_VENC_SendFrame(ch, vi_frame, 1000);
            if (sent == CVI_SUCCESS) {
                info->is_running = 1;
                priv.venc_input_vi_ch[ch] = vi_ch;
                return 0;
            }
            const int copied = _mmf_venc_push_copy(ch, data, w, h, format, vi_frame);
            if (copied == CVI_SUCCESS) priv.venc_input_vi_ch[ch] = vi_ch;
            return copied;
        }
	}

	return _mmf_venc_push_copy(ch, data, w, h, format);
}

int mmf_trim_idle_copy_buffers(void) {
    // Called under the capture owner's serialization when changing codec.
    // Keep encoders alive for mixed clients, but not unused copy reserves.
    for (int ch = 0; ch < MMF_VENC_MAX_CHN; ++ch) {
        venc_info_t *info = &priv.venc[ch];
        if (!info->is_inited || info->is_running || priv.venc_stream_acquired[ch]) continue;
        if (info->capture_frame) {
            const int released = _mmf_free_frame(info->capture_frame);
            info->capture_frame = NULL;
            if (released != CVI_SUCCESS) return released;
        }
        if (info->pool_id != VB_INVALID_POOLID) {
            const int released = _destroy_vb_pool(info->pool_id);
            if (released != CVI_SUCCESS) return released;
            info->pool_id = VB_INVALID_POOLID;
        }
    }
    if (priv.enc_jpg_is_init && !priv.enc_jpg_running
        && priv.enc_jpg_frame_fmt == PIXEL_FORMAT_NV21) {
        if (priv.enc_jpg_frame) {
            const int released = _mmf_free_frame(priv.enc_jpg_frame);
            priv.enc_jpg_frame = NULL;
            if (released != CVI_SUCCESS) return released;
        }
        const int released = _destroy_vb_pool(priv.enc_jpg_input_pool_id);
        if (released != CVI_SUCCESS) return released;
        priv.enc_jpg_input_pool_id = VB_INVALID_POOLID;
    }
    return CVI_SUCCESS;
}

int mmf_venc_push_vi(int ch, int vi_ch) {
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || vi_ch < 0 || vi_ch >= MMF_VI_MAX_CHN
		|| !priv.vi_frame_valid[vi_ch] || !priv.vi_frame_deferred[vi_ch]) {
		printf("Invalid native frame. venc:%d vi:%d\r\n", ch, vi_ch);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
    if (!info->is_inited || info->is_running) return CVI_FAILURE;
	VIDEO_FRAME_INFO_S *frame = &priv.vi_frame[vi_ch];
	if ((info->type != 1 && info->type != 2) ||
		frame->stVFrame.enPixelFormat != PIXEL_FORMAT_NV21) {
		printf("Unsupported native frame. venc_type:%d format:%d\r\n",
			info->type, frame->stVFrame.enPixelFormat);
		return -1;
	}

	CVI_S32 ret = diagnostic_force_copy() ? CVI_FAILURE : CVI_VENC_SendFrame(ch, frame, 1000);
	if (ret == CVI_SUCCESS) {
		info->is_running = 1;
		priv.venc_input_vi_ch[ch] = vi_ch;
		return 0;
	}

	printf("CVI_VENC_SendFrame native failed with %#x, fallback to mapped copy\n", ret);
	uint8_t *data = (uint8_t *)_mmf_map_vi_frame(vi_ch);
	if (data == NULL) {
		return -1;
	}
    const int copied = _mmf_venc_push_copy(ch, data, frame->stVFrame.u32Width,
        frame->stVFrame.u32Height, frame->stVFrame.enPixelFormat, frame);
    if (copied == CVI_SUCCESS) priv.venc_input_vi_ch[ch] = vi_ch;
    return copied;
}

int mmf_venc_pop(int ch, mmf_stream_t *stream) {
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !priv.venc[ch].is_inited) {
		printf("Invalid venc ch:%d\r\n", ch);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	VENC_STREAM_S *venc_stream = (VENC_STREAM_S *)&priv.venc[ch].capture_stream;
	if (!info->is_running) {
		/* No frame was submitted, so reporting success would expose an empty
		 * stream to kvm_vision and turn an ordinary profile transition into the
		 * misleading IMG_VENC_ERROR (-2). */
		return -1;
	}
	if (priv.venc_stream_acquired[ch]) {
		fprintf(stderr, "[kvm_mmf] VENC channel %d stream was not released before next pop\n", ch);
		fflush(stderr);
		return -1;
	}
	// One frame was already submitted. GetStream waits for that frame itself;
	// an extra 80 ms select deadline can abandon it while the codec lock is
	// still held. StopRecvFrame/ResetChn do not cancel H.26x encoding in this SDK.

	VENC_CHN_STATUS_S stStatus;
	s32Ret = CVI_VENC_QueryStatus(ch, &stStatus);
	if (s32Ret != CVI_SUCCESS) {
		printf("CVI_VENC_QueryStatus failed with %#x\n", s32Ret);
		return s32Ret;
	}

	/*
	 * QueryStatus reports how many VENC_PACK_S entries GetStream may fill.
	 * Allocating a fixed eight entries and checking u32PackCount afterwards is
	 * too late: a vendor frame with more packs has already overrun the heap.
	 */
	if (stStatus.u32CurPacks == 0 || stStatus.u32CurPacks > 64) {
		fprintf(stderr, "[kvm_mmf] invalid VENC channel %d queried pack count: %u\n",
			ch, stStatus.u32CurPacks);
		fflush(stderr);
		return -1;
	}
	if (venc_pack_capacity[ch] < stStatus.u32CurPacks) {
		VENC_PACK_S *storage = (VENC_PACK_S *)realloc(venc_pack_storage[ch],
			stStatus.u32CurPacks * sizeof(VENC_PACK_S));
		if (storage == NULL) {
			printf("Realloc failed!\r\n");
			return -1;
		}
		venc_pack_storage[ch] = storage;
		venc_pack_capacity[ch] = stStatus.u32CurPacks;
	}
	memset(venc_pack_storage[ch], 0, stStatus.u32CurPacks * sizeof(VENC_PACK_S));
	venc_stream->pstPack = venc_pack_storage[ch];
	if (!venc_stream->pstPack) {
		printf("Malloc failed!\r\n");
		return -1;
	}

	if (stStatus.u32CurPacks > 0) {
		unsigned pending_waits = 0;
		do {
			s32Ret = CVI_VENC_GetStream(ch, venc_stream, 1000);
			if (s32Ret == CVI_ERR_VENC_BUSY && (++pending_waits == 1 || pending_waits % 5 == 0)) {
				fprintf(stderr, "[kvm_mmf] VENC channel %d frame still pending after %u waits; retaining input\n", ch, pending_waits);
			}
			if (s32Ret == CVI_ERR_VENC_BUSY) usleep(1000);
		} while (s32Ret == CVI_ERR_VENC_BUSY);
		if (s32Ret != CVI_SUCCESS) {
			printf("CVI_VENC_GetStream failed with %#x\n", s32Ret);
			return s32Ret;
		}
		priv.venc_stream_acquired[ch] = true;
	} else {
		printf("CVI_VENC_QueryStatus find not pack\r\n");
		return -1;
	}

	if (stream) {
		stream->count = venc_stream->u32PackCount;
		if (stream->count > 8) {
			fprintf(stderr, "[kvm_mmf] VENC channel %d returned too many packs: queried=%u returned=%d\n",
				ch, stStatus.u32CurPacks, stream->count);
			fflush(stderr);
			const int released = mmf_venc_free(ch);
			if (released != CVI_SUCCESS) return released;
			return -1;
		}
		for (int i = 0; i < stream->count; i++) {
			VENC_PACK_S *pack = &venc_stream->pstPack[i];
			stream->data[i] = pack->pu8Addr == NULL ? NULL : pack->pu8Addr + pack->u32Offset;
			stream->data_size[i] = pack->u32Offset > pack->u32Len ? -1 :
				(int)(pack->u32Len - pack->u32Offset);
			if (stream->data[i] == NULL || stream->data_size[i] <= 0) {
				static unsigned int invalid_pack_count = 0;
				invalid_pack_count++;
				if (invalid_pack_count <= 16 ||
					(invalid_pack_count & (invalid_pack_count - 1)) == 0) {
					fprintf(stderr,
						"[kvm_mmf] invalid VENC pack #%u: ch=%d pack=%d/%d queried=%u addr=%p len=%u offset=%u data_num=%u frame_end=%d\n",
						invalid_pack_count, ch, i, stream->count, stStatus.u32CurPacks,
						pack->pu8Addr, pack->u32Len, pack->u32Offset,
						pack->u32DataNum, pack->bFrameEnd);
					fflush(stderr);
				}
			}
		}
	}

	return 0;
}

int mmf_venc_free(int ch) {
	CVI_S32 s32Ret = CVI_SUCCESS;
	if (ch < 0 || ch >= MMF_VENC_MAX_CHN || !priv.venc[ch].is_inited) {
		printf("Invalid venc ch:%d\r\n", ch);
		return -1;
	}

	venc_info_t *info = (venc_info_t *)&priv.venc[ch];
	VENC_STREAM_S *venc_stream = (VENC_STREAM_S *)&priv.venc[ch].capture_stream;
	if (!info->is_running && !priv.venc_stream_acquired[ch]) {
		// Encoder reinitialization happens after CameraCviMmf has already
		// acquired the next VI frame.  With no submitted VENC frame there is
		// nothing to release here; releasing all VI frames would invalidate
		// the Image that is about to be sent to the new encoder.
		return s32Ret;
	}
	if (!priv.venc_stream_acquired[ch]) {
		// A wait error is not a completed frame. Keep both the pending state and
		// VI lease so deletion must drain it before closing the codec channel.
		return CVI_ERR_VENC_BUSY;
	}

	if (priv.venc_stream_acquired[ch]) {
		s32Ret = CVI_VENC_ReleaseStream(ch, venc_stream);
		if (s32Ret != CVI_SUCCESS) {
			printf("CVI_VENC_ReleaseStream failed with %#x\n", s32Ret);
		}
		if (s32Ret != CVI_SUCCESS) return s32Ret;
		priv.venc_stream_acquired[ch] = false;
	}

	info->is_running = 0;
	if (priv.venc_input_vi_ch[ch] >= 0) {
		_mmf_release_vi_frame(priv.venc_input_vi_ch[ch]);
		priv.venc_input_vi_ch[ch] = -1;
	}
	return s32Ret;
}
