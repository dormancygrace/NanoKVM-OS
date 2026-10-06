#pragma once
#include <algorithm>
namespace nanokvm {
struct CaptureRateTier {
    int long_side;
    int short_side;
    int fps;
};
// Rate policy follows the pixel geometry, independently of orientation: the
// first tier that contains the normalized size applies; 0x0 covers the rest.
// server/common/video_status.go holds the same table (a Go test compares them).
// 1088 is the legacy aligned FHD width, not an additional resolution tier.
// Above QHD, 30 fps needs the video overclock (encoder and VPSS).
inline constexpr CaptureRateTier capture_rate_tiers[] = {
    {1280, 720, 120},
    {1920, 1088, 75},
    {2560, 1440, 50},
    {0, 0, 30},
};
inline int capture_rate_limit(int width, int height) {
    if (width <= 0 || height <= 0) return 120;
    const int longer = std::max(width, height);
    const int shorter = std::min(width, height);
    for (const auto &tier : capture_rate_tiers) {
        if (tier.long_side == 0 || (longer <= tier.long_side && shorter <= tier.short_side)) return tier.fps;
    }
    return capture_rate_tiers[0].fps;
}
inline int capture_stream_rate_limit(int sw, int sh, int ow, int oh) {
    return std::min(capture_rate_limit(sw, sh), capture_rate_limit(ow, oh));
}
}
