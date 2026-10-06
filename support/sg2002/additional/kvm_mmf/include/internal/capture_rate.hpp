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
// With the video overclock the encoder sustains about 250 Mpix/s: QHD at 60
// and UHD at 30 fps; 1080p gets 100 of the about 109 fps it reaches, which
// leaves room for the per-frame overhead. 1088 is the legacy aligned FHD
// width, not an additional resolution tier.
inline constexpr CaptureRateTier capture_rate_tiers[] = {
    {1280, 720, 120},
    {1920, 1088, 100},
    {2560, 1440, 60},
    {0, 0, 30},
};
// Above QHD at 50 fps one frame takes the encoder about a whole frame period.
inline constexpr long fast_pixel_rate = 2560L * 1440 * 50;
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
