#pragma once

// The headless-Linux console helper, as the Go server's service/vm/serial_console.go: GET /api/vm/serial-console
// (the gadget's identity, the setup script and the helper disk), POST /api/vm/serial-console/test (an Enter on
// ttyGS0 and what the host answers: login, password, shell, text, silent, busy, unavailable, error), and the public
// GET /nanokvm-serial-console.sh the host fetches with curl or wget over the LAN or the USB network.

#include "http/router.hpp"

#include <string>

namespace nkos::system::serial_console {

void add_routes(http::Router &router);

// The helper disk image (the one built with the computer sensors' agents, else the shipped serial console disk),
// and whether the virtual drive's second LUN holds it (and whether that is an older, replaced image).
std::string helper_disk_image();
void helper_disk_lun(bool &attached, bool &stale);

} // namespace nkos::system::serial_console
