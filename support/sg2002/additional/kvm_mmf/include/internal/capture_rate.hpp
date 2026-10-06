#pragma once
#include <algorithm>
namespace nanokvm {
// Rate policy follows the pixel geometry, independently of orientation.
inline int capture_rate_limit(int width, int height) {
    if (width <= 0 || height <= 0) return 120;
    const int longer = std::max(width, height);
    const int shorter = std::min(width, height);
    if (longer <= 1280 && shorter <= 720) return 120;
    // 1088 is the legacy aligned FHD width, not an additional resolution tier.
    if (longer <= 1920 && shorter <= 1088) return 75;
    // 3840x2160 needs the video overclock for 30 fps (encoder and VPSS).
    if (longer > 2560 || shorter > 1440) return 30;
    return 50;
}
inline int capture_stream_rate_limit(int sw, int sh, int ow, int oh) {
    return std::min(capture_rate_limit(sw, sh), capture_rate_limit(ow, oh));
}
}
