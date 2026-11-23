#pragma once

// What the network family needs of Go's standard library, with Go's semantics: strings.Fields/TrimSpace (Unicode
// white space), os file operations with Go's error texts, net/netip address parsing and printing, the network
// interfaces as net.Interfaces lists them, external commands as os/exec runs them (stdout only unless combined),
// request binding as Gin does it (encoding/json or the form) and the API's JSON answers as gin writes them.

#include "http/router.hpp"

#include <array>
#include <chrono>
#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::network {

// --- strings --------------------------------------------------------------------------------------------------------

bool is_space_rune(char32_t r);
// strings.Fields and strings.TrimSpace: Unicode white space, invalid UTF-8 is not space.
std::vector<std::string> fields(std::string_view s);
std::string trim_space(std::string_view s);
std::string trim_right(std::string_view s, std::string_view cutset);
std::string trim_left(std::string_view s, std::string_view cutset);
std::string trim_chars(std::string_view s, std::string_view cutset);
// strings.Split (sep not empty).
std::vector<std::string> split(std::string_view s, std::string_view sep);
std::string join(const std::vector<std::string> &parts, std::string_view sep);
bool valid_utf8(std::string_view s);
std::size_t rune_count(std::string_view s);
std::string hex_encode(std::string_view s);
// strconv.Quote (fmt's %q): Go escapes for quotes, backslashes, control characters, invalid UTF-8 and the
// non-printable code points it is most likely to meet.
std::string go_quote(std::string_view s);
// hex.DecodeString: even length, hex digits of either case.
std::optional<std::string> hex_decode(std::string_view s);
// strconv.Atoi: optional sign, decimal digits, int64 range; nullopt on error.
std::optional<long long> atoi(std::string_view s);
// strconv.ParseUint(s, base, bits): base 0 takes 0x/0o/0b/0 prefixes and underscores between digits.
std::optional<unsigned long long> parse_uint(std::string_view s, int base, int bits);
// strconv.ParseFloat(s, 64) for decimal numbers (what iw and wpa_supplicant print); nullopt on error.
std::optional<double> parse_float(std::string_view s);
// strconv.FormatFloat(f, 'f', -1, 64) and encoding/json's float format.
std::string json_float(double f);

// --- errors and files ------------------------------------------------------------------------------------------------

// A Go error: its text, and the errno underneath (os.IsNotExist is code == ENOENT). Empty text: nil.
struct Error {
	int code = 0;
	std::string text;
	explicit operator bool() const { return !text.empty(); }
	bool not_exist() const;
};
Error make_error(std::string text);
Error wrap(const std::string &prefix, const Error &err);  // fmt.Errorf("prefix: %w", err)
std::string errno_text(int code);                        // syscall.Errno.Error()
Error path_error(const char *op, const std::string &path, int code);
Error join_errors(const std::vector<Error> &errs);       // errors.Join

std::optional<std::string> read_file(const std::string &path, Error *err = nullptr);
Error stat_path(const std::string &path);   // os.Stat
Error lstat_path(const std::string &path);  // os.Lstat
bool exists(const std::string &path);
bool is_dir(const std::string &path);
// os.WriteFile: truncates an existing file in place (its mode stays), creates a new one with mode.
Error write_file(const std::string &path, std::string_view data, unsigned mode);
// writePrivateFile: os.WriteFile 0600, then chmod 0600.
Error write_private_file(const std::string &path, std::string_view data);
// trial.go writeFileAtomic: path.tmp (0600), synced, renamed over path; the directory made first.
Error write_file_atomic(const std::string &path, std::string_view data);
// internal/atomicfile.Write: a temporary .<name>.tmp-N beside path with perm, synced, renamed, directory synced.
Error atomicfile_write(const std::string &path, std::string_view data, unsigned perm);
Error mkdir_all(const std::string &path, unsigned mode);
Error mkdir(const std::string &path, unsigned mode);
Error chmod_path(const std::string &path, unsigned mode);
Error remove(const std::string &path);      // os.Remove
Error remove_all(const std::string &path);  // os.RemoveAll
Error rename(const std::string &from, const std::string &to);
std::string dir_name(const std::string &path);   // filepath.Dir
std::string base_name(const std::string &path);  // filepath.Base
std::string path_join(const std::string &a, const std::string &b);
struct DirEntry {
	std::string name;
	bool dir = false;
	bool info_ok = false;
	std::int64_t mtime_ns = 0;
};
// os.ReadDir: sorted by name.
std::vector<DirEntry> read_dir(const std::string &path, Error *err = nullptr);
// filepath.EvalSymlinks; nullopt on error.
std::optional<std::string> eval_symlinks(const std::string &path);

// --- addresses (net/netip, net) --------------------------------------------------------------------------------------

struct Addr {
	int kind = 0;  // 0 invalid, 4, 6
	std::array<std::uint8_t, 16> b{};  // IPv4 in b[0..3]
	std::string zone;

	bool valid() const { return kind != 0; }
	bool is4() const { return kind == 4; }
	bool is6() const { return kind == 6; }
	bool is4in6() const;
	Addr unmap() const;
	bool unspecified() const;
	bool loopback() const;
	bool multicast() const;
	bool private_() const;
	bool link_local_unicast() const;
	int bit_len() const { return kind == 4 ? 32 : kind == 6 ? 128 : 0; }
	Addr next() const;
	std::string str() const;
	bool operator==(const Addr &o) const;
};
Addr addr4(std::uint8_t a, std::uint8_t b, std::uint8_t c, std::uint8_t d);
std::optional<Addr> parse_addr(std::string_view s);  // netip.ParseAddr

struct Prefix {
	Addr addr;
	int bits = -1;
	bool valid() const { return addr.valid() && bits >= 0; }
	Prefix masked() const;
	bool contains(const Addr &a) const;
	std::string str() const;
};
std::optional<Prefix> parse_prefix(std::string_view s);  // netip.ParsePrefix

// net.ParseIP: 16 bytes (IPv4 as ::ffff:a.b.c.d), nullopt for an invalid address or one with a zone.
using IP16 = std::array<std::uint8_t, 16>;
std::optional<IP16> parse_ip(std::string_view s);
std::string ip_string(const IP16 &ip);  // net.IP.String
bool ip_is_v4(const IP16 &ip);          // To4() != nil
// net.ParseMAC accepts 6, 8 or 20 byte addresses in the three notations.
bool parse_mac_ok(std::string_view s);
// net.SplitHostPort and net.JoinHostPort.
bool split_host_port(std::string_view hostport, std::string &host, std::string &port);
std::string join_host_port(const std::string &host, const std::string &port);

// --- interfaces (net.Interfaces, Interface.Addrs) ---------------------------------------------------------------------

struct IfAddr {
	bool v4 = false;
	IP16 ip{};          // IPv4 as ::ffff:a.b.c.d, as Go keeps it
	int prefix_len = 0;
	std::string str() const;  // IPNet.String: "ip/len"
	std::string mask_str() const;  // net.IP(mask).String()
};
struct Interface {
	std::string name;
	int index = 0;
	unsigned flags = 0;       // IFF_*
	std::string hwaddr;       // raw bytes, empty when all zero
	std::vector<IfAddr> addrs;
};
// In index order, each with its addresses in the kernel's order.
std::vector<Interface> interfaces(bool *ok = nullptr);
std::optional<Interface> interface_by_name(const std::string &name);

// --- commands (os/exec) ----------------------------------------------------------------------------------------------

struct RunOptions {
	std::optional<std::chrono::milliseconds> timeout;  // none: wait for the command
	std::string input;
	std::vector<std::string> env;  // extra NAME=value entries
	bool combined = false;         // CombinedOutput; otherwise Output (stdout; stderr kept apart)
	// Cmd.WaitDelay: after the process ended, how long its output pipes may stay open (a daemon it started holds
	// them); none: until they close.
	std::optional<std::chrono::milliseconds> wait_delay;
};
struct RunResult {
	std::string out;   // stdout, or stdout and stderr when combined
	std::string err_out;
	std::string error;  // err.Error(), empty on success
	int exit_code = -1;
	bool ok() const { return error.empty(); }
};
RunResult run(const std::vector<std::string> &argv, const RunOptions &options = {});
// exec.LookPath: the program in $PATH, or nullopt.
std::optional<std::string> look_path(const std::string &name);
// oomscore.Unprotected: the command at the normal OOM adjustment and nice value.
std::vector<std::string> unprotected(const std::string &name, const std::vector<std::string> &args);
// exec.Command("sync").Run()
Error sync_files();

// --- requests (Gin binding) -------------------------------------------------------------------------------------------

// A decoded JSON value as encoding/json reads it.
struct JValue {
	enum class Kind { null, boolean, number, string, array, object } kind = Kind::null;
	bool flag = false;
	std::string text;  // the string, or the number's literal
	std::vector<JValue> items;
	std::vector<std::pair<std::string, JValue>> members;  // in document order
};
// The first JSON value of text, as json.Decoder.Decode reads it (what follows is not read); nullopt on a syntax error
// or no value.
std::optional<JValue> parse_json(std::string_view text);
// json.Unmarshal: the whole text is one JSON value (white space around it only).
std::optional<JValue> parse_json_whole(std::string_view text);

// The request as Gin's binding sees it: method, Content-Type, query and body.
struct RequestData {
	std::string method, content_type, query, body;
};
RequestData request_data(const http::Request &req);
// Gin's c.Query(name): the first value of the query parameter, "" when absent.
std::string go_query(const std::string &query, const std::string &name);

// Binds fields the way encoding/json fills a struct (keys matched exactly, else without regard to case; a value of
// another type is an error) or Gin's form binding (keys exact; bool and int parsed from text).
class Binder {
public:
	// ShouldBindJSON (json=true) or ShouldBind.
	static Binder bind(const RequestData &data, bool json_only);
	bool ok() const { return ok_; }
	bool json() const { return json_; }
	// Each returns whether the field was present (a JSON null counts as absent); a wrong type clears ok().
	bool str(const std::string &json_name, const std::string &form_name, std::string &out);
	bool boolean(const std::string &json_name, const std::string &form_name, bool &out);
	bool integer(const std::string &json_name, const std::string &form_name, long long &out);
	bool strings(const std::string &json_name, const std::string &form_name, std::vector<std::string> &out);
	// A nested JSON object (form binding has none).
	std::optional<Binder> object(const std::string &json_name);
	void fail() { ok_ = false; }

private:
	const JValue *member(const std::string &name) const;
	bool ok_ = false, json_ = false;
	JValue value_;
	std::map<std::string, std::vector<std::string>> form_;
};

// --- answers (proto.Response through gin) ----------------------------------------------------------------------------

std::string quote(std::string_view s);  // encoding/json string
void send(http::Response &res, int code, std::string_view msg, std::string_view data_json = {});
inline void ok(http::Response &res) { send(res, 0, "success"); }
inline void ok(http::Response &res, std::string_view data_json) { send(res, 0, "success", data_json); }
inline void fail(http::Response &res, int code, std::string_view msg) { send(res, code, msg); }
std::string json_strings(const std::vector<std::string> &list, bool nil_as_null);

// The local address of the request's connection (IPv4 unmapped), from its socket. Loop thread.
std::optional<IP16> local_address(const http::Request &req);

} // namespace nkos::network
