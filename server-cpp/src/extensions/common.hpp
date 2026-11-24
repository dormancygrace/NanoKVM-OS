#pragma once

// What the extensions family (the Go server's service/extensions: VPNs, network services, ACME) shares: answers as
// proto.Response writes them through gin (encoding/json, HTML escaping), external commands as os/exec runs them
// (process groups, a replaced environment, Go's error texts), the private file write of the VPN profiles, Go's
// strconv.Quote and duration texts, the random source, and the program-version cache (internal/toolcache).

#include "http/router.hpp"

#include <chrono>
#include <cstdint>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::extensions {

// --- answers -------------------------------------------------------------------------------------------------------

// A proto.Response: code, msg and the data as JSON ("" is null).
struct Reply {
	int code = 0;
	std::string msg = "success";
	std::string data;
};
inline Reply ok(std::string data = {}) { return Reply{0, "success", std::move(data)}; }
inline Reply fail(int code, std::string msg) { return Reply{code, std::move(msg), {}}; }
// The body gin writes: {"code":..,"msg":..,"data":..}.
std::string reply_json(const Reply &reply);
void send(http::Response &res, const Reply &reply);

// encoding/json strings and lists.
std::string q(std::string_view s);
std::string json_strings(const std::vector<std::string> &list);  // [] for an empty list
std::string json_bool(bool b);

// Runs fn on a thread of its own and answers the request with its reply: the routes run commands, take the
// family's locks and wait for package operations.
void answer_later(http::Request &req, http::Response &res, std::function<Reply()> fn);

// --- Go texts -------------------------------------------------------------------------------------------------------

// strconv.Quote, with Go's IsPrint table.
std::string go_quote(std::string_view s);
bool is_print(char32_t r);
// utf8.DecodeRuneInString: the rune and its width (U+FFFD and 1 for an invalid byte).
std::pair<char32_t, std::size_t> decode_rune(std::string_view s, std::size_t i);
void append_rune(std::string &out, char32_t r);
// strconv.ParseInt(s, 10, 64) and ParseUint(s, 10, 64) with the error ignored, as callers that write "n, _ :=" get
// them: 0 for a syntax error, the limit of the type when out of range.
std::int64_t parse_int_value(std::string_view s);
std::uint64_t parse_uint_value(std::string_view s);
// time.Duration.String for whole seconds ("30s", "1m0s", "1h20m0s").
std::string go_duration(std::int64_t seconds);
// time.Now().UTC().Format(time.RFC3339), replaceable by a check.
extern std::function<std::string()> now_rfc3339;
std::string rfc3339(std::int64_t unix_seconds);

// --- commands -------------------------------------------------------------------------------------------------------

struct Command {
	std::vector<std::string> argv;  // argv[0] without a slash is looked up in $PATH (exec.LookPath)
	std::optional<std::vector<std::string>> env;  // cmd.Env: replaces the environment
	std::vector<std::string> extra_env;           // appended to os.Environ() (when env is unset)
	std::string dir;
	std::optional<std::string> input;             // stdin; none: /dev/null
	std::optional<std::chrono::milliseconds> timeout;
	bool group = false;       // Setpgid, and the timeout kills the whole group (cmd.Cancel)
	bool kill_group_after = false;  // runGroup: the group is killed once the command ended
	std::optional<std::chrono::milliseconds> wait_delay;
	enum class Capture { output, combined, none } capture = Capture::output;
	bool keep_stderr = false;  // with Capture::output: stderr kept in Result::err_out (cmd.Stderr set to a buffer)
	// Combined output, given as it arrives (instead of keeping it in out).
	std::function<void(std::string_view)> sink;
};
struct Result {
	bool started = false;
	std::string out;           // stdout, or stdout and stderr when combined
	std::string error;         // err.Error(); "" on success
	bool exited = false;       // an *exec.ExitError (the program ran and exited non-zero or by a signal)
	int exit_code = -1;        // ExitCode(): -1 for a signal
	bool deadline = false;     // the context's deadline passed (ctx.Err() != nil)
	std::string err_out;       // stderr, with Capture::output and keep_stderr
	bool ok() const { return error.empty(); }
};
Result run(const Command &cmd);
// cmd.Start of a long-lived command: stdin on /dev/null, stdout and stderr each a pipe (read end returned) or
// /dev/null. pid < 0 with error (Go's text) when it could not start.
struct Child {
	int pid = -1;
	int out = -1, err = -1;
	std::string error;
};
Child spawn(const std::vector<std::string> &argv, bool group, bool pipe_out, bool pipe_err);
// The first https:// URL of text, as https://[^[:space:]]+ finds it; "" when there is none.
std::string find_url(std::string_view text);

// exec.LookPath.
std::optional<std::string> look_path(const std::string &name);
// exec: "name": executable file not found in $PATH
std::string not_found(const std::string &name);

// --- files ----------------------------------------------------------------------------------------------------------

// writePrivate / atomicWrite of the VPN services: the directory (0700), a ".pending-" temporary file there (0600),
// synced, renamed over path, the directory synced. "" or Go's error text.
std::string write_private(const std::string &path, std::string_view data);
std::optional<std::string> read_file(const std::string &path);
bool stat_ok(const std::string &path);
bool lstat_ok(const std::string &path);
// errno of os.Remove (0 on success).
int remove_file(const std::string &path);
// os.RemoveAll: "" or Go's error text.
std::string remove_all(const std::string &path);

// --- randomness -----------------------------------------------------------------------------------------------------

// crypto/rand.Read; replaceable by a check. false when no randomness is available.
extern std::function<bool(unsigned char *, std::size_t)> random_bytes;
std::string hex_encode(std::string_view bytes);
// The random part of an os.CreateTemp name (not from random_bytes: Go draws it from its own generator).
std::string temp_suffix();

// --- internal/toolcache ---------------------------------------------------------------------------------------------

// A program's output, run again only when the program file changed (inode, size, mtime) or, after a failure, once
// retry has passed.
class ToolCache {
public:
	using Runner = std::function<std::pair<std::string, std::string>(const std::string &path, const std::vector<std::string> &args)>;
	ToolCache(Runner run, std::chrono::seconds retry) : run_(std::move(run)), retry_(retry) {}
	// The output and the error text ("" on success).
	std::pair<std::string, std::string> output(const std::string &path, const std::vector<std::string> &args);

private:
	struct Entry {
		std::string stamp, out, err;
		std::chrono::steady_clock::time_point at;
	};
	std::mutex mutex_;
	Runner run_;
	std::chrono::seconds retry_;
	std::map<std::string, Entry> entries_;
};

// pkg/resolvconf.Has: openresolv holds a record under key.
bool resolvconf_has(const std::string &key);

} // namespace nkos::extensions
