#pragma once
#include <cstdint>
namespace nanokvm {
// A reader counts as active for this long after its last read request.
inline constexpr uint64_t reader_active_window_ms = 2000;
inline bool reader_active(uint64_t last_ms, uint64_t now_ms) {
    return last_ms != 0 && now_ms >= last_ms && now_ms - last_ms < reader_active_window_ms;
}
// Pre-submitting the next video frame keeps a VI lease on the video output
// between two reads. A concurrent JPEG read has to wait for that lease on
// every read, and a fully leased output can starve its VPSS pool (a group
// fails as a whole when one output has no buffer). So pre-submit only while
// no JPEG reader is active; the cost is the pre-submit pacing of the video
// stream during that time.
inline bool presubmit_next_video_frame(long pixel_rate, long fast_pixel_rate, bool jpeg_reader_active) {
    return pixel_rate > fast_pixel_rate && !jpeg_reader_active;
}
}
