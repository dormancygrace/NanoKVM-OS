#pragma once

// Network standby, as the Go server's standby package: the parts both the server and the RAM helper (nkos-standby)
// use. The configuration handed to the helper (config.json, written as Go's json.Marshal writes it), the state of
// the standby page and its HTTP answers, mount and block statistics parsing, the checks of the handoff (init table,
// Wi-Fi configuration, routes, RAM budget) and the copy of executables with their libraries into the RAM root.
// Nothing here needs the server's event loop or libraries.

#include <chrono>
#include <cstdint>
#include <functional>
#include <memory>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace nkos::standby {

inline constexpr const char *directory = "/run/nkos-standby";
inline constexpr const char *root = "/run/nkos-standby/root";
inline constexpr const char *marker = "/run/nkos-standby/armed";
inline constexpr const char *helper = "/usr/sbin/nkos-standby";

// A Go error: empty text is nil.
using Error = std::string;

struct Config {
	std::string token, address, client_address;
	long long prefix = 0;
	std::string gateway, interface, mac;
	bool dhcp = false, wifi = false;
	std::string hostname;
	long long port = 0;
	std::optional<std::string> certificate, private_key;  // nullopt: nil (null in JSON)
	bool magic_wake = false;
};

// json.Marshal(Config): the untagged field names, []byte as base64, nil as null.
std::string config_json(const Config &c);
// LoadConfig: json.Unmarshal (keys matched without regard to case) and the checks of the token, address and port.
// JSON errors read "json: <reason>" (their exact text is Go's encoding/json's, not reproduced).
Error parse_config(std::string_view data, Config &c);
Error load_config(const std::string &path, Config &c);

struct Status {
	std::string state;
	bool safe = false;
	std::string error;
	std::string mac;
	bool magic_wake = false;
};
// json.Encoder's line: {"state":..,"safe":..,"error":.. (omitted when empty),"mac":..,"magicWake":..}\n
std::string status_json(const Status &s);

// The restart serialization point; wake requests during the transition are discarded.
class State {
public:
	State() = default;
	explicit State(const Config &c);
	Status get();
	void fail(const std::string &error);
	void restart_failed(const std::string &error);
	void ready();
	// first, or the error text.
	std::pair<bool, Error> restart(bool magic);
	// received: nanoseconds since the epoch of the packet (0: unknown); against the monotonic readiness time.
	std::pair<bool, Error> magic_restart(std::int64_t received_ns);

private:
	std::pair<bool, Error> restart_locked(bool magic);
	std::mutex mu_;
	Status status_;
	std::int64_t ready_at_ns_ = 0;
};

// The request as Handler reads it.
struct HttpIn {
	std::string method, path;
	std::string authorization, origin, sec_fetch_site;  // first values, "" when absent
	std::string host;
	bool tls = false;
};
struct Reply {
	int status = 200;
	std::vector<std::pair<std::string, std::string>> headers;
	std::string body;
	bool restart_first = false;  // the caller restarts after 250 ms and reports a failure with State::restart_failed
};
// Handler(c, state, restart): every answer of the standby page's mux.
Reply handle(const Config &c, State &state, const HttpIn &in);
// http.Error's answer (Content-Type, nosniff, the text and a newline) on top of headers already set.
void http_error(Reply &r, std::string_view text, int status);

// The page and its script, byte for byte as the Go server embeds them.
std::string_view page_html();
std::string_view page_js();

// --- mounts and block devices ----------------------------------------------------------------------------------------

struct Mount {
	std::string point, source, type, options, super_options;
};
bool has_option(std::string_view options, std::string_view option);
std::string unescape_mount(std::string_view s);
// ParseMounts of /proc/self/mountinfo; "invalid mountinfo".
Error parse_mounts(std::string_view data, std::vector<Mount> &out);
Error read_mounts(std::vector<Mount> &out, const std::string &path = "/proc/self/mountinfo");
bool memory_fs(std::string_view type);
bool block_fs(std::string_view type);
// syncfs on every block filesystem's mount point.
Error sync_filesystems(const std::vector<Mount> &mounts);

struct Counters {
	std::uint64_t writes = 0, written = 0, discards = 0, discarded = 0, flushes = 0, in_flight = 0;
	bool operator==(const Counters &) const = default;
};
Error parse_counters(std::string_view data, Counters &c);
// processReleased of /proc/PID/stat.
Error process_released(std::string_view stat, bool &released);

// --- the handoff's checks ---------------------------------------------------------------------------------------------

// handoffInittab: the init table with the RAM helper's shutdown, restart and acknowledge actions.
Error handoff_inittab(std::string_view table, std::string &out);
// ramWPA: the managed station networks only, for the RAM root.
Error ram_wpa(std::string_view data, std::string &out);
// /proc/net/route: the interface of the client's return route; the management routes are simple ones; the default
// gateway of iface.
Error client_route(std::string_view table, const std::string &address, std::string &iface);
Error simple_routes(std::string_view table, const Config &c);
Error default_gateway(std::string_view table, const std::string &iface, std::string &gateway);
Error check_ram_budget(std::int64_t payload, std::int64_t available);
// skipsStop of OpenRC's deptree: the service is marked failed only because it is kept at shutdown.
Error skips_stop(const std::string &deptree, const std::string &service, bool &skipped);
// watchdogsReleased: every watchdog of sysfs has left userspace ownership.
Error watchdogs_released(const std::string &sysfs);
// MagicPacket: six 0xff then 16 copies of the six-byte MAC anywhere in the packet.
bool magic_packet(std::string_view packet, std::string_view mac);

// watchFailures: an inotify watch of OpenRC's failed-service directory (made 0755 when missing). result() reads its
// events: a service that failed or refused shutdown, unless it is only kept at shutdown (skipsStop of deptree).
class FailureWatch {
public:
	~FailureWatch();
	static Error open(const std::string &directory, const std::string &deptree, std::unique_ptr<FailureWatch> &out);
	Error result();

private:
	int fd_ = -1;
	Error existing_;
	std::string deptree_;
};

// RecoveryError: storage was released and disks protected before the failure, so an automatic restart is safe.
struct Failure {
	Error text;
	bool recovery = false;  // a RecoveryError
};
std::string recovery_text(const Error &cause);
// recoverFatal: for a RecoveryError, wait two minutes and restart; other errors come back as they are.
Failure recover_fatal(const Failure &err, const std::function<void(std::chrono::seconds)> &wait,
                      const std::function<Error()> &reboot);

// --- RAM root staging ----------------------------------------------------------------------------------------------

// copyFile: a regular file of at most 32 MiB, to a new file (O_EXCL) with mode; parents made 0700.
Error copy_file(const std::string &src, const std::string &dst, unsigned mode);
// os.MkdirAll.
Error mkdir_all(const std::string &path, unsigned mode);
// staticELF: no program interpreter.
Error static_elf(const std::string &name);
// stageELF: the executable at name in source_root (normally "/"), its interpreter and the libraries it imports,
// copied below target_root when that is not empty; files collects every path (as on the device).
Error stage_elf(const std::string &name, const std::string &source_root, const std::string &target_root,
                std::map<std::string, bool> &files);

// --- Go texts ----------------------------------------------------------------------------------------------------------

// strconv.ParseUint(s, base, bits) for base 2..36; err gets Go's error text.
std::optional<std::uint64_t> parse_uint(std::string_view s, int base, int bits, Error &err);
// net.ParseIP(s).To4() as four bytes, or nullopt.
std::optional<std::string> parse_ip4(std::string_view s);

} // namespace nkos::standby
