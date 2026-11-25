#pragma once

// USB audio, as the Go server's service/stream/audio: the computer's sound through the gadget's UAC1 function,
// captured and Opus-encoded (20 ms stereo packets at 48 kHz) by /kvmapp/system/bin/usb-audio-capture, shared by
// every listener (WebRTC peers' audio tracks and GET /api/stream/audio, a WebRTC connection of its own). Loop thread.

#include "http/router.hpp"

#include <chrono>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <string>

namespace nkos::video::audio {

inline constexpr int sample_rate = 48000;
inline constexpr int channels = 2;
inline constexpr std::chrono::milliseconds frame_duration{20};
inline constexpr const char *opus_fmtp = "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1;maxaveragebitrate=192000";

struct Packet {
	std::shared_ptr<const std::string> data;
	std::uint64_t index = 0;
	std::chrono::steady_clock::time_point at;
};

// The gadget has the audio function (it is never added here).
bool enabled();

// A listener of the shared capture; packets come on the loop thread. ended runs when the capture stops for good (a
// USB rebind, audio disabled). Destroying the listener leaves.
class Listener {
public:
	virtual ~Listener();
	std::function<void(const Packet &)> packet;
	std::function<void()> ended;
};
// nullptr with error set: audio disabled, too many listeners, a capture restarting.
std::unique_ptr<Listener> listen(std::string &error);

// The browser listener slots (8), shared by peers' audio tracks and the standalone connection.
bool acquire_session();
void release_session();

// The RTP clock skip for a pause: elapsed since the previous packet, minus the frames it covers (dropped + 1);
// shorter gaps are capture jitter.
std::chrono::nanoseconds pause_gap(std::chrono::nanoseconds elapsed, std::uint64_t dropped);

// Releases ALSA before a USB rebind (any change of the gadget's functions).
void stop();

// GET /api/stream/audio.
void add_routes(http::Router &router);

} // namespace nkos::video::audio
