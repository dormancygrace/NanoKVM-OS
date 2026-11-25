#include "system/serial_console.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "system/serial_terminal.hpp"

#include <glaze/glaze.hpp>
#include <openssl/evp.h>

#include <algorithm>
#include <cctype>
#include <cerrno>
#include <chrono>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <fstream>
#include <poll.h>
#include <regex>
#include <sys/stat.h>
#include <termios.h>
#include <unistd.h>

namespace nkos::system::serial_console {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

const std::string script_name = "nanokvm-serial-console.sh";
const std::string disk_name = "nanokvm-serial-console.img";
const std::string script_path = "/" + script_name;
const std::string device = "/dev/ttyGS0";
const std::string share_dir = "/kvmapp/system/share/serial-console";
const std::string helper_disk_file = "/var/lib/nanokvm/helper-disk.img";
const std::string serial_number_path = "/sys/kernel/config/usb_gadget/g0/strings/0x409/serialnumber";
const std::string gadget_dir = "/sys/kernel/config/usb_gadget/g0";
const std::string lun_file = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.1/file";
constexpr auto probe_timeout = 5s;

std::string script_file() { return share_dir + "/" + script_name; }
std::string disk_file() { return share_dir + "/" + disk_name; }

std::string trim(std::string s)
{
	auto space = [](unsigned char c) { return std::isspace(c) != 0; };
	s.erase(s.begin(), std::find_if_not(s.begin(), s.end(), space));
	s.erase(std::find_if_not(s.rbegin(), s.rend(), space).base(), s.end());
	return s;
}

std::optional<std::string> read_file(const std::string &path)
{
	std::ifstream f(path, std::ios::binary);
	if (!f) return std::nullopt;
	return std::string((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
}

bool regular(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode);
}

// SHA-256 (hex) and size of a file.
bool file_sha256(const std::string &path, std::string &sum, std::int64_t &size)
{
	std::ifstream f(path, std::ios::binary);
	if (!f) return false;
	EVP_MD_CTX *ctx = EVP_MD_CTX_new();
	EVP_DigestInit_ex(ctx, EVP_sha256(), nullptr);
	char buf[65536];
	size = 0;
	while (f.read(buf, sizeof buf) || f.gcount() > 0) {
		EVP_DigestUpdate(ctx, buf, static_cast<std::size_t>(f.gcount()));
		size += f.gcount();
	}
	unsigned char digest[32];
	unsigned int n = 0;
	EVP_DigestFinal_ex(ctx, digest, &n);
	EVP_MD_CTX_free(ctx);
	static const char hex[] = "0123456789abcdef";
	sum.clear();
	for (unsigned i = 0; i < n; i++) {
		sum += hex[digest[i] >> 4];
		sum += hex[digest[i] & 15];
	}
	return true;
}

// idVendor or idProduct as the host's sysfs shows it: 4 lowercase hex digits, "" when unknown.
std::string gadget_id(const std::string &name)
{
	auto data = read_file(gadget_dir + "/" + name);
	if (!data) return "";
	std::string v = trim(*data);
	static const std::regex pattern("^0[xX][0-9a-fA-F]{1,4}$");
	if (!std::regex_match(v, pattern)) return "";
	unsigned id = static_cast<unsigned>(std::stoul(v.substr(2), nullptr, 16));
	if (id == 0) return "";
	char out[8];
	std::snprintf(out, sizeof out, "%04x", id);
	return out;
}

struct FileRsp {
	std::string path;
	bool available = false;
	std::optional<std::string> sha256;
	std::optional<std::int64_t> size;
};
struct DiskRsp {
	std::string file;
	bool available = false;
	bool attached = false;
	bool stale = false;
};
struct StateRsp {
	std::string serialNumber;
	std::string vendorId;
	std::string productId;
	std::string port;
	FileRsp script;
	DiskRsp disk;
};
struct TestRsp {
	bool ok = false;
	std::string state;
	std::optional<std::string> response;
};

StateRsp state()
{
	StateRsp r;
	r.serialNumber = trim(read_file(serial_number_path).value_or(""));
	r.port = device;
	r.vendorId = gadget_id("idVendor");
	r.productId = gadget_id("idProduct");
	r.script.path = script_path;
	r.disk.file = helper_disk_image();
	std::string sum;
	std::int64_t size = 0;
	if (file_sha256(script_file(), sum, size)) {
		r.script.available = true;
		r.script.sha256 = sum;
		if (size) r.script.size = size;
	}
	struct stat st{};
	if (::stat(r.disk.file.c_str(), &st) == 0) {
		r.disk.available = true;
		helper_disk_lun(r.disk.attached, r.disk.stale);
	}
	return r;
}

// ESC sequences out, controls (but line breaks and tabs, which become spaces) out, spaces collapsed; the last 200
// bytes. Then: login, password, shell (a prompt ends the text), text, or silent.
std::pair<std::string, std::string> classify(const std::string &data)
{
	// The three patterns of the Go server, each removed in its own pass, by scanning: std::regex recurses once per
	// repeated character, and what the host sends here is read on a pool thread's small stack.
	//   CSI  ESC [ [0-9;?]* [ -/]* [@-~]
	//   OSC  ESC ] [^BEL ESC]* (BEL | ESC \)
	//   ESC  ESC and one byte other than [ and ]
	auto strip = [](const std::string &in, std::size_t (*match)(const std::string &, std::size_t)) {
		std::string out;
		for (std::size_t i = 0; i < in.size();) {
			std::size_t n = in[i] == '\x1b' ? match(in, i) : 0;
			if (n) {
				i += n;
			} else {
				out += in[i++];
			}
		}
		return out;
	};
	auto csi = [](const std::string &in, std::size_t i) -> std::size_t {
		std::size_t j = i + 1;
		if (j >= in.size() || in[j] != '[') return 0;
		j++;
		while (j < in.size() && ((in[j] >= '0' && in[j] <= '9') || in[j] == ';' || in[j] == '?')) j++;
		while (j < in.size() && in[j] >= ' ' && in[j] <= '/') j++;
		return j < in.size() && in[j] >= '@' && in[j] <= '~' ? j + 1 - i : 0;
	};
	auto osc = [](const std::string &in, std::size_t i) -> std::size_t {
		std::size_t j = i + 1;
		if (j >= in.size() || in[j] != ']') return 0;
		j++;
		while (j < in.size() && in[j] != '\x07' && in[j] != '\x1b') j++;
		if (j < in.size() && in[j] == '\x07') return j + 1 - i;
		return j + 1 < in.size() && in[j + 1] == '\\' ? j + 2 - i : 0;
	};
	auto esc = [](const std::string &in, std::size_t i) -> std::size_t {
		return i + 1 < in.size() && in[i + 1] != '[' && in[i + 1] != ']' ? 2 : 0;
	};
	std::string clean = strip(strip(strip(data, csi), osc), esc);
	std::string mapped;
	for (unsigned char c : clean) {
		if (c == '\n' || c == '\r' || c == '\t') mapped += ' ';
		else if (c < 0x20 || c == 0x7f) continue;
		else mapped += static_cast<char>(c);
	}
	std::string text;
	bool space = false;
	for (char c : mapped) {
		if (c == ' ') {
			space = true;
			continue;
		}
		if (space && !text.empty()) text += ' ';
		space = false;
		text += c;
	}
	if (text.size() > 200) text = text.substr(text.size() - 200);
	std::string lower = text;
	std::transform(lower.begin(), lower.end(), lower.begin(), [](unsigned char c) { return std::tolower(c); });
	if (text.empty()) return {"silent", ""};
	if (lower.find("login:") != std::string::npos) return {"login", text};
	if (lower.find("password:") != std::string::npos) return {"password", text};
	char last = text.back();
	if (last == '$' || last == '#' || last == '>' || last == '%') return {"shell", text};
	return {"text", text};
}

// Another process has the device open (the terminal of another program, a getty).
bool device_in_use()
{
	std::string self = std::to_string(::getpid());
	DIR *proc = ::opendir("/proc");
	if (!proc) return false;
	bool used = false;
	while (!used) {
		dirent *p = ::readdir(proc);
		if (!p) break;
		std::string pid = p->d_name;
		if (pid.empty() || pid.find_first_not_of("0123456789") != std::string::npos || pid == self) continue;
		std::string fds = "/proc/" + pid + "/fd";
		DIR *d = ::opendir(fds.c_str());
		if (!d) continue;
		while (dirent *f = ::readdir(d)) {
			char target[256];
			ssize_t n = ::readlink((fds + "/" + f->d_name).c_str(), target, sizeof target - 1);
			if (n > 0) {
				target[n] = 0;
				if (device == target) {
					used = true;
					break;
				}
			}
		}
		::closedir(d);
	}
	::closedir(proc);
	return used;
}

// One carriage return on the gadget port and what the host sends back, as the browser terminal's first Enter
// would. Blocking (at most probe_timeout); fd opened and closed here.
std::pair<std::string, std::string> probe_fd()
{
	struct stat st{};
	std::string lock = "/var/lock/LCK.." + device.substr(device.rfind('/') + 1);
	if (::stat(lock.c_str(), &st) == 0 || device_in_use()) return {"busy", ""};
	int fd = ::open(device.c_str(), O_RDWR | O_NOCTTY | O_NONBLOCK | O_CLOEXEC);
	if (fd < 0) {
		if (errno == ENOENT || errno == ENXIO || errno == ENODEV) return {"unavailable", ""};
		return {"error", ""};
	}
	termios saved{};
	if (::tcgetattr(fd, &saved) != 0) {
		::close(fd);
		return {"error", ""};
	}
	termios raw = saved;
	raw.c_iflag &= ~(IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON);
	raw.c_oflag &= ~OPOST;
	raw.c_lflag &= ~(ECHO | ECHONL | ICANON | ISIG | IEXTEN);
	raw.c_cflag &= ~(CSIZE | PARENB);
	raw.c_cflag |= CS8 | CREAD | CLOCAL;
	raw.c_cc[VMIN] = 0;
	raw.c_cc[VTIME] = 0;
	if (::tcsetattr(fd, TCSANOW, &raw) != 0) {
		::close(fd);
		return {"error", ""};
	}
	auto done = [&](std::pair<std::string, std::string> r) {
		::tcsetattr(fd, TCSANOW, &saved);
		::close(fd);
		return r;
	};
	// Whatever the host sent before (a banner, an old prompt) is not the answer to this Enter.
	char drain[4096];
	while (::read(fd, drain, sizeof drain) > 0) {
	}
	if (::write(fd, "\r", 1) != 1) return done({"error", ""});
	std::string data;
	auto deadline = Clock::now() + probe_timeout;
	Clock::time_point quiet_since{};
	std::size_t answered = 0;
	static const std::string query = "\x1b[6n", reply = "\x1b[24;80R";
	while (Clock::now() < deadline && data.size() < 4096) {
		pollfd p{fd, POLLIN, 0};
		if (::poll(&p, 1, 100) > 0) {
			char buf[1024];
			ssize_t n = ::read(fd, buf, sizeof buf);
			if (n > 0) {
				data.append(buf, static_cast<std::size_t>(n));
				quiet_since = Clock::now();
				// Some gettys reset the terminal and ask where the cursor is before the prompt, and wait.
				std::size_t queries = 0;
				for (std::size_t at = data.find(query); at != std::string::npos; at = data.find(query, at + 1)) queries++;
				for (; answered < queries; answered++)
					if (::write(fd, reply.data(), reply.size()) != static_cast<ssize_t>(reply.size())) return done({"error", ""});
				auto c = classify(data);
				if (c.first == "login" || c.first == "password") return done(c);
				continue;
			}
			if (n < 0 && errno != EAGAIN) break;
		}
		// A prompt is complete once the host has been quiet for a moment.
		if (!data.empty() && Clock::now() - quiet_since > 400ms) break;
	}
	return done(classify(data));
}

} // namespace

std::string helper_disk_image() { return regular(helper_disk_file) ? helper_disk_file : disk_file(); }

void helper_disk_lun(bool &attached, bool &stale)
{
	attached = stale = false;
	auto data = read_file(lun_file);
	if (!data) return;
	std::string lun = trim(*data);
	std::string path = lun;
	const std::string deleted = " (deleted)";
	if (path.size() >= deleted.size() && path.compare(path.size() - deleted.size(), deleted.size(), deleted) == 0)
		path.resize(path.size() - deleted.size());
	if (path != helper_disk_file && path != disk_file()) return;
	attached = true;
	stale = path != lun || path != helper_disk_image();
}

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/serial-console", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) { api::ok(out, state()); });
	}, admin));
	router.add("POST", "/api/vm/serial-console/test", auth::guarded([](http::Request &req, http::Response &res) {
		// The lease covers the probe, the restoration of the line settings included: a terminal session or a
		// second probe waits for it.
		auto release = serial_terminal::claim_serial_port(device);
		if (!release) return api::ok(res, TestRsp{false, "busy", std::nullopt});
		http::defer(req, res, [release = *release](http::Response &out) {
			auto [state, text] = probe_fd();
			if (state == "error" || state == "unavailable") log::warn("serial console test: {}", state);
			// Released on the loop thread, where the port registry lives.
			loop::post(release);
			TestRsp r{state == "login" || state == "password" || state == "shell", state, std::nullopt};
			if (!text.empty()) r.response = text;
			api::ok(out, r);
		});
	}, admin));
	// Public: the script is part of the firmware source, and the host that fetches it has no credentials.
	router.add("GET", script_path, [](http::Request &req, http::Response &res) {
		std::string sum;
		std::int64_t size = 0;
		if (!file_sha256(script_file(), sum, size)) {
			res.text(404, "the serial console helper is not installed on this NanoKVM");
			return;
		}
		(void)req;
		res.set_header("Content-Disposition", "inline; filename=\"" + script_name + "\"");
		res.set_header("Cache-Control", "no-cache");
		res.set_header("X-Checksum-Sha256", sum);
		res.status = 200;
		res.content_type = "text/x-shellscript; charset=utf-8";
		res.file = script_file();
		res.done = true;
	});
}

} // namespace nkos::system::serial_console
