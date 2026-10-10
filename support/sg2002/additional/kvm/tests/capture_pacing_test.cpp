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
    puts("PASS capture pacing: reader window and pre-submit gating");
}
