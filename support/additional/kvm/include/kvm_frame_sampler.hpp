#pragma once
#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
namespace nanokvm {
// Sample only visible luma, as the existing MJPEG change detector did. The
// first frame and any geometry change must be sent, including an all-black
// initial frame. Padding is never sampled. This is not a full-frame comparison.
class FrameSampler {
public:
    bool changed(const uint8_t *luma, size_t length, int width, int height) {
        if (!luma || width <= 0 || height <= 0) return true;
        const uint64_t pixels = static_cast<uint64_t>(width) * height;
        if (pixels > length) return true;
        const size_t count = static_cast<size_t>(std::min<uint64_t>(pixels, samples_.size()));
        bool changed = width != width_ || height != height_;
        for (size_t i = 0; i < count; ++i) {
            const uint8_t value = luma[i * pixels / count];
            changed |= samples_[i] != value;
            samples_[i] = value;
        }
        width_ = width;
        height_ = height;
        return changed;
    }
private:
    std::array<uint8_t, 76800> samples_{};
    int width_ = 0, height_ = 0;
};
}
