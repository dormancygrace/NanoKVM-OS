// Network boot, as the Go server's service/storage/netboot.go and netboot_release.go: the official iPXE CD image
// goes on the virtual drive, the NanoKVM's DHCP server on USB network gives iPXE (and only iPXE) the address of a
// one-screen script on usb0, and that script loads the netboot.xyz menus of the chosen release over USB internet
// sharing. Both releases come from GitHub; every iPXE image is checked against the SHA-256 GitHub publishes for
// it. Network boot is a runtime state, like a mount: a restart ends it and the next server start undoes what it
// changed. Also the part of usbinternet's runtime it reads and writes (the menu port, the sharing flag, the status).

#include "storage/internal.hpp"
#include "usb/usbinternet.hpp"
#include "storage/storage.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/http_client.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"

#include <glaze/glaze.hpp>

#include <arpa/inet.h>
#include <cerrno>
#include <csignal>
#include <cstring>
#include <ctime>
#include <dirent.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::storage {

namespace {

constexpr int netboot_port = 8070;
constexpr const char *ipxe_image = "ipxe.iso";             // BIOS and UEFI, every architecture, no Secure Boot
constexpr const char *ipxe_sb_image = "ipxe-x86_64-sb.iso";  // x86-64 UEFI, signed for Secure Boot
constexpr std::int64_t ipxe_max_bytes = 64 << 20;
const std::string github_api = "https://api.github.com";
const std::string ipxe_download_base = "https://github.com/ipxe/ipxe/releases/download";
const std::string netbootxyz_menu_base = "https://boot.netboot.xyz";

bool digits(std::string_view s)
{
	if (s.empty()) return false;
	for (char c : s)
		if (c < '0' || c > '9') return false;
	return true;
}

// ^[0-9]+\.[0-9]+\.[0-9]+$
bool version(std::string_view s)
{
	auto a = s.find('.');
	if (a == std::string_view::npos) return false;
	auto b = s.find('.', a + 1);
	if (b == std::string_view::npos) return false;
	return digits(s.substr(0, a)) && digits(s.substr(a + 1, b - a - 1)) && digits(s.substr(b + 1));
}

bool sha256_digest(const std::string &s, std::string &hex)
{
	if (s.size() != 71 || !s.starts_with("sha256:")) return false;
	for (char c : s.substr(7))
		if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) return false;
	hex = s.substr(7);
	return true;
}

std::string state_path() { return netboot_dir + "/state.json"; }

std::string ipxe_image_path(const std::string &tag, const std::string &name) { return netboot_dir + "/ipxe-" + tag + "/" + name; }

std::string image_for(bool secure_boot) { return secure_boot ? ipxe_sb_image : ipxe_image; }

// os.MkdirAll.
Error mkdir_all(const std::string &path, unsigned mode)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) {
		if (S_ISDIR(st.st_mode)) return {};
		return Error::path("mkdir", path, ENOTDIR);
	}
	std::string parent = go::dir(path);
	if (parent != path && parent != "/" && parent != ".")
		if (auto e = mkdir_all(parent, mode)) return e;
	if (::mkdir(path.c_str(), mode) != 0 && errno != EEXIST) return Error::path("mkdir", path, errno);
	return {};
}

Error write_file_atomic(const std::string &path, const std::string &data)
{
	if (auto e = mkdir_all(go::dir(path), 0700)) return e;
	int fd;
	std::string temp;
	if (auto e = go::create_temp(go::dir(path), ".netboot-", fd, temp)) return e;
	Error err;
	for (std::size_t off = 0; off < data.size() && !err;) {
		ssize_t n = ::write(fd, data.data() + off, data.size() - off);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) err = Error::path("write", temp, n < 0 ? errno : EIO);
		else off += static_cast<std::size_t>(n);
	}
	if (!err && ::fsync(fd) != 0) err = Error::path("sync", temp, errno);
	if (::close(fd) != 0 && !err) err = Error::path("close", temp, errno);
	if (!err) err = go::rename(temp, path);
	::unlink(temp.c_str());
	return err;
}

struct StateJson {
	std::optional<std::string> ipxe;
	std::optional<std::map<std::string, std::string>> ipxeFiles;
	std::optional<std::string> netbootxyz, checkedAt;
	std::optional<bool> secureBoot, sharingByNetboot;
};

struct AssetJson {
	std::optional<std::string> name, digest;
	std::optional<std::int64_t> size;
};
struct ReleaseJson {
	std::optional<std::string> tag_name;
	std::optional<bool> draft, prerelease;
	std::optional<std::vector<AssetJson>> assets;
};

} // namespace

} // namespace nkos::storage

template <>
struct glz::meta<nkos::storage::StateJson> {
	using T = nkos::storage::StateJson;
	static constexpr auto value = object("ipxe", &T::ipxe, "ipxeFiles", &T::ipxeFiles, "netbootxyz", &T::netbootxyz,
	                                     "checkedAt", &T::checkedAt, "secureBoot", &T::secureBoot, "sharingByNetboot",
	                                     &T::sharingByNetboot);
};
template <>
struct glz::meta<nkos::storage::AssetJson> {
	using T = nkos::storage::AssetJson;
	static constexpr auto value = object("name", &T::name, "size", &T::size, "digest", &T::digest);
};
template <>
struct glz::meta<nkos::storage::ReleaseJson> {
	using T = nkos::storage::ReleaseJson;
	static constexpr auto value =
		object("tag_name", &T::tag_name, "draft", &T::draft, "prerelease", &T::prerelease, "assets", &T::assets);
};
namespace nkos::storage {

bool ipxe_tag(const std::string &s) { return s.size() > 1 && s[0] == 'v' && version(std::string_view(s).substr(1)); }
bool netbootxyz_tag(const std::string &s) { return version(s); }

Error load_netboot_state(NetbootState &st)
{
	st = {};
	std::string data;
	if (auto e = go::read_file(state_path(), data)) return e.sys == ENOENT ? Error{} : e;
	StateJson j;
	auto a = data.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return Error::of("read network boot state: unexpected end of JSON input");
	if (go::trim_space(data) == "null") return {};
	if (data[a] != '{' || glz::read<glz::opts{.error_on_unknown_keys = false}>(j, data))
		return Error::of("read network boot state: invalid JSON");
	st.ipxe = j.ipxe.value_or("");
	st.ipxe_files = j.ipxeFiles;
	st.netbootxyz = j.netbootxyz.value_or("");
	st.checked_at = j.checkedAt.value_or("");
	st.secure_boot = j.secureBoot.value_or(false);
	st.sharing_by_netboot = j.sharingByNetboot.value_or(false);
	if (!st.ipxe.empty() && !ipxe_tag(st.ipxe)) {
		st.ipxe.clear();
		st.ipxe_files.reset();
	}
	if (!st.netbootxyz.empty() && !netbootxyz_tag(st.netbootxyz)) st.netbootxyz.clear();
	return {};
}

std::string netboot_state_json(const NetbootState &st)
{
	std::string files = "null";
	if (st.ipxe_files) {
		if (st.ipxe_files->empty()) {
			files = "{}";
		} else {
			files = "{";
			bool first = true;
			for (const auto &[k, v] : *st.ipxe_files) {
				files += std::string(first ? "" : ",") + "\n    " + gojson::quote(k) + ": " + gojson::quote(v);
				first = false;
			}
			files += "\n  }";
		}
	}
	return "{\n  \"ipxe\": " + gojson::quote(st.ipxe) + ",\n  \"ipxeFiles\": " + files + ",\n  \"netbootxyz\": " +
	       gojson::quote(st.netbootxyz) + ",\n  \"checkedAt\": " + gojson::quote(st.checked_at) + ",\n  \"secureBoot\": " +
	       (st.secure_boot ? "true" : "false") + ",\n  \"sharingByNetboot\": " + (st.sharing_by_netboot ? "true" : "false") +
	       "\n}";
}

namespace {

Error save_netboot_state(const NetbootState &st) { return write_file_atomic(state_path(), netboot_state_json(st) + "\n"); }

} // namespace

std::string netboot_script(const std::string &tag)
{
	return "#!ipxe\n"
	       "# NanoKVM network boot: the netboot.xyz " + tag + " menus.\n"
	       "set site_name netboot.xyz\n"
	       "set boot_domain boot.netboot.xyz/" + tag + "\n"
	       "set ipxe_version ${version}\n"
	       "set version " + tag + "\n"
	       "chain --autofree https://${boot_domain}/menu.ipxe || chain --autofree http://${boot_domain}/menu.ipxe || goto failed\n"
	       ":failed\n"
	       "echo netboot.xyz is not reachable through the NanoKVM. Check its internet connection.\n"
	       "prompt --timeout 30000 Press any key for the iPXE shell... ||\n"
	       "shell\n";
}

Error parse_github_release(const std::string &repo, const std::string &body, GithubRelease &release)
{
	ReleaseJson j;
	auto a = body.find_first_not_of(" \t\r\n");
	if (a == std::string::npos || body[a] != '{' || glz::read<glz::opts{.error_on_unknown_keys = false}>(j, body))
		return Error::of("unexpected answer from GitHub for " + repo);
	if (j.draft.value_or(false) || j.prerelease.value_or(false))
		return Error::of(repo + ": the latest release is not a stable release");
	release.tag = go::trim_space(j.tag_name.value_or(""));
	release.assets.clear();
	if (j.assets)
		for (const auto &asset : *j.assets)
			release.assets.push_back({asset.name.value_or(""), asset.digest.value_or(""), asset.size.value_or(0)});
	return {};
}

Error ipxe_digests(const GithubRelease &rel, std::map<std::string, std::string> &want)
{
	want.clear();
	if (!ipxe_tag(rel.tag)) return Error::of("unexpected iPXE release " + go_quote(rel.tag));
	for (const auto &a : rel.assets) {
		if (a.name != ipxe_image && a.name != ipxe_sb_image) continue;
		std::string hex;
		if (!sha256_digest(a.digest, hex)) return Error::of("iPXE " + rel.tag + ": GitHub publishes no SHA-256 for " + a.name);
		if (a.size <= 0 || a.size > ipxe_max_bytes) return Error::of("iPXE " + rel.tag + ": unexpected size of " + a.name);
		want[a.name] = hex;
	}
	for (const char *name : {ipxe_image, ipxe_sb_image})
		if (want[name].empty()) return Error::of("iPXE " + rel.tag + " has no " + name);
	return {};
}

// --- usbinternet's runtime ---

std::string usb_flags_boot()
{
	return go::exists(usb_flags_layout_app) && go::exists(usb_flags_layout_active) ? usb_flags_layout_dir : usb_flags_legacy_dir;
}

usb::usbinternet::Runtime usbnet_runtime();

int netboot_port_file() { return usb::usbinternet::netboot_port(usbnet_runtime()); }

namespace {

Error set_netboot_port(int port)
{
	if (auto e = usb::usbinternet::set_netboot_port(usbnet_runtime(), port); !e.empty()) return Error::of(e);
	return {};
}

bool usb_internet_enabled() { return usb::usbinternet::enabled(usbnet_runtime()); }

} // namespace

std::string prefix_address(const std::string &prefix)
{
	auto slash = prefix.find('/');
	if (slash == std::string::npos) return {};
	std::string addr = prefix.substr(0, slash), bits = prefix.substr(slash + 1);
	if (!digits(bits) || (bits.size() > 1 && bits[0] == '0') || bits.size() > 3) return {};
	int n = std::stoi(bits);
	unsigned char buf[16];
	char text[INET6_ADDRSTRLEN];
	if (addr.find(':') == std::string::npos) {
		if (n > 32 || ::inet_pton(AF_INET, addr.c_str(), buf) != 1) return {};
		return ::inet_ntop(AF_INET, buf, text, sizeof text) ? text : "";
	}
	if (n > 128 || addr.find('%') != std::string::npos || ::inet_pton(AF_INET6, addr.c_str(), buf) != 1) return {};
	return ::inet_ntop(AF_INET6, buf, text, sizeof text) ? text : "";
}

// usbinternet.Runtime over the paths this file reads: one implementation with the server's USB family and
// the nkos-usb-internet helper (usb/usbinternet).
usb::usbinternet::Runtime usbnet_runtime()
{
	auto r = usb::usbinternet::default_runtime();
	r.boot = usb_flags_boot();
	r.run = usbnet_run;
	r.gadget = gadget_dir;
	r.net = usbnet_net;
	return r;
}

usb::usbinternet::Status read_usb_status()
{
	return usb::usbinternet::read_status(usbnet_runtime(), static_cast<long long>(std::time(nullptr)));
}

std::string usb_internet_status_json() { return usb::usbinternet::status_json(read_usb_status()); }
std::string usb_internet_address() { return prefix_address(read_usb_status().address); }

namespace {

Error set_sharing(bool enabled)
{
	std::string path = usb_flags_boot() + "/usb.internet";
	if (!enabled) {
		if (auto e = go::remove(path); e && e.sys != ENOENT) return e;
		return {};
	}
	return go::write_file(path, "", 0644);
}

Error apply_usb_network()
{
	std::lock_guard h(gadget_mutex());
	auto r = process::run({"sh", usb_internet_run, "start"}, {.timeout = std::chrono::seconds(20)});
	if (r.ok()) return {};
	if (!r.started) return Error::of("apply USB network: exec: \"sh\": executable file not found in $PATH");
	return Error::of("apply USB network: " + go::exit_text(r.exit_code, r.timed_out ? SIGKILL : r.signal));
}

// errors.Join: the messages of the errors given, one per line.
Error join(std::initializer_list<Error> errors)
{
	std::string text;
	for (const auto &e : errors)
		if (e) text += (text.empty() ? "" : "\n") + e.text;
	return Error::of(text);
}

// Withdraws the menu port and, when network boot turned it on, USB internet sharing: the cleanup of a disable, a
// failed enable and a start after network boot was left on.
Error undo_netboot(NetbootState &st)
{
	Error port = set_netboot_port(0), sharing;
	if (st.sharing_by_netboot) sharing = set_sharing(false);
	Error apply = apply_usb_network();
	Error err = join({port, sharing, apply});
	if (!err && st.sharing_by_netboot) {
		st.sharing_by_netboot = false;
		err = save_netboot_state(st);
	}
	return err;
}

// --- The menu listener: /boot.ipxe on usb0's address alone ---

class MenuServer {
public:
	MenuServer(std::function<std::string()> script, std::function<void()> fetched)
		: script_(std::move(script)), fetched_(std::move(fetched))
	{
	}
	~MenuServer() { close(); }

	Error listen(const std::string &address)
	{
		std::string where = go::join_host_port(address, std::to_string(netboot_port));
		sockaddr_storage ss{};
		socklen_t len;
		int family;
		if (address.find(':') == std::string::npos) {
			auto *in = reinterpret_cast<sockaddr_in *>(&ss);
			in->sin_family = AF_INET;
			in->sin_port = htons(netboot_port);
			if (::inet_pton(AF_INET, address.c_str(), &in->sin_addr) != 1)
				return Error::of("network boot menu: listen tcp " + where + ": invalid address");
			len = sizeof *in;
			family = AF_INET;
		} else {
			auto *in6 = reinterpret_cast<sockaddr_in6 *>(&ss);
			in6->sin6_family = AF_INET6;
			in6->sin6_port = htons(netboot_port);
			if (::inet_pton(AF_INET6, address.c_str(), &in6->sin6_addr) != 1)
				return Error::of("network boot menu: listen tcp " + where + ": invalid address");
			len = sizeof *in6;
			family = AF_INET6;
		}
		int fd = ::socket(family, SOCK_STREAM | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
		if (fd < 0) return Error::of("network boot menu: socket: " + go::errno_text(errno));
		int one = 1;
		::setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
		if (::bind(fd, reinterpret_cast<sockaddr *>(&ss), len) != 0) {
			int e = errno;
			::close(fd);
			return Error::of("network boot menu: listen tcp " + where + ": bind: " + go::errno_text(e));
		}
		if (::listen(fd, 16) != 0) {
			int e = errno;
			::close(fd);
			return Error::of("network boot menu: listen tcp " + where + ": listen: " + go::errno_text(e));
		}
		if (::pipe2(wake_, O_CLOEXEC | O_NONBLOCK) != 0) {
			::close(fd);
			return Error::of("network boot menu: " + go::errno_text(errno));
		}
		listener_ = fd;
		thread_ = std::thread([this] { run(); });
		return {};
	}

	void close()
	{
		if (!thread_.joinable()) return;
		(void)!::write(wake_[1], "x", 1);
		thread_.join();
		for (auto &c : conns_) ::close(c.fd);
		conns_.clear();
		::close(listener_);
		::close(wake_[0]);
		::close(wake_[1]);
	}

private:
	struct Conn {
		int fd;
		std::string in;
		SteadyClock::time_point request_start, idle_since;
		bool reading = false;
	};

	static std::string http_date()
	{
		std::time_t t = std::time(nullptr);
		std::tm tm{};
		::gmtime_r(&t, &tm);
		char buf[64];
		std::strftime(buf, sizeof buf, "%a, %d %b %Y %H:%M:%S GMT", &tm);
		return buf;
	}

	// Answers the complete requests in c.in; false closes the connection.
	bool serve(Conn &c)
	{
		for (;;) {
			auto end = c.in.find("\r\n\r\n");
			if (end == std::string::npos) return c.in.size() <= (1u << 20);
			std::string head = c.in.substr(0, end);
			c.in.erase(0, end + 4);
			c.reading = !c.in.empty();
			c.request_start = c.idle_since = SteadyClock::now();
			auto eol = head.find("\r\n");
			auto line = go::fields(head.substr(0, eol));
			if (line.size() != 3) return false;
			const std::string &method = line[0], &version = line[2];
			std::string path = line[1].substr(0, line[1].find('?'));
			bool keep = version == "HTTP/1.1";
			std::int64_t body = 0;
			for (std::size_t at = eol; at != std::string::npos && at < head.size();) {
				auto next = head.find("\r\n", at + 2);
				std::string h = head.substr(at + 2, next == std::string::npos ? std::string::npos : next - at - 2);
				at = next;
				auto colon = h.find(':');
				if (colon == std::string::npos) continue;
				std::string name = go::to_lower(h.substr(0, colon)), value = go::to_lower(go::trim_space(h.substr(colon + 1)));
				if (name == "connection") keep = value == "close" ? false : (value == "keep-alive" ? true : keep);
				if (name == "content-length" && digits(value) && value.size() < 18) body = std::stoll(value);
			}
			if (body > 0) keep = false;
			std::string status, type = "text/plain; charset=utf-8", text, extra;
			if (path == "/boot.ipxe") {
				if (method != "GET" && method != "HEAD") {
					status = "405 Method Not Allowed";
					text = "method not allowed\n";
					extra = "X-Content-Type-Options: nosniff\r\n";
				} else {
					fetched_();
					status = "200 OK";
					text = script_();
					extra = "Cache-Control: no-store\r\n";
				}
			} else {
				status = "404 Not Found";
				text = "404 page not found\n";
				extra = "X-Content-Type-Options: nosniff\r\n";
			}
			std::string out = "HTTP/1.1 " + status + "\r\n" + extra + "Content-Length: " + std::to_string(text.size()) +
			                  "\r\nContent-Type: " + type + "\r\nDate: " + http_date() + "\r\n" +
			                  (keep ? "" : "Connection: close\r\n") + "\r\n";
			if (method != "HEAD") out += text;
			for (std::size_t off = 0; off < out.size();) {
				pollfd p{c.fd, POLLOUT, 0};
				if (::poll(&p, 1, 10000) <= 0) return false;
				ssize_t n = ::send(c.fd, out.data() + off, out.size() - off, MSG_NOSIGNAL);
				if (n < 0 && (errno == EINTR || errno == EAGAIN)) continue;
				if (n <= 0) return false;
				off += static_cast<std::size_t>(n);
			}
			if (!keep) return false;
		}
	}

	void run()
	{
		for (;;) {
			std::vector<pollfd> fds{{wake_[0], POLLIN, 0}, {listener_, POLLIN, 0}};
			for (auto &c : conns_) fds.push_back({c.fd, POLLIN, 0});
			if (::poll(fds.data(), fds.size(), 1000) < 0 && errno != EINTR) return;
			if (fds[0].revents) return;
			auto now = SteadyClock::now();
			// The connections polled now; one accepted below is polled from the next turn on.
			for (std::size_t i = 0; i < conns_.size(); i++) {
				Conn &c = conns_[i];
				bool keep = true;
				if (fds[i + 2].revents) {
					char buf[4096];
					ssize_t n = ::recv(c.fd, buf, sizeof buf, 0);
					if (n <= 0) {
						keep = n < 0 && (errno == EAGAIN || errno == EINTR);
					} else {
						if (!c.reading) {
							c.reading = true;
							c.request_start = now;
						}
						c.in.append(buf, static_cast<std::size_t>(n));
						keep = serve(c);
					}
				}
				// ReadHeaderTimeout 10 s for a request begun, IdleTimeout 30 s between requests.
				if (keep && c.reading && now - c.request_start > std::chrono::seconds(10)) keep = false;
				if (keep && !c.reading && now - c.idle_since > std::chrono::seconds(30)) keep = false;
				if (!keep) {
					::close(c.fd);
					conns_.erase(conns_.begin() + static_cast<std::ptrdiff_t>(i));
					fds.erase(fds.begin() + static_cast<std::ptrdiff_t>(i) + 2);
					i--;
				}
			}
			if (fds[1].revents & POLLIN) {
				int fd = ::accept4(listener_, nullptr, nullptr, SOCK_CLOEXEC | SOCK_NONBLOCK);
				if (fd >= 0) conns_.push_back({fd, {}, now, now, false});
			}
		}
	}

	std::function<std::string()> script_;
	std::function<void()> fetched_;
	int listener_ = -1, wake_[2] = {-1, -1};
	std::vector<Conn> conns_;
	std::thread thread_;
};

} // namespace

struct NetbootSession {
	std::string image;
	bool secure_boot = false;
	std::mutex tag_mu;
	std::string netbootxyz;
	std::string address;
	std::unique_ptr<MenuServer> server;
	std::atomic<std::int64_t> fetches{0}, last_fetch{0};
	Event stop;

	std::string tag()
	{
		std::lock_guard lock(tag_mu);
		return netbootxyz;
	}
	void set_tag(const std::string &t)
	{
		std::lock_guard lock(tag_mu);
		netbootxyz = t;
	}
	// Serves the script on address:8070 only, never on all addresses.
	Error listen(const std::string &addr)
	{
		auto next = std::make_unique<MenuServer>([this] { return netboot_script(tag()); },
		                                         [this] {
			                                         fetches++;
			                                         last_fetch = static_cast<std::int64_t>(std::time(nullptr));
		                                         });
		if (auto e = next->listen(addr)) return e;
		if (server) server->close();
		server = std::move(next);
		address = addr;
		return {};
	}
	void close()
	{
		stop.fire();
		if (server) server->close();
	}
};

namespace {

bool usb_network_active()
{
	struct stat st{};
	return ::lstat(usb_network_link.c_str(), &st) == 0 && S_ISLNK(st.st_mode);
}

// The menu follows USB networking to another subnet (it moves on a conflict with another network).
void follow(std::shared_ptr<NetbootSession> n)
{
	std::thread([n] {
		while (!n->stop.wait_until(SteadyClock::now() + std::chrono::seconds(5))) {
			std::string address = usb_internet_address();
			Service &s = service();
			std::lock_guard lock(s.mu);
			if (s.netboot == n && !address.empty() && address != n->address) {
				if (auto e = n->listen(address)) log::warn("network boot: {}", e.text);
				else log::info("network boot menu moved to {}", address);
			}
		}
	}).detach();
}

std::string status_json(Error *load_error = nullptr)
{
	NetbootState st;
	Error err = load_netboot_state(st);
	if (load_error) *load_error = err;
	Service &s = service();
	std::lock_guard lock(s.mu);
	bool enabled = false, secure = st.secure_boot;
	std::string menu;
	std::int64_t fetches = 0, last = 0;
	if (auto n = s.netboot) {
		enabled = true;
		secure = n->secure_boot;
		menu = "http://" + go::join_host_port(n->address, std::to_string(netboot_port)) + "/boot.ipxe";
		fetches = n->fetches;
		last = n->last_fetch;
	}
	auto b = [](bool v) { return v ? "true" : "false"; };
	std::string out = "{\"enabled\":" + std::string(b(enabled)) + ",\"secureBoot\":" + b(secure) + ",\"ipxe\":" +
	                  gojson::quote(st.ipxe) + ",\"netbootxyz\":" + gojson::quote(st.netbootxyz) + ",\"checkedAt\":" +
	                  gojson::quote(st.checked_at);
	if (!menu.empty()) out += ",\"menu\":" + gojson::quote(menu);
	out += ",\"menuFetches\":" + std::to_string(fetches);
	if (last != 0) out += ",\"lastFetch\":" + std::to_string(last);
	out += ",\"usbNetwork\":" + std::string(b(usb_network_active())) + ",\"virtualDisk\":" +
	       b(!require_active_usb_storage()) + ",\"usbInternet\":" + usb_internet_status_json() + ",\"browserMedia\":" +
	       b(s.remote != nullptr) + "}";
	return out;
}

// Whether a request's client is still there, asked from other threads (the loop answers once a second).
struct RequestWatch {
	std::atomic<bool> gone{false}, finished{false};
};
std::shared_ptr<RequestWatch> watch_request(std::uint64_t id)
{
	auto w = std::make_shared<RequestWatch>();
	auto tick = std::make_shared<std::function<void()>>();
	*tick = [w, id, weak = std::weak_ptr<std::function<void()>>(tick)] {
		if (w->finished) return;
		if (!http::request_open(id)) {
			w->gone = true;
			return;
		}
		if (auto t = weak.lock()) loop::after(std::chrono::seconds(1), [t] { (*t)(); });
	};
	loop::after(std::chrono::seconds(1), [tick] { (*tick)(); });
	return w;
}

using Canceled = std::function<bool()>;

bool fetch_progress(const Canceled &canceled, SteadyClock::time_point deadline)
{
	return !(canceled && canceled()) && SteadyClock::now() < deadline;
}

Error latest_github_release(const std::string &repo, const Canceled &canceled, SteadyClock::time_point deadline,
                            GithubRelease &release)
{
	http_client::Request req;
	req.url = github_api + "/repos/" + repo + "/releases/latest";
	req.headers = {{"Accept", "application/vnd.github+json"}, {"User-Agent", "NanoKVM-OS"}};
	req.timeout = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - SteadyClock::now());
	req.max_body = 4u << 20;
	req.progress = [&](std::int64_t, std::int64_t) { return fetch_progress(canceled, deadline); };
	auto rsp = http_client::perform(req);
	if (rsp.status == 0) return Error::of("GitHub is not reachable");
	if (rsp.status != 200) return Error::of("GitHub answered " + std::to_string(rsp.status) + " for " + repo);
	if (rsp.too_large) return Error::of("unexpected answer from GitHub for " + repo);
	return parse_github_release(repo, rsp.body, release);
}

// Fetches url into path, which it takes only when its SHA-256 is digest. A partial or wrong file never does.
Error download_verified(const std::string &url, const std::string &path, const std::string &digest, const Canceled &canceled,
                        SteadyClock::time_point deadline)
{
	std::string name = go::base(path);
	if (auto e = mkdir_all(go::dir(path), 0700)) return e;
	int fd;
	std::string temp;
	if (auto e = go::create_temp(go::dir(path), ".download-", fd, temp)) return e;
	Sha256 hash;
	std::int64_t n = 0;
	Error write_err;
	http_client::Request req;
	req.url = url;
	req.headers = {{"User-Agent", "NanoKVM-OS"}};
	req.timeout = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - SteadyClock::now());
	int status = 0;
	req.on_headers = [&](int code, const auto &) {
		status = code;
		return code == 200;
	};
	req.sink = [&](std::string_view data) {
		std::size_t take = static_cast<std::size_t>(std::min<std::int64_t>(static_cast<std::int64_t>(data.size()), ipxe_max_bytes + 1 - n));
		data = data.substr(0, take);
		for (std::size_t off = 0; off < data.size();) {
			ssize_t w = ::write(fd, data.data() + off, data.size() - off);
			if (w < 0 && errno == EINTR) continue;
			if (w <= 0) {
				write_err = Error::path("write", temp, w < 0 ? errno : EIO);
				return false;
			}
			off += static_cast<std::size_t>(w);
		}
		hash.update(data);
		n += static_cast<std::int64_t>(data.size());
		return n <= ipxe_max_bytes;
	};
	req.progress = [&](std::int64_t, std::int64_t) { return fetch_progress(canceled, deadline); };
	auto rsp = http_client::perform(req);
	auto fail = [&](Error e) {
		::close(fd);
		::unlink(temp.c_str());
		return e;
	};
	if (rsp.status == 0 && status == 0) return fail(Error::of("download " + name + ": " + (rsp.error.empty() ? "failed" : rsp.error)));
	if (status != 200) return fail(Error::of("download " + name + ": server answered " + std::to_string(status)));
	if (write_err) return fail(write_err);
	if (n > ipxe_max_bytes) return fail(Error::of("download " + name + ": larger than " + std::to_string(ipxe_max_bytes) + " bytes"));
	if (!rsp.error.empty() && !rsp.stopped) return fail(Error::of("download " + name + ": " + rsp.error));
	if (::fsync(fd) != 0) return fail(Error::path("sync", temp, errno));
	if (::close(fd) != 0) {
		::unlink(temp.c_str());
		return Error::path("close", temp, errno);
	}
	std::string got = Sha256::hex(hash.digest());
	if (got != digest) {
		::unlink(temp.c_str());
		return Error::of("download " + name + ": SHA-256 " + got + " does not match the published " + digest);
	}
	auto e = go::rename(temp, path);
	::unlink(temp.c_str());
	return e;
}

// Checks an installed image again before it goes on the drive.
Error verify_file(const std::string &path, const std::string &digest)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return Error::path("open", path, errno);
	Sha256 hash;
	std::vector<char> buf(256 << 10);
	for (;;) {
		ssize_t n = ::read(fd, buf.data(), buf.size());
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			int e = errno;
			::close(fd);
			return Error::path("read", path, e);
		}
		if (n == 0) break;
		hash.update(std::string_view(buf.data(), static_cast<std::size_t>(n)));
	}
	::close(fd);
	if (Sha256::hex(hash.digest()) != digest)
		return Error::of(go::base(path) + " is damaged; update network boot to download it again");
	return {};
}

Error netbootxyz_available(const std::string &tag, const Canceled &canceled, SteadyClock::time_point deadline)
{
	http_client::Request req;
	req.url = netbootxyz_menu_base + "/" + tag + "/menu.ipxe";
	req.headers = {{"User-Agent", "NanoKVM-OS"}};
	req.timeout = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - SteadyClock::now());
	std::string head;
	int status = 0;
	req.on_headers = [&](int code, const auto &) {
		status = code;
		return true;
	};
	req.sink = [&](std::string_view data) {
		head.append(data.substr(0, 64 - std::min<std::size_t>(64, head.size())));
		return head.size() < 64;
	};
	req.progress = [&](std::int64_t, std::int64_t) { return fetch_progress(canceled, deadline); };
	auto rsp = http_client::perform(req);
	if (status == 0) return Error::of("boot.netboot.xyz is not reachable");
	if (status != 200 || !head.starts_with("#!ipxe")) return Error::of("boot.netboot.xyz does not serve the " + tag + " menus");
	return {};
}

// Installs the latest iPXE images and picks the latest netboot.xyz release. The old iPXE files stay: callers remove
// them (remove_old_ipxe) once nothing has them on the drive.
Error update_netboot_releases(const NetbootState &st, NetbootState &next, const Canceled &canceled,
                              SteadyClock::time_point deadline)
{
	GithubRelease ipxe_rel, xyz_rel;
	if (auto e = latest_github_release("ipxe/ipxe", canceled, deadline, ipxe_rel)) return e;
	std::map<std::string, std::string> digests;
	if (auto e = ipxe_digests(ipxe_rel, digests)) return e;
	if (auto e = latest_github_release("netbootxyz/netboot.xyz", canceled, deadline, xyz_rel)) return e;
	if (!netbootxyz_tag(xyz_rel.tag)) return Error::of("unexpected netboot.xyz release " + go_quote(xyz_rel.tag));
	if (auto e = netbootxyz_available(xyz_rel.tag, canceled, deadline)) return e;
	next = st;
	next.netbootxyz = xyz_rel.tag;
	next.checked_at = go::rfc3339(static_cast<std::int64_t>(std::time(nullptr)));
	bool same = st.ipxe == ipxe_rel.tag && (st.ipxe_files ? st.ipxe_files->size() : 0) == digests.size();
	for (const auto &[name, digest] : digests) {
		std::string have;
		if (st.ipxe_files)
			if (auto it = st.ipxe_files->find(name); it != st.ipxe_files->end()) have = it->second;
		same = same && have == digest && !verify_file(ipxe_image_path(ipxe_rel.tag, name), digest);
	}
	if (!same) {
		for (const auto &[name, digest] : digests)
			if (auto e = download_verified(ipxe_download_base + "/" + ipxe_rel.tag + "/" + name,
			                               ipxe_image_path(ipxe_rel.tag, name), digest, canceled, deadline))
				return e;
		next.ipxe = ipxe_rel.tag;
		next.ipxe_files = digests;
	}
	return save_netboot_state(next);
}

void remove_old_ipxe(const std::string &keep)
{
	DIR *d = ::opendir(netboot_dir.c_str());
	if (!d) return;
	std::vector<std::string> remove;
	while (dirent *e = ::readdir(d)) {
		std::string n = e->d_name;
		struct stat st{};
		if (n.starts_with("ipxe-") && n != "ipxe-" + keep && ::lstat((netboot_dir + "/" + n).c_str(), &st) == 0 && S_ISDIR(st.st_mode))
			remove.push_back(n);
	}
	::closedir(d);
	for (const auto &n : remove) go::remove_all(netboot_dir + "/" + n);
}

// Also works with the storage function unlinked or the gadget unbound; ejecting does not rebind the gadget, which
// would interrupt the computer's USB network.
Error eject_netboot_image()
{
	std::lock_guard h(gadget_mutex());
	std::string current;
	if (auto e = go::read_file(mount_device, current)) return e.sys == ENOENT ? Error{} : e;
	if (!go::trim_space(current).starts_with(netboot_dir + "/")) return {};
	return eject_local_image(true);
}

int disable_netboot(Error &err)
{
	NetbootState st;
	if ((err = load_netboot_state(st))) return -2;
	Service &s = service();
	{
		// The session and the cleanup markers stay until the cleanup works: a failed disable can be repeated, also
		// after a restart.
		std::lock_guard lock(s.mu);
		err = eject_netboot_image();
	}
	if (err) return -2;
	if ((err = undo_netboot(st))) return -2;
	std::lock_guard lock(s.mu);
	if (auto n = s.netboot) {
		n->close();
		s.netboot = nullptr;
	}
	return 0;
}

int enable_netboot(bool secure_boot, const Canceled &canceled, Error &err)
{
	Service &s = service();
	bool busy, on;
	{
		std::lock_guard lock(s.mu);
		busy = s.remote != nullptr;
		on = s.netboot != nullptr;
	}
	if (busy) {
		err = Error::of("Disconnect remote media before turning on network boot");
		return -5;
	}
	if (on)
		if (int code = disable_netboot(err)) return code;
	if (!usb_network_active()) {
		err = Error::of("Turn on USB network in the USB composition first");
		return -6;
	}
	if (require_active_usb_storage()) {
		err = Error::of("Turn on the virtual disk in the USB composition first");
		return -6;
	}
	if (!go::exists(usb_internet_tool)) {
		err = Error::of("Network boot requires the updated Alpine base package");
		return -4;
	}
	NetbootState st;
	if ((err = load_netboot_state(st))) return -2;
	if (st.ipxe.empty() || st.netbootxyz.empty()) {
		NetbootState next;
		if (auto e = update_netboot_releases(st, next, canceled, SteadyClock::now() + std::chrono::minutes(10))) {
			err = Error::of("download network boot files: " + e.text);
			return -3;
		}
		st = next;
		remove_old_ipxe(st.ipxe);
	}
	std::string name = image_for(secure_boot), image = ipxe_image_path(st.ipxe, name);
	std::string digest;
	if (st.ipxe_files)
		if (auto it = st.ipxe_files->find(name); it != st.ipxe_files->end()) digest = it->second;
	if ((err = verify_file(image, digest))) return -2;
	if (!usb_internet_enabled()) {
		st.sharing_by_netboot = true;
		if ((err = save_netboot_state(st))) return -2;
		if ((err = set_sharing(true))) {
			(void)undo_netboot(st);
			return -2;
		}
	}
	auto fail = [&](int code, Error e) {
		if (auto undo = undo_netboot(st)) e = Error::of(e.text + "; undo: " + undo.text);
		err = e;
		return code;
	};
	if (auto e = set_netboot_port(netboot_port)) return fail(-2, e);
	if (auto e = apply_usb_network()) return fail(-2, e);
	std::string address = usb_internet_address();
	if (address.empty()) return fail(-6, Error::of("USB network has no address; check the USB network status"));
	auto n = std::make_shared<NetbootSession>();
	n->image = image;
	n->secure_boot = secure_boot;
	n->set_tag(st.netbootxyz);
	{
		std::unique_lock lock(s.mu);
		if (s.remote) {
			lock.unlock();
			return fail(-5, Error::of("Disconnect remote media before turning on network boot"));
		}
		if (auto e = n->listen(address)) {
			lock.unlock();
			return fail(-2, e);
		}
		if (MountError m = mount_local_image(image, true, false)) {
			// The mount can fail after writing the LUN (while rebinding USB): no partial mount stays behind.
			Error joined = join({Error::of(m.msg), eject_netboot_image()});
			lock.unlock();
			n->close();
			return fail(-2, joined);
		}
		s.netboot = n;
	}
	follow(n);
	st.secure_boot = secure_boot;
	if (auto e = save_netboot_state(st)) log::warn("network boot: {}", e.text);
	log::info("network boot on: {}, netboot.xyz {}, menu on {}", go::base(image), st.netbootxyz, address);
	return 0;
}

// The slow network boot actions answer from a thread of their own (downloads take minutes).
void answer_later(http::Request &req, http::Response &res, std::function<void(http::Response &, const Canceled &)> fn)
{
	res.pending = true;
	auto watch = watch_request(req.id);
	std::thread([id = req.id, headers = res.headers, watch, fn = std::move(fn)] {
		http::Response out;
		out.headers = headers;
		fn(out, [watch] { return watch->gone.load(); });
		watch->finished = true;
		http::complete(id, std::move(out));
	}).detach();
}

} // namespace

void cleanup_netboot()
{
	Service &s = service();
	std::lock_guard op(s.netboot_op);
	NetbootState st;
	if (auto e = load_netboot_state(st)) {
		log::warn("network boot: {}", e.text);
		return;
	}
	Error err;
	{
		std::lock_guard lock(s.mu);
		err = eject_netboot_image();
	}
	if (err) {
		log::warn("network boot: eject the iPXE image: {}", err.text);
		return;
	}
	if (netboot_port_file() == 0 && !st.sharing_by_netboot) return;
	if (auto e = undo_netboot(st)) log::warn("network boot: undo after restart: {}", e.text);
}

void add_netboot_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/storage/netboot", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			Error err;
			std::string status = status_json(&err);
			if (err) log::warn("network boot: {}", err.text);
			api::send(out, 0, "success", status);
		});
	}, admin));
	router.add("POST", "/api/storage/netboot/update", auth::guarded([](http::Request &req, http::Response &res) {
		answer_later(req, res, [](http::Response &out, const Canceled &canceled) {
			Service &s = service();
			std::lock_guard op(s.netboot_op);
			NetbootState st, next;
			if (auto e = load_netboot_state(st)) return api::err(out, -2, e.text);
			if (auto e = update_netboot_releases(st, next, canceled, SteadyClock::now() + std::chrono::minutes(10))) {
				log::error("network boot update: {}", e.text);
				return api::err(out, -3, e.text);
			}
			std::string failed;
			{
				std::lock_guard lock(s.mu);
				if (auto n = s.netboot) {
					n->set_tag(next.netbootxyz);
					if (std::string image = ipxe_image_path(next.ipxe, image_for(n->secure_boot)); image != n->image) {
						MountError m = mount_local_image(image, true, false);
						if (!m) n->image = image;
						else failed = m.msg;
					}
				}
			}
			if (!failed.empty()) return api::err(out, -4, "Updated, but the new iPXE image could not be mounted: " + failed);
			remove_old_ipxe(next.ipxe);
			api::send(out, 0, "success", status_json());
		});
	}, admin));
	router.add("POST", "/api/storage/netboot/enable", auth::guarded([](http::Request &req, http::Response &res) {
		auto params = api::Params::bind(req);
		bool ok = true;
		std::optional<bool> secure;
		if (params) secure = params->boolean(json_content_type(req.header("content-type")) ? "secureBoot" : "SecureBoot", ok);
		if (!params || !ok) return api::err(res, -1, "invalid arguments");
		answer_later(req, res, [secure = secure.value_or(false)](http::Response &out, const Canceled &canceled) {
			std::lock_guard op(service().netboot_op);
			Error err;
			if (int code = enable_netboot(secure, canceled, err)) {
				log::error("network boot: {}", err.text);
				return api::err(out, code, err.text);
			}
			api::send(out, 0, "success", status_json());
		});
	}, admin));
	router.add("POST", "/api/storage/netboot/disable", auth::guarded([](http::Request &req, http::Response &res) {
		answer_later(req, res, [](http::Response &out, const Canceled &) {
			std::lock_guard op(service().netboot_op);
			Error err;
			if (int code = disable_netboot(err)) {
				log::error("network boot: {}", err.text);
				return api::err(out, code, err.text);
			}
			api::send(out, 0, "success", status_json());
		});
	}, admin));
}

} // namespace nkos::storage
