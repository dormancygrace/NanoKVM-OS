#pragma once

// System > Diagnostics, as the Go server's service/vm/diagnostics.go and dev_archive.go (administrators):
// GET /api/vm/diagnostics (an allow-listed status snapshot: versions, services, video, USB, apk, firewall hook
// chains; collected at most every 20 s, one collection at a time, within 8 s), GET /api/vm/diagnostics/report (the
// narrower issue-ready report as a JSON attachment), GET /api/vm/diagnostics/archive (the developer archive: the
// collector of nanokvm-base gathers files into a temporary directory, each is cleaned and masked, then packed as
// tar.gz).

#include "http/router.hpp"
#include "video/edid_profiles.hpp"

#include <chrono>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace nkos::system::diagnostics {

// The Go service's path variables.
struct Paths {
	std::string init_dir = "/etc/init.d";
	std::string modules_dir = "/lib/modules";
	std::string boot_dir = "/boot";
	std::string gadget_dir = "/sys/kernel/config/usb_gadget/g0";
	std::string apk_db = "/lib/apk/db/installed";
	std::string openvpn_run = "/run/nkos-openvpn";
	std::string proc_dir = "/proc";
	std::string archive_script = "/usr/libexec/nanokvm/dev-archive";
	std::string archive_etc = "/etc/kvm";
};
Paths &paths();

struct Item {
	std::string state, detail;
	bool optional = false;
};
struct Service {
	std::string name, desired;
	Item item;
};
struct Package {
	std::string name, version, state;
};
struct HookChain {
	std::string label, hook, policy, feature;
};

struct Snapshot {
	std::int64_t collected_at = 0;  // Unix milliseconds
	std::string application, image, alpine, kernel;
	Item modules, boot;
	std::vector<Package> packages;
	std::vector<Service> services;
	struct {
		bool enabled = false, signal = false;
		std::string profile, input, output, measured_fps, edid_state, state;
	} video;
	std::vector<std::string> usb_selected;
	Item usb_binding;
	std::string apk_state, apk_error_category;
	std::string firewall_state, firewall_detail;
	std::vector<HookChain> hook_chains;
};

// What the loop thread knows of the video: HDMI capture switched on, a signal, the board and receiver, and a monitor
// power cycle outstanding.
struct VideoInputs {
	bool enabled = false, signal = false;
	video::edid::Hardware hardware;
	bool power_cycle_pending = false;
};

// collectDiagnostics: blocking (files, rc-service and nft with a 2 s bound each, 8 s in all).
Snapshot collect(const VideoInputs &video);

// The JSON of the snapshot (GET /api/vm/diagnostics's data), and of the report (diagnosticsReport): compact as c.JSON
// writes it, or indented as json.MarshalIndent(v, "", "  ").
std::string snapshot_json(const Snapshot &s);
std::string report_json(const Snapshot &s, bool indent);

// External commands as runDiagnosticCommand: argv[0] looked up in PATH unless it has a slash, stdin /dev/null,
// stdout and stderr together up to limit bytes, a process group of its own killed at the deadline. error is the
// Go error's text ("" for nil); deadline_passed says whether the deadline had passed when the command returned.
struct CommandResult {
	std::string output, error;
	int exit_code = -1;  // the exit status of a command that exited, -1 otherwise
	bool deadline_passed = false;
};
CommandResult run_command(const std::vector<std::string> &argv, std::size_t limit,
                          std::chrono::steady_clock::time_point deadline);
// exec.LookPath.
bool look_path(const std::string &name);

// ---- developer archive (dev_archive.cpp) ----

// packDevArchive: the regular files under dir, cleaned and masked, then report (indented JSON, when given) as
// diagnostics.json and README.txt, as tar.gz; nullopt with error set (the archive's limits, unreadable files).
// now: Unix seconds and nanoseconds of the time written into the entries and README.
std::optional<std::string> pack(const std::string &dir, const std::optional<std::string> &report, std::string note,
                                std::int64_t now_sec, std::int32_t now_nsec, std::string &error);
// redactSettings: a JSON or YAML settings file with the values of credential-like keys hidden; nullopt when it
// does not parse.
std::optional<std::string> redact_settings(std::string_view data);

// The routes above.
void add_routes(http::Router &router);

} // namespace nkos::system::diagnostics
