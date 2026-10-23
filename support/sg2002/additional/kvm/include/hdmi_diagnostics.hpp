#pragma once
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <string>

namespace nanokvm {
enum class DiagnosticState { Unsupported, Ready, Invalid, IoError };
struct ReceiverDiagnostics {
    DiagnosticState state = DiagnosticState::Unsupported;
    uint32_t firmware = 0, tmds_raw = 0;
    uint32_t width = 0, height = 0, htotal = 0, vtotal = 0;
    uint32_t hsync = 0, hfront = 0, hback = 0, vsync = 0, vfront = 0, vback = 0;
    uint16_t csi_width = 0, csi_height = 0;
    uint8_t lanes = 0, polarity = 0, color = 0, hdmi_version = 0, status = 0;
    int error = 0, cleanup_error = 0;
};

/* Register locations/units from Rockchip UXC and ZHAW (see docs/VIDEO.md).
 * This is telemetry, never a source of capture geometry or color policy. */
inline ReceiverDiagnostics decode_uxc_diagnostics(const uint8_t identity[9],
    const uint8_t timing[18], const uint8_t clock[3], uint8_t version,
    uint8_t color, uint8_t final_status, uint16_t csi_width, uint16_t csi_height)
{
    ReceiverDiagnostics d;
    d.state = DiagnosticState::Invalid;
    d.lanes = identity[0]; d.status = final_status;
    d.firmware = (uint32_t(identity[5]) << 24) | (uint32_t(identity[6]) << 16) |
        (uint32_t(identity[7]) << 8) | identity[8];
    auto pair = [&](unsigned i) { return (uint32_t(timing[i]) << 8) | timing[i + 1]; };
    const unsigned scale = d.lanes <= 4 ? 2 : 1;
    d.polarity = timing[0] & 3;
    d.hsync = pair(2) * scale; d.hfront = pair(8) * scale; d.hback = pair(6) * scale;
    d.vsync = timing[1]; d.vfront = timing[5]; d.vback = timing[4];
    d.htotal = pair(12) * scale; d.vtotal = pair(10);
    d.width = pair(16) * scale; d.height = pair(14);
    d.csi_width = csi_width; d.csi_height = csi_height;
    // Keep all 24 raw bits. UXC clock units depend on firmware/reference clock;
    // the Rockchip kHz formula is not qualified on the NanoKVM 0x00000300 FW.
    d.tmds_raw = (uint32_t(clock[0]) << 16) | (uint32_t(clock[1]) << 8) | clock[2];
    d.color = (color >> 5) & 3; d.hdmi_version = version;
    if (identity[1] == 0x55 && final_status == 0x55 &&
        (d.lanes == 1 || d.lanes == 2 || d.lanes == 4 || d.lanes == 8) &&
        d.width && d.height && d.htotal > d.width && d.vtotal > d.height &&
        d.htotal == d.width + d.hfront + d.hback + d.hsync &&
        d.vtotal == d.height + d.vfront + d.vback + d.vsync &&
        d.firmware && d.firmware != UINT32_MAX)
        d.state = DiagnosticState::Ready;
    return d;
}
inline std::string format_receiver_diagnostics(const ReceiverDiagnostics &d)
{
    char text[640];
    if (d.state == DiagnosticState::Unsupported)
        return "[hdmi] diagnostics unavailable: verified register map is UXC-only";
    if (d.state == DiagnosticState::IoError) {
        snprintf(text, sizeof(text), "[hdmi] diagnostics I2C failure error=%d cleanup=%d",
            d.error, d.cleanup_error);
    } else {
        snprintf(text, sizeof(text),
            "[hdmi] diagnostics %s fw=0x%08x lanes=%u status=0x%02x "
            "HDMI=%ux%u total=%ux%u H(sync/front/back)=%u/%u/%u "
            "V(sync/front/back)=%u/%u/%u polarity=%u color_raw=%u "
            "hdmi_version_raw=0x%02x tmds_raw=0x%06x CSI=%ux%u",
            d.state == DiagnosticState::Ready ? "ready" : "invalid/incoherent",
            d.firmware, d.lanes, d.status, d.width, d.height, d.htotal, d.vtotal,
            d.hsync, d.hfront, d.hback, d.vsync, d.vfront, d.vback, d.polarity,
            d.color, d.hdmi_version, d.tmds_raw, d.csi_width, d.csi_height);
    }
    return text;
}
class DiagnosticSchedule {
    uint64_t key_ = UINT64_MAX;
    bool retry_ = false;
    std::chrono::steady_clock::time_point next_{};
public:
    void reset() { key_ = UINT64_MAX; }
    bool due(uint64_t key, std::chrono::steady_clock::time_point now) {
        if (key == key_ && (!retry_ || now < next_)) return false;
        key_ = key;
        return true;
    }
    void completed(const ReceiverDiagnostics &d, std::chrono::steady_clock::time_point now) {
        retry_ = d.state == DiagnosticState::IoError || d.state == DiagnosticState::Invalid;
        next_ = now + std::chrono::seconds(5);
    }
};
} // namespace nanokvm
