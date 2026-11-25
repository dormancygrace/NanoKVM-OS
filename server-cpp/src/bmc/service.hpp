#pragma once

// The server's Redfish and IPMI services, as the Go server's service/bmc and router/bmc.go: one settings page
// (GET /api/bmc/status, PUT /api/bmc/config), one BMC account for both protocols. Redfish (/redfish, /redfish/...)
// checks that account itself and takes no web login; with Redfish off it answers 404. IPMI over LAN listens on its
// UDP port while it is on; a port taken at start (the previous release's IPMI daemon during an update) is tried
// again every 5 s.

#include "bmc/config.hpp"
#include "bmc/ipmi.hpp"
#include "bmc/redfish.hpp"
#include "http/router.hpp"
#include "storage/storage.hpp"

#include <array>
#include <atomic>
#include <optional>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

namespace nkos::bmc {

// --- What other families provide (set before add_routes; the defaults do nothing) -------------------------------------

// The computer sensors agent's last report (hostsensors.Status of vm.HostSensorsStatus) and whether the feature is on.
struct HostSensor {
	std::string hardware, type, name, kind;
	double value = 0;
};
struct HostSensorsStatus {
	bool on = false;          // the feature is on
	bool has_report = false;  // a report arrived
	bool stale = false;       // the agent has been silent for longer than three intervals
	std::vector<HostSensor> sensors;
};
extern std::function<HostSensorsStatus()> host_sensors_status;
// Sets host_sensors_status from the USB family's sensors store. Before add_routes.
void link_host_sensors();

// The security notifications (service/notify): a web-style lockout, IPMI's attempt limit, a login.
extern std::function<void(const std::string &address, int failures, int minutes)> notify_login_locked_out;
extern std::function<void(const std::string &address, int attempts, int minutes)> notify_login_attempts_limited;
extern std::function<void(const std::string &user, const std::string &address)> notify_login_succeeded;

// --- The parts, for the routes and for checks ---------------------------------------------------------------------------

// The device identity in the resources: /etc/machine-id as a UUID and as 16 bytes, the release's version, and Get
// Device ID's firmware revision (major, minor in BCD).
std::string device_uuid(const std::string &machine_id);
std::array<std::uint8_t, 16> device_guid(const std::string &machine_id);
std::string firmware_version(const std::string &release);
std::pair<std::uint8_t, std::uint8_t> version_bytes(const std::string &version);

// What Redfish's virtual media needs of the storage family: the one virtual drive the web page, MCP and network boot
// share (Medium, InsertURL, InsertImageFile, EjectMedium; errors as their texts).
struct Drive {
	std::function<std::optional<storage::Medium>(std::string &error)> medium;
	std::function<std::string(const std::string &url, const std::string &username, const std::string &password,
	                          std::chrono::milliseconds timeout)> insert_url;
	std::function<std::string(const std::string &name, bool cdrom, bool &drive_in_use)> insert_image_file;
	std::function<std::string()> eject_medium;
};
Drive storage_drive();
// The drive as Redfish's VirtualMedia.
std::shared_ptr<redfish::Media> storage_media(Drive drive);
// The sensors agent's readings as Redfish's sensors.
std::shared_ptr<redfish::Sensors> agent_sensors(std::function<HostSensorsStatus()> status);

// A change from the settings page; an empty password keeps the stored one, absent pointers keep their settings.
struct Settings {
	bool redfish = false, ipmi = false;
	std::string username, password;
	long long ipmi_port = 0, hold_seconds = 0;
	std::optional<bool> power_led_connected, virtual_media;
};

class Service {
public:
	Service(std::string config_path, Atx &atx, std::shared_ptr<redfish::Media> media, std::shared_ptr<redfish::Sensors> sensors,
	        redfish::Info info, std::array<std::uint8_t, 16> guid);
	~Service();

	// The JSON data of GET /api/bmc/status, or the error.
	bool status(std::string &json, std::string &error);
	// Stores the settings and switches Redfish and IPMI at once; empty on success.
	std::string configure(const Settings &settings);
	// The Redfish handler of the current settings; nullptr while Redfish is off.
	std::shared_ptr<redfish::Handler> redfish();
	// Starts IPMI that is on, trying again while its port is taken (a thread of its own).
	void keep_ipmi();
	// For checks: hooks on the handlers and servers made from the settings.
	std::function<void(redfish::Handler &)> on_redfish;
	std::function<void(ipmi::Server &)> on_ipmi;

private:
	struct Run;
	bool running() const;
	// The UDP socket for config when IPMI is on and the running listener does not have its port (-1: none needed).
	int ipmi_socket(const Config &config, std::string &error);
	void apply_ipmi(const Config &config, int fd);
	void serve_ipmi(const Config &config, int fd);
	void stop_ipmi();
	void use_redfish(const Config &config);

	std::mutex mu_;
	std::string config_path_;
	Atx &atx_;
	std::shared_ptr<redfish::Media> media_;
	std::shared_ptr<redfish::Sensors> sensors_;
	redfish::Info info_;
	std::array<std::uint8_t, 16> guid_;
	std::unique_ptr<Run> ipmi_;
	std::mutex redfish_mu_;
	std::shared_ptr<redfish::Handler> redfish_;
	std::thread keeper_;
	std::atomic<bool> closing_{false};
};

// Any /redfish and /redfish/* (a middleware: the router has no wildcard routes) and the /api/bmc routes; starts IPMI.
// Call after the static-file middlewares, with the other families' routes.
void add_routes(http::Router &router);

} // namespace nkos::bmc
