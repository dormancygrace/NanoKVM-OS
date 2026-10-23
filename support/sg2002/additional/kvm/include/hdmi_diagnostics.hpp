#pragma once
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <string>
#include "hdmi_model.hpp"

namespace nanokvm {
enum class DiagnosticState { Unsupported, Ready, Invalid, IoError };
struct ReceiverDiagnostics {
    DiagnosticState state = DiagnosticState::Unsupported;
    ReceiverKind model = ReceiverKind::Unknown, expected_model = ReceiverKind::Unknown;
    uint16_t chip_id = 0;
    bool identity_checked = false;
    uint8_t firmware_raw[5]{}, firmware_size = 0, stream = 0;
    uint32_t byte_clock_raw = 0, tx_clock_raw = 0;
    uint8_t rx_status_raw = 0, lane_config_raw[2]{}, hpol_raw = 0, vpol_raw = 0;
    bool c_status_available = false, tx_timing_available = false;
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
    d.model = ReceiverKind::UXC;
    d.firmware_size = 4;
    for (unsigned i = 0; i < 4; ++i) d.firmware_raw[i] = identity[5 + i];
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
        d.vtotal == d.height + d.vfront + d.vback + d.vsync)
        d.state = DiagnosticState::Ready;
    return d;
}
/* Rockchip C's D2 measurement registers use half-pixel horizontal units.
 * Firmware and CSI data are separate reads; lane count and clock units are not inferred. */
inline ReceiverDiagnostics decode_c_diagnostics(const uint8_t timing[26],
    const uint8_t firmware[5], uint16_t csi_width, uint16_t csi_height)
{
    ReceiverDiagnostics d;
    d.model = ReceiverKind::C;
    d.state = DiagnosticState::Invalid;
    d.firmware_size = 5;
    for (unsigned i = 0; i < 5; ++i) d.firmware_raw[i] = firmware[i];
    auto pair = [&](unsigned i) { return (uint32_t(timing[i]) << 8) | timing[i + 1]; };
    d.hsync = pair(14) * 2; d.hfront = pair(22) * 2; d.hback = pair(18) * 2;
    d.vsync = timing[0]; d.vfront = timing[2]; d.vback = timing[1];
    d.htotal = pair(3) * 2; d.vtotal = pair(24);
    d.width = pair(5) * 2; d.height = pair(16);
    d.csi_width = csi_width; d.csi_height = csi_height;
    bool invalid_word = false;
    for (unsigned i : {3U, 5U, 14U, 16U, 18U, 22U, 24U})
        invalid_word |= pair(i) == UINT16_MAX;
    if (!invalid_word && d.width && d.height &&
        csi_width && csi_height && csi_width != UINT16_MAX && csi_height != UINT16_MAX &&
        d.htotal > d.width && d.vtotal > d.height &&
        d.htotal == d.width + d.hfront + d.hback + d.hsync &&
        d.vtotal == d.height + d.vfront + d.vback + d.vsync)
        d.state = DiagnosticState::Ready;
    return d;
}
/* This revision's producer is verified in the Espressif MCU image, not merely
 * guessed from the silicon ID. Unknown revisions keep the baseline map. */
inline bool d_has_25051301_layout(const uint8_t firmware[4])
{
    return firmware[0] == 0x25 && firmware[1] == 0x05 &&
        firmware[2] == 0x13 && firmware[3] == 0x01;
}
/* D firmware telemetry from E080..E096 (Espressif). This map does not expose
 * independently verified HDMI/CSI geometries. Only 25.05.13.01's additional
 * MIPI TX sync/porch fields are confirmed by its producer. E084 is an event
 * type, not a live cable-presence bit; clock units stay raw. */
inline ReceiverDiagnostics decode_d_diagnostics(const uint8_t data[23], uint8_t stream,
    const uint8_t *tx = nullptr)
{
    ReceiverDiagnostics d;
    d.model = ReceiverKind::D;
    d.state = DiagnosticState::Invalid;
    d.firmware_size = 4;
    for (unsigned i = 0; i < 4; ++i) d.firmware_raw[i] = data[i];
    auto pair = [&](unsigned i) { return (uint32_t(data[i]) << 8) | data[i + 1]; };
    // Producer 0x3452 copies full horizontal values for format 0x0a (YUV420
    // 8-bit); its other branch halves them. This is diagnostic scaling only.
    const unsigned scale = d_has_25051301_layout(data) && data[22] == 0x0a ? 1 : 2;
    d.htotal = pair(8) * scale; d.vtotal = pair(10);
    d.width = pair(12) * scale; d.height = pair(14);
    d.lanes = data[21] & 0x0f; d.color = data[22]; d.status = data[4];
    d.stream = stream;
    d.byte_clock_raw = (uint32_t(data[18]) << 16) | (uint32_t(data[19]) << 8) | data[20];
    if (pair(8) != UINT16_MAX && pair(10) != UINT16_MAX &&
        pair(12) && pair(12) != UINT16_MAX && pair(12) <= UINT16_MAX / scale &&
        pair(14) && pair(14) != UINT16_MAX &&
        d.htotal > d.width && d.vtotal > d.height &&
        (d.lanes == 1 || d.lanes == 2 || d.lanes == 4 || d.lanes == 8) &&
        d.color <= 0x0c && stream == 1)
        d.state = DiagnosticState::Ready;
    if (d_has_25051301_layout(data) && tx) {
        // MCU 0x33ed..0x35ff, labelled MIPI TX by its own debug strings.
        // No claim that these are independently measured HDMI timings.
        d.tx_timing_available = true;
        d.tx_clock_raw = (uint32_t(tx[0]) << 16) | (uint32_t(tx[1]) << 8) | tx[2];
        auto word = [&](unsigned i) { return (uint32_t(tx[i]) << 8) | tx[i + 1]; };
        d.hsync = word(3) * scale; d.hfront = word(5) * scale; d.hback = word(7) * scale;
        d.vsync = word(9); d.vfront = word(11); d.vback = word(13);
        d.hpol_raw = tx[15]; d.vpol_raw = tx[16];
        bool invalid_word = false;
        for (unsigned i : {3U, 5U, 7U, 9U, 11U, 13U})
            invalid_word |= word(i) == UINT16_MAX;
        // The half-unit producer truncates all four components separately.
        // floor(sum / 2) - sum(floor(component / 2)) is 0..2 raw units.
        // Full-unit format 0x0a and vertical timings must still match exactly.
        const uint32_t hsum = d.width + d.hsync + d.hfront + d.hback;
        const uint32_t rounding = scale == 2 ? 4 : 0;
        if (invalid_word || d.htotal < hsum || d.htotal - hsum > rounding ||
            d.vtotal != d.height + d.vsync + d.vfront + d.vback)
            d.state = DiagnosticState::Invalid;
    }
    return d;
}
inline std::string format_firmware_raw(const ReceiverDiagnostics &d)
{
    if (!d.firmware_size) return "unavailable";
    bool all_zero = true, all_ff = true;
    std::string value;
    for (unsigned i = 0; i < d.firmware_size && i < sizeof(d.firmware_raw); ++i) {
        char byte[4];
        snprintf(byte, sizeof(byte), "%02x", d.firmware_raw[i]);
        if (i) value += ".";
        value += byte;
        all_zero &= d.firmware_raw[i] == 0;
        all_ff &= d.firmware_raw[i] == 0xff;
    }
    return value + (all_zero || all_ff ? "(unavailable)" : "");
}
inline std::string format_receiver_diagnostics(const ReceiverDiagnostics &d)
{
    char text[768];
    if (d.state == DiagnosticState::Unsupported)
        return "[hdmi] diagnostics unavailable: receiver model unknown";
    if (d.state == DiagnosticState::IoError) {
        snprintf(text, sizeof(text), "[hdmi] diagnostics I2C failure error=%d cleanup=%d",
            d.error, d.cleanup_error);
    } else if (d.model == ReceiverKind::Unknown) {
        snprintf(text, sizeof(text),
            "[hdmi] diagnostics invalid identity model=unknown board_model=%s chip_id=0x%04x",
            receiver_name(d.expected_model), d.chip_id);
    } else if (d.model == ReceiverKind::C) {
        snprintf(text, sizeof(text),
            "[hdmi] diagnostics %s model=%s chip_id=0x%04x fw_raw=%s lanes=unavailable "
            "HDMI=%ux%u total=%ux%u H(sync/front/back)=%u/%u/%u "
            "V(sync/front/back)=%u/%u/%u CSI=%ux%u clock=unavailable "
            "rx_status_raw=0x%02x clock_detect_bit0=%s pll_status_bit6=%s "
            "lane_config_raw=0x%02x/0x%02x",
            d.state == DiagnosticState::Ready ? "ready" : "invalid/incoherent",
            receiver_name(d.model), d.chip_id, format_firmware_raw(d).c_str(),
            d.width, d.height, d.htotal, d.vtotal, d.hsync, d.hfront, d.hback,
            d.vsync, d.vfront, d.vback, d.csi_width, d.csi_height, d.rx_status_raw,
            !d.c_status_available ? "unavailable" : d.rx_status_raw & 1 ? "1" : "0",
            !d.c_status_available ? "unavailable" : d.rx_status_raw & 0x40 ? "1" : "0",
            d.lane_config_raw[0], d.lane_config_raw[1]);
    } else if (d.model == ReceiverKind::D) {
        char chip[24] = "exclusive-probe-only";
        if (d.identity_checked) snprintf(chip, sizeof(chip), "0x%04x", d.chip_id);
        snprintf(text, sizeof(text),
            "[hdmi] diagnostics %s board_model=%s chip_id=%s fw_raw=%s lanes=%u "
            "active=%ux%u total=%ux%u event_raw=0x%02x format_raw=0x%02x "
            "stream_raw=0x%02x byte_clock_raw=0x%06x HDMI_CSI_independent=unavailable "
            "%s",
            d.state == DiagnosticState::Ready ? "ready" : "invalid/incoherent",
            receiver_name(d.model), chip, format_firmware_raw(d).c_str(),
            d.lanes, d.width, d.height, d.htotal, d.vtotal, d.status, d.color,
            d.stream, d.byte_clock_raw,
            d.tx_timing_available ? "timing_profile=esp-25051301" : "sync_porches=unavailable");
        if (d.tx_timing_available) {
            char tx[256];
            snprintf(tx, sizeof(tx),
                " MIPI_TX_H(sync/front/back)=%u/%u/%u MIPI_TX_V(sync/front/back)=%u/%u/%u "
                "polarity_raw=0x%02x/0x%02x tx_clock_raw=0x%06x clock_units=unavailable",
                d.hsync, d.hfront, d.hback, d.vsync, d.vfront, d.vback,
                d.hpol_raw, d.vpol_raw, d.tx_clock_raw);
            return std::string(text) + tx;
        }
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
    if (d.model == ReceiverKind::UXC) {
        char identity[64];
        snprintf(identity, sizeof(identity), " model=LT6911UXC chip_id=0x%04x", d.chip_id);
        return std::string(text) + identity;
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
        // Invalid data is sampled again on a state/size transition or IRQ.
        retry_ = d.state == DiagnosticState::IoError;
        next_ = now + std::chrono::seconds(5);
    }
};
} // namespace nanokvm
