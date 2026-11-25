#include "system/gpio.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"

#include <linux/gpio.h>

#include <atomic>
#include <cerrno>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <map>
#include <mutex>
#include <poll.h>
#include <sys/eventfd.h>
#include <sys/ioctl.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace nkos::system::gpio {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

constexpr std::chrono::milliseconds sample_interval = 20ms, edge_interval = 20ms, retry_interval = 1s;
constexpr auto hdd_hold = 350ms, stream_hold = 60ms;
constexpr int enotsupp = 524;  // the kernel-internal errno some GPIO drivers leak

bool edges_unsupported_errno(int e) { return e == EINVAL || e == ENXIO || e == EOPNOTSUPP || e == enotsupp; }

} // namespace

Line &Line::operator=(Line &&other) noexcept
{
	if (this != &other) {
		if (fd_ >= 0) ::close(fd_);
		fd_ = other.fd_;
		other.fd_ = -1;
	}
	return *this;
}

Line::~Line()
{
	if (fd_ >= 0) ::close(fd_);
}

std::optional<Line> Line::open(const std::string &device, bool output, std::string &error, bool edges)
{
	auto a = device.find(':'), b = device.rfind(':');
	if (!device.starts_with("gpio-v2:") || a == b || b + 1 >= device.size()) {
		error = "invalid GPIO-v2 device \"" + device + "\"";
		return std::nullopt;
	}
	std::string label = device.substr(a + 1, b - a - 1);
	unsigned offset = static_cast<unsigned>(std::strtoul(device.c_str() + b + 1, nullptr, 10));
	std::vector<std::string> chips;
	if (DIR *d = ::opendir("/dev")) {
		while (auto *e = ::readdir(d))
			if (std::strncmp(e->d_name, "gpiochip", 8) == 0) chips.push_back(std::string("/dev/") + e->d_name);
		::closedir(d);
	}
	std::sort(chips.begin(), chips.end());
	for (const auto &path : chips) {
		int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
		if (fd < 0) {
			error = "open " + path + ": " + std::strerror(errno);
			return std::nullopt;
		}
		gpiochip_info info{};
		if (::ioctl(fd, GPIO_GET_CHIPINFO_IOCTL, &info) != 0) {
			error = std::strerror(errno);
			::close(fd);
			return std::nullopt;
		}
		if (label != info.label) {
			::close(fd);
			continue;
		}
		if (offset >= info.lines) {
			::close(fd);
			error = "GPIO line outside controller";
			return std::nullopt;
		}
		gpio_v2_line_request req{};
		req.offsets[0] = offset;
		req.num_lines = 1;
		std::strncpy(req.consumer, "nanokvm-atx", sizeof req.consumer - 1);
		req.config.flags = GPIO_V2_LINE_FLAG_INPUT;
		if (edges) req.config.flags |= GPIO_V2_LINE_FLAG_EDGE_RISING | GPIO_V2_LINE_FLAG_EDGE_FALLING;
		if (output) {
			req.config.flags = GPIO_V2_LINE_FLAG_OUTPUT;
			req.config.num_attrs = 1;
			req.config.attrs[0].mask = 1;
			req.config.attrs[0].attr.id = GPIO_V2_LINE_ATTR_ID_OUTPUT_VALUES;
			req.config.attrs[0].attr.values = 0;  // requested with the switch released
		}
		int rc = ::ioctl(fd, GPIO_V2_GET_LINE_IOCTL, &req);
		int e = errno;
		::close(fd);
		if (rc != 0) {
			errno = e;
			error = "request " + device + ": " + std::strerror(e);
			return std::nullopt;
		}
		if (edges) ::fcntl(req.fd, F_SETFL, ::fcntl(req.fd, F_GETFL) | O_NONBLOCK);
		return Line(req.fd);
	}
	error = "GPIO controller \"" + label + "\" not found";
	return std::nullopt;
}

bool Line::set(bool active, std::string &error)
{
	gpio_v2_line_values v{};
	v.mask = 1;
	v.bits = active ? 1 : 0;
	if (::ioctl(fd_, GPIO_V2_LINE_SET_VALUES_IOCTL, &v) == 0) return true;
	error = std::strerror(errno);
	return false;
}

std::optional<bool> Line::get(std::string &error)
{
	gpio_v2_line_values v{};
	v.mask = 1;
	if (::ioctl(fd_, GPIO_V2_LINE_GET_VALUES_IOCTL, &v) != 0) {
		error = std::strerror(errno);
		return std::nullopt;
	}
	return (v.bits & 1) != 0;
}

namespace {

std::mutex state_mutex;
std::optional<LedStatus> state;  // nullopt: no reading, or a failure
Clock::time_point hdd_activity_at{};
std::thread monitor;
int stop_fd = -1;
std::atomic<bool> stopping{false};

std::map<int, std::function<void()>> led_subscribers;
int next_led_subscriber = 1;

void notify_led_subscribers()
{
	auto copy = led_subscribers;
	for (auto &[id, fn] : copy) fn();
}

void publish(std::optional<LedStatus> s)
{
	bool changed;
	{
		std::lock_guard lock(state_mutex);
		changed = state.has_value() != s.has_value() ||
		          (s && (state->power != s->power || state->hdd != s->hdd));
		state = s;
		if (!s) hdd_activity_at = {};
	}
	if (changed) loop::post(notify_led_subscribers);
}

void note_hdd(Clock::time_point at)
{
	std::lock_guard lock(state_mutex);
	if (at > hdd_activity_at) hdd_activity_at = at;
}

bool wait_stop(std::chrono::milliseconds timeout)
{
	struct pollfd p{stop_fd, POLLIN, 0};
	return ::poll(&p, 1, static_cast<int>(timeout.count())) > 0 || stopping;
}

// Drains a line's queued edge events; the newest one's time, or nullopt.
std::optional<Clock::time_point> drain_edges(int fd, bool &failed, bool &unsupported)
{
	gpio_v2_line_event events[64];
	std::optional<Clock::time_point> newest;
	for (;;) {
		ssize_t n = ::read(fd, events, sizeof events);
		if (n < 0) {
			if (errno == EAGAIN || errno == EINTR) break;
			unsupported = edges_unsupported_errno(errno);
			failed = true;
			break;
		}
		if (n == 0 || n % static_cast<ssize_t>(sizeof(gpio_v2_line_event))) {
			failed = true;
			break;
		}
		for (ssize_t i = 0; i < n / static_cast<ssize_t>(sizeof(gpio_v2_line_event)); i++) {
			// Timestamps are CLOCK_MONOTONIC, the clock of steady_clock.
			Clock::time_point at{std::chrono::nanoseconds(events[i].timestamp_ns)};
			if (at > Clock::now()) at = Clock::now();
			if (!newest || at > *newest) newest = at;
		}
	}
	return newest;
}

void run(std::string power_path, std::string hdd_path)
{
	bool edges_unsupported = false;
	int logged = 0;  // 1 edges, 2 polling
	while (!stopping) {
		std::string error;
		bool edges = !edges_unsupported;
		std::optional<Line> power = Line::open(power_path, false, error, edges), hdd;
		if (power && !hdd_path.empty()) hdd = Line::open(hdd_path, false, error, edges);
		if (edges && (!power || (!hdd_path.empty() && !hdd)) && edges_unsupported_errno(errno)) {
			edges_unsupported = true;
			log::info("ATX LED monitor: polling every 20ms: {}", error);
			logged = 2;
			continue;
		}
		if (!power || (!hdd_path.empty() && !hdd)) {
			publish(std::nullopt);
			if (wait_stop(retry_interval)) return;
			continue;
		}
		if (edges && logged != 1) log::info("ATX LED monitor: waiting for GPIO edge events, serviced at most every 20ms");
		if (!edges && logged != 2) log::info("ATX LED monitor: polling every 20ms");
		logged = edges ? 1 : 2;

		Clock::time_point hdd_until{};
		auto levels = [&]() -> bool {
			std::string e;
			auto p = power->get(e);
			if (!p) return false;
			auto now = Clock::now();
			bool hdd_active = false;
			if (hdd) {
				auto h = hdd->get(e);
				if (!h) return false;
				if (!*h) {
					hdd_until = now + stream_hold;
					note_hdd(now);
				}
				hdd_active = !*h || now < hdd_until;
			}
			publish(LedStatus{!*p, hdd_active});
			return true;
		};

		bool retry = true;
		if (!edges) {
			while (!stopping && levels())
				if (wait_stop(sample_interval)) return;
			if (stopping) return;
			publish(std::nullopt);
		} else {
			Clock::time_point paused[2]{};
			if (!levels()) {
				publish(std::nullopt);
			} else {
				for (;;) {
					auto now = Clock::now();
					std::vector<pollfd> fds{{stop_fd, POLLIN, 0}};
					int index[2] = {-1, -1};
					Clock::time_point wake = Clock::time_point::max();
					for (int i = 0; i < 2; i++) {
						Line *l = i == 0 ? &*power : hdd ? &*hdd : nullptr;
						if (!l) continue;
						if (now >= paused[i]) {
							index[i] = static_cast<int>(fds.size());
							fds.push_back({l->fd(), POLLIN, 0});
						} else {
							wake = std::min(wake, paused[i]);
						}
					}
					if (hdd_until > now) wake = std::min(wake, hdd_until);
					int timeout = wake == Clock::time_point::max()
						              ? -1
						              : static_cast<int>(std::chrono::duration_cast<std::chrono::milliseconds>(wake - now).count()) + 1;
					if (::poll(fds.data(), fds.size(), timeout) < 0 && errno != EINTR) break;
					if (fds[0].revents || stopping) return;
					bool failed = false, unsupported = false;
					for (int i = 0; i < 2; i++) {
						if (index[i] < 0 || !fds[static_cast<std::size_t>(index[i])].revents) continue;
						Line *l = i == 0 ? &*power : &*hdd;
						auto at = drain_edges(l->fd(), failed, unsupported);
						paused[i] = Clock::now() + edge_interval;
						// Any HDD edge means the LED was lit then, even if the blink ended before the levels are read.
						if (i == 1 && at) {
							note_hdd(*at);
							if (*at + stream_hold > hdd_until) hdd_until = *at + stream_hold;
						}
					}
					if (unsupported) {
						edges_unsupported = true;
						log::info("ATX LED monitor: polling every 20ms: edge events unsupported");
						logged = 2;
						retry = false;  // reopen at once, for polling
						break;
					}
					if (failed || !levels()) {
						publish(std::nullopt);
						break;
					}
				}
			}
		}
		if (retry && wait_stop(retry_interval)) return;
	}
}

} // namespace

void start_led_monitor(const std::string &power, const std::string &hdd)
{
	if (monitor.joinable() || power.empty()) return;
	stop_fd = ::eventfd(0, EFD_CLOEXEC | EFD_NONBLOCK);
	stopping = false;
	monitor = std::thread(run, power, hdd);
}

void stop_led_monitor()
{
	if (monitor.joinable()) {
		stopping = true;
		std::uint64_t one = 1;
		(void)!::write(stop_fd, &one, sizeof one);
		monitor.join();
		::close(stop_fd);
		stop_fd = -1;
	}
	{
		std::lock_guard lock(state_mutex);
		state.reset();
		hdd_activity_at = {};
	}
	// Deliver the stop on the loop before a new monitor can start, even if no reading ever succeeded.
	notify_led_subscribers();
}

std::optional<LedStatus> stream_leds()
{
	std::lock_guard lock(state_mutex);
	return state;
}

int subscribe_leds(std::function<void()> on_change)
{
	int id = next_led_subscriber++;
	led_subscribers[id] = std::move(on_change);
	return id;
}

void unsubscribe_leds(int id) { led_subscribers.erase(id); }

bool led_monitor_running() { return monitor.joinable(); }

std::optional<LedStatus> current_leds()
{
	std::lock_guard lock(state_mutex);
	if (!state) return std::nullopt;
	LedStatus s = *state;
	if (Clock::now() < hdd_activity_at + hdd_hold) s.hdd = true;
	return s;
}

} // namespace nkos::system::gpio
