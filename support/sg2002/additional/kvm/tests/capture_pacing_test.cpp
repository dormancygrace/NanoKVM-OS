#include <cassert>
#include <cstdio>
#include "capture_pacing.hpp"
#include "internal/capture_rate.hpp"
int main() {
    using namespace nanokvm;
    // A reader is active for two seconds after its last request.
    assert(!reader_active(0, 5000));            // Never read.
    assert(reader_active(1000, 1000));
    assert(reader_active(1000, 2999));
    assert(!reader_active(1000, 3000));
    assert(!reader_active(1000, 999));          // Clock never goes backwards; be safe.
    // Pre-submit needs a high pixel rate and no JPEG reader.
    const long qhd60 = 2560L * 1440 * 60, fhd60 = 1920L * 1080 * 60, fhd100 = 1920L * 1080 * 100;
    assert(presubmit_next_video_frame(qhd60, fast_pixel_rate, false));
    assert(!presubmit_next_video_frame(qhd60, fast_pixel_rate, true));
    assert(!presubmit_next_video_frame(fhd60, fast_pixel_rate, false));
    assert(!presubmit_next_video_frame(fhd60, fast_pixel_rate, true));
    assert(presubmit_next_video_frame(fhd100, fast_pixel_rate, false));
    assert(!presubmit_next_video_frame(fhd100, fast_pixel_rate, true));
    assert(!presubmit_next_video_frame(2560L * 1440 * 50, fast_pixel_rate, false)); // Boundary.
    // The input rate snaps to a common rate; it rises at once and falls after
    // three equal readings.
    assert(InputRateFilter::nominal(0) == 0);
    assert(InputRateFilter::nominal(57) == 60 && InputRateFilter::nominal(61) == 60);
    assert(InputRateFilter::nominal(89) == 90 && InputRateFilter::nominal(91) == 90);
    assert(InputRateFilter::nominal(75) == 75 && InputRateFilter::nominal(50) == 50);
    assert(InputRateFilter::nominal(49) == 50 && InputRateFilter::nominal(73) == 72);
    assert(InputRateFilter::nominal(63) == 60 && InputRateFilter::nominal(66) == 66);
    InputRateFilter in;
    assert(in.update(0) == 0);
    assert(in.update(60) == 60);                 // First reading counts at once.
    assert(in.update(58) == 60 && in.update(0) == 60);
    assert(in.update(90) == 90);                 // Faster counts at once.
    assert(in.update(60) == 90 && in.update(89) == 90);  // A dip restarts the count.
    assert(in.update(60) == 90 && in.update(60) == 90 && in.update(60) == 60);
    puts("PASS capture pacing: reader window, pre-submit gating and input rate");
}
