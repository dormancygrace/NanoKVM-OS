#pragma once
#include <cstdint>
namespace nanokvm {
// MJPEG cannot run with a HDMI input larger than QHD: at 3840x2160 the JPEG
// channel wedges the hardware encoder (cviGetEncodedInfo = 2501, encoder
// timeout) until the next reboot. The size rule lives here so a host test can
// cover it; kvmv_read_img applies it to the geometry snapshot of the pass and
// keeps the size valid for the guarded step with a lease (geometry_gate.hpp).
constexpr uint16_t mjpeg_max_input_long_side = 2560;
// Portrait inputs count by their long side. An unknown input (0x0) is allowed:
// nothing is captured until a size is detected, and it is checked again then.
constexpr bool mjpeg_input_allowed(uint16_t width, uint16_t height) {
    return (width > height ? width : height) <= mjpeg_max_input_long_side;
}
}
