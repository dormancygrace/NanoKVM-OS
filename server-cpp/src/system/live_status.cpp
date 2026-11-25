#include "system/live_status.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "input/hid.hpp"
#include "system/atx.hpp"
#include "system/failsafe.hpp"
#include "system/gpio.hpp"
#include "usb/composition.hpp"
#include "video/hdmi.hpp"

#include <format>
#include <sys/stat.h>

namespace nkos::system {

namespace {

// The optional USB speaker function exists (subscribing never changes the composition).
bool audio_enabled()
{
	struct stat st{};
	return ::lstat("/sys/kernel/config/usb_gadget/g0/configs/c.1/uac1.audio0", &st) == 0 && S_ISLNK(st.st_mode);
}

} // namespace

// The states the remote desktop page watches, in one poll instead of five; each field has the payload of its own
// endpoint. The USB composition is included for administrators only, like GET /api/vm/device/virtual.
void add_live_status_route(http::Router &router)
{
	router.add("GET", "/api/vm/live-status", auth::guarded([](http::Request &req, http::Response &res) {
		bool admin = req.auth && req.auth->role == "admin";
		bool atx_on = atx::enabled();
		std::string gpio = "null";
		// With ATX off the LED monitor does not run: no gpio.
		if (atx_on)
			if (auto leds = gpio::current_leds()) gpio = std::format(R"({{"pwr":{},"hdd":{}}})", leds->power, leds->hdd);
		auto f = input::hid::input_functions();
		std::string input = std::format(R"({{"absolute":{},"available":{},"keyboard":{},"relative":{}}})", f.absolute,
		                                f.keyboard || f.relative || f.absolute, f.keyboard, f.relative);
		std::string audio = std::format(R"({{"audio":{},"bitrate":192000,"complexity":3}})", audio_enabled());
		std::string data = std::format(R"({{"recovery":{},"hdmi":{},"gpio":{},"input":{},"audio":{},"usb":{},"atx":{}}})",
		                               failsafe::status_json(), video::hdmi::state_json(), gpio, input, audio,
		                               admin ? usb::state_json() : "null", atx_on);
		api::send(res, 0, "success", data);
	}));
}

} // namespace nkos::system
