#include "system/hardware.hpp"

#include "common/log.hpp"

#include <fstream>
#include <sstream>

namespace nkos::system {

namespace {

std::string read_all(const char *path, bool &ok)
{
	std::ifstream in(path);
	ok = static_cast<bool>(in);
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

std::string trim(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

std::string path(const char *label, int offset) { return std::string("gpio-v2:") + label + ":" + std::to_string(offset); }

Hardware load()
{
	bool ok = false;
	std::string declared = read_all("/etc/kvm/hw", ok);
	if (!ok) log::error("ATX requires board revision: /etc/kvm/hw is missing");
	std::string hw = declared;
	std::erase(hw, '\n');
	std::string version = hw == "beta" ? "Beta" : hw == "pcie" ? "PCIE" : "Alpha";
	bool profile_ok = false;
	std::string profile = read_all("/etc/kvm/board-profile", profile_ok);
	if (profile_ok) declared = profile;
	Hardware h;
	h.version = version;
	h.board = trim(declared);
	if (h.board == "lite") {
		// Base/Lite has no ATX of its own; with the KVM-B board it is wired as the Full (Beta) one.
		h.version = "Beta";
		h.gpio_reset = path("3020000.gpio", 25);
	} else if (h.board == "alpha") {
		h.version = "Alpha";
		h.gpio_reset = path("3020000.gpio", 27);
		h.gpio_hdd_led = path("3020000.gpio", 25);
	} else if (h.board == "beta") {
		h.version = "Beta";
		h.gpio_reset = path("3020000.gpio", 25);
	} else if (h.board == "pcie") {
		h.version = "PCIE";
		h.gpio_reset = path("3020000.gpio", 25);
		h.gpio_hdd_led = path("5021000.gpio", 3);
	} else {
		log::error("ATX disabled: no recognized board revision");
		h.board.clear();
		return h;
	}
	h.gpio_power = path("3020000.gpio", 23);
	h.gpio_power_led = path("3020000.gpio", 24);
	return h;
}

} // namespace

const Hardware &hardware()
{
	static const Hardware h = load();
	return h;
}

} // namespace nkos::system
