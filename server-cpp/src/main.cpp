// NanoKVM-Server: the NanoKVM web server, in C++. Same files, API and behaviour as the Go server it replaces.

#include "auth/routes.hpp"
#include "osupdate/apkpkg.hpp"
#include "osupdate/routes.hpp"
#include "recovery/routes.hpp"
#include "bmc/service.hpp"
#include "rustdesk/rustdesk.hpp"
#include "standby/standby.hpp"
#include "system/cli.hpp"
#include "common/log.hpp"
#include "config/config.hpp"
#include "http/router.hpp"
#include "http/server.hpp"
#include "http/static_files.hpp"
#include "input/hid.hpp"
#include "network/network.hpp"
#include "input/keyboard_routes.hpp"
#include "input/region.hpp"
#include "system/atx.hpp"
#include "system/branding.hpp"
#include "system/bluetooth.hpp"
#include "system/cpufreq.hpp"
#include "system/dashboard.hpp"
#include "system/device_settings.hpp"
#include "system/failsafe.hpp"
#include "system/memory.hpp"
#include "system/info.hpp"
#include "system/journal.hpp"
#include "system/logs.hpp"
#include "system/notify.hpp"
#include "system/oled.hpp"
#include "system/live_status.hpp"
#include "system/time_settings.hpp"
#include "system/scripts.hpp"
#include "system/serial_console.hpp"
#include "usb/host_sensors.hpp"
#include "system/startpriority.hpp"
#include "system/ssh_keys.hpp"
#include "system/tls.hpp"
#include "system/terminal.hpp"
#include "usb/composition.hpp"
#include "storage/storage.hpp"
#include "input/jiggler.hpp"
#include "input/ws.hpp"
#include "video/hdmi.hpp"
#include "video/routes.hpp"
#include "video/vision.hpp"
#include "video/preview.hpp"
#include "video/screen_text.hpp"
#include "video/watermark.hpp"
#include "video/audio.hpp"
#include "video/webrtc.hpp"

#include <cerrno>
#include <sys/stat.h>
#include <set>
#include <fstream>
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

int main(int argc, char **argv)
{
	// The SMB reader is this binary started again with --nanokvm-smb-reader: it runs before anything else.
	if (int code = nkos::storage::run_smb_helper_if_requested(argc, argv); code >= 0) return code;
	using namespace nkos;
	// totp-reset USER, factory-reset: command-line modes that exit.
	if (int status = system::run_cli(argc, argv); status >= 0) return status;
	// Ahead of the other services while it starts (every thread created later inherits it).
	system::startpriority::raise();
	const config::Config &conf = config::load();
	log::set_level(conf.logger.level);
	system::logs::init();
	system::logs::capture_native_stderr();

	http::Router router;
	// middleware.SecurityHeaders: every response of the application.
	router.use([](http::Request &, http::Response &res) {
		res.set_header("X-Frame-Options", "DENY");
		res.set_header("Content-Security-Policy", "frame-ancestors 'none'");
		res.set_header("X-Content-Type-Options", "nosniff");
		res.set_header("Referrer-Policy", "no-referrer");
	});
	// middleware.USBHostGate: on a connection that arrived over usb0 the managed computer reaches only the sensors
	// agent's report and the serial console helper, unless it was given access to everything (usb.host_access).
	router.use([](http::Request &req, http::Response &res) {
		auto ingress = http::ingress_interface(req);
		if (!ingress || *ingress != "usb0") return;
		if ((req.method == "POST" && req.path == "/api/host-sensors") ||
		    (req.method == "GET" && req.path == "/nanokvm-serial-console.sh"))
			return;
		struct stat st{};
		bool layout = ::stat("/usr/share/nanokvm/usb-flags-layout", &st) == 0 && ::stat("/etc/kvm/usb/.layout-v1", &st) == 0;
		if (::stat(layout ? "/etc/kvm/usb/usb.host_access" : "/boot/usb.host_access", &st) == 0) return;
		res.status = 403;
		res.done = true;
	});
	// router.go: the standby routes before settings recovery, the body limit and the journal hook.
	standby::add_admission(router);
	standby::add_routes(router);
	// Settings recovery waits for its reboot: only signing in and out and the reboot change anything.
	router.use([](http::Request &req, http::Response &res) {
		if (req.method == "GET" || req.method == "HEAD" || req.method == "OPTIONS") return;
		if (req.path == "/api/vm/system/shutdown" || req.path == "/standby" || req.path.starts_with("/standby/") || req.path == "/standby.js") return;
		static const std::set<std::string> allowed{"/api/system/recovery/reboot", "/api/auth/login", "/api/auth/login/totp",
		                                           "/api/auth/logout", "/api/auth/activity"};
		if (allowed.count(req.path)) return;
		struct stat st{};
		if (::lstat("/var/lib/nanokvm-recovery/pending.json", &st) != 0 && errno == ENOENT) return;
		res.json(409, R"({"code":-1,"msg":"Settings recovery pending; reboot before changing settings."})");
		res.done = true;
	});
	// middleware.LimitRequestBody: 1 MiB, except the routes that take uploads with their own limits (the server
	// reads at most 8 MiB of any body).
	router.use([](http::Request &req, http::Response &res) {
		if (req.body.size() <= (1u << 20)) return;
		if (req.path == "/api/vm/system/shutdown" || req.path == "/standby" || req.path.starts_with("/standby/") || req.path == "/standby.js") return;
		static const std::set<std::string> own{"/api/download/file", "/api/storage/user-disk/files/upload",
		                                       "/api/branding/logo", "/api/branding/favicon",
		                                       "/api/extensions/openvpn/import", "/api/extensions/wireguard/import",
		                                       "/api/vm/script/upload", "/api/vm/video/watermark",
		                                       "/api/picoclaw/actions", "/api/picoclaw/mcp", "/api/picoclaw/load-image"};
		if (own.count(req.path) || req.path.rfind("/api/system/recovery/", 0) == 0)
			return;
		res.json(413, R"({"code":-1,"msg":"request body too large"})");
		res.done = true;
	});
	const std::string web = web_root();
	router.use(http::index_no_store());
	router.use(http::assets(web));
	router.use(http::static_files(web));

	system::journal::watch_changes(router);
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
	// switchOffRemovedAddons, once per system: settings left from add-ons removed before they became part of the OS.
	usb::switch_off_removed_addons(rustdesk::switch_off_stale);
	usb::add_routes(router);
	usb::add_host_sensors_routes(router);
	storage::add_routes(router);
	system::add_info_routes(router);
	system::logs::add_routes(router);
	system::notify::add_routes(router);
	system::terminal::add_routes(router);
	system::scripts::add_routes(router);
	system::serial_console::add_routes(router);
	video::preview::add_routes(router);
	video::screen_text::add_routes(router);
	video::watermark::add_routes(router);
	video::webrtc::add_routes(router);
	video::audio::add_routes(router);
	system::add_memory_routes(router);
	system::add_dashboard_routes(router);
	system::add_device_settings_routes(router);
	system::add_bluetooth_routes(router);
	system::add_oled_routes(router);
	system::add_cpufreq_routes(router);
	osupdate::add_routes(router);
	recovery::add_routes(router);
	system::tls::add_routes(router);
	system::ssh_keys::add_routes(router);
	network::add_routes(router);
	// After every router.use: BMC adds the /redfish middleware; it also starts IPMI.
	bmc::link_host_sensors();
	bmc::add_routes(router);
	rustdesk::add_routes(router);

	// The capture starts before the listeners, as the Go server's main does.
	// Video recovery after repeated crashes: the server runs without video.
	bool recovering = system::failsafe::init();
	system::apply_saved_cpu_frequency();
	// main.go initialize: the IPv6 policy and the udhcpc hooks, before the capture starts.
	network::initialize_ipv6();
	network::ensure_dhcp_integration();
	if (!recovering) {
		system::failsafe::video_phase("initializing");
		if (!video::vision::init()) log::error("video capture is unavailable");
		else video::watermark::apply_at_start();
	}
	network::publish_discovery();
	osupdate::start();
	apkpkg::start();
	system::journal::record_start();
	storage::cleanup_at_start();
	storage::start_downloads();
	// router/network.go: a change left on trial is undone, and the gateway check runs, before the listeners.
	network::restore_pending_trial();
	network::start_gateway_check();
	rustdesk::start();
	// main.go: PrepareServerCertificate before the HTTPS listener; a pair that cannot be prepared ends the start.
	if (auto e = system::tls::prepare(conf)) {
		log::error("prepare HTTPS certificate: {}", *e);
		return 2;
	}
	int status = http::run(conf, router, [] {
		system::tls::start();
		video::hdmi::start();
		if (!system::failsafe::video_disabled()) system::failsafe::video_phase("ready");
		input::hid::start();
		input::jiggler::start();
		system::atx::start();
		usb::start_host_sensors();
		system::start_dashboard();
		system::start_device_settings();
		system::logs::start();
		system::scripts::start_autostart();
		// nanokvm-app waits for this: the server accepts connections.
		if (!std::ofstream("/run/nanokvm/listening")) log::error("cannot mark the server listening");
	}, [] {
		// Still on the loop: peers close before the capture they read is released.
		video::webrtc::stop();
		video::audio::stop();
	});
	rustdesk::stop();
	system::atx::stop();
	input::jiggler::stop();
	input::hid::stop();
	video::vision::deinit();
	system::logs::flush();
	return status;
}
