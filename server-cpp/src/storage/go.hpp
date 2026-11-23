#pragma once

// The parts of Go's standard library whose exact behaviour the storage family's answers depend on: path cleaning
// (path/filepath), Unicode spaces and case folding (strings), UTF-8 decoding, URL parsing (net/url), Content-Disposition
// values (mime.FormatMediaType), and the text of Go's error values (os.PathError, os.LinkError, the errno messages
// of the syscall package, exec's exit status), which several answers pass on to the page as Go wrote them.

#include <cstdint>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::storage::go {

// path/filepath (Unix).
std::string clean(std::string_view path);
std::string base(std::string_view path);
std::string dir(std::string_view path);
std::string ext(std::string_view path);
std::string join(std::string_view a, std::string_view b);

// strings.
std::string trim_space(std::string_view s);
std::vector<std::string> fields(std::string_view s);
bool equal_fold(std::string_view a, std::string_view b);
std::string to_lower(std::string_view s);

// unicode/utf8 and unicode.
bool valid_utf8(std::string_view s);
// The runes as Go's range loop decodes them: U+FFFD for each byte of an invalid sequence.
std::u32string runes(std::string_view s);
bool is_control(char32_t r);
bool is_space(char32_t r);
// len(utf16.Encode([]rune(s))).
std::size_t utf16_length(std::string_view s);

// Error texts. errno_text is the syscall package's message ("no such file or directory").
std::string errno_text(int err);
// os.PathError: "op path: text".
std::string path_error(std::string_view op, std::string_view path, int err);
// exec.ExitError for a wait status ("exit status 1", "signal: killed").
std::string exit_text(int exit_code, int signal);

// An error as Go would have it: the text, the errno behind it (errors.Is checks) and, for the storage family's own
// sentinel errors, a tag.
struct Error {
	std::string text;
	int sys = 0;
	std::string tag;
	explicit operator bool() const { return !text.empty(); }
	static Error none() { return {}; }
	static Error of(std::string text, std::string tag = {}) { return {std::move(text), 0, std::move(tag)}; }
	static Error path(std::string_view op, std::string_view path, int err) { return {path_error(op, path, err), err, {}}; }
};

// os.Remove, os.Rename, os.ReadFile, os.WriteFile (truncating, created with mode), os.Lstat existence.
Error remove(const std::string &path);
Error rename(const std::string &from, const std::string &to);
// os.RemoveAll (no symbolic link followed).
void remove_all(const std::string &path);
Error read_file(const std::string &path, std::string &data);
Error write_file(const std::string &path, std::string_view data, unsigned mode);
bool exists(const std::string &path);  // os.Stat succeeds
bool lexists(const std::string &path);  // os.Lstat succeeds
// os.CreateTemp(dir, pattern): the last '*' (or the end) replaced by a random number; the open descriptor (O_RDWR,
// 0600, close-on-exec) and the path, or the error.
Error create_temp(const std::string &dir, const std::string &pattern, int &fd, std::string &path);

// net/url.Parse.
struct Url {
	std::string scheme, opaque, host, path, raw_path, raw_query, fragment;
	bool has_user = false, force_query = false;
	std::string hostname() const;
	std::string port() const;
	std::string escaped_path() const;
	std::string request_uri() const;
	// URL.String (without user information: the callers never keep it).
	std::string string() const;
};
std::optional<Url> parse_url(std::string_view raw);
// url.PathEscape-like escaping of the path (encodePath), for building request URLs.
std::string escape_path(std::string_view s);

// net.JoinHostPort.
std::string join_host_port(std::string_view host, std::string_view port);

// mime.FormatMediaType(type, {key: value}) for one parameter.
std::string format_media_type(std::string_view type, std::string_view key, std::string_view value);

// time.Time.UTC().Format(time.RFC3339) of seconds since the epoch.
std::string rfc3339(std::int64_t seconds);

// A float64 as encoding/json writes it.
std::string json_float(double value);

} // namespace nkos::storage::go
