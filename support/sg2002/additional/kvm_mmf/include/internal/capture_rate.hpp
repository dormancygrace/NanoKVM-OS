#pragma once
#include <algorithm>
namespace nanokvm {
// Qualified source geometries; portrait timings retain their measured limits.
inline int capture_rate_limit(int width, int height) {
    if (width <= 0 || height <= 0) return 120;
    if (width == 720 && height == 1280) return 120;
    if (width == 1080 && height == 1920) return 70;
    if (width == 1088 && height == 1920) return 60;
    if (width == 1296 && height == 2304) return 50;
    if (width == 1440 && height == 2560) return 40;
    if (width <= 1280 && height <= 720) return 120;
    if (width <= 1920 && height <= 1080) return 75;
    return 50;
}
inline int capture_stream_rate_limit(int sw, int sh, int ow, int oh) {
    return std::min(capture_rate_limit(sw, sh), capture_rate_limit(ow, oh));
}
}
