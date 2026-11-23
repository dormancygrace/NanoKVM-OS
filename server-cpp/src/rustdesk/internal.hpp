#pragma once

// The parts of the RustDesk family shared by its files (and the differential check): the settings file, the service
// operations and their answers in Go's JSON.

#include "network/util.hpp"
#include "rustdesk/rustdesk.hpp"

#include <chrono>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

namespace nkos::rustdesk {

struct Paths {
	std::string config_dir = "/etc/nanokvm-rustdesk";
	std::string config_file = "/etc/nanokvm-rustdesk/config.json";
	std::string runtime_dir = "/run/nanokvm-rustdesk";
	std::string password_file = "/run/nanokvm-rustdesk/temporary-password";
	std::string upstream_file = "/usr/share/nanokvm-rustdesk/upstream.json";
	std::string source_file = "/usr/share/nanokvm-rustdesk/source.json";
	std::string started_file = "/run/openrc/started/nanokvm-rustdesk";
	std::string runlevel_link = "/etc/runlevels/default/nanokvm-rustdesk";
	std::string world = "/etc/apk/world";
	std::string binary = "/usr/bin/nanokvm-rustdesk";
};
extern Paths paths;

// rustdesk.Config: the daemon's settings, as config.json and the API carry them.
struct Config {
	std::optional<bool> audio_enabled;   // audio_enabled, omitted when unset
	std::optional<bool> local_pointer;   // local_pointer, omitted when unset
	std::string listen;                  // listen_address, omitted when empty
	bool webrtc = false;                 // webrtc_enabled, omitted when false
	bool enabled = false;                // service_enabled
	bool official = false;               // use_official_id_server
	std::string rendezvous, relay, key;  // rendezvous_server, relay_server, server_key
	std::string password;                // password, omitted when empty
	std::string password_mode;
	std::string codec;
	long long max_clients = 0;
};

Config default_config();
// json.Unmarshal of a JSON value into c (fields matched exactly, else without regard to case; a value of another type
// is an error, the rest is still decoded). The error text as encoding/json gives it, or empty.
std::string decode_config(const network::JValue &value, Config &c);
// readConfig: the defaults, the file over them, the permanent-password migration, codec "auto". Error text or empty.
std::string read_config(Config &c);
// The fields as json.Marshal writes them (indent "": one line; else MarshalIndent with that indent).
std::string config_json(const Config &c, const std::string &indent);
// writeConfig: MarshalIndent with two spaces and a newline, through a 0600 temporary file, synced and renamed.
std::string write_config(const Config &c);

// serverAddress and validateConfig: error text or empty; c is normalised.
std::string server_address(const std::string &value, const std::string &port, std::string &out);
std::string validate_config(Config &c);

// encoding/json's compaction of a json.RawMessage it writes (HTML characters and U+2028/U+2029 escaped).
std::string compact_json(std::string_view text);

// A command of the service: name and arguments as Go's Service.run gets them; the timeout of its context.
struct CommandResult {
	std::string output;
	std::string error;  // empty: success
};
using Runner = std::function<CommandResult(const std::string &name, const std::vector<std::string> &args,
                                           std::chrono::milliseconds timeout)>;
// runCommand: rc-service start/restart without the server's OOM protection, the combined output; the error
// "<name> failed: <output>".
CommandResult run_command(const std::string &name, const std::vector<std::string> &args, std::chrono::milliseconds timeout);

struct Service {
	std::mutex mu;
	Runner run = run_command;
	std::shared_ptr<Bridge> bridge;
	UsbHooks usb;

	// Status: the data JSON, or the error text in error.
	std::string status(std::string &error);
	std::string configure(Config candidate);
	std::string configure_locked(Config candidate);
	std::string action(const std::string &action);
	// AcceptWebRTC's work (under mu).
	void accept_webrtc();
	// PrepareUSB: what the daemon asks before it accepts connections.
	std::string prepare_usb();

	bool daemon_running();
	bool wants_webrtc();
	std::pair<std::string, std::string> source();  // version, URL
};
Service &service();

// apkpkg.selected over the text of a world file.
bool world_selects(const std::string &world, const std::string &name);

} // namespace nkos::rustdesk
