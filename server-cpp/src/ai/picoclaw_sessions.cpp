// PicoClaw's chat sessions as the web page lists them (session_history.go): the JSONL sessions of the workspace with
// their .meta.json, opaque "sk_v1_" sessions, and the legacy JSON files.

#include "ai/files.hpp"
#include "ai/picoclaw_internal.hpp"
#include "storage/go.hpp"

#include <algorithm>
#include <cerrno>
#include <dirent.h>
#include <set>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::ai::picoclaw {

using json::Value;

namespace {

constexpr const char *pico_prefix = "agent:main:pico:direct:pico:";
constexpr const char *sanitized_prefix = "agent_main_pico_direct_pico_";
constexpr const char *opaque_prefix = "sk_v1_";
constexpr std::size_t max_line = 10 * 1024 * 1024;
constexpr std::size_t preview_runes = 60;

// A time as Go keeps it: the instant and the offset it was written with (RFC 3339 output keeps it).
struct Time {
	bool zero = true;
	long long seconds = 0;  // Unix
	long offset = 0;        // seconds east of UTC
	std::string rfc3339() const
	{
		if (zero) return "0001-01-01T00:00:00Z";
		std::time_t t = static_cast<std::time_t>(seconds + offset);
		std::tm tm{};
		gmtime_r(&t, &tm);
		char buf[40];
		std::strftime(buf, sizeof buf, "%Y-%m-%dT%H:%M:%S", &tm);
		std::string out = buf;
		if (offset == 0) return out + "Z";
		char z[8];
		long a = offset < 0 ? -offset : offset;
		std::snprintf(z, sizeof z, "%c%02ld:%02ld", offset < 0 ? '-' : '+', (a / 3600) % 100, a % 3600 / 60);
		return out + z;
	}
};

// time.Time's UnmarshalJSON (RFC 3339).
bool parse_time(const std::string &s, Time &out)
{
	// Every field is digits at its fixed place: sscanf alone would take a sign or a space for one.
	auto digits = [&s](std::size_t from, std::size_t count) {
		if (from + count > s.size()) return false;
		for (std::size_t k = from; k < from + count; k++)
			if (!std::isdigit(static_cast<unsigned char>(s[k]))) return false;
		return true;
	};
	if (!digits(0, 4) || !digits(5, 2) || !digits(8, 2) || !digits(11, 2) || !digits(14, 2) || !digits(17, 2)) return false;
	int Y, M, D, h, m, sec, n = 0;
	if (std::sscanf(s.c_str(), "%4d-%2d-%2dT%2d:%2d:%2d%n", &Y, &M, &D, &h, &m, &sec, &n) != 6 || n != 19) return false;
	std::size_t i = 19;
	if (i < s.size() && s[i] == '.') {
		i++;
		while (i < s.size() && std::isdigit(static_cast<unsigned char>(s[i]))) i++;
	}
	long offset = 0;
	if (i < s.size() && (s[i] == 'Z' || s[i] == 'z') && i + 1 == s.size()) {
	} else if (i + 6 == s.size() && (s[i] == '+' || s[i] == '-') && s[i + 3] == ':') {
		if (!digits(i + 1, 2) || !digits(i + 4, 2)) return false;
		long oh = (s[i + 1] - '0') * 10 + (s[i + 2] - '0'), om = (s[i + 4] - '0') * 10 + (s[i + 5] - '0');
		if (oh > 23 || om > 59) return false;
		offset = (oh * 3600 + om * 60) * (s[i] == '-' ? -1L : 1L);
	} else {
		return false;
	}
	if (M < 1 || M > 12 || D < 1 || D > 31 || h > 23 || m > 59 || sec > 59) return false;
	std::tm tm{};
	tm.tm_year = Y - 1900;
	tm.tm_mon = M - 1;
	tm.tm_mday = D;
	tm.tm_hour = h;
	tm.tm_min = m;
	tm.tm_sec = sec;
	out.zero = false;
	out.seconds = static_cast<long long>(timegm(&tm)) - offset;
	out.offset = offset;
	if (Y == 1 && M == 1 && D == 1 && h == 0 && m == 0 && sec == 0 && offset == 0) out.zero = true;
	return true;
}

Time mod_time(const std::string &path, bool &ok)
{
	struct stat st{};
	ok = ::stat(path.c_str(), &st) == 0;
	Time t;
	if (!ok) return t;
	t.zero = false;
	t.seconds = st.st_mtim.tv_sec;
	std::time_t secs = st.st_mtim.tv_sec;
	std::tm tm{};
	localtime_r(&secs, &tm);
	t.offset = tm.tm_gmtoff;
	return t;
}

struct Message {
	std::string role, content;
};
struct Stored {
	std::string key, summary;
	std::vector<Message> messages;
	Time created, updated;
};

std::optional<std::string> session_file(const std::string &dir, const std::string &name)
{
	if (name.find_first_of("/\\") != std::string::npos) return std::nullopt;
	std::string d = storage::go::clean(dir);
	std::string path = storage::go::clean(storage::go::join(d, name));
	if (path.rfind(d + "/", 0) != 0) return std::nullopt;
	return path;
}

std::string sanitize(const std::string &key)
{
	std::string out = key;
	std::replace(out.begin(), out.end(), ':', '_');
	return out;
}

bool member_string(const Value &obj, const char *name, std::string &out, bool &ok)
{
	for (const auto &[k, x] : obj.obj)
		if (json::to_lower(k) == name) {
			if (x.is_string()) out = x.str;
			else if (!x.is_null()) ok = false;
		}
	return ok;
}

// readSessionMeta; errors fail the session.
bool read_meta(const std::string &path, const std::string &key, std::string &meta_key, std::string &summary, long long &skip, Time &created,
               Time &updated)
{
	meta_key = key;
	std::string data;
	int e = read_file(path, data);
	if (e == ENOENT) return true;
	if (e != 0) return false;
	Value v;
	if (json::parse(data, v)) return false;
	if (v.is_null()) return true;
	if (!v.is_object()) return false;
	bool ok = true;
	std::string k;
	for (const auto &[name, x] : v.obj) {
		std::string n = json::to_lower(name);
		if (n == "key" && !x.is_null()) {
			if (!x.is_string()) ok = false;
			else k = x.str;
		} else if (n == "summary" && !x.is_null()) {
			if (!x.is_string()) ok = false;
			else summary = x.str;
		} else if (n == "skip" && !x.is_null()) {
			if (!x.is_number() || x.str.find_first_of(".eE") != std::string::npos) ok = false;
			else skip = std::strtoll(x.str.c_str(), nullptr, 10);
		} else if ((n == "created_at" || n == "updated_at") && !x.is_null()) {
			Time t;
			if (!x.is_string() || !parse_time(x.str, t)) ok = false;
			else (n == "created_at" ? created : updated) = t;
		}
	}
	if (!ok) return false;
	if (!k.empty()) meta_key = k;
	return true;
}

bool read_messages(const std::string &path, long long skip, std::vector<Message> &out)
{
	std::string data;
	if (read_file(path, data) != 0) return false;
	long long seen = 0;
	std::size_t start = 0;
	while (start < data.size()) {
		auto nl = data.find('\n', start);
		std::string line = data.substr(start, nl == std::string::npos ? std::string::npos : nl - start);
		start = nl == std::string::npos ? data.size() : nl + 1;
		if (!line.empty() && line.back() == '\r') line.pop_back();
		if (line.size() > max_line) return false;  // bufio.Scanner: token too long
		if (line.empty()) continue;
		if (++seen <= skip) continue;
		Value v;
		if (json::parse(line, v)) continue;
		if (!v.is_object() && !v.is_null()) continue;
		Message m;
		bool ok = true;
		member_string(v, "role", m.role, ok);
		member_string(v, "content", m.content, ok);
		if (!ok) continue;
		out.push_back(m);
	}
	return true;
}

bool read_jsonl(const std::string &dir, const std::string &base_name, const std::string &key, Stored &s)
{
	auto base = session_file(dir, base_name);
	if (!base) return false;
	std::string jsonl = *base + ".jsonl";
	long long skip = 0;
	if (!read_meta(*base + ".meta.json", key, s.key, s.summary, skip, s.created, s.updated)) return false;
	if (!read_messages(jsonl, skip, s.messages)) return false;
	if (s.created.zero || s.updated.zero) {
		bool ok;
		Time t = mod_time(jsonl, ok);
		if (ok) {
			if (s.created.zero) s.created = t;
			if (s.updated.zero) s.updated = t;
		}
	}
	return true;
}

bool read_legacy(const std::string &dir, const std::string &file, Stored &s)
{
	auto path = session_file(dir, file);
	if (!path) return false;
	std::string data;
	if (read_file(*path, data) != 0) return false;
	Value v;
	if (json::parse(data, v)) return false;
	if (v.is_null()) return true;
	if (!v.is_object()) return false;
	bool ok = true;
	for (const auto &[name, x] : v.obj) {
		std::string n = json::to_lower(name);
		if (n == "key" && !x.is_null()) {
			if (!x.is_string()) ok = false;
			else s.key = x.str;
		} else if (n == "summary" && !x.is_null()) {
			if (!x.is_string()) ok = false;
			else s.summary = x.str;
		} else if (n == "messages" && !x.is_null()) {
			if (!x.is_array()) {
				ok = false;
				continue;
			}
			s.messages.clear();
			for (const auto &item : x.arr) {
				Message m;
				if (item.is_object()) {
					member_string(item, "role", m.role, ok);
					member_string(item, "content", m.content, ok);
				} else if (!item.is_null()) {
					ok = false;
				}
				s.messages.push_back(m);
			}
		} else if ((n == "created" || n == "updated") && !x.is_null()) {
			Time t;
			if (!x.is_string() || !parse_time(x.str, t)) ok = false;
			else (n == "created" ? s.created : s.updated) = t;
		}
	}
	return ok;
}

bool empty_session(const Stored &s) { return s.messages.empty() && json::trim_space(s.summary).empty(); }

std::string truncate(const std::string &s, std::size_t max)
{
	auto r = json::runes(json::trim_space(s));
	if (r.size() <= max) return json::utf8(r);
	r.resize(max);
	return json::utf8(r) + "...";
}

bool counted(const Message &m) { return (m.role == "user" || m.role == "assistant") && !json::trim_space(m.content).empty(); }

std::string list_item(const std::string &id, const Stored &s, std::string &updated)
{
	std::string preview;
	for (const auto &m : s.messages)
		if (m.role == "user" && !json::trim_space(m.content).empty()) {
			preview = m.content;
			break;
		}
	std::string title = json::trim_space(s.summary);
	if (title.empty()) title = preview;
	title = truncate(title, preview_runes);
	preview = truncate(preview, preview_runes);
	if (preview.empty()) preview = "(empty)";
	if (title.empty()) title = preview;
	int count = 0;
	for (const auto &m : s.messages)
		if (counted(m)) count++;
	updated = s.updated.rfc3339();
	return "{\"id\":" + json::quote(id) + ",\"title\":" + json::quote(title) + ",\"preview\":" + json::quote(preview) +
	       ",\"message_count\":" + std::to_string(count) + ",\"created\":\"" + s.created.rfc3339() + "\",\"updated\":\"" + updated + "\"}";
}

std::string sessions_dir(std::string &error)
{
	std::string w = workspace_path(error);
	if (!error.empty()) return {};
	return w + "/sessions";
}

std::optional<std::string> opaque_id(const std::string &file)
{
	std::string base;
	if (file.size() >= 10 && file.compare(file.size() - 10, 10, ".meta.json") == 0) base = file.substr(0, file.size() - 10);
	else base = file.substr(0, file.size() - storage::go::ext(file).size());
	if (base.rfind(opaque_prefix, 0) == 0) return base;
	return std::nullopt;
}

bool ends_with(const std::string &s, const char *suffix)
{
	std::string_view x(suffix);
	return s.size() >= x.size() && s.compare(s.size() - x.size(), x.size(), x) == 0;
}

void not_found(http::Response &res)
{
	auto e = make_error(code_runtime_unavailable, "session not found");
	e.status = 404;
	write_error(res, e);
}

} // namespace

void sessions_list(http::Request &req, http::Response &res)
{
	std::string error;
	std::string dir = sessions_dir(error);
	if (!error.empty()) return write_error(res, make_error(code_runtime_unavailable, "failed to resolve sessions directory"));
	DIR *d = ::opendir(dir.c_str());
	if (!d) {
		if (errno == ENOENT) return write_success(res, "[]");
		return write_error(res, make_error(code_runtime_unavailable, "failed to read sessions directory"));
	}
	std::vector<std::string> names;
	while (struct dirent *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n == "." || n == "..") continue;
		struct stat st{};
		if (::lstat((dir + "/" + n).c_str(), &st) == 0 && S_ISDIR(st.st_mode)) continue;
		names.push_back(n);
	}
	::closedir(d);
	std::sort(names.begin(), names.end());
	std::vector<std::pair<std::string, std::string>> items;  // updated, JSON
	std::set<std::string> seen;
	for (const auto &name : names) {
		std::string id;
		Stored s;
		bool loaded = false;
		if (ends_with(name, ".jsonl")) {
			std::string base = name.substr(0, name.size() - 6);
			if (auto o = opaque_id(name)) {
				id = *o;
				loaded = read_jsonl(dir, id, id, s);
			} else {
				if (base.rfind(sanitized_prefix, 0) != 0) continue;
				id = base.substr(std::string(sanitized_prefix).size());
				loaded = read_jsonl(dir, sanitize(pico_prefix + id), pico_prefix + id, s);
			}
			if (loaded && empty_session(s)) continue;
		} else if (ends_with(name, ".meta.json")) {
			continue;
		} else if (ends_with(name, ".json")) {
			std::string base = name.substr(0, name.size() - 5);
			struct stat st{};
			if (::stat((dir + "/" + base + ".jsonl").c_str(), &st) == 0 && base.rfind(sanitized_prefix, 0) == 0) {
				std::string jid = base.substr(std::string(sanitized_prefix).size());
				Stored j;
				if (read_jsonl(dir, sanitize(pico_prefix + jid), pico_prefix + jid, j) && !empty_session(j)) continue;
			}
			loaded = read_legacy(dir, name, s);
			if (loaded && empty_session(s)) continue;
			if (loaded) {
				if (s.key.rfind(pico_prefix, 0) != 0) continue;
				id = s.key.substr(std::string(pico_prefix).size());
			}
		} else {
			continue;
		}
		if (!loaded) continue;
		if (!seen.insert(id).second) continue;
		std::string updated;
		std::string item = list_item(id, s, updated);
		items.emplace_back(updated, item);
	}
	std::stable_sort(items.begin(), items.end(), [](const auto &a, const auto &b) { return a.first > b.first; });
	auto num = [&](const char *name, long long fallback) {
		std::string q = req.query;
		bool present = false;
		std::string value;
		std::size_t start = 0;
		while (start <= q.size()) {
			auto amp = q.find('&', start);
			std::string pair = q.substr(start, amp == std::string::npos ? std::string::npos : amp - start);
			auto eq = pair.find('=');
			if (http::url_decode(pair.substr(0, eq)) == name) {
				present = true;
				value = eq == std::string::npos ? "" : http::url_decode(pair.substr(eq + 1));
				break;
			}
			if (amp == std::string::npos) break;
			start = amp + 1;
		}
		if (!present) return fallback;
		// strconv.Atoi: an error gives 0.
		std::string v = value;
		std::size_t i = (!v.empty() && (v[0] == '+' || v[0] == '-')) ? 1 : 0;
		if (i >= v.size() || v.find_first_not_of("0123456789", i) != std::string::npos || v.size() - i > 18) return 0LL;
		return std::stoll(v);
	};
	long long offset = num("offset", 0), limit = num("limit", 20);
	if (offset < 0) offset = 0;
	if (limit <= 0) limit = 20;
	if (offset >= static_cast<long long>(items.size())) return write_success(res, "[]");
	long long end = std::min<long long>(offset + limit, static_cast<long long>(items.size()));
	std::string out = "[";
	for (long long i = offset; i < end; i++) out += (i > offset ? "," : "") + items[static_cast<std::size_t>(i)].second;
	write_success(res, out + "]");
}

void sessions_get(http::Request &req, http::Response &res)
{
	std::string id = json::trim_space(req.param("id"));
	if (id.empty()) return write_error(res, make_error(code_invalid_action, "missing session id"));
	std::string error;
	std::string dir = sessions_dir(error);
	if (!error.empty()) return write_error(res, make_error(code_runtime_unavailable, "failed to resolve sessions directory"));
	bool opaque = id.rfind(opaque_prefix, 0) == 0;
	Stored s;
	bool ok = opaque ? read_jsonl(dir, id, id, s) : read_jsonl(dir, sanitize(pico_prefix + id), pico_prefix + id, s);
	bool missing = false;
	if (ok && empty_session(s)) {
		ok = false;
		missing = true;
	}
	if (!ok) {
		// The legacy file is tried when the JSONL one does not exist (or is empty).
		if (!opaque) {
			bool exists = false;
			if (auto base = session_file(dir, sanitize(pico_prefix + id))) {
				struct stat st{};
				exists = ::stat((*base + ".jsonl").c_str(), &st) == 0;
			}
			if (missing || !exists) {
				s = {};
				ok = read_legacy(dir, sanitize(pico_prefix + id) + ".json", s) && !empty_session(s);
			}
		}
		if (!ok) return not_found(res);
	}
	std::string messages = "[";
	bool first = true;
	for (const auto &m : s.messages)
		if (counted(m)) {
			messages += (first ? "" : ",") + std::string("{\"role\":") + json::quote(m.role) + ",\"content\":" + json::quote(m.content) + "}";
			first = false;
		}
	messages += "]";
	std::string out = "{\"id\":" + json::quote(id) + ",\"messages\":" + messages;
	if (!s.summary.empty()) out += ",\"summary\":" + json::quote(s.summary);
	out += ",\"created\":\"" + s.created.rfc3339() + "\",\"updated\":\"" + s.updated.rfc3339() + "\"}";
	write_success(res, out);
}

void sessions_delete(http::Request &req, http::Response &res)
{
	std::string id = json::trim_space(req.param("id"));
	if (id.empty()) return write_error(res, make_error(code_invalid_action, "missing session id"));
	std::string error;
	std::string dir = sessions_dir(error);
	if (!error.empty()) return write_error(res, make_error(code_runtime_unavailable, "failed to resolve sessions directory"));
	std::string name = id.rfind(opaque_prefix, 0) == 0 ? id : sanitize(pico_prefix + id);
	auto base = session_file(dir, name);
	if (!base) return not_found(res);
	bool removed = false;
	for (const char *suffix : {".jsonl", ".meta.json", ".json"}) {
		if (::unlink((*base + suffix).c_str()) != 0) {
			if (errno == ENOENT) continue;
			return write_error(res, make_error(code_runtime_unavailable, "failed to delete session"));
		}
		removed = true;
	}
	if (!removed) return not_found(res);
	write_success(res, "{\"deleted\":true,\"id\":" + json::quote(id) + "}");
}

} // namespace nkos::ai::picoclaw
