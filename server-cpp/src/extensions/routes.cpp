#include "extensions/routes.hpp"

#include "auth/session.hpp"
#include "extensions/acme.hpp"
#include "extensions/netbird.hpp"
#include "extensions/netsvc.hpp"
#include "extensions/openvpn.hpp"
#include "extensions/tailscale.hpp"
#include "extensions/vpn.hpp"
#include "extensions/wireguard.hpp"
#include "extensions/zerotier.hpp"
#include "network/util.hpp"

namespace nkos::extensions {

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	// Every route runs on a thread of its own: they run commands, wait for package operations and take the
	// services' locks.
	// The request as the route needs it, read on the loop thread (headers come from the connection).
	struct In {
		network::RequestData data;
		bool light = false;
	};
	auto add = [&](const char *method, const std::string &path, std::function<Reply(const In &)> fn) {
		router.add(method, "/api/extensions" + path, auth::guarded([fn](http::Request &req, http::Response &res) {
			auto in = std::make_shared<In>(In{network::request_data(req), req.query_value("light") == "1"});
			answer_later(req, res, [fn, in] { return fn(*in); });
		}, admin));
	};
	auto simple = [&](const char *method, const std::string &path, Reply (*fn)()) {
		add(method, path, [fn](const In &) { return fn(); });
	};

	simple("GET", "/vpn/versions", vpn::versions);
	simple("GET", "/openvpn/status", openvpn::get_status);
	add("POST", "/openvpn/import", [](const In &in) { return openvpn::import(in.data.content_type, in.data.body); });
	add("POST", "/openvpn/profile", [](const In &in) { return openvpn::change(in.data.body); });
	simple("GET", "/wireguard/status", wireguard::get_status);
	add("POST", "/wireguard/import", [](const In &in) { return wireguard::import(in.data.content_type, in.data.body); });
	add("POST", "/wireguard/profile", [](const In &in) { return wireguard::change(in.data.body); });

	// Settings > Services: LLDP, SNMP and UPS, each off until it is switched on.
	for (std::string name : {"lldp", "snmp", "ups"}) {
		add("GET", "/" + name + "/status", [name](const In &in) { return netsvc::status(name, in.light); });
		add("PUT", "/" + name + "/enabled", [name](const In &in) { return netsvc::set_enabled(name, in.data); });
		add("PUT", "/" + name + "/settings", [name](const In &in) { return netsvc::settings(name, in.data); });
		add("POST", "/" + name + "/reset", [name](const In &) { return netsvc::reset(name); });
	}

	simple("POST", "/tailscale/install", tailscale::install);
	add("POST", "/tailscale/uninstall", [](const In &in) { return tailscale::uninstall(in.data.body); });
	// The address the connection arrived at is read on the loop thread, before the status runs on its own.
	router.add("GET", "/api/extensions/tailscale/status", auth::guarded([](http::Request &req, http::Response &res) {
		auto local = network::local_address(req);
		answer_later(req, res, [local] { return tailscale::get_status(local); });
	}, admin));
	simple("POST", "/tailscale/up", tailscale::up);
	simple("POST", "/tailscale/down", tailscale::down);
	simple("POST", "/tailscale/login", tailscale::login);
	simple("POST", "/tailscale/logout", tailscale::logout);
	simple("POST", "/tailscale/start", tailscale::start);
	simple("POST", "/tailscale/stop", tailscale::stop);
	simple("POST", "/tailscale/restart", tailscale::restart);

	simple("POST", "/netbird/install", netbird::install);
	add("POST", "/netbird/uninstall", [](const In &in) { return netbird::uninstall(in.data.body); });
	simple("GET", "/netbird/status", netbird::get_status);
	simple("POST", "/netbird/login", netbird::login);
	simple("POST", "/netbird/down", netbird::down);
	simple("POST", "/netbird/start", netbird::start);
	simple("POST", "/netbird/stop", netbird::stop);
	simple("POST", "/netbird/restart", netbird::restart);

	simple("POST", "/zerotier/install", zerotier::install);
	add("POST", "/zerotier/uninstall", [](const In &in) { return zerotier::uninstall(in.data.body); });
	simple("GET", "/zerotier/status", zerotier::get_status);
	simple("POST", "/zerotier/start", zerotier::start);
	simple("POST", "/zerotier/stop", zerotier::stop);
	simple("POST", "/zerotier/restart", zerotier::restart);
	add("POST", "/zerotier/join", [](const In &in) { return zerotier::join(in.data.body); });    // join a network by its 16-digit ID
	add("POST", "/zerotier/leave", [](const In &in) { return zerotier::leave(in.data.body); });  // leave a joined network

	simple("GET", "/acme/status", acme::get_status);        // acme.sh, settings, certificate, last run
	simple("GET", "/acme/providers", acme::get_providers);  // installed DNS hooks and their variables
	simple("POST", "/acme/install", acme::install);         // install or update to the latest stable release
	simple("POST", "/acme/check", acme::check_update);      // ask GitHub for the latest stable release
	simple("POST", "/acme/uninstall", acme::uninstall);     // remove acme.sh, account and certificate
	add("POST", "/acme/issue", [](const In &in) { return acme::issue(in.data.body); });  // issue (background job)
	simple("POST", "/acme/renew", acme::renew);             // renew now (background job)
	simple("POST", "/acme/remove", acme::remove);           // stop renewing, back to the generated pair

	// The HTTP-01 tokens, for the CA, on the HTTPS listener and in plain HTTP mode; the redirect listener serves
	// the same (acme::challenge).
	router.add("GET", std::string(acme::challenge_prefix) + ":token", [](http::Request &req, http::Response &res) { acme::challenge(req, res); });
}

void start()
{
	openvpn::start();
	wireguard::start();
	acme::start();
}

} // namespace nkos::extensions
