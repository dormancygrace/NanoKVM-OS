#pragma once
#include <cstdint>
namespace nanokvm {
inline bool above_fhd(uint16_t w, uint16_t h) {
    if (w == 1088 && h == 1920) return false;
    const auto longer = w > h ? w : h;
    const auto shorter = w > h ? h : w;
    return longer > 1920 || shorter > 1080;
}
// Qualified portrait input rates. Zero means the ordinary landscape policy.
inline unsigned portrait_fps_limit(unsigned w, unsigned h) {
    if (w == 720 && h == 1280) return 120;
    if (w == 1080 && h == 1920) return 70;
    if (w == 1088 && h == 1920) return 60;
    if (w == 1296 && h == 2304) return 50;
    if (w == 1440 && h == 2560) return 40;
    return 0;
}
struct StreamSize { uint16_t width; uint16_t height; };
inline StreamSize stream_size(uint16_t iw, uint16_t ih, uint16_t mw, uint16_t mh) {
    uint32_t w = iw, h = ih;
    if (!w || !h) return {0, 0};
    if (((iw == 720 && ih == 1280) || (iw == 1080 && ih == 1920) ||
         (iw == 1088 && ih == 1920) || (iw == 1296 && ih == 2304) || (iw == 1440 && ih == 2560)) && mw && mh) {
        // Stream presets are expressed in landscape coordinates. Keep the
        // qualified full portrait at FHD or larger; smaller presets fit it
        // without MMF silently widening/cropping an unaligned VPSS channel.
        if (iw == 1088 && ih == 1920 && mw >= 1920 && mh >= 1080) return {iw, ih};
        const uint32_t capw = mh, caph = mw;
        w = capw;
        if (w * ih > caph * iw) w = caph * iw / ih;
        // Standard profiles preserve their exact width; legacy profiles retain
        // their existing scaled sizes. VB strides carry the memory alignment.
        const uint32_t mask = iw == 1088 ? ~63u : ~1u;
        w = (w < iw ? w : iw) & mask;
        h = w * ih / iw;
        return {uint16_t(w), uint16_t(h & ~1u)};
    }
    if (mw && mh && (w > mw || h > mh)) {
        if (uint32_t(mw) * h <= uint32_t(mh) * w) {
            h = h * mw / w; w = mw;
        } else { w = w * mh / h; h = mh; }
    }
    // NV21 chroma requires even dimensions.
    return {uint16_t(w & ~1u), uint16_t(h & ~1u)};
}
}
