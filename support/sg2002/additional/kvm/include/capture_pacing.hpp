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
// stream during that time. A JPEG reader on its own output while the video
// output is straight from capture shares neither: the video lease is a VI
// frame, the JPEG reads its own VPSS output, and pre-submitting stays on.
// Without it the video read waits for the next VI frame and then for the
// whole encode, every other frame (2560x1440: 25 instead of 60 fps).
//
// Below the fast pixel rate a video read alone keeps up, but one next to such
// a JPEG reader waits for the JPEG encode too, so it pre-submits there as well
// above 30 fps and half the fast pixel rate. Next to MJPEG at 25 fps,
// 2560x1440 gave 25 serially and 33 with it at 40 fps, 24 and 47 at 50, and
// 2560x1080 at 60 gave 34 and 58. Where the encoder is done long before the
// next frame, pre-submitting lost frames: 2560x1440 at 30 fps 24 serially and
// 15 with it, 1280x720 at 60 fps 31 with it.
inline bool presubmit_next_video_frame(long pixel_rate, long fast_pixel_rate, int fps,
                                       bool jpeg_reader_active, bool jpeg_apart = false) {
    if (jpeg_reader_active && !jpeg_apart) return false;
    return pixel_rate > fast_pixel_rate
        || (jpeg_reader_active && fps > 30 && pixel_rate > fast_pixel_rate / 2);
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
