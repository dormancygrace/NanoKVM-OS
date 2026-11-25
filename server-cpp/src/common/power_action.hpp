#pragma once

// One physical host: a power action is refused while another one runs, whoever asks (Redfish, IPMI, the host
// watchdog). The Go server's bmc.actionMu.

#include <mutex>

namespace nkos {

inline std::mutex &power_action_mutex()
{
	static std::mutex mutex;
	return mutex;
}

} // namespace nkos
