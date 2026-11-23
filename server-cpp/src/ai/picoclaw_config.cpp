// PicoClaw's files as the Go server edits them: config.json (decoded into a struct for reading, kept as a generic
// map for writing back with json.MarshalIndent), .security.yml (yaml.v3 with indent 2), the runtime intent and
// AGENT.md (config_files.go, runtime_defaults.go, model_config.go, agent_profile.go, runtime_intent.go).

#include "ai/files.hpp"
#include "ai/picoclaw_internal.hpp"

#include <algorithm>
#include <cerrno>
#include <cmath>
#include <cstdlib>
#include <openssl/rand.h>
#include <pwd.h>
#include <regex>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::ai::picoclaw {

using json::Value;

namespace {

constexpr int config_version = 3;
constexpr const char *gateway_host = "127.0.0.1";
constexpr int gateway_port = 18790;

std::string dir_of(const std::string &path)
{
	auto slash = path.rfind('/');
	if (slash == std::string::npos) return ".";
	return slash == 0 ? "/" : path.substr(0, slash);
}

bool fold_equal(std::string_view a, std::string_view b) { return json::to_lower(a) == json::to_lower(b); }

const char *kind_name(const Value &v)
{
	switch (v.kind) {
	case Value::Kind::boolean: return "bool";
	case Value::Kind::number: return "number";
	case Value::Kind::string: return "string";
	case Value::Kind::array: return "array";
	case Value::Kind::object: return "object";
	default: return "null";
	}
}

// encoding/json into a struct: members matched case-insensitively, the last one wins; the first type error is kept.
struct Decoder {
	std::string error;
	// field: the path of a struct field; element: the path of an array element (no "Go struct field").
	void type_error(const Value &v, const std::string &field, const char *type, bool element = false)
	{
		if (!error.empty()) return;
		std::string what = kind_name(v);
		if (v.is_number() && (std::string_view(type) == "int" || std::string_view(type) == "string")) what = "number";
		if (v.is_number() && std::string_view(type) == "int") what = "number " + v.str;
		error = "json: cannot unmarshal " + what + " into " + (element ? "" : "Go struct field ") + "picoclawConfigFile." + field + " of type " + type;
	}
	void str(const Value &v, const std::string &field, std::string &out)
	{
		if (v.is_null()) return;
		if (!v.is_string()) return type_error(v, field, "string");
		out = v.str;
	}
	void boolean(const Value &v, const std::string &field, bool &out)
	{
		if (v.is_null()) return;
		if (!v.is_bool()) return type_error(v, field, "bool");
		out = v.b;
	}
	void integer(const Value &v, const std::string &field, long long &out)
	{
		if (v.is_null()) return;
		if (!v.is_number() || v.str.find_first_of(".eE") != std::string::npos) return type_error(v, field, "int");
		out = std::strtoll(v.str.c_str(), nullptr, 10);
	}
	template <typename F>
	void object(const Value &v, const std::string &field, const char *type, F &&each, bool element = false)
	{
		if (v.is_null()) return;
		if (!v.is_object()) return type_error(v, field, type, element);
		for (const auto &[k, x] : v.obj) each(k, x);
	}
};

void decode_settings(Decoder &d, const Value &v, const std::string &path, PicoSettings &s)
{
	d.object(v, path, "picoclaw.picoclawPicoSettingsV3", [&](const std::string &k, const Value &x) {
		if (fold_equal(k, "token")) d.str(x, path + ".token", s.token);
		else if (fold_equal(k, "allow_token_query")) d.boolean(x, path + ".allow_token_query", s.allow_token_query);
		else if (fold_equal(k, "ping_interval")) d.integer(x, path + ".ping_interval", s.ping_interval);
		else if (fold_equal(k, "read_timeout")) d.integer(x, path + ".read_timeout", s.read_timeout);
		else if (fold_equal(k, "write_timeout")) d.integer(x, path + ".write_timeout", s.write_timeout);
		else if (fold_equal(k, "max_connections")) d.integer(x, path + ".max_connections", s.max_connections);
	});
}

bool decode_config(const Value &v, ConfigFile &c, std::string &error)
{
	Decoder d;
	if (!v.is_object() && !v.is_null()) {
		error = std::string("json: cannot unmarshal ") + kind_name(v) + " into Go value of type picoclaw.picoclawConfigFile";
		return false;
	}
	for (const auto &[k, x] : v.obj) {
		if (fold_equal(k, "agents")) {
			d.object(x, "agents", "struct { Defaults struct { ModelName string \"json:\\\"model_name\\\"\"; Model string \"json:\\\"model,omitempty\\\"\"; Workspace string \"json:\\\"workspace,omitempty\\\"\" } \"json:\\\"defaults\\\"\" }",
			         [&](const std::string &k2, const Value &y) {
				         if (!fold_equal(k2, "defaults")) return;
				         d.object(y, "agents.defaults", "struct { ModelName string \"json:\\\"model_name\\\"\"; Model string \"json:\\\"model,omitempty\\\"\"; Workspace string \"json:\\\"workspace,omitempty\\\"\" }", [&](const std::string &k3, const Value &z) {
					         if (fold_equal(k3, "model_name")) d.str(z, "agents.defaults.model_name", c.model_name);
					         else if (fold_equal(k3, "model")) d.str(z, "agents.defaults.model", c.model);
					         else if (fold_equal(k3, "workspace")) d.str(z, "agents.defaults.workspace", c.workspace);
				         });
			         });
		} else if (fold_equal(k, "gateway")) {
			d.object(x, "gateway", "struct { Host string \"json:\\\"host\\\"\"; Port int \"json:\\\"port\\\"\" }", [&](const std::string &k2, const Value &y) {
				if (fold_equal(k2, "host")) d.str(y, "gateway.host", c.gateway_host);
				else if (fold_equal(k2, "port")) d.integer(y, "gateway.port", c.gateway_port);
			});
		} else if (fold_equal(k, "model_list")) {
			if (x.is_null()) {
				c.model_list.clear();
				continue;
			}
			if (!x.is_array()) {
				d.type_error(x, "model_list", "[]struct { ModelName string \"json:\\\"model_name\\\"\"; Model string \"json:\\\"model\\\"\"; APIBase string \"json:\\\"api_base\\\"\"; APIKey string \"json:\\\"api_key\\\"\"; APIKeys []string \"json:\\\"api_keys,omitempty\\\"\" }");
				continue;
			}
			c.model_list.clear();
			for (std::size_t n = 0; n < x.arr.size(); n++) {
				const Value &item = x.arr[n];
				std::string at = "model_list." + std::to_string(n);
				ModelEntry m;
				d.object(item, at, "struct { ModelName string \"json:\\\"model_name\\\"\"; Model string \"json:\\\"model\\\"\"; APIBase string \"json:\\\"api_base\\\"\"; APIKey string \"json:\\\"api_key\\\"\"; APIKeys []string \"json:\\\"api_keys,omitempty\\\"\" }", [&](const std::string &k2, const Value &y) {
					if (fold_equal(k2, "model_name")) d.str(y, at + ".model_name", m.model_name);
					else if (fold_equal(k2, "model")) d.str(y, at + ".model", m.model);
					else if (fold_equal(k2, "api_base")) d.str(y, at + ".api_base", m.api_base);
					else if (fold_equal(k2, "api_key")) d.str(y, at + ".api_key", m.api_key);
					else if (fold_equal(k2, "api_keys")) {
						if (y.is_null()) return;
						if (!y.is_array()) return d.type_error(y, at + ".api_keys", "[]string");
						m.api_keys.clear();
						for (const auto &s : y.arr) {
							std::string one;
							d.str(s, at + ".api_keys", one);
							m.api_keys.push_back(one);
						}
					}
				}, true);
				c.model_list.push_back(std::move(m));
			}
		} else if (fold_equal(k, "channel_list")) {
			d.object(x, "channel_list", "map[string]picoclaw.picoclawChannelEntry", [&](const std::string &name, const Value &y) {
				ChannelEntry e = c.channels.count(name) ? c.channels[name] : ChannelEntry{};
				d.object(y, "channel_list." + name, "picoclaw.picoclawChannelEntry", [&](const std::string &k2, const Value &z) {
					if (fold_equal(k2, "type")) d.str(z, "channel_list.type", e.type);
					else if (fold_equal(k2, "enabled")) d.boolean(z, "channel_list.enabled", e.enabled);
					else if (fold_equal(k2, "settings")) decode_settings(d, z, "channel_list.settings", e.settings);
				});
				c.channels[name] = e;
			});
		}
	}
	error = d.error;
	return error.empty();
}

// --- A small YAML reader for .security.yml (block mappings and sequences, quoted and plain scalars) ---

struct YNode {
	enum Kind { scalar, map, seq } kind = scalar;
	std::string text;
	bool null = false;
	std::vector<std::pair<std::string, YNode>> map_items;
	std::vector<YNode> seq_items;
};

struct YLine {
	int indent;
	std::string text;
};

std::string strip_comment(const std::string &s)
{
	char quote = 0;
	for (std::size_t i = 0; i < s.size(); i++) {
		char c = s[i];
		if (quote) {
			if (c == '\\' && quote == '"') i++;
			else if (c == quote) quote = 0;
			continue;
		}
		if (c == '\'' || c == '"') quote = c;
		else if (c == '#' && (i == 0 || s[i - 1] == ' ' || s[i - 1] == '\t')) return s.substr(0, i);
	}
	return s;
}

bool parse_scalar(std::string s, YNode &out, std::string &error)
{
	s = json::trim_space(s);
	out = YNode{};
	if (s.empty() || s == "~" || s == "null" || s == "Null" || s == "NULL") {
		out.null = true;
		return true;
	}
	if (s == "{}") {
		out.kind = YNode::map;
		return true;
	}
	if (s == "[]") {
		out.kind = YNode::seq;
		return true;
	}
	if (s[0] == '\'') {
		if (s.size() < 2 || s.back() != '\'') {
			error = "yaml: found unexpected end of stream";
			return false;
		}
		std::string body = s.substr(1, s.size() - 2), text;
		for (std::size_t i = 0; i < body.size(); i++) {
			if (body[i] == '\'' && i + 1 < body.size() && body[i + 1] == '\'') i++;
			text += body[i];
		}
		out.text = text;
		return true;
	}
	if (s[0] == '"') {
		if (s.size() < 2 || s.back() != '"') {
			error = "yaml: found unexpected end of stream";
			return false;
		}
		std::string body = s.substr(1, s.size() - 2), text;
		for (std::size_t i = 0; i < body.size(); i++) {
			char c = body[i];
			if (c != '\\' || i + 1 >= body.size()) {
				text += c;
				continue;
			}
			char e = body[++i];
			switch (e) {
			case 'n': text += '\n'; break;
			case 't': text += '\t'; break;
			case 'r': text += '\r'; break;
			case '0': text += '\0'; break;
			case '"': text += '"'; break;
			case '\\': text += '\\'; break;
			case '/': text += '/'; break;
			case 'x':
			case 'u':
			case 'U': {
				int n = e == 'x' ? 2 : e == 'u' ? 4 : 8;
				if (i + static_cast<std::size_t>(n) >= body.size() + 0 && i + n > body.size() - 1 + 1) break;
				unsigned long cp = std::strtoul(body.substr(i + 1, static_cast<std::size_t>(n)).c_str(), nullptr, 16);
				json::append_utf8(text, static_cast<char32_t>(cp));
				i += static_cast<std::size_t>(n);
				break;
			}
			default: text += e;
			}
		}
		out.text = text;
		return true;
	}
	if (s[0] == '[' && s.back() == ']') {
		out.kind = YNode::seq;
		std::string inner = s.substr(1, s.size() - 2);
		std::size_t start = 0;
		while (start <= inner.size()) {
			auto comma = inner.find(',', start);
			std::string item = json::trim_space(inner.substr(start, comma == std::string::npos ? std::string::npos : comma - start));
			if (!item.empty()) {
				YNode n;
				if (!parse_scalar(item, n, error)) return false;
				out.seq_items.push_back(n);
			}
			if (comma == std::string::npos) break;
			start = comma + 1;
		}
		return true;
	}
	out.text = s;
	return true;
}

// The key of "key: value" / "key:" (quoted keys allowed); npos when the line is no mapping entry.
std::size_t key_end(const std::string &s)
{
	char quote = 0;
	for (std::size_t i = 0; i < s.size(); i++) {
		char c = s[i];
		if (quote) {
			if (c == quote) quote = 0;
			continue;
		}
		if ((c == '\'' || c == '"') && i == 0) quote = c;
		else if (c == ':' && (i + 1 == s.size() || s[i + 1] == ' ' || s[i + 1] == '\t')) return i;
	}
	return std::string::npos;
}

bool parse_block(const std::vector<YLine> &lines, std::size_t &i, int indent, YNode &out, std::string &error)
{
	if (i >= lines.size() || lines[i].indent < indent) {
		out.null = true;
		return true;
	}
	int level = lines[i].indent;
	if (lines[i].text.rfind("- ", 0) == 0 || lines[i].text == "-") {
		out.kind = YNode::seq;
		while (i < lines.size() && lines[i].indent == level && (lines[i].text.rfind("- ", 0) == 0 || lines[i].text == "-")) {
			std::string rest = lines[i].text == "-" ? "" : lines[i].text.substr(2);
			i++;
			YNode item;
			if (json::trim_space(rest).empty()) {
				if (!parse_block(lines, i, level + 1, item, error)) return false;
			} else if (key_end(rest) != std::string::npos) {
				// A mapping in a sequence item: its first entry is on the dash line.
				std::vector<YLine> sub{{level + 2, rest}};
				while (i < lines.size() && lines[i].indent > level) sub.push_back(lines[i++]);
				std::size_t j = 0;
				if (!parse_block(sub, j, level + 2, item, error)) return false;
			} else if (!parse_scalar(rest, item, error)) {
				return false;
			}
			out.seq_items.push_back(item);
		}
		return true;
	}
	out.kind = YNode::map;
	while (i < lines.size() && lines[i].indent == level) {
		const std::string &t = lines[i].text;
		auto ke = key_end(t);
		if (ke == std::string::npos) {
			error = "yaml: line " + std::to_string(i + 1) + ": could not find expected ':'";
			return false;
		}
		YNode key;
		if (!parse_scalar(t.substr(0, ke), key, error)) return false;
		std::string rest = json::trim_space(t.substr(ke + 1));
		i++;
		YNode value;
		if (rest.empty()) {
			if (i < lines.size() && (lines[i].indent > level || (lines[i].indent == level && lines[i].text.rfind("- ", 0) == 0))) {
				if (!parse_block(lines, i, lines[i].indent, value, error)) return false;
			} else {
				value.null = true;
			}
		} else if (!parse_scalar(rest, value, error)) {
			return false;
		}
		out.map_items.emplace_back(key.text, value);
	}
	if (i < lines.size() && lines[i].indent > level) {
		error = "yaml: line " + std::to_string(i + 1) + ": mapping values are not allowed in this context";
		return false;
	}
	return true;
}

bool parse_yaml(const std::string &text, YNode &out, std::string &error)
{
	std::vector<YLine> lines;
	std::size_t start = 0;
	while (start < text.size()) {
		auto nl = text.find('\n', start);
		std::string line = text.substr(start, nl == std::string::npos ? std::string::npos : nl - start);
		start = nl == std::string::npos ? text.size() : nl + 1;
		if (!line.empty() && line.back() == '\r') line.pop_back();
		if (line == "---" || line == "...") continue;
		line = strip_comment(line);
		std::size_t ind = line.find_first_not_of(' ');
		if (ind == std::string::npos) continue;
		std::string body = line.substr(ind);
		while (!body.empty() && (body.back() == ' ' || body.back() == '\t')) body.pop_back();
		lines.push_back({static_cast<int>(ind), body});
	}
	std::size_t i = 0;
	if (lines.empty()) {
		out.null = true;
		return true;
	}
	if (lines.size() == 1 && key_end(lines[0].text) == std::string::npos) return parse_scalar(lines[0].text, out, error);
	if (!parse_block(lines, i, lines[0].indent, out, error)) return false;
	if (i < lines.size()) {
		error = "yaml: line " + std::to_string(i + 1) + ": did not find expected key";
		return false;
	}
	return true;
}

bool load_security(const std::string &path, Security &sec, std::string &error)
{
	sec = {};
	std::string data;
	int e = read_file(path, data);
	if (e == ENOENT) return true;
	if (e != 0) {
		error = "failed to read picoclaw security config: open " + path + ": " + errno_text(e);
		return false;
	}
	YNode root;
	std::string perr;
	if (!parse_yaml(data, root, perr)) {
		error = "failed to parse picoclaw security config: " + perr;
		return false;
	}
	if (root.null) return true;
	if (root.kind != YNode::map) {
		error = "failed to parse picoclaw security config: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `" + root.text +
		        "` into picoclaw.picoclawSecurityConfig";
		return false;
	}
	for (const auto &[k, v] : root.map_items) {
		if (k == "model_list" && v.kind == YNode::map) {
			sec.has_model_list = true;
			for (const auto &[name, entry] : v.map_items) {
				std::vector<std::string> keys;
				for (const auto &[f, x] : entry.map_items)
					if (f == "api_keys")
						for (const auto &item : x.seq_items) keys.push_back(item.text);
				sec.model_list[name] = keys;
			}
		} else if (k == "channel_list" && v.kind == YNode::map) {
			sec.has_channel_list = true;
			for (const auto &[name, entry] : v.map_items) {
				SecurityChannel ch;
				for (const auto &[f, x] : entry.map_items) {
					if (f == "token") ch.token = x.text;
					else if (f == "settings" && !x.null) {
						SecurityChannelSettings s;
						for (const auto &[g, y] : x.map_items)
							if (g == "token") s.token = y.text;
						ch.settings = s;
					}
				}
				sec.channel_list[name] = ch;
			}
		}
	}
	return true;
}

// --- yaml.v3's writing of strings ---

bool yaml_resolves_non_string(const std::string &s)
{
	static const std::regex patterns(
	    R"(^(~|null|Null|NULL|true|True|TRUE|false|False|FALSE|y|Y|yes|Yes|YES|n|N|no|No|NO|on|On|ON|off|Off|OFF)$|)"
	    R"(^[-+]?(0|[1-9][0-9_]*)$|^[-+]?0[0-7_]+$|^[-+]?0x[0-9a-fA-F_]+$|^[-+]?0o[0-7_]+$|^[-+]?0b[01_]+$|)"
	    R"(^[-+]?(\.[0-9]+|[0-9][0-9_]*(\.[0-9_]*)?)([eE][-+]?[0-9]+)?$|^[-+]?\.(inf|Inf|INF)$|^\.(nan|NaN|NAN)$|)"
	    R"(^[0-9][0-9][0-9][0-9]-[0-9][0-9]?-[0-9][0-9]?([Tt ].*)?$)");
	return std::regex_search(s, patterns);
}

std::string yaml_scalar(const std::string &s)
{
	bool special = false;
	for (unsigned char c : s)
		if (c < 0x20 || c == 0x7f) special = true;
	if (s.empty() || special || yaml_resolves_non_string(s)) {
		static const char hex[] = "0123456789ABCDEF";
		std::string out = "\"";
		for (unsigned char c : s) {
			switch (c) {
			case '"': out += "\\\""; break;
			case '\\': out += "\\\\"; break;
			case '\n': out += "\\n"; break;
			case '\t': out += "\\t"; break;
			case '\r': out += "\\r"; break;
			default:
				if (c < 0x20 || c == 0x7f) {
					out += "\\x";
					out += hex[c >> 4];
					out += hex[c & 15];
				} else {
					out += static_cast<char>(c);
				}
			}
		}
		return out + "\"";
	}
	bool single = std::string_view("#,[]{}&*!|>'\"%@`").find(s[0]) != std::string_view::npos || s.front() == ' ' ||
	              s.back() == ' ' || s.back() == ':' || s == "-" || s.rfind("- ", 0) == 0 || s.rfind("? ", 0) == 0 ||
	              s.rfind(": ", 0) == 0 || s.find(": ") != std::string::npos || s.find(" #") != std::string::npos ||
	              s.rfind("---", 0) == 0 || s.rfind("...", 0) == 0;
	if (!single) return s;
	std::string out = "'";
	for (char c : s) {
		if (c == '\'') out += "''";
		else out += c;
	}
	return out + "'";
}

bool is_letter(char32_t r) { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80; }
bool is_digit(char32_t r) { return r >= '0' && r <= '9'; }

// yaml.v3's order of map keys (keyList.Less): digit runs compare as numbers.
bool yaml_less(const std::string &a, const std::string &b)
{
	auto ar = json::runes(a), br = json::runes(b);
	bool digits = false;
	for (std::size_t i = 0; i < ar.size() && i < br.size(); i++) {
		if (ar[i] == br[i]) {
			digits = is_digit(ar[i]);
			continue;
		}
		bool al = is_letter(ar[i]), bl = is_letter(br[i]);
		if (al && bl) return ar[i] < br[i];
		if (al || bl) return digits ? al : bl;
		std::size_t ai, bi;
		long long an = 0, bn = 0;
		if (ar[i] == '0' || br[i] == '0') {
			for (std::size_t j = i; j-- > 0 && is_digit(ar[j]);)
				if (ar[j] != '0') {
					an = bn = 1;
					break;
				}
		}
		for (ai = i; ai < ar.size() && is_digit(ar[ai]); ai++) an = an * 10 + (ar[ai] - '0');
		for (bi = i; bi < br.size() && is_digit(br[bi]); bi++) bn = bn * 10 + (br[bi] - '0');
		if (an != bn) return an < bn;
		if (ai != bi) return ai < bi;
		return ar[i] < br[i];
	}
	return ar.size() < br.size();
}

template <typename M>
std::vector<std::string> yaml_keys(const M &m)
{
	std::vector<std::string> keys;
	for (const auto &[k, v] : m) keys.push_back(k);
	std::stable_sort(keys.begin(), keys.end(), yaml_less);
	return keys;
}

std::string security_yaml(const Security &s)
{
	std::string out;
	if (!s.model_list.empty()) {
		out += "model_list:\n";
		for (const auto &name : yaml_keys(s.model_list)) {
			const auto &keys = s.model_list.at(name);
			if (keys.empty()) {
				out += "  " + yaml_scalar(name) + ": {}\n";
				continue;
			}
			out += "  " + yaml_scalar(name) + ":\n    api_keys:\n";
			for (const auto &k : keys) out += "      - " + yaml_scalar(k) + "\n";
		}
	}
	if (!s.channel_list.empty()) {
		out += "channel_list:\n";
		for (const auto &name : yaml_keys(s.channel_list)) {
			const auto &ch = s.channel_list.at(name);
			if (ch.token.empty() && !ch.settings) {
				out += "  " + yaml_scalar(name) + ": {}\n";
				continue;
			}
			out += "  " + yaml_scalar(name) + ":\n";
			if (!ch.token.empty()) out += "    token: " + yaml_scalar(ch.token) + "\n";
			if (ch.settings) {
				if (ch.settings->token.empty()) out += "    settings: {}\n";
				else out += "    settings:\n      token: " + yaml_scalar(ch.settings->token) + "\n";
			}
		}
	}
	return out.empty() ? "{}\n" : out;
}

std::string random_hex(std::size_t bytes)
{
	std::vector<unsigned char> buf(bytes);
	RAND_bytes(buf.data(), static_cast<int>(buf.size()));
	static const char hex[] = "0123456789abcdef";
	std::string out;
	for (unsigned char c : buf) {
		out += hex[c >> 4];
		out += hex[c & 15];
	}
	return out;
}

// --- Editing the generic config (picoclawConfigEditor) ---

struct Editor {
	Value &raw;
	bool changed = false;

	Value &ensure_object(const std::vector<std::string> &path)
	{
		Value *current = &raw;
		for (const auto &key : path) {
			Value *next = current->get(key);
			if (!next || !next->is_object()) {
				current->set(key, Value::object());
				next = current->get(key);
				changed = true;
			}
			current = next;
		}
		return *current;
	}
	// go_int: the default is a Go int, which never equals a decoded number (float64): always written.
	void set_value(const Value &value, const std::vector<std::string> &path, bool go_int = false)
	{
		std::vector<std::string> parent(path.begin(), path.end() - 1);
		Value &target = ensure_object(parent);
		const Value *current = target.get(path.back());
		if (!go_int && current && deep_equal(*current, value)) return;
		target.set(path.back(), value);
		changed = true;
	}
	static bool deep_equal(const Value &a, const Value &b)
	{
		if (a.kind != b.kind) return false;
		switch (a.kind) {
		case Value::Kind::null: return true;
		case Value::Kind::boolean: return a.b == b.b;
		case Value::Kind::number: return a.num == b.num;
		case Value::Kind::string: return a.str == b.str;
		case Value::Kind::array:
			if (a.arr.size() != b.arr.size()) return false;
			for (std::size_t i = 0; i < a.arr.size(); i++)
				if (!deep_equal(a.arr[i], b.arr[i])) return false;
			return true;
		case Value::Kind::object: {
			Value x = a, y = b;
			json::sort_keys(x);
			json::sort_keys(y);
			if (x.obj.size() != y.obj.size()) return false;
			for (std::size_t i = 0; i < x.obj.size(); i++)
				if (x.obj[i].first != y.obj[i].first || !deep_equal(x.obj[i].second, y.obj[i].second)) return false;
			return true;
		}
		}
		return false;
	}
};

void force_enable_pico_channel(Document &doc, Editor &editor)
{
	auto it = doc.config.channels.find("pico");
	if (it != doc.config.channels.end() && it->second.enabled) return;
	editor.set_value(Value::boolean(true), {"channel_list", "pico", "enabled"});
	doc.config.channels["pico"].enabled = true;
}

bool host_tools_enabled()
{
	std::string error;
	auto intent = intent_load(error);
	return error.empty() && intent.host_tools;
}

std::string startup_defaults(Editor &editor)
{
	editor.set_value(Value::number(config_version), {"version"}, true);
	editor.set_value(Value::boolean(true), {"agents", "defaults", "tool_feedback", "enabled"});
	editor.set_value(Value::string(gateway_host), {"gateway", "host"});
	editor.set_value(Value::number(gateway_port), {"gateway", "port"}, true);
	editor.set_value(Value::boolean(false), {"gateway", "hot_reload"});
	editor.set_value(Value::string("pico"), {"channel_list", "pico", "type"});
	editor.set_value(Value::boolean(false), {"channel_list", "pico", "settings", "allow_token_query"});
	editor.set_value(Value::number(30), {"channel_list", "pico", "settings", "ping_interval"}, true);
	editor.set_value(Value::number(60), {"channel_list", "pico", "settings", "read_timeout"}, true);
	editor.set_value(Value::number(10), {"channel_list", "pico", "settings", "write_timeout"}, true);
	editor.set_value(Value::number(100), {"channel_list", "pico", "settings", "max_connections"}, true);
	editor.set_value(Value::boolean(true), {"tools", "mcp", "enabled"});
	bool enabled = host_tools_enabled();
	editor.set_value(Value::boolean(!enabled), {"agents", "defaults", "restrict_to_workspace"});
	editor.set_value(Value::boolean(enabled), {"agents", "defaults", "allow_read_outside_workspace"});
	editor.set_value(Value::boolean(enabled), {"tools", "exec", "enabled"});
	editor.set_value(Value::boolean(enabled), {"tools", "exec", "allow_remote"});
	editor.set_value(Value::boolean(!enabled), {"tools", "exec", "enable_deny_patterns"});
	editor.set_value(Value::boolean(enabled), {"tools", "write_file", "enabled"});
	editor.set_value(Value::boolean(enabled), {"tools", "edit_file", "enabled"});
	editor.set_value(Value::boolean(enabled), {"tools", "append_file", "enabled"});
	editor.set_value(Value::boolean(enabled), {"tools", "cron", "allow_command"});
	std::string token, error;
	if (!internal_token(token, error)) return error;
	Value headers = Value::object();
	headers.set(internal_token_header, Value::string(token));
	int port = platform().http_port > 0 ? platform().http_port : 80;
	std::vector<std::pair<std::string, Value>> fields = {
	    {"enabled", Value::boolean(true)},
	    {"type", Value::string("http")},
	    {"url", Value::string("http://127.0.0.1:" + std::to_string(port) + "/api/picoclaw/mcp")},
	    {"headers", headers},
	};
	Value &servers = editor.ensure_object({"tools", "mcp", "servers"});
	Value *entry = servers.get("nanokvm");
	if (!entry || !entry->is_object()) {
		servers.set("nanokvm", Value::object());
		entry = servers.get("nanokvm");
		editor.changed = true;
	}
	for (const auto &[k, v] : fields) {
		const Value *cur = entry->get(k);
		if (cur && Editor::deep_equal(*cur, v)) continue;
		entry->set(k, v);
		editor.changed = true;
	}
	return {};
}

bool ensure_pico_token(Document &doc)
{
	std::string token;
	if (auto it = doc.security.channel_list.find("pico"); it != doc.security.channel_list.end()) {
		if (it->second.settings) token = json::trim_space(it->second.settings->token);
		if (token.empty()) token = json::trim_space(it->second.token);
	}
	if (token.empty())
		if (auto c = doc.config.channels.find("pico"); c != doc.config.channels.end()) token = json::trim_space(c->second.settings.token);
	if (token.empty()) token = random_hex(24);
	SecurityChannel entry = doc.security.channel_list.count("pico") ? doc.security.channel_list["pico"] : SecurityChannel{};
	SecurityChannelSettings settings = entry.settings ? *entry.settings : SecurityChannelSettings{};
	if (json::trim_space(settings.token) == token) return false;
	settings.token = token;
	entry.settings = settings;
	doc.security.channel_list["pico"] = entry;
	return true;
}

std::string target_model_name(const ConfigFile &c) { return json::trim_space(c.model_name); }

bool keyless_provider(const std::string &model)
{
	std::string m = json::trim_space(model);
	auto slash = m.find('/');
	if (slash == std::string::npos) return false;
	std::string provider = json::to_lower(json::trim_space(m.substr(0, slash)));
	return provider == "lmstudio" || provider == "ollama" || provider == "vllm";
}

bool model_configured(const ConfigFile &cfg, const Security &sec, const std::string &name)
{
	if (name.empty()) return false;
	for (const auto &m : cfg.model_list) {
		if (json::trim_space(m.model_name) != name) continue;
		if (m.api_base.empty()) continue;
		if (keyless_provider(m.model)) return true;
		if (!sec.model_list.empty()) {
			if (auto it = sec.model_list.find(name); it != sec.model_list.end() && !it->second.empty()) return true;
			for (const auto &[k, v] : sec.model_list)
				if (k.rfind(name + ":", 0) == 0 && !v.empty()) return true;
		}
		return !json::trim_space(m.api_key).empty() || !m.api_keys.empty();
	}
	return false;
}

std::string extract_model_name(const std::string &model)
{
	std::string m = json::trim_space(model);
	if (m.empty()) return {};
	auto slash = m.rfind('/');
	if (slash != std::string::npos && slash < m.size() - 1) return json::trim_space(m.substr(slash + 1));
	return m;
}

const std::vector<std::string> known_providers = {
    "anthropic", "azure", "azure_openai", "baichuan", "bedrock", "cerebras", "cohere", "dashscope", "deepseek", "fireworks_ai",
    "gemini", "google", "groq", "lmstudio", "mistral", "moonshot", "ollama", "openai", "openai_compatible", "openrouter",
    "qwen", "siliconflow", "together_ai", "vertex_ai", "vllm", "volcengine", "xai", "zhipu"};

bool validate_model(const std::string &model_in, std::string &name, std::string &error)
{
	std::string model = json::trim_space(model_in);
	auto slash = model.find('/');
	std::string provider = slash == std::string::npos ? model : model.substr(0, slash);
	provider = json::to_lower(json::trim_space(provider));
	std::string ref = slash == std::string::npos ? "" : json::trim_space(model.substr(slash + 1));
	if (slash == std::string::npos || provider.empty() || ref.empty()) {
		error = "model identifier must use provider/model format";
		return false;
	}
	static const std::regex pattern("^[a-z][a-z0-9_-]*$");
	if (!std::regex_match(provider, pattern)) {
		error = "model provider " + json::go_quote(provider) + " is invalid";
		return false;
	}
	if (provider == "openao") {
		error = "model provider \"openao\" is invalid; did you mean openai?";
		return false;
	}
	if (std::find(known_providers.begin(), known_providers.end(), provider) == known_providers.end()) {
		error = "model provider " + json::go_quote(provider) + " is not supported by this PicoClaw integration";
		return false;
	}
	name = extract_model_name(model);
	if (name.empty()) {
		error = "model identifier must include a model name";
		return false;
	}
	return true;
}

// fmt.Sprintf("%v", x) of a model_name member, trimmed.
std::string v_text(const Value *v) { return json::trim_space(v ? json::go_v(*v) : std::string("<nil>")); }

std::string indexed_model_name(const Value &list, std::size_t target, const std::string &name)
{
	std::size_t current = 0;
	for (std::size_t i = 0; i < list.arr.size(); i++) {
		const Value &item = list.arr[i];
		if (!item.is_object()) continue;
		if (v_text(item.get("model_name")) != name) continue;
		if (i == target) return name + ":" + std::to_string(current);
		current++;
	}
	return name;
}

} // namespace

// --- Paths ---

std::string home(std::string &error)
{
	error.clear();
	if (const char *h = std::getenv("PICOCLAW_HOME"); h && !json::trim_space(h).empty()) return json::trim_space(h);
	if (struct passwd *pw = getpwuid(getuid()); pw && pw->pw_dir && *pw->pw_dir) return std::string(pw->pw_dir) + "/.picoclaw";
	if (const char *h = std::getenv("HOME"); h && !json::trim_space(h).empty()) return json::trim_space(h) + "/.picoclaw";
	return "/root/.picoclaw";
}

std::string config_path(std::string &error)
{
	std::string h = home(error);
	if (!error.empty()) return {};
	return h + "/config.json";
}

std::optional<Document> load_document(std::string &error, bool *not_exist)
{
	if (not_exist) *not_exist = false;
	std::string path = config_path(error);
	if (!error.empty()) return std::nullopt;
	std::string data;
	int e = read_file(path, data);
	if (e != 0) {
		if (not_exist) *not_exist = e == ENOENT;
		error = "failed to read picoclaw config: open " + path + ": " + errno_text(e);
		return std::nullopt;
	}
	Document doc;
	doc.config_path = path;
	if (auto err = json::parse(data, doc.raw)) {
		error = "failed to parse picoclaw config: " + err->message;
		return std::nullopt;
	}
	std::string derr;
	if (!decode_config(doc.raw, doc.config, derr)) {
		error = "failed to parse picoclaw config: " + derr;
		return std::nullopt;
	}
	if (!doc.raw.is_object()) {
		if (doc.raw.is_null()) doc.raw = Value::object();
		else {
			error = "failed to parse picoclaw config for update: json: cannot unmarshal " + std::string(kind_name(doc.raw)) +
			        " into Go value of type map[string]interface {}";
			return std::nullopt;
		}
	}
	doc.security_path = dir_of(path) + "/.security.yml";
	if (!load_security(doc.security_path, doc.security, error)) return std::nullopt;
	return doc;
}

std::string Document::save_config()
{
	Value sorted = raw;
	json::sort_keys(sorted);
	std::string data = json::marshal_indent(sorted, "  ");
	std::string err = save_file_plain(config_path, data);
	return err.empty() ? std::string() : "failed to write picoclaw config: " + err;
}

std::string Document::save_security()
{
	std::string err = save_file_plain(security_path, security_yaml(security));
	return err.empty() ? std::string() : "failed to write picoclaw security config: " + err;
}

std::string Document::resolved_gateway_token() const
{
	std::string token;
	if (auto it = security.channel_list.find("pico"); it != security.channel_list.end()) {
		if (it->second.settings && !it->second.settings->token.empty()) token = it->second.settings->token;
		else if (!it->second.token.empty()) token = it->second.token;
		else if (auto c = config.channels.find("pico"); c != config.channels.end()) token = c->second.settings.token;
	} else if (auto c = config.channels.find("pico"); c != config.channels.end()) {
		token = c->second.settings.token;
	}
	return json::trim_space(token);
}

std::optional<GatewaySettings> load_gateway_settings(std::string &error)
{
	auto doc = load_document(error);
	if (!doc) return std::nullopt;
	// ensurePicoclawPicoChannelEnabled: a disabled pico channel is switched on in the file.
	if (auto it = doc->config.channels.find("pico"); it == doc->config.channels.end() || !it->second.enabled) {
		Editor editor{doc->raw};
		force_enable_pico_channel(*doc, editor);
		if (editor.changed)
			if (auto e = doc->save_config(); !e.empty()) {
				error = e;
				return std::nullopt;
			}
	}
	const ConfigFile &cfg = doc->config;
	std::string host = cfg.gateway_host;
	if (host.empty() || host == "0.0.0.0") host = gateway_host;
	long long port = cfg.gateway_port <= 0 ? gateway_port : cfg.gateway_port;
	PicoSettings pico;
	if (auto it = cfg.channels.find("pico"); it != cfg.channels.end()) pico = it->second.settings;
	GatewaySettings s;
	s.gateway_url = "ws://" + host + ":" + std::to_string(port) + "/pico/ws";
	s.token = doc->resolved_gateway_token();
	s.allow_token_query = pico.allow_token_query;
	s.target_model_name = target_model_name(cfg);
	if (model_configured(cfg, doc->security, s.target_model_name)) {
		s.model_configured = true;
		s.model_name = s.target_model_name;
	}
	if (pico.ping_interval > 0) s.ping_interval_ms = static_cast<int>(pico.ping_interval * 1000);
	if (pico.read_timeout > 0) s.read_timeout_ms = static_cast<int>(pico.read_timeout * 1000);
	return s;
}

std::string ensure_startup_defaults(bool *not_exist)
{
	std::string error;
	auto doc = load_document(error, not_exist);
	if (!doc) return error;
	Editor editor{doc->raw};
	force_enable_pico_channel(*doc, editor);
	if (auto e = startup_defaults(editor); !e.empty()) return e;
	bool token_changed = ensure_pico_token(*doc);
	if (token_changed)
		if (auto e = doc->save_security(); !e.empty()) return e;
	if (editor.changed)
		if (auto e = doc->save_config(); !e.empty()) return e;
	return {};
}

std::string update_model_config(const std::string &api_base, const std::string &api_key, const std::string &model, std::string &error)
{
	if (api_base.empty()) {
		error = "model api_base is required";
		return {};
	}
	if (model.empty()) {
		error = "model identifier is required";
		return {};
	}
	std::string name;
	if (!validate_model(model, name, error)) return {};
	if (api_key.empty() && !keyless_provider(model)) {
		error = "model api_key is required";
		return {};
	}
	bool not_exist = false;
	auto doc = load_document(error, &not_exist);
	if (!doc) {
		if (!not_exist) return {};
		auto onboard = onboard_for_config();
		if (onboard) {
			error = "failed to initialize PicoClaw config before saving model config: " + onboard->text();
			return {};
		}
		doc = load_document(error);
		if (!doc) {
			error = "failed to load PicoClaw config after initialization: " + error;
			return {};
		}
	}
	error.clear();
	doc->raw.set("version", Value::number(config_version));
	Value list = Value::array();
	if (const Value *l = doc->raw.get("model_list"); l && l->is_array()) list = *l;
	std::size_t index = 0;
	bool updated = false;
	for (std::size_t i = 0; i < list.arr.size(); i++) {
		Value &item = list.arr[i];
		if (!item.is_object()) continue;
		if (v_text(item.get("model_name")) != name) continue;
		item.set("model_name", Value::string(name));
		item.set("model", Value::string(model));
		item.set("api_base", Value::string(api_base));
		item.erase("api_key");
		item.erase("api_keys");
		updated = true;
		index = i;
		break;
	}
	if (!updated) {
		Value item = Value::object();
		item.set("model_name", Value::string(name));
		item.set("model", Value::string(model));
		item.set("api_base", Value::string(api_base));
		list.arr.push_back(item);
		index = list.arr.size() - 1;
	}
	// The list is written back when it was changed in place too (Go's slice shares the map's array).
	if (const Value *l = doc->raw.get("model_list"); updated && l && l->is_array()) doc->raw.set("model_list", list);
	else if (!updated) doc->raw.set("model_list", list);
	Value *agents = doc->raw.get("agents");
	if (!agents || !agents->is_object()) {
		doc->raw.set("agents", Value::object());
		agents = doc->raw.get("agents");
	}
	Value *defaults = agents->get("defaults");
	if (!defaults || !defaults->is_object()) {
		agents->set("defaults", Value::object());
		defaults = agents->get("defaults");
	}
	defaults->set("model_name", Value::string(name));
	defaults->erase("model");
	if (auto e = doc->save_config(); !e.empty()) {
		error = e;
		return {};
	}
	std::string security_name = indexed_model_name(list, index, name);
	if (api_key.empty()) {
		doc->security.model_list.erase(security_name);
		doc->security.model_list.erase(name);
	} else {
		doc->security.model_list[security_name] = {api_key};
	}
	if (auto e = doc->save_security(); !e.empty()) {
		error = e;
		return {};
	}
	return name;
}

std::string workspace_path(std::string &error)
{
	std::string path = config_path(error);
	if (!error.empty()) return {};
	std::string ignored;
	if (auto doc = load_document(ignored)) {
		std::string w = json::trim_space(doc->config.workspace);
		if (!w.empty()) {
			if (w == "~" || w.rfind("~/", 0) == 0) {
				if (const char *h = std::getenv("HOME"); h && *h) w = w == "~" ? std::string(h) : std::string(h) + "/" + w.substr(2);
			}
			return w;
		}
	}
	return dir_of(path) + "/workspace";
}

std::string detect_agent_profile()
{
	std::string error;
	std::string w = workspace_path(error);
	if (!error.empty()) return "kvm";
	std::string data;
	if (read_file(w + "/AGENT.md", data) != 0) return "kvm";
	std::size_t start = 0;
	while (start <= data.size()) {
		auto nl = data.find('\n', start);
		std::string line = json::trim_space(data.substr(start, nl == std::string::npos ? std::string::npos : nl - start));
		if (line.rfind("name:", 0) == 0) {
			std::string name = json::trim_space(line.substr(5));
			return name == "pico" ? "default" : "kvm";
		}
		if (nl == std::string::npos) break;
		start = nl + 1;
	}
	return "kvm";
}

std::string apply_agent_profile(const std::string &profile)
{
	std::string file = profile == "default" ? "AGENT.md" : profile == "kvm" ? "AGENT_KVM.md" : "";
	if (file.empty()) return "unsupported agent profile: " + profile;
	std::vector<std::string> candidates;
	char cwd[4096];
	if (getcwd(cwd, sizeof cwd)) {
		candidates.push_back(std::string(cwd) + "/picoclaw/" + file);
		candidates.push_back(std::string(cwd) + "/docs/" + file);
	}
	candidates.push_back("/kvmapp/picoclaw/" + file);
	char exe[4096];
	ssize_t n = readlink("/proc/self/exe", exe, sizeof exe - 1);
	if (n > 0) candidates.push_back(dir_of(std::string(exe, static_cast<std::size_t>(n))) + "/../picoclaw/" + file);
	std::string source;
	struct stat st{};
	for (const auto &c : candidates)
		if (::stat(c.c_str(), &st) == 0) {
			source = c;
			break;
		}
	if (source.empty()) return "agent profile source not found: " + file;
	std::string error;
	std::string w = workspace_path(error);
	if (!error.empty()) return error;
	for (std::size_t p = 1; p <= w.size(); p++) {
		if (p != w.size() && w[p] != '/') continue;
		if (::mkdir(w.substr(0, p).c_str(), 0755) != 0 && errno != EEXIST)
			return "failed to create picoclaw workspace: mkdir " + w.substr(0, p) + ": " + errno_text(errno);
	}
	std::string content;
	if (int e = read_file(source, content); e != 0) return "failed to read agent profile source: open " + source + ": " + errno_text(e);
	if (auto e = write_file(w + "/AGENT.md", content, 0644); !e.empty()) return "failed to write picoclaw AGENT.md: " + e;
	return {};
}

// --- Runtime intent ---

namespace {
std::mutex intent_mu;

std::string utc_now()
{
	auto t = WallClock::to_time_t(WallClock::now());
	std::tm tm{};
	gmtime_r(&t, &tm);
	char buf[32];
	std::strftime(buf, sizeof buf, "%Y-%m-%dT%H:%M:%SZ", &tm);
	return buf;
}

Intent load_locked(std::string &error)
{
	error.clear();
	std::string data;
	int e = read_file(runtime_intent_file, data);
	if (e == ENOENT) return {};
	if (e != 0) {
		error = "read PicoClaw runtime intent: open " + runtime_intent_file + ": " + errno_text(e);
		return {};
	}
	Value v;
	std::string perr;
	if (auto err = json::parse(data, v)) perr = err->message;
	else if (!v.is_object() && !v.is_null()) perr = std::string("json: cannot unmarshal ") + kind_name(v) + " into Go value of type picoclaw.RuntimeIntentStatus";
	Intent intent;
	auto str = [&](const char *name, std::string &out) {
		for (const auto &[k, x] : v.obj)
			if (fold_equal(k, name)) {
				if (x.is_string()) out = x.str;
				else if (!x.is_null() && perr.empty())
					perr = std::string("json: cannot unmarshal ") + kind_name(x) + " into Go struct field RuntimeIntentStatus." + name + " of type string";
			}
	};
	auto flag = [&](const char *name, bool &out) {
		for (const auto &[k, x] : v.obj)
			if (fold_equal(k, name)) {
				if (x.is_bool()) out = x.b;
				else if (!x.is_null() && perr.empty())
					perr = std::string("json: cannot unmarshal ") + kind_name(x) + " into Go struct field RuntimeIntentStatus." + name + " of type bool";
			}
	};
	if (perr.empty()) {
		flag("desired_running", intent.desired_running);
		str("updated_at", intent.updated_at);
		str("updated_by", intent.updated_by);
		str("last_started_at", intent.last_started_at);
		str("last_stopped_at", intent.last_stopped_at);
		str("last_error", intent.last_error);
		flag("host_tools", intent.host_tools);
	}
	if (!perr.empty()) {
		error = "parse PicoClaw runtime intent: " + perr;
		Intent bad;
		bad.last_error = perr;
		return bad;
	}
	return intent;
}

std::string save_locked(const Intent &intent)
{
	std::string data = "{\n  \"desired_running\": " + std::string(intent.desired_running ? "true" : "false");
	auto field = [&](const char *name, const std::string &v) {
		if (!v.empty()) data += ",\n  \"" + std::string(name) + "\": " + json::quote(v);
	};
	field("updated_at", intent.updated_at);
	field("updated_by", intent.updated_by);
	field("last_started_at", intent.last_started_at);
	field("last_stopped_at", intent.last_stopped_at);
	field("last_error", intent.last_error);
	if (intent.host_tools) data += ",\n  \"host_tools\": true";
	data += "\n}\n";
	return save_file(runtime_intent_file, data, ".picoclaw-runtime.", "PicoClaw runtime intent");
}
} // namespace

std::string intent_json(const Intent &i)
{
	std::string out = std::string("{\"desired_running\":") + (i.desired_running ? "true" : "false");
	auto field = [&](const char *name, const std::string &v) {
		if (!v.empty()) out += ",\"" + std::string(name) + "\":" + json::quote(v);
	};
	field("updated_at", i.updated_at);
	field("updated_by", i.updated_by);
	field("last_started_at", i.last_started_at);
	field("last_stopped_at", i.last_stopped_at);
	field("last_error", i.last_error);
	if (i.host_tools) out += ",\"host_tools\":true";
	return out + "}";
}

Intent intent_load(std::string &error)
{
	std::lock_guard lock(intent_mu);
	return load_locked(error);
}

std::string intent_set_desired(bool desired, const std::string &by)
{
	std::lock_guard lock(intent_mu);
	std::string error;
	Intent intent = load_locked(error);
	if (!error.empty()) {
		intent = {};
		intent.last_error = error;
	}
	std::string now = utc_now();
	intent.desired_running = desired;
	intent.updated_at = now;
	intent.updated_by = by;
	intent.last_error.clear();
	if (desired) intent.last_started_at = now;
	else intent.last_stopped_at = now;
	return save_locked(intent);
}

std::string intent_set_host_tools(bool enabled, const std::string &by)
{
	std::lock_guard lock(intent_mu);
	std::string error;
	Intent intent = load_locked(error);
	if (!error.empty()) return error;
	intent.host_tools = enabled;
	intent.updated_at = utc_now();
	intent.updated_by = by;
	return save_locked(intent);
}

std::string intent_set_last_error(const std::string &message)
{
	std::lock_guard lock(intent_mu);
	std::string error;
	Intent intent = load_locked(error);
	if (!error.empty()) intent = {};
	intent.last_error = message;
	intent.updated_at = utc_now();
	if (intent.updated_by.empty()) intent.updated_by = "system";
	return save_locked(intent);
}

} // namespace nkos::ai::picoclaw
