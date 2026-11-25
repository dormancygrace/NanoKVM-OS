#include "video/capture_status.hpp"

#include "common/time.hpp"

#include <glaze/glaze.hpp>

#include <format>
#include <fstream>
#include <map>
#include <sstream>

namespace nkos::video::capture_status {

namespace {

struct Status {
	bool ok = false;
	int result = 0;
	std::string message;
	std::string mode;
	std::string severity;
	std::string updatedAt;
	// With -6, an input refused for the video memory of this boot, which a restart into the updated kernel fixes.
	std::optional<int> inputWidth;
	std::optional<int> inputHeight;
	std::optional<bool> videoMemory;
};

std::map<std::string, Status> latest_by_mode;
std::vector<Subscriber> subscribers;

std::pair<int, int> rejected_input()
{
	std::ifstream in("/run/nanokvm/rejected_input");
	int w = 0, h = 0;
	if (!(in >> w >> h) || w <= 0 || h <= 0 || w > 65535 || h > 65535) return {0, 0};
	return {w, h};
}

std::pair<const char *, const char *> message_of(int result)
{
	switch (result) {
	case -8: return {"MJPEG is unavailable while the HDMI input is 3840x2160: use H.264 or H.265", "error"};
	case -7: return {"HDMI input resolution error", "error"};
	case -6: return {"Unsupported HDMI resolution", "error"};
	case -5: return {"Retrieving image", "warning"};
	case -4: return {"Changing image resolution", "warning"};
	case -3: return {"Image buffer full", "error"};
	case -2: return {"Encoder error", "error"};
	case -1: return {"No image captured", "error"};
	default: return result < 0 ? std::pair{"Capture failed", "error"} : std::pair{"", ""};
	}
}

bool same(const Status &a, const Status &b)
{
	if (a.ok && b.ok) return true;
	return a.ok == b.ok && a.result == b.result && a.mode == b.mode && a.inputWidth.value_or(0) == b.inputWidth.value_or(0) &&
	       a.inputHeight.value_or(0) == b.inputHeight.value_or(0) && a.videoMemory.value_or(false) == b.videoMemory.value_or(false);
}

std::string json(const Status &s)
{
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = true}>(s, out);
	return out;
}

} // namespace

void update(const std::string &mode, int result)
{
	auto [message, severity] = message_of(result);
	Status next{result >= 0, result, message, mode, severity, rfc3339nano(WallClock::now())};
	if (result == -6) {
		auto [w, h] = rejected_input();
		if (w > 0) {
			next.inputWidth = w;
			next.inputHeight = h;
			next.videoMemory = true;
			next.message = std::format("The HDMI input {}x{} needs more video memory: restart the device to use the updated kernel", w, h);
		}
	}
	auto it = latest_by_mode.find(mode);
	if (it != latest_by_mode.end() && same(it->second, next)) return;
	latest_by_mode[mode] = next;
	std::string text = json(next);
	for (auto &s : subscribers) s(text);
}

std::vector<std::string> latest()
{
	std::vector<std::string> out;
	for (auto &[mode, s] : latest_by_mode) out.push_back(json(s));
	return out;
}

void subscribe(Subscriber subscriber) { subscribers.push_back(std::move(subscriber)); }

} // namespace nkos::video::capture_status
