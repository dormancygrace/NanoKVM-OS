// The preferred gateway and the Ethernet gateway check, as the Go server's service/network gateway.go and
// gateway_check.go: default-route metrics by preference (ip route), and a probe of the Ethernet gateway every few
// seconds that moves its route below Wi-Fi's while it does not answer and restarts DHCP for a gateway gone silent.

#include "network/internal.hpp"
#include "network/network.hpp"

#include "common/log.hpp"

#include <algorithm>
#include <csignal>
#include <thread>

namespace nkos::network {

namespace {

constexpr int gateway_demoted_metric = 900;
constexpr int gateway_demote_after = 3;   // failed checks in a row
constexpr int gateway_restore_after = 2;  // passed checks in a row
constexpr auto gateway_dhcp_after = std::chrono::minutes(2);
constexpr auto gateway_check_dhcp_holdoff = std::chrono::seconds(60);
constexpr auto gateway_check_interval = std::chrono::seconds(5);

std::uint64_t gateway_check_generation = 0;  // under gateway_mu

bool is_gateway_interface(const std::string &iface) { return iface.rfind("eth", 0) == 0 || iface.rfind("wlan", 0) == 0; }

bool is_demoted_metric(int metric) { return metric >= gateway_demoted_metric && metric < 1000; }

std::vector<std::string> route_args(const char *verb, const GatewayRoute &route, int metric, bool proto)
{
	std::vector<std::string> args{"-4", "route", verb, "default", "via", route.gateway, "dev", route.interface};
	if (proto) args.insert(args.end(), {"proto", "dhcp"});
	args.insert(args.end(), {"metric", std::to_string(metric)});
	return args;
}

Error route_command(const std::vector<std::string> &args)
{
	auto r = gateway_route_command(args);
	return r.ok() ? Error{} : make_error(r.error);
}

Error set_gateway_route(const GatewayRoute &route, int metric) { return route_command(route_args("replace", route, metric, true)); }
// add fails when any default route holds the metric already.
Error add_gateway_route(const GatewayRoute &route, int metric) { return route_command(route_args("add", route, metric, true)); }
Error delete_gateway_route(const GatewayRoute &route, int metric) { return route_command(route_args("del", route, metric, false)); }

bool wifi_connected(const std::vector<GatewayRoute> &routes)
{
	for (const auto &route : routes)
		if (gateway_kind(route.interface) == "wifi" && gateway_carrier(route.interface)) return true;
	return false;
}

int atoi_int(std::string_view s, bool &ok)
{
	auto v = network::atoi(s);
	ok = v && *v >= INT32_MIN && *v <= INT32_MAX;
	return ok ? static_cast<int>(*v) : 0;
}

} // namespace

std::mutex gateway_mu;

std::function<RunResult(const std::vector<std::string> &)> gateway_route_command = [](const std::vector<std::string> &args) {
	std::vector<std::string> argv{"ip"};
	argv.insert(argv.end(), args.begin(), args.end());
	return run(argv);
};

std::function<std::set<int>()> occupied_metrics = [] {
	std::set<int> metrics;
	auto table = read_file(paths.gateway_route_table);
	if (!table) return metrics;
	for (const auto &line : split(*table, "\n")) {
		auto f = fields(line);
		if (f.size() < 8 || f[1] != "00000000" || f[7] != "00000000") continue;
		bool ok = false;
		int metric = atoi_int(f[6], ok);
		if (ok) metrics.insert(metric);
	}
	return metrics;
};

std::string gateway_route_json(const GatewayRoute &r)
{
	std::string out = "{\"interface\":" + quote(r.interface) + ",\"gateway\":" + quote(r.gateway) + ",\"metric\":" + std::to_string(r.metric);
	if (r.reachable) out += std::string(",\"reachable\":") + (*r.reachable ? "true" : "false");
	return out + ",\"demoted\":" + (r.demoted ? "true" : "false") + "}";
}

std::string gateway_routes_json(const std::vector<GatewayRoute> &routes)
{
	std::string out = "[";
	for (std::size_t i = 0; i < routes.size(); i++) out += (i ? "," : "") + gateway_route_json(routes[i]);
	return out + "]";
}

std::string read_gateway_preference()
{
	auto data = read_file(paths.gateway_preference);
	if (!data) return "auto";
	std::string value = trim_space(*data);
	return value == "ethernet" || value == "wifi" || value == "auto" ? value : "auto";
}

Error write_gateway_preference(const std::string &preferred)
{
	if (preferred != "auto" && preferred != "ethernet" && preferred != "wifi") return make_error("invalid gateway preference");
	if (auto err = mkdir_all(dir_name(paths.gateway_preference), 0755)) return wrap("create gateway configuration", err);
	std::string tmp = paths.gateway_preference + ".tmp";
	if (auto err = write_file(tmp, preferred + "\n", 0600)) return wrap("write gateway configuration", err);
	return rename(tmp, paths.gateway_preference);
}

std::vector<GatewayRoute> parse_default_gateway_entries(const std::string &table)
{
	std::vector<GatewayRoute> routes;
	for (const auto &line : split(table, "\n")) {
		auto f = fields(line);
		if (f.size() < 8 || f[1] != "00000000" || f[7] != "00000000") continue;
		std::string iface = f[0], gateway = parse_route_gateway(f[2]);
		bool ok = false;
		int metric = atoi_int(f[6], ok);
		if (gateway.empty() || !is_gateway_interface(iface)) continue;
		routes.push_back(GatewayRoute{iface, gateway, metric, std::nullopt, false});
	}
	return routes;
}

std::vector<GatewayRoute> default_gateway_entries()
{
	auto table = read_file(paths.gateway_route_table);
	if (!table) return {};
	return parse_default_gateway_entries(*table);
}

std::vector<GatewayRoute> unique_gateway_routes(const std::vector<GatewayRoute> &entries)
{
	std::vector<GatewayRoute> routes;
	std::set<std::string> seen;
	for (const auto &route : entries)
		if (seen.insert(route.interface + std::string(1, '\0') + route.gateway).second) routes.push_back(route);
	return routes;
}

std::string gateway_kind(const std::string &iface)
{
	if (iface.rfind("eth", 0) == 0) return "ethernet";
	if (iface.rfind("wlan", 0) == 0) return "wifi";
	return "";
}

int preference_metric(const std::string &preferred, const std::string &iface, int index)
{
	if (preferred == "auto") return 100 + index;
	if (gateway_kind(iface) == preferred) return 10 + index;
	return 200 + index;
}

Error apply_gateway_preference(const std::string &preferred, const std::vector<GatewayRoute> &entries)
{
	// A stable order: the table lists routes by metric, which this changes.
	auto routes = unique_gateway_routes(entries);
	std::sort(routes.begin(), routes.end(), [](const GatewayRoute &a, const GatewayRoute &b) {
		if (a.interface != b.interface) return a.interface < b.interface;
		return a.gateway < b.gateway;
	});
	// Routes of other interfaces keep their metrics: a preferred metric one of them holds moves to the next free one.
	std::set<int> managed_metrics, used = occupied_metrics(), taken;
	for (const auto &entry : entries) managed_metrics.insert(entry.metric);
	used.insert(managed_metrics.begin(), managed_metrics.end());
	std::vector<int> desired(routes.size()), current(routes.size());
	for (std::size_t index = 0; index < routes.size(); index++) {
		const auto &route = routes[index];
		int metric = preference_metric(preferred, route.interface, static_cast<int>(index));
		// A route the gateway check demoted stays demoted; the metric of the preference is what it gets back.
		if (demoted_original(route.interface)) {
			if (auto err = write_demoted_original(route.interface, metric)) return wrap("record the route for " + route.interface, err);
			metric = gateway_demoted_metric;
		}
		while (taken.count(metric) || (used.count(metric) && !managed_metrics.count(metric))) metric++;
		desired[index] = metric;
		taken.insert(metric);
		used.insert(metric);
	}
	auto metrics_of = [&](const GatewayRoute &route) {
		std::vector<int> metrics;
		for (const auto &entry : entries)
			if (entry.interface == route.interface && entry.gateway == route.gateway) metrics.push_back(entry.metric);
		return metrics;
	};
	int next = 1000;
	for (std::size_t index = 0; index < routes.size(); index++) {
		const auto &route = routes[index];
		auto old = metrics_of(route);
		if (old.size() == 1 && old[0] == desired[index]) {
			current[index] = desired[index];
			continue;
		}
		// add, not replace: it fails rather than take a metric someone holds.
		Error err;
		for (int attempt = 0; attempt < 16; attempt++) {
			while (used.count(next)) next++;
			used.insert(next);
			if (!(err = add_gateway_route(route, next))) break;
		}
		if (err) return wrap("apply route for " + route.interface, err);
		current[index] = next;
		for (int metric : old)
			if (auto e = delete_gateway_route(route, metric)) return wrap("remove the old route for " + route.interface, e);
	}
	for (std::size_t index = 0; index < routes.size(); index++) {
		const auto &route = routes[index];
		if (current[index] == desired[index]) continue;
		if (auto err = set_gateway_route(route, desired[index])) return wrap("apply route for " + route.interface, err);
		if (auto err = delete_gateway_route(route, current[index])) return wrap("remove the interim route for " + route.interface, err);
	}
	return {};
}

// --- the gateway check (gateway_check.go) ----------------------------------------------------------------------------

bool valid_gateway_check(const GatewayCheckConfig &c)
{
	if (c.method != "arp" && c.method != "icmp") return false;
	if (c.host.empty()) return true;
	auto alnum = [](char x) { return (x >= 'A' && x <= 'Z') || (x >= 'a' && x <= 'z') || (x >= '0' && x <= '9'); };
	if (c.host.size() > 253 || !alnum(c.host[0])) return false;
	return std::all_of(c.host.begin() + 1, c.host.end(), [&](char x) { return alnum(x) || x == '.' || x == ':' || x == '-'; });
}

GatewayCheckConfig read_gateway_check()
{
	GatewayCheckConfig fallback;
	auto data = read_file(paths.gateway_check_file);
	if (!data) return fallback;
	auto v = parse_json_whole(*data);
	if (!v || (v->kind != JValue::Kind::object && v->kind != JValue::Kind::null)) return fallback;
	GatewayCheckConfig stored;
	stored.method.clear();
	auto fold = [](std::string s) {
		for (auto &c : s)
			if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
		return s;
	};
	for (const auto &[key, member] : v->members) {
		std::string k = fold(key);
		if (member.kind == JValue::Kind::null) continue;
		if (k == "enabled") {
			if (member.kind != JValue::Kind::boolean) return fallback;
			stored.enabled = member.flag;
		} else if (k == "method" || k == "host") {
			if (member.kind != JValue::Kind::string) return fallback;
			(k == "method" ? stored.method : stored.host) = member.text;
		}
	}
	if (!valid_gateway_check(stored)) return fallback;
	return stored;
}

std::string gateway_check_json(const GatewayCheckConfig &c)
{
	return std::string("{\"enabled\":") + (c.enabled ? "true" : "false") + ",\"method\":" + quote(c.method) + ",\"host\":" + quote(c.host) + "}";
}

Error write_gateway_check(const GatewayCheckConfig &c)
{
	std::string contents = gateway_check_json(c);
	if (auto err = mkdir_all(dir_name(paths.gateway_check_file), 0755)) return wrap("create gateway configuration", err);
	std::string tmp = paths.gateway_check_file + ".tmp";
	if (auto err = write_file(tmp, contents + "\n", 0600)) return wrap("write gateway check", err);
	return rename(tmp, paths.gateway_check_file);
}

// Probes, replaced in the check. Each waits up to 2 s for one answer: Wi-Fi power save alone can delay one past a second.
std::function<bool(const std::string &, const std::string &, const std::string &)> probe_gateway =
	[](const std::string &iface, const std::string &gateway, const std::string &method) {
		if (method == "icmp") return run({"ping", "-q", "-c", "1", "-W", "2", "-I", iface, gateway}).ok();
		return run({"arping", "-q", "-c", "1", "-w", "2", "-I", iface, gateway}).ok();
	};

std::function<bool(const std::string &, const std::string &)> probe_host = [](const std::string &iface, const std::string &host) {
	return run({"ping", "-q", "-c", "1", "-W", "2", "-I", iface, host}).ok();
};

// Whether a DHCP server answers on the interface: a one-shot discover whose lease is not applied (-s /bin/true). A
// router that is down, rather than a stale lease, answers nothing; releasing the lease then would leave the interface
// without an address.
std::function<bool(const std::string &)> probe_dhcp_server = [](const std::string &iface) {
	return run({"udhcpc", "-i", iface, "-n", "-q", "-f", "-s", "/bin/true", "-t", "3", "-T", "2", "-A", "0"}).ok();
};

// Release, then discover again: udhcpc only looks for another server when its lease runs out.
std::function<Error(int)> restart_gateway_dhcp = [](int pid) -> Error {
	if (::kill(pid, SIGUSR2) != 0) return Error{errno, errno_text(errno)};
	if (::kill(pid, SIGUSR1) != 0) return Error{errno, errno_text(errno)};
	return {};
};

bool gateway_carrier(const std::string &iface)
{
	auto data = read_file(path_join(path_join(paths.gateway_sys_net, iface), "carrier"));
	return data && trim_space(*data) == "1";
}

int gateway_dhcp_client(const std::string &iface)
{
	if (iface.rfind("eth", 0) != 0) return 0;
	auto data = read_file(paths.gateway_dhcp_pid_file);
	if (!data) return 0;
	bool ok = false;
	int pid = atoi_int(trim_space(*data), ok);
	if (!ok || pid <= 1) return 0;
	auto comm = read_file(path_join(path_join(paths.gateway_proc_dir, std::to_string(pid)), "comm"));
	if (!comm || trim_space(*comm) != "udhcpc") return 0;
	return pid;
}

std::optional<int> demoted_original(const std::string &iface)
{
	auto data = read_file(path_join(paths.gateway_demoted_dir, iface));
	if (!data) return std::nullopt;
	bool ok = false;
	int metric = atoi_int(trim_space(*data), ok);
	if (!ok) return std::nullopt;
	return metric;
}

Error write_demoted_original(const std::string &iface, int metric)
{
	if (auto err = mkdir_all(paths.gateway_demoted_dir, 0755)) return err;
	return write_file(path_join(paths.gateway_demoted_dir, iface), std::to_string(metric) + "\n", 0644);
}

std::pair<int, Error> move_gateway_route(const GatewayRoute &route, const std::vector<GatewayRoute> &entries, int from, int limit)
{
	auto used = occupied_metrics();
	for (const auto &entry : entries) used.insert(entry.metric);
	Error err;
	for (int metric = from; metric < limit; metric++) {
		if (used.count(metric)) continue;
		if ((err = add_gateway_route(route, metric))) continue;
		for (const auto &entry : entries) {
			if (entry.interface == route.interface && entry.gateway == route.gateway && entry.metric != metric)
				if (auto e = delete_gateway_route(route, entry.metric)) return {metric, e};
		}
		return {metric, {}};
	}
	if (!err) err = make_error("no free metric");
	return {0, err};
}

Error demote_gateway(const GatewayRoute &route, const std::vector<GatewayRoute> &entries)
{
	if (auto err = write_demoted_original(route.interface, route.metric)) return err;
	return move_gateway_route(route, entries, gateway_demoted_metric, 1000).second;
}

Error restore_gateway(const GatewayRoute &route, const std::vector<GatewayRoute> &entries)
{
	auto original = demoted_original(route.interface);
	int metric = original && !is_demoted_metric(*original) ? *original : preference_metric(read_gateway_preference(), route.interface, 0);
	if (auto err = move_gateway_route(route, entries, metric, gateway_demoted_metric).second) return err;
	return remove(path_join(paths.gateway_demoted_dir, route.interface));
}

void restore_all_gateways()
{
	Error err;
	auto files = read_dir(paths.gateway_demoted_dir, &err);
	if (err) return;
	auto entries = default_gateway_entries();
	for (const auto &file : files) {
		bool restored = false;
		for (const auto &route : unique_gateway_routes(entries)) {
			if (route.interface != file.name) continue;
			if (auto e = restore_gateway(route, entries)) log::warn("gateway check: restore {}: {}", route.interface, e.text);
			restored = true;
		}
		if (!restored) (void)remove(path_join(paths.gateway_demoted_dir, file.name));
	}
}

GatewayChecker gateway_watch;

void GatewayChecker::tick(const GatewayCheckConfig &config)
{
	if (!config.enabled) {
		// Health is cleared under gateway_mu, as a round publishes it: a round that was running cannot bring it back
		// afterwards.
		std::lock_guard lock(gateway_mu);
		gateway_check_generation++;
		{
			std::lock_guard h(mu);
			health.clear();
		}
		restore_all_gateways();
		return;
	}
	std::uint64_t generation;
	{
		std::lock_guard lock(gateway_mu);
		generation = gateway_check_generation;
	}
	// current: the check was not turned off since the round started (callers hold gateway_mu).
	auto current = [&] { return gateway_check_generation == generation; };
	auto start = now();
	std::map<std::string, std::shared_ptr<GatewayHealth>> results;
	// The round's results are published when it is done, unless the check was turned off meanwhile.
	struct Publish {
		GatewayChecker *self;
		std::map<std::string, std::shared_ptr<GatewayHealth>> &results;
		std::function<bool()> current;
		~Publish()
		{
			std::lock_guard lock(gateway_mu);
			if (current()) {
				std::lock_guard h(self->mu);
				self->health = results;
			}
		}
	} publish{this, results, current};

	// The probes take seconds while a gateway is silent: outside gateway_mu, so that the preferred-gateway setting
	// does not wait for them.
	for (const auto &route : unique_gateway_routes(default_gateway_entries())) {
		if (results.count(route.interface) || gateway_kind(route.interface) != "ethernet" || !gateway_carrier(route.interface)) continue;
		auto h = std::make_shared<GatewayHealth>();
		{
			std::lock_guard lock(mu);
			if (auto it = health.find(route.interface); it != health.end() && it->second) *h = *it->second;
		}
		bool gateway_ok = probe_gateway(route.interface, route.gateway, config.method);
		bool ok = gateway_ok && (config.host.empty() || probe_host(route.interface, config.host));
		if (ok) h->passes++, h->fails = 0;
		else h->passes = 0, h->fails++;
		if (gateway_ok) h->silent_since.reset();
		else if (!h->silent_since) h->silent_since = start;
		h->checked = true;
		h->reachable = ok;
		results[route.interface] = h;
	}

	// The table again: routes may have moved while the probes ran.
	std::vector<GatewayRoute> silent;
	{
		std::lock_guard lock(gateway_mu);
		if (!current()) return;
		auto entries = default_gateway_entries();
		auto routes = unique_gateway_routes(entries);
		for (const auto &route : routes) {
			auto it = results.find(route.interface);
			std::shared_ptr<GatewayHealth> h = it == results.end() ? nullptr : it->second;
			// A route in the demoted band without its record (a renew moved it there between two steps of a restore) is
			// demoted all the same.
			bool demoted = demoted_original(route.interface).has_value() || is_demoted_metric(route.metric);
			if (!h) {
				// Without carrier the kernel ignores the route; a demotion stays for when the link comes back.
			} else if (demoted && h->passes >= gateway_restore_after) {
				if (auto err = restore_gateway(route, entries)) log::warn("gateway check: restore {}: {}", route.interface, err.text);
				else log::info("gateway check: {} via {} answers again, route restored", route.interface, route.gateway);
			} else if (!demoted && h->fails >= gateway_demote_after && wifi_connected(routes)) {
				if (auto err = demote_gateway(route, entries)) log::warn("gateway check: demote {}: {}", route.interface, err.text);
				else log::warn("gateway check: {} via {} does not answer, traffic moves to the other connection", route.interface, route.gateway);
			}
			if (h && h->silent_since && start - *h->silent_since >= gateway_dhcp_after &&
			    (!h->last_dhcp || start - *h->last_dhcp >= gateway_check_dhcp_holdoff))
				silent.push_back(route);
		}
		// A demotion whose interface lost its default route is over.
		Error err;
		auto files = read_dir(paths.gateway_demoted_dir, &err);
		if (!err) {
			for (const auto &file : files) {
				bool found = std::any_of(routes.begin(), routes.end(), [&](const GatewayRoute &r) { return r.interface == file.name; });
				if (!found) (void)remove(path_join(paths.gateway_demoted_dir, file.name));
			}
		}
	}

	// The discover takes seconds too: outside gateway_mu as well.
	for (const auto &route : silent) {
		auto h = results[route.interface];
		int pid = gateway_dhcp_client(route.interface);
		bool stale;
		{
			std::lock_guard lock(gateway_mu);
			stale = !current();
		}
		if (pid == 0 || stale) continue;
		h->last_dhcp = start;
		if (!probe_dhcp_server(route.interface)) {
			if (!h->kept_lease)
				log::warn("gateway check: gateway {} silent and no DHCP server answers on {}, keeping the lease", route.gateway, route.interface);
			h->kept_lease = true;
			continue;
		}
		// The check may have been turned off during the discover: the restart under gateway_mu, so that turning it off
		// waits for it or prevents it.
		Error err;
		{
			std::lock_guard lock(gateway_mu);
			if (!current()) return;
			h->silent_since.reset();
			h->kept_lease = false;
			err = restart_gateway_dhcp(pid);
		}
		if (err) log::warn("gateway check: restart DHCP on {}: {}", route.interface, err.text);
		else log::warn("gateway check: gateway {} silent for 2 minutes, DHCP on {} starts again", route.gateway, route.interface);
	}
}

std::pair<bool, bool> GatewayChecker::status(const std::string &iface)
{
	std::lock_guard lock(mu);
	if (auto it = health.find(iface); it != health.end() && it->second) return {it->second->checked, it->second->reachable};
	return {false, false};
}

std::vector<GatewayRoute> gateway_status_routes()
{
	auto routes = unique_gateway_routes(default_gateway_entries());
	for (auto &route : routes) {
		route.demoted = demoted_original(route.interface).has_value();
		if (auto [checked, reachable] = gateway_watch.status(route.interface); checked) route.reachable = reachable;
	}
	return routes;
}

void start_gateway_check()
{
	static std::once_flag once;
	std::call_once(once, [] {
		std::thread([] {
			for (;;) {
				gateway_watch.tick(read_gateway_check());
				std::this_thread::sleep_for(gateway_check_interval);
			}
		}).detach();
	});
}

Decoded<std::string> decode_set_gateway_preference(const RequestData &data)
{
	Decoded<std::string> d;
	auto b = Binder::bind(data, false);
	std::string preferred;
	if (b.ok()) b.str("preferred", "Preferred", preferred);
	if (!b.ok() || (preferred != "auto" && preferred != "ethernet" && preferred != "wifi")) {
		d.refusal = {-1, "invalid gateway preference"};
		return d;
	}
	d.value = preferred;
	return d;
}

Decoded<GatewayCheckConfig> decode_set_gateway_check(const RequestData &data)
{
	Decoded<GatewayCheckConfig> d;
	auto b = Binder::bind(data, false);
	GatewayCheckConfig c;
	c.method.clear();
	if (b.ok()) {
		b.boolean("enabled", "Enabled", c.enabled);
		b.str("method", "Method", c.method);
		b.str("host", "Host", c.host);
	}
	// validate: method empty or arp/icmp, host at most 253 characters.
	if (!b.ok() || (!c.method.empty() && c.method != "arp" && c.method != "icmp") || rune_count(c.host) > 253) {
		d.refusal = {-1, "invalid gateway check"};
		return d;
	}
	c.host = trim_space(c.host);
	if (c.method.empty()) c.method = "arp";
	if (!valid_gateway_check(c)) {
		d.refusal = {-1, "invalid gateway check"};
		return d;
	}
	d.value = c;
	return d;
}

} // namespace nkos::network
