// NanoKVM-Server: the NanoKVM web server, in C++. Same files, API and behaviour as the Go server it replaces.

#include "auth/routes.hpp"
#include "common/log.hpp"
#include "config/config.hpp"
#include "http/router.hpp"
#include "http/server.hpp"
#include "http/static_files.hpp"
#include "input/hid.hpp"
#include "input/keyboard_routes.hpp"
#include "input/region.hpp"
#include "system/atx.hpp"
#include "system/branding.hpp"
#include "system/failsafe.hpp"
#include "system/live_status.hpp"
#include "system/time_settings.hpp"
#include "usb/composition.hpp"
#include "input/jiggler.hpp"
#include "input/ws.hpp"
#include "video/hdmi.hpp"
#include "video/routes.hpp"
#include "video/vision.hpp"

#include <climits>
#include <string>
#include <unistd.h>

namespace {

// The web UI next to the executable, as the Go server finds it (/kvmapp/server/web).
std::string web_root()
{
	char path[PATH_MAX];
	ssize_t n = readlink("/proc/self/exe", path, sizeof path - 1);
	if (n <= 0) return "/kvmapp/server/web";
	std::string exe(path, static_cast<size_t>(n));
	return exe.substr(0, exe.rfind('/')) + "/web";
}

} // namespace

int main()
{
	using namespace nkos;
	const config::Config &conf = config::load();
	log::set_level(conf.logger.level);

	http::Router router;
	// middleware.SecurityHeaders: every response of the application.
	router.use([](http::Request &, http::Response &res) {
		res.set_header("X-Frame-Options", "DENY");
		res.set_header("Content-Security-Policy", "frame-ancestors 'none'");
		res.set_header("X-Content-Type-Options", "nosniff");
		res.set_header("Referrer-Policy", "no-referrer");
	});
	const std::string web = web_root();
	router.use(http::index_no_store());
	router.use(http::assets(web));
	router.use(http::static_files(web));

	auth::add_routes(router);
	video::add_routes(router);
	input::ws::add_routes(router);
	system::failsafe::add_routes(router);
	system::atx::add_routes(router);
	system::add_live_status_route(router);
	system::add_time_routes(router);
	system::add_branding_routes(router);
	input::add_region_routes(router);
	input::add_keyboard_routes(router);
	input::jiggler::add_routes(router);
	usb::add_routes(router);

	// The capture starts before the listeners, as the Go server's main does.
	// Video recovery after repeated crashes: the server runs without video.
	bool recovering = system::failsafe::init();
	if (!recovering) {
		system::failsafe::video_phase("initializing");
		if (!video::vision::init()) log::error("video capture is unavailable");
	}
	int status = http::run(conf, router, [] {
		video::hdmi::start();
		if (!system::failsafe::video_disabled()) system::failsafe::video_phase("ready");
		input::hid::start();
		input::jiggler::start();
		system::atx::start();
	});
	system::atx::stop();
	input::jiggler::stop();
	input::hid::stop();
	video::vision::deinit();
	return status;
}
