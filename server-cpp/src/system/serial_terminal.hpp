#pragma once

// The serial terminal (GET /api/vm/terminal?port=...): the page's bytes go to a tty device and back, without a pty
// or a program. The device is opened per page with exclusive access (flock against another server, TIOCEXCL
// against other programs) and raw line settings; one holder per device within the server (claim_serial_port), as
// the console probe also opens it. Loop thread only.

#include "http/router.hpp"
#include "http/websocket.hpp"

#include <functional>
#include <memory>
#include <optional>
#include <string>

namespace nkos::system::serial_terminal {

struct Settings {
	std::string device;
	int baud = 9600;
	std::string parity = "none";  // none, even, odd
	std::string flow = "none";    // none, hard, soft
	int data_bits = 8;
	int stop_bits = 1;
};

// The terminal's serial parameters from the query, or the error text for a 400. The gadget port (ttyGS) is a USB
// byte stream whose line coding is nominal: it always gets 9600 8N1 without flow control, so inherited termios
// cannot swallow stream bytes.
std::optional<Settings> settings_from_query(const http::Request &request, std::string &error);

std::shared_ptr<http::WsHandler> make_page(Settings settings);

// Records the caller as the device's holder; nullopt while another holder (a page, the console probe) has it. The
// returned function releases it, once the device is closed.
std::optional<std::function<void()>> claim_serial_port(const std::string &device);

} // namespace nkos::system::serial_terminal
