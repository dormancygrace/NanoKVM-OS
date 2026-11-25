#pragma once

// The board and its ATX lines, as the Go server's config/hardware.go. NanoKVM OS uses the GPIO character device
// (ABI v2) with controller-local offsets, written "gpio-v2:<controller label>:<offset>". An ATX pin assignment is
// never guessed: an unknown board has none.

#include <string>

namespace nkos::system {

struct Hardware {
	std::string board;  // alpha, beta, pcie, lite, or "" when unknown
	std::string version;  // Alpha, Beta, PCIE (the native application's revision)
	std::string gpio_reset, gpio_power, gpio_power_led, gpio_hdd_led;
};

// Read once (/etc/kvm/hw, /etc/kvm/board-profile).
const Hardware &hardware();

} // namespace nkos::system
