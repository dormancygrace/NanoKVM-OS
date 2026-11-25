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
#include <map>
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
	int setting_kbps = 0;  // the bit rate setting; params.bitrate may be capped below it
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

// The bit rate below the setting while no viewer of the stream takes it cleanly: in a window a viewer lags when it
// lost at least lag_share of the pictures, and keeps up when it lost less than clean_share. A step down (0.75x)
// follows two windows running in which one lagged and none kept up (one viewer keeping up keeps the rate for all; a
// viewer that keeps up only by crowding the others off a shared link loses a few pictures too), a step up (1.5x) a calm
// while without such a window, but for ceiling_hold not above 0.9x the rate the viewers last lagged at. The encoder
// takes a new rate at an IDR (osdrv 0030), so each step asks for one.
struct RateCap {
	static constexpr int min_kbps = 1000;
	static constexpr double lag_share = 0.15, clean_share = 0.02;
	static constexpr auto window = std::chrono::seconds(1), calm = std::chrono::seconds(8),
	                      ceiling_hold = std::chrono::seconds(60), settle = std::chrono::seconds(5);
	int kbps = 0;  // 0: the setting
	std::map<const void *, int> lost;  // pictures each viewer lost in the current window
	int frames = 0;                     // pictures published in the current window
	int lagging_windows = 0;            // windows running in which every viewer lagged
	int ceiling = 0;                    // 0.9x the rate of the last step down, for ceiling_hold
	int setting = 0;                    // the setting the cap applies to
	int viewers = 0;                    // the viewer count of the last window
	Clock::time_point viewers_changed{};  // a viewer joining or leaving: losses there are no lag
	Clock::time_point window_start{}, last_struggle{}, last_change{}, last_down{};
};
RateCap cap;

// The viewers the lag reports cover, or 0 while a subscriber reports none (its viewers keep the rate).
int lag_viewers()
{
	for (auto &s : subscribers)
		if (!s.subscriber->reports_lag()) return 0;
	return hdmi::viewers_of("direct") + hdmi::viewers_of("rtsp");
}

// The encoder module takes a new bit rate at a requested IDR (osdrv 0030). Until a reboot after an update the old
// module may still run: it would never apply a lowered rate, so the rate stays at the setting.
bool encoder_takes_rate()
{
	static const bool takes = [] {
		std::ifstream in("/sys/module/cvi_vc_driver/parameters/nk_idr_bitrate");
		int v = 0;
		return in >> v && v == 1;
	}();
	return takes;
}

int capped_bitrate(int setting, Clock::time_point now)
{
	if (!encoder_takes_rate()) return setting;
	// A new setting starts over from it: a cap below the old one says nothing about the new one.
	if (setting != cap.setting) {
		cap.setting = setting;
		cap.kbps = cap.ceiling = cap.lagging_windows = 0;
	}
	cap.frames++;
	if (cap.window_start == Clock::time_point{}) cap.window_start = now;
	if (now - cap.window_start >= RateCap::window) {
		int viewers = lag_viewers(), lagging = 0;
		if (viewers != cap.viewers) {
			cap.viewers = viewers;
			cap.viewers_changed = now;
		}
		int losing = 0;  // viewers that do not keep up
		for (auto &[viewer, n] : cap.lost) {
			if (n >= RateCap::lag_share * cap.frames) lagging++;
			if (n >= RateCap::clean_share * cap.frames) losing++;
		}
		bool settled = now - cap.viewers_changed >= RateCap::settle;
		// The same condition steps down and holds a step up: a viewer that keeps up beside a slow one (or a WebRTC or
		// RustDesk viewer, which counts as keeping up) lets the rate come back.
		bool struggling = settled && viewers > 0 && lagging > 0 && losing >= viewers;
		if (struggling) cap.last_struggle = now;
		cap.lagging_windows = struggling ? cap.lagging_windows + 1 : 0;
		int current = cap.kbps ? cap.kbps : setting;
		if (cap.lagging_windows >= 2 && current > RateCap::min_kbps) {
			cap.kbps = std::max(RateCap::min_kbps, current * 3 / 4);
			cap.ceiling = current * 9 / 10;
			cap.last_change = cap.last_down = now;
			cap.lagging_windows = 0;
			log::info("video: no viewer keeps up; bit rate {} -> {} kbit/s", current, cap.kbps);
		} else if (cap.kbps && now - cap.last_struggle >= RateCap::calm && now - cap.last_change >= RateCap::calm &&
		           !(now - cap.last_down < RateCap::ceiling_hold && current >= cap.ceiling)) {
			int up = current * 3 / 2;
			if (now - cap.last_down < RateCap::ceiling_hold) up = std::min(up, std::max(cap.ceiling, current));
			cap.kbps = up >= setting ? 0 : up;
			cap.last_change = now;
			log::info("video: viewers keep up; bit rate {} -> {} kbit/s", current, cap.kbps ? cap.kbps : setting);
		}
		cap.lost.clear();
		cap.frames = 0;
		cap.window_start = now;
	}
	return cap.kbps && cap.kbps < setting ? cap.kbps : setting;
}

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
	cap = {};
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
		auto applied = session->params.bitrate;
		session->params = capture_params(session->codec, capture_screen());
		session->setting_kbps = session->params.bitrate;
		session->params.bitrate = static_cast<std::uint16_t>(capped_bitrate(session->setting_kbps, now));
		// The encoder takes a new bit rate at an IDR (osdrv 0030): a changed one asks for it, whoever changed it.
		if (session->params.bitrate != applied) keyframe_requested = true;
		vision::update(session->params);
	} else if (int rate = capped_bitrate(session->setting_kbps, now); rate != session->params.bitrate) {
		session->params.bitrate = static_cast<std::uint16_t>(rate);
		keyframe_requested = true;
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
	s.setting_kbps = s.params.bitrate;
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

void report_lag(const void *viewer) { cap.lost[viewer]++; }

int bitrate_cap() { return session && cap.kbps && cap.kbps < cap.setting ? cap.kbps : 0; }

void count_mjpeg_frame()
{
	mjpeg_count++;
	if (!counting) {
		counting = true;
		loop::after(std::chrono::seconds(1), count_rates);
	}
}

} // namespace nkos::video
