#pragma once
#include <cstdint>
namespace nanokvm {
struct StreamSize { uint16_t width; uint16_t height; };
inline StreamSize stream_size(uint16_t iw, uint16_t ih, uint16_t mw, uint16_t mh) {
    uint32_t w = iw, h = ih;
    if (!w || !h) return {0, 0};
    if (mw && mh && (w > mw || h > mh)) {
        if (uint32_t(mw) * h <= uint32_t(mh) * w) {
            h = h * mw / w; w = mw;
        } else { w = w * mh / h; h = mh; }
    }
    // NV21 chroma requires even dimensions.
    return {uint16_t(w & ~1u), uint16_t(h & ~1u)};
}
}
