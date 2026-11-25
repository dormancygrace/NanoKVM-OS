#pragma once

// Settings > Services, as the Go server's service/extensions/netsvc: LLDP (lldpd), SNMP (net-snmp) and UPS (Network
// UPS Tools). Each is an Alpine package of the system, an OpenRC service that is off until it is switched on, and a
// small configuration written from the settings (marked as ours; a configuration somebody wrote by hand, as apk
// audit tells, is used as it is and only replaced on request).

#include "extensions/common.hpp"
#include "network/util.hpp"

#include <map>
#include <string>
#include <vector>

namespace nkos::extensions::netsvc {

// The routes of one service: GET status (?light=1 for the dashboard), PUT enabled, PUT settings, POST reset.
Reply status(const std::string &service, bool light);
Reply set_enabled(const std::string &service, const network::RequestData &request);
Reply settings(const std::string &service, const network::RequestData &request);
Reply reset(const std::string &service);

// The parsers, for checks.
struct Neighbor {
	std::string interface, protocol, name, description, port, port_description, address, age;
};
std::vector<Neighbor> parse_lldp_neighbors(const std::string &data);
std::map<std::string, std::string> parse_ups_values(const std::string &out);
// source: the network as snmpd takes it ("" for anyone); false when it is not IPv4.
bool snmp_source(const std::string &text, std::string &out);

} // namespace nkos::extensions::netsvc
