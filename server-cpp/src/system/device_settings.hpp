#pragma once

// Hostname, mDNS, SSH and reboot: GET/POST /api/vm/hostname, /api/vm/mdns (+enable/disable), /api/vm/ssh
// (+enable with a new root password and a recent sign-in, +disable), POST /api/vm/system/reboot.

#include "http/router.hpp"

namespace nkos::system {

// 30 s after the start the mDNS choice and the service's runlevel agree again (ReconcileMdnsAfterBoot).
void start_device_settings();
void add_device_settings_routes(http::Router &router);

} // namespace nkos::system
