#include "video/screen.hpp"

#include "video/vision.hpp"

#include <algorithm>
#include <charconv>
#include <chrono>
#include <fstream>
#include <map>
#include <cerrno>
#include <optional>
#include <sstream>
#include <sys/stat.h>

namespace nkos::video {

namespace {

using Clock = std::chrono::steady_clock;

std::optional<std::string> read_file(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

std::optional<int> parse_int(std::string text)
{
	auto first = text.find_first_not_of(" \t\r\n");
	if (first == std::string::npos) return std::nullopt;
	auto last = text.find_last_not_of(" \t\r\n");
	text = text.substr(first, last - first + 1);
	// strconv.Atoi takes a leading plus sign; from_chars does not.
	if (text.size() > 1 && text[0] == '+' && text[1] != '-' && text[1] != '+') text.erase(0, 1);
	int value = 0;
	auto [end, ec] = std::from_chars(text.data(), text.data() + text.size(), value);
	if (ec != std::errc() || end != text.data() + text.size()) return std::nullopt;
	return value;
}

int validate_fps(int fps) { return std::clamp(fps, 10, 120); }

void set_value(Screen &target, std::string_view key, int value)
{
	if (key == "resolution") {
		if (value < 0 || value > 65535) return;
		auto height = static_cast<std::uint16_t>(value);
		if (height == 0 || resolution_width(height) != 0) {
			target.width = resolution_width(height);
			target.height = height;
		}
	} else if (key == "quality") {
		if (value >= 1 && value <= 100) target.quality = static_cast<std::uint16_t>(value);
		else if (value > 100 && value <= max_quality_value) target.bitrate = static_cast<std::uint16_t>(value);
	} else if (key == "mjpeg_chroma") {
		if (value == 420 || value == 422) target.mjpeg_chroma = static_cast<std::uint16_t>(value);
	} else if (key == "fps") {
		target.fps = validate_fps(value);
	}
}

void check(Screen &target)
{
	if (target.mjpeg_chroma != 420 && target.mjpeg_chroma != 422) target.mjpeg_chroma = 422;
	if (target.height != 0 && resolution_width(target.height) == 0) target.width = 1920, target.height = 1080;
	if (target.quality < 1 || target.quality > 100) target.quality = 80;
	if (target.bitrate <= 100 || target.bitrate > max_quality_value) target.bitrate = default_bitrate;
}

Screen load()
{
	Screen s;
	const std::pair<const char *, const char *> files[] = {
		{"fps", "/kvmapp/kvm/fps"}, {"mjpeg_chroma", "/kvmapp/kvm/mjpeg_chroma"},
		{"quality", "/kvmapp/kvm/qlty"}, {"resolution", "/kvmapp/kvm/res"}};
	for (auto [key, path] : files)
		if (auto text = read_file(path))
			if (auto value = parse_int(*text)) set_value(s, key, *value);
	check(s);
	return s;
}

Screen &current()
{
	static Screen s = [] {
		Screen initial = load();
		VideoPool pool = booted_video_pool();
		if (initial.height > 1440 && !pool.uhd()) initial.width = 2560, initial.height = 1440;
		if (initial.height > 1080 && !pool.qhd()) initial.width = 1920, initial.height = 1080;
		return initial;
	}();
	return s;
}

struct SourceTiming {
	Clock::time_point expires{};
	int width = 0, height = 0;
} source_timing;

int applied_capture_fps = -1;

bool portrait_profile_input(int width, int height)
{
	return (width == 720 && height == 1280) || (width == 1080 && height == 1920) || (width == 1088 && height == 1920) ||
	       (width == 1440 && height == 2560);
}

// The size native capture encodes for an input and a stream limit (0 is the input size), as stream_size() in
// stream_size.hpp: the input fitted into the limit box, never enlarged; for the portrait profiles the box is
// turned upright. Rounding is left out: it cannot cross the H.264 limits.
std::pair<int, int> h264_output_size(int input_width, int input_height, std::uint16_t stream_height)
{
	int box_width = resolution_width(stream_height), box_height = stream_height;
	if (box_width == 0 || box_height == 0) return {input_width, input_height};
	if (portrait_profile_input(input_width, input_height)) std::swap(box_width, box_height);
	if (input_width <= box_width && input_height <= box_height) return {input_width, input_height};
	if (box_width * input_height <= box_height * input_width) return {box_width, input_height * box_width / input_width};
	return {input_width * box_height / input_height, box_height};
}

// Native stream_size including its portrait rules and rounding.
std::pair<int, int> stream_size(int input_width, int input_height, int box_width, int box_height)
{
	if (input_width <= 0 || input_height <= 0) return {0, 0};
	if (portrait_profile_input(input_width, input_height) && box_width > 0 && box_height > 0) {
		if (input_width == 1088 && input_height == 1920 && box_width >= 1920 && box_height >= 1080)
			return {input_width, input_height};
		int width = box_height;
		if (width * input_height > box_width * input_width) width = box_width * input_width / input_height;
		int mask = input_width == 1088 ? ~63 : ~1;
		width = std::min(width, input_width) & mask;
		return {width, (width * input_height / input_width) & ~1};
	}
	int width = input_width, height = input_height;
	if (box_width > 0 && box_height > 0 && (width > box_width || height > box_height)) {
		if (box_width * height <= box_height * width) height = height * box_width / width, width = box_width;
		else width = width * box_height / height, height = box_height;
	}
	return {width & ~1, height & ~1};
}

} // namespace

std::uint16_t resolution_width(std::uint16_t height)
{
	switch (height) {
	case 2160: return 3840;
	case 1440: return 2560;
	case 1080: return 1920;
	case 720: return 1280;
	case 600: return 800;
	default: return 0;
	}
}

const Screen &screen() { return current(); }

void set_screen(std::string_view key, int value) { set_value(current(), key, value); }

void check_screen() { check(current()); }

int read_video_value(const std::string &path)
{
	auto text = read_file(path);
	if (!text) return 0;
	return parse_int(*text).value_or(0);
}

std::pair<int, int> capture_input_size()
{
	return {read_video_value("/run/nanokvm/width"), read_video_value("/run/nanokvm/height")};
}

int capture_rate_limit(int width, int height)
{
	// The native capture_rate.hpp table: with the video overclock the encoder sustains about 250 Mpix/s.
	struct Tier {
		int long_side, short_side, fps;
	};
	static constexpr Tier tiers[] = {{1280, 720, 120}, {1920, 1088, 100}, {2560, 1440, 60},
	                                 {2560, 1600, 50}, {3440, 1440, 40}, {0, 0, 30}};
	if (width <= 0 || height <= 0) return 120;
	int longer = std::max(width, height), shorter = std::min(width, height);
	for (const auto &t : tiers)
		if (t.long_side == 0 || (longer <= t.long_side && shorter <= t.short_side)) return t.fps;
	return tiers[0].fps;
}

namespace {
std::map<const void *, int> viewer_rates;
}

void set_viewer_rate(const void *viewer, int fps)
{
	if (fps == 0) viewer_rates.erase(viewer);
	else viewer_rates[viewer] = validate_fps(fps);
}

Screen capture_screen()
{
	Screen next = current();
	auto now = Clock::now();
	if (now > source_timing.expires) {
		auto [w, h] = capture_input_size();
		source_timing = {now + std::chrono::seconds(1), w, h};
	}
	int limit = std::min(capture_rate_limit(source_timing.width, source_timing.height),
	                     capture_rate_limit(next.width, next.height));
	for (auto &[viewer, fps] : viewer_rates) limit = std::min(limit, fps);
	if (next.fps > limit) next.fps = limit;
	if (applied_capture_fps != next.fps) {
		applied_capture_fps = next.fps;
		vision::set_capture_fps(next.fps);
	}
	return next;
}

VideoPool booted_video_pool()
{
	static std::optional<VideoPool> cached;
	if (cached) return *cached;
	VideoPool pool;
	std::ifstream in("/proc/device-tree/reserved-memory/ion/size", std::ios::binary);
	unsigned char b[4];
	if (in.read(reinterpret_cast<char *>(b), 4) && in.gcount() == 4 && in.peek() == std::char_traits<char>::eof()) {
		std::uint32_t size = std::uint32_t(b[0]) << 24 | std::uint32_t(b[1]) << 16 | std::uint32_t(b[2]) << 8 | b[3];
		struct stat st{};
		// Any doubt about the property counts as reusable: it only withholds UHD.
		bool reusable = ::stat("/proc/device-tree/reserved-memory/ion/reusable", &st) == 0 || errno != ENOENT;
		pool = {static_cast<int>(size >> 20), reusable};
		cached = pool;
	}
	return pool;
}

bool h264_allowed_for(int input_width, int input_height, std::uint16_t stream_height)
{
	if (input_width <= 0 || input_height <= 0) return true;
	auto [w, h] = h264_output_size(input_width, input_height, stream_height);
	return std::max(w, h) <= 2560 && h <= 1920;
}

bool stream_scaled(int input_width, int input_height, std::uint16_t stream_height)
{
	auto [w, h] = stream_size(input_width, input_height, resolution_width(stream_height), stream_height);
	return w != input_width || h != input_height;
}

} // namespace nkos::video
