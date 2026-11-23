#pragma once

// The parts of Go's standard library the notifications rely on, with Go's behaviour: what the Go server accepted,
// wrote and sent stays the same (UTF-8 and Unicode spaces, %q, URLs and query strings, mail addresses, RFC 2047
// words, quoted-printable, multipart bodies, the time layouts of the messages).

#include <chrono>
#include <cstddef>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace nkos::notify::gostd {

// utf8.DecodeRuneInString at i: the rune and its length; an invalid byte is U+FFFD of length 1.
std::pair<char32_t, std::size_t> decode_rune(std::string_view s, std::size_t i);
void append_rune(std::string &out, char32_t r);
// utf8.RuneCountInString.
std::size_t rune_count(std::string_view s);
// unicode.IsSpace.
bool is_space(char32_t r);
// strings.TrimSpace, strings.Fields, strings.ToLower.
std::string trim_space(std::string_view s);
std::vector<std::string> fields(std::string_view s);
std::string to_lower(std::string_view s);
// strings.ContainsAny.
bool contains_any(std::string_view s, std::string_view chars);
// strconv.Quote: the %q of a string.
std::string quote(std::string_view s);
// strings.NewReplacer(old1, new1, ...).Replace for the replacers of the notifications: at each position the first
// pair (in order) whose old string matches is replaced.
std::string replace(std::string_view s, const std::vector<std::pair<std::string_view, std::string_view>> &pairs);

namespace url {

std::string query_escape(std::string_view s);
std::string path_escape(std::string_view s);
std::optional<std::string> query_unescape(std::string_view s);

// url.URL, the parts the notifications use.
struct URL {
	std::string scheme, opaque, host, path, raw_path, raw_query, fragment;
	bool force_query = false;
	std::string escaped_path() const;
	std::string request_uri() const;
};

// url.Parse; nullopt where Go returns an error.
std::optional<URL> parse(std::string_view raw);

// url.Values: keys in order, each with its values in order.
using Values = std::map<std::string, std::vector<std::string>>;
// URL.Query (url.ParseQuery with its errors ignored).
Values parse_query(std::string_view query);
// Values.Encode.
std::string encode(const Values &values);

} // namespace url

namespace mail {

struct Address {
	std::string name, address;
	// Address.String.
	std::string string() const;
};

// mail.ParseAddress; nullopt where Go returns an error.
std::optional<Address> parse_address(std::string_view s);

} // namespace mail

namespace mime {

// mime.QEncoding.Encode and mime.BEncoding.Encode.
std::string q_encode(std::string_view charset, std::string_view s);
std::string b_encode(std::string_view charset, std::string_view s);
// What mime/quotedprintable's Writer writes for s, closed.
std::string quoted_printable(std::string_view s);

} // namespace mime

// mime/multipart.Writer: parts of header lines (written sorted by name) and content.
class Multipart {
public:
	// boundary: empty for a random one (60 hex digits, as Go's).
	explicit Multipart(std::string boundary = {});
	const std::string &boundary() const { return boundary_; }
	// FormDataContentType.
	std::string form_data_content_type() const;
	void part(std::vector<std::pair<std::string, std::string>> header, std::string_view content);
	// WriteField and CreateFormFile + Write.
	void field(std::string_view name, std::string_view value);
	void file(std::string_view field, std::string_view filename, std::string_view data);
	// Close: the body.
	std::string close();

private:
	std::string boundary_, body_;
	bool first_ = true;
};

// Times in Go's layouts, in the local zone unless utc.
using Clock = std::chrono::system_clock;
std::string format_datetime_zone(Clock::time_point t);   // "2006-01-02 15:04:05 MST"
std::string format_rfc3339(Clock::time_point t, bool utc);  // time.RFC3339
std::string format_rfc1123z(Clock::time_point t);       // time.RFC1123Z
std::string format_clock(Clock::time_point t);          // "15:04"

// Lower-case hex of n random bytes (crypto/rand), and base64.RawURLEncoding of n random bytes.
std::string random_hex(std::size_t n);
std::string random_base64url(std::size_t n);
std::string base64url_raw(std::string_view data);
std::optional<std::string> base64url_raw_decode(std::string_view text);

} // namespace nkos::notify::gostd
