// GET/POST /api/vm/memory/limit, as the Go server's service/vm/memory.go and utils/memory.go: the saved limit in MiB
// (/etc/kvm/GOMEMLIMIT, at least 50), the default without one (a quarter of the memory Linux keeps while 2160p
// capture holds its buffers), the limit in effect and the minimum. The setting belongs to the Go runtime's
// collector; this server has no such collector, so nothing applies it here and the limit in effect is none (0). The
// file is kept, written and removed as the Go server does, so the setting survives a return to it.

#include "system/diagnostics.hpp"

#include "notify/json.hpp"
#include "osupdate/gostr.hpp"
#include "storage/go.hpp"

#include <cerrno>
#include <charconv>
#include <climits>
#include <map>
#include <cstdio>
#include <mutex>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::system::diagnostics {

namespace {

namespace go = storage::go;

constexpr std::int64_t minimum_mib = 50;
std::mutex mutex;

// strconv.ParseInt(s, 10, 64).
std::optional<std::int64_t> parse_int(std::string_view s)
{
	bool neg = false;
	if (!s.empty() && (s[0] == '+' || s[0] == '-')) {
		neg = s[0] == '-';
		s.remove_prefix(1);
	}
	if (s.empty()) return std::nullopt;
	std::uint64_t v = 0;
	auto [end, ec] = std::from_chars(s.data(), s.data() + s.size(), v);
	if (ec != std::errc{} || end != s.data() + s.size()) return std::nullopt;
	if (neg ? v > static_cast<std::uint64_t>(INT64_MAX) + 1 : v > static_cast<std::uint64_t>(INT64_MAX)) return std::nullopt;
	return neg ? static_cast<std::int64_t>(0 - v) : static_cast<std::int64_t>(v);
}

// normalizeGoMemLimit.
std::optional<std::int64_t> normalize(std::int64_t limit)
{
	if (limit < 0 || limit > INT64_MAX / (1024 * 1024)) return std::nullopt;
	return std::max(limit, minimum_mib);
}

// The limit without a saved one, in bytes (INT64_MAX: none), from /proc/meminfo; read once, as Go does at start.
std::int64_t default_limit()
{
	static const std::int64_t limit = [] {
		std::string data;
		if (go::read_file(paths().meminfo, data)) return INT64_MAX;
		std::int64_t total = 0, cma = 0;
		std::size_t start = 0;
		for (;;) {
			std::size_t nl = data.find('\n', start);
			auto f = gostr::fields(std::string_view(data).substr(start, nl == std::string::npos ? std::string::npos : nl - start));
			if (f.size() >= 2) {
				auto kib = parse_int(f[1]);
				if (kib && *kib > 0) {
					if (f[0] == "MemTotal:") total = *kib * 1024;
					else if (f[0] == "CmaTotal:") cma = *kib * 1024;
				}
			}
			if (nl == std::string::npos) break;
			start = nl + 1;
		}
		if (total == 0) return INT64_MAX;
		constexpr std::int64_t video = 104 << 20;
		cma = std::min(cma, total);
		std::int64_t linux_memory = total - cma;
		if (cma > video) linux_memory += cma - video;
		return linux_memory / 4;
	}();
	return limit;
}

std::int64_t to_mib(std::int64_t limit) { return limit <= 0 || limit == INT64_MAX ? 0 : limit / (1024 * 1024); }

// goMemoryLimitStore.read: the saved limit, nullopt when the file cannot be read or parsed.
std::optional<std::int64_t> read_limit()
{
	std::string data;
	if (go::read_file(paths().memory_limit_file, data)) return std::nullopt;
	auto v = parse_int(go::trim_space(data));
	if (!v) return std::nullopt;
	return normalize(*v);
}

// url.ParseQuery: false on a malformed escape or a semicolon; the first value of each key is kept.
bool parse_query(std::string_view text, std::map<std::string, std::string> &out)
{
	auto unescape = [](std::string_view s, std::string &v) {
		v.clear();
		for (std::size_t i = 0; i < s.size(); i++) {
			if (s[i] == '+') {
				v += ' ';
			} else if (s[i] == '%') {
				unsigned x = 0;
				if (i + 2 >= s.size()) return false;
				auto [p, ec] = std::from_chars(s.data() + i + 1, s.data() + i + 3, x, 16);
				if (ec != std::errc{} || p != s.data() + i + 3) return false;
				v += static_cast<char>(x);
				i += 2;
			} else {
				v += s[i];
			}
		}
		return true;
	};
	bool ok = true;
	while (!text.empty()) {
		auto amp = text.find('&');
		std::string_view pair = text.substr(0, amp);
		text = amp == std::string_view::npos ? std::string_view{} : text.substr(amp + 1);
		if (pair.find(';') != std::string_view::npos) {
			ok = false;
			continue;
		}
		if (pair.empty()) continue;
		auto eq = pair.find('=');
		std::string key, value;
		if (!unescape(pair.substr(0, eq), key) ||
		    !unescape(eq == std::string_view::npos ? std::string_view{} : pair.substr(eq + 1), value)) {
			ok = false;
			continue;
		}
		out.emplace(std::move(key), std::move(value));
	}
	return ok;
}

} // namespace

std::optional<std::string> memory_limit_json()
{
	std::lock_guard lock(mutex);
	bool enabled = false;
	std::int64_t limit = 0;
	struct stat st{};
	if (::stat(paths().memory_limit_file.c_str(), &st) == 0) {
		auto saved = read_limit();
		if (!saved) return std::nullopt;
		enabled = true;
		limit = *saved;
	}
	return std::string("{\"enabled\":") + (enabled ? "true" : "false") + ",\"limit\":" + std::to_string(limit) +
	       ",\"default\":" + std::to_string(to_mib(default_limit())) + ",\"current\":0,\"minimum\":" +
	       std::to_string(minimum_mib) + "}";
}

bool set_memory_limit(bool enabled, std::int64_t limit)
{
	std::lock_guard lock(mutex);
	const std::string &path = paths().memory_limit_file;
	if (!enabled) {
		auto e = go::remove(path);
		return !e || e.sys == ENOENT;
	}
	auto value = normalize(limit);
	if (!value) return false;
	// Renamed within the directory: neither readers nor the next start see a truncated setting.
	int fd = -1;
	std::string temp;
	if (go::create_temp(go::dir(path), ".GOMEMLIMIT-*", fd, temp)) return false;
	std::string text = std::to_string(*value) + "\n";
	bool ok = ::fchmod(fd, 0644) == 0 && ::write(fd, text.data(), text.size()) == static_cast<ssize_t>(text.size());
	ok = ::close(fd) == 0 && ok;
	if (ok) ok = !go::rename(temp, path);
	if (!ok) ::unlink(temp.c_str());
	return ok;
}

bool bind_memory_limit(std::string_view content_type, std::string_view body, std::string_view query, bool &enabled,
                       std::int64_t &limit)
{
	enabled = false;
	limit = 0;
	std::string type(content_type);
	if (auto semi = type.find(';'); semi != std::string::npos) type.resize(semi);
	type = go::to_lower(go::trim_space(type));
	if (type == "application/json") {
		// json.Decoder.Decode of the first value into the struct (fields matched ignoring case).
		namespace j = notify::json;
		j::Value v;
		if (!j::decode_first(body, v)) return false;
		return j::members(v, {"Enabled", "Limit"}, [&](std::string_view k, const j::Value &m) {
			if (m.is_null()) return true;
			if (k == "Enabled") return j::get(m, enabled);
			if (m.kind != j::Value::Kind::number) return false;
			auto n = parse_int(m.text);
			if (!n) return false;
			limit = *n;
			return true;
		});
	}
	// Gin's form binding: a urlencoded body, then the query; fields by their Go names.
	std::map<std::string, std::string> form;
	bool ok = true;
	if (type == "application/x-www-form-urlencoded") ok = parse_query(body, form);
	ok = parse_query(query, form) && ok;
	if (!ok) return false;
	if (auto it = form.find("Enabled"); it != form.end()) {
		const std::string &t = it->second;
		if (t.empty() || t == "0" || t == "f" || t == "F" || t == "false" || t == "FALSE" || t == "False") enabled = false;
		else if (t == "1" || t == "t" || t == "T" || t == "true" || t == "TRUE" || t == "True") enabled = true;
		else return false;
	}
	if (auto it = form.find("Limit"); it != form.end()) {
		if (it->second.empty()) {
			limit = 0;
		} else {
			auto n = parse_int(it->second);
			if (!n) return false;
			limit = *n;
		}
	}
	return true;
}

} // namespace nkos::system::diagnostics
