#include "input/hid.hpp"
#include "usb/core_hooks.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "common/time.hpp"

#include <glaze/glaze.hpp>

#include <atomic>
#include <cerrno>
#include <condition_variable>
#include <cstring>
#include <deque>
#include <fcntl.h>
#include <future>
#include <mutex>
#include <poll.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>

namespace nkos::input::hid {

namespace {

using namespace std::chrono_literals;
using Bytes = std::vector<std::uint8_t>;

constexpr const char *hid0 = "/dev/hidg0", *hid1 = "/dev/hidg1", *hid2 = "/dev/hidg2";
constexpr const char *flags_dir = "/etc/kvm/usb";
constexpr auto write_timeout = 50ms;

bool exists(const std::string &path)
{
	struct stat st{};
	return ::lstat(path.c_str(), &st) == 0;
}

struct Disabled {
	bool keyboard = false, relative = false, absolute = false;
};

Disabled disabled_functions()
{
	bool all = exists(std::string(flags_dir) + "/disable_hid");
	return {all || exists(std::string(flags_dir) + "/usb.disable_keyboard"),
	        all || exists(std::string(flags_dir) + "/usb.disable_relative"),
	        all || exists(std::string(flags_dir) + "/usb.disable_absolute")};
}

// One device file, owned by one writer thread.
struct Device {
	const char *path;
	int fd = -1;
	bool disabled = false;

	bool ensure_open(std::string &error)
	{
		if (fd >= 0) return true;
		fd = ::open(path, O_WRONLY | O_NONBLOCK | O_CLOEXEC);
		if (fd < 0) {
			error = std::string(path) + ": " + std::strerror(errno);
			return false;
		}
		return true;
	}
	void close()
	{
		if (fd >= 0) ::close(fd);
		fd = -1;
	}
	// A write the host does not take (EAGAIN) is retried until the timeout; a failure closes the device.
	bool write(const Bytes &data, std::string &error)
	{
		if (disabled) return true;
		if (!ensure_open(error)) return false;
		auto deadline = std::chrono::steady_clock::now() + write_timeout;
		for (;;) {
			ssize_t n = ::write(fd, data.data(), data.size());
			if (n == static_cast<ssize_t>(data.size())) return true;
			if (n >= 0) {
				error = "short write";
				break;
			}
			if (errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR) {
				error = std::strerror(errno);
				break;
			}
			if (std::chrono::steady_clock::now() >= deadline) {
				error = "timeout after 50ms";
				break;
			}
			std::this_thread::sleep_for(1ms);
		}
		error = std::string(path) + ": " + error;
		close();
		return false;
	}
};

std::atomic<bool> keyboard_open{false};
void notify_led_reader();

Bytes keyboard_release() { return Bytes(8, 0); }
Bytes relative_release() { return Bytes(5, 0); }
Bytes absolute_release(const Bytes &position)
{
	Bytes r(7, 0);
	if (position.size() >= 5) std::copy(position.begin() + 1, position.begin() + 5, r.begin() + 1);
	return r;
}

bool keyboard_active(const Bytes &r)
{
	if (r.size() != 8) return false;
	if (r[0]) return true;
	for (std::size_t i = 2; i < 8; i++)
		if (r[i]) return true;
	return false;
}

void post_complete(const Report &report, bool ok)
{
	loop::post([reservation = report.complete_reservation ? report.reservation : nullptr, complete = report.complete, ok] {
		if (reservation) reservation->complete(ok);
		if (complete) complete(ok);
	});
}

void post_reset(const std::function<void(control::ReportKind)> &reset, control::ReportKind kind)
{
	if (reset) loop::post([reset, kind] { reset(kind); });
}

// A gated write: under the write lane, only while the reservation still holds (without one: always).
bool gated(const std::shared_ptr<control::Reservation> &reservation, const std::function<bool(std::string &)> &write,
           std::string &error)
{
	if (!reservation) return write(error);
	control::lock_write_lane();
	bool ok;
	if (!reservation->valid()) {
		error = "manual input is blocked";
		ok = false;
	} else {
		ok = write(error);
	}
	control::unlock_write_lane();
	return ok;
}

} // namespace

struct Lane::State {
	// Keyboard thread.
	bool keyboard_active = false;
	std::shared_ptr<control::Reservation> keyboard_execute;
	std::function<void(control::ReportKind)> keyboard_reset;
	// Mouse thread.
	bool relative_active = false, absolute_active = false;
	Bytes absolute_release_report = Bytes(7, 0);
	std::shared_ptr<control::Reservation> mouse_execute;
	std::function<void(control::ReportKind)> mouse_reset;
};

namespace {

struct Job {
	enum Kind { report, close_lane, release_all, open, close_devices, open_retry, hold } kind;
	std::shared_ptr<Lane::State> lane;
	Report data;
	std::function<void(bool ok)> done;  // close_devices, open_retry: on the writer thread
};

class Writer {
public:
	explicit Writer(bool mouse) : mouse_(mouse) {}

	void start() { thread_ = std::thread([this] { run(); }); }
	void stop()
	{
		{
			std::lock_guard lock(mutex_);
			stopping_ = true;
		}
		wake_.notify_all();
		if (thread_.joinable()) thread_.join();
	}
	void push(Job job)
	{
		{
			std::lock_guard lock(mutex_);
			jobs_.push_back(std::move(job));
		}
		wake_.notify_one();
	}

private:
	void run()
	{
		for (;;) {
			Job job;
			{
				std::unique_lock lock(mutex_);
				wake_.wait(lock, [&] { return stopping_ || !jobs_.empty(); });
				if (jobs_.empty()) break;
				job = std::move(jobs_.front());
				jobs_.pop_front();
			}
			switch (job.kind) {
			case Job::open: reopen(); break;
			case Job::hold:
				// A change of the USB gadget: the devices closed, this writer held until the change is over.
				for (auto *d : devices()) d->close();
				if (!mouse_) {
					keyboard_open = false;
					notify_led_reader();
				}
				if (job.done) job.done(true);
				reopen();
				break;
			case Job::close_devices:
				for (auto *d : devices()) d->close();
				if (!mouse_) {
					keyboard_open = false;
					notify_led_reader();
				}
				if (job.done) job.done(true);
				break;
			case Job::open_retry: {
				auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(2);
				bool ok = false;
				for (;;) {
					reopen();
					ok = true;
					for (auto *d : devices()) ok = ok && (d->disabled || d->fd >= 0);
					if (ok || std::chrono::steady_clock::now() >= deadline) break;
					std::this_thread::sleep_for(std::chrono::milliseconds(100));
				}
				if (job.done) job.done(ok);
				break;
			}
			case Job::release_all: release_everything(); break;
			case Job::close_lane: close_lane(*job.lane); break;
			case Job::report:
				if (mouse_) mouse_report(*job.lane, job.data);
				else keyboard_report(*job.lane, job.data);
				break;
			}
		}
		for (auto &d : devices()) d->close();
	}

	std::vector<Device *> devices() { return mouse_ ? std::vector<Device *>{&relative_, &absolute_} : std::vector<Device *>{&keyboard_}; }

	void reopen()
	{
		Disabled d = disabled_functions();
		keyboard_.disabled = d.keyboard;
		relative_.disabled = d.relative;
		absolute_.disabled = d.absolute;
		for (auto *dev : devices()) {
			dev->close();
			std::string error;
			if (!dev->disabled && !dev->ensure_open(error)) log::error("open {} failed: {}", dev->path, error);
		}
		if (!mouse_) {
			keyboard_open = keyboard_.fd >= 0;
			notify_led_reader();
		}
	}

	bool write_keyboard(const Bytes &r, std::string &error)
	{
		bool ok = keyboard_.write(r, error);
		if (!ok) {
			keyboard_open = false;
			notify_led_reader();
		} else if (!keyboard_open && keyboard_.fd >= 0) {
			keyboard_open = true;
			notify_led_reader();
		}
		return ok;
	}

	// The reports of a lane queued behind a failed one are stale.
	int drop_queued(const Lane::State &lane)
	{
		int dropped = 0;
		std::lock_guard lock(mutex_);
		for (auto it = jobs_.begin(); it != jobs_.end();) {
			if (it->kind == Job::report && it->lane.get() == &lane) {
				post_complete(it->data, false);
				it = jobs_.erase(it);
				dropped++;
			} else {
				++it;
			}
		}
		return dropped;
	}

	void keyboard_report(Lane::State &lane, Report &r)
	{
		lane.keyboard_execute = r.reservation;
		lane.keyboard_reset = r.reset;
		if (r.data.size() != 8) {
			post_complete(r, false);
			return;
		}
		std::string error;
		if (!gated(r.reservation, [&](std::string &e) { return write_keyboard(r.data, e); }, error)) {
			log::error("write to {} failed: {}", hid0, error);
			if (int n = drop_queued(lane)) log::debug("dropped {} stale keyboard HID reports after write failure", n);
			std::string cleanup_error;
			if (!gated(r.reservation, [&](std::string &e) { return write_keyboard(keyboard_release(), e); }, cleanup_error)) {
				log::error("release keyboard after write failure failed: {}", cleanup_error);
				lane.keyboard_active = lane.keyboard_active || keyboard_active(r.data);
				post_complete(r, false);
				return;
			}
			lane.keyboard_active = false;
			post_reset(r.reset, control::ReportKind::keyboard);
			post_complete(r, false);
			return;
		}
		lane.keyboard_active = keyboard_active(r.data);
		post_complete(r, true);
	}

	void mouse_report(Lane::State &lane, Report &r)
	{
		r.data = normalize_mouse_report(std::move(r.data));
		lane.mouse_execute = r.reservation;
		lane.mouse_reset = r.reset;
		auto rel = [&](const Bytes &b, std::string &e) { return gated(r.reservation, [&](std::string &x) { return relative_.write(b, x); }, e); };
		auto abs = [&](const Bytes &b, std::string &e) { return gated(r.reservation, [&](std::string &x) { return absolute_.write(b, x); }, e); };
		auto failure = [&](const std::string &write_error) {
			log::error("mouse HID write failed: {}", write_error);
			if (int n = drop_queued(lane)) log::debug("dropped {} stale mouse HID reports after write failure", n);
			if (r.data.size() == 5 && r.data[0]) lane.relative_active = true;
			if (r.data.size() == 7 && r.data[0]) {
				lane.absolute_active = true;
				lane.absolute_release_report = absolute_release(r.data);
			}
			std::string e;
			if (lane.relative_active || r.data.size() == 5) {
				if (!rel(relative_release(), e)) log::error("release relative mouse after write failure failed: {}", e);
				else {
					lane.relative_active = false;
					post_reset(r.reset, control::ReportKind::relative_mouse);
				}
			}
			if (lane.absolute_active || r.data.size() == 7) {
				Bytes release = r.data.size() == 7 ? absolute_release(r.data) : lane.absolute_release_report;
				if (!abs(release, e)) log::error("release absolute mouse after write failure failed: {}", e);
				else {
					lane.absolute_active = false;
					post_reset(r.reset, control::ReportKind::absolute_mouse);
				}
			}
			post_complete(r, false);
		};
		std::string error;
		if (r.data.size() == 5) {
			if (lane.absolute_active) {
				if (!abs(lane.absolute_release_report, error)) return failure(error);
				lane.absolute_active = false;
				post_reset(r.reset, control::ReportKind::absolute_mouse);
			}
			if (!rel(r.data, error)) return failure(error);
			lane.relative_active = r.data[0] != 0;
			post_complete(r, true);
		} else if (r.data.size() == 7) {
			if (lane.relative_active) {
				if (!rel(relative_release(), error)) return failure(error);
				lane.relative_active = false;
				post_reset(r.reset, control::ReportKind::relative_mouse);
			}
			if (!abs(r.data, error)) return failure(error);
			lane.absolute_release_report = absolute_release(r.data);
			lane.absolute_active = r.data[0] != 0;
			post_complete(r, true);
		} else {
			post_complete(r, false);
		}
	}

	void close_lane(Lane::State &lane)
	{
		std::string error;
		if (!mouse_) {
			if (!lane.keyboard_active) return;
			if (!gated(lane.keyboard_execute, [&](std::string &e) { return write_keyboard(keyboard_release(), e); }, error)) {
				log::error("release keyboard on queue close failed: {}", error);
				return;
			}
			post_reset(lane.keyboard_reset, control::ReportKind::keyboard);
			return;
		}
		if (lane.relative_active) {
			if (!gated(lane.mouse_execute, [&](std::string &e) { return relative_.write(relative_release(), e); }, error))
				log::error("release relative mouse on queue close failed: {}", error);
			else post_reset(lane.mouse_reset, control::ReportKind::relative_mouse);
		}
		if (lane.absolute_active) {
			if (!gated(lane.mouse_execute, [&](std::string &e) { return absolute_.write(lane.absolute_release_report, e); }, error))
				log::error("release absolute mouse on queue close failed: {}", error);
			else post_reset(lane.mouse_reset, control::ReportKind::absolute_mouse);
		}
	}

	void release_everything()
	{
		std::string error;
		if (!mouse_) {
			if (!write_keyboard(keyboard_release(), error)) log::warn("failed to release HID state during control switch: release keyboard: {}", error);
			return;
		}
		if (!relative_.write(relative_release(), error)) log::warn("failed to release HID state during control switch: release relative mouse: {}", error);
		if (!absolute_.write(absolute_release({}), error)) log::warn("failed to release HID state during control switch: release absolute mouse: {}", error);
	}

	bool mouse_;
	Device keyboard_{hid0}, relative_{hid1}, absolute_{hid2};
	std::mutex mutex_;
	std::condition_variable wake_;
	std::deque<Job> jobs_;
	bool stopping_ = false;
	std::thread thread_;
};

Writer keyboard_writer(false), mouse_writer(true);

// --- keyboard LEDs -----------------------------------------------------------------------------------------------

LedStatus leds;
std::vector<std::function<void(const LedStatus &)>> led_subscribers;
int led_notify[2] = {-1, -1};
std::atomic<bool> led_stopping{false};
std::thread led_thread;

void notify_led_reader()
{
	if (led_notify[1] >= 0) {
		char b = 1;
		(void)!::write(led_notify[1], &b, 1);
	}
}

void drain(int fd)
{
	char buf[64];
	while (::read(fd, buf, sizeof buf) > 0) {
	}
}

void update_leds(std::uint8_t report)
{
	LedStatus next;
	next.num_lock = report & 1;
	next.caps_lock = report & 2;
	next.scroll_lock = report & 4;
	next.known = true;
	next.updated = WallClock::now();
	next.updated_at = rfc3339nano(next.updated);
	bool same = next.num_lock == leds.num_lock && next.caps_lock == leds.caps_lock &&
	            next.scroll_lock == leds.scroll_lock && next.known == leds.known;
	leds = next;
	if (same) return;
	for (auto &s : led_subscribers) s(led_status());
}

// Waits for the keyboard's output reports (one byte: the LED bitmap) while the keyboard device is open; a change of
// the device wakes it through the notifier.
void read_leds()
{
	int reader = -1;
	while (!led_stopping) {
		if (reader < 0 && keyboard_open) reader = ::open(hid0, O_RDONLY | O_CLOEXEC);
		struct pollfd fds[2] = {{led_notify[0], POLLIN, 0}, {reader, POLLIN, 0}};
		int n = ::poll(fds, reader >= 0 ? 2 : 1, -1);
		if (n < 0) continue;
		if (fds[0].revents & POLLIN) {
			drain(led_notify[0]);
			if (!keyboard_open && reader >= 0) {
				::close(reader);
				reader = -1;
			}
			continue;
		}
		if (reader < 0) continue;
		if (fds[1].revents & (POLLERR | POLLHUP | POLLNVAL)) {
			::close(reader);
			reader = -1;
			std::this_thread::sleep_for(100ms);
			continue;
		}
		std::uint8_t buf[64];
		ssize_t got = ::read(reader, buf, sizeof buf);
		if (got > 0) {
			std::uint8_t report = buf[0];
			loop::post([report] { update_leds(report); });
			continue;
		}
		::close(reader);
		reader = -1;
		std::this_thread::sleep_for(100ms);
	}
	if (reader >= 0) ::close(reader);
}

struct LedJson {
	bool keyboardEnabled;
	bool numLock;
	bool capsLock;
	bool scrollLock;
	bool known;
	std::string updatedAt;
};

} // namespace

Lane::Lane() : state_(std::make_shared<State>()) {}

Lane::~Lane()
{
	keyboard_writer.push({Job::close_lane, state_, {}});
	mouse_writer.push({Job::close_lane, state_, {}});
}

void Lane::keyboard(Report report) { keyboard_writer.push({Job::report, state_, std::move(report)}); }
void Lane::mouse(Report report) { mouse_writer.push({Job::report, state_, std::move(report)}); }

void release_all()
{
	keyboard_writer.push({Job::release_all, nullptr, {}});
	mouse_writer.push({Job::release_all, nullptr, {}});
}

void open()
{
	keyboard_writer.push({Job::open, nullptr, {}});
	mouse_writer.push({Job::open, nullptr, {}});
}

std::function<void()> hold_closed()
{
	auto release = std::make_shared<std::promise<void>>();
	auto released = release->get_future().share();
	auto k = std::make_shared<std::promise<void>>(), m = std::make_shared<std::promise<void>>();
	auto fk = k->get_future(), fm = m->get_future();
	keyboard_writer.push({Job::hold, nullptr, {}, [k, released](bool) {
		k->set_value();
		released.wait();
	}});
	mouse_writer.push({Job::hold, nullptr, {}, [m, released](bool) {
		m->set_value();
		released.wait();
	}});
	fk.wait();
	fm.wait();
	return [release] { release->set_value(); };
}

void reset_usb_phy(std::function<void(std::string error)> done)
{
	loop::blocking([done = std::move(done)] {
		auto wait_both = [](Job::Kind kind) {
			std::promise<bool> k, m;
			auto fk = k.get_future(), fm = m.get_future();
			keyboard_writer.push({kind, nullptr, {}, [&k](bool ok) { k.set_value(ok); }});
			mouse_writer.push({kind, nullptr, {}, [&m](bool ok) { m.set_value(ok); }});
			return fk.get() && fm.get();
		};
		wait_both(Job::close_devices);
		auto r = process::run({"sh", "/etc/init.d/S03usbdev", "restart_phy"}, {.timeout = std::chrono::seconds(10)});
		std::string error;
		if (!r.ok()) error = "restart usb phy: " + (r.timed_out ? std::string("timeout") : std::to_string(r.exit_code));
		if (!wait_both(Job::open_retry) && error.empty()) error = "reopen HID devices after usb phy reset";
		loop::post([done, error] { done(error); });
	});
}

std::vector<std::uint8_t> normalize_mouse_report(std::vector<std::uint8_t> report)
{
	if (report.size() == 4 || report.size() == 6) report.push_back(0);
	return report;
}

Functions input_functions()
{
	Disabled d = disabled_functions();
	// As a USB host, and with USB off, the gadget is unbound: no input reaches the computer.
	if (exists(std::string(flags_dir) + "/usb.host") || exists(std::string(flags_dir) + "/usb.off")) d = {true, true, true};
	return {!d.keyboard, !d.relative, !d.absolute};
}

bool keyboard_usb_enabled() { return exists("/sys/kernel/config/usb_gadget/g0/configs/c.1/hid.GS0"); }

LedStatus led_status()
{
	LedStatus s = leds;
	s.keyboard_enabled = keyboard_usb_enabled();
	if (!s.keyboard_enabled) s.known = false;
	if (s.updated_at.empty()) s.updated_at = "0001-01-01T00:00:00Z";
	return s;
}

std::string led_status_json(const LedStatus &s)
{
	std::string out;
	(void)glz::write_json(LedJson{s.keyboard_enabled, s.num_lock, s.caps_lock, s.scroll_lock, s.known, s.updated_at}, out);
	return out;
}

void on_led_status(std::function<void(const LedStatus &)> subscriber) { led_subscribers.push_back(std::move(subscriber)); }

void start()
{
	if (::pipe2(led_notify, O_CLOEXEC | O_NONBLOCK) != 0) log::error("keyboard LED notifier: {}", std::strerror(errno));
	keyboard_writer.start();
	mouse_writer.start();
	led_thread = std::thread(read_leds);
	open();
}

void stop()
{
	keyboard_writer.stop();
	mouse_writer.stop();
	led_stopping = true;
	notify_led_reader();
	if (led_thread.joinable()) led_thread.join();
}

} // namespace nkos::input::hid
