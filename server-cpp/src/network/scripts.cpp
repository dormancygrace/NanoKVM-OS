// The scripts of server-cpp/data/network-scripts that the server installs or runs, embedded at build time
// (embed_scripts.cmake): the udhcpc hooks for DNS, the gateway and the Wi-Fi source rule, and S02ipv6.

#include "network/internal.hpp"

#include "network_scripts.inc"

namespace nkos::network {

const std::string_view udhcpc_dns_hook = embedded::dns_hook;
const std::string_view udhcpc_gateway_hook = embedded::gateway_hook;
const std::string_view udhcpc_wifi_source_hook = embedded::wifi_source_hook;
const std::string_view ipv6_boot_script_text = embedded::ipv6_boot_script;

} // namespace nkos::network
