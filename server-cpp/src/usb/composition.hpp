#pragma once

// The USB routes of the Go server's router/vm.go (administrators): the gadget's composition (GET, POST and PUT
// /api/vm/device/virtual), the port's role (PUT /api/vm/usb/role), the devices attached in the host role
// (GET /api/vm/usb/devices), USB internet sharing and the computer's access over USB (GET/PUT /api/vm/usb-internet,
// PUT /api/vm/usb-host-access). A change of the gadget runs under storage::gadget_mutex() (Go's HID lock) with the
// HID devices closed and USB audio stopped, restarts S03usbdev once, verifies the result in configfs and restores
// the previous files and running state when anything fails.

#include "http/router.hpp"

#include <string>

namespace nkos::usb {

// The JSON of GET /api/vm/device/virtual (also the live status's "usb"), as the flags and the gadget are now.
std::string state_json();

void add_routes(http::Router &router);

} // namespace nkos::usb
