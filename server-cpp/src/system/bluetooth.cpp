// Bluetooth of the AIC Wi-Fi chip, as the Go server's service/vm/bluetooth.go: on at boot through a softdep in
// /etc/modprobe.d (marked as NanoKVM's), loaded or unloaded now, and the controller's core version read over the
// kernel's management socket.

#include "system/bluetooth.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/process.hpp"

#include <cerrno>
#include <chrono>
#include <cstring>
#include <fcntl.h>
#include <format>
#include <fstream>
#include <mutex>
#include <optional>
#include <sstream>
#include <string>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/utsname.h>
#include <thread>
#include <unistd.h>

namespace nkos::system {

namespace {

using namespace std::chrono_literals;

constexpr const char *conf_marker = "# Managed by NanoKVM: Bluetooth setting. Remove to turn Bluetooth off.\n";
constexpr const char *conf = "/etc/modprobe.d/nanokvm-bluetooth.conf";
constexpr const char *hci = "/sys/class/bluetooth/hci0";
constexpr const char *bt_module = "aic8800_btsdio";
constexpr const char *wifi_module = "aic8800_fdrv";
constexpr const char *reset_prefix = "echo 0 >/proc/self/oom_score_adj; renice 0 \"$$\" >/dev/null; ";

std::mutex mutex;

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

bool module_loaded(const std::string &name) { return exists("/sys/module/" + name); }

std::optional<std::string> read(const char *path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

std::string kernel_release()
{
	utsname u{};
	return ::uname(&u) == 0 ? u.release : "";
}

bool available()
{
	if (!module_loaded(wifi_module)) return false;
	std::string release = kernel_release();
	return !release.empty() && exists("/lib/modules/" + release + "/extra/" + bt_module + ".ko");
}

bool conf_owned()
{
	auto data = read(conf);
	return data && data->starts_with(conf_marker);
}

// "" or the error, with the command's output.
std::string run(std::vector<std::string> argv)
{
	std::string shown;
	for (const auto &a : argv) shown += (shown.empty() ? "" : " ") + a;
	std::vector<std::string> full = {"/bin/sh", "-c", std::string(reset_prefix) + "exec \"$0\" \"$@\""};
	full.insert(full.end(), argv.begin(), argv.end());
	auto r = process::run(full);
	if (r.ok()) return "";
	std::string out = r.output;
	while (!out.empty() && std::isspace(static_cast<unsigned char>(out.back()))) out.pop_back();
	return shown + ": exit " + std::to_string(r.exit_code) + ": " + out;
}

bool write_conf_content(const std::string &content)
{
	::mkdir("/etc/modprobe.d", 0755);
	std::string temp = std::string("/etc/modprobe.d/.nanokvm-bluetooth-") + std::to_string(::getpid());
	int fd = ::open(temp.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0644);
	if (fd < 0) return false;
	bool ok = ::write(fd, content.data(), content.size()) == static_cast<ssize_t>(content.size()) && ::fsync(fd) == 0;
	ok = ::close(fd) == 0 && ok;
	ok = ok && ::chmod(temp.c_str(), 0644) == 0 && ::rename(temp.c_str(), conf) == 0;
	if (!ok) ::unlink(temp.c_str());
	return ok;
}

std::string write_conf()
{
	auto data = read(conf);
	if (data && !data->starts_with(conf_marker)) return std::string(conf) + " exists and is not managed by NanoKVM";
	if (!write_conf_content(std::string(conf_marker) + "softdep " + wifi_module + " post: " + bt_module + "\n"))
		return "cannot write " + std::string(conf);
	return "";
}

std::string remove_conf()
{
	auto data = read(conf);
	if (!data) return "";
	if (!data->starts_with(conf_marker)) return std::string(conf) + " exists and is not managed by NanoKVM";
	return ::unlink(conf) == 0 ? "" : "cannot remove " + std::string(conf);
}

// The controller's core specification version (MGMT Read Controller Information on index 0).
std::optional<std::string> controller_version()
{
	if (!exists(hci)) return std::nullopt;
	constexpr int af_bluetooth = 31, btproto_hci = 1;
	struct SockaddrHci {
		sa_family_t family;
		std::uint16_t dev, channel;
	} addr{af_bluetooth, 0xffff, 3};  // HCI_DEV_NONE, HCI_CHANNEL_CONTROL
	int fd = ::socket(af_bluetooth, SOCK_RAW | SOCK_CLOEXEC, btproto_hci);
	if (fd < 0) return std::nullopt;
	struct Closer {
		int fd;
		~Closer() { ::close(fd); }
	} closer{fd};
	if (::bind(fd, reinterpret_cast<sockaddr *>(&addr), sizeof addr) != 0) return std::nullopt;
	constexpr std::uint16_t read_info = 0x0004, cmd_complete = 0x0001, cmd_status = 0x0002, index = 0;
	unsigned char req[6] = {read_info & 0xff, read_info >> 8, index & 0xff, index >> 8, 0, 0};
	if (::write(fd, req, sizeof req) != static_cast<ssize_t>(sizeof req)) return std::nullopt;
	auto le16 = [](const unsigned char *p) { return static_cast<std::uint16_t>(p[0] | p[1] << 8); };
	auto deadline = std::chrono::steady_clock::now() + 2s;
	static const char *versions[] = {"1.0b", "1.1", "1.2", "2.0", "2.1", "3.0", "4.0", "4.1",
	                                 "4.2",  "5.0", "5.1", "5.2", "5.3", "5.4", "6.0", "6.1"};
	unsigned char buf[512];
	for (;;) {
		auto left = std::chrono::duration_cast<std::chrono::microseconds>(deadline - std::chrono::steady_clock::now());
		if (left.count() <= 0) return std::nullopt;
		left = std::max(left, std::chrono::microseconds(1000));
		timeval tv{static_cast<time_t>(left.count() / 1000000), static_cast<suseconds_t>(left.count() % 1000000)};
		if (::setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv) != 0) return std::nullopt;
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0) return std::nullopt;
		if (n < 6 || le16(buf + 2) != index) continue;
		std::uint16_t code = le16(buf);
		const unsigned char *params = buf + 6;
		std::size_t plen = static_cast<std::size_t>(n) - 6;
		if ((code != cmd_complete && code != cmd_status) || plen < 3 || le16(params) != read_info) continue;
		if (params[2] != 0) return std::nullopt;
		if (code != cmd_complete || plen < 3 + 6 + 1) return std::nullopt;
		std::uint8_t hci_version = params[3 + 6];
		if (hci_version < std::size(versions)) return std::string(versions[hci_version]);
		return std::format("HCI {}", hci_version);
	}
}

std::string apply(bool enabled)
{
	auto previous = read(conf);
	auto restore = [&](std::string cause) {
		bool undone = previous ? write_conf_content(*previous) : remove_conf().empty();
		if (!undone) cause += "\nrestore the Bluetooth boot setting failed";
		return cause;
	};
	if (enabled) {
		if (auto e = write_conf(); !e.empty()) return e;
		if (module_loaded(bt_module)) return "";
		if (auto e = run({"modprobe", bt_module}); !e.empty()) return restore(e);
		auto deadline = std::chrono::steady_clock::now() + 5s;
		while (!controller_version() && std::chrono::steady_clock::now() < deadline) std::this_thread::sleep_for(250ms);
		return "";
	}
	if (auto e = remove_conf(); !e.empty()) return e;
	if (module_loaded(bt_module))
		if (auto e = run({"rmmod", bt_module}); !e.empty()) return restore(e);
	if (module_loaded("bluetooth"))
		if (auto e = run({"modprobe", "-r", "bluetooth"}); !e.empty()) log::info("bluetooth.ko stays loaded: {}", e);
	return "";
}

std::string state_json()
{
	bool loaded = module_loaded(bt_module);
	std::string version = loaded ? controller_version().value_or("") : "";
	return std::format(R"({{"available":{},"enabled":{},"loaded":{},"version":{}}})", available(), conf_owned(), loaded,
	                   api::quote(version));
}

void set(http::Request &req, http::Response &res, bool enabled)
{
	http::defer(req, res, [enabled](http::Response &out) {
		std::lock_guard lock(mutex);
		if (!available()) return api::err(out, -2, "Bluetooth is not available on this device");
		if (auto e = apply(enabled); !e.empty()) {
			log::error("failed to turn Bluetooth {}: {}", enabled ? "on" : "off", e);
			return api::err(out, -1, "failed to change Bluetooth state");
		}
		api::ok(out);
	});
}

} // namespace

void add_bluetooth_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/bluetooth", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::lock_guard lock(mutex);
			api::send(out, 0, "success", state_json());
		});
	}, admin));
	router.add("POST", "/api/vm/bluetooth/enable", auth::guarded([](http::Request &req, http::Response &res) { set(req, res, true); }, admin));
	router.add("POST", "/api/vm/bluetooth/disable", auth::guarded([](http::Request &req, http::Response &res) { set(req, res, false); }, admin));
}

} // namespace nkos::system
