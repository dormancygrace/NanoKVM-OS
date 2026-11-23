// POST /api/vm/screen (one setting) and POST /api/vm/video (a set applied together), as the Go server's
// service/vm/screen.go and video.go: every setting is checked before any changes, then they apply in order. Loop
// thread; the MJPEG chroma goes through the device thread with the stream paused, and requests are served one at a
// time.
//
// Not yet here: the monitor settings (monitor, portrait, portrait_resolution, monitor_power_cycle_ack,
// monitorIdentity) and the EDID refresh that follows a changed frame rate; they write the HDMI receiver's EDID.

#include "video/settings.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"
#include "system/failsafe.hpp"
#include "video/monitor.hpp"
#include "video/screen.hpp"
#include "video/vision.hpp"

#include <glaze/glaze.hpp>

#include <deque>
#include <format>
#include <fstream>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace nkos::video {

namespace {

constexpr int max_quality = 20000;
constexpr int mjpeg_max_long_side = 2560;
constexpr const char *mjpeg_blocked_message =
	"MJPEG is unavailable while the HDMI input is 3840x2160: use H.264 or H.265";
constexpr const char *h264_blocked_message =
	"H.264 takes at most 2560x1440 (1080x1920 in portrait): use H.265 or a stream resolution of 1440 or lower "
	"(1080 or lower in portrait)";
constexpr const char *monitor_unavailable = "monitor (EDID) settings are not available in this build yet";
constexpr const char *transport_file = "/kvmapp/kvm/transport";
constexpr const char *hevc_direct_file = "/etc/kvm/hevc_vi_direct";

struct Setting {
	std::string type;
	int value = 0;
	bool confirm = false;
};

struct Failure {
	int code;
	std::string msg;
};

bool monitor_setting(const std::string &type)
{
	return type == "monitor" || type == "portrait" || type == "portrait_resolution" || type == "monitor_power_cycle_ack";
}

const char *screen_file(const std::string &type)
{
	if (type == "type") return "/kvmapp/kvm/type";
	if (type == "fps") return "/kvmapp/kvm/fps";
	if (type == "mjpeg_chroma") return "/kvmapp/kvm/mjpeg_chroma";
	if (type == "quality") return "/kvmapp/kvm/qlty";
	if (type == "resolution") return "/kvmapp/kvm/res";
	if (type == "hevc_direct") return hevc_direct_file;
	return nullptr;
}

bool write_screen(const std::string &type, const std::string &value)
{
	const char *file = screen_file(type);
	if (!file) return false;
	std::ofstream out(file, std::ios::trunc);
	if (!(out << value)) {
		log::error("write kvm {} failed", file);
		return false;
	}
	return true;
}

int hevc_direct()
{
	std::ifstream in(hevc_direct_file);
	std::string v;
	return in >> v && v == "0" ? 0 : 1;
}

// The input the saved monitor setting asks the source for (MonitorTargetSize): 0x0 when the receiver cannot be
// programmed or Auto is the stock profile.
std::pair<int, int> monitor_target_size()
{
	if (!monitor::profile_supported()) return {0, 0};
	if (monitor::portrait_enabled()) {
		auto r = monitor::portrait_resolution();
		return {monitor::portrait_width(r), r};
	}
	auto id = monitor::selected_resolution();
	if (id == 0) id = monitor::auto_height();
	auto [w, h] = monitor::size(id);
	return {w, h};
}

std::optional<Failure> validate(const Setting &s, std::optional<int> batch_height)
{
	if (monitor_setting(s.type)) return Failure{-4, monitor_unavailable};
	const auto pool = booted_video_pool();
	if (s.type == "mjpeg_chroma") {
		if (s.value != 420 && s.value != 422) return Failure{-1, "MJPEG chroma must be 420 or 422"};
	} else if (s.type == "hevc_direct") {
		if (s.value != 0 && s.value != 1) return Failure{-1, "hevc_direct must be 0 or 1"};
	} else if (s.type == "resolution") {
		if (s.value < 0 || s.value > 2160 || (s.value > 1440 && !pool.uhd()))
			return Failure{-1, "unsupported stream limit"};
		if (s.value != 0 && resolution_width(static_cast<std::uint16_t>(s.value)) == 0)
			return Failure{-1, "unsupported stream limit"};
	} else if (s.type == "fps") {
		if (s.value < 10 || s.value > 120) return Failure{-1, "FPS must be between 10 and 120"};
	} else if (s.type == "type") {
		if (s.value < 0 || s.value > 2) return Failure{-1, "stream type must be MJPEG, H.264, or H.265"};
		auto [w, h] = capture_input_size();
		if (s.value == 0 && std::max(w, h) > mjpeg_max_long_side) return Failure{-3, mjpeg_blocked_message};
		if (s.value == 1) {
			std::uint16_t limit = screen().height;
			if (batch_height && *batch_height >= 0 && *batch_height <= 65535) limit = static_cast<std::uint16_t>(*batch_height);
			bool allowed = h264_allowed_for(w, h, limit);
			if (!allowed) {
				auto [tw, th] = monitor_target_size();
				allowed = tw > 0 && th > 0 && h264_allowed_for(tw, th, limit);
			}
			if (!allowed) return Failure{-3, h264_blocked_message};
		}
	} else if (s.type == "quality") {
		if (s.value < 1 || s.value > max_quality) return Failure{-1, "quality must be 1-100, or a bitrate up to 20000 kbit/s"};
	} else {
		return Failure{-1, "unknown screen setting"};
	}
	return std::nullopt;
}

// Applies one validated setting; done gets the failure or the response data of the single-setting route ("" for
// none).
void apply(const Setting &s, std::function<void(std::optional<Failure>, std::string)> done)
{
	if (s.type == "mjpeg_chroma") {
		std::uint16_t previous = screen().mjpeg_chroma;
		vision::set_mjpeg_chroma(static_cast<std::uint16_t>(s.value), [s, previous, done](int result) {
			if (result != 0) return done(Failure{-4, "cannot apply MJPEG chroma"}, "");
			if (!write_screen(s.type, std::to_string(s.value))) {
				vision::set_mjpeg_chroma(previous, [](int) {});
				return done(Failure{-2, "update screen failed"}, "");
			}
			set_screen(s.type, s.value);
			auto [active, reason] = vision::mjpeg_chroma_status();
			done(std::nullopt, std::format(R"({{"mjpegChroma":{},"mjpegChromaActive":{},"mjpegChromaFallback":{}}})",
			                               s.value, active, api::quote(reason)));
		});
		return;
	}
	if (s.type == "hevc_direct") {
		// libkvm follows the file within a second.
		if (!write_screen(s.type, std::to_string(s.value))) return done(Failure{-2, "update screen failed"}, "");
		return done(std::nullopt, std::format(R"({{"hevcDirect":{}}})", hevc_direct()));
	}
	bool ok = s.type == "type" ? write_screen("type", std::vector<std::string>{"mjpeg", "h264", "h265"}[static_cast<std::size_t>(s.value)])
	                           : write_screen(s.type, std::to_string(s.value));
	if (!ok) return done(Failure{-2, "update screen failed"}, "");
	set_screen(s.type, s.value);
	log::debug("update screen: {} {}", s.type, s.value);
	if (s.type == "fps") return done(std::nullopt, std::format(R"({{"fps":{}}})", screen().fps));
	done(std::nullopt, "");
}

// Applies settings in order, stopping at the first failure.
void apply_all(std::shared_ptr<std::vector<Setting>> list, std::size_t i,
               std::function<void(std::optional<Failure>, std::string)> done)
{
	if (i == list->size()) return done(std::nullopt, "");
	apply((*list)[i], [list, i, done](std::optional<Failure> f, std::string data) {
		if (f || i + 1 == list->size()) return done(f, data);
		apply_all(list, i + 1, done);
	});
}

// One request at a time (Go's videoSettingsMutex): the others wait their turn.
bool busy = false;
std::deque<std::function<void()>> waiting;

void run_serialized(std::function<void(std::function<void()> finished)> job)
{
	auto finished = [] {
		busy = false;
		if (waiting.empty()) return;
		auto next = std::move(waiting.front());
		waiting.pop_front();
		next();
	};
	auto start = [job = std::move(job), finished] {
		busy = true;
		job(finished);
	};
	if (busy) waiting.push_back(std::move(start));
	else start();
}

struct VideoSettingsReq {
	bool confirmPowerCycle = false;
	std::optional<int> type, quality, bitRate, mjpegChroma, hevcDirect, height, fps, portraitResolution, monitor;
	std::optional<bool> portrait;
	std::optional<glz::raw_json> monitorIdentity;
	std::optional<std::string> transport;
};

bool valid_transport(const std::string &t) { return t == "direct" || t == "webrtc" || t == "mjpeg"; }

bool store_transport(const std::string &t)
{
	if (stream_transport() == t) {
		std::ifstream in(transport_file);
		if (in) return true;
	}
	return config::atomic_write(transport_file, t + "\n", 0644);
}

void respond(std::uint64_t id, std::optional<Failure> f, std::string data)
{
	http::Response out;
	if (f) api::err(out, f->code, f->msg);
	else if (data.empty()) api::ok(out);
	else api::send(out, 0, "success", data);
	http::complete(id, std::move(out));
}

} // namespace

void add_setter_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("POST", "/api/vm/screen", auth::guarded([](http::Request &req, http::Response &res) {
		if (!system::failsafe::require_video(res)) return;
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> type;
		std::optional<long long> value;
		std::optional<bool> confirm;
		if (params) {
			type = params->string("type", ok);
			value = params->integer("value", ok);
			confirm = params->boolean("confirmPowerCycle", ok);
		}
		if (!params || !ok || !type || type->empty()) return api::err(res, -1, "invalid arguments");
		Setting s{*type, static_cast<int>(value.value_or(0)), confirm.value_or(false)};
		res.pending = true;
		run_serialized([s, id = req.id](std::function<void()> finished) {
			if (auto f = validate(s, std::nullopt)) {
				respond(id, f, "");
				return finished();
			}
			apply(s, [id, finished](std::optional<Failure> f, std::string data) {
				respond(id, f, data);
				finished();
			});
		});
	}, admin));

	router.add("POST", "/api/vm/video", auth::guarded([](http::Request &req, http::Response &res) {
		if (!system::failsafe::require_video(res)) return;
		VideoSettingsReq r;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body)) return api::err(res, -1, "invalid arguments");
		if (r.quality && (*r.quality < 1 || *r.quality > 100)) return api::err(res, -1, "MJPEG quality must be 1-100");
		if (r.bitRate && (*r.bitRate <= 100 || *r.bitRate > max_quality))
			return api::err(res, -1, "bitrate must be 101-20000 kbit/s");
		if (r.transport && !valid_transport(*r.transport)) return api::err(res, -1, "transport must be direct, webrtc or mjpeg");
		if (r.monitorIdentity || r.monitor || r.portrait || r.portraitResolution) return api::err(res, -4, monitor_unavailable);
		auto list = std::make_shared<std::vector<Setting>>();
		auto add = [&](const char *name, const std::optional<int> &v) {
			if (v) list->push_back({name, *v, r.confirmPowerCycle});
		};
		add("type", r.type);
		add("quality", r.quality);
		add("quality", r.bitRate);
		add("mjpeg_chroma", r.mjpegChroma);
		add("hevc_direct", r.hevcDirect);
		add("resolution", r.height);
		add("fps", r.fps);
		res.pending = true;
		run_serialized([list, height = r.height, transport = r.transport, id = req.id](std::function<void()> finished) {
			for (const auto &s : *list)
				if (auto f = validate(s, height)) {
					respond(id, f, "");
					return finished();
				}
			apply_all(list, 0, [id, transport, finished](std::optional<Failure> f, std::string) {
				if (!f && transport && !store_transport(*transport)) f = Failure{-2, "write transport failed"};
				respond(id, f, f ? "" : video_capabilities_json());
				finished();
			});
		});
	}, admin));
}

} // namespace nkos::video
