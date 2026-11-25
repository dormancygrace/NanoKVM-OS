#include "standby/standby.hpp"
#include "http/server.hpp"

#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"
#include "network/network.hpp"
#include "network/util.hpp"
#include "storage/go.hpp"
#include "system/journal.hpp"

#include <openssl/rand.h>

#include <cerrno>
#include <csignal>
#include <cstring>
#include <fcntl.h>
#include <net/if.h>
#include <atomic>
#include <set>
#include <sys/file.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <thread>
#include <unistd.h>

namespace nkos::standby {

namespace go = nkos::storage::go;

std::chrono::milliseconds begin_wait{30000};

// --- Gate --------------------------------------------------------------------------------------------------------

bool Gate::enter(bool mutating)
{
	std::lock_guard lock(mu_);
	if (closed_ && mutating) return false;
	if (mutating) active_++;
	return true;
}

bool Gate::enter_stream()
{
	std::lock_guard lock(mu_);
	return !closed_;
}

void Gate::leave(bool mutating)
{
	if (!mutating) return;
	std::lock_guard lock(mu_);
	active_--;
}

Error Gate::begin(const std::function<void()> &sweep)
{
	{
		std::lock_guard lock(mu_);
		if (closed_) return "shutdown already requested";
		closed_ = true;
	}
	auto deadline = std::chrono::steady_clock::now() + begin_wait;
	for (;;) {
		if (sweep) sweep();
		{
			std::lock_guard lock(mu_);
			if (active_ == 0) return "";
			if (std::chrono::steady_clock::now() > deadline) {
				closed_ = false;
				return "another operation is running; finish it before shutdown";
			}
		}
		std::this_thread::sleep_for(std::chrono::milliseconds(50));
	}
}

void Gate::cancel()
{
	std::lock_guard lock(mu_);
	closed_ = false;
}

int Gate::active()
{
	std::lock_guard lock(mu_);
	return active_;
}

Gate &requests()
{
	static Gate gate;
	return gate;
}

namespace {

// Mutating requests admitted and not yet answered (loop thread).
std::set<std::uint64_t> admitted;

// Requests whose connection closed before their answer was final: no after hook runs for them.
void sweep_closed()
{
	for (auto it = admitted.begin(); it != admitted.end();) {
		if (http::request_open(*it)) {
			++it;
			continue;
		}
		it = admitted.erase(it);
		requests().leave(true);
	}
}

// From a blocking thread: one sweep on the loop, waited for briefly.
void sweep_from_worker()
{
	auto done = std::make_shared<std::atomic<bool>>(false);
	loop::post([done] {
		sweep_closed();
		done->store(true);
	});
	for (int i = 0; i < 20 && !done->load(); i++) std::this_thread::sleep_for(std::chrono::milliseconds(1));
}

// websocket.IsWebSocketUpgrade: "upgrade" among the Connection tokens and "websocket" among the Upgrade ones.
bool has_token(const http::Request &req, std::string_view name, std::string_view token)
{
	for (const auto &value : req.header_values(name))
		for (const auto &part : network::split(value, ","))
			if (go::equal_fold(network::trim_space(part), token)) return true;
	return false;
}

void refuse(http::Response &res) { res.json(409, R"({"code":-1,"msg":"NanoKVM shutdown is in progress"})"); }

} // namespace

std::function<void()> begin_sweep = [] { sweep_from_worker(); };

void add_admission(http::Router &router)
{
	router.use([](http::Request &req, http::Response &res) {
		const std::string &path = req.path;
		if (path == "/api/vm/system/shutdown" || path == "/standby" || path.starts_with("/standby/") || path == "/standby.js")
			return;
		if (req.method == "GET" &&
		    ((has_token(req, "connection", "upgrade") && has_token(req, "upgrade", "websocket")) || path == "/api/stream/mjpeg")) {
			if (!requests().enter_stream()) refuse(res);
			return;
		}
		bool mutating = req.method != "GET" && req.method != "HEAD" && req.method != "OPTIONS";
		// These GET handlers perform a radio scan or mount the user disk.
		if (path == "/api/network/wifi/scan" || path == "/api/storage/user-disk/files" ||
		    path == "/api/storage/user-disk/files/download")
			mutating = true;
		if (!requests().enter(mutating)) {
			refuse(res);
			return;
		}
		if (mutating) admitted.insert(req.id);
	});
	router.after([](const http::Request &req, const http::Response &) {
		if (admitted.erase(req.id)) requests().leave(true);
	});
}

Error pending()
{
	struct stat st{};
	if (::stat(marker, &st) != 0 && errno == ENOENT) return "";
	return "NanoKVM shutdown is in progress";
}

// --- preparation -------------------------------------------------------------------------------------------------

namespace {

// Owns the cross-process exclusion locks until init starts shutdown.
struct Prepared {
	Config config;
	std::vector<int> locks;
	bool transfer = false, armed = false, bound = false;

	void close()
	{
		if (armed || bound) return;
		for (int fd : locks) ::close(fd);
		locks.clear();
		if (transfer) ::unlink("/run/nanokvm-transfer-in-progress");
		::unlink(marker);
		go::remove_all(directory);
	}
	Error arm();
};

Error lock_file(const std::string &name, int &out)
{
	int fd = ::open(name.c_str(), O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0600);
	if (fd < 0) return "operation busy (" + name + "): " + go::errno_text(errno);
	struct stat st{};
	Error err;
	if (::fstat(fd, &st) != 0) err = go::path_error("stat", name, errno);
	else if (!S_ISREG(st.st_mode)) err = "not a regular lock file";
	else if (::flock(fd, LOCK_EX | LOCK_NB) != 0) err = go::errno_text(errno);
	if (!err.empty()) {
		::close(fd);
		return "operation busy (" + name + "): " + err;
	}
	out = fd;
	return "";
}

Error write_file(const std::string &path, std::string_view data, unsigned mode)
{
	auto e = go::write_file(path, data, mode);
	return e.text;
}

Error read_file(const std::string &path, std::string &data) { return go::read_file(path, data).text; }

bool not_exist(int err) { return err == ENOENT; }

Error snapshot_network(Config &c)
{
	auto ip = network::parse_ip(c.address);
	if (!ip || !network::ip_is_v4(*ip)) return "open NanoKVM at its direct Ethernet or Wi-Fi IPv4 address first";
	bool ok = true;
	auto ifaces = network::interfaces(&ok);
	if (!ok) return "route ip+net: netlinkrib: cannot list the interfaces";
	bool found = false;
	for (const auto &nic : ifaces) {
		for (const auto &addr : nic.addrs) {
			if (addr.ip != *ip) continue;
			if (nic.name != "eth0" && nic.name != "wlan0")
				return "standby does not support USB, VPN, VLAN or bridge management; use eth0 or station Wi-Fi";
			struct stat st{};
			if (!(::stat(("/sys/class/net/" + nic.name + "/master").c_str(), &st) != 0 && errno == ENOENT))
				return "bridged management is unsupported";
			if (!(nic.flags & IFF_UP) || nic.hwaddr.size() != 6) return "management interface is not ready";
			static const char hex[] = "0123456789abcdef";
			std::string mac;
			for (unsigned char b : nic.hwaddr) {
				if (!mac.empty()) mac += ':';
				mac += hex[b >> 4];
				mac += hex[b & 15];
			}
			c.interface = nic.name;
			c.mac = mac;
			c.prefix = addr.prefix_len;
			c.wifi = nic.name == "wlan0";
			found = true;
		}
	}
	if (!found) return "use the device's direct IPv4 address; proxy/VPN and hostname handoff is unsupported";
	struct stat st{};
	if (c.wifi && !(::stat("/run/nanokvm-wifiap", &st) != 0 && errno == ENOENT))
		return "Wi-Fi access-point standby is unsupported; join a Wi-Fi network or use Ethernet";
	std::string pid_file = "/run/udhcpc." + c.interface + ".pid";
	std::string data;
	if (auto e = go::read_file(pid_file, data); !e) {
		auto pid = network::atoi(network::trim_space(data));
		if (!pid || *pid < 2) return "invalid DHCP state";
		std::string cmd;
		auto ce = go::read_file("/proc/" + std::to_string(*pid) + "/cmdline", cmd);
		auto args = network::split(cmd, std::string_view("\0", 1));
		bool matched = false;
		for (std::size_t i = 1; i < args.size(); i++)
			if (args[i - 1] == "-i" && args[i] == c.interface) matched = true;
		if (ce || args[0].find("udhcpc") == std::string::npos || !matched) return "DHCP state is not current";
		c.dhcp = true;
	} else if (e.sys != ENOENT) {
		return e.text;
	}
	if (!c.dhcp) {
		if (c.wifi) return "station Wi-Fi without the managed DHCP client is unsupported";
		std::string saved;
		if (go::read_file("/boot/eth.nodhcp", saved)) return "cannot confirm static Ethernet configuration";
		bool matched = false;
		for (const auto &line : network::split(saved, "\n")) {
			auto f = network::fields(line.substr(0, line.find('#')));
			if (!f.empty() && f[0].substr(0, f[0].find('/')) == c.address) matched = true;
		}
		if (!matched) return "management address does not match saved static Ethernet configuration";
	}
	std::string table;
	if (Error e = read_file("/proc/net/route", table); !e.empty()) return e;
	std::string route;
	if (Error e = client_route(table, c.client_address, route); !e.empty()) return e;
	if (route != c.interface)
		return "connect through the selected Ethernet or Wi-Fi network before shutdown; VPN/proxy ingress is unsupported";
	if (Error e = simple_routes(table, c); !e.empty()) return e;
	if (Error e = default_gateway(table, c.interface, c.gateway); !e.empty()) return e;
	std::string hostname;
	if (!go::read_file("/etc/hostname", hostname)) c.hostname = network::trim_space(hostname);
	return "";
}

Error mem_available(std::int64_t &out)
{
	std::string data;
	if (Error e = read_file("/proc/meminfo", data); !e.empty()) return e;
	for (const auto &line : network::split(data, "\n")) {
		auto f = network::fields(line);
		if (f.size() > 1 && f[0] == "MemAvailable:") {
			auto n = network::atoi(f[1]);
			if (!n) return "strconv.ParseInt: parsing " + network::go_quote(f[1]) + ": invalid syntax";
			out = *n * 1024;
			return "";
		}
	}
	return "cannot determine available RAM";
}

Error file_size(const std::string &name, std::int64_t &size)
{
	struct stat st{};
	if (::stat(name.c_str(), &st) != 0) return go::path_error("stat", name, errno);
	size = st.st_size;
	return "";
}

Error prepare(const Config &request, std::unique_ptr<Prepared> &result)
{
	// Admission is already closed. Refuse active or retrying network trials before any snapshot or staging.
	if (auto busy = network::trial_check_idle()) return "finish network setup before shutdown: " + *busy;
	struct stat init_info{}, busy_info{};
	if (::stat("/proc/1/exe", &init_info) != 0) return go::path_error("stat", "/proc/1/exe", errno);
	if (::stat("/bin/busybox", &busy_info) != 0 || init_info.st_dev != busy_info.st_dev || init_info.st_ino != busy_info.st_ino)
		return "standby requires NanoKVM BusyBox init";
	std::string table, handed;
	if (Error e = read_file("/etc/inittab", table); !e.empty()) return e;
	if (Error e = handoff_inittab(table, handed); !e.empty()) return e;
	std::vector<Mount> mounts;
	if (Error e = read_mounts(mounts); !e.empty()) return e;
	bool root_ok = false, ram_ok = false;
	for (const auto &m : mounts) {
		if (m.point == "/") root_ok = m.source == "/dev/mmcblk0p2" && (m.type == "ext4" || m.type == "f2fs");
		if (m.point == "/run") ram_ok = m.type == "tmpfs" && !has_option(m.options, "noexec") && !has_option(m.options, "ro");
		if (!memory_fs(m.type) && !block_fs(m.type)) return "unsupported mounted filesystem " + m.type + " at " + m.point;
	}
	if (!root_ok || !ram_ok) return "standby requires the standard SD root and executable /run tmpfs";
	struct stat st{};
	if (!(::lstat(directory, &st) != 0 && not_exist(errno)))
		return "standby preparation already exists; reboot before retrying";
	for (std::string name : {"/var/lib/nanokvm-recovery/pending.json", "/boot/rec"})
		if (!(::stat(name.c_str(), &st) != 0 && not_exist(errno)))
			return "finish recovery/network setup before shutdown (" + name + ")";
	Config c = request;
	if (Error e = snapshot_network(c); !e.empty()) return e;
	if (::mkdir(directory, 0700) != 0) return go::path_error("mkdir", directory, errno);
	auto p = std::make_unique<Prepared>();
	p->config = c;
	auto failed = [&p](Error e) {
		p->close();
		return e;
	};
	// The updater's existing lock location, created as osupdate creates it.
	if (Error e = mkdir_all("/kvmapp/.os-update", 0700); !e.empty()) return failed(e);
	for (std::string name : {"/kvmapp/.os-update/lock", "/run/nanokvm-apk.lock", "/lib/apk/db/lock"}) {
		int fd = -1;
		if (Error e = lock_file(name, fd); !e.empty()) return failed(e);
		p->locks.push_back(fd);
	}
	int t = ::open("/run/nanokvm-transfer-in-progress", O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
	if (t < 0) return failed("an upload or image download is running");
	::close(t);
	p->transfer = true;
	// Closing the API gate and taking the locks precede this marker; queued package workers recheck it.
	if (Error e = write_file(marker, "preparing\n", 0600); !e.empty()) return failed(e);
	unsigned char token[32];
	if (RAND_bytes(token, sizeof token) != 1) return failed("crypto/rand: cannot read random bytes");
	static const char hex[] = "0123456789abcdef";
	p->config.token.clear();
	for (unsigned char b : token) {
		p->config.token += hex[b >> 4];
		p->config.token += hex[b & 15];
	}
	for (std::string dir : {"bin", "lib", "usr/lib", "dev", "proc", "sys", "run", "tmp"})
		if (Error e = mkdir_all(go::join(root, dir), 0700); !e.empty()) return failed(e);
	if (Error e = static_elf(helper); !e.empty()) return failed(e);
	// Discover the ELF closure and reserve RAM before copying any payload.
	std::map<std::string, bool> planned;
	if (Error e = stage_elf("/bin/busybox", "/", "", planned); !e.empty()) return failed(e);
	std::string wifi_config;
	if (c.wifi) {
		if (Error e = stage_elf("/sbin/wpa_supplicant", "/", "", planned); !e.empty()) return failed(e);
		std::string data;
		if (Error e = read_file("/run/nanokvm-wifi/wpa_supplicant.conf", data); !e.empty()) return failed(e);
		if (data.size() > (256u << 10)) return failed("Wi-Fi configuration is too large");
		if (Error e = ram_wpa(data, wifi_config); !e.empty()) return failed(e);
	}
	std::int64_t payload = 0, size = 0;
	if (Error e = file_size(helper, payload); !e.empty()) return failed(e);
	for (const auto &[name, _] : planned) {
		if (Error e = file_size(name, size); !e.empty()) return failed(e);
		payload += size;
	}
	std::int64_t available = 0;
	if (Error e = mem_available(available); !e.empty()) return failed(e);
	if (Error e = check_ram_budget(payload, available); !e.empty()) return failed(e);
	struct statfs space{};
	if (::statfs("/run", &space) != 0) return failed(go::errno_text(errno));
	if (static_cast<std::int64_t>(space.f_bavail) * static_cast<std::int64_t>(space.f_bsize) < payload + (4 << 20))
		return failed("/run has insufficient free space");
	if (Error e = copy_file(helper, std::string(root) + "/init", 0700); !e.empty()) return failed(e);
	std::map<std::string, bool> seen;
	if (Error e = stage_elf("/bin/busybox", "/", root, seen); !e.empty()) return failed(e);
	if (c.wifi) {
		if (Error e = stage_elf("/sbin/wpa_supplicant", "/", root, seen); !e.empty()) return failed(e);
		if (Error e = write_file(std::string(root) + "/wpa.conf", wifi_config, 0600); !e.empty()) return failed(e);
	}
	if (Error e = write_file(std::string(root) + "/config.json", config_json(p->config), 0600); !e.empty()) return failed(e);
	network::RunOptions options;
	options.combined = true;
	auto probe = network::run({std::string(root) + "/init", "probe", root}, options);
	if (!probe.ok()) return failed("RAM helper probe: " + probe.out + ": " + probe.error);
	if (Error e = sync_filesystems(mounts); !e.empty()) return failed(e);
	result = std::move(p);
	return "";
}

// Arm first gets acknowledgement from an init-owned respawn action: sleeping for an assumed SIGHUP delay could
// otherwise turn SIGQUIT into a no-op.
Error Prepared::arm()
{
	std::string data, table;
	if (Error e = read_file("/etc/inittab", data); !e.empty()) return e;
	if (Error e = handoff_inittab(data, table); !e.empty()) return e;
	std::string dir = directory;
	if (Error e = write_file(dir + "/inittab", table, 0600); !e.empty()) return e;
	std::string rc;
	if (Error e = read_file("/etc/rc.conf", rc); !e.empty()) return e;
	// No '*' here: OpenRC expands this value unquoted, so /.* would become /. and /..
	rc += "\n# RAM-only standby handoff\nRC_NO_UMOUNTS=\"/:/.+\"\n";
	if (Error e = write_file(dir + "/rc.conf", rc, 0600); !e.empty()) return e;
	if (::mount((dir + "/rc.conf").c_str(), "/etc/rc.conf", "", MS_BIND, nullptr) != 0) return go::errno_text(errno);
	bound = true;
	if (::mount((dir + "/inittab").c_str(), "/etc/inittab", "", MS_BIND, nullptr) != 0) {
		Error err = go::errno_text(errno);
		if (::umount2("/etc/rc.conf", 0) != 0)
			return err + "; cannot restore rc.conf (" + go::errno_text(errno) + "); reboot before retrying";
		bound = false;
		return err;
	}
	auto rollback = [this](const Error &cause) -> Error {
		if (::umount2("/etc/inittab", 0) != 0)
			return cause + "; cannot restore init (" + go::errno_text(errno) + "); reboot before retrying";
		if (::umount2("/etc/rc.conf", 0) != 0)
			return cause + "; cannot restore rc.conf (" + go::errno_text(errno) + "); reboot before retrying";
		if (::kill(1, SIGHUP) != 0)
			return cause + "; cannot reload restored init (" + go::errno_text(errno) + "); reboot before retrying";
		bound = false;
		return cause;
	};
	if (::kill(1, SIGHUP) != 0) return rollback(go::errno_text(errno));
	auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
	for (;;) {
		std::string ack;
		if (!go::read_file(dir + "/ack", ack) && ack == config.token) break;
		if (std::chrono::steady_clock::now() > deadline) return rollback("init did not acknowledge the RAM handoff");
		std::this_thread::sleep_for(std::chrono::milliseconds(25));
	}
	// The acknowledge worker waits for commit, keeping this API alive long enough to deliver its answer.
	if (Error e = write_file(dir + "/commit", "1", 0600); !e.empty()) return rollback(e);
	armed = true;
	return "";
}

// RemoveStale: a preparation left by a process that ended before closing it; an armed handoff stays.
Error remove_stale()
{
	struct stat st{};
	if (::lstat(directory, &st) != 0 && errno == ENOENT) return "";
	std::vector<Mount> mounts;
	if (Error e = read_mounts(mounts); !e.empty()) return e;
	for (const auto &m : mounts)
		if (m.point == "/etc/inittab" || m.point == "/etc/rc.conf") return "the standby handoff is armed; reboot to clear it";
	go::remove_all(directory);
	return "";
}

// --- the route's state ---------------------------------------------------------------------------------------------

struct Page {
	Config config;
	std::shared_ptr<State> state;
	std::string restart_error;
};

std::mutex page_mu;
std::shared_ptr<Page> page = std::make_shared<Page>(Page{{}, std::make_shared<State>(Config{}), "not ready"});

std::mutex route_mu;  // Go's mu: held for a whole shutdown request
std::unique_ptr<Prepared> prepared;
std::shared_ptr<State> pending_state;
bool committed = false;

// Ends an uncommitted preparation; its page stays, so a browser with the key learns that NanoKVM kept running.
void abandon(const std::string &reason)
{
	if (prepared) prepared->close();
	prepared.reset();
	requests().cancel();
	if (pending_state) pending_state->fail(reason);
}

std::string ok_body(std::string_view data) { return std::string("{\"code\":0,\"msg\":\"success\",\"data\":") + std::string(data) + "}"; }

} // namespace

Reply serve_page(const HttpIn &in)
{
	std::shared_ptr<Page> current;
	{
		std::lock_guard lock(page_mu);
		current = page;
	}
	Reply r = handle(current->config, *current->state, in);
	if (r.restart_first) {
		// Let the reply leave the socket before restarting; the server cannot restart the device itself.
		loop::after(std::chrono::milliseconds(250), [current] { current->state->restart_failed(current->restart_error); });
	}
	return r;
}

void answer_shutdown(const ShutdownCall &call, http::Response &out)
{
	// http.MaxBytesReader(4096): the JSON value must be complete within the first 4096 bytes.
	network::RequestData data{"POST", "application/json", "", call.body.substr(0, 4096)};
	auto b = network::Binder::bind(data, true);
	bool magic_wake = false;
	std::string action, token;
	b.boolean("magicWake", "magicWake", magic_wake);
	b.str("action", "action", action);
	b.str("token", "token", token);
	if (!b.ok()) {
		network::fail(out, -1, "invalid shutdown request");
		return;
	}
	std::lock_guard lock(route_mu);
	if (action == "commit" || action == "cancel") {
		bool match = prepared && token.size() == prepared->config.token.size();
		unsigned char diff = 0;
		if (match)
			for (std::size_t i = 0; i < token.size(); i++) diff |= static_cast<unsigned char>(token[i] ^ prepared->config.token[i]);
		if (!match || diff != 0) {
			network::fail(out, -1, "invalid shutdown capability");
			return;
		}
		if (action == "cancel") {
			if (committed) {
				network::fail(out, -1, "shutdown already committed");
				return;
			}
			abandon("shutdown cancelled; NanoKVM is still running");
			network::ok(out);
			return;
		}
		if (!committed) {
			if (Error e = prepared->arm(); !e.empty()) {
				abandon("shutdown failed; NanoKVM is still running: " + e);
				network::fail(out, -1, e);
				return;
			}
			committed = true;
			system::journal::record("system", "system.action", {{"action", "shutdown"}}, call.user);
		}
		network::ok(out);
		return;
	}
	if (action != "prepare") {
		network::fail(out, -1, "invalid shutdown action");
		return;
	}
	if (Error e = requests().begin(begin_sweep); !e.empty()) {
		network::fail(out, -1, e);
		return;
	}
	bool ok = false;
	struct Cancel {
		bool &ok;
		~Cancel()
		{
			if (!ok) requests().cancel();
		}
	} cancel{ok};
	std::string host = call.host, h, port;
	if (network::split_host_port(host, h, port)) host = h;
	Config cfg;
	cfg.address = host;
	cfg.client_address = call.client;
	cfg.port = call.http_port;
	cfg.magic_wake = magic_wake;
	if (call.client.empty()) {
		network::fail(out, -1, "cannot identify the management client");
		return;
	}
	if (call.proto == "https") {
		// Preserve the transport and the certificate the browser accepted; never fall back to plain HTTP.
		if (!call.tls) {
			network::fail(out, -1, "open the direct HTTPS address before shutdown");
			return;
		}
		cfg.port = call.https_port;
		if (call.cert_pem.empty() || call.key_pem.empty()) {
			network::fail(out, -1, "could not stage the current HTTPS certificate");
			return;
		}
		cfg.certificate = call.cert_pem;
		cfg.private_key = call.key_pem;
	}
	std::unique_ptr<Prepared> p;
	if (Error e = prepare(cfg, p); !e.empty()) {
		network::fail(out, -1, e);
		return;
	}
	auto state = std::make_shared<State>(p->config);
	{
		std::lock_guard page_lock(page_mu);
		page = std::make_shared<Page>(Page{p->config, state, "shutdown is still running"});
	}
	// A serial, not the address: an abandoned preparation's address can be the next one's.
	static std::uint64_t serial = 0;
	std::uint64_t id = ++serial;
	std::string token_out = p->config.token, mac = p->config.mac;
	prepared = std::move(p);
	pending_state = state;
	loop::after(std::chrono::minutes(1), [id] {
		loop::blocking([id] {
			std::lock_guard expiry_lock(route_mu);
			if (prepared && serial == id && !committed) abandon("shutdown preparation expired; ordinary services remain running");
		});
	});
	ok = true;
	out.set_header("Cache-Control", "no-store");
	out.json(200, ok_body("{\"mac\":" + network::quote(mac) + ",\"token\":" + network::quote(token_out) + "}"));
}

bool cross_origin_write(const http::Request &req)
{
	bool cross_site = go::equal_fold(network::trim_space(req.header("sec-fetch-site")), "cross-site");
	return !auth::check_websocket_origin(req) || (req.header("origin").empty() && cross_site);
}

void add_routes(http::Router &router)
{
	if (Error e = remove_stale(); !e.empty()) log::error("standby: {}", e);
	auto serve = [](http::Request &req, http::Response &res) {
		if (req.method == "HEAD") {
			// Gin has no HEAD for these GET routes.
			res.text(404, "404 page not found");
			return;
		}
		HttpIn in{req.method, req.path, req.header("authorization"), req.header("origin"), req.header("sec-fetch-site"),
		          req.header("host"), req.tls};
		Reply r = serve_page(in);
		res.status = r.status;
		for (const auto &[k, v] : r.headers) {
			if (k == "Content-Type") res.content_type = v;
			else res.set_header(k, v);
		}
		res.body = std::move(r.body);
		res.done = true;
	};
	router.add("GET", "/standby", serve);
	router.add("GET", "/standby.js", serve);
	router.add("GET", "/standby/status", serve);
	router.add("POST", "/standby/start", serve);
	auto inner = auth::guarded(
	    [](http::Request &req, http::Response &res) {
		    const auto &conf = config::get();
		    ShutdownCall call{req.body, req.header("host"), req.peer, req.auth ? req.auth->username : "", conf.proto,
		                      req.tls, conf.port.http, conf.port.https, {}, {}};
		    if (call.tls && call.proto == "https") http::current_certificate_pem(call.cert_pem, call.key_pem);
		    http::defer(req, res, [call = std::move(call)](http::Response &out) { answer_shutdown(call, out); });
	    },
	    auth::Guard{{"admin"}});
	router.add("POST", "/api/vm/system/shutdown", [inner](http::Request &req, http::Response &res) {
		if (cross_origin_write(req)) {
			res.json(403, R"({"code":-1,"msg":"cross-origin request refused"})");
			return;
		}
		inner(req, res);
	});
}

} // namespace nkos::standby
