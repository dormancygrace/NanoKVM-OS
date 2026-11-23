#include "usb/host_sensors.hpp"
#include "notify/notify.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"
#include "system/atx.hpp"
#include "system/gpio.hpp"
#include "system/hardware.hpp"
#include "system/journal.hpp"
#include "system/serial_terminal.hpp"
#include "usb/apk_selected.hpp"
#include "usb/core_hooks.hpp"
#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"
#include "usb/helper_disk.hpp"
#include "usb/host_sensors_core.hpp"
#include "usb/host_watchdog_core.hpp"
#include "usb/requests.hpp"

#include <openssl/evp.h>

#include <arpa/inet.h>
#include <fcntl.h>
#include <ifaddrs.h>
#include <linux/if_ether.h>
#include <linux/if_packet.h>
#include <net/if.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/file.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <termios.h>
#include <unistd.h>

#include <atomic>
#include <cerrno>
#include <chrono>
#include <condition_variable>
#include <cstring>
#include <format>
#include <fstream>
#include <future>
#include <memory>
#include <mutex>
#include <thread>

namespace nkos::usb {

namespace {

using namespace std::chrono_literals;

const std::string mode_file = "/etc/kvm/host-sensors";
const std::string view_file = "/etc/kvm/host-sensors-view.json";
const std::string share_dir = "/usr/share/nanokvm/host-sensors";
const std::string agent_file = share_dir + "/nanokvm-sensors.exe";
const std::string linux_agent_file = share_dir + "/nanokvm-sensors.sh";
const std::string source_file = "/usr/share/nanokvm-host-sensors/source.json";
const std::string watchdog_file = "/etc/kvm/host-watchdog.json";
const std::string serial_device = "/dev/ttyGS0";
const std::string interface = "usb0";
const std::string console_disk = "/kvmapp/system/share/serial-console/nanokvm-serial-console.img";
const std::string helper_lun_file = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.1/file";
const std::string addon_settings_checked = "/etc/kvm/addon-settings-checked";
constexpr const char *ingest_path = "/api/host-sensors";

sensors::Store store;

sensors::Nanos monotonic_now()
{
	return std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now().time_since_epoch()).count();
}

std::string trimmed_file(const std::string &path, bool &ok)
{
	std::ifstream in(path, std::ios::binary);
	ok = static_cast<bool>(in);
	std::string data((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
	return gotext::trim_space(data);
}

// --- the USB network's own addresses (the agent posts from that network only) ---

std::vector<sensors::Network> usb_networks(std::vector<std::string> *addresses = nullptr)
{
	std::vector<sensors::Network> out;
	ifaddrs *list = nullptr;
	if (::getifaddrs(&list) != 0) return out;
	for (ifaddrs *a = list; a; a = a->ifa_next) {
		if (!a->ifa_addr || a->ifa_addr->sa_family != AF_INET || interface != a->ifa_name) continue;
		auto *addr = reinterpret_cast<sockaddr_in *>(a->ifa_addr);
		auto *mask = reinterpret_cast<sockaddr_in *>(a->ifa_netmask);
		out.push_back({ntohl(addr->sin_addr.s_addr), mask ? ntohl(mask->sin_addr.s_addr) : 0xffffffffu});
		if (addresses) {
			char text[INET_ADDRSTRLEN];
			::inet_ntop(AF_INET, &addr->sin_addr, text, sizeof text);
			addresses->emplace_back(text);
		}
	}
	::freeifaddrs(list);
	return out;
}

// --- the serial reader ---

// The reader that holds the gadget port while the mode is serial. Not waited for when stopped: the kernel holds
// the close of the port up to 15 s while a "ready" line is unsent; the reader keeps the port's lease until then.
struct Reader {
	std::mutex mutex;
	std::condition_variable wake;
	bool stop = false;
	// false when stopped meanwhile.
	bool wait(std::chrono::milliseconds d)
	{
		std::unique_lock lock(mutex);
		return !wake.wait_for(lock, d, [&] { return stop; });
	}
	bool stopped()
	{
		std::lock_guard lock(mutex);
		return stop;
	}
};

std::mutex reader_mutex;
std::shared_ptr<Reader> reader;
std::string serial_state;

void set_serial_state(const std::string &state)
{
	std::lock_guard lock(reader_mutex);
	serial_state = state;
}

// The port's lease in the server (serial_terminal's registry lives on the loop thread).
std::optional<std::function<void()>> claim_port()
{
	std::promise<std::optional<std::function<void()>>> claimed;
	auto f = claimed.get_future();
	loop::post([&claimed] { claimed.set_value(system::serial_terminal::claim_serial_port(serial_device)); });
	return f.get();
}

enum class OpenError { none, busy, missing, other };

// openSerial with 9600 8N1, no flow control, raw: flock against another server, TIOCEXCL against other programs.
int open_serial(OpenError &why, std::string &error)
{
	int fd = ::open(serial_device.c_str(), O_RDWR | O_NOCTTY | O_NONBLOCK | O_CLOEXEC);
	if (fd < 0) {
		int e = errno;
		why = e == ENOENT || e == EIO || e == ENXIO || e == ENODEV ? OpenError::missing : OpenError::other;
		error = "open " + serial_device + ": " + gotext::errno_text(e);
		return -1;
	}
	if (::flock(fd, LOCK_EX | LOCK_NB) != 0) {
		::close(fd);
		why = OpenError::busy;
		return -1;
	}
	::ioctl(fd, TIOCEXCL);
	termios t{};
	if (::tcgetattr(fd, &t) != 0) {
		int e = errno;
		::close(fd);
		why = e == EIO || e == ENXIO || e == ENODEV ? OpenError::missing : OpenError::other;
		error = "not a serial device: " + gotext::errno_text(e);
		return -1;
	}
	t.c_iflag &= ~(IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON | IXOFF | IXANY);
	t.c_oflag &= ~OPOST;
	t.c_lflag &= ~(ECHO | ECHONL | ICANON | ISIG | IEXTEN);
	t.c_cflag &= ~(CSIZE | PARENB | PARODD | CSTOPB | CRTSCTS | CBAUD);
	t.c_cflag |= CREAD | CLOCAL | CS8 | B9600;
	cfsetispeed(&t, B9600);
	cfsetospeed(&t, B9600);
	t.c_cc[VMIN] = 0;
	t.c_cc[VTIME] = 0;
	if (::tcsetattr(fd, TCSANOW, &t) != 0) {
		int e = errno;
		::close(fd);
		why = e == EIO || e == ENXIO || e == ENODEV ? OpenError::missing : OpenError::other;
		error = "line settings: " + gotext::errno_text(e);
		return -1;
	}
	::tcflush(fd, TCIOFLUSH);
	why = OpenError::none;
	return fd;
}

// readHostSensorLines: every complete line to the store until stop, a hang-up or a read error. While it reads, the
// NanoKVM says "nanokvm-ready" every second (an agent that cannot see the modem lines writes only after hearing it),
// one line at a time and never once the computer's login console answered.
void read_lines(int fd, Reader &r)
{
	static const std::string ready = "nanokvm-ready\n";
	std::vector<char> buf(16 << 10);
	sensors::LineSplitter lines;
	auto said = std::chrono::steady_clock::now();  // the first "ready" waits: a waiting prompt arrives first
	std::string tail;
	bool console = false;
	for (;;) {
		if (r.stopped()) return;
		if (!console && std::chrono::steady_clock::now() - said >= 1s) {
			int pending = 0;
			if (::ioctl(fd, TIOCOUTQ, &pending) != 0 || pending == 0) (void)!::write(fd, ready.data(), ready.size());
			said = std::chrono::steady_clock::now();
		}
		pollfd p{fd, POLLIN, 0};
		int n = ::poll(&p, 1, 500);
		if (n < 0) {
			if (errno == EINTR) continue;
			return;
		}
		if (n == 0) continue;
		// POLLHUP or POLLERR alone: the computer closed the port or the gadget was rebound.
		if (!(p.revents & POLLIN)) return;
		ssize_t got = ::read(fd, buf.data(), buf.size());
		if (got <= 0) {
			if (got < 0 && (errno == EAGAIN || errno == EINTR)) continue;
			return;
		}
		std::string_view data(buf.data(), static_cast<std::size_t>(got));
		if (!console) {
			tail.append(data);
			if (tail.size() > 512) tail.erase(0, tail.size() - 512);
			// A report is one long line of JSON; a prompt is short text.
			if (tail.find("\"sensors\"") == std::string::npos && sensors::login_console(tail)) {
				console = true;
				set_serial_state("console");
			}
		}
		lines.feed(data, [](std::optional<std::string_view> line) {
			if (auto e = store.accept(line.value_or(""), "serial", {}, monotonic_now()); !e.empty())
				log::debug("host sensors: rejected a serial report: {}", e);
		});
	}
}

void run_serial(std::shared_ptr<Reader> r)
{
	for (;;) {
		if (r->stopped()) return;
		auto release = claim_port();
		if (!release) {
			set_serial_state("busy");
			if (!r->wait(1s)) return;
			continue;
		}
		OpenError why;
		std::string error;
		int fd = open_serial(why, error);
		if (fd < 0) {
			loop::post(*release);
			if (why == OpenError::busy) set_serial_state("busy");
			else if (why == OpenError::missing) set_serial_state("unavailable");
			else {
				set_serial_state("error");
				log::warn("host sensors: {}: {}", serial_device, error);
			}
			if (!r->wait(2s)) return;
			continue;
		}
		set_serial_state("open");
		read_lines(fd, *r);
		::close(fd);
		loop::post(*release);
		set_serial_state("closed");
		if (!r->wait(1s)) return;
	}
}

// applyHostSensorsMode: the reader runs while the mode is serial.
void apply_mode(const std::string &mode)
{
	std::shared_ptr<Reader> old;
	{
		std::lock_guard lock(reader_mutex);
		old = std::move(reader);
		reader.reset();
	}
	if (old) {
		std::lock_guard lock(old->mutex);
		old->stop = true;
		old->wake.notify_all();
	}
	if (mode != "serial") {
		set_serial_state("");
		return;
	}
	auto r = std::make_shared<Reader>();
	{
		std::lock_guard lock(reader_mutex);
		reader = r;
		serial_state = "starting";
	}
	std::thread(run_serial, r).detach();
}

// hostSensorsApply: the reader's and the helper disk's changes one at a time.
std::mutex apply_mutex;
helperdisk::Composer composer;

void reconcile()
{
	std::lock_guard lock(apply_mutex);
	std::string mode = sensors::read_mode(mode_file);
	composer.reconcile(mode != "off");
	bool want = mode == "serial";
	bool running;
	{
		std::lock_guard r(reader_mutex);
		running = reader != nullptr;
	}
	if (want && !running) {
		apply_mode("serial");
	} else if (!want && running) {
		apply_mode("off");
		store.clear();
	}
}

// --- the host watchdog ---

std::mutex watchdog_mutex;
watchdog::Watchdog watchdog_state;
std::string watchdog_last;

// hostWatchdogLink: the life of the USB link (the USB network's carrier changes with every enumeration).
std::string watchdog_link()
{
	bool ok;
	std::string v = trimmed_file("/sys/class/net/" + interface + "/carrier_changes", ok);
	return ok ? v : "";
}

// The BMC page's "power LED connected" setting, reread every 30 s (notifyPowerLED's cache).
bool power_led_disconnected()
{
	static std::mutex mutex;
	static std::chrono::steady_clock::time_point at{};
	static bool unconnected = false;
	std::lock_guard lock(mutex);
	auto now = std::chrono::steady_clock::now();
	if (at == std::chrono::steady_clock::time_point{} || now - at >= 30s) {
		at = now;
		unconnected = false;
		std::ifstream in("/etc/kvm/bmc/config.json", std::ios::binary);
		if (in) {
			std::string data((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
			bool redfish = false, ipmi = false, disconnected = false, media = false;
			std::string user, password;
			long long port = 0, hold = 0;
			std::string error;
			bool ok = goj::unmarshal(data, "Config", [&](goj::Reader &r) {
				r.object("Config", "bmc.Config", {
					{"redfish", [&](goj::Reader &x) { x.boolean(redfish); }}, {"ipmi", [&](goj::Reader &x) { x.boolean(ipmi); }},
					{"username", [&](goj::Reader &x) { x.string(user); }}, {"password", [&](goj::Reader &x) { x.string(password); }},
					{"ipmiPort", [&](goj::Reader &x) { x.integer(port); }}, {"holdSeconds", [&](goj::Reader &x) { x.integer(hold); }},
					{"powerLedDisconnected", [&](goj::Reader &x) { x.boolean(disconnected); }},
					{"virtualMedia", [&](goj::Reader &x) { x.boolean(media); }},
				});
			}, error);
			unconnected = ok && disconnected;
		}
	}
	return unconnected;
}

// notifyPowerLED: the host's power as the LED shows it, and whether that is known.
void power_led(bool &on, bool &known)
{
	on = known = false;
	if (!system::atx::enabled() || power_led_disconnected()) return;
	auto leds = system::gpio::current_leds();
	if (!leds) return;
	on = leds->power;
	known = true;
}

watchdog::Signs signs_now(const watchdog::Config &config, sensors::Nanos now)
{
	watchdog::Signs s;
	s.now = now;
	s.watching = config.action != watchdog::off && sensors::read_mode(mode_file) == "network";
	if (!s.watching) return s;
	sensors::From from;
	store.last(s.report, from);
	s.report_link = from.link;
	power_led(s.power_on, s.power_known);
	s.link = watchdog_link();
	bool ok;
	std::string udc = trimmed_file("/sys/kernel/config/usb_gadget/g0/UDC", ok);
	s.configured = trimmed_file("/sys/class/udc/" + udc + "/state", ok) == "configured";
	return s;
}

// arpProbe: count ARP requests for target on the interface, a second apart, answers awaited for wait in all.
void arp_probe(std::uint32_t target, int count, std::chrono::milliseconds wait, int &sent, bool &answered, std::string &error)
{
	sent = 0;
	answered = false;
	unsigned index = ::if_nametoindex(interface.c_str());
	if (index == 0) {
		error = "route ip+net: no such network interface";
		return;
	}
	std::uint32_t source = 0;
	bool have_source = false;
	for (auto &n : usb_networks())
		if ((target & n.mask) == (n.address & n.mask)) source = n.address, have_source = true;
	unsigned char mac[6]{};
	bool have_mac = false;
	{
		int s = ::socket(AF_INET, SOCK_DGRAM | SOCK_CLOEXEC, 0);
		ifreq ifr{};
		std::strncpy(ifr.ifr_name, interface.c_str(), IFNAMSIZ - 1);
		if (s >= 0 && ::ioctl(s, SIOCGIFHWADDR, &ifr) == 0 && ifr.ifr_hwaddr.sa_family == 1) {
			std::memcpy(mac, ifr.ifr_hwaddr.sa_data, 6);
			have_mac = true;
		}
		if (s >= 0) ::close(s);
	}
	if (!have_source || !have_mac) {
		error = "the address is not on this interface's network";
		return;
	}
	int fd = ::socket(AF_PACKET, SOCK_DGRAM | SOCK_CLOEXEC, htons(ETH_P_ARP));
	if (fd < 0) {
		error = gotext::errno_text(errno);
		return;
	}
	struct Close {
		int fd;
		~Close() { ::close(fd); }
	} close_fd{fd};
	sockaddr_ll bind_addr{};
	bind_addr.sll_family = AF_PACKET;
	bind_addr.sll_protocol = htons(ETH_P_ARP);
	bind_addr.sll_ifindex = static_cast<int>(index);
	if (::bind(fd, reinterpret_cast<sockaddr *>(&bind_addr), sizeof bind_addr) != 0) {
		error = gotext::errno_text(errno);
		return;
	}
	// An Ethernet/IPv4 ARP request; the kernel adds the link header.
	unsigned char request[28]{};
	request[1] = 1;     // Ethernet
	request[2] = 0x08;  // IPv4
	request[4] = 6;
	request[5] = 4;
	request[7] = 1;  // request
	std::memcpy(request + 8, mac, 6);
	std::uint32_t src_n = htonl(source), dst_n = htonl(target);
	std::memcpy(request + 14, &src_n, 4);
	std::memcpy(request + 24, &dst_n, 4);
	sockaddr_ll everyone = bind_addr;
	everyone.sll_hatype = 1;
	everyone.sll_halen = 6;
	std::memset(everyone.sll_addr, 0xff, 6);
	auto deadline = std::chrono::steady_clock::now() + wait;
	auto next = std::chrono::steady_clock::now();
	unsigned char packet[128];
	while (std::chrono::steady_clock::now() < deadline) {
		if (sent < count && std::chrono::steady_clock::now() >= next) {
			if (::sendto(fd, request, sizeof request, 0, reinterpret_cast<sockaddr *>(&everyone), sizeof everyone) < 0) {
				error = "sending a request: " + gotext::errno_text(errno);
				return;
			}
			sent++;
			next += 1s;
		}
		auto until = deadline;
		if (sent < count && next < until) until = next;
		auto ms = std::chrono::duration_cast<std::chrono::milliseconds>(until - std::chrono::steady_clock::now()).count() + 1;
		pollfd p{fd, POLLIN, 0};
		int ready = ::poll(&p, 1, static_cast<int>(std::max<long long>(ms, 0)));
		if (ready < 0 && errno != EINTR) {
			error = gotext::errno_text(errno);
			return;
		}
		if (ready <= 0) continue;
		ssize_t n = ::recv(fd, packet, sizeof packet, MSG_DONTWAIT);
		if (n >= 28 && std::memcmp(packet + 14, &dst_n, 4) == 0) {
			answered = true;
			return;
		}
	}
}

// askHost: ARP requests on the USB network to the address the reports came from. The host's kernel answers them
// whatever its firewall; a hung one does not. A question not asked is never taken for a host that did not answer.
std::pair<std::string, std::string> ask_host(const std::string &peer)
{
	std::uint32_t target = 0;
	in_addr v4{};
	in6_addr v6{};
	if (::inet_pton(AF_INET, peer.c_str(), &v4) == 1) target = ntohl(v4.s_addr);
	else if (::inet_pton(AF_INET6, peer.c_str(), &v6) == 1 && IN6_IS_ADDR_V4MAPPED(&v6))
		target = static_cast<std::uint32_t>(v6.s6_addr[12]) << 24 | v6.s6_addr[13] << 16 | v6.s6_addr[14] << 8 | v6.s6_addr[15];
	else if (::inet_pton(AF_INET6, peer.c_str(), &v6) != 1)
		return {"unknown", "the address of the last report is not known"};
	bool ok;
	if (trimmed_file("/sys/class/net/" + interface + "/carrier", ok) != "1" || !ok)
		return {"unknown", "the USB network is down on the NanoKVM"};
	int sent = 0;
	bool answered = false;
	std::string error;
	if (target == 0) error = "the address is not on this interface's network";  // an IPv6 peer
	else arp_probe(target, watchdog::arp_requests, 5s, sent, answered, error);
	return watchdog::arp_verdict(sent, answered, error);
}

// bmc.Power over the ATX lines (service/vm ATX.State and ATX.Press), pressing as the actor "watchdog".
watchdog::Power live_power()
{
	auto lines = [] {
		system::Hardware h = system::hardware();
		if (!system::atx::enabled()) h.gpio_power = h.gpio_reset = h.gpio_power_led = h.gpio_hdd_led = "";
		return h;
	};
	watchdog::Power p;
	p.state = [lines](watchdog::ATXState &st) -> std::string {
		auto h = lines();
		bool led_line = !h.gpio_power_led.empty();
		st = {!h.gpio_power.empty(), !h.gpio_reset.empty(), led_line && !power_led_disconnected(), false};
		if (led_line) {
			auto leds = system::gpio::current_leds();
			if (!leds) return "ATX LED monitor stopped";
			if (st.power_led) st.power_on = leds->power;
		}
		return {};
	};
	p.press = [lines](const std::string &button, int ms) -> std::string {
		auto h = lines();
		std::string device;
		if (button == "power") {
			device = h.gpio_power;
			if (device.empty()) return "this board has no ATX power control";
		} else if (button == "reset") {
			device = h.gpio_reset;
			if (device.empty()) return "this board has no reset line";
		} else {
			return "unknown button";
		}
		if (ms < 100 || ms > 60000) return "the press must last 100-60000 ms";
		if (auto e = system::atx::pulse_line(device, std::chrono::milliseconds(ms)); !e.empty()) return e;
		system::journal::action("action.button", {{"button", button}, {"seconds", std::format("{:.1f}", ms / 1000.0)}}, "watchdog");
		return {};
	};
	p.sleep = [](int ms) -> std::string {
		std::this_thread::sleep_for(std::chrono::milliseconds(ms));
		return {};
	};
	return p;
}

// notify.Emit's journal entry (the channels and their repeat suppression come with the notifications family).
void emit(const std::string &severity, const std::string &message, const std::map<std::string, std::string> &vars)
{
	{
		std::lock_guard lock(watchdog_mutex);
		watchdog_last = message;
	}
	nkos::notify::Event e;
	e.kind = std::string(nkos::notify::kind_power);
	e.severity = severity == "critical" ? nkos::notify::Severity::critical
	             : severity == "warning" ? nkos::notify::Severity::warning
	                                     : nkos::notify::Severity::info;
	e.message = message;
	for (const auto &[k, v] : vars) e.vars[k] = v;
	nkos::notify::emit(std::move(e));
}

sensors::Nanos minutes(const watchdog::Config &c) { return c.minutes * 60'000'000'000LL; }

// stepHostWatchdog.
void step_watchdog()
{
	auto config = watchdog::load(watchdog_file);
	auto signs = signs_now(config, monotonic_now());
	bool due;
	{
		std::lock_guard lock(watchdog_mutex);
		due = watchdog_state.step(signs, minutes(config));
		if (due) watchdog_state.decided();
	}
	if (!due) return;
	// Asking the host and pressing take seconds: the page is not kept waiting for them.
	sensors::Nanos at;
	sensors::From from;
	store.last(at, from);
	std::map<std::string, std::string> vars{{"minutes", std::to_string(config.minutes)}};
	auto [answer, why] = ask_host(from.peer);
	if (answer == "alive") {
		// The agent stopped; the host lives.
		return emit("info", "watchdog.agent-silent", vars);
	}
	if (answer == "unknown") {
		log::warn("host watchdog: the host could not be asked: {}", why);
		vars["error"] = why;
		return emit("warning", "watchdog.unknown", vars);
	}
	// Whatever changed while the host was asked ends the decision: the setting, a report, a restart of the host,
	// sleep, power off. Nothing done is reopened: the next step decides anew.
	auto decided_under = config;
	config = watchdog::load(watchdog_file);
	signs = signs_now(config, monotonic_now());
	bool still;
	{
		std::lock_guard lock(watchdog_mutex);
		still = config == decided_under && watchdog_state.still_due(signs, minutes(config));
		if (!still) watchdog_state.reopen();
	}
	if (!still) {
		log::info("host watchdog: the host did not answer, but the situation changed while it was asked: nothing done, deciding anew");
		return;
	}
	std::string message = "watchdog.host-silent", error;
	auto power = live_power();
	if (config.action == watchdog::reset) message = "watchdog.reset", error = power.reset_host();
	else if (config.action == watchdog::cycle) message = "watchdog.cycle", error = power.cycle_host();
	if (!error.empty()) {
		log::error("host watchdog: {}", error);
		vars["error"] = error;
		message = "watchdog.failed";
	}
	emit("critical", message, vars);
}

// --- the page ---

std::string state_json()
{
	sensors::StateInputs in;
	in.mode = sensors::read_mode(mode_file);
	{
		std::lock_guard lock(reader_mutex);
		in.serial_state = serial_state;
	}
	in.serial_port = serial_device;
	usb_networks(&in.addresses);
	in.agent_json = sensors::agent_file_json("/nanokvm-sensors.exe", agent_file);
	in.linux_agent_json = sensors::agent_file_json("/nanokvm-sensors.sh", linux_agent_file);
	in.agent_version = sensors::addon_version(source_file);
	in.status_json = store.status_json(monotonic_now());
	in.view_json = sensors::view_json(sensors::read_view(view_file));
	sensors::helper_disk_lun(helper_lun_file, composer.disk_file, console_disk, in.disk_attached, in.disk_stale);
	auto wd = watchdog::load(watchdog_file);
	in.watchdog_action = wd.action;
	in.watchdog_minutes = wd.minutes;
	{
		std::lock_guard lock(watchdog_mutex);
		in.watchdog_armed = watchdog_state.armed();
		in.watchdog_last = watchdog_last;
	}
	return sensors::state_json(in);
}

bool file_sha256(const std::string &path, std::string &sum)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return false;
	EVP_MD_CTX *ctx = EVP_MD_CTX_new();
	EVP_DigestInit_ex(ctx, EVP_sha256(), nullptr);
	char buf[65536];
	while (in.read(buf, sizeof buf) || in.gcount() > 0) EVP_DigestUpdate(ctx, buf, static_cast<std::size_t>(in.gcount()));
	unsigned char digest[32];
	EVP_DigestFinal_ex(ctx, digest, nullptr);
	EVP_MD_CTX_free(ctx);
	sum.clear();
	for (unsigned char c : digest) sum += std::format("{:02x}", c);
	return !in.bad();
}

void serve_agent(http::Request &req, http::Response &res, const std::string &path, const std::string &name,
                 const std::string &type)
{
	http::defer(req, res, [path, name, type](http::Response &out) {
		std::string sum;
		if (!file_sha256(path, sum)) return out.text(404, "the sensors agent is not installed on this NanoKVM");
		out.set_header("Content-Disposition", "attachment; filename=\"" + name + "\"");
		out.set_header("Cache-Control", "no-cache");
		out.set_header("X-Checksum-Sha256", sum);
		out.status = 200;
		out.content_type = type;
		out.file = path;
		out.done = true;
	});
}

} // namespace

std::string switch_off_stale_host_sensors(bool selected)
{
	if (selected) return {};
	if (::unlink(mode_file.c_str()) != 0) {
		if (errno == ENOENT) return {};
		return "remove " + mode_file + ": " + gotext::errno_text(errno);
	}
	log::info("host sensors: switched off the setting of the removed add-on");
	return {};
}

void switch_off_removed_addons(const std::function<std::string(bool selected)> &rustdesk_switch_off)
{
	struct stat st{};
	if (::stat(addon_settings_checked.c_str(), &st) == 0) return;
	std::string error;
	bool rustdesk = apkpkg::selected("nanokvm-rustdesk", error);
	if (!error.empty()) return log::error("removed add-on settings: {}", error);
	bool sensors_chosen = apkpkg::selected("nanokvm-host-sensors", error);
	if (!error.empty()) return log::error("removed add-on settings: {}", error);
	if (auto e = rustdesk_switch_off(rustdesk); !e.empty()) return log::error("removed add-on settings: RustDesk: {}", e);
	if (auto e = switch_off_stale_host_sensors(sensors_chosen); !e.empty())
		return log::error("removed add-on settings: host sensors: {}", e);
	if (!config::atomic_write(addon_settings_checked, "", 0644))
		log::error("removed add-on settings: open {}: {}", addon_settings_checked, gotext::errno_text(errno));
}

std::string host_sensors_status(bool &on)
{
	on = sensors::read_mode(mode_file) != "off";
	return on ? store.status_json(monotonic_now()) : std::string();
}

void start_host_sensors()
{
	std::thread([] {
		for (;;) {
			reconcile();
			std::this_thread::sleep_for(10s);
		}
	}).detach();
	std::thread([] {
		for (;;) {
			std::this_thread::sleep_for(5s);
			step_watchdog();
		}
	}).detach();
}

void add_host_sensors_routes(http::Router &router)
{
	const auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/host-sensors", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) { api::send(out, 0, "success", state_json()); });
	}));
	router.add("PUT", "/api/vm/host-sensors", auth::guarded([](http::Request &req, http::Response &res) {
		std::string mode;
		if (!bind_mode(req.body, mode) || (mode != "off" && mode != "serial" && mode != "network"))
			return api::err(res, -1, "mode is off, serial or network");
		http::defer(req, res, [mode](http::Response &out) {
			if (!config::atomic_write(mode_file, mode + "\n", 0644)) {
				log::error("host sensors setting: {}", gotext::errno_text(errno));
				return api::err(out, -2, "the setting could not be saved");
			}
			reconcile();
			// A report that came the other way, or before the switch-off, is not what the new mode shows.
			store.clear();
			log::info("host sensors: {}", mode);
			api::send(out, 0, "success", state_json());
		});
	}, admin));
	router.add("PUT", "/api/vm/host-sensors/view", auth::guarded([](http::Request &req, http::Response &res) {
		sensors::View view;
		std::string error;
		if (!sensors::parse_view(std::string_view(req.body).substr(0, 2 << 20), view, error)) return api::err(res, -1, error);
		http::defer(req, res, [view](http::Response &out) {
			if (!config::atomic_write(view_file, sensors::view_json(view) + "\n", 0644)) {
				log::error("host sensors view: {}", gotext::errno_text(errno));
				return api::err(out, -2, "the setting could not be saved");
			}
			api::send(out, 0, "success", state_json());
		});
	}, admin));
	router.add("PUT", "/api/vm/host-sensors/watchdog", auth::guarded([](http::Request &req, http::Response &res) {
		watchdog::Config config{"", 0};
		std::size_t end;
		std::string error;
		bool ok = goj::decode(req.body, "Config", [&](goj::Reader &r) {
			r.object("Config", "hostwatchdog.Config", {
				{"action", [&](goj::Reader &x) { x.string(config.action); }},
				{"minutes", [&](goj::Reader &x) { x.integer(config.minutes); }},
			});
		}, end, error);
		if (!ok) return api::err(res, -1, "invalid watchdog setting");
		http::defer(req, res, [config](http::Response &out) {
			if (auto e = watchdog::save(watchdog_file, config); !e.empty()) return api::err(out, -1, e);
			log::info("host watchdog: {} after {} min", config.action, config.minutes);
			api::send(out, 0, "success", state_json());
		});
	}, admin));
	// Public: the computer has no credentials. Network mode and the USB network only; the JSON content type keeps a
	// page in the computer's browser from posting without a preflight.
	router.add("POST", ingest_path, [](http::Request &req, http::Response &res) {
		if (sensors::read_mode(mode_file) != "network")
			return res.text(403, "host sensors over the network are off on this NanoKVM");
		if (!sensors::from_network(req.peer, usb_networks())) return res.text(403, "host sensors are taken from the USB network only");
		if (!req.header("content-type").starts_with("application/json")) return res.text(415, "application/json is required");
		// The link is read now, with the report in hand: the report came over this life of the USB link.
		sensors::From from{req.peer, watchdog_link()};
		std::string_view body = std::string_view(req.body).substr(0, sensors::max_report_bytes + 1);
		if (auto e = store.accept(body, "network", from, monotonic_now()); !e.empty()) return res.text(400, e);
		res.status = 204;
		res.done = true;
	});
	// Public, like the console helper: the agents are part of the firmware.
	router.add("GET", "/nanokvm-sensors.exe", [](http::Request &req, http::Response &res) {
		serve_agent(req, res, agent_file, "nanokvm-sensors.exe", "application/octet-stream");
	});
	router.add("GET", "/nanokvm-sensors.sh", [](http::Request &req, http::Response &res) {
		serve_agent(req, res, linux_agent_file, "nanokvm-sensors.sh", "text/x-shellscript; charset=utf-8");
	});
}

} // namespace nkos::usb
