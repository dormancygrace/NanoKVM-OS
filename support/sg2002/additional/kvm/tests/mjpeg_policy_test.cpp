#include <cassert>
#include <cstdint>
#include <cstdio>
#include "mjpeg_policy.hpp"

namespace {
struct Size { uint16_t width, height; };
// kvmv_read_img applies the rule to the input size detected at the moment of
// each gate, before a channel is created or submitted to.
bool gate_allows(const Size &detected) {
    return nanokvm::mjpeg_input_allowed(detected.width, detected.height);
}
}

int main() {
    static_assert(nanokvm::mjpeg_input_allowed(2560, 1440), "QHD");
    static_assert(!nanokvm::mjpeg_input_allowed(3840, 2160), "UHD");
    assert(nanokvm::mjpeg_input_allowed(1920, 1080));
    assert(nanokvm::mjpeg_input_allowed(2560, 1440));
    assert(nanokvm::mjpeg_input_allowed(1440, 2560));   // portrait QHD
    assert(nanokvm::mjpeg_input_allowed(0, 0));         // unknown input
    assert(nanokvm::mjpeg_input_allowed(1280, 720));
    assert(!nanokvm::mjpeg_input_allowed(3840, 2160));
    assert(!nanokvm::mjpeg_input_allowed(2160, 3840));  // portrait UHD
    assert(!nanokvm::mjpeg_input_allowed(2561, 1440));  // one over the limit
    assert(!nanokvm::mjpeg_input_allowed(1080, 2562));
    assert(!nanokvm::mjpeg_input_allowed(3840, 0));     // half-known input

    // An input that changes between reads: each read follows the size detected
    // at that moment, never a size seen earlier.
    const Size sequence[] = {{1920, 1080}, {2560, 1440}, {3840, 2160}, {3840, 2160},
                             {0, 0}, {1920, 1080}, {2160, 3840}, {1440, 2560}};
    const bool expected[] = {true, true, false, false, true, true, false, true};
    for (unsigned i = 0; i < sizeof(sequence) / sizeof(sequence[0]); ++i)
        assert(gate_allows(sequence[i]) == expected[i]);

    // Allowed at the first gate, UHD at the second (just before the JPEG
    // channel): the second gate refuses although the first one passed.
    Size detected = {1920, 1080};
    assert(gate_allows(detected));
    detected = {3840, 2160};
    assert(!gate_allows(detected));
    std::puts("mjpeg policy ok");
    return 0;
}
