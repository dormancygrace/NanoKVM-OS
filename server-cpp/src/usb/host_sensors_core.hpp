#pragma once

// What the managed computer reports about itself, as the Go server's hostsensors package: temperatures, voltages,
// fan speeds and the like, read there by an agent (nanokvm-sensors) and sent over the USB serial port or the USB
// network. The computer is not trusted: a report is bounded and checked before anything of it is kept, and only the
// last one is kept. Also the page's choice of readings (View). Any thread (the store locks itself).

#include <cstdint>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::usb::sensors {

inline constexpr int version = 1;
inline constexpr std::size_t max_report_bytes = 256 << 10;
inline constexpr std::size_t max_sensors = 1024;
inline constexpr std::size_t max_view_entries = 4096;

struct Sensor {
	std::string hw, type, name, kind;
	double value = 0;
};

struct Report {
	long long v = 0;
	std::string host, os, agent;
	long long interval = 0;
	bool limited = false;
	std::vector<Sensor> sensors;
};

// The unit of a kind; nullopt for a kind this server does not know.
std::optional<std::string> unit(const std::string &kind);

// hostsensors.Parse: the checked report, or Go's error text.
bool parse(std::string_view data, Report &out, std::string &error);
std::string report_json(const Report &r);

// Where a network report came from: the sender's address and the life of the USB link it came over.
struct From {
	std::string peer, link;
};

// Times are nanoseconds of a monotonic clock; 0 is "none".
using Nanos = std::int64_t;

class Store {
public:
	// Parses and keeps a report; the error is remembered for the status. Empty on success.
	std::string accept(std::string_view data, const std::string &transport, const From &from, Nanos now);
	void clear();
	// When the last report came and from where; at is 0 when there is none.
	void last(Nanos &at, From &from) const;
	// The status JSON (hostsensors.Status) as of now.
	std::string status_json(Nanos now) const;

private:
	mutable std::mutex mutex_;
	std::optional<Report> report_;
	std::string transport_;
	Nanos at_ = 0;
	From from_;
	std::uint64_t rejected_ = 0;
	std::string last_reject_;
};

// SplitLines: data added to the pending line; complete lines (trimmed, non-empty) go to accept, a line dropped for
// its length as nullopt (a rejected report).
struct LineSplitter {
	std::string line;
	bool overflow = false;
	void feed(std::string_view data, const std::function<void(std::optional<std::string_view>)> &accept);
};

// LoginConsole: what came on the serial port is a login console's prompt, not an agent.
bool login_console(std::string_view received);

// FromNetwork: peer (an address without port) is on one of the networks and is not the NanoKVM's own address there.
struct Network {
	std::uint32_t address = 0, mask = 0;  // host order
};
bool from_network(const std::string &peer, const std::vector<Network> &networks);

// The page's choice of readings: exceptions by key.
struct View {
	std::map<std::string, bool> overrides;
};
std::string key(const Sensor &s);
bool parse_view(std::string_view data, View &out, std::string &error);
std::string view_json(const View &v);
// hostSensorsView: the saved view, empty when missing or invalid (logged).
View read_view(const std::string &path);

// --- the settings page's state (service/vm/host_sensors.go) ---

// hostSensorsMode: "serial" or "network" from the file, else "off".
std::string read_mode(const std::string &path);
// hostSensorsAddonVersion: the package version of the agents (the part before '-'), "" when unknown.
std::string addon_version(const std::string &source_json);
// proto.SerialConsoleFile of a download: {"path","available","size"} (size only when available and non-zero).
std::string agent_file_json(const std::string &url_path, const std::string &file);
// helperDiskLun: whether the helper LUN holds one of the two helper images, and whether that one is stale.
void helper_disk_lun(const std::string &lun_file, const std::string &helper_file, const std::string &console_disk,
                     bool &attached, bool &stale);

struct StateInputs {
	std::string mode, serial_port, serial_state;
	std::vector<std::string> addresses;
	std::string agent_json, linux_agent_json, agent_version, status_json, view_json;
	bool disk_attached = false, disk_stale = false;
	std::string watchdog_action;
	long long watchdog_minutes = 0;
	bool watchdog_armed = false;
	std::string watchdog_last;
};
// hostSensorsState's gin.H (keys sorted).
std::string state_json(const StateInputs &in);

} // namespace nkos::usb::sensors
