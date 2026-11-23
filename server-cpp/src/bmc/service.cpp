#include "bmc/service.hpp"

#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "network/util.hpp"
#include "storage/go.hpp"
#include "storage/storage.hpp"

#include <openssl/sha.h>

#include <algorithm>
#include <set>

#include <arpa/inet.h>
#include <cerrno>
#include <cstring>
#include <ctime>
#include <net/if.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/eventfd.h>
#include <sys/socket.h>
#include <unistd.h>

namespace nkos::bmc {

std::function<HostSensorsStatus()> host_sensors_status = [] { return HostSensorsStatus{}; };
std::function<void(const std::string &, int, int)> notify_login_locked_out = [](const std::string &, int, int) {};
std::function<void(const std::string &, int, int)> notify_login_attempts_limited = [](const std::string &, int, int) {};
std::function<void(const std::string &, const std::string &)> notify_login_succeeded = [](const std::string &, const std::string &) {};

namespace {

// While IPMI is on but cannot open its port, it is tried again this often.
constexpr auto ipmi_retry = std::chrono::seconds(5);

std::string lower(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return s;
}

// A UDP address as Go's UDPAddr.String writes it: IPv4 (also mapped) plain, IPv6 in brackets with its zone.
std::string address_string(const sockaddr_in6 &a)
{
	char text[INET6_ADDRSTRLEN] = {};
	const auto *b = a.sin6_addr.s6_addr;
	bool mapped = std::all_of(b, b + 10, [](unsigned char c) { return c == 0; }) && b[10] == 0xff && b[11] == 0xff;
	if (mapped) {
		inet_ntop(AF_INET, b + 12, text, sizeof text);
		return std::string(text) + ":" + std::to_string(ntohs(a.sin6_port));
	}
	inet_ntop(AF_INET6, &a.sin6_addr, text, sizeof text);
	std::string host = text;
	if (a.sin6_scope_id) {
		char name[IF_NAMESIZE] = {};
		host += "%" + std::string(if_indextoname(a.sin6_scope_id, name) ? name : std::to_string(a.sin6_scope_id).c_str());
	}
	return "[" + host + "]:" + std::to_string(ntohs(a.sin6_port));
}

bool same_ipmi(const Config &a, const Config &b)
{
	return a.username == b.username && a.password == b.password && a.ipmi_port == b.ipmi_port && a.hold_seconds == b.hold_seconds;
}

// The JSON of a Go time.Time: RFC 3339 with nanoseconds (trailing zeros dropped) in the local zone, "Z" for UTC.
std::string json_time(std::int64_t ns)
{
	std::time_t secs = static_cast<std::time_t>(ns / 1000000000);
	long frac = static_cast<long>(ns % 1000000000);
	if (frac < 0) frac += 1000000000, secs--;
	std::tm tm{};
	localtime_r(&secs, &tm);
	char base[64];
	std::strftime(base, sizeof base, "%Y-%m-%dT%H:%M:%S", &tm);
	std::string out = base;
	if (frac) {
		std::string f = std::to_string(1000000000 + frac).substr(1);
		while (f.back() == '0') f.pop_back();
		out += "." + f;
	}
	long off = tm.tm_gmtoff;
	if (off == 0) return out + "Z";
	long a = off < 0 ? -off : off;
	std::string hh = std::to_string(100 + a / 3600).substr(1), mm = std::to_string(100 + a % 3600 / 60).substr(1);
	return out + (off < 0 ? "-" : "+") + hh + ":" + mm;
}

std::string read_text(const std::string &path)
{
	return network::read_file(path).value_or("");
}

// The drive as Redfish's VirtualMedia: http(s):// and smb:// images read as the computer asks (a CD/DVD drive),
// file:///data/NAME an image on the card (.iso a CD/DVD drive, .img a writable disk).
struct StorageMedia : redfish::Media {
	Drive drive;
	std::string medium(redfish::Medium &out) override
	{
		std::string error;
		auto current = drive.medium(error);
		if (!current) return error.empty() ? "cannot read the virtual drive" : error;
		out = redfish::Medium{current->inserted, current->image, current->cdrom, "URI"};
		if (current->source == "local") out.image = "file://" + current->image;
		else if (current->source == "browser") out.via = "Applet";
		else if (current->source == "netboot") out.via = "Oem", out.image = "file://" + current->image;
		return {};
	}
	// The refusals that mean "something else holds the drive", for the client's status code.
	static redfish::MediaError in_use(const std::string &error, bool drive_in_use = false)
	{
		if (error.empty()) return {};
		static const char *held[] = {"Turn off network boot first", "Remote media is already connected",
		                             "Turn off network boot before connecting an ISO", "Unmount the current image first",
		                             "Disconnect remote media before mounting another image",
		                             "Turn off network boot before mounting another image", "the virtual drive is in use"};
		bool in = drive_in_use;
		for (const char *h : held) in = in || error == h;
		if (in) return {true, std::string(redfish::media_in_use) + "\n" + error};
		return {false, error};
	}
	redfish::MediaError insert(const redfish::MediaRequest &request, std::chrono::milliseconds timeout) override
	{
		auto u = storage::go::parse_url(request.image);
		if (!u) return {false, "Image is not a URL."};
		bool writable = request.write_protected && !*request.write_protected;
		std::string scheme = lower(u->scheme);
		if (scheme == "http" || scheme == "https" || scheme == "smb") {
			if (writable) return {false, "An image from a URL is a read-only CD/DVD: WriteProtected must be true."};
			return in_use(drive.insert_url(request.image, request.username, request.password, timeout));
		}
		if (scheme == "file") {
			if (!u->host.empty() || !u->raw_query.empty() || !u->fragment.empty())
				return {false, "A local image is file:///data/NAME.iso or file:///data/NAME.img."};
			bool cdrom = lower(u->path).ends_with(".iso");
			if (cdrom && writable) return {false, "An .iso image is a read-only CD/DVD: WriteProtected must be true."};
			if (!cdrom && request.write_protected && *request.write_protected)
				return {false, "An .img image is a writable disk: WriteProtected must be false."};
			bool drive_in_use = false;
			std::string e = drive.insert_image_file(u->path, cdrom, drive_in_use);
			return in_use(e, drive_in_use);
		}
		return {false, "Image must be an http://, https:// or smb:// URL, or file:///data/NAME for an image on the NanoKVM."};
	}
	redfish::MediaError eject() override { return in_use(drive.eject_medium()); }
};

// Redfish's PhysicalContext values for the agent's part classes; "other" has none.
const std::map<std::string, std::string> contexts = {
	{"cpu", "CPU"}, {"gpu", "GPU"}, {"motherboard", "SystemBoard"}, {"memory", "Memory"}, {"storage", "StorageDevice"},
	{"network", "NetworkingDevice"}, {"cooler", "Fan"}, {"psu", "PowerSupply"}, {"battery", "Battery"},
};

struct AgentSensors : redfish::Sensors {
	std::function<HostSensorsStatus()> status;
	void readings(std::vector<redfish::Reading> &out, bool &fresh, bool &on) override
	{
		out.clear();
		HostSensorsStatus s = status ? status() : HostSensorsStatus{};
		on = s.on;
		fresh = false;
		if (!s.on || !s.has_report || s.stale) return;
		std::set<std::string> seen;
		for (const auto &sensor : s.sensors) {
			// The ID is made of what names the reading (hostsensors.Key): the same in every report and after a restart.
			std::string key = sensor.type + "\t" + sensor.hardware + "\t" + sensor.kind + "\t" + sensor.name;
			unsigned char sum[SHA256_DIGEST_LENGTH];
			SHA256(reinterpret_cast<const unsigned char *>(key.data()), key.size(), sum);
			std::string id = network::hex_encode(std::string(reinterpret_cast<char *>(sum), 6));
			if (!seen.insert(id).second) continue;  // a second reading of the same name is a duplicate
			auto ctx = contexts.find(sensor.type);
			out.push_back({id, sensor.hardware + " " + sensor.name, sensor.kind, ctx == contexts.end() ? "" : ctx->second, sensor.value});
		}
		fresh = true;
	}
};

} // namespace

std::string device_uuid(const std::string &machine_id)
{
	std::string h = network::trim_space(machine_id);
	if (h.size() != 32) return "";
	return h.substr(0, 8) + "-" + h.substr(8, 4) + "-" + h.substr(12, 4) + "-" + h.substr(16, 4) + "-" + h.substr(20, 12);
}

std::array<std::uint8_t, 16> device_guid(const std::string &machine_id)
{
	std::array<std::uint8_t, 16> guid{};
	auto b = network::hex_decode(network::trim_space(machine_id));
	if (b && b->size() == 16) std::copy(b->begin(), b->end(), guid.begin());
	return guid;
}

std::string firmware_version(const std::string &release)
{
	for (const auto &line : network::split(release, "\n")) {
		if (!line.starts_with("VERSION=")) continue;
		std::string value = network::trim_chars(line.substr(8), "\"");
		if (value.starts_with("v")) value.erase(0, 1);
		return value;
	}
	return "";
}

std::pair<std::uint8_t, std::uint8_t> version_bytes(const std::string &version)
{
	auto parts = network::split(version, ".");
	if (parts.size() > 3) {
		std::string rest;
		for (std::size_t i = 2; i < parts.size(); i++) rest += (i > 2 ? "." : "") + parts[i];
		parts.resize(2);
		parts.push_back(rest);
	}
	long long major = network::atoi(parts[0]).value_or(0), minor = 0;
	if (parts.size() > 1) minor = network::atoi(parts[1]).value_or(0);
	minor %= 100;
	return {static_cast<std::uint8_t>(major & 0x7F), static_cast<std::uint8_t>((minor / 10) << 4 | minor % 10)};
}

Drive storage_drive()
{
	return Drive{[](std::string &error) { return storage::medium(error); }, storage::insert_url, storage::insert_image_file,
	             storage::eject_medium};
}

std::shared_ptr<redfish::Media> storage_media(Drive drive)
{
	auto m = std::make_shared<StorageMedia>();
	m->drive = std::move(drive);
	return m;
}

std::shared_ptr<redfish::Sensors> agent_sensors(std::function<HostSensorsStatus()> status)
{
	auto s = std::make_shared<AgentSensors>();
	s->status = std::move(status);
	return s;
}

// IPMI while it runs: its UDP socket and protocol server, with the settings it started with.
struct Service::Run {
	Config config;
	std::shared_ptr<ipmi::Server> server;
	int fd = -1, wake = -1;
	std::thread thread;
	std::atomic<bool> done{false};
	// The server stops to give the socket to its successor.
	std::atomic<bool> hand_over{false};
	// Ends the serving thread and waits for it.
	void end()
	{
		std::uint64_t one = 1;
		if (wake >= 0 && ::write(wake, &one, sizeof one) < 0) {}
		if (thread.joinable()) thread.join();
		if (wake >= 0) ::close(wake);
		wake = -1;
	}
};

Service::Service(std::string config_path, Atx &atx, std::shared_ptr<redfish::Media> media,
                 std::shared_ptr<redfish::Sensors> sensors, redfish::Info info, std::array<std::uint8_t, 16> guid)
	: config_path_(std::move(config_path)), atx_(atx), media_(std::move(media)), sensors_(std::move(sensors)),
	  info_(std::move(info)), guid_(guid)
{
}

Service::~Service()
{
	closing_ = true;
	if (keeper_.joinable()) keeper_.join();
	std::lock_guard lock(mu_);
	stop_ipmi();
}

bool Service::running() const { return ipmi_ && !ipmi_->done; }

void Service::use_redfish(const Config &config)
{
	std::shared_ptr<redfish::Handler> h;
	if (config.redfish && validate(config).empty()) {
		// 0: the long press.
		auto power = std::make_unique<Power>(atx_, std::chrono::seconds(config.hold_seconds), "Redfish");
		h = std::make_shared<redfish::Handler>(config.username, config.password, info_, std::move(power));
		// The security events cover the BMC account as the web login.
		h->on_lockout = [](const std::string &addr, int failures, std::chrono::seconds lock) {
			notify_login_locked_out(addr, failures, static_cast<int>((lock.count() + 59) / 60));
		};
		// Its own list, apart from a web user of the same name.
		h->on_login = [](const std::string &user, const std::string &addr) {
			loop::blocking([user, addr] { notify_login_succeeded(user + " (Redfish)", addr); });
		};
		h->sensors = sensors_;
		if (config.virtual_media && media_) h->media = media_;
		if (on_redfish) on_redfish(*h);
	}
	std::lock_guard lock(redfish_mu_);
	redfish_ = std::move(h);
}

std::shared_ptr<redfish::Handler> Service::redfish()
{
	std::lock_guard lock(redfish_mu_);
	return redfish_;
}

int Service::ipmi_socket(const Config &config, std::string &error)
{
	if (!config.ipmi || (running() && ipmi_->config.ipmi_port == config.ipmi_port)) return -1;
	auto fail = [&](const char *op, int err) {
		error = "IPMI cannot use UDP port " + std::to_string(config.ipmi_port) + ": listen udp :" +
		        std::to_string(config.ipmi_port) + ": " + op + ": " + network::errno_text(err);
		return -2;
	};
	int fd = ::socket(AF_INET6, SOCK_DGRAM | SOCK_CLOEXEC, 0);
	if (fd < 0) return fail("socket", errno);
	int zero = 0, one = 1;
	::setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &zero, sizeof zero);
	::setsockopt(fd, SOL_SOCKET, SO_BROADCAST, &one, sizeof one);
	sockaddr_in6 a{};
	a.sin6_family = AF_INET6;
	a.sin6_port = htons(static_cast<std::uint16_t>(config.ipmi_port));
	a.sin6_addr = in6addr_any;
	if (::bind(fd, reinterpret_cast<sockaddr *>(&a), sizeof a) != 0) {
		int err = errno;
		::close(fd);
		return fail("bind", err);
	}
	return fd;
}

void Service::serve_ipmi(const Config &config, int fd)
{
	auto [major, minor] = version_bytes(info_.firmware_version);
	auto server = std::make_shared<ipmi::Server>();
	server->username = config.username;
	server->password = config.password;
	server->guid = guid_;
	server->firmware_major = major;
	server->firmware_minor = minor;
	server->log = [](const std::string &m) { log::warn("{}", m); };
	server->power = std::make_shared<Power>(atx_, std::chrono::seconds(config.hold_seconds), "IPMI");
	server->on_event = [](const ipmi::Event &e) {
		// IPMI counts every login, failed or not: its own message.
		if (e.kind == "lockout") notify_login_attempts_limited(e.address, e.attempts, e.minutes);
		// Its own list, apart from a web or Redfish user of the same name.
		else if (e.kind == "login") notify_login_succeeded(e.user + " (IPMI)", e.address);
	};
	if (on_ipmi) on_ipmi(*server);
	auto run = std::make_unique<Run>();
	run->config = config;
	run->server = server;
	run->fd = fd;
	run->wake = ::eventfd(0, EFD_CLOEXEC);
	Run *r = run.get();
	r->thread = std::thread([r] {
		std::vector<std::uint8_t> buf(1024);
		std::string failure;
		for (;;) {
			pollfd fds[2] = {{r->fd, POLLIN, 0}, {r->wake, POLLIN, 0}};
			if (::poll(fds, 2, -1) < 0) {
				if (errno == EINTR) continue;
				failure = network::errno_text(errno);
				break;
			}
			if (fds[1].revents) break;
			if (!fds[0].revents) continue;
			sockaddr_in6 from{};
			socklen_t len = sizeof from;
			ssize_t n = ::recvfrom(r->fd, buf.data(), buf.size(), 0, reinterpret_cast<sockaddr *>(&from), &len);
			if (n < 0) {
				if (errno == EINTR || errno == EAGAIN) continue;
				failure = "read udp: " + network::errno_text(errno);
				break;
			}
			ipmi::Bytes reply = r->server->handle(ipmi::Bytes(buf.begin(), buf.begin() + n), address_string(from));
			if (!reply.empty()) ::sendto(r->fd, reply.data(), reply.size(), MSG_NOSIGNAL, reinterpret_cast<sockaddr *>(&from), len);
		}
		r->server->finish();
		if (!r->hand_over && !failure.empty()) {
			// The socket failed: free the port.
			::close(r->fd);
			r->fd = -1;
			log::error("IPMI stopped: {}", failure);
		}
		r->done = true;
	});
	ipmi_ = std::move(run);
	log::info("IPMI: listening on UDP port {}", config.ipmi_port);
}

void Service::stop_ipmi()
{
	if (!ipmi_) return;
	ipmi_->end();
	if (ipmi_->fd >= 0) ::close(ipmi_->fd);
	ipmi_.reset();
}

// IPMI that runs with the same account, port and hold keeps its sessions; a new account or hold on the same port
// takes over the socket.
void Service::apply_ipmi(const Config &config, int fd)
{
	if (!config.ipmi) {
		stop_ipmi();
	} else if (fd >= 0) {
		stop_ipmi();
		serve_ipmi(config, fd);
	} else if (ipmi_ && !same_ipmi(ipmi_->config, config)) {
		ipmi_->hand_over = true;
		ipmi_->end();
		int kept = ipmi_->fd;
		ipmi_.reset();
		serve_ipmi(config, kept);
	}
}

bool Service::status(std::string &json, std::string &error)
{
	std::lock_guard lock(mu_);
	Config config = load(config_path_, error);
	if (!error.empty()) return false;
	bool run = running();
	json = "{\"redfish\":" + std::string(config.redfish ? "true" : "false");
	json += ",\"ipmi\":" + std::string(config.ipmi ? "true" : "false");
	json += ",\"ipmiRunning\":" + std::string(run ? "true" : "false");
	if (run) {
		ipmi::Stats s = ipmi_->server->stats();
		json += ",\"ipmiDaemon\":{\"port\":" + std::to_string(ipmi_->config.ipmi_port) + ",\"sessions\":" + std::to_string(s.sessions) +
		        ",\"lockedOut\":" + std::to_string(s.locked_out) + ",\"lastCommand\":" + gojson::quote(s.last_command);
		if (s.last_command_at)
			json += ",\"lastCommandAt\":" +
			        gojson::quote(json_time(s.last_command_at));
		json += "}";
	}
	json += ",\"username\":" + gojson::quote(config.username);
	json += ",\"passwordSet\":" + std::string(config.password.empty() ? "false" : "true");
	json += ",\"ipmiPort\":" + std::to_string(config.ipmi_port);
	json += ",\"holdSeconds\":" + std::to_string(config.hold_seconds);
	json += ",\"powerLedConnected\":" + std::string(config.power_led_disconnected ? "false" : "true");
	json += ",\"virtualMedia\":" + std::string(config.virtual_media ? "true" : "false") + "}";
	return true;
}

std::string Service::configure(const Settings &settings)
{
	std::lock_guard lock(mu_);
	std::string error;
	Config config = load(config_path_, error);
	if (!error.empty()) return error;
	config.redfish = settings.redfish;
	config.ipmi = settings.ipmi;
	config.username = settings.username;
	if (!settings.password.empty()) config.password = settings.password;
	if (settings.ipmi_port != 0) config.ipmi_port = settings.ipmi_port;
	config.hold_seconds = settings.hold_seconds;
	if (settings.power_led_connected) config.power_led_disconnected = !*settings.power_led_connected;
	if (settings.virtual_media) config.virtual_media = *settings.virtual_media;
	if (auto e = validate(config); !e.empty()) return e;
	// The new IPMI port is taken before anything changes: a port that cannot be used leaves everything as it was.
	int fd = ipmi_socket(config, error);
	if (fd == -2) return error;
	if (auto e = save(config_path_, config); !e.empty()) {
		if (fd >= 0) ::close(fd);
		return e;
	}
	use_redfish(config);
	apply_ipmi(config, fd);
	return {};
}

void Service::keep_ipmi()
{
	std::string error;
	{
		std::lock_guard lock(mu_);
		Config config = load(config_path_, error);
		if (error.empty()) use_redfish(config);
		else log::error("BMC settings: {}", error);
		if (!error.empty() || !config.ipmi) return;
	}
	// One attempt now; while the port is taken, the next ones every ipmi_retry on a thread of its own.
	auto attempt = [this](std::string &error) {
		std::lock_guard lock(mu_);
		Config config = load(config_path_, error);
		if (error.empty()) error = validate(config);
		if (!error.empty() || !config.ipmi || running()) return true;
		int fd = ipmi_socket(config, error);
		if (fd == -2) return false;
		apply_ipmi(config, fd);
		return true;
	};
	if (attempt(error)) return;
	log::error("{}; trying again", error);
	keeper_ = std::thread([this, attempt] {
		for (;;) {
			for (int i = 0; i < 50 && !closing_; i++) std::this_thread::sleep_for(ipmi_retry / 50);
			std::string error;
			if (closing_ || attempt(error)) return;
		}
	});
}

namespace {

Service &service()
{
	static Service s(config_path, board_atx(), storage_media(storage_drive()), agent_sensors([] { return host_sensors_status(); }),
	                 redfish::Info{device_uuid(read_text("/etc/machine-id")), firmware_version(read_text("/etc/nanokvm-release"))},
	                 device_guid(read_text("/etc/machine-id")));
	return s;
}

// router/bmc.go's PUT /api/bmc/config body (ShouldBindJSON).
std::optional<Settings> bind_settings(const http::Request &req)
{
	auto b = network::Binder::bind(network::request_data(req), true);
	Settings s;
	bool flag = false;
	if (b.ok()) {
		b.boolean("redfish", "", s.redfish);
		b.boolean("ipmi", "", s.ipmi);
		b.str("username", "", s.username);
		b.str("password", "", s.password);
		b.integer("ipmiPort", "", s.ipmi_port);
		b.integer("holdSeconds", "", s.hold_seconds);
		if (b.boolean("powerLedConnected", "", flag)) s.power_led_connected = flag;
		if (b.boolean("virtualMedia", "", flag)) s.virtual_media = flag;
	}
	if (!b.ok()) return std::nullopt;
	return s;
}

} // namespace

void add_routes(http::Router &router)
{
	Service &s = service();
	// Redfish checks the BMC account itself: these paths take no web login.
	router.use([&s](http::Request &req, http::Response &res) {
		if (req.path != "/redfish" && !req.path.starts_with("/redfish/")) return;
		redfish::Request r;
		r.method = req.method;
		r.path = req.path;
		r.body = req.body;
		r.peer = req.peer;
		for (const char *name : {"x-auth-token", "authorization", "sec-fetch-site", "content-type"})
			if (auto v = req.header(name); !v.empty()) r.headers[name] = v;
		http::defer(req, res, [&s, r = std::move(r)](http::Response &out) {
			auto handler = s.redfish();
			if (!handler) {
				// With Redfish off the service does not exist.
				out.set_header("X-Content-Type-Options", "nosniff");
				out.text(404, "404 page not found\n");
				return;
			}
			redfish::Response w = handler->serve(r);
			out.status = w.status;
			for (const auto &[name, value] : w.headers) {
				if (name == "Content-Type") out.content_type = value;
				else out.set_header(name, value);
			}
			out.body = std::move(w.body);
		});
	});
	router.add("GET", "/api/bmc/status", auth::guarded([&s](http::Request &req, http::Response &res) {
		res.set_header("Cache-Control", "no-store");
		http::defer(req, res, [&s](http::Response &out) {
			std::string json, error;
			if (!s.status(json, error)) return network::fail(out, -1, error);
			network::ok(out, json);
		});
	}, auth::Guard{{"admin"}}));
	router.add("PUT", "/api/bmc/config", auth::guarded([&s](http::Request &req, http::Response &res) {
		auto settings = bind_settings(req);
		if (!settings) return network::fail(res, -1, "invalid configuration");
		http::defer(req, res, [&s, settings = *settings](http::Response &out) {
			if (auto e = s.configure(settings); !e.empty()) return network::fail(out, -1, e);
			network::ok(out);
		});
	}, auth::Guard{{"admin"}}));
	s.keep_ipmi();
}

} // namespace nkos::bmc
