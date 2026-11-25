#include "video/settings.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "system/failsafe.hpp"
#include "video/monitor.hpp"
#include "video/screen.hpp"
#include "video/source.hpp"
#include "video/vision.hpp"

#include <glaze/glaze.hpp>

#include <fstream>
#include <sys/stat.h>

namespace nkos::video {

namespace {

constexpr int mjpeg_max_long_side = 2560;

struct ModeCapability {
	std::uint16_t id = 0, height = 0, width = 0;
	std::string aspect;
	std::optional<std::uint16_t> effectiveWidth, effectiveHeight;
	std::vector<int> rates;
	std::vector<std::string> codecs;
	bool available = false;
	std::optional<std::string> reason;
};

struct PortraitCapability {
	std::uint16_t resolution = 0, width = 0;
	int rate = 0;
	std::vector<int> rates;
	std::vector<std::string> codecs, transports;
	bool available = false;
	std::optional<std::string> reason;
};

struct LimitCapability {
	std::uint16_t height = 0, width = 0;
	bool available = false;
	std::optional<std::string> reason;
};

struct RateTier {
	int longSide, shortSide, fps;
};

std::string trim(std::string s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

std::string read_line(const char *path)
{
	std::ifstream in(path);
	std::string s((std::istreambuf_iterator<char>(in)), {});
	return trim(s);
}

std::vector<std::string> mode_codecs(int w, int h)
{
	if (h264_allowed_for(w, h, 0)) return {"h264", "h265"};
	return {"h265"};
}

template <typename T>
std::string json(const T &v)
{
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = true}>(v, out);
	return out;
}

std::string capabilities()
{
	VideoPool pool = booted_video_pool();
	bool qhd = pool.qhd(), uhd = pool.uhd();
	bool programmable = monitor::profile_supported(), live = monitor::high_refresh_supported();
	auto fits = [&](std::uint16_t id) {
		auto need = monitor::pool_need(id);
		return !((need == "uhd" && !uhd) || (need == "qhd" && !qhd));
	};
	auto mode = [&](std::uint16_t id, bool needs_live, bool memory_ok) {
		auto [w, h] = monitor::size(id);
		ModeCapability m{id, h, w, monitor::aspect(id), {}, {}, monitor::rates(id), {}, programmable, {}};
		if (id == 0) {
			auto [ew, eh] = monitor::size(monitor::auto_height());
			m.effectiveWidth = ew ? std::optional<std::uint16_t>(ew) : std::nullopt;
			m.effectiveHeight = eh ? std::optional<std::uint16_t>(eh) : std::nullopt;
			m.codecs = mode_codecs(ew, eh);
		} else {
			m.codecs = mode_codecs(w, h);
		}
		if (!programmable || (needs_live && !live) || (monitor::wider_profile(id) && !monitor::wider_profile_offered(id))) {
			m.available = false;
			m.reason = "receiver";
		} else if (!memory_ok) {
			m.available = false;
			m.reason = "video-memory";
		}
		return m;
	};
	std::vector<ModeCapability> modes = {mode(0, false, true),     mode(2160, true, uhd),     mode(1440, true, qhd),
	                                     mode(1080, false, true),  mode(720, false, true),    mode(1600, false, fits(1600)),
	                                     mode(1200, false, fits(1200)), mode(3440, false, fits(3440)),
	                                     mode(2560, false, fits(2560)), mode(2880, false, fits(2880)),
	                                     mode(1280, false, fits(1280))};
	bool portrait_supported = monitor::portrait_supported();
	auto portrait = [&](std::uint16_t resolution, std::uint16_t width, int rate, std::vector<std::string> codecs,
	                    std::vector<std::string> transports, bool ok) {
		PortraitCapability p{resolution, width, rate, monitor::portrait_rates(resolution), codecs, transports, ok, {}};
		if (!ok) {
			p.reason = "receiver";
			// Only when the current boot image would capture it.
			bool supported = resolution == 2560 ? monitor::portrait_max_supported_for_shared_pool()
			                                    : monitor::portrait_supported_for_shared_pool();
			if (live && programmable && supported) p.reason = "video-memory";
		}
		return p;
	};
	std::vector<std::string> all{"mjpeg", "h264", "h265"}, all_transports{"direct", "webrtc", "mjpeg"};
	std::vector<PortraitCapability> portraits = {
		portrait(1280, 720, 120, all, all_transports, portrait_supported),
		portrait(1920, 1080, 90, all, all_transports, portrait_supported),
		portrait(2560, 1440, 60, {"h265"}, {"direct", "webrtc"}, monitor::portrait_max_supported()),
	};
	auto limit = [](std::uint16_t h, bool ok) {
		LimitCapability l{h, resolution_width(h), ok, {}};
		if (!ok) l.reason = "video-memory";
		return l;
	};
	std::vector<LimitCapability> limits = {limit(0, true), limit(2160, uhd), limit(1440, qhd),
	                                       limit(1080, true), limit(720, true), limit(600, true)};
	std::vector<RateTier> tiers = {{1280, 720, 120}, {1920, 1088, 100}, {2560, 1440, 60},
	                               {2560, 1600, 50}, {3440, 1440, 40}, {0, 0, 30}};
	auto [iw, ih] = capture_input_size();
	bool mjpeg_ok = std::max(iw, ih) <= mjpeg_max_long_side;
	bool h264_ok = h264_allowed_for(iw, ih, screen().height);
	std::string mjpeg = std::format(R"({{"available":{}{},"maxSide":{},"tiles":true}})", mjpeg_ok,
	                                mjpeg_ok ? "" : R"(,"reason":"mjpeg-4k")", mjpeg_max_long_side);
	std::string h264 = std::format(R"({{"available":{}{},"maxLongSide":2560,"maxHeight":1920}})", h264_ok,
	                               h264_ok ? "" : R"(,"reason":"h264-4k")");
	// gin.H: keys in sorted order at every level.
	std::string input = std::format(R"({{"fps":{},"height":{},"maxFps":{},"receiver":{},"width":{}}})",
	                                read_video_value("/run/nanokvm/input_fps"), ih, capture_rate_limit(iw, ih),
	                                api::quote(monitor::receiver_model()), iw);
	std::string portrait_json = std::format(R"({{"enabled":{},"profiles":{},"resolution":{}}})", monitor::portrait_enabled(),
	                                        json(portraits), monitor::portrait_resolution());
	std::string monitor_json = std::format(
		R"({{"aspects":{},"followsStreamRate":{},"identity":{},"modes":{},"portrait":{},"powerCyclePending":{},"programmable":{},"refreshHz":{},"requiresPowerCycle":{},"selected":{},"stockProfile":{}}})",
		json(monitor::aspects), monitor::refresh_follows_stream(), monitor::identity_json(), json(modes), portrait_json,
		monitor::power_cycle_pending(), programmable, monitor::refresh_hz(), monitor::requires_power_cycle(),
		monitor::selected_resolution(), monitor::stock_profile());
	std::string stream = std::format(
		R"({{"h264":{},"limits":{},"maxFps":120,"minFps":10,"mjpeg":{},"rateTiers":{},"transport":{}}})", h264,
		json(limits), mjpeg, json(tiers), api::quote(stream_transport()));
	return std::format(
		R"({{"input":{},"monitor":{},"stream":{},"transports":{{"direct":["h264","h265"],"mjpeg":["mjpeg"],"webrtc":["h264","h265"]}},"videoMemoryMiB":{}}})",
		input, monitor_json, stream, pool.mib);
}

bool file_exists(const char *path)
{
	struct stat st{};
	return ::stat(path, &st) == 0;
}

std::string screen_json()
{
	const Screen &s = screen();
	auto [chroma, fallback] = vision::mjpeg_chroma_status();
	bool encoder_active = active_codec().has_value();
	int video_path = encoder_active && read_video_value("/run/nanokvm/now_fps") != 0 ? read_video_value("/run/nanokvm/video_path") : 0;
	int hevc_direct = read_line("/etc/kvm/hevc_vi_direct") == "0" ? 0 : 1;
	bool hevc_supported = read_line("/sys/module/cvi_vc_driver/parameters/nk_packed_src") == "1";
	bool frame_detect = read_line("/etc/kvm/frame_detect") == "1";
	return std::format(
		R"({{"bitRate":{},"bitRateCap":{},"effectiveFps":{},"fps":{},"frameDetect":{},"height":{},"hevcDirect":{},"hevcDirectSupported":{},"inputHeight":{},"inputWidth":{},"measuredFps":{},"measuredKbps":{},"mjpegChroma":{},"mjpegChromaActive":{},"mjpegChromaFallback":{},"mjpegMeasuredFps":{},"mjpegOutputHeight":{},"mjpegOutputWidth":{},"monitor":{},"monitorHighRefreshSupported":{},"monitorPowerCyclePending":{},"monitorRequiresPowerCycle":{},"monitorSupported":{},"outputHeight":{},"outputWidth":{},"portrait":{},"portraitMaxSupported":{},"portraitResolution":{},"portraitSupported":{},"qhdSupported":{},"quality":{},"uhdSupported":{},"viDirectH264Supported":{},"videoOutputHeight":{},"videoOutputWidth":{},"videoPath":{},"width":{}}})",
		s.bitrate, bitrate_cap(), capture_screen().fps, s.fps, frame_detect, s.height, hevc_direct, hevc_supported,
		read_video_value("/run/nanokvm/height"), read_video_value("/run/nanokvm/width"),
		read_video_value("/run/nanokvm/now_fps"), read_video_value("/run/nanokvm/now_kbps"), s.mjpeg_chroma, chroma,
		api::quote(fallback), read_video_value("/run/nanokvm/mjpeg_fps"), read_video_value("/run/nanokvm/mjpeg_height"),
		read_video_value("/run/nanokvm/mjpeg_width"), monitor::selected_resolution(), monitor::high_refresh_supported(),
		monitor::power_cycle_pending(), monitor::requires_power_cycle(), monitor::profile_supported(),
		read_video_value("/run/nanokvm/stream_height"), read_video_value("/run/nanokvm/stream_width"),
		monitor::portrait_enabled(), monitor::portrait_max_supported(), monitor::portrait_resolution(),
		monitor::portrait_supported(), booted_video_pool().qhd(), s.quality, booted_video_pool().uhd(),
		file_exists("/sys/module/cv181x_vi/parameters/nk_yuv420"), read_video_value("/run/nanokvm/video_height"),
		read_video_value("/run/nanokvm/video_width"), video_path, s.width);
}

} // namespace

std::string video_capabilities_json() { return capabilities(); }

std::string stream_transport()
{
	std::string v = read_line("/kvmapp/kvm/transport");
	return v == "direct" || v == "webrtc" || v == "mjpeg" ? v : "direct";
}

void add_settings_routes(http::Router &router)
{
	router.add("GET", "/api/vm/video/capabilities", auth::guarded([](http::Request &, http::Response &res) {
		api::send(res, 0, "success", capabilities());
	}));
	router.add("GET", "/api/vm/screen", auth::guarded([](http::Request &, http::Response &res) {
		api::send(res, 0, "success", screen_json());
	}));
	add_setter_routes(router);
}

} // namespace nkos::video
