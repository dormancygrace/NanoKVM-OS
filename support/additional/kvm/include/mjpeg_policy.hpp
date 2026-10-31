#pragma once
#include <cstdint>
namespace nanokvm {
// UHD input with a secondary JPEG output is still unsupported: a 3840x2160
// H.265 stream plus even 1920x1080 JPEG can stall VPSS frame delivery. Check
// input geometry, not just the requested JPEG size. kvmv_read_img keeps that
// geometry snapshot valid for the guarded step with a lease (geometry_gate.hpp).
constexpr uint16_t mjpeg_max_input_long_side = 2560;
// Portrait inputs count by their long side. An unknown input (0x0) is allowed:
// nothing is captured until a size is detected, and it is checked again then.
constexpr bool mjpeg_input_allowed(uint16_t width, uint16_t height) {
    return (width > height ? width : height) <= mjpeg_max_input_long_side;
}
}
