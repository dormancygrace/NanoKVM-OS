#pragma once

// The USB gadget's composition, as the Go server's service/vm virtual-device, usb_identity, usb-role and
// internal/storageidentity: which functions the computer sees (keyboard, relative and absolute mouse, network,
// virtual disk, serial console, speaker), the port's role (gadget, host, off), the identity the computer sees and
// the endpoint budget. S03usbdev applies the flags in /etc/kvm/usb at every start. Reading only, for now.

#include "http/router.hpp"

#include <string>

namespace nkos::usb {

inline constexpr const char *flags_dir = "/etc/kvm/usb";

// "normal" or "hid-only" (the gadget's bcdDevice); "normal" when the gadget is not set up.
std::string gadget_mode();
// gadget, host or off.
std::string role();
// The JSON of GET /api/vm/device/virtual.
std::string state_json();

// GET /api/vm/device/virtual (administrators).
void add_routes(http::Router &router);

} // namespace nkos::usb
