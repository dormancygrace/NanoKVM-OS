// The routes of the Go server's router/rustdesk.go and the background work rustdeskRouter starts.

#include "rustdesk/internal.hpp"

#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "video/audio.hpp"

#include <condition_variable>
#include <thread>

namespace nkos::rustdesk {

namespace {

// The bridge of the Go server (bridge.go, control.go, audio.go, pointer.go, webrtc.go) is not part of this server
// yet: it refuses to open its sockets, so turning RustDesk on fails with this message instead of starting a daemon
// that could not reach the device.
struct UnavailableBridge : Bridge {
	std::string start() override { return "RustDesk is not available in this NanoKVM server"; }
	void stop() override {}
	bool audio_enabled() override { return video::audio::enabled(); }
};

std::mutex monitor_mu;
std::condition_variable monitor_cv;
bool monitor_stop = false;
std::thread monitor;

std::shared_ptr<Bridge> current_bridge()
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	if (!s.bridge) s.bridge = std::make_shared<UnavailableBridge>();
	return s.bridge;
}

// Bridge.Monitor: the bridge's sockets are open while RustDesk is on and closed while it is off.
void monitor_loop()
{
	auto bridge = current_bridge();
	std::string last_error;
	std::unique_lock lock(monitor_mu);
	while (!monitor_stop) {
		lock.unlock();
		Config c;
		if (read_config(c).empty()) {
			if (c.enabled) {
				std::string e = bridge->start();
				if (!e.empty() && e != last_error) log::warn("RustDesk: {}", e);
				last_error = e;
			} else {
				bridge->stop();
				last_error.clear();
			}
		}
		bridge->expire();
		lock.lock();
		monitor_cv.wait_for(lock, std::chrono::seconds(2), [] { return monitor_stop; });
	}
	lock.unlock();
	bridge->stop();
}

void bind_failed(http::Response &res) { network::fail(res, -1, "invalid configuration"); }

} // namespace

void set_bridge(std::shared_ptr<Bridge> bridge)
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	s.bridge = std::move(bridge);
}

void set_usb_hooks(UsbHooks hooks)
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	s.usb = std::move(hooks);
}

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/rustdesk/status", auth::guarded([](http::Request &req, http::Response &res) {
		res.set_header("Cache-Control", "no-store");
		http::defer(req, res, [](http::Response &out) {
			std::string error;
			std::string data = service().status(error);
			if (!error.empty()) network::fail(out, -1, error);
			else network::ok(out, data);
		});
	}, admin));
	router.add("PUT", "/api/rustdesk/config", auth::guarded([](http::Request &req, http::Response &res) {
		// c.ShouldBindJSON: the first JSON value of the body into the configuration.
		auto value = network::parse_json(req.body);
		Config candidate;
		if (!value || !decode_config(*value, candidate).empty()) return bind_failed(res);
		http::defer(req, res, [candidate = std::move(candidate)](http::Response &out) {
			if (auto e = service().configure(candidate); !e.empty()) network::fail(out, -1, e);
			else network::ok(out);
		});
	}, admin));
	router.add("POST", "/api/rustdesk/:action", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [action = req.param("action")](http::Response &out) {
			if (auto e = service().action(action); !e.empty()) network::fail(out, -1, e);
			else network::ok(out);
		});
	}, admin));
}

void start()
{
	current_bridge();
	{
		std::lock_guard lock(monitor_mu);
		monitor_stop = false;
	}
	monitor = std::thread(monitor_loop);
	// Service.AcceptWebRTC.
	loop::blocking([] { service().accept_webrtc(); });
}

void stop()
{
	{
		std::lock_guard lock(monitor_mu);
		monitor_stop = true;
	}
	monitor_cv.notify_all();
	if (monitor.joinable()) monitor.join();
}

} // namespace nkos::rustdesk
