// NanoKVM-Server: the NanoKVM web server, in C++. Same files, API and behaviour as the Go server it replaces.

#include "auth/routes.hpp"
#include "osupdate/apkpkg.hpp"
#include "osupdate/routes.hpp"
#include "recovery/routes.hpp"
#include "notify/notify.hpp"
#include "ai/routes.hpp"
#include "extensions/acme.hpp"
#include "extensions/routes.hpp"
#include "auth/session.hpp"
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
#include "system/diagnostics.hpp"
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
#include "usb/remote_access.hpp"
#include "storage/storage.hpp"
#include "input/jiggler.hpp"
#include "input/ws.hpp"
#include "video/hdmi.hpp"
#include "video/monitor.hpp"
#include "video/routes.hpp"
#include "video/rtsp.hpp"
#include "video/vision.hpp"
#include "video/preview.hpp"
#include "video/screen_text.hpp"
#include "video/screen_watch.hpp"
#include "video/watermark.hpp"
#include "video/audio.hpp"
#include "video/webrtc.hpp"

#include "common/loop.hpp"
#include "common/process.hpp"

#include <cerrno>
#include <cstring>
#include <future>
#include <memory>
#include <sys/stat.h>
#include <set>
#include <fstream>
#include <climits>
#include <string>
#include <unistd.h>
#include <vector>

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

char **program_arguments;

// Replaces the process with a fresh start once the saved monitor profile was lowered (restartAfterMonitorFit), so
// the capture detects the HDMI input again; the new process carries NKOS_MONITOR_FIT_RESTARTED and does not restart
// again. The vendor media libraries open their devices without close-on-exec, so their descriptors would survive the
// exec and the drivers would keep this process's VPSS group, VI pipe and video buffers next to the new set: the
// capture is released first (with the device thread held still, as on a stop) and every other descriptor closes on
// exec. Loop thread; returns only when the exec failed.
void restart_after_monitor_fit()
{
	using namespace nkos;
	log::info("restarting to re-detect the HDMI input after lowering the monitor profile");
	auto parked = std::make_shared<std::promise<void>>();
	auto release = std::make_shared<std::promise<void>>();
	std::shared_future<void> resume = release->get_future().share();
	std::future<void> held = parked->get_future();
	loop::device([parked, resume] {
		parked->set_value();
		resume.wait();
	});
	held.wait();
	video::vision::deinit();
	char path[PATH_MAX];
	ssize_t n = ::readlink("/proc/self/exe", path, sizeof path - 1);
	if (n > 0) {
		path[n] = 0;
		std::vector<char *> env;
		for (char **e = environ; *e; e++) env.push_back(*e);
		std::string marker = "NKOS_MONITOR_FIT_RESTARTED=1";
		env.push_back(marker.data());
		env.push_back(nullptr);
		// The stderr pipe's reader does not survive the exec.
		system::logs::restore_native_stderr();
		process::cloexec_inherited();
		::execve(path, program_arguments, env.data());
	}
	log::error("restart after lowering the monitor profile failed: {}", std::strerror(errno));
	release->set_value();
}

} // namespace

int main(int argc, char **argv)
{
	// The SMB reader is this binary started again with --nanokvm-smb-reader: it runs before anything else.
	if (int code = nkos::storage::run_smb_helper_if_requested(argc, argv); code >= 0) return code;
	program_arguments = argv;
	using namespace nkos;
	// totp-reset USER, factory-reset: command-line modes that exit.
	if (int status = system::run_cli(argc, argv); status >= 0) return status;
	// Ahead of the other services while it starts (every thread created later inherits it).
	system::startpriority::raise();
	const config::Config &conf = config::load();
	log::set_level(conf.logger.level);
	system::logs::init();
	system::logs::capture_native_stderr();

	// The internal token the AI helpers use on the loopback paths.
	if (!ai::ensure_internal_token()) return 1;
	http::Router router;
	// main.go: without authentication, the origin protections come first.
	if (conf.authentication == "disable") auth::add_unauthenticated_protections(router);
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
	// middleware.LimitRequestBody: 1 MiB, except the routes that take uploads with their own limits, which get the
	// most the server keeps of any body. The server applies the limit while it reads: a longer body is not kept,
	// and the request arrives here marked.
	router.set_body_limit([](const http::Request &req) -> std::size_t {
		static const std::set<std::string> own{"/api/download/file", "/api/storage/user-disk/files/upload",
		                                       "/api/branding/logo", "/api/branding/favicon",
		                                       "/api/extensions/openvpn/import", "/api/extensions/wireguard/import",
		                                       "/api/vm/script/upload", "/api/vm/video/watermark",
		                                       "/api/picoclaw/actions", "/api/picoclaw/mcp", "/api/picoclaw/load-image"};
		if (own.count(req.path) || req.path.rfind("/api/system/recovery/", 0) == 0) return 8u << 20;
		return http::Router::default_body_limit;
	});
	router.use([](http::Request &req, http::Response &res) {
		if (!req.body_over_limit) return;
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
	notify::add_routes(router);
	system::terminal::add_routes(router);
	system::scripts::add_routes(router);
	system::serial_console::add_routes(router);
	video::preview::add_routes(router);
	video::screen_text::add_routes(router);
	video::watermark::add_routes(router);
	video::webrtc::add_routes(router);
	video::audio::add_routes(router);
	system::add_memory_routes(router);
	system::diagnostics::add_routes(router);
	system::add_dashboard_routes(router);
	system::add_device_settings_routes(router);
	system::add_bluetooth_routes(router);
	system::add_oled_routes(router);
	system::add_cpufreq_routes(router);
	osupdate::add_routes(router);
	extensions::add_routes(router);
	recovery::add_routes(router);
	system::tls::add_routes(router);
	system::ssh_keys::add_routes(router);
	ai::add_routes(router);
	network::add_routes(router);
	// After every router.use: BMC adds the /redfish middleware; it also starts IPMI.
	bmc::link_host_sensors();
	bmc::notify_login_locked_out = notify::login_locked_out;
	bmc::notify_login_attempts_limited = notify::login_attempts_limited;
	bmc::notify_login_succeeded = notify::login_succeeded;
	bmc::add_routes(router);
	// The daemon's sockets and the USB preset RustDesk switches to (router/rustdesk.go: NewBridge, NewService).
	rustdesk::set_bridge(rustdesk::make_bridge());
	rustdesk::set_usb_hooks({usb::enable_remote_access_usb, usb::ensure_remote_access_usb, usb::restore_usb_after_remote_access});
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
	extensions::start();
	// The plain-HTTP listener answers the ACME HTTP-01 challenge itself; everything else goes to HTTPS.
	http::on_plain_http([](const http::Request &req, http::Response &res) {
		if (ai::plain_http(req, res)) return true;
		if (!req.path.starts_with(extensions::acme::challenge_prefix)) return false;
		extensions::acme::challenge(req, res);
		return true;
	});
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
		// StartNotifyWatch: the host's power LED and the HDMI signal while capture runs.
		notify::start_host_watch([]() -> std::optional<bool> {
			if (video::hdmi::disabled() || video::hdmi::idle_stopped()) return std::nullopt;
			return video::vision::hdmi_signal_active();
		});
		// StartScreenWatch: crashes on the host's screen, once the capture runs.
		video::screen_watch::start();
		video::rtsp::start();
		if (!system::failsafe::video_disabled()) system::failsafe::video_phase("ready");
		// The monitor profile is kept in the HDMI receiver across restarts and reinstalls: it must not exceed the
		// video memory of this boot, and it must be the saved one after an installation or a write by anything but
		// this server. Programming can take a while (device thread).
		if (!system::failsafe::video_disabled()) video::monitor::start(video::hdmi::disabled(), restart_after_monitor_fit);
		input::hid::start();
		ai::start();
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
		video::rtsp::stop();
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
