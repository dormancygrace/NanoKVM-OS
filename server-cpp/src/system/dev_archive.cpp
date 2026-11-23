// The developer archive (Diagnostics > Prepare archive for developers), as the Go server's service/vm/dev_archive.go:
// the collector of nanokvm-base gathers logs, system, hardware, network and allow-listed settings into a temporary
// directory; every file then passes the log viewer's sanitizer and address masking (settings files are parsed and
// their credential-like keys hidden instead) and is packed into a tar.gz the way Go's archive/tar and compress/gzip
// write it. Screen content never leaves the device: saved frames and screenshots are refused by name.

#include "system/diagnostics.hpp"

#include "common/gojson.hpp"
#include "common/tz.hpp"
#include "notify/json.hpp"
#include "recovery/yaml_v3.hpp"
#include "storage/go.hpp"
#include "system/log_mask.hpp"

#include <zlib.h>

#include <algorithm>
#include <cerrno>
#include <charconv>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::system::diagnostics {

namespace {

namespace go = storage::go;

constexpr std::size_t archive_limit = 16 << 20;  // the compressed archive
constexpr std::size_t raw_limit = 24 << 20;      // the cleaned text of all files together
constexpr std::size_t file_limit = 1 << 20;      // the last MiB of each file
constexpr const char *too_large = "the developer archive is too large";

constexpr const char *readme_format = R"(NanoKVM OS developer archive

Collected on the device by %s, created %s.

Contents: logs of the last three boots, the kernel and apk; system state
(versions, processes, memory, mounts, services, packages); hardware (video
capture, HDMI receiver diagnostics, USB gadget, thermal); network state;
allow-listed settings; diagnostics.json, the report of the Diagnostics page.

Before packing, every file was cleaned on the device:
- IPv4, IPv6 and MAC addresses and Wi-Fi network names are masked;
- entries that may hold passwords, keys, tokens or cookies are hidden;
- JSON and YAML settings are parsed and the values of keys naming
  credentials, user names or keys are hidden with everything under them
  (comments are dropped); a settings file that does not parse, or uses
  YAML anchors, is omitted.
Not collected at all: passwords, keys and certificates, the web login
secret, Wi-Fi and VPN configuration, user scripts, the USB serial number,
screen or video content and the contents of files on the user disk (the
names of images attached as virtual disks may appear).
Not masked: host names and web user names in logs.
%s)";

// ---- names ----

// A lower-case ASCII literal under (?i) ending at end: where it starts, or npos. RE2 folds s with U+017F and k with
// U+212A.
std::size_t fold_suffix(std::string_view s, std::size_t end, std::string_view lit)
{
	for (std::size_t k = lit.size(); k-- > 0;) {
		char p = lit[k];
		if (end >= 1) {
			unsigned char c = static_cast<unsigned char>(s[end - 1]);
			if (c == static_cast<unsigned char>(p) || (p >= 'a' && p <= 'z' && c == static_cast<unsigned char>(p - 32))) {
				end--;
				continue;
			}
		}
		if (p == 's' && end >= 2 && s.substr(end - 2, 2) == "\xc5\xbf") {
			end -= 2;
			continue;
		}
		if (p == 'k' && end >= 3 && s.substr(end - 3, 3) == "\xe2\x84\xaa") {
			end -= 3;
			continue;
		}
		return std::string_view::npos;
	}
	return end;
}

// (?i)(screenshot|snapshot|frame-dump|\.(bgr|rgb|yuv|nv12|jpe?g|png|bmp|raw|h26[45]))$
bool frame_name(std::string_view name)
{
	static constexpr std::string_view suffixes[] = {"screenshot", "snapshot", "frame-dump", ".bgr",  ".rgb",
	                                                ".yuv",       ".nv12",    ".jpg",       ".jpeg", ".png",
	                                                ".bmp",       ".raw",     ".h264",      ".h265"};
	for (auto s : suffixes)
		if (fold_suffix(name, name.size(), s) != std::string_view::npos) return true;
	return false;
}

// ^config/.*\.(json|ya?ml)$ (the dot of .* stops at a line break).
bool structured_name(std::string_view name)
{
	if (!name.starts_with("config/")) return false;
	for (std::string_view ext : {".json", ".yaml", ".yml"})
		if (name.size() >= 7 + ext.size() && name.ends_with(ext)) {
			std::string_view middle = name.substr(7, name.size() - 7 - ext.size());
			return middle.find('\n') == std::string_view::npos;
		}
	return false;
}

// (?i)(cred|pass|secret|private|cookie|api_?key|authorization|bearer)|(^|_)(key|token|user|username|login)$|
// [a-z0-9](key|token|user)$
bool secret_key(std::string_view key)
{
	// Folded to ASCII lower case (U+017F to s, U+212A to k): the positions the pattern looks at stay comparable.
	std::string k;
	for (std::size_t i = 0; i < key.size(); i++) {
		unsigned char c = static_cast<unsigned char>(key[i]);
		if (key.substr(i, 2) == "\xc5\xbf") k += 's', i++;
		else if (key.substr(i, 3) == "\xe2\x84\xaa") k += 'k', i += 2;
		else if (c >= 'A' && c <= 'Z') k += static_cast<char>(c + 32);
		else if (c >= 0x80) k += '\x80';  // another non-ASCII byte: matches nothing in the pattern
		else k += static_cast<char>(c);
	}
	for (std::string_view w : {"cred", "pass", "secret", "private", "cookie", "apikey", "api_key", "authorization", "bearer"})
		if (k.find(w) != std::string::npos) return true;
	for (std::string_view w : {"key", "token", "user", "username", "login"}) {
		if (!k.ends_with(w)) continue;
		std::size_t at = k.size() - w.size();
		if (at == 0 || k[at - 1] == '_') return true;
	}
	for (std::string_view w : {"key", "token", "user"}) {
		if (!k.ends_with(w) || k.size() == w.size()) continue;
		char c = k[k.size() - w.size() - 1];
		if ((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) return true;
	}
	return false;
}

// ---- JSON settings ----

// A JSON string as an Encoder with SetEscapeHTML(false) writes it.
std::string quote_plain(std::string_view s)
{
	std::string q = gojson::quote(s);
	// Undo the HTML escapes only (<, >, &); other \u00XX escapes stay.
	std::string out;
	out.reserve(q.size());
	for (std::size_t i = 0; i < q.size(); i++) {
		if (q[i] == '\\' && i + 1 < q.size()) {
			std::string_view esc = std::string_view(q).substr(i, 6);
			if (esc == "\\u003c") out += '<', i += 5;
			else if (esc == "\\u003e") out += '>', i += 5;
			else if (esc == "\\u0026") out += '&', i += 5;
			else out += q[i], out += q[++i];
			continue;
		}
		out += q[i];
	}
	return out;
}

std::string json_float(double f)
{
	double a = std::fabs(f);
	char buf[64];
	if (a != 0 && (a < 1e-6 || a >= 1e21)) {
		auto [ptr, ec] = std::to_chars(buf, buf + sizeof buf, f, std::chars_format::scientific);
		std::string s(buf, ptr);
		std::size_t n = s.size();
		if (n >= 4 && s[n - 4] == 'e' && s[n - 3] == '-' && s[n - 2] == '0') s.erase(n - 2, 1);
		return s;
	}
	// strconv.FormatFloat(f, 'f', -1, 64): the shortest digits, then zeros up to the decimal point (to_chars' fixed
	// form would write a large integer's exact value).
	auto [ptr, ec] = std::to_chars(buf, buf + sizeof buf, f, std::chars_format::scientific);
	std::string_view s(buf, static_cast<std::size_t>(ptr - buf));
	std::string out;
	if (s[0] == '-') out += '-', s.remove_prefix(1);
	auto e = s.find('e');
	std::string digits;
	for (char c : s.substr(0, e))
		if (c != '.') digits += c;
	int exp = 0;
	std::string_view es = s.substr(e + 1);
	if (es[0] == '+') es.remove_prefix(1);
	std::from_chars(es.data(), es.data() + es.size(), exp);
	int dp = exp + 1, n = static_cast<int>(digits.size());
	if (dp <= 0) out += "0." + std::string(static_cast<std::size_t>(-dp), '0') + digits;
	else if (dp >= n) out += digits + std::string(static_cast<std::size_t>(dp - n), '0');
	else out += digits.substr(0, static_cast<std::size_t>(dp)) + "." + digits.substr(static_cast<std::size_t>(dp));
	return out;
}

// json.Unmarshal into an interface{}, the keys of credentials hidden, written by an Encoder with SetIndent("", "  ").
bool write_redacted(const notify::json::Value &v, std::string &out, int level, bool hidden)
{
	using Kind = notify::json::Value::Kind;
	auto newline = [&](int l) {
		out += '\n';
		out.append(static_cast<std::size_t>(l) * 2, ' ');
	};
	if (hidden) {
		out += "\"<hidden>\"";
		return true;
	}
	switch (v.kind) {
	case Kind::null: out += "null"; return true;
	case Kind::boolean: out += v.boolean ? "true" : "false"; return true;
	case Kind::string: out += quote_plain(v.text); return true;
	case Kind::number: {
		double f = 0;
		auto [ptr, ec] = std::from_chars(v.text.data(), v.text.data() + v.text.size(), f);
		if (ec != std::errc{} || !std::isfinite(f)) return false;  // out of float64's range
		out += json_float(f);
		return true;
	}
	case Kind::array:
		if (v.items.empty()) {
			out += "[]";
			return true;
		}
		out += '[';
		for (std::size_t i = 0; i < v.items.size(); i++) {
			if (i) out += ',';
			newline(level + 1);
			if (!write_redacted(v.items[i], out, level + 1, false)) return false;
		}
		newline(level);
		out += ']';
		return true;
	case Kind::object: {
		// A map: the last of duplicate keys, sorted.
		std::vector<std::pair<std::string_view, const notify::json::Value *>> members;
		for (const auto &[key, value] : v.members) {
			auto it = std::find_if(members.begin(), members.end(), [&](auto &m) { return m.first == key; });
			if (it != members.end()) it->second = &value;
			else members.emplace_back(key, &value);
		}
		std::sort(members.begin(), members.end(), [](auto &a, auto &b) { return a.first < b.first; });
		if (members.empty()) {
			out += "{}";
			return true;
		}
		out += '{';
		for (std::size_t i = 0; i < members.size(); i++) {
			if (i) out += ',';
			newline(level + 1);
			out += quote_plain(members[i].first);
			out += ": ";
			if (!write_redacted(*members[i].second, out, level + 1, secret_key(members[i].first))) return false;
		}
		newline(level);
		out += '}';
		return true;
	}
	}
	return false;
}

// ---- Wi-Fi network names ----

// knownSSIDs: the saved networks and the last chosen one, at least 3 bytes, longest first.
std::vector<std::string> known_ssids(const std::string &etc)
{
	std::vector<std::string> seen;
	auto add = [&](std::string name) {
		if (std::find(seen.begin(), seen.end(), name) == seen.end()) seen.push_back(std::move(name));
	};
	std::string data;
	if (!go::read_file(go::join(etc, "wifi.ssid"), data)) add(go::trim_space(data));
	if (DIR *d = ::opendir(go::join(etc, "wifi.saved").c_str())) {
		while (auto *e = ::readdir(d)) {
			std::string_view n = e->d_name;
			if (n.size() % 2) continue;
			std::string decoded;
			bool ok = true;
			for (std::size_t i = 0; ok && i < n.size(); i += 2) {
				unsigned v = 0;
				auto [p, ec] = std::from_chars(n.data() + i, n.data() + i + 2, v, 16);
				ok = ec == std::errc{} && p == n.data() + i + 2;
				decoded += static_cast<char>(v);
			}
			if (ok) add(decoded);
		}
		::closedir(d);
	}
	std::vector<std::string> names;
	for (auto &n : seen)
		// A name that is not valid UTF-8 cannot be a Go pattern (regexp.MustCompile panics there); it is left to the
		// line rules.
		if (n.size() >= 3 && go::valid_utf8(n)) names.push_back(n);
	std::stable_sort(names.begin(), names.end(), [](auto &a, auto &b) { return a.size() > b.size(); });
	return names;
}

std::string mask_ssids(std::string_view text, const std::vector<std::string> &names)
{
	std::string out;
	out.reserve(text.size());
	std::size_t copied = 0;
	for (std::size_t i = 0; i < text.size();) {
		const std::string *hit = nullptr;
		for (const auto &n : names)
			if (text.compare(i, n.size(), n) == 0) {
				hit = &n;
				break;
			}
		if (!hit) {
			i++;
			continue;
		}
		out.append(text.substr(copied, i - copied));
		out += "<ssid>";
		copied = i = i + hit->size();
	}
	out.append(text.substr(copied));
	return out;
}

// ---- files ----

// readLimited: at most the last limit bytes of a file.
bool read_limited(const std::string &path, std::size_t limit, std::string &data)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return false;
	struct stat st{};
	bool ok = ::fstat(fd, &st) == 0;
	if (ok && static_cast<std::size_t>(st.st_size) > limit) ok = ::lseek(fd, st.st_size - static_cast<off_t>(limit), SEEK_SET) >= 0;
	data.clear();
	char buf[65536];
	while (ok && data.size() < limit) {
		ssize_t got = ::read(fd, buf, std::min(sizeof buf, limit - data.size()));
		if (got < 0 && errno == EINTR) continue;
		if (got < 0) ok = false;
		if (got <= 0) break;
		data.append(buf, static_cast<std::size_t>(got));
	}
	::close(fd);
	return ok;
}

// binaryData: more than one byte in twenty is a control character other than tab, newline or carriage return.
bool binary_data(std::string_view data)
{
	std::size_t control = 0;
	for (unsigned char b : data)
		if (b < 0x20 && b != '\t' && b != '\n' && b != '\r') control++;
	return control * 20 > data.size();
}

// filepath.WalkDir's regular files under dir (symbolic links not followed); false when a directory cannot be read.
bool walk(const std::string &dir, std::vector<std::string> &files)
{
	DIR *d = ::opendir(dir.c_str());
	if (!d) return false;
	std::vector<std::pair<std::string, unsigned char>> entries;
	while (auto *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n == "." || n == "..") continue;
		unsigned char type = e->d_type;
		if (type == DT_UNKNOWN) {
			struct stat st{};
			if (::lstat((dir + "/" + n).c_str(), &st) == 0)
				type = S_ISDIR(st.st_mode) ? DT_DIR : S_ISREG(st.st_mode) ? DT_REG : DT_LNK;
		}
		entries.emplace_back(std::move(n), type);
	}
	::closedir(d);
	std::sort(entries.begin(), entries.end());
	for (const auto &[n, type] : entries) {
		std::string path = dir + "/" + n;
		if (type == DT_REG) files.push_back(path);
		else if (type == DT_DIR && !walk(path, files)) return false;
	}
	return true;
}

// ---- tar (USTAR, PAX for names USTAR cannot hold) and gzip, as archive/tar and compress/gzip write them ----

struct Gzip {
	z_stream z{};
	std::string out;
	bool ok = false;

	Gzip()
	{
		ok = deflateInit2(&z, Z_DEFAULT_COMPRESSION, Z_DEFLATED, 15 + 16, 8, Z_DEFAULT_STRATEGY) == Z_OK;
		// Go's header: no name, modification time 0, OS 255 (unknown).
		static gz_header header{};
		header.os = 255;
		if (ok) ok = deflateSetHeader(&z, &header) == Z_OK;
	}
	~Gzip() { deflateEnd(&z); }
	Gzip(const Gzip &) = delete;
	Gzip &operator=(const Gzip &) = delete;

	void write(std::string_view data, int flush = Z_NO_FLUSH)
	{
		z.next_in = reinterpret_cast<Bytef *>(const_cast<char *>(data.data()));
		z.avail_in = static_cast<uInt>(data.size());
		char buf[65536];
		for (;;) {
			z.next_out = reinterpret_cast<Bytef *>(buf);
			z.avail_out = sizeof buf;
			int r = deflate(&z, flush);
			out.append(buf, sizeof buf - z.avail_out);
			if (flush == Z_FINISH ? r == Z_STREAM_END || r == Z_STREAM_ERROR : z.avail_out != 0) break;
		}
	}
};

bool ascii(std::string_view s)
{
	for (unsigned char c : s)
		if (c >= 0x80 || c == 0) return false;
	return true;
}

// splitUSTARPath.
bool split_ustar(std::string_view name, std::string_view &prefix, std::string_view &suffix)
{
	std::size_t length = name.size();
	if (length <= 100 || !ascii(name)) return false;
	if (length > 156) length = 156;
	else if (name[length - 1] == '/') length--;
	std::size_t i = name.substr(0, length).rfind('/');
	if (i == std::string_view::npos || i == 0) return false;
	std::size_t nlen = name.size() - i - 1;
	if (nlen > 100 || nlen == 0 || i > 155) return false;
	prefix = name.substr(0, i);
	suffix = name.substr(i + 1);
	return true;
}

// formatString: the text, then a NUL when there is room.
void put_string(char *field, std::size_t size, std::string_view s)
{
	std::memcpy(field, s.data(), std::min(size, s.size()));
	if (s.size() < size) field[s.size()] = 0;
}

void put_octal(char *field, std::size_t size, std::int64_t v)
{
	char digits[32];
	int n = std::snprintf(digits, sizeof digits, "%llo", static_cast<unsigned long long>(v));
	std::string s(digits, static_cast<std::size_t>(n));
	if (s.size() + 1 < size) s.insert(0, size - 1 - s.size(), '0');
	put_string(field, size, s);
}

// toASCII: the runes below 0x80 other than NUL.
std::string to_ascii(std::string_view s)
{
	std::string out;
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x80) {
			if (c) out += static_cast<char>(c);
			i++;
			continue;
		}
		// Skip one rune (or one byte of an invalid sequence).
		int n = c >= 0xf0 && c < 0xf5 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 1;
		bool valid = n > 1 && i + static_cast<std::size_t>(n) <= s.size();
		for (int k = 1; valid && k < n; k++) valid = (static_cast<unsigned char>(s[i + static_cast<std::size_t>(k)]) & 0xc0) == 0x80;
		if (valid && !go::valid_utf8(s.substr(i, static_cast<std::size_t>(n)))) valid = false;
		i += valid ? static_cast<std::size_t>(n) : 1;
	}
	return out;
}

struct Tar {
	Gzip gz;
	std::size_t pending_pad = 0;

	void block(const char *b) { gz.write(std::string_view(b, 512)); }

	static void finish_header(char *b, bool ustar_magic)
	{
		if (ustar_magic) {
			std::memcpy(b + 257, "ustar\0", 6);
			std::memcpy(b + 263, "00", 2);
		}
		std::memset(b + 148, ' ', 8);
		unsigned sum = 0;
		for (int i = 0; i < 512; i++) sum += static_cast<unsigned char>(b[i]);
		put_octal(b + 148, 7, sum);
		b[155] = ' ';
	}

	void pad()
	{
		if (pending_pad) {
			static const char zeros[512] = {};
			gz.write(std::string_view(zeros, pending_pad));
			pending_pad = 0;
		}
	}

	void data(std::string_view text)
	{
		gz.write(text);
		pending_pad = (512 - text.size() % 512) % 512;
	}

	// templateV7Plus for a regular file of mode 0644 and size, modified at mtime.
	static void v7(char *b, std::string_view name, std::int64_t size, std::int64_t mtime, char flag)
	{
		b[156] = flag;
		put_string(b, 100, name);
		put_octal(b + 100, 8, 0644);
		put_octal(b + 108, 8, 0);
		put_octal(b + 116, 8, 0);
		put_octal(b + 124, 12, size);
		put_octal(b + 136, 12, mtime);
		put_octal(b + 329, 8, 0);
		put_octal(b + 337, 8, 0);
	}

	void file(const std::string &name, std::string_view text, std::int64_t mtime)
	{
		pad();
		char b[512] = {};
		bool fits = name.size() <= 100 && ascii(name);
		std::string_view prefix, suffix;
		if (fits || split_ustar(name, prefix, suffix)) {
			v7(b, fits ? std::string_view(name) : suffix, static_cast<std::int64_t>(text.size()), mtime, '0');
			put_string(b + 345, 155, prefix);
			finish_header(b, true);
			block(b);
		} else {
			// A PAX extended header with the path, then the main header with what fits.
			std::string record = "path=" + name + "\n";
			std::size_t size = record.size() + 1;
			size += std::to_string(size).size();
			std::string rec = std::to_string(size) + " " + record;
			if (rec.size() != size) rec = std::to_string(rec.size()) + " " + record;
			std::size_t slash = name.rfind('/');
			std::string dir = slash == std::string::npos ? "" : name.substr(0, slash + 1);
			std::string base = slash == std::string::npos ? name : name.substr(slash + 1);
			std::string pax = go::clean(dir + "PaxHeaders.0/" + base);
			pax = to_ascii(pax);
			if (pax.size() > 100) pax.resize(100);
			while (!pax.empty() && pax.back() == '/') pax.pop_back();
			char h[512] = {};
			h[156] = 'x';
			put_string(h, 100, pax);
			put_octal(h + 100, 8, 0);
			put_octal(h + 108, 8, 0);
			put_octal(h + 116, 8, 0);
			put_octal(h + 124, 12, static_cast<std::int64_t>(rec.size()));
			put_octal(h + 136, 12, 0);
			finish_header(h, true);
			block(h);
			data(rec);
			pad();
			v7(b, to_ascii(name), static_cast<std::int64_t>(text.size()), mtime, '0');
			finish_header(b, true);
			block(b);
		}
		data(text);
	}

	void close()
	{
		pad();
		static const char zeros[1024] = {};
		gz.write(std::string_view(zeros, sizeof zeros));
		gz.write({}, Z_FINISH);
	}
};

std::string rfc3339(std::int64_t sec)
{
	auto t = tz::Zone::device().local(sec);
	char buf[40];
	std::snprintf(buf, sizeof buf, "%04d-%02d-%02dT%02d:%02d:%02d", t.year, t.month, t.day, t.hour, t.minute, t.second);
	std::string out = buf;
	if (t.offset == 0) return out + "Z";
	int off = t.offset < 0 ? -t.offset : t.offset;
	std::snprintf(buf, sizeof buf, "%c%02d:%02d", t.offset < 0 ? '-' : '+', off / 3600, off / 60 % 60);
	return out + buf;
}

} // namespace

std::optional<std::string> redact_settings(std::string_view data)
{
	notify::json::Value value;
	if (notify::json::unmarshal(data, value)) {
		std::string out;
		if (!write_redacted(value, out, 0, false)) return std::nullopt;
		return out + "\n";
	}
	return recovery::yamlv3::redact(data, secret_key);
}

std::optional<std::string> pack(const std::string &dir, const std::optional<std::string> &report, std::string note,
                                std::int64_t now_sec, std::int32_t now_nsec, std::string &error)
{
	std::vector<std::string> files;
	if (!walk(dir, files)) {
		error = "cannot read the collected files";
		return std::nullopt;
	}
	std::sort(files.begin(), files.end());

	Tar tar;
	if (!tar.gz.ok) {
		error = "cannot prepare the developer archive";
		return std::nullopt;
	}
	// The header time is rounded to the second (Writer.WriteHeader without a format).
	std::int64_t mtime = now_sec + (now_nsec >= 500000000 ? 1 : 0);
	auto known = known_ssids(paths().archive_etc);
	std::size_t raw = 0;
	auto add = [&](const std::string &name, std::string text) {
		if (!known.empty()) text = mask_ssids(text, known);
		raw += text.size();
		if (raw > raw_limit) return false;
		tar.file("nanokvm-dev-archive/" + name, text, mtime);
		return tar.gz.out.size() <= archive_limit;
	};
	std::vector<std::string> omitted;
	for (const auto &path : files) {
		std::string rel = path.substr(dir.size() + 1);
		if (frame_name(rel)) {
			omitted.push_back(rel);
			continue;
		}
		std::string data;
		if (!read_limited(path, file_limit, data) || binary_data(data)) {
			omitted.push_back(rel);
			continue;
		}
		// Text with stray NUL bytes: the device-tree model ends in one, and apk's log carries a few.
		std::erase(data, '\0');
		if (structured_name(rel)) {
			// Parsed, not matched line by line: a flow mapping or a block scalar would hide a credential from a line
			// rule. Already cleaned by key, so the line rules do not apply.
			auto clean = redact_settings(data);
			if (!clean) {
				omitted.push_back(rel);
				continue;
			}
			if (!add(rel, logs::mask_addresses(*clean))) {
				error = too_large;
				return std::nullopt;
			}
			continue;
		}
		if (!add(rel, logs::sanitize_file(data))) {
			error = too_large;
			return std::nullopt;
		}
	}
	if (report && !add("diagnostics.json", *report + "\n")) {
		error = too_large;
		return std::nullopt;
	}
	if (!omitted.empty()) {
		note += "\nOmitted as binary or unreadable: ";
		for (std::size_t i = 0; i < omitted.size(); i++) note += (i ? ", " : "") + omitted[i];
		note += "\n";
	}
	std::string collector = "the NanoKVM OS collector";
	if (std::string data; !go::read_file(go::join(dir, "collector"), data)) collector = go::trim_space(data);
	std::string stamp = rfc3339(now_sec);
	// fmt.Sprintf(readme, collector, created, note).
	std::string readme;
	std::string_view f = readme_format;
	for (const std::string *arg : {&collector, &stamp, &note}) {
		auto at = f.find("%s");
		readme.append(f.substr(0, at));
		readme += *arg;
		f.remove_prefix(at + 2);
	}
	readme.append(f);
	if (!add("README.txt", readme)) {
		error = too_large;
		return std::nullopt;
	}
	tar.close();
	if (tar.gz.out.size() > archive_limit) {
		error = too_large;
		return std::nullopt;
	}
	return std::move(tar.gz.out);
}

} // namespace nkos::system::diagnostics
