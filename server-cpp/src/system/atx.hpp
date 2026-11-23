#pragma once

// The ATX switch (System > Device), as the Go server's service/vm/atx.go: whether the board's lines for the host's
// power and reset buttons and its power and HDD LEDs are used (/etc/kvm/atx: on/off; absent: the board's default,
// written at the first start: on for PCIe, off for Cube and Lite, which need the KVM-B board). The LED monitor runs
// while ATX is on.

#include "http/router.hpp"

#include <chrono>
#include <string>

namespace nkos::system::atx {

bool available();
// One press of a line (held for duration); "" or the error. The page's presses, the sensors watchdog and the BMC share
// it, so presses never overlap.
std::string pulse_line(const std::string &device, std::chrono::milliseconds duration);
bool enabled();
// Writes the board's default at the first start; starts the LED monitor when ATX is on.
void start();
void stop();

// GET /api/vm/atx, POST /api/vm/atx (administrators).
void add_routes(http::Router &router);

} // namespace nkos::system::atx
