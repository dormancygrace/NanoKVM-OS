// POST /api/vm/screen (one setting) and POST /api/vm/video (a set applied together), as the Go server's
// service/vm/screen.go and video.go: every setting is checked before any changes, then the stream settings apply in
// order and the monitor (EDID) settings make one final state after them, written once. A changed stream rate
// rewrites the EDID when the monitor refresh follows it. Loop thread; the MJPEG chroma and the EDID go through the
// device thread with the stream paused, and requests are served one at a time.

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

#include <climits>
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
constexpr const char *power_cycle_message = "Physical power cycle required after EDID programming; confirm before writing";
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

using Done = std::function<void(std::optional<Failure>, std::string)>;

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

struct MonitorIdentityReq {
	bool custom = false;
	std::string manufacturer;
	std::uint16_t productCode = 0;
	std::string name;
	std::optional<std::uint32_t> serialNumber;
	std::string serialText;
	std::uint8_t week = 0;
	std::uint16_t year = 0;

	// The requested identity, nullopt for NanoKVM's own.
	std::optional<edid::Identity> identity() const
	{
		if (!custom) return std::nullopt;
		return edid::Identity{manufacturer, productCode, name, serialNumber, serialText, week, year};
	}
};

struct VideoSettingsReq {
	bool confirmPowerCycle = false;
	std::optional<int> type, quality, bitRate, mjpegChroma, hevcDirect, height, fps, portraitResolution, monitor;
	std::optional<bool> portrait;
	std::optional<MonitorIdentityReq> monitorIdentity;
	std::optional<std::string> transport;

	monitor::Settings monitor_settings() const
	{
		monitor::Settings m;
		m.portrait = portrait;
		m.sync_refresh = fps.has_value();
		if (monitor) m.resolution = static_cast<std::uint16_t>(*monitor);
		if (portraitResolution) m.portrait_resolution = static_cast<std::uint16_t>(*portraitResolution);
		if (monitorIdentity) {
			m.set_identity = true;
			m.identity = monitorIdentity->identity();
		}
		return m;
	}
};

// The input a batch leads to, when it sets the monitor (batchMonitorInput).
std::optional<std::pair<int, int>> batch_monitor_input(const VideoSettingsReq *batch)
{
	if (!batch || (!batch->portrait && !batch->portraitResolution && !batch->monitor)) return std::nullopt;
	for (const auto *v : {&batch->portraitResolution, &batch->monitor})
		if (*v && (**v < 0 || **v > 65535)) return std::nullopt;
	auto state = monitor::state_after(batch->monitor_settings());
	if (state.portrait) return std::pair<int, int>{monitor::portrait_width(state.portrait_resolution), state.portrait_resolution};
	auto id = state.resolution == 0 ? monitor::auto_height() : state.resolution;
	if (id == 0) return std::nullopt;
	auto [w, h] = monitor::size(id);
	return std::pair<int, int>{w, h};
}

std::optional<Failure> validate(const Setting &s, const VideoSettingsReq *batch, const VideoPool &pool)
{
	// Portrait profiles also write receiver flash on a Cube or Lite: checked before any setting changes.
	if ((s.type == "monitor" || s.type == "portrait" || s.type == "portrait_resolution") && monitor::requires_power_cycle() &&
	    !s.confirm)
		return Failure{-5, power_cycle_message};
	if (s.type == "mjpeg_chroma") {
		if (s.value != 420 && s.value != 422) return Failure{-1, "MJPEG chroma must be 420 or 422"};
	} else if (s.type == "hevc_direct") {
		if (s.value != 0 && s.value != 1) return Failure{-1, "hevc_direct must be 0 or 1"};
	} else if (s.type == "monitor_power_cycle_ack") {
		if (!s.confirm) return Failure{-1, "power cycle confirmation required"};
	} else if (s.type == "portrait") {
		if (s.value != 0 && s.value != 1) return Failure{-1, "portrait must be 0 or 1"};
	} else if (s.type == "portrait_resolution") {
		if (s.value != 1280 && s.value != 1920 && s.value != 2560) return Failure{-1, "unsupported portrait monitor resolution"};
		if (s.value == 2560 && !monitor::portrait_max_supported_for(pool))
			return Failure{-3, "maximum portrait monitor profile is unavailable"};
		if (s.value != 2560 && !monitor::portrait_supported_for(pool)) return Failure{-3, "portrait monitor profile is unavailable"};
	} else if (s.type == "monitor") {
		if (s.value < 0 || s.value > 65535 || !monitor::valid_profile(static_cast<std::uint16_t>(s.value)))
			return Failure{-1, "unsupported monitor profile"};
		auto id = static_cast<std::uint16_t>(s.value);
		if (monitor::fits_video_pool({id, false, monitor::portrait_default}, pool)) {
			auto [w, h] = monitor::size(id);
			std::string memory = monitor::pool_need(id) == "uhd" ? "the UHD" : "the QHD or UHD";
			return Failure{-3, std::format("{}x{} requires {} video memory mode (Settings > Memory)", w, h, memory)};
		}
		if (monitor::wider_profile(id) && !monitor::wider_profile_offered(id))
			return Failure{-3, "this HDMI receiver does not take that 16:10 or 21:9 monitor profile"};
	} else if (s.type == "resolution") {
		if (s.value < 0 || s.value > 2160 || (s.value > 1440 && !pool.uhd())) return Failure{-1, "unsupported stream limit"};
		if (!edid::resolution_map.count(static_cast<std::uint16_t>(s.value))) return Failure{-1, "unsupported stream limit"};
	} else if (s.type == "fps") {
		if (s.value < 10 || s.value > 120) return Failure{-1, "FPS must be between 10 and 120"};
	} else if (s.type == "type") {
		if (s.value < 0 || s.value > 2) return Failure{-1, "stream type must be MJPEG, H.264, or H.265"};
		auto target = batch_monitor_input(batch);
		if (s.value == 0) {
			auto [w, h] = target ? *target : capture_input_size();
			if (std::max(w, h) > mjpeg_max_long_side) return Failure{-3, mjpeg_blocked_message};
		} else if (s.value == 1) {
			std::uint16_t limit = screen().height;
			if (batch && batch->height && *batch->height >= 0 && *batch->height <= 65535)
				limit = static_cast<std::uint16_t>(*batch->height);
			bool allowed;
			if (target) {
				allowed = h264_allowed_for(target->first, target->second, limit);
			} else {
				auto [w, h] = capture_input_size();
				allowed = h264_allowed_for(w, h, limit);
				if (!allowed) {
					auto [tw, th] = monitor_target_size();
					allowed = tw > 0 && th > 0 && h264_allowed_for(tw, th, limit);
				}
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

void apply_monitor(monitor::Settings settings, std::function<void(std::optional<Failure>)> done)
{
	monitor::apply(std::move(settings), screen().fps, [done](std::string error) {
		if (!error.empty()) return done(Failure{-4, error});
		done(std::nullopt);
	});
}

// Applies one validated setting; done gets the failure or the response data of the single-setting route ("" for
// none).
void apply(const Setting &s, Done done)
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
	if (s.type == "monitor_power_cycle_ack") {
		if (auto e = monitor::clear_power_cycle_pending(); !e.empty()) return done(Failure{-4, e}, "");
		return done(std::nullopt, "");
	}
	if (s.type == "portrait" || s.type == "portrait_resolution" || s.type == "monitor") {
		monitor::Settings m;
		if (s.type == "portrait") m.portrait = s.value == 1;
		else if (s.type == "portrait_resolution") m.portrait_resolution = static_cast<std::uint16_t>(s.value);
		else m.resolution = static_cast<std::uint16_t>(s.value);
		return apply_monitor(m, [done](std::optional<Failure> f) { done(f, ""); });
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

bool monitor_setting(const std::string &type)
{
	return type == "monitor" || type == "portrait" || type == "portrait_resolution";
}

// Applies the stream settings of a batch in order (the monitor ones make one state after them), stopping at the
// first failure.
void apply_stream(std::shared_ptr<std::vector<Setting>> list, std::size_t i, std::function<void(std::optional<Failure>)> done)
{
	while (i < list->size() && monitor_setting((*list)[i].type)) i++;
	if (i == list->size()) return done(std::nullopt);
	apply((*list)[i], [list, i, done](std::optional<Failure> f, std::string) {
		if (f) return done(f);
		apply_stream(list, i + 1, done);
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

bool valid_transport(const std::string &t) { return t == "direct" || t == "webrtc" || t == "mjpeg"; }

bool store_transport(const std::string &t)
{
	if (stream_transport() == t && std::ifstream(transport_file)) return true;
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

// A monitor identity in a request: checked before any setting is applied. Like a profile change it writes the EDID.
std::optional<Failure> validate_identity(const VideoSettingsReq &r)
{
	if (!r.monitorIdentity) return std::nullopt;
	if (!monitor::profile_supported()) return Failure{-1, "EDID programming is unsupported on this board/HDMI chip"};
	if (monitor::requires_power_cycle() && !r.confirmPowerCycle) return Failure{-5, power_cycle_message};
	if (auto id = r.monitorIdentity->identity())
		if (auto e = edid::validate(*id)) return Failure{-1, *e};
	return std::nullopt;
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
		// No setting takes a value outside int: one that only fits after narrowing is not a valid one.
		if (value && (*value < INT_MIN || *value > INT_MAX)) return api::err(res, -1, "invalid arguments");
		Setting s{*type, static_cast<int>(value.value_or(0)), confirm.value_or(false)};
		res.pending = true;
		run_serialized([s, id = req.id](std::function<void()> finished) {
			if (auto f = validate(s, nullptr, booted_video_pool())) {
				respond(id, f, "");
				return finished();
			}
			apply(s, [s, id, finished](std::optional<Failure> f, std::string data) {
				if (f || s.type != "fps") {
					respond(id, f, data);
					return finished();
				}
				// The saved EDID refresh follows the stream rate (a new profile only when the rate selects one).
				monitor::Settings sync;
				sync.sync_refresh = true;
				apply_monitor(sync, [id, data, finished](std::optional<Failure> f) {
					respond(id, f, f ? "" : data);
					finished();
				});
			});
		});
	}, admin));

	router.add("POST", "/api/vm/video", auth::guarded([](http::Request &req, http::Response &res) {
		if (!system::failsafe::require_video(res)) return;
		auto r = std::make_shared<VideoSettingsReq>();
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(*r, req.body)) return api::err(res, -1, "invalid arguments");
		if (r->quality && (*r->quality < 1 || *r->quality > 100)) return api::err(res, -1, "MJPEG quality must be 1-100");
		if (r->bitRate && (*r->bitRate <= 100 || *r->bitRate > max_quality))
			return api::err(res, -1, "bitrate must be 101-20000 kbit/s");
		if (r->transport && !valid_transport(*r->transport)) return api::err(res, -1, "transport must be direct, webrtc or mjpeg");
		if (auto f = validate_identity(*r)) return api::err(res, f->code, f->msg);
		auto list = std::make_shared<std::vector<Setting>>();
		auto add = [&](const char *name, const std::optional<int> &v) {
			if (v) list->push_back({name, *v, r->confirmPowerCycle});
		};
		add("type", r->type);
		add("quality", r->quality);
		add("quality", r->bitRate);
		add("mjpeg_chroma", r->mjpegChroma);
		add("hevc_direct", r->hevcDirect);
		add("resolution", r->height);
		add("fps", r->fps);
		add("portrait_resolution", r->portraitResolution);
		if (r->portrait) {
			int v = *r->portrait ? 1 : 0;
			add("portrait", v);
		}
		add("monitor", r->monitor);
		res.pending = true;
		run_serialized([list, r, id = req.id](std::function<void()> finished) {
			auto pool = booted_video_pool();
			for (const auto &s : *list)
				if (auto f = validate(s, r.get(), pool)) {
					respond(id, f, "");
					return finished();
				}
			apply_stream(list, 0, [r, id, finished](std::optional<Failure> f) {
				if (f) {
					respond(id, f, "");
					return finished();
				}
				apply_monitor(r->monitor_settings(), [r, id, finished](std::optional<Failure> f) {
					if (!f && r->transport && !store_transport(*r->transport)) f = Failure{-2, "write transport failed"};
					respond(id, f, f ? "" : video_capabilities_json());
					finished();
				});
			});
		});
	}, admin));
}

} // namespace nkos::video
