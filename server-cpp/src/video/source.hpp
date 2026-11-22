#pragma once

// The device's H.264/H.265 stream, shared by every viewer (Direct, WebRTC, RustDesk): one encoder session runs
// while anyone subscribes, at the device codec. A subscriber gets every frame from its first key frame on; when the
// session ends under it (the codec was changed, H.264 can no longer encode the picture) it is told and must
// subscribe again. As the Go server's service/stream/video_source.go; everything runs on the loop thread, so a
// frame goes from the capture slot to the viewers' queues without a hand-over.

#include "video/encoder.hpp"
#include "video/frame.hpp"

#include <optional>
#include <string>

namespace nkos::video {

struct Subscriber {
	virtual ~Subscriber() = default;
	virtual void frame(const Frame &frame) = 0;
	// The session ended; the subscription is gone.
	virtual void ended() = 0;
};

struct SubscribeError {
	std::string message;
};

// Joins the stream at codec (nullopt: the device codec, else the running one or the default). An error when the
// codec conflicts with the selected or running one ("video encoder is already in use ...") or H.264 cannot encode
// the picture.
std::optional<SubscribeError> subscribe(Subscriber *subscriber, std::optional<Codec> codec);
void unsubscribe(Subscriber *subscriber);

// The default codec of a viewer that names none.
inline constexpr Codec default_codec = Codec::h265;

// The device codec as streams use it: a selected H.264 that cannot encode the current input reads as H.265.
std::optional<Codec> effective_selected_codec();
// The codec of a running session with subscribers.
std::optional<Codec> active_codec();
// An explicit selection (saved by the caller); a running session at another codec ends.
void select_codec(Codec codec);

// Coalesced key frame request of any viewer.
void request_keyframe();

} // namespace nkos::video
