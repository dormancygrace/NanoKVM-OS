#pragma once

// Where the AI families' HID reports go: the server writes them to the USB gadget (keyboard /dev/hidg0, relative
// mouse /dev/hidg1, absolute mouse /dev/hidg2) through the input lane of the AI operation, the differential check
// records them.

#include <cstdint>
#include <memory>
#include <string>
#include <vector>

namespace nkos::input::control {
class Cancel;
}

namespace nkos::ai {

enum class HidDevice { keyboard, relative, absolute };

struct HidSink {
	virtual ~HidSink() = default;
	// Writes one report now and waits for the result; false with error when it was not written. operation: the AI
	// operation whose report it is (a cancelled one's reports are not written); nullptr for none.
	virtual bool write(HidDevice device, const std::vector<std::uint8_t> &report,
	                   const std::shared_ptr<input::control::Cancel> &operation, std::string &error) = 0;
	// Writes without waiting (PicoClaw's WriteHid0/1/2: errors are only logged).
	virtual void post(HidDevice device, std::vector<std::uint8_t> report) = 0;
};

// The gadget, through one lane of the HID writers (mcp_routes.cpp).
HidSink &gadget_hid();

} // namespace nkos::ai
