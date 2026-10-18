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
// The nominal rate of the HDMI input. The VI channel rate reads a few frames
// either side of it: take the nearest common rate within three (57-63 is 60,
// 87-93 is 90; a tie takes the faster), else the reading itself. A faster
// rate counts at once: the stream setting still caps the encoder, so raising
// the cap is never worse than without it. A slower one counts after three
// equal readings, once a second each, so a dip does not rebuild the encoder.
// A zero reading (channel off) keeps the rate.
struct InputRateFilter {
    int rate = 0, candidate = 0, seen = 0;
    static int nominal(int measured) {
        static constexpr int rates[] = {24, 25, 30, 48, 50, 60, 72, 75, 90, 100, 120, 144};
        if (measured <= 0) return 0;
        int best = measured, distance = 3;
        for (int r : rates) {
            const int d = r > measured ? r - measured : measured - r;
            if (d <= distance) { best = r; distance = d; }
        }
        return best;
    }
    int update(int measured) {
        const int next = nominal(measured);
        if (next == 0 || next == rate) { seen = 0; return rate; }
        if (next > rate) { rate = next; seen = 0; return rate; }
        if (next != candidate) { candidate = next; seen = 0; }
        if (++seen >= 3) { rate = next; seen = 0; }
        return rate;
    }
};
}
