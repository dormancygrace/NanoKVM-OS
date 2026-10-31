// Host test of the real tiled MJPEG change map and region JPEG channels
// (kvm_mmf): the IVE is a CPU implementation of SAD (16x16 blocks) and DMA,
// the encoder a fault-injected stub. No hardware is opened.
#include "../../support/additional/kvm_mmf/src/kvm_mmf.cpp"
#include <atomic>
#include <cassert>
#include <thread>
#include <cstdlib>
#include <vector>

// --- IVE on the CPU; physical addresses are host pointers.
static uint8_t *at(CVI_U64 address) { return reinterpret_cast<uint8_t *>(static_cast<uintptr_t>(address)); }
static int ive_handle;
IVE_HANDLE CVI_IVE_CreateHandle() { return &ive_handle; }
CVI_S32 CVI_IVE_DestroyHandle(IVE_HANDLE) { return 0; }
CVI_S32 CVI_IVE_CreateImage(IVE_HANDLE, IVE_IMAGE_S *image, IVE_IMAGE_TYPE_E type, uint16_t width, uint16_t height)
{
    const int bytes = type == IVE_IMAGE_TYPE_U16C1 ? 2 : 1, stride = (width + 15) / 16 * 16;
    *image = {};
    image->enType = type;
    image->u32Width = width;
    image->u32Height = height;
    image->u32Stride[0] = stride;
    void *memory = calloc(static_cast<size_t>(stride) * height * bytes + 64, 1);
    image->u64PhyAddr[0] = image->u64VirAddr[0] = reinterpret_cast<uintptr_t>(memory);
    return memory ? 0 : -1;
}
CVI_S32 CVI_SYS_FreeI(IVE_HANDLE, IVE_IMAGE_S *image) { free(at(image->u64PhyAddr[0])); return 0; }
CVI_S32 CVI_IVE_BufRequest(IVE_HANDLE, IVE_IMAGE_S *) { return 0; }
CVI_S32 CVI_IVE_SAD(IVE_HANDLE, IVE_SRC_IMAGE_S *a, IVE_SRC_IMAGE_S *b, IVE_DST_IMAGE_S *, IVE_DST_IMAGE_S *thr,
                    IVE_SAD_CTRL_S *ctrl, CVI_BOOL)
{
    assert(ctrl->enMode == IVE_SAD_MODE_MB_16X16 && a->u32Width == b->u32Width && a->u32Height == b->u32Height);
    assert(a->u32Height % 16 == 0 && a->u32Width % 16 == 0);
    for (CVI_U32 by = 0; by < a->u32Height / 16; ++by)
        for (CVI_U32 bx = 0; bx < a->u32Width / 16; ++bx) {
            unsigned sum = 0;
            for (int y = 0; y < 16; ++y)
                for (int x = 0; x < 16; ++x) {
                    const int p = at(a->u64PhyAddr[0])[(by * 16 + y) * a->u32Stride[0] + bx * 16 + x];
                    const int q = at(b->u64PhyAddr[0])[(by * 16 + y) * b->u32Stride[0] + bx * 16 + x];
                    sum += p > q ? p - q : q - p;
                }
            at(thr->u64PhyAddr[0])[by * thr->u32Stride[0] + bx] = sum >= ctrl->u16Thr ? ctrl->u8MaxVal : ctrl->u8MinVal;
        }
    return 0;
}
CVI_S32 CVI_IVE_DMA(IVE_HANDLE, IVE_DATA_S *from, IVE_DST_DATA_S *to, IVE_DMA_CTRL_S *ctrl, CVI_BOOL)
{
    assert(ctrl->enMode == IVE_DMA_MODE_DIRECT_COPY && from->u32Width == to->u32Width && from->u32Height == to->u32Height);
    for (CVI_U32 y = 0; y < from->u32Height; ++y)
        memcpy(at(to->u64PhyAddr) + y * to->u32Stride, at(from->u64PhyAddr) + y * from->u32Stride, from->u32Width);
    return 0;
}
// The IVE driver with osdrv patch 0020.
extern "C" FILE *__real_fopen(const char *, const char *);
extern "C" FILE *__wrap_fopen(const char *path, const char *mode)
{
    if (!strcmp(path, "/sys/module/cv181x_ive/parameters/timeout_ms")) return fmemopen((void *)"100\n", 4, "r");
    return __real_fopen(path, mode);
}

// --- Encoder with injected faults.
static int get_stream_error, destroy_error, destroys, sends, released_frames;
static int get_param_error, set_param_error, start_error, release_error;
static bool allocated[MMF_VENC_MAX_CHN];
static uint8_t jpeg[] = {0xff, 0xd8, 0x01, 0xff, 0xd9};
CVI_S32 CVI_VENC_CreateChn(VENC_CHN ch, const VENC_CHN_ATTR_S *) {
    assert(ch == 2 || ch == 3);
    if (allocated[ch]) return CVI_ERR_VENC_EXIST;
    allocated[ch] = true;
    return 0;
}
CVI_S32 CVI_VENC_GetJpegParam(VENC_CHN, VENC_JPEG_PARAM_S *param) { *param = {}; return get_param_error; }
CVI_S32 CVI_VENC_SetJpegParam(VENC_CHN, const VENC_JPEG_PARAM_S *) { return set_param_error; }
CVI_S32 CVI_VENC_StartRecvFrame(VENC_CHN, const VENC_RECV_PIC_PARAM_S *) { return start_error; }
CVI_S32 CVI_VENC_SendFrame(VENC_CHN, const VIDEO_FRAME_INFO_S *, CVI_S32) { ++sends; return 0; }
CVI_S32 CVI_VENC_GetStream(VENC_CHN, VENC_STREAM_S *stream, CVI_S32)
{
    if (get_stream_error) return get_stream_error;
    stream->u32PackCount = 1;
    stream->pstPack->pu8Addr = jpeg;
    stream->pstPack->u32Len = sizeof(jpeg);
    stream->pstPack->u32Offset = 0;
    return 0;
}
CVI_S32 CVI_VENC_ReleaseStream(VENC_CHN, VENC_STREAM_S *) { return release_error; }
CVI_S32 CVI_VENC_StopRecvFrame(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_ResetChn(VENC_CHN) { return 0; }
CVI_S32 CVI_VENC_DestroyChn(VENC_CHN ch) {
    ++destroys;
    if (!destroy_error) allocated[ch] = false;
    return destroy_error;
}
CVI_S32 CVI_VPSS_ReleaseChnFrame(VPSS_GRP, VPSS_CHN, const VIDEO_FRAME_INFO_S *) { ++released_frames; return 0; }
CVI_S32 CVI_VI_ReleaseChnFrame(VI_PIPE, VI_CHN, const VIDEO_FRAME_INFO_S *) { ++released_frames; return 0; }
// Linked by the frame release; nothing is mapped and no pool is reclaimed here.
CVI_S32 CVI_SYS_Munmap(void *, CVI_U32) { abort(); }
CVI_S32 CVI_VB_DestroyPool(VB_POOL) { abort(); }
CVI_S32 CVI_VPSS_DetachVbPool(VPSS_GRP, VPSS_CHN) { abort(); }

// --- A frame on VI channel 0.
static const int W = 256, H = 136;
static std::vector<uint8_t> luma, chroma;
static int chroma_rows;
static void frame(int format)
{
    chroma_rows = format == PIXEL_FORMAT_NV21 ? H / 2 : H;
    luma.assign(static_cast<size_t>(W) * H, 100);
    chroma.assign(static_cast<size_t>(W) * chroma_rows, 128);
    VIDEO_FRAME_S &v = priv.vi_frame[0].stVFrame;
    v = {};
    v.enPixelFormat = static_cast<PIXEL_FORMAT_E>(format);
    v.u32Width = W;
    v.u32Height = H;
    v.u32Stride[0] = v.u32Stride[1] = W;
    v.u64PhyAddr[0] = reinterpret_cast<uintptr_t>(luma.data());
    v.u64PhyAddr[1] = reinterpret_cast<uintptr_t>(chroma.data());
    priv.vi_frame_valid[0] = true;
}

static uint8_t map[64 * 64];
static int cols, rows;
// Changed blocks of one read, -1 without a map.
static int read_map()
{
    int changed = 0;
    const int result = mmf_tile_map(0, map, sizeof(map), &cols, &rows, &changed);
    return result == 0 ? changed : -1;
}
// A change shows within two reads (one of each row parity) at the block
// (column, row) of the map, and is gone once kept.
static void expect_found(int column, int row, const char *what)
{
    bool found = false;
    for (int read = 0; read < 2 && !found; ++read) {
        const int changed = read_map();
        assert(changed >= 0);
        found = changed > 0 && map[row * cols + column];
    }
    if (!found) { fprintf(stderr, "not found: %s\n", what); abort(); }
    assert(mmf_tile_map_keep(0, 0, 0, W, H) == 0);
    assert(read_map() == 0 && read_map() == 0);
}

int main()
{
    vi_jpeg_ch = -1;
    for (int format : {PIXEL_FORMAT_NV16, PIXEL_FORMAT_NV21}) {
        mmf_tile_map_close();
        frame(format);
        assert(read_map() == -1 && map[0] == 0); // nothing kept yet: whole picture
        assert(mmf_tile_map_keep(0, 0, 0, W, H) == 0);
        assert(read_map() == 0 && read_map() == 0 && cols == W / 16 && rows == (H + 15) / 16);

        // Equal luma, other colour: a 16x16 block at pixels (32, 48).
        const int scale = format == PIXEL_FORMAT_NV21 ? 2 : 1;
        for (int y = 48 / scale; y < 64 / scale; ++y)
            for (int x = 32; x < 48; ++x) chroma[y * W + x] = 200;
        expect_found(2, 3, "chroma block");

        // Colour of one chroma row, then brightness of one pixel row (odd).
        chroma[(101 / scale) * W + 70] = 30;
        expect_found(70 / 16, 101 / 16, "chroma row");
        luma[101 * W + 70] = 0;
        expect_found(70 / 16, 101 / 16, "luma row");

        // The rows below the last whole block row (the strips).
        luma[135 * W + 200] = 0;
        expect_found(200 / 16, rows - 1, "luma bottom row");
        chroma[(chroma_rows - 1) * W + 200] = 0;
        expect_found(200 / 16, rows - 1, "chroma bottom row");

        // Keeping only the sent tile: the other changed tile stays changed.
        luma[10 * W + 10] = 0;
        luma[10 * W + 200] = 0;
        for (int read = 0; read < 2; ++read) read_map();
        assert(mmf_tile_map_keep(0, 0, 0, 128, 128) == 0);
        bool left = false, kept = false;
        for (int read = 0; read < 2; ++read) {
            if (read_map() > 0) {
                left = left || map[200 / 16];
                kept = kept || map[0];
            }
        }
        assert(left && !kept);
        assert(mmf_tile_map_keep(0, 0, 0, W, H) == 0);
        mmf_tile_map_reset();
        assert(read_map() == -1);
    }
    mmf_tile_map_close();
    puts("PASS tile map: luma and chroma changes of NV16 and NV21, one-row changes within two reads, strips, kept tiles.");

    // Region channels: a submission without a stream is retired with the
    // channel; a failed destroy keeps the channel and its frame until a
    // retry succeeds.
    frame(PIXEL_FORMAT_NV16);
    uint8_t *data = nullptr;
    int size = 0, recreated = 0;
    assert(mmf_enc_jpg_region(2, 0, 0, 0, 128, 128, 80, &data, &size, &recreated) == 0 && recreated == 1);
    assert(size == sizeof(jpeg) && mmf_enc_jpg_region_free(2) == 0);

    get_stream_error = CVI_ERR_VENC_BUSY;
    destroys = 0;
    assert(mmf_enc_jpg_region(2, 0, 0, 0, 128, 128, 80, &data, &size, &recreated) == CVI_ERR_VENC_BUSY);
    assert(destroys == 1 && !_mmf_jpg_region_holds(0));
    released_frames = 0;
    mmf_vi_frame_release(0);
    assert(released_frames == 1 && !priv.vi_frame_valid[0]);

    frame(PIXEL_FORMAT_NV16);
    destroy_error = CVI_FAILURE;
    destroys = 0;
    assert(mmf_enc_jpg_region(3, 0, 0, 0, W, H, 80, &data, &size, &recreated) == CVI_ERR_VENC_BUSY);
    assert(destroys == 1);
    released_frames = 0;
    mmf_vi_frame_release(0); // refused: the encoder may still read it
    assert(released_frames == 0 && priv.vi_frame_valid[0] && destroys == 2);
    assert(mmf_enc_jpg_region_close() != 0 && destroys == 3);
    assert(mmf_enc_jpg_region(3, 0, 0, 0, W, H, 80, &data, &size, &recreated) == CVI_FAILURE && destroys == 4);
    destroy_error = 0;
    get_stream_error = 0;
    mmf_vi_frame_release(0); // the retry destroys the channel: then the frame goes
    assert(released_frames == 1 && !priv.vi_frame_valid[0] && destroys == 5);
    assert(mmf_enc_jpg_region_close() == 0 && destroys == 5);
    puts("PASS region JPEG channels: a stream that never comes retires the channel; its frame stays until it is destroyed.");

    // Creation is already ownership, even if setup never reached StartRecvFrame.
    for (int setup_step = 0; setup_step < 3; ++setup_step) {
        frame(PIXEL_FORMAT_NV16);
        get_param_error = setup_step == 0 ? CVI_FAILURE : 0;
        set_param_error = setup_step == 1 ? CVI_FAILURE : 0;
        start_error = setup_step == 2 ? CVI_FAILURE : 0;
        destroy_error = CVI_FAILURE;
        destroys = 0;
        assert(mmf_enc_jpg_region(2, 0, 0, 0, 128, 128, 80, &data, &size, &recreated) != 0);
        assert(allocated[2] && destroys == 1);
        assert(mmf_enc_jpg_region_close() != 0 && allocated[2] && destroys == 2);
        get_param_error = set_param_error = start_error = destroy_error = 0;
        // A read must first retire the incomplete channel, then create a usable one.
        assert(mmf_enc_jpg_region(2, 0, 0, 0, 128, 128, 80, &data, &size, &recreated) == 0);
        assert(destroys == 3 && recreated == 1);
        assert(mmf_enc_jpg_region_free(2) == 0 && mmf_enc_jpg_region_close() == 0);
        assert(!allocated[2]);
    }

    // Failed output release also requires retirement before another submission.
    frame(PIXEL_FORMAT_NV16);
    assert(mmf_enc_jpg_region(3, 0, 0, 0, W, H, 80, &data, &size, &recreated) == 0);
    release_error = destroy_error = CVI_FAILURE;
    destroys = 0;
    assert(mmf_enc_jpg_region_free(3) != 0 && allocated[3] && destroys == 1);
    const int sent = sends;
    assert(mmf_enc_jpg_region(3, 0, 0, 0, W, H, 80, &data, &size, &recreated) != 0);
    assert(sends == sent && destroys == 2);
    release_error = destroy_error = 0;
    assert(mmf_enc_jpg_region(3, 0, 0, 0, W, H, 80, &data, &size, &recreated) == 0);
    assert(sends == sent + 1 && recreated == 1 && destroys == 3);
    assert(mmf_enc_jpg_region_free(3) == 0 && mmf_enc_jpg_region_close() == 0);
    puts("PASS region setup/release faults: live channels stay tracked and retire before reuse.");

    // Model the real locks: NV16 JPEG runs outside vi_mutex, while video
    // releases another output under vi_mutex. Run this contract with TSan
    // as well: the cross-output ownership indicator must not race.
    frame(PIXEL_FORMAT_NV16);
    priv.vi_frame[1] = priv.vi_frame[0];
    priv.vi_frame_valid[1] = true;
    for (int ch = 0; ch < MMF_VI_MAX_CHN; ++ch) priv.vi_chn_pool_id[ch] = VB_INVALID_POOLID;
    std::mutex capture_lock;
    std::atomic<bool> start{false}, done{false};
    std::thread jpeg_thread([&] {
        while (!start.load()) std::this_thread::yield();
        for (int i = 0; i < 10000; ++i) {
            mmf_jpg_hold();
            uint8_t *bytes = nullptr;
            int length = 0, created = 0;
            assert(mmf_enc_jpg_region(2, 1, 0, 0, 128, 128, 80, &bytes, &length, &created) == 0);
            assert(mmf_enc_jpg_region_free(2) == 0);
            mmf_jpg_unhold();
        }
        done.store(true);
    });
    std::thread video_thread([&] {
        start.store(true);
        do {
            std::lock_guard<std::mutex> lock(capture_lock);
            priv.vi_frame_valid[0] = true;
            priv.vi_frame[0].stVFrame.enPixelFormat = PIXEL_FORMAT_NV21;
            assert(_mmf_release_vi_frame(0) == 0);
        } while (!done.load());
    });
    jpeg_thread.join();
    video_thread.join();
    assert(mmf_enc_jpg_region_close() == 0);
    puts("PASS region ownership while NV16 JPEG encodes beside another video output.");
}
