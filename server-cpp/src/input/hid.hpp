#pragma once

// The USB HID gadget: keyboard /dev/hidg0, relative mouse /dev/hidg1, absolute mouse /dev/hidg2, as the Go server's
// service/hid. Reports are written by two threads, one for the keyboard and one for the mice, so a host that does
// not take keyboard reports does not hold the mouse up and nothing waits on the event loop. A write retries for
// 50 ms while the host does not accept reports (EAGAIN); a failed one closes the device (reopened by the next
// write), drops the reports queued behind it on that lane and releases what the lane held down.
//
// A lane is one producer's queue (a page's socket): its reports keep their order, and closing it releases what it
// still holds. The keyboard LED state the host sends (num, caps, scroll lock) is read by a thread of its own.

#include "common/time.hpp"
#include "input/control.hpp"

#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <vector>

namespace nkos::input::hid {

struct Report {
	std::vector<std::uint8_t> data;  // keyboard: 8 bytes; relative mouse: 5 (or 4); absolute: 7 (or 6)
	// Asked on the writer thread just before the write; a refused report is not written (complete(false)).
	std::shared_ptr<control::Reservation> reservation;
	// Loop thread, after the write: ok, and which kinds the writer released after a failure.
	std::function<void(bool ok)> complete;
	std::function<void(control::ReportKind)> reset;
	// false: the reservation only gates the write; its owner completes it once (a paste, a key sequence).
	bool complete_reservation = true;
};

class Lane {
public:
	Lane();
	~Lane();  // closes: releases held keys and buttons through the reservation of the last report
	void keyboard(Report report);
	void mouse(Report report);

	struct State;
	std::shared_ptr<State> state_;
};

// Releases every key and button on all three devices (a control switch), in order after the queued reports.
void release_all();

// Rereads the disabled functions (usb.disable_*, disable_hid) and reopens the devices.
void open();

// Restarts the USB PHY (S03usbdev restart_phy) with the devices closed, then reopens them (retrying for 2 s);
// done runs on the loop thread with an error message, empty on success.
void reset_usb_phy(std::function<void(std::string error)> done);

// 4- and 6-byte mouse reports of older pages get the AC pan byte.
std::vector<std::uint8_t> normalize_mouse_report(std::vector<std::uint8_t> report);

// The functions of the USB profile (for the live status): enabled, and whether the port is a gadget at all.
struct Functions {
	bool keyboard = false, relative = false, absolute = false;
};
Functions input_functions();

// Keyboard LEDs, as the host last set them; known is false until its first output report. Loop thread.
struct LedStatus {
	bool keyboard_enabled = false, num_lock = false, caps_lock = false, scroll_lock = false, known = false;
	std::string updated_at;  // RFC 3339 (Go's zero time until known)
	WallClock::time_point updated{};
};
inline constexpr const char *led_status_event = "hid-led-status";
LedStatus led_status();
std::string led_status_json(const LedStatus &status);
void on_led_status(std::function<void(const LedStatus &)> subscriber);
bool keyboard_usb_enabled();

// Starts the writer threads and the LED reader; stop() ends them.
void start();
void stop();

} // namespace nkos::input::hid
