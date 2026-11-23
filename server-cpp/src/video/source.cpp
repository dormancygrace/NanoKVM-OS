#include "video/source.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"
#include "video/hdmi.hpp"
#include "video/parameter_sets.hpp"
#include "video/screen.hpp"
#include "video/videogate.hpp"
#include "video/vision.hpp"

#include <atomic>
#include <chrono>
#include <cstdio>
#include <format>
#include <fstream>
#include <set>
#include <vector>

namespace nkos::video {

namespace {

using Clock = std::chrono::steady_clock;

struct Subscription {
	Subscriber *subscriber;
	bool waiting_for_keyframe = true;  // the shared encoder may be in the middle of a GOP
};

struct Session {
	Codec codec;
	bool h265_fallback = false;  // H.265 because the selected H.264 could not encode the picture; kept until it ends
	Clock::time_point started, refreshed, last_keyframe_request;
	vision::CaptureParams params;
	Screen screen;
	std::shared_ptr<hdmi::Lease> lease;  // the capture runs while the session does
	std::shared_ptr<videogate::Hold> gate;  // the video gate, held while the session runs
};

std::optional<Codec> selected = [] {
	auto c = selected_codec();
	return c;
}();
std::optional<Session> session;
std::vector<Subscription> subscribers;
bool keyframe_requested = false;

// Frames and bytes per second of the published stream, averaged over five seconds: /run/nanokvm/now_fps (the main
// stream as the OLED and the dashboard show it), now_kbps and mjpeg_fps.
struct RateWindow {
	std::int64_t counts[5]{};
	int next = 0, filled = 0;
	std::int64_t add(std::int64_t count)
	{
		counts[next] = count;
		next = (next + 1) % 5;
		if (filled < 5) filled++;
		std::int64_t sum = 0;
		for (int i = 0; i < filled; i++) sum += counts[i];
		return (sum + filled / 2) / filled;
	}
};
std::int64_t frame_count = 0, byte_count = 0, mjpeg_count = 0;
bool counting = false;
RateWindow video_frames, mjpeg_frames, video_bytes;

void write_value(const char *path, std::int64_t value)
{
	if (FILE *f = std::fopen(path, "w")) {
		std::fprintf(f, "%lld", static_cast<long long>(value));
		std::fclose(f);
	}
}

void count_rates()
{
	std::int64_t video = video_frames.add(frame_count);
	std::int64_t mjpeg = mjpeg_frames.add(mjpeg_count);
	mjpeg_count = 0;
	std::int64_t kbps = video_bytes.add(byte_count) * 8 / 1000;
	frame_count = byte_count = 0;
	write_value("/run/nanokvm/now_fps", video ? video : mjpeg);
	write_value("/run/nanokvm/mjpeg_fps", mjpeg);
	write_value("/run/nanokvm/now_kbps", kbps);
	loop::after(std::chrono::seconds(1), count_rates);
}

bool h264_blocked(Codec codec)
{
	if (codec != Codec::h264) return false;
	auto [w, h] = capture_input_size();
	return !h264_allowed_for(w, h, screen().height);
}

std::optional<Codec> effective_selection()
{
	if (!selected) return std::nullopt;
	if (*selected != Codec::h264) return selected;
	if (session && session->h265_fallback) return session->codec;
	if (h264_blocked(Codec::h264)) return Codec::h265;
	return selected;
}

int normalized_fps(int fps) { return fps < 1 ? 30 : fps; }

vision::CaptureParams capture_params(Codec codec, const Screen &s)
{
	return {s.width, s.height, s.bitrate, static_cast<std::uint8_t>(native_codec(codec)),
	        static_cast<std::uint8_t>(normalized_fps(s.fps))};
}

void stop_session()
{
	if (!session) return;
	vision::stop();
	session.reset();
}

// Ends the session with every subscriber; they are told after the source is idle, so they may subscribe again.
void end_session()
{
	auto ended = std::move(subscribers);
	subscribers.clear();
	stop_session();
	for (auto &s : ended) s.subscriber->ended();
}

void publish(Frame frame);

void on_frame(Frame frame)
{
	if (!session) return;
	auto now = Clock::now();
	if (keyframe_requested && now - session->last_keyframe_request >= std::chrono::milliseconds(500)) {
		keyframe_requested = false;
		session->last_keyframe_request = now;
		vision::request_keyframe();
	}
	const Screen &current = screen();
	if (!(current == session->screen) || now - session->refreshed >= std::chrono::milliseconds(500)) {
		session->screen = current;
		session->refreshed = now;
		// A picture grown past what H.264 encodes: its viewers reconnect and join the codec that can.
		if (h264_blocked(session->codec)) {
			end_session();
			return;
		}
		session->params = capture_params(session->codec, capture_screen());
		vision::update(session->params);
	}
	frame.duration = std::chrono::microseconds(1'000'000 / session->params.fps);
	frame.timestamp = std::chrono::duration_cast<std::chrono::microseconds>(now - session->started).count();
	publish(std::move(frame));
}

void publish(Frame frame)
{
	if (frame.result >= 0) {
		if (frame.size == 0) return;
		if (frame.key()) fix_parameter_sets(session->codec, true, frame);
	}
	// A subscriber may leave (or end the session) while it takes a frame.
	std::vector<Subscriber *> targets;
	targets.reserve(subscribers.size());
	for (auto &s : subscribers) {
		// Native errors stay visible while a new decoder waits for its first key frame.
		if (s.waiting_for_keyframe && frame.result >= 0) {
			if (!frame.key()) continue;
			s.waiting_for_keyframe = false;
		}
		targets.push_back(s.subscriber);
	}
	for (auto *t : targets) {
		bool still = false;
		for (auto &s : subscribers) still = still || s.subscriber == t;
		if (still) t->frame(frame);
	}
	if (frame.result >= 0) {
		frame_count++;
		byte_count += static_cast<std::int64_t>(frame.size);
		if (!counting) {
			counting = true;
			loop::after(std::chrono::seconds(1), count_rates);
		}
	}
}

// The capture lease and the encoder, once the video gate is held.
bool launch_session()
{
	session->lease = std::make_shared<hdmi::Lease>();
	if (!vision::start(session->params, on_frame)) {
		session.reset();
		return false;
	}
	return true;
}

bool start_session(Codec codec)
{
	check_screen();
	Session s;
	s.codec = codec;
	s.h265_fallback = selected && *selected == Codec::h264 && codec == Codec::h265;
	s.started = s.refreshed = Clock::now();
	s.screen = screen();
	s.params = capture_params(codec, capture_screen());
	session = s;
	// Background OCR gives way, and has ended, before the capture lease can wake the capture and before the encoder
	// takes its memory; until then the session waits with its subscribers.
	session->gate = videogate::hold([] {
		if (session && !session->lease && !launch_session()) end_session();
	});
	if (!session->gate->granted()) return true;
	return launch_session();
}

std::string conflict(Codec active, Codec requested)
{
	return std::format("video encoder is already in use with {{Codec:{}}}; requested {{Codec:{}}}", codec_name(active),
	                   codec_name(requested));
}

} // namespace

std::optional<SubscribeError> subscribe(Subscriber *subscriber, std::optional<Codec> requested)
{
	auto effective = effective_selection();
	Codec codec = default_codec;
	if (requested) codec = *requested;
	else if (effective) codec = *effective;
	else if (session && !subscribers.empty()) codec = session->codec;
	if (effective && *effective != codec) return SubscribeError{conflict(*effective, codec)};
	if (session && session->codec != codec) return SubscribeError{conflict(session->codec, codec)};
	if (h264_blocked(codec)) return SubscribeError{h264_blocked_message};
	if (!session && !start_session(codec)) return SubscribeError{"video capture is unavailable"};
	subscribers.push_back({subscriber, true});
	keyframe_requested = true;
	return std::nullopt;
}

void unsubscribe(Subscriber *subscriber)
{
	std::erase_if(subscribers, [subscriber](const Subscription &s) { return s.subscriber == subscriber; });
	if (subscribers.empty()) stop_session();
}

std::optional<Codec> effective_selected_codec() { return effective_selection(); }

std::optional<Codec> active_codec()
{
	if (!session || subscribers.empty()) return std::nullopt;
	return session->codec;
}

void select_codec(Codec codec)
{
	selected = codec;
	if (session && session->codec != codec) end_session();
}

void request_keyframe() { keyframe_requested = true; }

void count_mjpeg_frame()
{
	mjpeg_count++;
	if (!counting) {
		counting = true;
		loop::after(std::chrono::seconds(1), count_rates);
	}
}

} // namespace nkos::video
