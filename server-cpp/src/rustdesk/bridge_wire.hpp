#pragma once

// The byte formats of the RustDesk bridge (the Go server's service/rustdesk bridge.go, control.go, audio.go,
// pointer.go and webrtc.go), apart from the sockets that carry them:
//
// media.sock  a JSON line {"Version":1,"Video":"info"|"encoded","Codec":""|"auto"|"h264"|"h265"}, then OKVF frames:
//             magic[4], version 1, codec (1 H.264, 2 H.265; 0: the payload is a UTF-8 error), flags (1 key),
//             reserved, sequence u64be, timestamp u64be (us), duration u32be (us), width u16be, height u16be,
//             payload length u32be, reserved[4]; from the daemon {"request":"keyframe"} and {"request":"fps","fps":N}.
// control.sock  OKHC v1 (see the statuses below and control_* in bridge.cpp).
// audio.sock  a JSON line {"version":1,"audio":"info"|"opus"}, then OKAF packets: magic[4], version 1, codec
//             (0 unavailable, 1 Opus), channels, reserved, sample rate u32be, payload length u16be, reserved[2].
// webrtc.sock length-prefixed JSON commands and events (u32le), then RustDesk's own framing of messages.

#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::rustdesk::wire {

// --- media (OKVF) ---

inline constexpr std::size_t media_header_size = 40;
inline constexpr std::size_t max_media_error = 2048;

std::string frame_header(int native_codec, bool key, std::uint16_t width, std::uint16_t height, std::uint64_t sequence,
                         std::int64_t timestamp_us, std::int64_t duration_us, std::uint32_t length);
// Codec zero: a bounded UTF-8 error instead of a frame (header and message).
std::string media_error(std::string_view message);

struct MediaRequest {
	long long version = 0;
	std::string video, codec;
};
// json.Unmarshal of the first line into the request and its checks (version 1, video info/encoded, codec empty,
// auto, h264 or h265).
bool parse_media_request(std::string_view line, MediaRequest &out);

// A line from the daemon while frames flow: a key frame request (the exact line), a frame rate (fps > 0), or nothing.
enum class MediaCommand { none, keyframe, fps };
MediaCommand parse_media_command(std::string_view line, long long &fps);

// strconv.ParseUint(strings.TrimSpace(text), 10, 16), 0 when it fails: a dimension file's value.
std::uint16_t parse_dimension(std::string_view text);

// --- audio (OKAF) ---

inline constexpr std::size_t max_audio_packet = 1275;
std::string audio_header(std::uint8_t codec, std::size_t length);
// A json.Decoder with DisallowUnknownFields over the line: one value, version 1, audio info or opus, nothing after.
bool parse_audio_request(std::string_view line, std::string &audio);

// --- control (OKHC) ---

inline constexpr std::uint8_t control_version = 1;
inline constexpr std::size_t control_record_size = 8, control_max_records = 8, control_max_connections = 16;
enum Record : std::uint8_t { record_keyboard = 1, record_relative = 2, record_absolute = 3, record_heartbeat = 4,
	                         record_close = 5, record_prepare = 6 };
enum Status : std::uint8_t { status_ok = 0, status_lease_held = 1, status_rejected = 2, status_invalid = 3,
	                         status_timeout = 4, status_failed = 5 };

enum class Kind { keyboard = 0, relative = 1, absolute = 2 };
// The USB reports of a record (several for an absolute position on a relative-only mouse), or status_invalid.
// reportHeld and releaseReport as the bridge's session uses them.
bool report_held(Kind kind, const std::vector<std::uint8_t> &report);
std::vector<std::uint8_t> release_report(Kind kind, const std::vector<std::uint8_t> &previous);

// relativePointer: normalized absolute positions (0..32767) turned into relative movement in screen pixels, with
// the fractions carried over, the buttons kept and the wheel sent once.
struct RelativePointer {
	bool known = false;
	std::int32_t x = 0, y = 0;
	std::int64_t x_remainder = 0, y_remainder = 0;
	std::int32_t width = 0, height = 0;
	std::vector<std::vector<std::uint8_t>> reports(std::uint8_t buttons, std::int32_t x, std::int32_t y, std::int8_t wheel);
};

// One record turned into its reports: false (status_invalid) for an out-of-range record. pointer is used (and
// moved) when the mouse is relative only.
bool record_reports(const std::uint8_t *record, RelativePointer *pointer, Kind &kind,
                    std::vector<std::vector<std::uint8_t>> &reports);

// --- WebRTC (webrtc.sock) ---

inline constexpr std::size_t rtc_max_message = 8u << 20, rtc_max_fragment = 60000, rtc_max_signal = 64u << 10,
                             rtc_max_inbound = 256u << 10;

struct RtcCommand {
	std::string op, offer, token, candidate;
};
// json.Unmarshal of a command; false when it fails.
bool parse_rtc_command(std::string_view json, RtcCommand &out);
struct RtcEvent {
	std::string event, answer, token, fingerprint, session_key, candidate, error;
};
// json.Marshal with the empty fields left out.
std::string rtc_event_json(const RtcEvent &e);
// The u32le length before a command or event.
std::string length_prefix(std::size_t n);

// The offer of a webrtc:// endpoint: its SDP, or the error ("invalid WebRTC endpoint", "invalid SDP offer").
bool decode_offer(std::string_view endpoint, std::string &sdp, std::string &error);
// rtcFingerprint: "sha-256 AA:BB:..." of the SDP, or the error.
bool rtc_fingerprint(std::string_view sdp, std::string &out, std::string &error);
// appendRTCFragment: a message's fragment (flag byte 1: more follow, 0: the last) into buffer; complete with the
// payload when it was the last. false: an invalid fragment.
bool append_rtc_fragment(std::string &buffer, std::string_view fragment, std::string &payload, bool &complete);
// RustDesk's message framing: a 1-4 byte little-endian header (length << 2 | width - 1), then the message.
std::string rustdesk_frame_header(std::size_t length);
// The header's width from its first byte, and the length from the whole header (false: over rtc_max_message).
inline std::size_t rustdesk_header_width(std::uint8_t first) { return (first & 3u) + 1; }
bool rustdesk_frame_length(const std::uint8_t *header, std::size_t width, std::size_t &length);

} // namespace nkos::rustdesk::wire
