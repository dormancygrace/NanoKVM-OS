#pragma once
#include "cvi_buffer.h"
#include "cvi_sys.h"
#include "cvi_vb.h"
#include <cstddef>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <sys/mman.h>

namespace nanokvm::media {
// Only frames created here may be released here. One VB allocation has one
// mapping, irrespective of the number of planes or gaps between planes.
struct OwnedFrame {
    VIDEO_FRAME_INFO_S info;
    VB_BLK block;
    void *mapping;
    CVI_U32 mapping_length;
};
static_assert(offsetof(OwnedFrame, info) == 0);
inline CVI_S32 release(VIDEO_FRAME_INFO_S *frame) {
    if (!frame) return CVI_SUCCESS;
    auto *owned = reinterpret_cast<OwnedFrame *>(frame);
    CVI_S32 result = CVI_SUCCESS;
    if (owned->mapping) result = CVI_SYS_Munmap(owned->mapping, owned->mapping_length);
    const CVI_S32 released = CVI_VB_ReleaseBlock(owned->block);
    free(owned);
    return result != CVI_SUCCESS ? result : released;
}
inline VIDEO_FRAME_INFO_S *allocate(VB_POOL pool, SIZE_S size, PIXEL_FORMAT_E format,
                                    const VB_CAL_CONFIG_S &layout) {
    if (!size.u32Width || !size.u32Height || !layout.u32VBSize || !layout.plane_num
        || layout.plane_num > 3 || !layout.u16AddrAlign) return nullptr;
    auto *owned = static_cast<OwnedFrame *>(calloc(1, sizeof(OwnedFrame)));
    if (!owned) return nullptr;
    owned->block = CVI_VB_GetBlock(pool, layout.u32VBSize);
    if (owned->block == VB_INVALID_HANDLE) { free(owned); return nullptr; }
    const CVI_U64 physical = CVI_VB_Handle2PhysAddr(owned->block);
    auto &frame = owned->info.stVFrame;
    owned->info.u32PoolId = CVI_VB_Handle2PoolId(owned->block);
    if (!physical || physical > UINT64_MAX - layout.u32VBSize
        || owned->info.u32PoolId == VB_INVALID_POOLID) { release(&owned->info); return nullptr; }
    frame.enCompressMode = COMPRESS_MODE_NONE;
    frame.enPixelFormat = format;
    frame.enVideoFormat = VIDEO_FORMAT_LINEAR;
    frame.enColorGamut = COLOR_GAMUT_BT709;
    frame.enDynamicRange = DYNAMIC_RANGE_SDR8;
    frame.u32Width = size.u32Width;
    frame.u32Height = size.u32Height;
    CVI_U64 end = physical;
    for (unsigned i = 0; i < layout.plane_num; ++i) {
        const CVI_U64 gap = i == 0 ? 0 : (layout.u16AddrAlign - end % layout.u16AddrAlign) % layout.u16AddrAlign;
        const CVI_U64 offset = end - physical + gap;
        const CVI_U32 length = i == 0 ? layout.u32MainYSize : layout.u32MainCSize;
        if (!length || offset > layout.u32VBSize || length > layout.u32VBSize - offset) {
            release(&owned->info); return nullptr;
        }
        frame.u64PhyAddr[i] = physical + offset;
        frame.u32Length[i] = length;
        frame.u32Stride[i] = i == 0 ? layout.u32MainStride : layout.u32CStride;
        end = frame.u64PhyAddr[i] + length;
    }
    void *mapping = CVI_SYS_MmapCache(physical, layout.u32VBSize);
    if (!mapping || mapping == MAP_FAILED) { release(&owned->info); return nullptr; }
    owned->mapping = mapping;
    owned->mapping_length = layout.u32VBSize;
    for (unsigned i = 0; i < layout.plane_num; ++i)
        frame.pu8VirAddr[i] = static_cast<CVI_U8 *>(mapping) + (frame.u64PhyAddr[i] - physical);
    // Do not expose old VB contents through row padding to the hardware.
    memset(mapping, 0, layout.u32VBSize);
    return &owned->info;
}
struct Plane {
    const uint8_t *data;
    size_t length;
    uint32_t stride;
};
struct View {
    uint32_t width, height;
    PIXEL_FORMAT_E format;
    Plane planes[2];
};
inline bool valid_plane(const Plane &plane, size_t row_bytes, size_t rows) {
    return plane.data && rows && plane.stride >= row_bytes
        && (rows - 1) <= SIZE_MAX / plane.stride
        && (rows - 1) * plane.stride <= plane.length
        && row_bytes <= plane.length - (rows - 1) * plane.stride;
}
inline CVI_S32 copy_and_flush(VIDEO_FRAME_INFO_S *destination, const View &source) {
    if (!destination || !source.width || !source.height) return CVI_FAILURE;
    auto &frame = destination->stVFrame;
    if (frame.u32Width != source.width || frame.u32Height != source.height) return CVI_FAILURE;
    const bool gray = source.format == PIXEL_FORMAT_UINT8_C1;
    const bool nv21 = source.format == PIXEL_FORMAT_NV21 || gray;
    if ((!nv21 && source.format != PIXEL_FORMAT_RGB_888)
        || frame.enPixelFormat != (nv21 ? PIXEL_FORMAT_NV21 : PIXEL_FORMAT_RGB_888)
        || (nv21 && (source.width % 2 || source.height % 2))) return CVI_FAILURE;
    const unsigned planes = nv21 ? 2 : 1;
    const size_t row_bytes = static_cast<size_t>(source.width) * (nv21 ? 1 : 3);
    // Validate all planes before writing any destination data or flushing.
    for (unsigned i = 0; i < planes; ++i) {
        const size_t rows = i == 0 ? source.height : source.height / 2;
        const Plane dst{frame.pu8VirAddr[i], frame.u32Length[i], frame.u32Stride[i]};
        if (!frame.u64PhyAddr[i] || !valid_plane(dst, row_bytes, rows)
            || (!(gray && i == 1) && !valid_plane(source.planes[i], row_bytes, rows))) return CVI_FAILURE;
    }
    for (unsigned i = 0; i < planes; ++i) {
        const size_t rows = i == 0 ? source.height : source.height / 2;
        for (size_t row = 0; row < rows; ++row) {
            uint8_t *out = frame.pu8VirAddr[i] + row * frame.u32Stride[i];
            if (gray && i == 1) memset(out, 128, row_bytes);
            else memcpy(out, source.planes[i].data + row * source.planes[i].stride, row_bytes);
        }
    }
    // Every CPU-written plane must reach memory before VENC/VPSS can read it.
    for (unsigned i = 0; i < planes; ++i) {
        const CVI_S32 result = CVI_SYS_IonFlushCache(frame.u64PhyAddr[i], frame.pu8VirAddr[i], frame.u32Length[i]);
        if (result != CVI_SUCCESS) return result;
    }
    return CVI_SUCCESS;
}
inline CVI_S32 copy_packed_and_flush(VIDEO_FRAME_INFO_S *destination, const uint8_t *data,
                                    int width, int height, PIXEL_FORMAT_E format) {
    if (!data || width <= 0 || height <= 0 || width > UINT16_MAX || height > UINT16_MAX) return CVI_FAILURE;
    const bool rgb = format == PIXEL_FORMAT_RGB_888;
    const uint32_t stride = static_cast<uint32_t>(width) * (rgb ? 3 : 1);
    const size_t y_length = static_cast<size_t>(stride) * height;
    View source{static_cast<uint32_t>(width), static_cast<uint32_t>(height), format,
                {{data, y_length, stride}, {nullptr, 0, 0}}};
    if (format == PIXEL_FORMAT_NV21) source.planes[1] = {data + y_length, y_length / 2, stride};
    return copy_and_flush(destination, source);
}
inline CVI_S32 copy_mapped_nv21_and_flush(VIDEO_FRAME_INFO_S *destination,
                                          const VIDEO_FRAME_INFO_S *input, const uint8_t *mapping) {
    if (!input || !mapping) return CVI_FAILURE;
    const auto &frame = input->stVFrame;
    if (frame.enPixelFormat != PIXEL_FORMAT_NV21 || frame.u32Length[2]
        || frame.u64PhyAddr[1] < frame.u64PhyAddr[0]) return CVI_FAILURE;
    // Existing MMF VI mappings span the sum of the plane lengths. A physical
    // gap outside that mapping cannot be safely copied by this interface.
    const uint64_t offset = frame.u64PhyAddr[1] - frame.u64PhyAddr[0];
    const uint64_t mapped_size = static_cast<uint64_t>(frame.u32Length[0]) + frame.u32Length[1];
    if (offset < frame.u32Length[0] || offset > mapped_size || frame.u32Length[1] > mapped_size - offset)
        return CVI_FAILURE;
    View source{frame.u32Width, frame.u32Height, frame.enPixelFormat,
                {{mapping, frame.u32Length[0], frame.u32Stride[0]},
                 {mapping + offset, frame.u32Length[1], frame.u32Stride[1]}}};
    return copy_and_flush(destination, source);
}
} // namespace nanokvm::media
