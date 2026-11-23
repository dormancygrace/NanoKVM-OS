#pragma once

// The managed computer's sensors, as the Go server's service/vm/host_sensors.go, helper_disk.go and
// host_watchdog.go: an agent on the computer (nanokvm-sensors) sends a report every few seconds over the USB serial
// port (the server then holds /dev/ttyGS0) or the USB network (POST /api/host-sensors from usb0 only), per
// /etc/kvm/host-sensors (off, serial, network). While on, the helper disk carries the agents. The host watchdog
// acts on the reports' silence: it asks the host (ARP over usb0) and then notifies, presses reset or cycles power.

#include "http/router.hpp"

#include <functional>
#include <string>

namespace nkos::usb {

// GET /api/vm/host-sensors (signed in), PUT /api/vm/host-sensors, .../view, .../watchdog (administrators), and the
// public POST /api/host-sensors, GET /nanokvm-sensors.exe and /nanokvm-sensors.sh.
void add_host_sensors_routes(http::Router &router);

// StartHostSensors: the saved mode applied now (on a thread of its own) and looked at again every 10 s (a package
// update rebuilds the helper disk without a restart); the host watchdog every 5 s. Call once the loop runs.
void start_host_sensors();

// SwitchOffStaleHostSensors: without the package chosen (apkpkg.Selected), a mode left from the removed add-on goes.
std::string switch_off_stale_host_sensors(bool selected);

// switchOffRemovedAddons (router.go), once per system (/etc/kvm/addon-settings-checked): RustDesk's and the
// computer sensors' settings left from add-ons removed before they became part of NanoKVM OS are switched off.
// rustdesk_switch_off is rustdesk.SwitchOffStale ("" or the error). Call before start_host_sensors.
void switch_off_removed_addons(const std::function<std::string(bool selected)> &rustdesk_switch_off);

// HostSensorsStatus for Redfish: the status JSON and whether the feature is on.
std::string host_sensors_status(bool &on);

} // namespace nkos::usb
