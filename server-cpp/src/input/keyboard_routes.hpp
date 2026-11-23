#pragma once

// The HTTP input routes, as the Go server's service/hid: paste text (US, German, French or Spanish layout; normal or
// slow), key sequences (Ctrl+Alt+Del, a SysRq key, REISUB), the keyboard LEDs, the shortcuts and the leader key,
// and resetting the USB PHY. Routes that type ask for the input lease of the tab with input control
// (X-NanoKVM-Input-Lease) and check it again before every key press.

#include "http/router.hpp"

namespace nkos::input {

void add_keyboard_routes(http::Router &router);

} // namespace nkos::input
