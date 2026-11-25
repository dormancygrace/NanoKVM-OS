#pragma once

// GPIO lines through the character device (GPIO ABI v2), as the Go server's internal/gpioio, and the ATX LED
// monitor (service/vm/gpio_monitor.go): the host's power and HDD LEDs, read on edge events when the lines report
// them (each line serviced at most every 20 ms), else sampled every 20 ms. A blink of the HDD LED counts as activity
// for 350 ms for readers of the current state and 60 ms for the live stream. The LEDs are active low.

#include <chrono>
#include <functional>
#include <optional>
#include <string>

namespace nkos::system::gpio {

// A requested line; closes on destruction.
class Line {
public:
	// device: "gpio-v2:<label>:<offset>"; error describes a failure.
	static std::optional<Line> open(const std::string &device, bool output, std::string &error, bool edges = false);
	Line(Line &&other) noexcept : fd_(other.fd_) { other.fd_ = -1; }
	Line &operator=(Line &&other) noexcept;
	~Line();
	bool set(bool active, std::string &error);
	std::optional<bool> get(std::string &error);
	int fd() const { return fd_; }

private:
	explicit Line(int fd) : fd_(fd) {}
	int fd_ = -1;
};

struct LedStatus {
	bool power = false, hdd = false;
};

// The monitor of the ATX LED lines (power, hdd: device paths, hdd may be empty). start() runs it on a thread of its
// own; current() is the state with the long HDD hold, nullopt before the first reading or after a failure.
void start_led_monitor(const std::string &power, const std::string &hdd);
void stop_led_monitor();
std::optional<LedStatus> current_leds();
// The state with the short HDD hold (for live streams); nullopt before a reading or after a failure.
std::optional<LedStatus> stream_leds();
// on_change runs on the loop thread whenever the stream state changes (and when the monitor stops); returns an id
// for unsubscribe.
int subscribe_leds(std::function<void()> on_change);
void unsubscribe_leds(int id);
bool led_monitor_running();

} // namespace nkos::system::gpio
