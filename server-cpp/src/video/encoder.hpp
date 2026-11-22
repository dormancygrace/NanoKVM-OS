#pragma once

// The device codec: H.264 or H.265, selected by an administrator and kept in /etc/kvm/encoder_codec. Every viewer
// of the device stream shares one encoder, so a viewer asking for another codec than the running one is refused.

#include <optional>
#include <string>
#include <string_view>

namespace nkos::video {

enum class Codec { h264, h265 };

inline const char *codec_name(Codec c) { return c == Codec::h264 ? "h264" : "h265"; }
std::optional<Codec> parse_codec(std::string_view name);
inline int native_codec(Codec c) { return c == Codec::h265 ? 2 : 1; }

// The saved selection, read once at start; nullopt when none was ever saved.
std::optional<Codec> selected_codec();
// Saves a selection; an error message on failure.
std::optional<std::string> save_selected_codec(Codec codec);

} // namespace nkos::video
