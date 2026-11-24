#pragma once

// ZeroTier, as the Go server's service/extensions/zerotier: the nanokvm-zerotier package (ZeroTier's agent without
// the network controller, under ZeroTier's own names and home) installed and removed on request, the zerotier-one
// service run through OpenRC, networks joined and left by their 16-digit ID, status from "zerotier-cli -j info" and
// "zerotier-cli -j listnetworks".

#include "extensions/common.hpp"

#include <string>

namespace nkos::extensions::zerotier {

Reply install();
Reply uninstall(const std::string &body);
Reply get_status();
Reply start();
Reply stop();
Reply restart();
// The body is {"network": "<16 hexadecimal digits>"}.
Reply join(const std::string &body);
Reply leave(const std::string &body);

} // namespace nkos::extensions::zerotier
