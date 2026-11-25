// The BMC's view of the computer sensors (vm.HostSensorsStatus) from the USB family's store.
#include "bmc/service.hpp"
#include "usb/host_sensors.hpp"

#include <glaze/glaze.hpp>

namespace nkos::bmc {

namespace {

struct SensorJ {
	std::string hw, type, name, kind;
	double value = 0;
};
struct ReportJ {
	std::vector<SensorJ> sensors;
};
struct StatusJ {
	std::optional<ReportJ> report;
	bool stale = false;
};

} // namespace

void link_host_sensors()
{
	host_sensors_status = [] {
		HostSensorsStatus out;
		std::string json = usb::host_sensors_status(out.on);
		if (!out.on) return out;
		StatusJ s;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(s, json)) return out;
		out.stale = s.stale;
		if (s.report) {
			out.has_report = true;
			for (auto &x : s.report->sensors) out.sensors.push_back(HostSensor{x.hw, x.type, x.name, x.kind, x.value});
		}
		return out;
	};
}

} // namespace nkos::bmc
