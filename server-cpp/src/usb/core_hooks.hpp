#pragma once

// What the USB family needs from the HID writer and the ATX switch, implemented in input/hid.cpp and
// system/atx.cpp (the core's files) next to what they guard.

#include <chrono>
#include <functional>
#include <string>

namespace nkos::input::hid {

// Go's h.CloseNoLock ... defer h.OpenNoLock around a change of the gadget: closes the three devices and holds both
// writer threads where they are (reports queued meanwhile wait, as behind Go's HID lock) until the returned
// function runs, which reopens the devices once and lets the writers go on. Blocking: never on the loop thread.
std::function<void()> hold_closed();

} // namespace nkos::input::hid

namespace nkos::system::atx {

// One press of a line as the page's buttons press it (one press at a time, the line let go at shutdown): "" or the
// error. Blocking for the press's length.
std::string pulse_line(const std::string &device, std::chrono::milliseconds duration);

} // namespace nkos::system::atx
