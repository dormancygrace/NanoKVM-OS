#include "system/tls_x509.hpp"

#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "config/config.hpp"

#include <openssl/bn.h>
#include <openssl/core_names.h>
#include <openssl/ec.h>
#include <openssl/evp.h>
#include <openssl/obj_mac.h>
#include <openssl/param_build.h>
#include <openssl/pem.h>
#include <openssl/rand.h>
#include <openssl/x509.h>
#include <openssl/x509v3.h>

#include <algorithm>
#include <arpa/inet.h>
#include <cerrno>
#include <cstring>
#include <ctime>
#include <fcntl.h>
#include <format>
#include <functional>
#include <memory>
#include <set>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::system::tls {

namespace {

// ---- Go error texts and files ----

const char *const errno_texts[] = {
	nullptr, "operation not permitted", "no such file or directory", "no such process", "interrupted system call",
	"input/output error", "no such device or address", "argument list too long", "exec format error",
	"bad file descriptor", "no child processes", "resource temporarily unavailable", "cannot allocate memory",
	"permission denied", "bad address", "block device required", "device or resource busy", "file exists",
	"invalid cross-device link", "no such device", "not a directory", "is a directory", "invalid argument",
	"too many open files in system", "too many open files", "inappropriate ioctl for device", "text file busy",
	"file too large", "no space left on device", "illegal seek", "read-only file system", "too many links",
	"broken pipe", "numerical argument out of domain", "numerical result out of range", "resource deadlock avoided",
	"file name too long", "no locks available", "function not implemented", "directory not empty",
	"too many levels of symbolic links"};

// ---- BIGNUM helpers ----

struct BnFree {
	void operator()(BIGNUM *b) const { BN_free(b); }
};
using Bn = std::unique_ptr<BIGNUM, BnFree>;
struct CtxFree {
	void operator()(BN_CTX *c) const { BN_CTX_free(c); }
};
struct PkeyFree {
	void operator()(EVP_PKEY *k) const { EVP_PKEY_free(k); }
};
using Pkey = std::unique_ptr<EVP_PKEY, PkeyFree>;
struct GroupFree {
	void operator()(EC_GROUP *g) const { EC_GROUP_free(g); }
};
struct PointFree {
	void operator()(EC_POINT *p) const { EC_POINT_free(p); }
};

Bn bn(std::string_view bytes)
{
	return Bn(BN_bin2bn(reinterpret_cast<const unsigned char *>(bytes.data()), static_cast<int>(bytes.size()), nullptr));
}
Bn bn_int(std::uint64_t v)
{
	Bn b(BN_new());
	BN_set_word(b.get(), v);
	return b;
}
std::string bn_bytes(const BIGNUM *b)
{
	std::string out(static_cast<size_t>(BN_num_bytes(b)), '\0');
	BN_bn2bin(b, reinterpret_cast<unsigned char *>(out.data()));
	return out;
}
std::string strip_zeros(std::string_view b)
{
	size_t i = 0;
	while (i < b.size() && b[i] == 0) i++;
	return std::string(b.substr(i));
}

// ---- time ----

std::int64_t days_from_civil(std::int64_t y, unsigned m, unsigned d)
{
	y -= m <= 2;
	const std::int64_t era = (y >= 0 ? y : y - 399) / 400;
	const unsigned yoe = static_cast<unsigned>(y - era * 400);
	const unsigned doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
	const unsigned doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
	return era * 146097 + static_cast<std::int64_t>(doe) - 719468;
}

void civil_from_days(std::int64_t z, std::int64_t &y, unsigned &m, unsigned &d)
{
	z += 719468;
	const std::int64_t era = (z >= 0 ? z : z - 146096) / 146097;
	const unsigned doe = static_cast<unsigned>(z - era * 146097);
	const unsigned yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
	y = static_cast<std::int64_t>(yoe) + era * 400;
	const unsigned doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
	const unsigned mp = (5 * doy + 2) / 153;
	d = doy - (153 * mp + 2) / 5 + 1;
	m = mp < 10 ? mp + 3 : mp - 9;
	y += m <= 2;
}

bool leap(std::int64_t y) { return y % 4 == 0 && (y % 100 != 0 || y % 400 == 0); }
int days_in(int month, std::int64_t year)
{
	static const int days[] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
	return month == 2 && leap(year) ? 29 : days[month - 1];
}

// A time as Go keeps it after parsing: the instant and the fields in its zone.
struct GoTime {
	std::int64_t unix = 0;
	int offset = 0;  // seconds east of UTC
	std::int64_t year = 0;
	int month = 0, day = 0, hour = 0, minute = 0, second = 0;
};

bool fixed_digits(std::string_view s, size_t pos, size_t n, int &out)
{
	if (pos + n > s.size()) return false;
	out = 0;
	for (size_t i = pos; i < pos + n; i++) {
		if (s[i] < '0' || s[i] > '9') return false;
		out = out * 10 + (s[i] - '0');
	}
	return true;
}

// time.Parse with "060102150405Z0700" (two-digit year, with or without seconds) or "20060102150405Z0700", then the
// round trip through Format that cryptobyte requires: strict digits, 'Z' for UTC, +hhmm otherwise.
std::optional<GoTime> parse_go_time(std::string_view t, bool two_digit_year, bool seconds)
{
	GoTime g;
	size_t pos = 0;
	int v = 0;
	if (two_digit_year) {
		if (!fixed_digits(t, 0, 2, v)) return std::nullopt;
		g.year = v >= 69 ? 1900 + v : 2000 + v;
		pos = 2;
	} else {
		if (!fixed_digits(t, 0, 4, v)) return std::nullopt;
		g.year = v;
		pos = 4;
	}
	if (!fixed_digits(t, pos, 2, g.month) || !fixed_digits(t, pos + 2, 2, g.day) || !fixed_digits(t, pos + 4, 2, g.hour) ||
	    !fixed_digits(t, pos + 6, 2, g.minute))
		return std::nullopt;
	pos += 8;
	if (seconds) {
		if (!fixed_digits(t, pos, 2, g.second)) return std::nullopt;
		pos += 2;
	}
	if (pos >= t.size()) return std::nullopt;
	if (t[pos] == 'Z') {
		if (pos + 1 != t.size()) return std::nullopt;
	} else {
		int hh = 0, mm = 0;
		if ((t[pos] != '+' && t[pos] != '-') || pos + 5 != t.size() || !fixed_digits(t, pos + 1, 2, hh) ||
		    !fixed_digits(t, pos + 3, 2, mm))
			return std::nullopt;
		if (hh > 24 || mm >= 60) return std::nullopt;  // > 60 is Parse's range error, 60 fails the round trip
		g.offset = (hh * 60 + mm) * 60 * (t[pos] == '-' ? -1 : 1);
		if (g.offset == 0) return std::nullopt;  // formats back as 'Z'
	}
	if (g.month < 1 || g.month > 12 || g.hour >= 24 || g.minute >= 60 || g.second >= 60) return std::nullopt;
	if (g.day < 1 || g.day > days_in(g.month, g.year)) return std::nullopt;
	return g;
}

void set_unix(GoTime &g)
{
	g.unix = days_from_civil(g.year, static_cast<unsigned>(g.month), static_cast<unsigned>(g.day)) * 86400 + g.hour * 3600 +
	         g.minute * 60 + g.second - g.offset;
}

// ---- Go string quoting (strconv.Quote for the texts that reach messages) ----

std::string go_quote(std::string_view s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	for (size_t i = 0; i < s.size(); i++) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		switch (c) {
		case '"': out += "\\\""; continue;
		case '\\': out += "\\\\"; continue;
		case '\a': out += "\\a"; continue;
		case '\b': out += "\\b"; continue;
		case '\f': out += "\\f"; continue;
		case '\n': out += "\\n"; continue;
		case '\r': out += "\\r"; continue;
		case '\t': out += "\\t"; continue;
		case '\v': out += "\\v"; continue;
		}
		if (c < 0x20 || c == 0x7f || c >= 0x80) {
			out += "\\x";
			out += hex[c >> 4];
			out += hex[c & 15];
		} else {
			out += static_cast<char>(c);
		}
	}
	return out + "\"";
}

std::string hex_lower(std::string_view b)
{
	static const char hex[] = "0123456789abcdef";
	std::string out;
	for (unsigned char c : b) {
		out += hex[c >> 4];
		out += hex[c & 15];
	}
	return out;
}

// ---- cryptobyte: DER reading as golang.org/x/crypto/cryptobyte ----

namespace tag {
constexpr std::uint8_t BOOLEAN = 0x01, INTEGER = 0x02, BIT_STRING = 0x03, OCTET_STRING = 0x04, NULL_ = 0x05,
                       OBJECT_IDENTIFIER = 0x06, UTF8String = 12, NumericString = 18, PrintableString = 19,
                       T61String = 20, IA5String = 22, UTCTime = 23, GeneralizedTime = 24, BMPString = 30,
                       SEQUENCE = 0x30, SET = 0x31;
constexpr std::uint8_t context(int n) { return static_cast<std::uint8_t>(0x80 | n); }
constexpr std::uint8_t constructed(std::uint8_t t) { return static_cast<std::uint8_t>(t | 0x20); }
} // namespace tag

using Oid = std::vector<int>;

std::string oid_string(const Oid &oid)
{
	std::string s;
	for (size_t i = 0; i < oid.size(); i++) {
		if (i) s += '.';
		s += std::to_string(oid[i]);
	}
	return s;
}

struct Der {
	std::string_view s;

	bool empty() const { return s.empty(); }

	bool read_any(std::string_view *out, std::uint8_t *out_tag, bool skip_header)
	{
		if (s.size() < 2) return false;
		std::uint8_t t = static_cast<std::uint8_t>(s[0]), len_byte = static_cast<std::uint8_t>(s[1]);
		if ((t & 0x1f) == 0x1f) return false;
		if (out_tag) *out_tag = t;
		std::uint64_t length, header;
		if (!(len_byte & 0x80)) {
			length = len_byte + 2u;
			header = 2;
		} else {
			unsigned len_len = len_byte & 0x7f;
			if (len_len == 0 || len_len > 4 || s.size() < 2 + len_len) return false;
			std::uint32_t len32 = 0;
			for (unsigned i = 0; i < len_len; i++) len32 = len32 << 8 | static_cast<std::uint8_t>(s[2 + i]);
			if (len32 < 128) return false;
			if ((len32 >> ((len_len - 1) * 8)) == 0) return false;
			header = 2 + len_len;
			length = header + len32;
			if (static_cast<std::uint32_t>(header + len32) < len32) return false;
		}
		if (length > s.size()) return false;
		std::string_view element = s.substr(0, length);
		s.remove_prefix(length);
		if (out) *out = skip_header ? element.substr(header) : element;
		return true;
	}
	bool read(std::string_view &out, std::uint8_t t)
	{
		std::uint8_t got = 0;
		return read_any(&out, &got, true) && got == t;
	}
	bool read_element(std::string_view &out, std::uint8_t t)
	{
		std::uint8_t got = 0;
		return read_any(&out, &got, false) && got == t;
	}
	bool peek(std::uint8_t t) const { return !s.empty() && static_cast<std::uint8_t>(s[0]) == t; }
	bool read_optional(std::string_view &out, bool &present, std::uint8_t t)
	{
		present = peek(t);
		return !present || read(out, t);
	}
	bool skip_optional(std::uint8_t t)
	{
		if (!peek(t)) return true;
		std::string_view unused;
		return read(unused, t);
	}
	static bool check_integer(std::string_view b)
	{
		if (b.empty()) return false;
		if (b.size() == 1) return true;
		auto b0 = static_cast<std::uint8_t>(b[0]), b1 = static_cast<std::uint8_t>(b[1]);
		return !((b0 == 0 && !(b1 & 0x80)) || (b0 == 0xff && (b1 & 0x80)));
	}
	bool read_int64(std::int64_t &out)
	{
		std::string_view b;
		if (!read(b, tag::INTEGER) || !check_integer(b) || b.size() > 8) return false;
		std::uint64_t v = 0;
		for (unsigned char c : b) v = v << 8 | c;
		unsigned shift = 64 - static_cast<unsigned>(b.size()) * 8;
		out = shift >= 64 ? 0 : static_cast<std::int64_t>(v << shift) >> shift;
		return true;
	}
	bool read_uint64(std::uint64_t &out)
	{
		std::string_view b;
		if (!read(b, tag::INTEGER) || !check_integer(b)) return false;
		if (b.size() > 9 || (b.size() == 9 && b[0] != 0)) return false;
		if (static_cast<std::uint8_t>(b[0]) & 0x80) return false;
		out = 0;
		for (unsigned char c : b) out = out << 8 | c;
		return true;
	}
	// A big integer: two's complement bytes as read (checked minimal).
	bool read_big(std::string_view &out)
	{
		return read(out, tag::INTEGER) && check_integer(out);
	}
	bool read_bool(bool &out)
	{
		std::string_view b;
		if (!read(b, tag::BOOLEAN) || b.size() != 1) return false;
		if (b[0] == 0) out = false;
		else if (static_cast<std::uint8_t>(b[0]) == 0xff) out = true;
		else return false;
		return true;
	}
	static bool base128(std::string_view &s, int &out)
	{
		int ret = 0;
		for (int i = 0; !s.empty(); i++) {
			if (i == 5) return false;
			if (ret >= 1 << (31 - 7)) return false;
			ret <<= 7;
			auto b = static_cast<std::uint8_t>(s[0]);
			s.remove_prefix(1);
			if (i == 0 && b == 0x80) return false;
			ret |= b & 0x7f;
			if (!(b & 0x80)) {
				out = ret;
				return true;
			}
		}
		return false;
	}
	bool read_oid(Oid &out)
	{
		std::string_view b;
		if (!read(b, tag::OBJECT_IDENTIFIER) || b.empty()) return false;
		Oid comps;
		int v;
		if (!base128(b, v)) return false;
		if (v < 80) {
			comps.push_back(v / 40);
			comps.push_back(v % 40);
		} else {
			comps.push_back(2);
			comps.push_back(v - 80);
		}
		while (!b.empty()) {
			if (!base128(b, v)) return false;
			comps.push_back(v);
		}
		out = std::move(comps);
		return true;
	}
	bool read_bit_string(std::string &bytes, int &bit_length)
	{
		std::string_view b;
		if (!read(b, tag::BIT_STRING) || b.empty()) return false;
		auto padding = static_cast<std::uint8_t>(b[0]);
		b.remove_prefix(1);
		if (padding > 7 || (b.empty() && padding != 0) ||
		    (!b.empty() && (static_cast<std::uint8_t>(b.back()) & ((1u << padding) - 1)) != 0))
			return false;
		bit_length = static_cast<int>(b.size()) * 8 - padding;
		bytes = std::string(b);
		return true;
	}
	bool read_utc_time(GoTime &out)
	{
		std::string_view b;
		if (!read(b, tag::UTCTime)) return false;
		auto t = parse_go_time(b, true, true);
		if (!t) t = parse_go_time(b, true, false);
		if (!t) return false;
		if (t->year >= 2050) t->year -= 100;
		set_unix(*t);
		out = *t;
		return true;
	}
	bool read_generalized_time(GoTime &out)
	{
		std::string_view b;
		if (!read(b, tag::GeneralizedTime)) return false;
		auto t = parse_go_time(b, false, true);
		if (!t) return false;
		set_unix(*t);
		out = *t;
		return true;
	}
};

// BitString.RightAlign.
std::string right_align(const std::string &bytes, int bit_length)
{
	int shift = 8 - bit_length % 8;
	if (shift == 8 || bytes.empty()) return bytes;
	std::string a(bytes.size(), '\0');
	auto b = reinterpret_cast<const std::uint8_t *>(bytes.data());
	a[0] = static_cast<char>(b[0] >> shift);
	for (size_t i = 1; i < bytes.size(); i++) {
		a[i] = static_cast<char>(static_cast<std::uint8_t>(b[i - 1] << (8 - shift)) | (b[i] >> shift));
	}
	return a;
}

// Two's complement bytes: sign and magnitude.
bool big_negative(std::string_view b) { return !b.empty() && (static_cast<std::uint8_t>(b[0]) & 0x80); }
bool big_positive(std::string_view b)
{
	if (big_negative(b)) return false;
	for (char c : b)
		if (c) return true;
	return false;
}
// The magnitude of a non-negative integer.
std::string big_magnitude(std::string_view b) { return strip_zeros(b); }

// ---- encoding/asn1: Unmarshal for the private key structures ----

enum class Kind { int64, bigint, octets, oid, bitstring, raw, sequence, sequence_of };

struct Params {
	bool optional = false, explicit_ = false, omit_empty = false;
	int tag = -1;
	bool has_default = false;
	std::int64_t default_value = 0;
};

struct TL {
	int cls = 0, tag = 0, length = 0;
	bool compound = false;
};

std::string structural(std::string_view msg) { return "asn1: structure error: " + std::string(msg); }
std::string syntax(std::string_view msg) { return "asn1: syntax error: " + std::string(msg); }

std::optional<std::string> asn1_base128(std::string_view b, size_t &offset, int &out)
{
	std::int64_t r = 0;
	for (int shifted = 0; offset < b.size(); shifted++) {
		if (shifted == 5) return structural("base 128 integer too large");
		r <<= 7;
		auto c = static_cast<std::uint8_t>(b[offset]);
		if (shifted == 0 && c == 0x80) return syntax("integer is not minimally encoded");
		r |= c & 0x7f;
		offset++;
		if (!(c & 0x80)) {
			out = static_cast<int>(r);
			if (r > 0x7fffffff) return structural("base 128 integer too large");
			return std::nullopt;
		}
	}
	return syntax("truncated base 128 integer");
}

std::optional<std::string> parse_tag_length(std::string_view b, size_t &offset, TL &t)
{
	if (offset >= b.size()) return "asn1: internal error in parseTagAndLength";
	auto c = static_cast<std::uint8_t>(b[offset++]);
	t.cls = c >> 6;
	t.compound = c & 0x20;
	t.tag = c & 0x1f;
	if (t.tag == 0x1f) {
		if (auto e = asn1_base128(b, offset, t.tag)) return e;
		if (t.tag < 0x1f) return syntax("non-minimal tag");
	}
	if (offset >= b.size()) return syntax("truncated tag or length");
	c = static_cast<std::uint8_t>(b[offset++]);
	if (!(c & 0x80)) {
		t.length = c & 0x7f;
	} else {
		int n = c & 0x7f;
		if (n == 0) return syntax("indefinite length found (not DER)");
		t.length = 0;
		for (int i = 0; i < n; i++) {
			if (offset >= b.size()) return syntax("truncated tag or length");
			c = static_cast<std::uint8_t>(b[offset++]);
			if (t.length >= 1 << 23) return structural("length too large");
			t.length = t.length << 8 | c;
			if (t.length == 0) return structural("superfluous leading zeros in length");
		}
		if (t.length < 0x80) return structural("non-minimal length");
	}
	return std::nullopt;
}

std::string params_text(const Params &p)
{
	// The tag is a pointer in Go and prints as its address; no field that reaches a shown message has one.
	return std::format("{{optional:{} explicit:{} application:false private:false defaultValue:{} tag:{} stringType:0 "
	                   "timeType:0 set:false omitEmpty:{}}}",
	                   p.optional, p.explicit_, p.has_default ? "0xc000000000" : "<nil>",
	                   p.tag >= 0 ? "0xc000000000" : "<nil>", p.omit_empty);
}

struct Field {
	bool present = false;
	TL t;
	std::string_view inner, full;
};

bool invalid_length(size_t offset, int length, size_t size) { return offset + static_cast<size_t>(length) > size; }

// parseField up to the content: the tag checks, defaults and lengths. The caller decodes inner per kind.
std::optional<std::string> asn1_field(std::string_view b, size_t &offset, const Params &p, Kind kind,
                                      std::string_view type_name, Field &out)
{
	const size_t init = offset;
	out = Field{};
	if (offset == b.size()) {
		if (!p.optional) return syntax("sequence truncated");
		return std::nullopt;
	}
	TL t;
	if (auto e = parse_tag_length(b, offset, t)) return e;
	if (p.explicit_) {
		if (offset == b.size()) return structural("explicit tag has no child");
		if (t.cls == 2 && t.tag == p.tag && (t.length == 0 || t.compound)) {
			if (kind == Kind::raw) {
			} else if (t.length > 0) {
				if (auto e = parse_tag_length(b, offset, t)) return e;
			} else {
				return structural("zero length explicit tag was not an asn1.Flag");
			}
		} else {
			if (p.optional) {
				offset = init;
				return std::nullopt;
			}
			return structural("explicitly tagged member didn't match");
		}
	}
	bool match_any = kind == Kind::raw;
	int universal = 0;
	bool compound = false;
	switch (kind) {
	case Kind::int64:
	case Kind::bigint: universal = 2; break;
	case Kind::octets: universal = 4; break;
	case Kind::oid: universal = 6; break;
	case Kind::bitstring: universal = 3; break;
	case Kind::sequence:
	case Kind::sequence_of: universal = 16, compound = true; break;
	case Kind::raw: break;
	}
	bool match_any_class_and_tag = match_any;
	int expected_class = 0, expected_tag = universal;
	if (!p.explicit_ && p.tag >= 0) {
		expected_class = 2;
		expected_tag = p.tag;
		match_any_class_and_tag = false;
	}
	if ((!match_any_class_and_tag && (t.cls != expected_class || t.tag != expected_tag)) ||
	    (!match_any && t.compound != compound)) {
		if (p.optional) {
			offset = init;
			return std::nullopt;
		}
		return structural(std::format("tags don't match ({} vs {{class:{} tag:{} length:{} isCompound:{}}}) {} {} @{}",
		                              expected_tag, t.cls, t.tag, t.length, t.compound, params_text(p), type_name, offset));
	}
	if (invalid_length(offset, t.length, b.size())) return syntax("data truncated");
	out.present = true;
	out.t = t;
	out.inner = b.substr(offset, static_cast<size_t>(t.length));
	offset += static_cast<size_t>(t.length);
	out.full = b.substr(init, offset - init);
	return std::nullopt;
}

std::optional<std::string> asn1_check_integer(std::string_view b)
{
	if (b.empty()) return structural("empty integer");
	if (b.size() == 1) return std::nullopt;
	auto b0 = static_cast<std::uint8_t>(b[0]), b1 = static_cast<std::uint8_t>(b[1]);
	if ((b0 == 0 && !(b1 & 0x80)) || (b0 == 0xff && (b1 & 0x80))) return structural("integer not minimally-encoded");
	return std::nullopt;
}

std::optional<std::string> asn1_int64(std::string_view b, std::int64_t &out)
{
	if (auto e = asn1_check_integer(b)) return e;
	if (b.size() > 8) return structural("integer too large");
	std::uint64_t v = 0;
	for (unsigned char c : b) v = v << 8 | c;
	unsigned shift = 64 - static_cast<unsigned>(b.size()) * 8;
	out = shift >= 64 ? 0 : static_cast<std::int64_t>(v << shift) >> shift;
	return std::nullopt;
}

std::optional<std::string> asn1_oid(std::string_view b, Oid &out)
{
	if (b.empty()) return syntax("zero length OBJECT IDENTIFIER");
	size_t offset = 0;
	int v = 0;
	if (auto e = asn1_base128(b, offset, v)) return e;
	Oid s;
	if (v < 80) {
		s.push_back(v / 40);
		s.push_back(v % 40);
	} else {
		s.push_back(2);
		s.push_back(v - 80);
	}
	while (offset < b.size()) {
		if (auto e = asn1_base128(b, offset, v)) return e;
		s.push_back(v);
	}
	out = std::move(s);
	return std::nullopt;
}

std::optional<std::string> asn1_bitstring(std::string_view b)
{
	if (b.empty()) return syntax("zero length BIT STRING");
	auto padding = static_cast<std::uint8_t>(b[0]);
	if (padding > 7 || (b.size() == 1 && padding > 0) || (static_cast<std::uint8_t>(b.back()) & ((1u << padding) - 1)) != 0)
		return syntax("invalid padding bits in BIT STRING");
	return std::nullopt;
}

// Reads one field of a kind with a simple value: integer, big integer, octets, OID, bit string, raw.
struct Value {
	bool present = false;
	std::int64_t i = 0;
	std::string_view bytes;  // big integer (two's complement), octets, bit string content, raw full bytes
	Oid oid;
	Field field;
};

std::optional<std::string> asn1_value(std::string_view b, size_t &offset, const Params &p, Kind kind,
                                      std::string_view type_name, Value &v)
{
	v = Value{};
	if (auto e = asn1_field(b, offset, p, kind, type_name, v.field)) return e;
	if (!v.field.present) {
		if (p.has_default) v.i = p.default_value;
		return std::nullopt;
	}
	v.present = true;
	switch (kind) {
	case Kind::int64: return asn1_int64(v.field.inner, v.i);
	case Kind::bigint:
		v.bytes = v.field.inner;
		return asn1_check_integer(v.field.inner);
	case Kind::octets: v.bytes = v.field.inner; return std::nullopt;
	case Kind::oid: return asn1_oid(v.field.inner, v.oid);
	case Kind::bitstring: v.bytes = v.field.inner; return asn1_bitstring(v.field.inner);
	case Kind::raw: v.bytes = v.field.full; return std::nullopt;
	default: return std::nullopt;
	}
}

// AlgorithmIdentifier {Algorithm ObjectIdentifier; Parameters RawValue `asn1:"optional"`}.
struct AlgorithmIdentifier {
	Oid algorithm;
	std::string_view parameters;  // FullBytes, empty when absent
};
std::optional<std::string> asn1_algorithm_identifier(std::string_view inner, AlgorithmIdentifier &ai)
{
	size_t off = 0;
	Value v;
	if (auto e = asn1_value(inner, off, {}, Kind::oid, "ObjectIdentifier", v)) return e;
	ai.algorithm = v.oid;
	Params optional;
	optional.optional = true;
	if (auto e = asn1_value(inner, off, optional, Kind::raw, "RawValue", v)) return e;
	ai.parameters = v.present ? v.bytes : std::string_view();
	return std::nullopt;
}

// pkcs8 {Version int; Algo pkix.AlgorithmIdentifier; PrivateKey []byte}.
struct Pkcs8 {
	AlgorithmIdentifier algo;
	std::string_view private_key;
};
std::optional<std::string> unmarshal_pkcs8(std::string_view der, Pkcs8 &out)
{
	size_t offset = 0;
	Field f;
	if (auto e = asn1_field(der, offset, {}, Kind::sequence, "pkcs8", f)) return e;
	std::string_view in = f.inner;
	size_t off = 0;
	Value v;
	if (auto e = asn1_value(in, off, {}, Kind::int64, "int", v)) return e;
	Field algo;
	if (auto e = asn1_field(in, off, {}, Kind::sequence, "AlgorithmIdentifier", algo)) return e;
	if (auto e = asn1_algorithm_identifier(algo.inner, out.algo)) return e;
	if (auto e = asn1_value(in, off, {}, Kind::octets, "", v)) return e;
	out.private_key = v.bytes;
	return std::nullopt;
}

// ecPrivateKey {Version int; PrivateKey []byte; NamedCurveOID `optional,explicit,tag:0`; PublicKey `optional,explicit,tag:1`}.
struct EcPrivateKey {
	std::int64_t version = 0;
	std::string_view private_key;
	Oid curve;
};
std::optional<std::string> unmarshal_ec(std::string_view der, EcPrivateKey &out)
{
	size_t offset = 0;
	Field f;
	if (auto e = asn1_field(der, offset, {}, Kind::sequence, "ecPrivateKey", f)) return e;
	std::string_view in = f.inner;
	size_t off = 0;
	Value v;
	if (auto e = asn1_value(in, off, {}, Kind::int64, "int", v)) return e;
	out.version = v.i;
	if (auto e = asn1_value(in, off, {}, Kind::octets, "", v)) return e;
	out.private_key = v.bytes;
	Params p0;
	p0.optional = p0.explicit_ = true;
	p0.tag = 0;
	if (auto e = asn1_value(in, off, p0, Kind::oid, "ObjectIdentifier", v)) return e;
	out.curve = v.oid;
	Params p1 = p0;
	p1.tag = 1;
	if (auto e = asn1_value(in, off, p1, Kind::bitstring, "BitString", v)) return e;
	return std::nullopt;
}

// pkcs1PrivateKey {Version int; N *big.Int; E int; D, P, Q *big.Int; Dp, Dq, Qinv `optional`;
// AdditionalPrimes []pkcs1AdditionalRSAPrime `optional,omitempty`}.
struct Pkcs1 {
	std::int64_t version = 0, e = 0;
	std::string_view n, d, p, q, dp, dq, qinv;
	bool has_dp = false, has_dq = false, has_qinv = false;
	std::vector<std::string_view> additional;  // primes
	size_t rest = 0;
};
std::optional<std::string> unmarshal_pkcs1(std::string_view der, Pkcs1 &out)
{
	size_t offset = 0;
	Field f;
	if (auto e = asn1_field(der, offset, {}, Kind::sequence, "pkcs1PrivateKey", f)) return e;
	out.rest = der.size() - offset;
	std::string_view in = f.inner;
	size_t off = 0;
	Value v;
	if (auto e = asn1_value(in, off, {}, Kind::int64, "int", v)) return e;
	out.version = v.i;
	if (auto e = asn1_value(in, off, {}, Kind::bigint, "", v)) return e;
	out.n = v.bytes;
	if (auto e = asn1_value(in, off, {}, Kind::int64, "int", v)) return e;
	out.e = v.i;
	std::string_view *mandatory[] = {&out.d, &out.p, &out.q};
	for (auto *m : mandatory) {
		if (auto e = asn1_value(in, off, {}, Kind::bigint, "", v)) return e;
		*m = v.bytes;
	}
	Params optional;
	optional.optional = true;
	struct {
		std::string_view *value;
		bool *has;
	} opts[] = {{&out.dp, &out.has_dp}, {&out.dq, &out.has_dq}, {&out.qinv, &out.has_qinv}};
	for (auto &o : opts) {
		if (auto e = asn1_value(in, off, optional, Kind::bigint, "", v)) return e;
		*o.value = v.bytes;
		*o.has = v.present;
	}
	Params additional;
	additional.optional = additional.omit_empty = true;
	Field seq;
	if (auto e = asn1_field(in, off, additional, Kind::sequence_of, "", seq)) return e;
	if (!seq.present) return std::nullopt;
	// parseSequenceOf: every element a universal SEQUENCE within the bytes, then each parsed.
	std::string_view s = seq.inner;
	size_t count = 0;
	for (size_t o = 0; o < s.size();) {
		TL t;
		if (auto e = parse_tag_length(s, o, t)) return e;
		if (t.cls != 0 || !t.compound || t.tag != 16) return structural("sequence tag mismatch");
		if (invalid_length(o, t.length, s.size())) return syntax("truncated sequence");
		o += static_cast<size_t>(t.length);
		count++;
	}
	size_t o = 0;
	for (size_t i = 0; i < count; i++) {
		Field el;
		if (auto e = asn1_field(s, o, {}, Kind::sequence, "pkcs1AdditionalRSAPrime", el)) return e;
		size_t eo = 0;
		Value prime, ignored;
		if (auto e = asn1_value(el.inner, eo, {}, Kind::bigint, "", prime)) return e;
		if (auto e = asn1_value(el.inner, eo, {}, Kind::bigint, "", ignored)) return e;
		if (auto e = asn1_value(el.inner, eo, {}, Kind::bigint, "", ignored)) return e;
		out.additional.push_back(prime.bytes);
	}
	return std::nullopt;
}

// asn1.Unmarshal into []byte (an OCTET STRING at the top).
std::optional<std::string> unmarshal_octets(std::string_view der, std::string_view &out)
{
	size_t offset = 0;
	Value v;
	if (auto e = asn1_value(der, offset, {}, Kind::octets, "", v)) return e;
	out = v.bytes;
	return std::nullopt;
}

// ---- OIDs ----

const Oid oid_rsa{1, 2, 840, 113549, 1, 1, 1}, oid_dsa{1, 2, 840, 10040, 4, 1}, oid_ecdsa{1, 2, 840, 10045, 2, 1},
	oid_x25519{1, 3, 101, 110}, oid_ed25519{1, 3, 101, 112}, oid_mldsa44{2, 16, 840, 1, 101, 3, 4, 3, 17},
	oid_mldsa65{2, 16, 840, 1, 101, 3, 4, 3, 18}, oid_mldsa87{2, 16, 840, 1, 101, 3, 4, 3, 19},
	oid_p224{1, 3, 132, 0, 33}, oid_p256{1, 2, 840, 10045, 3, 1, 7}, oid_p384{1, 3, 132, 0, 34},
	oid_p521{1, 3, 132, 0, 35}, oid_rsapss{1, 2, 840, 113549, 1, 1, 10}, oid_sha256{2, 16, 840, 1, 101, 3, 4, 2, 1},
	oid_sha384{2, 16, 840, 1, 101, 3, 4, 2, 2}, oid_sha512{2, 16, 840, 1, 101, 3, 4, 2, 3},
	oid_mgf1{1, 2, 840, 113549, 1, 1, 8}, oid_aia{1, 3, 6, 1, 5, 5, 7, 1, 1}, oid_aia_ocsp{1, 3, 6, 1, 5, 5, 7, 48, 1},
	oid_aia_issuers{1, 3, 6, 1, 5, 5, 7, 48, 2};

int curve_of(const Oid &oid)
{
	if (oid == oid_p224) return NID_secp224r1;
	if (oid == oid_p256) return NID_X9_62_prime256v1;
	if (oid == oid_p384) return NID_secp384r1;
	if (oid == oid_p521) return NID_secp521r1;
	return 0;
}
const char *curve_name(int nid)
{
	switch (nid) {
	case NID_secp224r1: return "P-224";
	case NID_X9_62_prime256v1: return "P-256";
	case NID_secp384r1: return "P-384";
	case NID_secp521r1: return "P-521";
	}
	return "";
}
const char *curve_short(int nid)
{
	switch (nid) {
	case NID_secp224r1: return "P224";
	case NID_X9_62_prime256v1: return "P256";
	case NID_secp384r1: return "P384";
	case NID_secp521r1: return "P521";
	}
	return "";
}

// Signature algorithms (x509.SignatureAlgorithm values).
enum Sig {
	UnknownSig = 0, MD2WithRSA, MD5WithRSA, SHA1WithRSA, SHA256WithRSA, SHA384WithRSA, SHA512WithRSA, DSAWithSHA1,
	DSAWithSHA256, ECDSAWithSHA1, ECDSAWithSHA256, ECDSAWithSHA384, ECDSAWithSHA512, SHA256WithRSAPSS, SHA384WithRSAPSS,
	SHA512WithRSAPSS, PureEd25519, MLDSA44, MLDSA65, MLDSA87
};
enum class Hash { none, md5, sha1, sha256, sha384, sha512 };
enum class PkAlgo { unknown, rsa, dsa, ecdsa, ed25519, mldsa };
struct SigDetails {
	Sig algo;
	Oid oid;
	PkAlgo pk;
	Hash hash;
	bool pss;
};
const std::vector<SigDetails> &sig_details()
{
	static const std::vector<SigDetails> table = {
		{MD5WithRSA, {1, 2, 840, 113549, 1, 1, 4}, PkAlgo::rsa, Hash::md5, false},
		{SHA1WithRSA, {1, 2, 840, 113549, 1, 1, 5}, PkAlgo::rsa, Hash::sha1, false},
		{SHA1WithRSA, {1, 3, 14, 3, 2, 29}, PkAlgo::rsa, Hash::sha1, false},
		{SHA256WithRSA, {1, 2, 840, 113549, 1, 1, 11}, PkAlgo::rsa, Hash::sha256, false},
		{SHA384WithRSA, {1, 2, 840, 113549, 1, 1, 12}, PkAlgo::rsa, Hash::sha384, false},
		{SHA512WithRSA, {1, 2, 840, 113549, 1, 1, 13}, PkAlgo::rsa, Hash::sha512, false},
		{SHA256WithRSAPSS, oid_rsapss, PkAlgo::rsa, Hash::sha256, true},
		{SHA384WithRSAPSS, oid_rsapss, PkAlgo::rsa, Hash::sha384, true},
		{SHA512WithRSAPSS, oid_rsapss, PkAlgo::rsa, Hash::sha512, true},
		{DSAWithSHA1, {1, 2, 840, 10040, 4, 3}, PkAlgo::dsa, Hash::sha1, false},
		{DSAWithSHA256, {2, 16, 840, 1, 101, 3, 4, 3, 2}, PkAlgo::dsa, Hash::sha256, false},
		{ECDSAWithSHA1, {1, 2, 840, 10045, 4, 1}, PkAlgo::ecdsa, Hash::sha1, false},
		{ECDSAWithSHA256, {1, 2, 840, 10045, 4, 3, 2}, PkAlgo::ecdsa, Hash::sha256, false},
		{ECDSAWithSHA384, {1, 2, 840, 10045, 4, 3, 3}, PkAlgo::ecdsa, Hash::sha384, false},
		{ECDSAWithSHA512, {1, 2, 840, 10045, 4, 3, 4}, PkAlgo::ecdsa, Hash::sha512, false},
		{PureEd25519, oid_ed25519, PkAlgo::ed25519, Hash::none, false},
		{MLDSA44, oid_mldsa44, PkAlgo::mldsa, Hash::none, false},
		{MLDSA65, oid_mldsa65, PkAlgo::mldsa, Hash::none, false},
		{MLDSA87, oid_mldsa87, PkAlgo::mldsa, Hash::none, false},
	};
	return table;
}

const std::string_view null_bytes("\x05\x00", 2);

// getSignatureAlgorithmFromAI.
Sig signature_algorithm(const AlgorithmIdentifier &ai)
{
	if (ai.algorithm == oid_ed25519 || ai.algorithm == oid_mldsa44 || ai.algorithm == oid_mldsa65 ||
	    ai.algorithm == oid_mldsa87) {
		if (!ai.parameters.empty()) return UnknownSig;
	}
	if (ai.algorithm != oid_rsapss) {
		for (const auto &d : sig_details())
			if (ai.algorithm == d.oid) return d.algo;
		return UnknownSig;
	}
	// pssParameters {Hash AlgorithmIdentifier `explicit,tag:0`; MGF AlgorithmIdentifier `explicit,tag:1`;
	// SaltLength int `explicit,tag:2`; TrailerField int `optional,explicit,tag:3,default:1`}.
	std::string_view der = ai.parameters;
	size_t offset = 0;
	Field f;
	if (asn1_field(der, offset, {}, Kind::sequence, "pssParameters", f)) return UnknownSig;
	std::string_view in = f.inner;
	size_t off = 0;
	Params p;
	p.explicit_ = true;
	AlgorithmIdentifier hash, mgf;
	p.tag = 0;
	Field hf;
	if (asn1_field(in, off, p, Kind::sequence, "AlgorithmIdentifier", hf) || asn1_algorithm_identifier(hf.inner, hash))
		return UnknownSig;
	p.tag = 1;
	Field mf;
	if (asn1_field(in, off, p, Kind::sequence, "AlgorithmIdentifier", mf) || asn1_algorithm_identifier(mf.inner, mgf))
		return UnknownSig;
	p.tag = 2;
	Value salt;
	if (asn1_value(in, off, p, Kind::int64, "int", salt)) return UnknownSig;
	Params trailer;
	trailer.optional = trailer.explicit_ = trailer.has_default = true;
	trailer.tag = 3;
	trailer.default_value = 1;
	Value tf;
	if (asn1_value(in, off, trailer, Kind::int64, "int", tf)) return UnknownSig;
	// mgf1HashFunc from the MGF parameters.
	AlgorithmIdentifier mgf_hash;
	{
		size_t mo = 0;
		Field mh;
		if (asn1_field(mgf.parameters, mo, {}, Kind::sequence, "AlgorithmIdentifier", mh) ||
		    asn1_algorithm_identifier(mh.inner, mgf_hash))
			return UnknownSig;
	}
	if ((!hash.parameters.empty() && hash.parameters != null_bytes) || mgf.algorithm != oid_mgf1 ||
	    mgf_hash.algorithm != hash.algorithm || (!mgf_hash.parameters.empty() && mgf_hash.parameters != null_bytes) ||
	    tf.i != 1)
		return UnknownSig;
	if (hash.algorithm == oid_sha256 && salt.i == 32) return SHA256WithRSAPSS;
	if (hash.algorithm == oid_sha384 && salt.i == 48) return SHA384WithRSAPSS;
	if (hash.algorithm == oid_sha512 && salt.i == 64) return SHA512WithRSAPSS;
	return UnknownSig;
}

PkAlgo public_key_algorithm(const Oid &oid)
{
	if (oid == oid_rsa) return PkAlgo::rsa;
	if (oid == oid_dsa) return PkAlgo::dsa;
	if (oid == oid_ecdsa) return PkAlgo::ecdsa;
	if (oid == oid_ed25519) return PkAlgo::ed25519;
	if (oid == oid_mldsa44 || oid == oid_mldsa65 || oid == oid_mldsa87) return PkAlgo::mldsa;
	return PkAlgo::unknown;
}

// ---- curve arithmetic ----

struct Curve {
	std::unique_ptr<EC_GROUP, GroupFree> group;
	Bn p, order;
	int size = 0;  // field bytes
};
Curve curve(int nid)
{
	Curve c;
	c.group.reset(EC_GROUP_new_by_curve_name(nid));
	c.p.reset(BN_new());
	c.order.reset(BN_new());
	EC_GROUP_get_curve(c.group.get(), c.p.get(), nullptr, nullptr, nullptr);
	EC_GROUP_get_order(c.group.get(), c.order.get(), nullptr);
	c.size = (BN_num_bits(c.p.get()) + 7) / 8;
	return c;
}

// ecdsa.ParseUncompressedPublicKey; "" when valid, else the error.
std::string check_uncompressed_point(int nid, std::string_view data)
{
	if (data.empty() || data[0] != 4) return "ecdsa: invalid uncompressed public key";
	Curve c = curve(nid);
	std::string name = curve_short(nid);
	if (data.size() != 1 + 2 * static_cast<size_t>(c.size)) return "invalid " + name + " point encoding";
	Bn x = bn(data.substr(1, static_cast<size_t>(c.size))), y = bn(data.substr(1 + static_cast<size_t>(c.size)));
	if (BN_cmp(x.get(), c.p.get()) >= 0 || BN_cmp(y.get(), c.p.get()) >= 0) return "invalid " + name + "Element encoding";
	std::unique_ptr<EC_POINT, PointFree> pt(EC_POINT_new(c.group.get()));
	std::unique_ptr<BN_CTX, CtxFree> ctx(BN_CTX_new());
	if (!EC_POINT_set_affine_coordinates(c.group.get(), pt.get(), x.get(), y.get(), ctx.get()) ||
	    EC_POINT_is_on_curve(c.group.get(), pt.get(), ctx.get()) != 1)
		return name + " point not on curve";
	return "";
}

// d*G uncompressed; "" when d is a multiple of the order (the point at infinity).
std::string scalar_base_mult(int nid, std::string_view d)
{
	Curve c = curve(nid);
	std::unique_ptr<EC_POINT, PointFree> pt(EC_POINT_new(c.group.get()));
	std::unique_ptr<BN_CTX, CtxFree> ctx(BN_CTX_new());
	Bn k = bn(d);
	if (!EC_POINT_mul(c.group.get(), pt.get(), k.get(), nullptr, nullptr, ctx.get())) return "";
	if (EC_POINT_is_at_infinity(c.group.get(), pt.get())) return "";
	std::string out(1 + 2 * static_cast<size_t>(c.size), '\0');
	if (EC_POINT_point2oct(c.group.get(), pt.get(), POINT_CONVERSION_UNCOMPRESSED,
	                       reinterpret_cast<unsigned char *>(out.data()), out.size(), ctx.get()) != out.size())
		return "";
	return out;
}

// ---- names ----

enum class AnyKind { string, integer, bitstring, octets, oid, time, boolean, nil, raw };
struct AnyValue {
	AnyKind kind = AnyKind::nil;
	std::string s;   // string; octets; bit string bytes; raw full bytes
	std::int64_t i = 0;
	int bit_length = 0;
	bool b = false;
	Oid oid;
	GoTime time;
};
struct Atv {
	Oid type;
	AnyValue value;
};
using Rdn = std::vector<Atv>;

bool printable(unsigned char b)
{
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || (b >= '\'' && b <= ')') ||
	       (b >= '+' && b <= '/') || b == ' ' || b == ':' || b == '=' || b == '?' || b == '*' || b == '&';
}

void append_utf8(std::string &out, std::uint32_t r)
{
	if (r > 0x10ffff || (r >= 0xd800 && r <= 0xdfff)) r = 0xfffd;
	if (r < 0x80) out += static_cast<char>(r);
	else if (r < 0x800) {
		out += static_cast<char>(0xc0 | r >> 6);
		out += static_cast<char>(0x80 | (r & 0x3f));
	} else if (r < 0x10000) {
		out += static_cast<char>(0xe0 | r >> 12);
		out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (r & 0x3f));
	} else {
		out += static_cast<char>(0xf0 | r >> 18);
		out += static_cast<char>(0x80 | ((r >> 12) & 0x3f));
		out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
		out += static_cast<char>(0x80 | (r & 0x3f));
	}
}

bool valid_utf8(std::string_view s)
{
	for (size_t i = 0; i < s.size();) {
		auto c = static_cast<std::uint8_t>(s[i]);
		if (c < 0x80) {
			i++;
			continue;
		}
		int n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 0;
		if (!n || i + static_cast<size_t>(n) > s.size()) return false;
		std::uint32_t cp = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
		for (int k = 1; k < n; k++) {
			auto cc = static_cast<std::uint8_t>(s[i + static_cast<size_t>(k)]);
			if ((cc & 0xc0) != 0x80) return false;
			cp = cp << 6 | (cc & 0x3f);
		}
		if ((n == 3 && (cp < 0x800 || (cp >= 0xd800 && cp <= 0xdfff))) || (n == 4 && (cp < 0x10000 || cp > 0x10ffff)))
			return false;
		i += static_cast<size_t>(n);
	}
	return true;
}

bool ascii(std::string_view s)
{
	for (char c : s)
		if (static_cast<unsigned char>(c) > 0x7f) return false;
	return true;
}

// parseASN1String.
std::optional<std::string> parse_asn1_string(std::uint8_t t, std::string_view value, std::string &out)
{
	switch (t) {
	case tag::T61String:
		out.clear();
		for (unsigned char c : value) append_utf8(out, c);
		return std::nullopt;
	case tag::PrintableString:
		for (unsigned char c : value)
			if (!printable(c)) return "invalid PrintableString";
		out = std::string(value);
		return std::nullopt;
	case tag::UTF8String:
		if (!valid_utf8(value)) return "invalid UTF-8 string";
		out = std::string(value);
		return std::nullopt;
	case tag::BMPString: {
		if (value.size() % 2) return "invalid BMPString";
		if (value.size() >= 2 && value[value.size() - 1] == 0 && value[value.size() - 2] == 0) value.remove_suffix(2);
		out.clear();
		for (size_t i = 0; i < value.size(); i += 2) {
			std::uint32_t point = static_cast<std::uint32_t>(static_cast<std::uint8_t>(value[i]) << 8) |
			                      static_cast<std::uint8_t>(value[i + 1]);
			if (point == 0xfffe || point == 0xffff || (point >= 0xfdd0 && point <= 0xfdef) ||
			    (point >= 0xd800 && point <= 0xdfff))
				return "invalid BMPString";
			append_utf8(out, point);
		}
		return std::nullopt;
	}
	case tag::IA5String:
		if (!ascii(value)) return "invalid IA5String";
		out = std::string(value);
		return std::nullopt;
	case tag::NumericString:
		for (char c : value)
			if (!((c >= '0' && c <= '9') || c == ' ')) return "invalid NumericString";
		out = std::string(value);
		return std::nullopt;
	}
	return std::format("unsupported string type: {}", t);
}

// readASN1Any.
std::optional<std::string> read_any(Der &der, AnyValue &v)
{
	std::string_view full;
	std::uint8_t t = 0;
	if (!der.read_any(&full, &t, false)) return "invalid ASN.1 element";
	Der f{full};
	switch (t) {
	case tag::T61String:
	case tag::PrintableString:
	case tag::UTF8String:
	case tag::BMPString:
	case tag::IA5String:
	case tag::NumericString: {
		std::string_view raw;
		if (!f.read(raw, t)) return "invalid ASN.1 element";
		v.kind = AnyKind::string;
		return parse_asn1_string(t, raw, v.s);
	}
	case tag::INTEGER:
		if (!f.read_int64(v.i)) return "invalid ASN.1 integer";
		v.kind = AnyKind::integer;
		return std::nullopt;
	case tag::BIT_STRING:
		if (!f.read_bit_string(v.s, v.bit_length)) return "invalid ASN.1 BIT STRING";
		v.kind = AnyKind::bitstring;
		return std::nullopt;
	case tag::OCTET_STRING: {
		std::string_view s;
		if (!f.read(s, tag::OCTET_STRING)) return "invalid ASN.1 OCTET STRING";
		v.kind = AnyKind::octets;
		v.s = std::string(s);
		return std::nullopt;
	}
	case tag::OBJECT_IDENTIFIER:
		if (!f.read_oid(v.oid)) return "invalid ASN.1 OBJECT IDENTIFIER";
		v.kind = AnyKind::oid;
		return std::nullopt;
	case tag::UTCTime:
		if (!f.read_utc_time(v.time)) return "x509: malformed UTCTime";
		v.kind = AnyKind::time;
		return std::nullopt;
	case tag::GeneralizedTime:
		if (!f.read_generalized_time(v.time)) return "x509: malformed GeneralizedTime";
		v.kind = AnyKind::time;
		return std::nullopt;
	case tag::BOOLEAN:
		if (!f.read_bool(v.b)) return "invalid ASN.1 BOOLEAN";
		v.kind = AnyKind::boolean;
		return std::nullopt;
	case tag::NULL_: v.kind = AnyKind::nil; return std::nullopt;
	default: {
		std::string_view unused;
		std::uint8_t ut;
		if (!f.read_any(&unused, &ut, true)) return "invalid ASN.1 element";
		v.kind = AnyKind::raw;
		v.s = std::string(full);
		return std::nullopt;
	}
	}
}

std::optional<std::string> parse_name(std::string_view raw, std::vector<Rdn> &out)
{
	Der d{raw};
	std::string_view seq;
	if (!d.read(seq, tag::SEQUENCE)) return "x509: invalid RDNSequence";
	Der r{seq};
	while (!r.empty()) {
		Rdn rdn;
		std::string_view set;
		if (!r.read(set, tag::SET)) return "x509: invalid RDNSequence";
		Der s{set};
		while (!s.empty()) {
			std::string_view atv;
			if (!s.read(atv, tag::SEQUENCE)) return "x509: invalid RDNSequence: invalid attribute";
			Der a{atv};
			Atv attr;
			if (!a.read_oid(attr.type)) return "x509: invalid RDNSequence: invalid attribute type";
			if (auto e = read_any(a, attr.value)) return "x509: invalid RDNSequence: invalid attribute value: " + *e;
			rdn.push_back(std::move(attr));
		}
		out.push_back(std::move(rdn));
	}
	return std::nullopt;
}

bool standard_attribute(const Oid &t, int &which)
{
	if (t.size() == 4 && t[0] == 2 && t[1] == 5 && t[2] == 4) {
		switch (t[3]) {
		case 3: case 5: case 6: case 7: case 8: case 9: case 10: case 11: case 17: which = t[3]; return true;
		}
	}
	return false;
}

std::string der_length(size_t n)
{
	std::string out;
	if (n < 0x80) {
		out += static_cast<char>(n);
		return out;
	}
	std::string b;
	while (n) {
		b.insert(b.begin(), static_cast<char>(n & 0xff));
		n >>= 8;
	}
	out += static_cast<char>(0x80 | b.size());
	return out + b;
}
std::string der(std::uint8_t t, std::string_view content) { return std::string(1, static_cast<char>(t)) + der_length(content.size()) + std::string(content); }

std::string two_digits(int v) { return std::format("{:02}", v); }

// asn1.Marshal of a value readASN1Any produced; nullopt for nil.
std::optional<std::string> marshal_any(const AnyValue &v)
{
	switch (v.kind) {
	case AnyKind::integer: {
		std::int64_t i = v.i;
		int n = 1;
		for (std::int64_t x = i; x > 127; x >>= 8) n++;
		for (std::int64_t x = i; x < -128; x >>= 8) n++;
		std::string c;
		for (int k = n - 1; k >= 0; k--) c += static_cast<char>((i >> (k * 8)) & 0xff);
		return der(tag::INTEGER, c);
	}
	case AnyKind::bitstring: {
		std::string c(1, static_cast<char>((8 - v.bit_length % 8) % 8));
		return der(tag::BIT_STRING, c + v.s);
	}
	case AnyKind::octets: return der(tag::OCTET_STRING, v.s);
	case AnyKind::oid: {
		std::string c;
		auto put = [&](std::int64_t n) {
			int len = 0;
			if (n == 0) len = 1;
			else
				for (std::int64_t x = n; x > 0; x >>= 7) len++;
			for (int k = len - 1; k >= 0; k--) {
				auto o = static_cast<std::uint8_t>((n >> (k * 7)) & 0x7f);
				if (k) o |= 0x80;
				c += static_cast<char>(o);
			}
		};
		put(static_cast<std::int64_t>(v.oid[0]) * 40 + v.oid[1]);
		for (size_t k = 2; k < v.oid.size(); k++) put(v.oid[k]);
		return der(tag::OBJECT_IDENTIFIER, c);
	}
	case AnyKind::time: {
		const GoTime &t = v.time;
		bool utc = t.year >= 1950 && t.year < 2050;
		std::string c = utc ? two_digits(static_cast<int>(t.year % 100)) : std::format("{:04}", t.year);
		c += two_digits(t.month) + two_digits(t.day) + two_digits(t.hour) + two_digits(t.minute) + two_digits(t.second);
		if (t.offset / 60 == 0) c += 'Z';
		else {
			int minutes = (t.offset < 0 ? -t.offset : t.offset) / 60;
			c += t.offset > 0 ? '+' : '-';
			c += std::format("{:04}", minutes / 60 * 100 + minutes % 60);
		}
		return der(utc ? tag::UTCTime : tag::GeneralizedTime, c);
	}
	case AnyKind::boolean: return der(tag::BOOLEAN, std::string(1, v.b ? '\xff' : '\0'));
	case AnyKind::raw: return v.s;
	case AnyKind::nil:
	case AnyKind::string: break;
	}
	return std::nullopt;
}

// RDNSequence.String.
std::string rdn_string(const std::vector<Rdn> &r)
{
	static const std::map<std::string, std::string> names = {
		{"2.5.4.6", "C"}, {"2.5.4.10", "O"}, {"2.5.4.11", "OU"}, {"2.5.4.3", "CN"}, {"2.5.4.5", "SERIALNUMBER"},
		{"2.5.4.7", "L"}, {"2.5.4.8", "ST"}, {"2.5.4.9", "STREET"}, {"2.5.4.17", "POSTALCODE"}};
	std::string buf;
	for (size_t i = 0; i < r.size(); i++) {
		const Rdn &rdn = r[r.size() - 1 - i];
		if (i > 0) buf += ',';
		for (size_t j = 0; j < rdn.size(); j++) {
			const Atv &tv = rdn[j];
			if (j > 0) buf += '+';
			std::string oid = oid_string(tv.type);
			std::string type_name;
			auto it = names.find(oid);
			if (it == names.end()) {
				if (tv.value.kind != AnyKind::string) {
					if (auto d = marshal_any(tv.value)) {
						buf += oid + "=#" + hex_lower(*d);
						continue;
					}
				}
				type_name = oid;
			} else {
				type_name = it->second;
			}
			std::string value = tv.value.kind == AnyKind::string ? tv.value.s : "<nil>";
			std::string escaped;
			for (size_t k = 0; k < value.size(); k++) {
				char c = value[k];
				bool escape = false;
				switch (c) {
				case ',': case '+': case '"': case '\\': case '<': case '>': case ';': escape = true; break;
				case ' ': escape = k == 0 || k == value.size() - 1; break;
				case '#': escape = k == 0; break;
				}
				if (escape) escaped += '\\';
				escaped += c;
			}
			buf += type_name + "=" + escaped;
		}
	}
	return buf;
}

// pkix.Name filled from the sequence, then Name.String and the CommonName.
void describe_name(const std::vector<Rdn> &rdns, std::string &text, std::string &cn)
{
	std::vector<Atv> names;
	std::map<int, std::vector<std::string>> fields;
	std::string common, serial;
	for (const auto &rdn : rdns) {
		for (const auto &atv : rdn) {
			names.push_back(atv);
			if (atv.value.kind != AnyKind::string) continue;
			int which;
			if (!standard_attribute(atv.type, which)) continue;
			if (which == 3) common = atv.value.s;
			else if (which == 5) serial = atv.value.s;
			else fields[which].push_back(atv.value.s);
		}
	}
	std::vector<Rdn> seq;
	for (const auto &atv : names) {
		int which;
		if (standard_attribute(atv.type, which)) continue;
		seq.push_back({atv});
	}
	auto append = [&](const std::vector<std::string> &values, int which) {
		if (values.empty()) return;
		Rdn s;
		for (const auto &v : values) {
			Atv a;
			a.type = {2, 5, 4, which};
			a.value.kind = AnyKind::string;
			a.value.s = v;
			s.push_back(std::move(a));
		}
		seq.push_back(std::move(s));
	};
	append(fields[6], 6);
	append(fields[8], 8);
	append(fields[7], 7);
	append(fields[9], 9);
	append(fields[17], 17);
	append(fields[10], 10);
	append(fields[11], 11);
	if (!common.empty()) append({common}, 3);
	if (!serial.empty()) append({serial}, 5);
	text = rdn_string(seq);
	cn = common;
}

// ---- SANs and extensions ----

bool domain_name_valid(std::string_view s, bool constraint)
{
	if (s.empty()) return true;
	if (s.back() == '.') return false;
	if (constraint && s[0] == '.') s.remove_prefix(1);
	long last_dot = -1;
	for (size_t i = 0; i <= s.size(); i++) {
		if (i < s.size() && (static_cast<unsigned char>(s[i]) < 33 || static_cast<unsigned char>(s[i]) > 126)) return false;
		if (i == s.size() || s[i] == '.') {
			long label = static_cast<long>(i);
			if (last_dot >= 0) label -= last_dot + 1;
			if (label == 0) return false;
			last_dot = static_cast<long>(i);
		}
	}
	return true;
}

bool parse_ip(std::string_view s)
{
	std::string z(s);
	unsigned char buf[16];
	return inet_pton(AF_INET, z.c_str(), buf) == 1 || inet_pton(AF_INET6, z.c_str(), buf) == 1;
}

bool v4_mapped(std::string_view ip)
{
	if (ip.size() != 16) return false;
	for (int i = 0; i < 10; i++)
		if (ip[static_cast<size_t>(i)] != 0) return false;
	return static_cast<std::uint8_t>(ip[10]) == 0xff && static_cast<std::uint8_t>(ip[11]) == 0xff;
}

// The host of a URI as net/url finds it (scheme "//" authority), for the domain check.
std::optional<std::string> uri_error(const std::string &uri)
{
	for (unsigned char c : uri)
		if (c < 0x20 || c == 0x7f)
			return std::format("x509: cannot parse URI {}: parse {}: net/url: invalid control character in URL",
			                   go_quote(uri), go_quote(uri));
	// url.URL.Host: the authority after "scheme://" without the user information (the port included).
	std::string_view u = uri;
	if (auto hash = u.find('#'); hash != std::string_view::npos) u = u.substr(0, hash);
	auto colon = u.find(':');
	if (colon == std::string_view::npos || colon == 0) return std::nullopt;
	std::string_view rest = u.substr(colon + 1);
	if (rest.substr(0, 2) != "//") return std::nullopt;
	rest.remove_prefix(2);
	std::string_view host = rest.substr(0, rest.find_first_of("/?"));
	if (auto at = host.rfind('@'); at != std::string_view::npos) host.remove_prefix(at + 1);
	if (!host.empty() && !domain_name_valid(host, false))
		return std::format("x509: cannot parse URI {}: invalid domain", go_quote(uri));
	return std::nullopt;
}

std::optional<std::string> parse_san(std::string_view value, Certificate &out, bool &unhandled)
{
	Der d{value};
	std::string_view seq;
	if (!d.read(seq, tag::SEQUENCE)) return "x509: invalid subject alternative names";
	Der s{seq};
	size_t names = 0;
	while (!s.empty()) {
		std::string_view san;
		std::uint8_t t;
		if (!s.read_any(&san, &t, true)) return "x509: invalid subject alternative name";
		int kind = t ^ 0x80;
		switch (kind) {
		case 1:
			if (!ascii(san)) return "x509: SAN rfc822Name is malformed";
			names++;
			break;
		case 2:
			if (!ascii(san)) return "x509: SAN dNSName is malformed";
			out.dns_names.emplace_back(san);
			names++;
			break;
		case 6:
			if (!ascii(san)) return "x509: SAN uniformResourceIdentifier is malformed";
			if (auto e = uri_error(std::string(san))) return e;
			names++;
			break;
		case 7:
			if (san.size() == 16) {
				if (v4_mapped(san)) return "x509: SAN iPAddress contains IPv4-mapped IPv6 address";
			} else if (san.size() != 4) {
				return "x509: cannot parse IP address of length " + std::to_string(san.size());
			}
			out.ip_addresses.emplace_back(san);
			names++;
			break;
		}
	}
	if (!names) unhandled = true;
	return std::nullopt;
}

bool valid_ip_mask(std::string_view mask)
{
	bool zero = false;
	for (unsigned char b : mask) {
		if (zero) {
			if (b) return false;
			continue;
		}
		switch (b) {
		case 0x00: case 0x80: case 0xc0: case 0xe0: case 0xf0: case 0xf8: case 0xfc: case 0xfe: zero = true; break;
		case 0xff: break;
		default: return false;
		}
	}
	return true;
}

std::optional<std::string> parse_name_constraints(std::string_view value, bool &unhandled)
{
	Der outer{value};
	std::string_view top;
	if (!outer.read(top, tag::SEQUENCE) || !outer.empty()) return "x509: invalid NameConstraints extension";
	Der t{top};
	std::string_view permitted, excluded;
	bool have_permitted = false, have_excluded = false;
	if (!t.read_optional(permitted, have_permitted, tag::constructed(tag::context(0))) ||
	    !t.read_optional(excluded, have_excluded, tag::constructed(tag::context(1))) || !t.empty())
		return "x509: invalid NameConstraints extension";
	if ((!have_permitted && !have_excluded) || (permitted.empty() && excluded.empty()))
		return "x509: empty name constraints extension";
	auto values = [&](std::string_view subtrees) -> std::optional<std::string> {
		Der st{subtrees};
		while (!st.empty()) {
			std::string_view seq, v;
			std::uint8_t tg;
			if (!st.read(seq, tag::SEQUENCE)) return "x509: invalid NameConstraints extension";
			Der sd{seq};
			if (!sd.read_any(&v, &tg, true)) return "x509: invalid NameConstraints extension";
			std::string s(v);
			if (tg == tag::context(2)) {
				if (!ascii(s)) return std::format("x509: invalid constraint value: x509: {} cannot be encoded as an IA5String", go_quote(s));
				if (!domain_name_valid(s, true)) return std::format("x509: failed to parse dnsName constraint {}", go_quote(s));
			} else if (tg == tag::context(7)) {
				std::string_view ip, mask;
				if (v.size() == 8) ip = v.substr(0, 4), mask = v.substr(4);
				else if (v.size() == 32) ip = v.substr(0, 16), mask = v.substr(16);
				else return std::format("x509: IP constraint contained value of length {}", v.size());
				if (!valid_ip_mask(mask)) return std::format("x509: IP constraint contained invalid mask {}", hex_lower(mask));
				if (v4_mapped(ip)) return "x509: IP constraint contained IPv4-mapped IPv6 address";
			} else if (tg == tag::context(1)) {
				if (!ascii(s)) return std::format("x509: invalid constraint value: x509: {} cannot be encoded as an IA5String", go_quote(s));
				if (s.find('@') == std::string::npos && !domain_name_valid(s, true))
					return std::format("x509: failed to parse rfc822Name constraint {}", go_quote(s));
			} else if (tg == tag::context(6)) {
				if (!ascii(s)) return std::format("x509: invalid constraint value: x509: {} cannot be encoded as an IA5String", go_quote(s));
				if (parse_ip(s)) return std::format("x509: failed to parse URI constraint {}: cannot be IP address", go_quote(s));
				if (!domain_name_valid(s, true)) return std::format("x509: failed to parse URI constraint {}", go_quote(s));
			} else {
				unhandled = true;
			}
		}
		return std::nullopt;
	};
	if (auto e = values(permitted)) return e;
	return values(excluded);
}

bool oid_der_valid(std::string_view der)
{
	if (der.empty() || (static_cast<std::uint8_t>(der.back()) & 0x80)) return false;
	bool start = true;
	for (unsigned char c : der) {
		if (start && c == 0x80) return false;
		start = !(c & 0x80);
	}
	return true;
}

std::optional<std::string> process_extension(const Oid &id, bool critical, std::string_view value, Certificate &out)
{
	bool unhandled = false;
	if (id.size() == 4 && id[0] == 2 && id[1] == 5 && id[2] == 29) {
		Der v{value};
		switch (id[3]) {
		case 15: {
			std::string bits;
			int len;
			if (!v.read_bit_string(bits, len)) return "x509: invalid key usage";
			break;
		}
		case 19: {
			std::string_view seq;
			if (!v.read(seq, tag::SEQUENCE)) return "x509: invalid basic constraints";
			Der s{seq};
			bool ca;
			if (s.peek(tag::BOOLEAN) && !s.read_bool(ca)) return "x509: invalid basic constraints";
			if (s.peek(tag::INTEGER)) {
				std::uint64_t mpl;
				if (!s.read_uint64(mpl) || mpl > static_cast<std::uint64_t>(INT64_MAX)) return "x509: invalid basic constraints";
			}
			break;
		}
		case 17:
			if (auto e = parse_san(value, out, unhandled)) return e;
			break;
		case 30:
			if (auto e = parse_name_constraints(value, unhandled)) return e;
			break;
		case 31: {
			std::string_view seq;
			if (!v.read(seq, tag::SEQUENCE)) return "x509: invalid CRL distribution points";
			Der s{seq};
			while (!s.empty()) {
				std::string_view dp;
				if (!s.read(dp, tag::SEQUENCE)) return "x509: invalid CRL distribution point";
				Der d{dp};
				std::string_view name;
				bool present;
				if (!d.read_optional(name, present, tag::constructed(tag::context(0)))) return "x509: invalid CRL distribution point";
				if (!present) continue;
				Der n{name};
				std::string_view full;
				if (!n.read(full, tag::constructed(tag::context(0)))) return "x509: invalid CRL distribution point";
				Der f{full};
				while (!f.empty()) {
					if (!f.peek(tag::context(6))) break;
					std::string_view uri;
					if (!f.read(uri, tag::context(6))) return "x509: invalid CRL distribution point";
				}
			}
			break;
		}
		case 35: {
			if (critical) return "x509: authority key identifier incorrectly marked critical";
			std::string_view akid;
			if (!v.read(akid, tag::SEQUENCE)) return "x509: invalid authority key identifier";
			Der a{akid};
			if (a.peek(tag::context(0))) {
				std::string_view id0;
				if (!a.read(id0, tag::context(0))) return "x509: invalid authority key identifier";
			}
			break;
		}
		case 36: {
			std::string_view seq;
			if (!v.read(seq, tag::SEQUENCE)) return "x509: invalid policy constraints extension";
			Der s{seq};
			for (int n = 0; n < 2; n++) {
				if (!s.peek(tag::context(n))) continue;
				std::string_view b;
				if (!s.read(b, tag::context(n)) || !Der::check_integer(b) || b.size() > 8)
					return "x509: invalid policy constraints extension";
			}
			break;
		}
		case 37: {
			std::string_view seq;
			if (!v.read(seq, tag::SEQUENCE)) return "x509: invalid extended key usages";
			Der s{seq};
			while (!s.empty()) {
				Oid eku;
				if (!s.read_oid(eku)) return "x509: invalid extended key usages";
			}
			break;
		}
		case 14: {
			if (critical) return "x509: subject key identifier incorrectly marked critical";
			std::string_view skid;
			if (!v.read(skid, tag::OCTET_STRING)) return "x509: invalid subject key identifier";
			break;
		}
		case 32: {
			std::string_view seq;
			if (!v.read(seq, tag::SEQUENCE)) return "x509: invalid certificate policies";
			Der s{seq};
			std::set<std::string> seen;
			while (!s.empty()) {
				std::string_view cp, oid;
				if (!s.read(cp, tag::SEQUENCE)) return "x509: invalid certificate policies";
				Der c{cp};
				if (!c.read(oid, tag::OBJECT_IDENTIFIER)) return "x509: invalid certificate policies";
				if (!seen.insert(std::string(oid)).second) return "x509: invalid certificate policies";
				if (!oid_der_valid(oid)) return "x509: invalid certificate policies";
			}
			break;
		}
		case 33: {
			std::string_view seq;
			if (!v.read(seq, tag::SEQUENCE)) return "x509: invalid policy mappings extension";
			Der s{seq};
			while (!s.empty()) {
				std::string_view m, a, b;
				if (!s.read(m, tag::SEQUENCE)) return "x509: invalid policy mappings extension";
				Der md{m};
				if (!md.read(a, tag::OBJECT_IDENTIFIER) || !md.read(b, tag::OBJECT_IDENTIFIER))
					return "x509: invalid policy mappings extension";
			}
			break;
		}
		case 54: {
			std::string_view b;
			Der copy = v;
			if (!copy.read(b, tag::INTEGER) || !Der::check_integer(b)) return "x509: invalid inhibit any policy extension";
			// ReadASN1Integer into an int: 64 bits, signed.
			if (b.size() > 8) return "x509: invalid inhibit any policy extension";
			break;
		}
		default: unhandled = true;
		}
	} else if (id == oid_aia) {
		if (critical) return "x509: authority info access incorrectly marked critical";
		Der v{value};
		std::string_view seq;
		if (!v.read(seq, tag::SEQUENCE)) return "x509: invalid authority info access";
		Der s{seq};
		while (!s.empty()) {
			std::string_view aia;
			if (!s.read(aia, tag::SEQUENCE)) return "x509: invalid authority info access";
			Der a{aia};
			Oid method;
			if (!a.read_oid(method)) return "x509: invalid authority info access";
			if (!a.peek(tag::context(6))) continue;
			std::string_view uri;
			if (!a.read(uri, tag::context(6))) return "x509: invalid authority info access";
		}
	} else {
		unhandled = true;
	}
	(void)unhandled;
	return std::nullopt;
}

std::optional<std::string> parse_public_key(PkAlgo algo, const AlgorithmIdentifier &ai, const std::string &data, PublicKey &out)
{
	switch (algo) {
	case PkAlgo::rsa: {
		if (ai.parameters != null_bytes) return "x509: RSA key missing NULL parameters";
		Der d{data};
		std::string_view seq;
		if (!d.read(seq, tag::SEQUENCE)) return "x509: invalid RSA public key";
		Der s{seq};
		std::string_view n;
		std::int64_t e;
		if (!s.read_big(n)) return "x509: invalid RSA modulus";
		if (!s.read_int64(e)) return "x509: invalid RSA public exponent";
		if (!big_positive(n)) return "x509: RSA modulus is not a positive number";
		if (e <= 0) return "x509: RSA public exponent is not a positive number";
		out.kind = KeyKind::rsa;
		out.n = big_magnitude(n);
		out.e = e;
		return std::nullopt;
	}
	case PkAlgo::ecdsa: {
		Der p{ai.parameters};
		Oid named;
		if (!p.read_oid(named)) return "x509: invalid ECDSA parameters";
		int nid = curve_of(named);
		if (!nid) return "x509: unsupported elliptic curve";
		if (auto e = check_uncompressed_point(nid, data); !e.empty()) return e;
		out.kind = KeyKind::ecdsa;
		out.curve = nid;
		out.point = data;
		return std::nullopt;
	}
	case PkAlgo::ed25519:
		if (!ai.parameters.empty()) return "x509: Ed25519 key encoded with illegal parameters";
		if (data.size() != 32) return "x509: wrong Ed25519 public key size";
		out.kind = KeyKind::ed25519;
		out.point = data;
		return std::nullopt;
	case PkAlgo::mldsa:
		// ML-DSA keys are not used on the device: kept as an unusable key type.
		if (!ai.parameters.empty()) return "x509: ML-DSA key encoded with illegal parameters";
		if (data.size() != (ai.algorithm == oid_mldsa44 ? 1312u : ai.algorithm == oid_mldsa65 ? 1952u : 2592u))
			return "mldsa: invalid public key length";
		out.kind = KeyKind::mldsa;
		out.point = data;
		return std::nullopt;
	case PkAlgo::dsa: {
		Der d{data};
		std::string_view y;
		if (!d.read_big(y)) return "x509: invalid DSA public key";
		Der p{ai.parameters};
		std::string_view seq, pp, q, g;
		if (!p.read(seq, tag::SEQUENCE)) return "x509: invalid DSA parameters";
		Der s{seq};
		if (!s.read_big(pp) || !s.read_big(q) || !s.read_big(g)) return "x509: invalid DSA parameters";
		if (!big_positive(y) || !big_positive(pp) || !big_positive(q) || !big_positive(g))
			return "x509: zero or negative DSA parameter";
		out.kind = KeyKind::dsa;
		return std::nullopt;
	}
	case PkAlgo::unknown: break;
	}
	return "x509: unknown public key algorithm";
}

// ---- signatures ----

const EVP_MD *md_of(Hash h)
{
	switch (h) {
	case Hash::md5: return EVP_md5();
	case Hash::sha1: return EVP_sha1();
	case Hash::sha256: return EVP_sha256();
	case Hash::sha384: return EVP_sha384();
	case Hash::sha512: return EVP_sha512();
	case Hash::none: break;
	}
	return nullptr;
}

// Certificate.CheckSignature(algo, RawTBSCertificate, Signature) == nil.
bool check_signature(const Certificate &c)
{
	Hash hash = Hash::none;
	PkAlgo pk = PkAlgo::unknown;
	bool pss = false;
	for (const auto &d : sig_details())
		if (d.algo == c.signature_algorithm) {
			hash = d.hash;
			pk = d.pk;
			pss = d.pss;
			break;
		}
	if (hash == Hash::none && pk != PkAlgo::ed25519 && pk != PkAlgo::mldsa) return false;
	if (hash == Hash::md5) return false;
	switch (c.key.kind) {
	case KeyKind::rsa: {
		if (pk != PkAlgo::rsa) return false;
		Bn n = bn(c.key.n);
		if (BN_num_bits(n.get()) < 1024 || !BN_is_odd(n.get())) return false;
		if (c.key.e < 2 || !(c.key.e & 1) || c.key.e > 0x7fffffff) return false;
		break;
	}
	case KeyKind::ecdsa:
		if (pk != PkAlgo::ecdsa) return false;
		break;
	case KeyKind::ed25519:
		if (pk != PkAlgo::ed25519) return false;
		break;
	default: return false;
	}
	const unsigned char *p = reinterpret_cast<const unsigned char *>(c.raw_spki.data());
	Pkey key(d2i_PUBKEY(nullptr, &p, static_cast<long>(c.raw_spki.size())));
	if (!key) return false;
	std::unique_ptr<EVP_MD_CTX, decltype(&EVP_MD_CTX_free)> ctx(EVP_MD_CTX_new(), EVP_MD_CTX_free);
	EVP_PKEY_CTX *pctx = nullptr;
	if (EVP_DigestVerifyInit(ctx.get(), &pctx, md_of(hash), nullptr, key.get()) != 1) return false;
	if (pss) {
		if (EVP_PKEY_CTX_set_rsa_padding(pctx, RSA_PKCS1_PSS_PADDING) != 1 ||
		    EVP_PKEY_CTX_set_rsa_pss_saltlen(pctx, RSA_PSS_SALTLEN_DIGEST) != 1 ||
		    EVP_PKEY_CTX_set_rsa_mgf1_md(pctx, md_of(hash)) != 1)
			return false;
	} else if (c.key.kind == KeyKind::rsa) {
		if (EVP_PKEY_CTX_set_rsa_padding(pctx, RSA_PKCS1_PADDING) != 1) return false;
	}
	return EVP_DigestVerify(ctx.get(), reinterpret_cast<const unsigned char *>(c.signature.data()), c.signature.size(),
	                        reinterpret_cast<const unsigned char *>(c.raw_tbs.data()), c.raw_tbs.size()) == 1;
}

// ---- RSA private key checks (crypto/rsa Precompute/Validate, crypto/internal/fips140/rsa) ----

std::optional<std::string> check_rsa_public(const BIGNUM *n, std::int64_t e)
{
	if (!BN_is_odd(n)) return "crypto/rsa: public modulus is even";
	if (e < 2) return "crypto/rsa: public exponent too small or negative";
	if (!(e & 1)) return "crypto/rsa: public exponent is even";
	if (e > 0x7fffffff) return "crypto/rsa: public exponent too large";
	return std::nullopt;
}

std::optional<std::string> check_rsa_private(Pkcs1 &k, PrivateKey &out)
{
	std::unique_ptr<BN_CTX, CtxFree> ctx(BN_CTX_new());
	Bn n = bn(big_magnitude(k.n)), d = bn(big_magnitude(k.d)), p = bn(big_magnitude(k.p)), q = bn(big_magnitude(k.q));
	Bn one = bn_int(1);
	auto modulus = [&](const BIGNUM *m) { return BN_cmp(m, one.get()) > 0; };
	if (!k.additional.empty()) {
		// A key with more than two primes cannot be handed to the listener (serving_key_pem): it is refused here,
		// before anything is stored, rather than stored and then not served.
		return "multi-prime RSA keys are not supported";
	}
	bool precomputed = k.has_dp && k.has_dq && k.has_qinv;
	Bn dp, dq, qinv;
	if (!modulus(n.get()) || !modulus(p.get()) || !modulus(q.get())) return "modulus must be > 1";
	if (BN_cmp(d.get(), n.get()) >= 0) return "input overflows the modulus";
	Bn pm1(BN_dup(p.get())), qm1(BN_dup(q.get()));
	BN_sub_word(pm1.get(), 1);
	BN_sub_word(qm1.get(), 1);
	if (precomputed) {
		dp = bn(big_magnitude(k.dp));
		dq = bn(big_magnitude(k.dq));
		qinv = bn(big_magnitude(k.qinv));
		if (BN_cmp(qinv.get(), p.get()) >= 0) return "input overflows the modulus";
	} else {
		if (!modulus(pm1.get()) || !modulus(qm1.get())) return "modulus must be > 1";
		dp.reset(BN_new());
		dq.reset(BN_new());
		BN_mod(dp.get(), d.get(), pm1.get(), ctx.get());
		BN_mod(dq.get(), d.get(), qm1.get(), ctx.get());
		if (!BN_is_odd(p.get())) return "crypto/rsa: p is even";
		// qInv = q^(p-2) mod p.
		Bn pm2(BN_dup(p.get())), qm(BN_new());
		BN_sub_word(pm2.get(), 2);
		BN_mod(qm.get(), q.get(), p.get(), ctx.get());
		qinv.reset(BN_new());
		BN_mod_exp(qinv.get(), qm.get(), pm2.get(), p.get(), ctx.get());
	}
	if (auto e = check_rsa_public(n.get(), k.e)) return e;
	if (BN_cmp(p.get(), n.get()) >= 0 || BN_cmp(q.get(), n.get()) >= 0) return "crypto/rsa: invalid prime";
	Bn t(BN_new());
	BN_mod_mul(t.get(), p.get(), q.get(), n.get(), ctx.get());
	if (!BN_is_zero(t.get())) return "crypto/rsa: p * q != n";
	if (!modulus(pm1.get())) return "crypto/rsa: invalid prime";
	if (BN_cmp(dp.get(), pm1.get()) >= 0) return "crypto/rsa: invalid CRT exponent";
	Bn e = bn_int(static_cast<std::uint64_t>(k.e));
	BN_mod_mul(t.get(), e.get(), dp.get(), pm1.get(), ctx.get());
	if (!BN_is_one(t.get())) return "crypto/rsa: invalid CRT exponent";
	if (!modulus(qm1.get())) return "crypto/rsa: invalid prime";
	if (BN_cmp(dq.get(), qm1.get()) >= 0) return "crypto/rsa: invalid CRT exponent";
	BN_mod_mul(t.get(), e.get(), dq.get(), qm1.get(), ctx.get());
	if (!BN_is_one(t.get())) return "crypto/rsa: invalid CRT exponent";
	BN_mod_mul(t.get(), q.get(), qinv.get(), p.get(), ctx.get());
	if (!BN_is_one(t.get())) return "crypto/rsa: invalid CRT coefficient";
	BN_mod(t.get(), d.get(), pm1.get(), ctx.get());
	if (BN_cmp(t.get(), dp.get()) != 0) return "crypto/rsa: d does not match dP";
	BN_mod(t.get(), d.get(), qm1.get(), ctx.get());
	if (BN_cmp(t.get(), dq.get()) != 0) return "crypto/rsa: d does not match dQ";
	if (BN_cmp(p.get(), q.get()) == 0) return "crypto/rsa: p == q";
	BN_sub(t.get(), p.get(), q.get());
	BN_set_negative(t.get(), 0);
	int half = BN_num_bits(n.get()) / 2;
	if (BN_num_bits(t.get()) <= half - 100) return "crypto/rsa: |p - q| too small";
	if (BN_num_bits(d.get()) <= half) return "crypto/rsa: d too small";
	out.dp = bn_bytes(dp.get());
	out.dq = bn_bytes(dq.get());
	out.qinv = bn_bytes(qinv.get());
	return std::nullopt;
}

const char *const hint_ec = "x509: failed to parse private key (use ParseECPrivateKey instead for this key format)";
const char *const hint_pkcs1 = "x509: failed to parse private key (use ParsePKCS1PrivateKey instead for this key format)";
const char *const hint_pkcs8 = "x509: failed to parse private key (use ParsePKCS8PrivateKey instead for this key format)";

// x509.ParsePKCS1PrivateKey.
std::expected<PrivateKey, std::string> parse_pkcs1(std::string_view der)
{
	Pkcs1 k;
	auto err = unmarshal_pkcs1(der, k);
	if (!err && k.rest > 0) return std::unexpected(syntax("trailing data"));
	if (err) {
		EcPrivateKey ec;
		if (!unmarshal_ec(der, ec)) return std::unexpected(hint_ec);
		Pkcs8 p8;
		if (!unmarshal_pkcs8(der, p8)) return std::unexpected(hint_pkcs8);
		return std::unexpected(*err);
	}
	if (k.version > 1) return std::unexpected("x509: unsupported private key version");
	if (!big_positive(k.n) || !big_positive(k.d) || !big_positive(k.p) || !big_positive(k.q) ||
	    (k.has_dp && !big_positive(k.dp)) || (k.has_dq && !big_positive(k.dq)) || (k.has_qinv && !big_positive(k.qinv)))
		return std::unexpected("x509: private key contains zero or negative value");
	for (auto a : k.additional)
		if (!big_positive(a)) return std::unexpected("x509: private key contains zero or negative prime");
	PrivateKey key;
	if (auto e = check_rsa_private(k, key)) return std::unexpected(*e);
	key.kind = KeyKind::rsa;
	key.pub.kind = KeyKind::rsa;
	key.pub.n = big_magnitude(k.n);
	key.pub.e = k.e;
	key.d = big_magnitude(k.d);
	key.p = big_magnitude(k.p);
	key.q = big_magnitude(k.q);
	return key;
}

// x509.parseECPrivateKey.
std::expected<PrivateKey, std::string> parse_ec(const Oid *named, std::string_view der)
{
	EcPrivateKey ec;
	if (auto e = unmarshal_ec(der, ec)) {
		Pkcs8 p8;
		if (!unmarshal_pkcs8(der, p8)) return std::unexpected(hint_pkcs8);
		Pkcs1 k;
		if (!unmarshal_pkcs1(der, k)) return std::unexpected(hint_pkcs1);
		return std::unexpected("x509: failed to parse EC private key: " + *e);
	}
	if (ec.version != 1) return std::unexpected(std::format("x509: unknown EC private key version {}", ec.version));
	int nid = curve_of(named ? *named : ec.curve);
	if (!nid) return std::unexpected("x509: unknown elliptic curve");
	Curve c = curve(nid);
	size_t size = static_cast<size_t>((BN_num_bits(c.order.get()) + 7) / 8);
	std::string_view pk = ec.private_key;
	while (pk.size() > size) {
		if (pk[0] != 0) return std::unexpected("x509: invalid private key length");
		pk.remove_prefix(1);
	}
	std::string d(size - pk.size(), '\0');
	d += pk;
	std::string q = scalar_base_mult(nid, d);
	if (q.empty()) return std::unexpected("ecdsa: invalid public key encoding");
	if (BN_cmp(bn(d).get(), c.order.get()) >= 0) return std::unexpected("input overflows the modulus");
	PrivateKey key;
	key.kind = KeyKind::ecdsa;
	key.pub.kind = KeyKind::ecdsa;
	key.pub.curve = nid;
	key.pub.point = q;
	key.d = d;
	return key;
}

// x509.ParsePKCS8PrivateKey.
std::expected<PrivateKey, std::string> parse_pkcs8(std::string_view der)
{
	Pkcs8 p8;
	if (auto e = unmarshal_pkcs8(der, p8)) {
		EcPrivateKey ec;
		if (!unmarshal_ec(der, ec)) return std::unexpected(hint_ec);
		Pkcs1 k;
		if (!unmarshal_pkcs1(der, k)) return std::unexpected(hint_pkcs1);
		return std::unexpected(*e);
	}
	const Oid &algo = p8.algo.algorithm;
	if (algo == oid_rsa) {
		auto key = parse_pkcs1(p8.private_key);
		if (!key) return std::unexpected("x509: failed to parse RSA private key embedded in PKCS#8: " + key.error());
		return key;
	}
	if (algo == oid_ecdsa) {
		Oid named;
		std::string_view params = p8.algo.parameters;
		size_t off = 0;
		Value v;
		bool have = !asn1_value(params, off, {}, Kind::oid, "ObjectIdentifier", v);
		if (have) named = v.oid;
		auto key = parse_ec(have ? &named : nullptr, p8.private_key);
		if (!key) return std::unexpected("x509: failed to parse EC private key embedded in PKCS#8: " + key.error());
		return key;
	}
	if (algo == oid_ed25519 || algo == oid_x25519) {
		const char *name = algo == oid_ed25519 ? "Ed25519" : "X25519";
		if (!p8.algo.parameters.empty()) return std::unexpected(std::format("x509: invalid {} private key parameters", name));
		std::string_view seed;
		if (auto e = unmarshal_octets(p8.private_key, seed))
			return std::unexpected(std::format("x509: invalid {} private key: {}", name, *e));
		PrivateKey key;
		if (algo == oid_x25519) {
			if (seed.size() != 32) return std::unexpected("crypto/ecdh: invalid private key size");
			key.kind = KeyKind::x25519;
			return key;
		}
		if (seed.size() != 32) return std::unexpected(std::format("x509: invalid Ed25519 private key length: {}", seed.size()));
		Pkey k(EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, nullptr, reinterpret_cast<const unsigned char *>(seed.data()), 32));
		std::string pub(32, '\0');
		size_t len = 32;
		if (!k || EVP_PKEY_get_raw_public_key(k.get(), reinterpret_cast<unsigned char *>(pub.data()), &len) != 1)
			return std::unexpected("x509: invalid Ed25519 private key");
		key.kind = KeyKind::ed25519;
		key.pub.kind = KeyKind::ed25519;
		key.pub.point = pub;
		key.d = std::string(seed);
		return key;
	}
	if (algo == oid_mldsa44 || algo == oid_mldsa65 || algo == oid_mldsa87) {
		if (!p8.algo.parameters.empty()) return std::unexpected("x509: invalid ML-DSA private key parameters");
		std::string_view pk = p8.private_key;
		if (pk.empty()) return std::unexpected("x509: invalid ML-DSA private key length: 0");
		switch (static_cast<std::uint8_t>(pk[0])) {
		case 0x80: break;
		case 0x04: return std::unexpected("x509: semi-expanded ML-DSA private keys without seed are not supported");
		case 0x30:
			return std::unexpected("x509: ML-DSA private keys with both seed and expanded key are not supported, use e.g. "
			                       "\"openssl pkey -provparam ml-dsa.output_formats=seed-only\" to convert to a seed-only key");
		default: return std::unexpected(std::format("x509: invalid ML-DSA private key: invalid ASN.1 tag {:02x}", static_cast<std::uint8_t>(pk[0])));
		}
		if (pk.size() != 34) return std::unexpected(std::format("x509: invalid ML-DSA private key length: {}", pk.size()));
		if (pk[1] != 32) return std::unexpected("x509: invalid ML-DSA private key ASN.1 encoding");
		PrivateKey key;
		key.kind = KeyKind::mldsa;
		return key;
	}
	return std::unexpected("x509: PKCS#8 wrapping contained private key with unknown algorithm: " + oid_string(algo));
}

bool same_public(const PublicKey &a, const PublicKey &b)
{
	if (a.kind != b.kind) return false;
	switch (a.kind) {
	case KeyKind::rsa: return a.n == b.n && a.e == b.e;
	case KeyKind::ecdsa: return a.curve == b.curve && a.point == b.point;
	case KeyKind::ed25519: return a.point == b.point;
	default: return false;
	}
}

std::string mode_error(std::string_view op, const std::string &path) { return path_error(op, path, errno); }

} // namespace

// ---- public API ----

Instant now()
{
	timespec ts{};
	clock_gettime(CLOCK_REALTIME, &ts);
	return {ts.tv_sec, ts.tv_nsec};
}

std::string errno_text(int err)
{
	if (err > 0 && err < static_cast<int>(std::size(errno_texts)) && errno_texts[err]) return errno_texts[err];
	return "errno " + std::to_string(err);
}

std::string path_error(std::string_view op, const std::string &path, int err)
{
	return std::string(op) + " " + path + ": " + errno_text(err);
}

std::optional<std::string> read_file(const std::string &path, std::string &error)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) {
		error = mode_error("open", path);
		return std::nullopt;
	}
	std::string data;
	char buf[8192];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0) {
			if (errno == EINTR) continue;
			error = mode_error("read", path);
			::close(fd);
			return std::nullopt;
		}
		if (n == 0) break;
		data.append(buf, static_cast<size_t>(n));
	}
	::close(fd);
	return data;
}

std::string format_date(std::int64_t unix)
{
	std::int64_t days = unix >= 0 ? unix / 86400 : (unix - 86399) / 86400, y;
	unsigned m, d;
	civil_from_days(days, y, m, d);
	return std::format("{:04}-{:02}-{:02}", y, m, d);
}

std::string format_rfc3339(std::int64_t unix)
{
	std::int64_t days = unix >= 0 ? unix / 86400 : (unix - 86399) / 86400, y;
	std::int64_t secs = unix - days * 86400;
	unsigned m, d;
	civil_from_days(days, y, m, d);
	return std::format("{:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z", y, m, d, secs / 3600, secs / 60 % 60, secs % 60);
}

std::optional<PemBlock> pem_decode(std::string_view data, std::string_view &rest)
{
	auto get_line = [](std::string_view d, std::string_view &line, std::string_view &after) -> size_t {
		size_t i = d.find('\n'), j;
		if (i == std::string_view::npos) {
			i = d.size();
			j = i;
		} else {
			j = i + 1;
			if (i > 0 && d[i - 1] == '\r') i--;
		}
		line = d.substr(0, i);
		while (!line.empty() && (line.back() == ' ' || line.back() == '\t')) line.remove_suffix(1);
		after = d.substr(j);
		return j;
	};
	auto trim = [](std::string_view s) {
		auto space = [](char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'; };
		while (!s.empty() && space(s.front())) s.remove_prefix(1);
		while (!s.empty() && space(s.back())) s.remove_suffix(1);
		return s;
	};
	static const std::string_view pem_start = "\n-----BEGIN ", pem_end = "\n-----END ", eol = "-----";
	std::string_view r = data;
	long end_trailer_index = 0;
	for (;;) {
		if (end_trailer_index < 0 || static_cast<size_t>(end_trailer_index) > r.size()) {
			rest = data;
			return std::nullopt;
		}
		r = r.substr(static_cast<size_t>(end_trailer_index));
		size_t end = r.find(pem_end);
		if (end == std::string_view::npos) {
			rest = data;
			return std::nullopt;
		}
		long end_index = static_cast<long>(end);
		end_trailer_index = end_index + static_cast<long>(pem_end.size());
		size_t begin = r.substr(0, end).rfind(pem_start.substr(1));
		if (begin == std::string_view::npos || (begin > 0 && r[begin - 1] != '\n')) continue;
		long shift = static_cast<long>(begin + pem_start.size() - 1);
		r = r.substr(static_cast<size_t>(shift));
		end_index -= shift;
		end_trailer_index -= shift;
		std::string_view type_line, after;
		size_t consumed = get_line(r, type_line, after);
		r = after;
		end_index -= static_cast<long>(consumed);
		end_trailer_index -= static_cast<long>(consumed);
		if (type_line.size() < eol.size() || type_line.substr(type_line.size() - eol.size()) != eol) continue;
		type_line.remove_suffix(eol.size());
		PemBlock p;
		p.type = std::string(type_line);
		bool truncated = false;
		for (;;) {
			if (r.empty()) {
				truncated = true;
				break;
			}
			std::string_view line, next;
			size_t c = get_line(r, line, next);
			size_t colon = line.find(':');
			if (colon == std::string_view::npos) break;
			p.headers[std::string(trim(line.substr(0, colon)))] = std::string(trim(line.substr(colon + 1)));
			r = next;
			end_index -= static_cast<long>(c);
			end_trailer_index -= static_cast<long>(c);
		}
		if (truncated) {
			rest = data;
			return std::nullopt;
		}
		if (!p.headers.empty() && end_index < 0) continue;
		if (end_trailer_index < 0 || static_cast<size_t>(end_trailer_index) > r.size()) continue;
		std::string_view end_trailer = r.substr(static_cast<size_t>(end_trailer_index));
		size_t trailer_len = type_line.size() + eol.size();
		if (end_trailer.size() < trailer_len) continue;
		std::string_view rest_of_end_line = end_trailer.substr(trailer_len);
		end_trailer = end_trailer.substr(0, trailer_len);
		if (end_trailer.substr(0, type_line.size()) != type_line || end_trailer.substr(type_line.size()) != eol) continue;
		std::string_view s, unused;
		get_line(rest_of_end_line, s, unused);
		if (!s.empty()) continue;
		if (end_index > 0) {
			// Spaces and tabs removed, then base64.StdEncoding (which skips '\r' and '\n').
			std::string b64;
			for (char c : r.substr(0, static_cast<size_t>(end_index)))
				if (c != ' ' && c != '\t') b64 += c;
			auto decoded = base64::decode(b64);
			if (!decoded) continue;
			p.bytes = std::move(*decoded);
		}
		std::string_view line, after_end;
		get_line(r.substr(static_cast<size_t>(end_index + static_cast<long>(pem_end.size()) - 1)), line, after_end);
		rest = after_end;
		return p;
	}
}

std::string pem_encode(std::string_view type, std::string_view bytes)
{
	static const char table[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	std::string b64;
	size_t i = 0;
	for (; i + 3 <= bytes.size(); i += 3) {
		unsigned v = static_cast<unsigned>(static_cast<unsigned char>(bytes[i]) << 16 |
		                                   static_cast<unsigned char>(bytes[i + 1]) << 8 | static_cast<unsigned char>(bytes[i + 2]));
		b64 += table[v >> 18];
		b64 += table[(v >> 12) & 63];
		b64 += table[(v >> 6) & 63];
		b64 += table[v & 63];
	}
	if (size_t r = bytes.size() - i) {
		unsigned v = static_cast<unsigned>(static_cast<unsigned char>(bytes[i]) << 16);
		if (r == 2) v |= static_cast<unsigned>(static_cast<unsigned char>(bytes[i + 1]) << 8);
		b64 += table[v >> 18];
		b64 += table[(v >> 12) & 63];
		b64 += r == 2 ? table[(v >> 6) & 63] : '=';
		b64 += '=';
	}
	std::string out = "-----BEGIN " + std::string(type) + "-----\n";
	for (size_t k = 0; k < b64.size(); k += 64) out += b64.substr(k, 64) + "\n";
	return out + "-----END " + std::string(type) + "-----\n";
}

std::expected<Certificate, std::string> parse_certificate(std::string_view der)
{
	Certificate c;
	Der input{der};
	std::string_view element;
	if (!input.read_element(element, tag::SEQUENCE)) return std::unexpected("x509: malformed certificate");
	c.raw = std::string(element);
	Der in{element};
	std::string_view body;
	if (!in.read(body, tag::SEQUENCE)) return std::unexpected("x509: malformed certificate");
	Der cert{body};
	std::string_view tbs_element;
	if (!cert.read_element(tbs_element, tag::SEQUENCE)) return std::unexpected("x509: malformed tbs certificate");
	c.raw_tbs = std::string(tbs_element);
	Der t{tbs_element};
	std::string_view tbs_body;
	if (!t.read(tbs_body, tag::SEQUENCE)) return std::unexpected("x509: malformed tbs certificate");
	Der tbs{tbs_body};

	std::int64_t version = 0;
	{
		std::string_view v;
		bool present;
		if (!tbs.read_optional(v, present, tag::constructed(tag::context(0)))) return std::unexpected("x509: malformed version");
		if (present) {
			Der vd{v};
			if (!vd.read_int64(version) || !vd.empty()) return std::unexpected("x509: malformed version");
		}
	}
	if (version < 0) return std::unexpected("x509: malformed version");
	version++;
	if (version > 3) return std::unexpected("x509: invalid version");

	std::string_view serial;
	if (!tbs.read_big(serial)) return std::unexpected("x509: malformed serial number");
	if (big_negative(serial)) return std::unexpected("x509: negative serial number");

	std::string_view sig_ai_element, sig_ai, outer_sig_ai;
	if (!tbs.read_element(sig_ai_element, tag::SEQUENCE)) return std::unexpected("x509: malformed signature algorithm identifier");
	{
		Der s{sig_ai_element};
		if (!s.read(sig_ai, tag::SEQUENCE)) return std::unexpected("x509: malformed signature algorithm identifier");
	}
	if (!cert.read(outer_sig_ai, tag::SEQUENCE)) return std::unexpected("x509: malformed algorithm identifier");
	if (outer_sig_ai != sig_ai) return std::unexpected("x509: inner and outer signature algorithm identifiers don't match");
	AlgorithmIdentifier ai;
	{
		Der a{sig_ai};
		if (!a.read_oid(ai.algorithm)) return std::unexpected("x509: malformed OID");
		if (!a.empty()) {
			std::string_view params;
			std::uint8_t pt;
			if (!a.read_any(&params, &pt, false)) return std::unexpected("x509: malformed parameters");
			ai.parameters = params;
		}
	}
	c.signature_algorithm = signature_algorithm(ai);

	std::string_view issuer;
	if (!tbs.read_element(issuer, tag::SEQUENCE)) return std::unexpected("x509: malformed issuer");
	c.raw_issuer = std::string(issuer);
	std::vector<Rdn> issuer_rdns;
	if (auto e = parse_name(issuer, issuer_rdns)) return std::unexpected(*e);
	describe_name(issuer_rdns, c.issuer, c.issuer_cn);

	std::string_view validity;
	if (!tbs.read(validity, tag::SEQUENCE)) return std::unexpected("x509: malformed validity");
	{
		Der v{validity};
		GoTime times[2];
		for (auto &tm : times) {
			if (v.peek(tag::UTCTime)) {
				if (!v.read_utc_time(tm)) return std::unexpected("x509: malformed UTCTime");
			} else if (v.peek(tag::GeneralizedTime)) {
				if (!v.read_generalized_time(tm)) return std::unexpected("x509: malformed GeneralizedTime");
			} else {
				return std::unexpected("x509: unsupported time format");
			}
		}
		c.not_before = times[0].unix;
		c.not_after = times[1].unix;
	}

	std::string_view subject;
	if (!tbs.read_element(subject, tag::SEQUENCE)) return std::unexpected("x509: malformed issuer");
	c.raw_subject = std::string(subject);
	std::vector<Rdn> subject_rdns;
	if (auto e = parse_name(subject, subject_rdns)) return std::unexpected(*e);
	describe_name(subject_rdns, c.subject, c.subject_cn);

	std::string_view spki_element, spki_body, pk_ai_seq;
	if (!tbs.read_element(spki_element, tag::SEQUENCE)) return std::unexpected("x509: malformed spki");
	c.raw_spki = std::string(spki_element);
	{
		Der s{spki_element};
		if (!s.read(spki_body, tag::SEQUENCE)) return std::unexpected("x509: malformed spki");
	}
	Der spki{spki_body};
	if (!spki.read(pk_ai_seq, tag::SEQUENCE)) return std::unexpected("x509: malformed public key algorithm identifier");
	AlgorithmIdentifier pk_ai;
	{
		Der a{pk_ai_seq};
		if (!a.read_oid(pk_ai.algorithm)) return std::unexpected("x509: malformed OID");
		if (!a.empty()) {
			std::string_view params;
			std::uint8_t pt;
			if (!a.read_any(&params, &pt, false)) return std::unexpected("x509: malformed parameters");
			pk_ai.parameters = params;
		}
	}
	PkAlgo pk_algo = public_key_algorithm(pk_ai.algorithm);
	std::string spk;
	int spk_bits = 0;
	if (!spki.read_bit_string(spk, spk_bits)) return std::unexpected("x509: malformed subjectPublicKey");
	if (pk_algo != PkAlgo::unknown) {
		if (auto e = parse_public_key(pk_algo, pk_ai, right_align(spk, spk_bits), c.key)) return std::unexpected(*e);
	}

	if (version > 1) {
		if (!tbs.skip_optional(tag::context(1))) return std::unexpected("x509: malformed issuerUniqueID");
		if (!tbs.skip_optional(tag::context(2))) return std::unexpected("x509: malformed subjectUniqueID");
		if (version == 3) {
			std::string_view extensions;
			bool present;
			if (!tbs.read_optional(extensions, present, tag::constructed(tag::context(3))))
				return std::unexpected("x509: malformed extensions");
			if (present) {
				std::set<std::string> seen;
				Der e{extensions};
				std::string_view list;
				if (!e.read(list, tag::SEQUENCE)) return std::unexpected("x509: malformed extensions");
				Der l{list};
				struct Ext {
					Oid id;
					bool critical;
					std::string_view value;
				};
				std::vector<Ext> exts;
				while (!l.empty()) {
					std::string_view ext;
					if (!l.read(ext, tag::SEQUENCE)) return std::unexpected("x509: malformed extension");
					Der x{ext};
					Ext parsed{{}, false, {}};
					if (!x.read_oid(parsed.id)) return std::unexpected("x509: malformed extension OID field");
					if (x.peek(tag::BOOLEAN) && !x.read_bool(parsed.critical))
						return std::unexpected("x509: malformed extension critical field");
					if (!x.read(parsed.value, tag::OCTET_STRING)) return std::unexpected("x509: malformed extension value field");
					std::string id = oid_string(parsed.id);
					if (!seen.insert(id).second)
						return std::unexpected(std::format("x509: certificate contains duplicate extension with OID {}", go_quote(id)));
					exts.push_back(std::move(parsed));
				}
				for (const auto &x : exts)
					if (auto err = process_extension(x.id, x.critical, x.value, c)) return std::unexpected(*err);
			}
		}
	}

	std::string sig;
	int sig_bits = 0;
	if (!cert.read_bit_string(sig, sig_bits)) return std::unexpected("x509: malformed signature");
	c.signature = right_align(sig, sig_bits);
	if (der.size() != c.raw.size()) return std::unexpected("x509: trailing data");
	return c;
}

std::expected<PrivateKey, std::string> parse_private_key(std::string_view der)
{
	auto key = parse_pkcs8(der);
	std::string pkcs8_error = key ? "" : key.error();
	if (!key) key = parse_pkcs1(der);
	if (!key) key = parse_ec(nullptr, der);
	if (!key) return std::unexpected("tls: failed to parse private key: " + pkcs8_error);
	if (key->kind != KeyKind::rsa && key->kind != KeyKind::ecdsa && key->kind != KeyKind::ed25519)
		return std::unexpected("tls: found unknown private key type in PKCS#8 wrapping");
	return key;
}

std::expected<Pair, std::string> x509_key_pair(std::string_view cert_pem, std::string_view key_pem)
{
	Pair pair;
	std::vector<std::string> skipped;
	std::string_view rest = cert_pem;
	for (;;) {
		auto block = pem_decode(rest, rest);
		if (!block) break;
		if (block->type == "CERTIFICATE") pair.chain.push_back(std::move(block->bytes));
		else skipped.push_back(block->type);
	}
	auto list = [](const std::vector<std::string> &v) {
		std::string s = "[";
		for (size_t i = 0; i < v.size(); i++) s += (i ? " " : "") + v[i];
		return s + "]";
	};
	if (pair.chain.empty()) {
		if (skipped.empty()) return std::unexpected("tls: failed to find any PEM data in certificate input");
		if (skipped.size() == 1 && skipped[0].size() >= 11 && skipped[0].ends_with("PRIVATE KEY"))
			return std::unexpected("tls: failed to find certificate PEM data in certificate input, but did find a private key; PEM inputs may have been switched");
		return std::unexpected("tls: failed to find \"CERTIFICATE\" PEM block in certificate input after skipping PEM blocks of the following types: " + list(skipped));
	}
	skipped.clear();
	std::optional<PemBlock> key_block;
	rest = key_pem;
	for (;;) {
		key_block = pem_decode(rest, rest);
		if (!key_block) {
			if (skipped.empty()) return std::unexpected("tls: failed to find any PEM data in key input");
			if (skipped.size() == 1 && skipped[0] == "CERTIFICATE")
				return std::unexpected("tls: found a certificate rather than a key in the PEM for the private key");
			return std::unexpected("tls: failed to find PEM block with type ending in \"PRIVATE KEY\" in key input after skipping PEM blocks of the following types: " + list(skipped));
		}
		if (key_block->type == "PRIVATE KEY" || key_block->type.ends_with(" PRIVATE KEY")) break;
		skipped.push_back(key_block->type);
	}
	auto leaf = parse_certificate(pair.chain[0]);
	if (!leaf) return std::unexpected(leaf.error());
	pair.leaf = std::move(*leaf);
	auto key = parse_private_key(key_block->bytes);
	if (!key) return std::unexpected(key.error());
	pair.key = std::move(*key);
	switch (pair.leaf.key.kind) {
	case KeyKind::rsa:
	case KeyKind::ecdsa:
	case KeyKind::ed25519:
		if (pair.key.kind != pair.leaf.key.kind) return std::unexpected("tls: private key type does not match public key type");
		if (!same_public(pair.key.pub, pair.leaf.key)) return std::unexpected("tls: private key does not match public key");
		break;
	default: return std::unexpected("tls: unknown public key algorithm");
	}
	return pair;
}

std::expected<Pair, std::string> load_x509_key_pair(const std::string &crt, const std::string &key)
{
	std::string error;
	auto c = read_file(crt, error);
	if (!c) return std::unexpected(error);
	auto k = read_file(key, error);
	if (!k) return std::unexpected(error);
	return x509_key_pair(*c, *k);
}

std::expected<Parsed, std::string> parse_certificate_pair(std::string_view cert_pem, std::string_view key_pem)
{
	Parsed out;
	int count = 0;
	std::string_view rest = cert_pem;
	for (;;) {
		auto block = pem_decode(rest, rest);
		if (!block) break;
		if (block->type != "CERTIFICATE") continue;
		if (++count > 8) return std::unexpected("more than 8 certificates in the upload");
		out.cert_out += pem_encode(block->type, block->bytes);
	}
	if (!count) return std::unexpected("no certificate found: upload a PEM file with a CERTIFICATE block");
	rest = key_pem;
	for (;;) {
		auto block = pem_decode(rest, rest);
		if (!block) break;
		auto proc = block->headers.find("Proc-Type");
		if (block->type == "ENCRYPTED PRIVATE KEY" ||
		    (proc != block->headers.end() && proc->second.find("ENCRYPTED") != std::string::npos))
			return std::unexpected("the private key is password-protected: upload an unencrypted key");
		if (block->type == "PRIVATE KEY" || block->type.ends_with(" PRIVATE KEY")) {
			out.key_out = pem_encode(block->type, block->bytes);
			break;
		}
	}
	if (out.key_out.empty()) return std::unexpected("no private key found: upload a PEM file with a PRIVATE KEY block");
	auto pair = x509_key_pair(out.cert_out, out.key_out);
	if (!pair) {
		std::string e = pair.error();
		if (e.starts_with("tls: ")) e.erase(0, 5);
		return std::unexpected(e);
	}
	out.pair = std::move(*pair);
	return out;
}

std::expected<Parsed, std::string> load_certificate_pair(const std::string &crt, const std::string &key)
{
	std::string error;
	auto c = read_file(crt, error);
	if (!c) return std::unexpected("read certificate: " + error);
	auto k = read_file(key, error);
	if (!k) return std::unexpected("read private key: " + error);
	return parse_certificate_pair(*c, *k);
}

std::string fingerprint(const Pair &pair)
{
	unsigned char sum[32];
	unsigned int len = 0;
	EVP_Digest(pair.leaf.raw.data(), pair.leaf.raw.size(), sum, &len, EVP_sha256(), nullptr);
	static const char hex[] = "0123456789ABCDEF";
	std::string out;
	for (unsigned i = 0; i < len; i++) {
		if (i) out += ':';
		out += hex[sum[i] >> 4];
		out += hex[sum[i] & 15];
	}
	return out;
}

namespace {

// net.IP.String.
std::string ip_string(std::string_view ip)
{
	if (ip.size() == 4)
		return std::format("{}.{}.{}.{}", static_cast<std::uint8_t>(ip[0]), static_cast<std::uint8_t>(ip[1]),
		                   static_cast<std::uint8_t>(ip[2]), static_cast<std::uint8_t>(ip[3]));
	// RFC 5952 as netip: the longest run of two or more zero groups (the first of equal runs) becomes "::".
	int groups[8];
	for (int i = 0; i < 8; i++)
		groups[i] = static_cast<std::uint8_t>(ip[static_cast<size_t>(2 * i)]) << 8 | static_cast<std::uint8_t>(ip[static_cast<size_t>(2 * i + 1)]);
	int best = -1, best_len = 0;
	for (int i = 0; i < 8;) {
		if (groups[i]) {
			i++;
			continue;
		}
		int j = i;
		while (j < 8 && !groups[j]) j++;
		if (j - i > best_len && j - i >= 2) best = i, best_len = j - i;
		i = j;
	}
	std::string out;
	for (int i = 0; i < 8; i++) {
		if (i == best) {
			out += "::";
			i += best_len - 1;
			continue;
		}
		if (!out.empty() && out.back() != ':') out += ':';
		out += std::format("{:x}", groups[i]);
	}
	return out;
}

std::string key_type(const PublicKey &k)
{
	switch (k.kind) {
	case KeyKind::ecdsa: return std::string("ECDSA ") + curve_name(k.curve);
	case KeyKind::rsa: return std::format("RSA {}", BN_num_bits(bn(k.n).get()));
	case KeyKind::ed25519: return "Ed25519";
	case KeyKind::mldsa: return "*mldsa.PublicKey";
	case KeyKind::dsa: return "*dsa.PublicKey";
	case KeyKind::x25519:
	case KeyKind::unknown: break;
	}
	return "<nil>";
}

} // namespace

std::string describe(const Pair &pair, Instant at)
{
	const Certificate &leaf = pair.leaf;
	std::string dns = "[", ips = "[";
	for (size_t i = 0; i < leaf.dns_names.size(); i++) dns += (i ? "," : "") + gojson::quote(leaf.dns_names[i]);
	for (size_t i = 0; i < leaf.ip_addresses.size(); i++) ips += (i ? "," : "") + gojson::quote(ip_string(leaf.ip_addresses[i]));
	bool expired = at.sec > leaf.not_after || (at.sec == leaf.not_after && at.nsec > 0);
	bool self_signed = leaf.raw_issuer == leaf.raw_subject && check_signature(leaf);
	return "{\"subject\":" + gojson::quote(leaf.subject_cn.empty() ? leaf.subject : leaf.subject_cn) +
	       ",\"issuer\":" + gojson::quote(leaf.issuer_cn.empty() ? leaf.issuer : leaf.issuer_cn) + ",\"dnsNames\":" + dns +
	       "],\"ipAddresses\":" + ips + "],\"notBefore\":\"" + format_rfc3339(leaf.not_before) + "\",\"notAfter\":\"" +
	       format_rfc3339(leaf.not_after) + "\",\"expired\":" + (expired ? "true" : "false") + ",\"fingerprint\":\"" +
	       fingerprint(pair) + "\",\"keyType\":" + gojson::quote(key_type(leaf.key)) +
	       ",\"selfSigned\":" + (self_signed ? "true" : "false") + ",\"chain\":" + std::to_string(pair.chain.size()) + "}";
}

std::optional<std::string> serving_key_pem(const PrivateKey &key)
{
	Pkey pkey;
	std::unique_ptr<OSSL_PARAM_BLD, decltype(&OSSL_PARAM_BLD_free)> bld(OSSL_PARAM_BLD_new(), OSSL_PARAM_BLD_free);
	std::vector<Bn> keep;
	auto push_bn = [&](const char *name, std::string_view bytes) {
		keep.push_back(bn(bytes));
		return OSSL_PARAM_BLD_push_BN(bld.get(), name, keep.back().get()) == 1;
	};
	const char *type = nullptr;
	switch (key.kind) {
	case KeyKind::rsa: {
		if (key.primes != 2) return std::nullopt;
		type = "RSA";
		keep.push_back(bn_int(static_cast<std::uint64_t>(key.pub.e)));
		if (!push_bn(OSSL_PKEY_PARAM_RSA_N, key.pub.n) ||
		    OSSL_PARAM_BLD_push_BN(bld.get(), OSSL_PKEY_PARAM_RSA_E, keep.front().get()) != 1 ||
		    !push_bn(OSSL_PKEY_PARAM_RSA_D, key.d) || !push_bn(OSSL_PKEY_PARAM_RSA_FACTOR1, key.p) ||
		    !push_bn(OSSL_PKEY_PARAM_RSA_FACTOR2, key.q) || !push_bn(OSSL_PKEY_PARAM_RSA_EXPONENT1, key.dp) ||
		    !push_bn(OSSL_PKEY_PARAM_RSA_EXPONENT2, key.dq) || !push_bn(OSSL_PKEY_PARAM_RSA_COEFFICIENT1, key.qinv))
			return std::nullopt;
		break;
	}
	case KeyKind::ecdsa: {
		type = "EC";
		const char *group = OBJ_nid2sn(key.pub.curve);
		if (OSSL_PARAM_BLD_push_utf8_string(bld.get(), OSSL_PKEY_PARAM_GROUP_NAME, group, 0) != 1 ||
		    !push_bn(OSSL_PKEY_PARAM_PRIV_KEY, key.d) ||
		    OSSL_PARAM_BLD_push_octet_string(bld.get(), OSSL_PKEY_PARAM_PUB_KEY, key.pub.point.data(), key.pub.point.size()) != 1)
			return std::nullopt;
		break;
	}
	case KeyKind::ed25519:
		pkey.reset(EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, nullptr, reinterpret_cast<const unsigned char *>(key.d.data()), key.d.size()));
		break;
	default: return std::nullopt;
	}
	if (type) {
		std::unique_ptr<OSSL_PARAM, decltype(&OSSL_PARAM_free)> params(OSSL_PARAM_BLD_to_param(bld.get()), OSSL_PARAM_free);
		std::unique_ptr<EVP_PKEY_CTX, decltype(&EVP_PKEY_CTX_free)> ctx(EVP_PKEY_CTX_new_from_name(nullptr, type, nullptr), EVP_PKEY_CTX_free);
		EVP_PKEY *raw = nullptr;
		if (!params || !ctx || EVP_PKEY_fromdata_init(ctx.get()) != 1 ||
		    EVP_PKEY_fromdata(ctx.get(), &raw, EVP_PKEY_KEYPAIR, params.get()) != 1)
			return std::nullopt;
		pkey.reset(raw);
	}
	if (!pkey) return std::nullopt;
	std::unique_ptr<BIO, decltype(&BIO_free)> bio(BIO_new(BIO_s_mem()), BIO_free);
	if (PEM_write_bio_PKCS8PrivateKey(bio.get(), pkey.get(), nullptr, nullptr, 0, nullptr, nullptr) != 1) return std::nullopt;
	char *data = nullptr;
	long len = BIO_get_mem_data(bio.get(), &data);
	return std::string(data, static_cast<size_t>(len));
}

std::string generate_certificate_files(const std::string &crt, const std::string &key, Instant at)
{
	// ECDSA P-256: on the device an RSA-2048 signature costs about 80 ms of every full handshake and its generation
	// several seconds at first boot; P-256 costs a few milliseconds.
	Pkey pkey(EVP_PKEY_Q_keygen(nullptr, nullptr, "EC", "P-256"));
	if (!pkey) return "failed to generate ECDSA private key";
	std::unique_ptr<X509, decltype(&X509_free)> cert(X509_new(), X509_free);
	// A random serial below 2^128, as rand.Int(rand.Reader, 1<<128).
	Bn limit(BN_new()), serial(BN_new());
	BN_lshift(limit.get(), bn_int(1).get(), 128);
	if (BN_rand_range(serial.get(), limit.get()) != 1) return "failed to generate serial number";
	std::unique_ptr<ASN1_INTEGER, decltype(&ASN1_INTEGER_free)> sn(BN_to_ASN1_INTEGER(serial.get(), nullptr), ASN1_INTEGER_free);
	std::unique_ptr<X509_NAME, decltype(&X509_NAME_free)> name(X509_NAME_new(), X509_NAME_free);
	X509_NAME_add_entry_by_NID(name.get(), NID_commonName, V_ASN1_PRINTABLESTRING,
	                           reinterpret_cast<const unsigned char *>("localhost"), -1, -1, 0);
	time_t not_before = static_cast<time_t>(at.sec), not_after = static_cast<time_t>(at.sec + 315360000);
	if (X509_set_version(cert.get(), X509_VERSION_3) != 1 || X509_set_serialNumber(cert.get(), sn.get()) != 1 ||
	    X509_set_subject_name(cert.get(), name.get()) != 1 || X509_set_issuer_name(cert.get(), name.get()) != 1 ||
	    !X509_time_adj_ex(X509_getm_notBefore(cert.get()), 0, 0, &not_before) ||
	    !X509_time_adj_ex(X509_getm_notAfter(cert.get()), 0, 0, &not_after) || X509_set_pubkey(cert.get(), pkey.get()) != 1)
		return "failed to create certificate";
	// The extensions in the order Go writes them: key usage, extended key usage, basic constraints, names.
	X509V3_CTX ctx;
	X509V3_set_ctx_nodb(&ctx);
	X509V3_set_ctx(&ctx, cert.get(), cert.get(), nullptr, nullptr, 0);
	const std::pair<int, const char *> extensions[] = {{NID_key_usage, "critical,digitalSignature"},
	                                                   {NID_ext_key_usage, "serverAuth"},
	                                                   {NID_basic_constraints, "critical,CA:FALSE"},
	                                                   {NID_subject_alt_name, "DNS:localhost,IP:127.0.0.1,IP:::1"}};
	for (const auto &[nid, value] : extensions) {
		X509_EXTENSION *ext = X509V3_EXT_conf_nid(nullptr, &ctx, nid, value);
		if (!ext) return "failed to create certificate";
		int ok = X509_add_ext(cert.get(), ext, -1);
		X509_EXTENSION_free(ext);
		if (ok != 1) return "failed to create certificate";
	}
	if (X509_sign(cert.get(), pkey.get(), EVP_sha256()) <= 0) return "failed to create certificate";

	std::unique_ptr<BIO, decltype(&BIO_free)> kb(BIO_new(BIO_s_mem()), BIO_free);
	if (PEM_write_bio_PKCS8PrivateKey(kb.get(), pkey.get(), nullptr, nullptr, 0, nullptr, nullptr) != 1)
		return "failed to marshal private key";
	char *data = nullptr;
	long len = BIO_get_mem_data(kb.get(), &data);
	std::string key_pem(data, static_cast<size_t>(len));
	unsigned char *der = nullptr;
	int der_len = i2d_X509(cert.get(), &der);
	if (der_len <= 0) return "failed to create certificate";
	std::string cert_pem = pem_encode("CERTIFICATE", std::string_view(reinterpret_cast<char *>(der), static_cast<size_t>(der_len)));
	OPENSSL_free(der);
	// The key before the certificate, each atomically: a power cut leaves an incomplete pair (regenerated at the
	// next start), never a torn one.
	if (!config::atomic_write(key, key_pem, 0600)) return "write " + key + ": " + errno_text(errno);
	if (!config::atomic_write(crt, cert_pem, 0644)) return "write " + crt + ": " + errno_text(errno);
	return "";
}

} // namespace nkos::system::tls
