#include "system/serial_terminal.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/server.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <cctype>
#include <cerrno>
#include <cstdint>
#include <cstring>
#include <fcntl.h>
#include <map>
#include <regex>
#include <set>
#include <sys/file.h>
#include <sys/ioctl.h>
#include <termios.h>
#include <unistd.h>

namespace nkos::system::serial_terminal {

namespace {

// Device output that waits for the page beyond this pauses the reads; the device keeps its own buffer meanwhile.
constexpr std::size_t max_pending_output = 256 << 10;
// Page input that waits for the device (flow control asserted, nothing at the other end) beyond this ends the page:
// Go queued 32 messages of at most 64 KiB.
constexpr std::size_t max_pending_input = 2 << 20;
constexpr std::size_t read_size = 4096;

const char *disconnected_notice = "The serial device was disconnected.";

const std::map<int, speed_t> bauds{
	{50, B50}, {75, B75}, {110, B110}, {134, B134}, {150, B150}, {200, B200}, {300, B300}, {600, B600},
	{1200, B1200}, {1800, B1800}, {2400, B2400}, {4800, B4800}, {9600, B9600}, {19200, B19200}, {38400, B38400},
	{57600, B57600}, {115200, B115200}, {230400, B230400}, {460800, B460800}, {500000, B500000}, {576000, B576000},
	{921600, B921600}, {1000000, B1000000}, {1152000, B1152000}, {1500000, B1500000}, {2000000, B2000000},
	{2500000, B2500000}, {3000000, B3000000}, {3500000, B3500000}, {4000000, B4000000},
};

std::set<std::string> held_ports;

std::string trim(std::string s)
{
	auto space = [](unsigned char c) { return std::isspace(c) != 0; };
	s.erase(s.begin(), std::find_if_not(s.begin(), s.end(), space));
	s.erase(std::find_if_not(s.rbegin(), s.rend(), space).base(), s.end());
	return s;
}

bool to_int(const std::string &s, int &out)
{
	if (s.empty() || s.size() > 9) return false;
	std::size_t i = s[0] == '+' || s[0] == '-' ? 1 : 0;
	if (i == s.size()) return false;
	for (std::size_t j = i; j < s.size(); j++)
		if (!std::isdigit(static_cast<unsigned char>(s[j]))) return false;
	out = std::stoi(s);
	return true;
}

termios raw_termios(const termios &base, const Settings &s)
{
	termios t = base;
	t.c_iflag &= ~(IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON | IXOFF | IXANY);
	t.c_oflag &= ~OPOST;
	t.c_lflag &= ~(ECHO | ECHONL | ICANON | ISIG | IEXTEN);
	t.c_cflag &= ~(CSIZE | PARENB | PARODD | CSTOPB | CRTSCTS | CBAUD);
	t.c_cflag |= CREAD | CLOCAL;
	switch (s.data_bits) {
	case 5: t.c_cflag |= CS5; break;
	case 6: t.c_cflag |= CS6; break;
	case 7: t.c_cflag |= CS7; break;
	default: t.c_cflag |= CS8; break;
	}
	if (s.parity == "even") t.c_cflag |= PARENB;
	if (s.parity == "odd") t.c_cflag |= PARENB | PARODD;
	if (s.stop_bits == 2) t.c_cflag |= CSTOPB;
	if (s.flow == "hard") t.c_cflag |= CRTSCTS;
	if (s.flow == "soft") t.c_iflag |= IXON | IXOFF;
	auto it = bauds.find(s.baud);
	speed_t speed = it == bauds.end() ? B9600 : it->second;
	t.c_cflag |= speed;
	cfsetispeed(&t, speed);
	cfsetospeed(&t, speed);
	t.c_cc[VMIN] = 0;
	t.c_cc[VTIME] = 0;
	return t;
}

enum class OpenError { none, busy, missing, other };

// The device with exclusive access and the line settings, non-blocking; -1 with why.
int open_serial(const Settings &s, OpenError &why)
{
	int fd = ::open(s.device.c_str(), O_RDWR | O_NOCTTY | O_NONBLOCK | O_CLOEXEC);
	if (fd < 0) {
		why = errno == ENOENT || errno == EIO || errno == ENXIO || errno == ENODEV ? OpenError::missing : OpenError::other;
		return -1;
	}
	// flock against another server, TIOCEXCL against other programs.
	if (::flock(fd, LOCK_EX | LOCK_NB) != 0) {
		::close(fd);
		why = OpenError::busy;
		return -1;
	}
	::ioctl(fd, TIOCEXCL);
	termios base{};
	if (::tcgetattr(fd, &base) != 0) {
		log::error("serial terminal {}: not a serial device: {}", s.device, std::strerror(errno));
		::close(fd);
		why = OpenError::other;
		return -1;
	}
	termios t = raw_termios(base, s);
	if (::tcsetattr(fd, TCSANOW, &t) != 0) {
		log::error("serial terminal {}: line settings: {}", s.device, std::strerror(errno));
		::close(fd);
		why = OpenError::other;
		return -1;
	}
	::tcflush(fd, TCIOFLUSH);
	return fd;
}

bool gone(int err) { return err == EIO || err == ENXIO || err == ENODEV; }

// A resize message (JSON) has no meaning for a serial line; the browser's OSC 777 size reply is ordinary input.
struct WinSize {
	std::uint16_t rows = 0, cols = 0;
};
constexpr glz::opts lenient{.error_on_unknown_keys = false};

struct Page : http::WsHandler {
	Settings settings;
	http::WebSocket *ws = nullptr;
	int fd = -1;
	http::FdWatch watch = 0;
	std::function<void()> release;
	std::string pending, input;
	bool paused = false, ended = false;

	explicit Page(Settings s) : settings(std::move(s)) {}

	// Text after the device's output that reached the page: queued before the close.
	void notice(const std::string &text)
	{
		if (!ws || ws->closing()) return;
		if (!pending.empty()) ws->send(http::make_message(pending));
		pending.clear();
		ws->send(http::make_message("\r\n\x1b[31m" + text + "\x1b[0m\r\n"));
	}

	void finish(const char *text = nullptr)
	{
		if (text) notice(text);
		if (ws && !ws->closing()) ws->close(1000, "");
		end_device();
	}

	// The device closes after the page; the port is released once it is closed.
	void end_device()
	{
		if (ended) return;
		ended = true;
		if (watch) {
			http::unwatch_fd(watch, std::move(release));
			watch = 0;
		} else if (release) {
			// After a hangup lws closes the device once its close callback returned.
			loop::post(std::move(release));
		}
		release = nullptr;
	}

	void opened(http::WebSocket &socket) override
	{
		ws = &socket;
		auto claim = claim_serial_port(settings.device);
		if (!claim) {
			ended = true;
			notice("The serial port is in use by another session (a terminal or the console test).");
			ws->close(1000, "");
			return;
		}
		release = std::move(*claim);
		OpenError why = OpenError::none;
		fd = open_serial(settings, why);
		if (fd < 0) {
			switch (why) {
			case OpenError::busy: notice("The serial port is in use by another program."); break;
			case OpenError::missing: notice("The serial port is not available."); break;
			default: notice("The serial port could not be opened."); break;
			}
			ws->close(1000, "");
			end_device();
			return;
		}
		watch = http::watch_fd(fd, http::FdCallbacks{
			[this] { read_device(); },
			[this] { write_device(); },
			[this] {
				// lws closed the device on a hangup.
				watch = 0;
				fd = -1;
				finish(disconnected_notice);
			},
		});
		if (!watch) {
			::close(fd);
			fd = -1;
			finish("The serial port could not be opened.");
		}
	}

	void read_device()
	{
		char buf[read_size];
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n > 0) {
			pending.append(buf, static_cast<std::size_t>(n));
			ws->wake();
			if (pending.size() > max_pending_output && !paused) {
				paused = true;
				http::pause_fd(watch, true);
			}
			return;
		}
		if (n == 0) return finish(disconnected_notice);  // end of file on a tty: the other side hung up
		if (errno == EAGAIN || errno == EINTR) return;
		finish(gone(errno) ? disconnected_notice : nullptr);
	}

	void write_device()
	{
		while (!input.empty()) {
			ssize_t n = ::write(fd, input.data(), input.size());
			if (n > 0) {
				input.erase(0, static_cast<std::size_t>(n));
				continue;
			}
			if (n < 0 && (errno == EAGAIN || errno == EINTR)) {
				http::want_writable_fd(watch);
				return;
			}
			return finish(n < 0 && gone(errno) ? disconnected_notice : nullptr);
		}
	}

	void received(http::WebSocket &, std::string_view data, bool binary) override
	{
		if (ended) return;
		if (binary) {
			WinSize size;
			if (!glz::read<lenient>(size, std::string(data))) return;
		}
		if (input.size() + data.size() > max_pending_input) return finish();
		bool idle = input.empty();
		input.append(data);
		if (idle) write_device();
	}

	bool ready(http::WebSocket &) override { return !pending.empty(); }

	std::optional<http::Message> next(http::WebSocket &) override
	{
		if (pending.empty()) return std::nullopt;
		auto m = http::make_message(pending);
		pending.clear();
		if (paused && watch) {
			paused = false;
			http::pause_fd(watch, false);
		}
		return m;
	}

	void closed(http::WebSocket &) override
	{
		ws = nullptr;
		end_device();
	}
};

} // namespace

std::optional<std::function<void()>> claim_serial_port(const std::string &device)
{
	if (!held_ports.insert(device).second) return std::nullopt;
	return std::function<void()>([device] { held_ports.erase(device); });
}

std::optional<Settings> settings_from_query(const http::Request &request, std::string &error)
{
	static const std::regex port_pattern("^/dev/(tty(S|USB|ACM|GS)[0-9]+|serial[0-9]+)$");
	Settings s;
	s.device = trim(request.query_value("port"));
	if (!std::regex_match(s.device, port_pattern)) {
		error = "invalid serial port";
		return std::nullopt;
	}
	if (s.device.rfind("/dev/ttyGS", 0) == 0) return s;
	auto option = [&](const char *key, const char *fallback, std::string &value) {
		value = trim(request.query_value(key));
		std::transform(value.begin(), value.end(), value.begin(), [](unsigned char c) { return std::tolower(c); });
		if (value.empty()) value = fallback;
		if (value.find_first_of(" \t\r\n") != std::string::npos) {
			error = std::string("invalid serial ") + key;
			return false;
		}
		return true;
	};
	std::string value;
	if (!option("baud", "115200", value)) return std::nullopt;
	if (!to_int(value, s.baud) || !bauds.count(s.baud)) {
		error = "invalid serial baud";
		return std::nullopt;
	}
	if (!option("parity", "none", value)) return std::nullopt;
	if (value == "n" || value == "none") s.parity = "none";
	else if (value == "e" || value == "even") s.parity = "even";
	else if (value == "o" || value == "odd") s.parity = "odd";
	else {
		error = "invalid serial parity";
		return std::nullopt;
	}
	if (!option("flowControl", "none", value)) return std::nullopt;
	if (value == "n" || value == "none") s.flow = "none";
	else if (value == "h" || value == "hard") s.flow = "hard";
	else if (value == "s" || value == "x" || value == "soft") s.flow = "soft";
	else {
		error = "invalid serial flowControl";
		return std::nullopt;
	}
	if (!option("dataBits", "8", value)) return std::nullopt;
	if (!to_int(value, s.data_bits) || s.data_bits < 5 || s.data_bits > 8) {
		error = "invalid serial dataBits";
		return std::nullopt;
	}
	if (!option("stopBits", "1", value)) return std::nullopt;
	if (!to_int(value, s.stop_bits) || (s.stop_bits != 1 && s.stop_bits != 2)) {
		error = "invalid serial stopBits";
		return std::nullopt;
	}
	return s;
}

std::shared_ptr<http::WsHandler> make_page(Settings settings) { return std::make_shared<Page>(std::move(settings)); }

} // namespace nkos::system::serial_terminal
