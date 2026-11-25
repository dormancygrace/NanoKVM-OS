#include "ai/mcp.hpp"

#include "ai/files.hpp"
#include "ai/json.hpp"
#include "ai/keys.hpp"
#include "common/base64.hpp"
#include "common/log.hpp"

#include <algorithm>
#include <cmath>
#include <map>
#include <mutex>
#include <openssl/crypto.h>
#include <openssl/rand.h>
#include <set>
#include <shared_mutex>

namespace nkos::ai::mcp {

using json::Value;
namespace control = input::control;

namespace {

// --- Constants of service/mcp ---

constexpr const char *server_name = "nanokvm-remote-control";
constexpr const char *server_version = "v1.0.0";
constexpr int default_type_delay_ms = 30;
constexpr int default_hold_ms = 50;
constexpr int default_click_delay_ms = 50;
constexpr int max_type_text_duration_ms = 30000;
constexpr int max_type_text_runes = max_type_text_duration_ms / default_type_delay_ms;
constexpr std::size_t max_request_body = 1 << 20;
constexpr int max_relative_reports = 1024;
constexpr int default_drag_steps = 20;
constexpr int max_drag_steps = 200;
constexpr int default_drag_duration_ms = 300;
constexpr int max_drag_duration_ms = 5000;
constexpr const char *no_text_reader =
    "reading the screen text needs the models of a language (Settings > AI > Text recognition)";

// Protocol versions the Go SDK negotiates, newest first.
const std::vector<std::string> supported_versions = {"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"};
constexpr const char *version_new = "2026-07-28";
constexpr const char *version_default = "2025-03-26";
constexpr const char *version_latest = "2025-11-25";

// --- Config ---

std::mutex config_mu;
// hostControlGate: host control operations run under the shared side, the switch saves under the exclusive one.
std::shared_mutex host_gate;

bool load_config_locked(Config &config, std::string &error)
{
	config = {};
	std::string data;
	int e = read_file(config_file, data);
	if (e == ENOENT) return true;
	if (e != 0) {
		error = "open " + config_file + ": " + errno_text(e);
		return false;
	}
	Value v;
	if (auto err = json::parse(data, v)) {
		error = "decode MCP config: " + err->message;
		return false;
	}
	if (v.is_null()) return true;
	if (!v.is_object()) {
		error = "decode MCP config: json: cannot unmarshal " + std::string(v.is_array() ? "array" : v.is_string() ? "string" : v.is_number() ? "number" : "bool") +
		        " into Go value of type mcpservice.Config";
		return false;
	}
	// encoding/json matches field names case-insensitively, the exact name first; the last member wins.
	auto field = [&](std::string_view name) -> const Value * {
		const Value *found = nullptr;
		for (const auto &[k, x] : v.obj)
			if (json::to_lower(k) == json::to_lower(name)) found = &x;
		return found;
	};
	if (auto f = field("apiKey"); f && !f->is_null()) {
		if (!f->is_string()) {
			error = "decode MCP config: json: cannot unmarshal into Go struct field Config.apiKey of type string";
			return false;
		}
		config.api_key = f->str;
	}
	if (auto f = field("allowHostControl"); f && !f->is_null()) {
		if (!f->is_bool()) {
			error = "decode MCP config: json: cannot unmarshal into Go struct field Config.allowHostControl of type bool";
			return false;
		}
		config.allow_host_control = f->b;
	}
	return true;
}

bool save_config_locked(const Config &config, std::string &error)
{
	std::string data = "{\n  \"apiKey\": " + json::quote(config.api_key);
	if (config.allow_host_control) data += ",\n  \"allowHostControl\": true";
	data += "\n}\n";
	error = save_file(config_file, data, ".mcp.json.", "MCP config");
	return error.empty();
}

bool generate_api_key(std::string &key, std::string &error)
{
	unsigned char raw[32];
	if (RAND_bytes(raw, sizeof raw) != 1) {
		error = "generate MCP API key: random source failed";
		return false;
	}
	std::string b64 = base64::encode(std::string_view(reinterpret_cast<char *>(raw), sizeof raw));
	for (auto &c : b64) {
		if (c == '+') c = '-';
		else if (c == '/') c = '_';
	}
	while (!b64.empty() && b64.back() == '=') b64.pop_back();
	key = "nag_mcp_" + b64;
	return true;
}

bool update_config(const std::function<bool(Config &, std::string &)> &update, Config &config, std::string &error)
{
	std::lock_guard lock(config_mu);
	if (!load_config_locked(config, error)) return false;
	Config updated = config;
	if (!update(updated, error)) return false;
	if (!save_config_locked(updated, error)) return false;
	config = updated;
	return true;
}

bool host_control_allowed()
{
	Config config;
	std::string error;
	if (!load_config(config, error)) {
		log::error("MCP config: {}", error);
		return false;
	}
	return config.allow_host_control;
}

// --- Tool definitions ---

struct ToolDef {
	const char *name;
	const char *json;
};
#include "ai/mcp_tools.inc"

struct Tool {
	std::string name;
	std::string json;  // as tools/list writes it
	Value input_schema;
};

const std::vector<Tool> &all_tools()
{
	static const std::vector<Tool> tools = [] {
		std::vector<Tool> out;
		for (const auto &d : tool_defs) {
			Tool t{d.name, d.json, {}};
			Value v;
			(void)json::parse(t.json, v);
			if (auto s = v.get("inputSchema")) t.input_schema = *s;
			out.push_back(std::move(t));
		}
		return out;
	}();
	return tools;
}

bool host_tool(const std::string &name) { return name == "power" || name == "virtual_media" || name == "wake_on_lan"; }

// The tools of a request: host control while allowed, screen_text while text recognition is available (with its
// languages), power only with a power control.
std::vector<Tool> tool_set(bool control, bool text, const Devices &devices)
{
	std::vector<Tool> out;
	for (const auto &t : all_tools()) {
		if (host_tool(t.name) && !control) continue;
		if (t.name == "power" && !devices.power) continue;
		if (t.name == "screen_text" && !text) continue;
		Tool copy = t;
		if (t.name == "screen_text" && devices.text_languages) {
			// The language enum is the reader's languages.
			std::string list;
			for (const auto &l : devices.text_languages()) list += (list.empty() ? "" : ",") + json::quote(l);
			const std::string from = R"("enum":["latin","chinese","cyrillic"])";
			if (auto at = copy.json.find(from); at != std::string::npos) copy.json.replace(at, from.size(), "\"enum\":[" + list + "]");
			if (auto lang = copy.input_schema.get("properties"); lang && lang->get("language")) {
				Value e = Value::array();
				for (const auto &l : devices.text_languages()) e.arr.push_back(Value::string(l));
				lang->get("language")->set("enum", e);
			}
		}
		out.push_back(std::move(copy));
	}
	return out;
}

// --- Schema validation, as google/jsonschema-go ---

std::string json_type(const Value &v)
{
	switch (v.kind) {
	case Value::Kind::null: return "null";
	case Value::Kind::boolean: return "boolean";
	case Value::Kind::number: return std::trunc(v.num) == v.num ? "integer" : "number";
	case Value::Kind::string: return "string";
	case Value::Kind::array: return "array";
	case Value::Kind::object: return "object";
	}
	return "null";
}

bool equal_value(const Value &a, const Value &b)
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
			if (!equal_value(a.arr[i], b.arr[i])) return false;
		return true;
	case Value::Kind::object: {
		Value x = a, y = b;
		json::sort_keys(x);
		json::sort_keys(y);
		if (x.obj.size() != y.obj.size()) return false;
		for (std::size_t i = 0; i < x.obj.size(); i++)
			if (x.obj[i].first != y.obj[i].first || !equal_value(x.obj[i].second, y.obj[i].second)) return false;
		return true;
	}
	}
	return false;
}

// %q of a []string.
std::string quote_list(const std::vector<std::string> &list)
{
	std::string out = "[";
	for (std::size_t i = 0; i < list.size(); i++) out += (i ? " " : "") + json::go_quote(list[i]);
	return out + "]";
}

// The members of an object, the last of a duplicated key (as a decoded map has them), in key order.
std::vector<std::pair<std::string, const Value *>> members(const Value &v)
{
	std::map<std::string, const Value *> m;
	for (const auto &[k, x] : v.obj) m[k] = &x;
	return {m.begin(), m.end()};
}

std::string validate(const Value &inst, const Value &schema, const std::string &ptr)
{
	auto fail = [&](const std::string &e) { return "validating " + (ptr.empty() ? std::string("root") : ptr) + ": " + e; };
	if (schema.kind == Value::Kind::boolean) return schema.b ? "" : fail("false schema");
	if (auto t = schema.get("type")) {
		std::string got = json_type(inst);
		if (t->is_string()) {
			if (!(got == t->str || (got == "integer" && t->str == "number")))
				return fail("type: " + json::go_v(inst) + " has type " + json::go_quote(got) + ", want " + json::go_quote(t->str));
		} else if (t->is_array()) {
			bool ok = false;
			std::string joined;
			for (const auto &x : t->arr) {
				if (x.str == got || (got == "integer" && x.str == "number")) ok = true;
				joined += (joined.empty() ? "" : ", ") + x.str;
			}
			if (!ok) return fail("type: " + json::go_v(inst) + " has type " + json::go_quote(got) + ", want one of " + json::go_quote(joined));
		}
	}
	if (auto e = schema.get("enum")) {
		bool ok = false;
		for (const auto &x : e->arr)
			if (equal_value(x, inst)) ok = true;
		if (!ok) return fail("enum: " + json::go_v(inst) + " does not equal any of: " + json::go_v(*e));
	}
	if (inst.is_number()) {
		if (auto m = schema.get("minimum"); m && inst.num < m->num)
			return fail("minimum: " + json::float_rat(inst.num) + " is less than " + json::float_f(m->num));
		if (auto m = schema.get("maximum"); m && inst.num > m->num)
			return fail("maximum: " + json::float_rat(inst.num) + " is greater than " + json::float_f(m->num));
	}
	if (inst.is_string()) {
		auto n = static_cast<long long>(json::runes(inst.str).size());
		if (auto m = schema.get("minLength"); m && n < static_cast<long long>(m->num))
			return fail("minLength: " + json::go_quote(inst.str) + " contains " + std::to_string(n) + " Unicode code points, fewer than " + json::float_v(m->num));
		if (auto m = schema.get("maxLength"); m && n > static_cast<long long>(m->num))
			return fail("maxLength: " + json::go_quote(inst.str) + " contains " + std::to_string(n) + " Unicode code points, more than " + json::float_v(m->num));
	}
	if (auto any = schema.get("anyOf")) {
		std::vector<std::string> errs;
		for (std::size_t i = 0; i < any->arr.size(); i++) {
			auto e = validate(inst, any->arr[i], ptr + "/anyOf/" + std::to_string(i));
			if (!e.empty()) errs.push_back(e);
		}
		if (errs.size() == any->arr.size()) {
			std::string list = "[";
			for (std::size_t i = 0; i < any->arr.size(); i++) list += (i ? " " : "") + std::string("<anonymous schema>");
			std::string joined;
			for (std::size_t i = 0; i < errs.size(); i++) joined += (i ? "\n" : "") + errs[i];
			return fail("anyOf: did not validate against any of " + list + "]:\n" + joined);
		}
	}
	if (auto one = schema.get("oneOf")) {
		int matched = 0;
		for (std::size_t i = 0; i < one->arr.size(); i++)
			if (validate(inst, one->arr[i], ptr + "/oneOf/" + std::to_string(i)).empty()) matched++;
		if (matched > 1) return fail("oneOf: validated against both <anonymous schema> and <anonymous schema>");
		if (matched == 0) {
			std::string list = "[";
			for (std::size_t i = 0; i < one->arr.size(); i++) list += (i ? " " : "") + std::string("<anonymous schema>");
			return fail("oneOf: did not validate against any of " + list + "]");
		}
	}
	if (auto n = schema.get("not")) {
		if (validate(inst, *n, ptr + "/not").empty()) return fail("not: validated against <anonymous schema>");
	}
	if (inst.is_array()) {
		if (auto items = schema.get("items"))
			for (const auto &x : inst.arr)
				if (auto e = validate(x, *items, ptr + "/items"); !e.empty()) return fail(e);
		if (auto m = schema.get("minItems"); m && inst.arr.size() < static_cast<std::size_t>(m->num))
			return fail("minItems: array length " + std::to_string(inst.arr.size()) + " is less than " + json::float_v(m->num));
		if (auto m = schema.get("maxItems"); m && inst.arr.size() > static_cast<std::size_t>(m->num))
			return fail("maxItems: array length " + std::to_string(inst.arr.size()) + " is greater than " + json::float_v(m->num));
	}
	if (inst.is_object()) {
		auto present = members(inst);
		std::set<std::string> evaluated;
		if (auto props = schema.get("properties")) {
			for (const auto &[name, sub] : props->obj) {
				const Value *val = inst.get(name);
				if (!val) continue;
				if (auto e = validate(*val, sub, ptr + "/properties/" + name); !e.empty()) return fail(e);
				evaluated.insert(name);
			}
		}
		if (auto add = schema.get("additionalProperties")) {
			if (add->kind == Value::Kind::boolean && !add->b) {
				std::vector<std::string> disallowed;
				for (const auto &[k, x] : present)
					if (!evaluated.count(k)) disallowed.push_back(k);
				if (!disallowed.empty()) return fail("unexpected additional properties " + quote_list(disallowed));
			}
		}
		if (auto req = schema.get("required")) {
			std::vector<std::string> missing;
			for (const auto &r : req->arr)
				if (!inst.get(r.str)) missing.push_back(r.str);
			if (!missing.empty()) return fail("required: missing properties: " + quote_list(missing));
		}
	}
	return {};
}

// --- JSON-RPC ---

// segmentio/encoding's prefix of the input at an error.
std::string input_prefix(std::string_view rest)
{
	if (rest.size() < 32) return std::string(rest);
	return std::string(rest.substr(0, 32)) + "...";
}

// "json: cannot unmarshal %q into Go struct field T.f of type t" / "... into Go value of type t".
std::string type_error(std::string_view doc, const Value &v, std::size_t doc_begin, const std::string &into)
{
	std::size_t at = v.offset >= doc_begin ? v.offset - doc_begin : 0;
	return "json: cannot unmarshal " + json::go_quote(input_prefix(doc.substr(std::min(at, doc.size())))) + " into " + into;
}

// The decoder's text for a syntax error of the request (segmentio/encoding, as the SDK decodes).
std::string syntax_error(std::string_view body, const json::ParseError &e)
{
	if (e.kind == json::ParseError::Kind::eof) return "unexpected EOF";
	std::string_view rest = body.substr(std::min(e.offset, body.size()));
	if (e.message.find("after object key") != std::string::npos && e.message.find("pair") == std::string::npos) {
		return std::string("json: expected ':' after object field key but found '") + rest[0] + "': " + input_prefix(rest);
	}
	if (e.message.find("beginning of object key string") != std::string::npos)
		return "json: expected '\"' at the beginning of a string value: " + input_prefix(rest);
	if (e.message.find("looking for beginning of value") != std::string::npos && !rest.empty() && rest[0] == ']')
		return "json: unexpected trailing comma after object field: " + input_prefix(rest);
	return "json: " + e.message + ": " + input_prefix(rest);
}

struct Id {
	enum class Kind { none, number, string } kind = Kind::none;
	long long n = 0;
	std::string s;
	std::string json() const
	{
		if (kind == Kind::number) return std::to_string(n);
		if (kind == Kind::string) return json::quote(s, false);
		return "null";
	}
};

struct Message {
	bool request = false;
	std::string method;
	Id id;
	const Value *params = nullptr;  // absent: nullptr
	Value value;
	std::size_t doc_begin = 0, doc_end = 0;
};

// jsonrpc2.DecodeMessage of one message; error set on failure.
bool decode_message(std::string_view body, const Value &v, std::size_t begin, std::size_t end, Message &out, std::string &error)
{
	std::string_view doc = body.substr(begin, end - begin);
	if (!v.is_object()) {
		if (v.is_null()) {
			error = "invalid message version tag \"\"; expected \"2.0\"";
			return false;
		}
		error = "unmarshaling jsonrpc message: " + type_error(doc, v, begin, "Go value of type jsonrpc2.wireDecode");
		return false;
	}
	out.value = v;
	out.doc_begin = begin;
	out.doc_end = end;
	const Value *tag = v.get("jsonrpc");
	if (tag && !tag->is_null() && !tag->is_string()) {
		error = "unmarshaling jsonrpc message: " + type_error(doc, *tag, begin, "Go struct field jsonrpc2.wireDecode.jsonrpc of type string");
		return false;
	}
	std::string version = tag && tag->is_string() ? tag->str : "";
	if (version != "2.0") {
		error = "invalid message version tag " + json::go_quote(version) + "; expected \"2.0\"";
		return false;
	}
	const Value *id = out.value.get("id");
	if (id && !id->is_null()) {
		if (id->is_number()) {
			out.id.kind = Id::Kind::number;
			out.id.n = static_cast<long long>(id->num);
		} else if (id->is_string()) {
			out.id.kind = Id::Kind::string;
			out.id.s = id->str;
		} else {
			error = std::string("parse error: invalid ID type ") +
			        (id->is_bool() ? "bool" : id->is_array() ? "[]interface {}" : "map[string]interface {}");
			return false;
		}
	}
	const Value *method = out.value.get("method");
	if (method) {
		if (!method->is_string()) {
			std::string_view raw = body.substr(method->offset, method->end - method->offset);
			error = "unmarshaling jsonrpc message: json: cannot unmarshal " + json::go_quote(input_prefix(raw)) + " into Go value of type string";
			return false;
		}
		out.request = true;
		out.method = method->str;
		out.params = out.value.get("params");
		return true;
	}
	if (out.id.kind == Id::Kind::none) {
		error = "invalid request";
		return false;
	}
	return true;
}

struct MethodInfo {
	bool notification = false, missing_params_ok = false;
	const char *params_type = nullptr;
};

const std::map<std::string, MethodInfo> &method_infos()
{
	static const std::map<std::string, MethodInfo> m = {
	    {"completion/complete", {false, false, "CompleteParams"}},
	    {"server/discover", {false, true, "DiscoverParams"}},
	    {"initialize", {false, false, "initializeParamsV2"}},
	    {"ping", {false, true, "PingParams"}},
	    {"prompts/list", {false, true, "ListPromptsParams"}},
	    {"prompts/get", {false, false, "GetPromptParams"}},
	    {"tools/list", {false, true, "ListToolsParams"}},
	    {"tools/call", {false, false, "CallToolParamsRaw"}},
	    {"resources/list", {false, true, "ListResourcesParams"}},
	    {"resources/templates/list", {false, true, "ListResourceTemplatesParams"}},
	    {"resources/read", {false, false, "ReadResourceParams"}},
	    {"logging/setLevel", {false, false, "SetLoggingLevelParams"}},
	    {"resources/subscribe", {false, false, "SubscribeParams"}},
	    {"subscriptions/listen", {false, false, "SubscriptionsListenParams"}},
	    {"resources/unsubscribe", {false, false, "UnsubscribeParams"}},
	    {"notifications/cancelled", {true, true, "CancelledParams"}},
	    {"notifications/initialized", {true, true, "InitializedParams"}},
	    {"notifications/roots/list_changed", {true, true, "RootsListChangedParams"}},
	    {"notifications/progress", {true, false, "ProgressNotificationParams"}},
	};
	return m;
}

std::string response_ok(const Id &id, const std::string &result)
{
	return "{\"jsonrpc\":\"2.0\",\"id\":" + id.json() + ",\"result\":" + result + "}";
}

std::string response_error(const Id &id, int code, const std::string &message, const std::string &data = {})
{
	std::string out = "{\"jsonrpc\":\"2.0\",\"id\":" + id.json() + ",\"error\":{\"code\":" + std::to_string(code) +
	                  ",\"message\":" + json::quote(message, false);
	if (!data.empty()) out += ",\"data\":" + data;
	return out + "}}";
}

HttpResponse text_error(int status, const std::string &text)
{
	HttpResponse r;
	r.status = status;
	r.content_type = "text/plain; charset=utf-8";
	r.body = text + "\n";
	return r;
}

HttpResponse gin_json(int status, const std::string &message)
{
	HttpResponse r;
	r.status = status;
	r.content_type = "application/json; charset=utf-8";
	r.body = "{\"error\":" + json::quote(message) + "}";
	return r;
}

// --- Tool runs ---

struct ToolResult {
	std::string error;          // a Go error: the text content, isError
	std::string tool_error;     // toolError(...) with the output
	bool has_output = false;
	Value output;
	std::string image_base64;  // the screenshot's content instead of the output's text
};

ToolResult go_error(std::string e)
{
	ToolResult r;
	r.error = std::move(e);
	return r;
}

ToolResult with_output(Value out)
{
	ToolResult r;
	json::sort_keys(out);
	r.has_output = true;
	r.output = std::move(out);
	return r;
}

ToolResult ok_result()
{
	Value v = Value::object();
	v.set("ok", Value::boolean(true));
	return with_output(v);
}

std::string encode_result(const ToolResult &r)
{
	if (!r.error.empty() || (!r.has_output && r.tool_error.empty()))
		return R"({"content":[{"type":"text","text":)" + json::quote(r.error) + R"(}],"isError":true})";
	std::string out = json::marshal(r.output);
	std::string content;
	if (!r.tool_error.empty()) content = R"([{"type":"text","text":)" + json::quote(r.tool_error) + "}]";
	else if (!r.image_base64.empty()) content = R"([{"type":"image","mimeType":"image/jpeg","data":)" + json::quote(r.image_base64) + "}]";
	else content = R"([{"type":"text","text":)" + json::quote(out) + "}]";
	std::string result = "{\"content\":" + content + ",\"structuredContent\":" + out;
	if (!r.tool_error.empty()) result += ",\"isError\":true";
	return result + "}";
}

int clamp_int(long long v, long long lo, long long hi) { return static_cast<int>(std::clamp(v, lo, hi)); }

// time.Duration's String for whole milliseconds.
std::string duration_string(long long ms)
{
	if (ms == 0) return "0s";
	if (ms < 1000) return std::to_string(ms) + "ms";
	std::string s = std::to_string(ms / 1000);
	if (long long frac = ms % 1000) {
		char buf[8];
		std::snprintf(buf, sizeof buf, ".%03lld", frac);
		std::string f = buf;
		while (f.back() == '0') f.pop_back();
		s += f;
	}
	return s + "s";
}

// --- The remote computer (service/mcp/remote.go) ---

using Report = std::vector<std::uint8_t>;
const Report key_up(8, 0);

std::uint16_t absolute_coordinate(double v)
{
	v = std::max(0.0, std::min(1.0, v));
	return static_cast<std::uint16_t>(static_cast<std::uint16_t>(std::floor(0x7fff * v)) + 1);
}

Report absolute_report(std::uint16_t x, std::uint16_t y, std::uint8_t buttons, int wheel)
{
	return {buttons, static_cast<std::uint8_t>(x), static_cast<std::uint8_t>(x >> 8), static_cast<std::uint8_t>(y),
	        static_cast<std::uint8_t>(y >> 8), static_cast<std::uint8_t>(static_cast<std::int8_t>(clamp_int(wheel, -127, 127))), 0};
}

Report relative_report(int dx, int dy, std::uint8_t buttons, int wheel)
{
	return {buttons, static_cast<std::uint8_t>(static_cast<std::int8_t>(clamp_int(dx, -127, 127))),
	        static_cast<std::uint8_t>(static_cast<std::int8_t>(clamp_int(dy, -127, 127))),
	        static_cast<std::uint8_t>(static_cast<std::int8_t>(clamp_int(wheel, -127, 127))), 0};
}

int reports_for_delta(long long delta)
{
	if (delta == 0) return 0;
	if (delta < 0) return static_cast<int>((-(delta + 1)) / 127 + 1);
	return static_cast<int>((delta - 1) / 127 + 1);
}

bool button_bit(const std::string &button, std::uint8_t &bit, std::string &error)
{
	if (button == "left") bit = 1;
	else if (button == "right") bit = 2;
	else if (button == "middle") bit = 4;
	else {
		error = "unknown mouse button: " + button;
		return false;
	}
	return true;
}

std::mutex remote_mu;  // Remote.hidMu

struct Remote {
	HidSink &hid;
	std::shared_ptr<control::Cancel> op;

	std::string context_error() const { return op ? op->cause() : std::string(); }
	std::string sleep(long long ms) const
	{
		if (ms <= 0) return context_error();
		if (op && !op->sleep(std::chrono::milliseconds(ms))) return op->cause();
		return {};
	}
	std::string write(HidDevice d, const Report &r) const
	{
		std::string error;
		if (hid.write(d, r, op, error)) return {};
		// A report refused because the operation was cancelled reports the cause.
		if (auto c = context_error(); !c.empty()) return c;
		return error.empty() ? "HID write failed" : error;
	}
	// A release: written also after a cancel (keys and buttons must come up), as Go writes it.
	std::string write_release(HidDevice d, const Report &r) const
	{
		std::string error;
		if (hid.write(d, r, nullptr, error)) return {};
		return error.empty() ? "HID write failed" : error;
	}
	void write_quiet(HidDevice d, const Report &r) const { (void)write_release(d, r); }

	std::string type_text(const std::vector<Report> &reports, long long delay_ms) const
	{
		std::lock_guard lock(remote_mu);
		bool key_down = false;
		for (std::size_t i = 0; i < reports.size(); i++) {
			if (auto e = context_error(); !e.empty()) {
				if (key_down) write_quiet(HidDevice::keyboard, key_up);
				return e;
			}
			if (auto e = write(HidDevice::keyboard, reports[i]); !e.empty()) {
				write_quiet(HidDevice::keyboard, key_up);
				return e;
			}
			key_down = i % 2 == 0;
			if (!key_down && i + 1 < reports.size())
				if (auto e = sleep(delay_ms); !e.empty()) return e;
		}
		return {};
	}

	std::string press_keys(const Report &report, long long hold_ms) const
	{
		std::lock_guard lock(remote_mu);
		if (auto e = context_error(); !e.empty()) return e;
		if (auto e = write(HidDevice::keyboard, report); !e.empty()) {
			write_quiet(HidDevice::keyboard, key_up);
			return e;
		}
		if (auto e = sleep(hold_ms); !e.empty()) {
			write_quiet(HidDevice::keyboard, key_up);
			return e;
		}
		return write(HidDevice::keyboard, key_up);
	}

	std::string move_absolute(double x, double y) const
	{
		if (x < 0 || x > 1 || y < 0 || y > 1) return "absolute coordinates must be between 0 and 1";
		auto r = absolute_report(absolute_coordinate(x), absolute_coordinate(y), 0, 0);
		std::lock_guard lock(remote_mu);
		if (auto e = context_error(); !e.empty()) return e;
		return write(HidDevice::absolute, r);
	}

	std::string touch_absolute(double x, double y, const std::string &button, long long hold_ms) const
	{
		if (x < 0 || x > 1 || y < 0 || y > 1) return "absolute coordinates must be between 0 and 1";
		std::uint8_t bit;
		std::string error;
		if (!button_bit(button, bit, error)) return error;
		auto hx = absolute_coordinate(x), hy = absolute_coordinate(y);
		auto release = absolute_report(hx, hy, 0, 0);
		std::lock_guard lock(remote_mu);
		if (auto e = context_error(); !e.empty()) return e;
		if (auto e = write(HidDevice::absolute, absolute_report(hx, hy, bit, 0)); !e.empty()) {
			write_quiet(HidDevice::absolute, release);
			return e;
		}
		if (auto e = sleep(hold_ms); !e.empty()) {
			write_quiet(HidDevice::absolute, release);
			return e;
		}
		return write(HidDevice::absolute, release);
	}

	std::string move_relative(long long dx, long long dy) const
	{
		if (std::max(reports_for_delta(dx), reports_for_delta(dy)) > max_relative_reports)
			return "relative movement exceeds " + std::to_string(max_relative_reports) + " reports";
		std::vector<Report> reports;
		while (dx != 0 || dy != 0) {
			int x = clamp_int(dx, -127, 127), y = clamp_int(dy, -127, 127);
			reports.push_back(relative_report(x, y, 0, 0));
			dx -= x;
			dy -= y;
		}
		std::lock_guard lock(remote_mu);
		for (const auto &r : reports) {
			if (auto e = context_error(); !e.empty()) {
				write_quiet(HidDevice::relative, relative_report(0, 0, 0, 0));
				return e;
			}
			if (auto e = write(HidDevice::relative, r); !e.empty()) {
				write_quiet(HidDevice::relative, relative_report(0, 0, 0, 0));
				return e;
			}
		}
		return {};
	}

	std::string click(const std::string &button, int clicks, long long delay_ms) const
	{
		std::uint8_t bit;
		std::string error;
		if (!button_bit(button, bit, error)) return error;
		clicks = clamp_int(clicks, 1, 5);
		auto release = relative_report(0, 0, 0, 0);
		std::lock_guard lock(remote_mu);
		for (int i = 0; i < clicks; i++) {
			if (auto e = context_error(); !e.empty()) {
				write_quiet(HidDevice::relative, release);
				return e;
			}
			if (auto e = write(HidDevice::relative, relative_report(0, 0, bit, 0)); !e.empty()) {
				write_quiet(HidDevice::relative, release);
				return e;
			}
			if (auto e = sleep(delay_ms); !e.empty()) {
				write_quiet(HidDevice::relative, release);
				return e;
			}
			if (auto e = write(HidDevice::relative, release); !e.empty()) {
				write_quiet(HidDevice::relative, release);
				return e;
			}
			if (i + 1 < clicks)
				if (auto e = sleep(delay_ms); !e.empty()) return e;
		}
		return {};
	}

	std::string scroll(long long dy) const
	{
		if (reports_for_delta(dy) > max_relative_reports) return "scroll exceeds " + std::to_string(max_relative_reports) + " reports";
		auto x = absolute_coordinate(0.5), y = absolute_coordinate(0.5);
		auto release = absolute_report(x, y, 0, 0);
		std::lock_guard lock(remote_mu);
		while (dy != 0) {
			if (auto e = context_error(); !e.empty()) {
				write_quiet(HidDevice::absolute, release);
				return e;
			}
			int wheel = clamp_int(dy, -127, 127);
			if (auto e = write(HidDevice::absolute, absolute_report(x, y, 0, wheel)); !e.empty()) {
				write_quiet(HidDevice::absolute, release);
				return e;
			}
			if (auto e = write_release(HidDevice::absolute, release); !e.empty()) {
				write_quiet(HidDevice::absolute, release);
				return e;
			}
			dy -= wheel;
		}
		return {};
	}

	std::string drag(const std::string &button, double fx, double fy, double tx, double ty, int steps, long long duration_ms) const
	{
		for (double v : {fx, fy, tx, ty})
			if (v < 0 || v > 1) return "absolute coordinates must be between 0 and 1";
		std::uint8_t bit;
		std::string error;
		if (!button_bit(button, bit, error)) return error;
		steps = clamp_int(steps, 1, max_drag_steps);
		auto x = absolute_coordinate(fx), y = absolute_coordinate(fy);
		std::lock_guard lock(remote_mu);
		if (auto e = context_error(); !e.empty()) return e;
		if (auto e = write(HidDevice::absolute, absolute_report(x, y, 0, 0)); !e.empty()) return e;
		if (auto e = write(HidDevice::absolute, absolute_report(x, y, bit, 0)); !e.empty()) {
			write_quiet(HidDevice::absolute, absolute_report(x, y, 0, 0));
			return e;
		}
		auto release = [&] { return write_release(HidDevice::absolute, absolute_report(x, y, 0, 0)); };
		if (auto e = sleep(default_click_delay_ms); !e.empty()) {
			(void)release();
			return e;
		}
		for (int i = 1; i <= steps; i++) {
			double t = static_cast<double>(i) / steps;
			x = absolute_coordinate(fx + (tx - fx) * t);
			y = absolute_coordinate(fy + (ty - fy) * t);
			if (auto e = write(HidDevice::absolute, absolute_report(x, y, bit, 0)); !e.empty()) {
				(void)release();
				return e;
			}
			// duration / steps in nanoseconds, as time.Duration divides.
			long long step_ns = duration_ms * 1000000 / steps;
			if (step_ns > 0 && op && !op->sleep(std::chrono::nanoseconds(step_ns))) {
				(void)release();
				return op->cause();
			}
			if (step_ns <= 0)
				if (auto e = context_error(); !e.empty()) {
					(void)release();
					return e;
				}
		}
		return release();
	}
};

// --- The tools ---

struct Call {
	Devices &devices;
	const Value &args;
	const std::string &name;
};

// Typed reads of validated arguments.
const Value *arg(const Value &args, const char *name)
{
	const Value *v = args.get(name);
	return v && !v->is_null() ? v : nullptr;
}
std::string arg_string(const Value &args, const char *name)
{
	auto v = arg(args, name);
	return v && v->is_string() ? v->str : std::string();
}
long long arg_int(const Value &args, const char *name)
{
	auto v = arg(args, name);
	return v && v->is_number() ? static_cast<long long>(v->num) : 0;
}
std::optional<long long> arg_opt_int(const Value &args, const char *name)
{
	auto v = arg(args, name);
	if (!v || !v->is_number()) return std::nullopt;
	return static_cast<long long>(v->num);
}
std::optional<double> arg_opt_double(const Value &args, const char *name)
{
	auto v = arg(args, name);
	if (!v || !v->is_number()) return std::nullopt;
	return v->num;
}

std::string normalized_mode(const std::string &mode, const char *fallback)
{
	std::string m = json::to_lower(json::trim_space(mode));
	return m.empty() ? fallback : m;
}

// executor.begin: the mode held as mcp and the operation slot for HID tools.
struct Begun {
	std::unique_ptr<control::Hold> hold;
	std::shared_ptr<control::Cancel> op;
	~Begun()
	{
		if (op) control::end_operation(op);
	}
};

bool begin(control::OperationKind kind, Begun &b, std::string &error)
{
	if (kind == control::OperationKind::hid) {
		std::string e;
		b.hold = control::acquire_write(control::Mode::mcp, e);
		if (!b.hold) {
			error = "MCP service is disabled: " + e;
			return false;
		}
	}
	b.op = control::begin_operation(kind, error);
	if (!b.op) {
		b.hold.reset();
		return false;
	}
	return true;
}

// beginControl: the host control tools run in MCP mode while the switch allows them.
struct ControlHold {
	std::unique_ptr<control::Hold> hold;
	bool locked = false;
	~ControlHold()
	{
		if (locked) host_gate.unlock_shared();
	}
};

bool begin_control(ControlHold &h, std::string &error)
{
	std::string e;
	h.hold = control::acquire_write(control::Mode::mcp, e);
	if (!h.hold) {
		error = "MCP service is disabled: " + e;
		return false;
	}
	host_gate.lock_shared();
	h.locked = true;
	if (!host_control_allowed()) {
		error = "host control is off (Settings > AI > MCP)";
		return false;
	}
	return true;
}

std::vector<Report> type_reports(const std::string &text, std::string &skipped, int &skipped_count)
{
	std::vector<Report> reports;
	for (char32_t r : json::runes(text)) {
		std::uint8_t mods, code;
		if (!us_char(r, mods, code)) {
			json::append_utf8(skipped, r);
			skipped_count++;
			continue;
		}
		reports.push_back({mods, 0, code, 0, 0, 0, 0, 0});
		reports.push_back(key_up);
	}
	return reports;
}

ToolResult type_text(Call &c)
{
	std::string text = arg_string(c.args, "text");
	auto runes = json::runes(text);
	if (static_cast<int>(runes.size()) > max_type_text_runes)
		return go_error("text exceeds maximum length of " + std::to_string(max_type_text_runes) + " runes");
	auto d = arg_opt_int(c.args, "delayMs");
	long long delay = d ? clamp_int(*d, 0, 1000) : default_type_delay_ms;
	std::string skipped;
	int skipped_count = 0;
	auto reports = type_reports(text, skipped, skipped_count);
	long long typeable = static_cast<long long>(reports.size() / 2);
	if (typeable * delay > max_type_text_duration_ms)
		return go_error("typing duration exceeds 30s for " + std::to_string(typeable) + " typeable runes at " + duration_string(delay) + " delay");
	Begun b;
	std::string error;
	if (!begin(control::OperationKind::hid, b, error)) return go_error(error);
	Remote remote{*c.devices.hid, b.op};
	if (auto e = remote.type_text(reports, delay); !e.empty()) return go_error(e);
	Value out = Value::object();
	out.set("typed", Value::number(static_cast<double>(runes.size()) - skipped_count));
	if (!skipped.empty()) out.set("skipped", Value::string(skipped));
	out.set("skippedCount", Value::number(skipped_count));
	return with_output(out);
}

ToolResult press_keys(Call &c)
{
	const Value *keys = arg(c.args, "keys");
	if (!keys || keys->arr.empty()) return go_error("keys must not be empty");
	Begun b;
	std::string error;
	if (!begin(control::OperationKind::hid, b, error)) return go_error(error);
	auto h = arg_opt_int(c.args, "holdMs");
	long long hold = h ? clamp_int(*h, 0, 1000) : default_hold_ms;
	Report report(8, 0);
	std::set<std::uint8_t> seen;
	std::size_t index = 2;
	for (const auto &k : keys->arr) {
		std::uint8_t mod;
		if (mcp_modifier(k.str, mod)) {
			report[0] |= mod;
			continue;
		}
		std::uint8_t code;
		if (!mcp_key_code(k.str, code)) return go_error("unknown key: " + k.str);
		if (seen.count(code)) continue;
		if (index >= 8) return go_error("too many non-modifier keys");
		seen.insert(code);
		report[index++] = code;
	}
	Remote remote{*c.devices.hid, b.op};
	if (auto e = remote.press_keys(report, hold); !e.empty()) return go_error(e);
	return ok_result();
}

bool normalized_coordinates(const Value &args, double &x, double &y, std::string &error)
{
	auto ox = arg_opt_double(args, "x"), oy = arg_opt_double(args, "y");
	if (!ox || !oy) {
		error = "absolute mode requires both x and y";
		return false;
	}
	if (*ox < 0 || *ox > 1 || *oy < 0 || *oy > 1) {
		error = "absolute coordinates must be between 0 and 1";
		return false;
	}
	x = *ox;
	y = *oy;
	return true;
}

ToolResult move_mouse(Call &c)
{
	std::string mode = normalized_mode(arg_string(c.args, "mode"), "absolute");
	long long dx = arg_int(c.args, "deltaX"), dy = arg_int(c.args, "deltaY");
	bool has_x = arg(c.args, "x"), has_y = arg(c.args, "y");
	double x = 0, y = 0;
	std::string error;
	if (mode == "absolute") {
		if (dx != 0 || dy != 0) return go_error("absolute mode does not accept relative deltas");
		if (!normalized_coordinates(c.args, x, y, error)) return go_error(error);
	} else if (mode == "relative") {
		if (has_x || has_y) return go_error("relative mode does not accept absolute coordinates");
		if (dx == 0 && dy == 0) return go_error("relative mode requires deltaX or deltaY");
	} else {
		return go_error("unknown mouse mode: " + mode);
	}
	Begun b;
	if (!begin(control::OperationKind::hid, b, error)) return go_error(error);
	Remote remote{*c.devices.hid, b.op};
	std::string e = mode == "absolute" ? remote.move_absolute(x, y) : remote.move_relative(dx, dy);
	if (!e.empty()) return go_error(e);
	return ok_result();
}

ToolResult click_mouse(Call &c)
{
	std::string button = json::to_lower(json::trim_space(arg_string(c.args, "button")));
	if (button.empty()) button = "left";
	long long clicks = arg_int(c.args, "clicks");
	if (clicks == 0) clicks = 1;
	else if (clicks < 1 || clicks > 5) return go_error("clicks must be between 1 and 5");
	std::string mode = normalized_mode(arg_string(c.args, "mode"), "relative");
	double x = 0, y = 0;
	std::string error;
	if (mode == "absolute") {
		if (button != "left" && button != "right") return go_error("absolute mouse clicks only support left and right buttons");
		if (!normalized_coordinates(c.args, x, y, error)) return go_error(error);
	} else if (mode == "relative") {
		if (arg(c.args, "x") || arg(c.args, "y")) return go_error("relative mouse clicks do not accept absolute coordinates");
		if (button != "left" && button != "right" && button != "middle")
			return go_error("relative mouse clicks only support left, right, and middle buttons");
	} else {
		return go_error("unknown mouse mode: " + mode);
	}
	Begun b;
	if (!begin(control::OperationKind::hid, b, error)) return go_error(error);
	Remote remote{*c.devices.hid, b.op};
	if (mode == "absolute") {
		for (long long i = 0; i < clicks; i++) {
			if (auto e = remote.touch_absolute(x, y, button, default_click_delay_ms); !e.empty()) return go_error(e);
			if (i + 1 < clicks)
				if (auto e = remote.sleep(default_click_delay_ms); !e.empty()) return go_error(e);
		}
	} else if (auto e = remote.click(button, static_cast<int>(clicks), default_click_delay_ms); !e.empty()) {
		return go_error(e);
	}
	return ok_result();
}

ToolResult drag_mouse(Call &c)
{
	std::string button = json::to_lower(json::trim_space(arg_string(c.args, "button")));
	if (button.empty()) button = "left";
	const Value *from = arg(c.args, "from"), *to = arg(c.args, "to");
	if (!from || !to) return go_error("from and to are required");
	long long steps = arg_int(c.args, "steps");
	if (steps == 0) steps = default_drag_steps;
	if (steps < 1 || steps > max_drag_steps) return go_error("steps must be between 1 and " + std::to_string(max_drag_steps));
	auto d = arg_opt_int(c.args, "durationMs");
	long long duration = d ? clamp_int(*d, 0, max_drag_duration_ms) : default_drag_duration_ms;
	Begun b;
	std::string error;
	if (!begin(control::OperationKind::hid, b, error)) return go_error(error);
	auto num = [](const Value &p, const char *k) {
		auto v = p.get(k);
		return v && v->is_number() ? v->num : 0.0;
	};
	Remote remote{*c.devices.hid, b.op};
	if (auto e = remote.drag(button, num(*from, "x"), num(*from, "y"), num(*to, "x"), num(*to, "y"), static_cast<int>(steps), duration); !e.empty())
		return go_error(e);
	return ok_result();
}

ToolResult scroll_mouse(Call &c)
{
	Begun b;
	std::string error;
	if (!begin(control::OperationKind::hid, b, error)) return go_error(error);
	Remote remote{*c.devices.hid, b.op};
	if (auto e = remote.scroll(arg_int(c.args, "deltaY")); !e.empty()) return go_error(e);
	return ok_result();
}

ToolResult screenshot(Call &c)
{
	auto shot_output = [](const Snapshot &s, const std::string &message) {
		Value out = Value::object();
		out.set("ok", Value::boolean(s.ok));
		out.set("retCode", Value::number(s.ret_code));
		out.set("message", Value::string(message));
		out.set("width", Value::number(s.width));
		out.set("height", Value::number(s.height));
		out.set("size", Value::number(static_cast<double>(s.jpeg.size())));
		return out;
	};
	if (!c.devices.capture) {
		auto r = with_output(shot_output({}, "screenshot capture is unavailable"));
		r.tool_error = "screenshot capture is unavailable";
		return r;
	}
	std::string error;
	Snapshot s = c.devices.capture(static_cast<int>(arg_int(c.args, "quality")), arg_opt_int(c.args, "timeoutMs"), error);
	if (!error.empty()) {
		auto r = with_output(shot_output(s, error));
		r.tool_error = error;
		return r;
	}
	if (!s.ok || s.jpeg.empty()) {
		auto r = with_output(shot_output(s, s.message));
		r.tool_error = s.message.empty() ? "screenshot capture failed" : s.message;
		return r;
	}
	auto r = with_output(shot_output(s, s.message));
	r.image_base64 = base64::encode(s.jpeg);
	return r;
}

Value host_state_output(const HostState &h)
{
	Value power = Value::object();
	power.set("powerButton", Value::boolean(h.power_button));
	power.set("resetButton", Value::boolean(h.reset_button));
	power.set("known", Value::boolean(h.known));
	power.set("on", Value::boolean(h.on));
	power.set("diskActivity", Value::boolean(h.disk_activity));
	if (!h.power_error.empty()) power.set("error", Value::string(h.power_error));
	Value video = Value::object();
	video.set("capture", Value::boolean(h.capture));
	video.set("signal", Value::boolean(h.signal));
	Value keyboard = Value::object();
	keyboard.set("known", Value::boolean(h.keys_known));
	keyboard.set("capsLock", Value::boolean(h.caps_lock));
	keyboard.set("numLock", Value::boolean(h.num_lock));
	keyboard.set("scrollLock", Value::boolean(h.scroll_lock));
	Value out = Value::object();
	out.set("power", power);
	out.set("video", video);
	out.set("keyboard", keyboard);
	return out;
}

ToolResult host_status(Call &c)
{
	if (!c.devices.host_status) {
		auto r = with_output(host_state_output({}));
		r.tool_error = "host status is unavailable";
		return r;
	}
	return with_output(host_state_output(c.devices.host_status()));
}

Value screen_text_output(bool ok, const ScreenText &t)
{
	Value out = Value::object();
	out.set("ok", Value::boolean(ok));
	out.set("language", Value::string(t.language));
	out.set("width", Value::number(t.width));
	out.set("height", Value::number(t.height));
	out.set("text", Value::string(t.text));
	if (t.pieces_null && t.pieces.empty()) {
		out.set("pieces", Value::null());
	} else {
		Value pieces = Value::array();
		for (const auto &p : t.pieces) {
			Value box = Value::object();
			box.set("x", Value::number(p.box.x));
			box.set("y", Value::number(p.box.y));
			box.set("w", Value::number(p.box.w));
			box.set("h", Value::number(p.box.h));
			Value piece = Value::object();
			piece.set("text", Value::string(p.text));
			piece.set("score", Value::number(p.score));
			piece.set("box", box);
			pieces.arr.push_back(piece);
		}
		out.set("pieces", pieces);
	}
	return out;
}

ToolResult screen_text(Call &c)
{
	auto fail = [](const std::string &message) {
		auto r = with_output(screen_text_output(false, {}));
		r.tool_error = message;
		return r;
	};
	if (!c.devices.read_text) return fail(no_text_reader);
	std::optional<ScreenBox> area;
	if (const Value *a = arg(c.args, "area")) {
		auto num = [&](const char *k) {
			auto v = a->get(k);
			return v && v->is_number() ? v->num : 0.0;
		};
		ScreenBox box{num("x"), num("y"), num("w"), num("h")};
		if (box.w <= 0 || box.h <= 0 || box.x + box.w > 1 || box.y + box.h > 1) return fail("area must be non-empty and lie within the screen");
		area = box;
	}
	ScreenText text;
	std::string error;
	if (!c.devices.read_text(arg_string(c.args, "language"), area, text, error)) return fail(error);
	return with_output(screen_text_output(true, text));
}

ToolResult power(Call &c)
{
	static const std::map<std::string, std::string> actions = {{"on", "On"}, {"shutdown", "GracefulShutdown"}, {"force_off", "ForceOff"},
	                                                           {"reset", "ForceRestart"}, {"cycle", "PowerCycle"}, {"press", "PushPowerButton"}};
	std::string raw = arg_string(c.args, "action");
	auto it = actions.find(json::to_lower(json::trim_space(raw)));
	if (it == actions.end()) return go_error("unknown power action: " + raw);
	ControlHold h;
	std::string error;
	if (!begin_control(h, error)) return go_error(error);
	if (auto e = c.devices.power(it->second); !e.empty()) return go_error(e);
	return ok_result();
}

ToolResult virtual_media(Call &c)
{
	std::string raw = arg_string(c.args, "action");
	std::string action = json::to_lower(json::trim_space(raw));
	std::string file = arg_string(c.args, "file");
	const Value *cdrom_v = arg(c.args, "cdrom");
	bool cdrom = cdrom_v && cdrom_v->is_bool() && cdrom_v->b;
	if (action != "list" && action != "status" && action != "mount" && action != "eject") return go_error("unknown virtual media action: " + raw);
	if (action == "mount" && file.empty()) return go_error("mount needs file, one of the listed images");
	ControlHold h;
	std::string error;
	if (!begin_control(h, error)) return go_error(error);
	Value out = Value::object();
	std::vector<std::string> images;
	bool listed = false;
	if (action == "list") {
		images = c.devices.media_images(error);
		if (!error.empty()) return go_error(error);
		listed = true;
	} else if (action == "mount" || action == "eject") {
		std::string name = action == "mount" ? file : std::string();
		if (auto e = c.devices.media_mount(name, cdrom && !name.empty()); !e.empty()) return go_error(e);
	}
	bool mounted_cdrom = false;
	std::string mount_error;
	std::string mounted = c.devices.media_mounted(mounted_cdrom, mount_error);
	out.set("ok", Value::boolean(true));
	if (listed && !images.empty()) {
		Value list = Value::array();
		for (const auto &i : images) list.arr.push_back(Value::string(i));
		out.set("images", list);
	}
	out.set("mounted", Value::string(mounted));
	out.set("cdrom", Value::boolean(mounted_cdrom));
	if (!mount_error.empty()) out.set("unavailable", Value::string(mount_error));
	return with_output(out);
}

ToolResult wake_on_lan(Call &c)
{
	ControlHold h;
	std::string error;
	if (!begin_control(h, error)) return go_error(error);
	if (auto e = c.devices.wake(arg_string(c.args, "mac"), arg_string(c.args, "interface")); !e.empty()) return go_error(e);
	return ok_result();
}

ToolResult run_tool(const std::string &name, Devices &devices, const Value &args)
{
	Call c{devices, args, name};
	if (name == "type_text") return type_text(c);
	if (name == "press_keys") return press_keys(c);
	if (name == "move_mouse") return move_mouse(c);
	if (name == "click_mouse") return click_mouse(c);
	if (name == "drag_mouse") return drag_mouse(c);
	if (name == "scroll_mouse") return scroll_mouse(c);
	if (name == "screenshot") return screenshot(c);
	if (name == "host_status") return host_status(c);
	if (name == "screen_text") return screen_text(c);
	if (name == "power") return power(c);
	if (name == "virtual_media") return virtual_media(c);
	if (name == "wake_on_lan") return wake_on_lan(c);
	return go_error("unknown tool");
}

// --- Methods ---

struct Session {
	Devices &devices;
	std::vector<Tool> tools;
	std::string_view body;
};

// "handling 'm': invalid params: unmarshaling %q into a *mcp.T: <err>" for a params type mismatch; "" when fine.
std::string params_type_error(const Session &s, const Message &m, const Value &params, const MethodInfo &info,
                              const std::vector<std::pair<const char *, Value::Kind>> &fields)
{
	std::string_view raw = s.body.substr(params.offset, params.end - params.offset);
	std::string prefix = std::string("unmarshaling ") + json::go_quote(raw) + " into a *mcp." + info.params_type + ": ";
	if (!params.is_object() && !params.is_null())
		return prefix + type_error(raw, params, params.offset, std::string("Go value of type mcp.") + info.params_type);
	for (const auto &[name, kind] : fields) {
		const Value *f = params.get(name);
		if (!f || f->is_null() || f->kind == kind) continue;
		return prefix + type_error(raw, *f, params.offset, std::string("Go struct field mcp.") + info.params_type + "." + name + " of type " +
		                                                       (kind == Value::Kind::string ? "string" : "object"));
	}
	(void)m;
	return {};
}

std::string handle_call(Session &s, const Message &m, const MethodInfo &info)
{
	const std::string &method = m.method;
	static const Value null_value;
	const Value &params = m.params ? *m.params : null_value;
	auto invalid = [&](const std::string &e) { return response_error(m.id, -32602, "handling '" + method + "': invalid params: " + e); };

	if (method == "initialize") {
		if (auto e = params_type_error(s, m, params, info, {{"protocolVersion", Value::Kind::string}}); !e.empty())
			return response_error(m.id, 0, "handling 'initialize': " + e);
		if (params.is_null()) return response_error(m.id, 0, "handling 'initialize': missing required \"params\"");
		std::string requested;
		if (auto v = params.get("protocolVersion"); v && v->is_string()) requested = v->str;
		std::string version = version_latest;
		if (requested != version_new && std::find(supported_versions.begin(), supported_versions.end(), requested) != supported_versions.end())
			version = requested;
		return response_ok(m.id, std::string(R"({"capabilities":{"logging":{},"tools":{"listChanged":true}},"protocolVersion":")") + version +
		                             R"(","serverInfo":{"name":")" + server_name + R"(","version":")" + server_version + R"("}})");
	}
	std::vector<std::pair<const char *, Value::Kind>> fields;
	if (method == "tools/list" || method == "resources/list" || method == "prompts/list" || method == "resources/templates/list")
		fields = {{"cursor", Value::Kind::string}};
	if (method == "tools/call") fields = {{"name", Value::Kind::string}};
	if (auto e = params_type_error(s, m, params, info, fields); !e.empty()) return invalid(e);

	if (method == "ping") return response_ok(m.id, "{}");
	if (method == "tools/list" || method == "resources/list" || method == "prompts/list" || method == "resources/templates/list") {
		if (auto c = params.get("cursor"); c && c->is_string() && !c->str.empty()) return response_error(m.id, -32602, "invalid params");
		std::string list;
		if (method == "tools/list") {
			for (const auto &t : s.tools) list += (list.empty() ? "" : ",") + t.json;
			return response_ok(m.id, R"({"ttlMs":0,"cacheScope":"public","tools":[)" + list + "]}");
		}
		const char *key = method == "resources/list" ? "resources" : method == "prompts/list" ? "prompts" : "resourceTemplates";
		return response_ok(m.id, std::string(R"({"ttlMs":0,"cacheScope":"public",")") + key + "\":[]}");
	}
	if (method == "logging/setLevel") return response_ok(m.id, "{}");
	if (method == "completion/complete") return response_error(m.id, -32602, "invalid params: missing required 'ref' field");
	if (method == "resources/read") {
		std::string uri;
		if (auto u = params.get("uri"); u && u->is_string()) uri = u->str;
		return response_error(m.id, -32602, "Resource not found", "{\"uri\":" + json::quote(uri, false) + "}");
	}
	if (method == "prompts/get") {
		std::string name;
		if (auto n = params.get("name"); n && n->is_string()) name = n->str;
		return response_error(m.id, -32602, "unknown prompt " + json::go_quote(name));
	}
	if (method == "subscriptions/listen") return response_error(m.id, -32602, "invalid params: missing required 'notifications' field");
	if (method == "tools/call") {
		std::string name;
		if (auto n = params.get("name"); n && n->is_string()) name = n->str;
		auto it = std::find_if(s.tools.begin(), s.tools.end(), [&](const Tool &t) { return t.name == name; });
		if (it == s.tools.end()) return response_error(m.id, -32602, "unknown tool " + json::go_quote(name));
		Value args = Value::object();
		if (const Value *a = params.get("arguments"); a && !a->is_null()) {
			if (!a->is_object()) {
				std::string_view raw = s.body.substr(a->offset, a->end - a->offset);
				ToolResult r = go_error("validating \"arguments\": unmarshaling arguments: json: cannot unmarshal " +
				                        json::go_quote(input_prefix(raw)) + " into Go value of type map[string]interface {}");
				return response_ok(m.id, encode_result(r));
			}
			args = *a;
		}
		if (auto e = validate(args, it->input_schema, ""); !e.empty())
			return response_ok(m.id, encode_result(go_error("validating \"arguments\": " + e)));
		return response_ok(m.id, encode_result(run_tool(name, s.devices, args)));
	}
	return response_error(m.id, -32601, "method not found: " + json::go_quote(method));
}

// mime.ParseMediaType's media type, or "" when it does not parse.
std::string base_media_type(const std::string &value)
{
	std::string v = value;
	auto semi = v.find(';');
	std::string type = json::to_lower(json::trim_space(v.substr(0, semi)));
	auto token = [](const std::string &t) {
		if (t.empty()) return false;
		for (char c : t)
			if (c <= ' ' || c >= 0x7f || std::string_view("()<>@,;:\\\"/[]?=").find(c) != std::string_view::npos) return false;
		return true;
	};
	auto slash = type.find('/');
	if (slash == std::string::npos || !token(type.substr(0, slash)) || !token(type.substr(slash + 1))) return {};
	// Parameters: each must be token=value (quoted or token).
	std::string rest = semi == std::string::npos ? "" : v.substr(semi + 1);
	while (!json::trim_space(rest).empty()) {
		rest = json::trim_space(rest);
		auto eq = rest.find('=');
		if (eq == std::string::npos) return {};
		std::string key = json::trim_space(rest.substr(0, eq));
		if (!token(key)) return {};
		rest = json::trim_space(rest.substr(eq + 1));
		if (!rest.empty() && rest[0] == '"') {
			auto close = rest.find('"', 1);
			if (close == std::string::npos) return {};
			rest = rest.substr(close + 1);
		} else {
			std::size_t k = 0;
			while (k < rest.size() && rest[k] != ';') k++;
			if (!token(json::trim_space(rest.substr(0, k)))) return {};
			rest = rest.substr(k);
		}
		rest = json::trim_space(rest);
		if (rest.empty()) break;
		if (rest[0] != ';') return {};
		rest = rest.substr(1);
	}
	return type;
}

bool loopback_host(const std::string &host)
{
	std::string h = host;
	if (!h.empty() && h[0] == '[') {
		auto close = h.find(']');
		h = close == std::string::npos ? h : h.substr(1, close - 1);
	} else if (std::count(h.begin(), h.end(), ':') == 1) {
		h = h.substr(0, h.find(':'));
	}
	return h == "localhost" || h == "::1" || h.rfind("127.", 0) == 0;
}

} // namespace

// --- Config API ---

bool load_config(Config &config, std::string &error)
{
	std::lock_guard lock(config_mu);
	return load_config_locked(config, error);
}

bool ensure_api_key(Config &config, std::string &error)
{
	return update_config([](Config &c, std::string &e) { return !c.api_key.empty() || generate_api_key(c.api_key, e); }, config, error);
}

bool regenerate_api_key(Config &config, std::string &error)
{
	return update_config([](Config &c, std::string &e) { return generate_api_key(c.api_key, e); }, config, error);
}

bool set_host_control(bool allowed, Config &config, std::string &error)
{
	std::unique_lock gate(host_gate);
	return update_config([allowed](Config &c, std::string &) {
		c.allow_host_control = allowed;
		return true;
	}, config, error);
}

std::string config_json(const Config &config, const control::ModeStatus &status)
{
	bool enabled = status.mode == control::Mode::mcp && !status.transitioning;
	return std::string("{\"enabled\":") + (enabled ? "true" : "false") + ",\"apiKey\":" + json::quote(config.api_key) +
	       ",\"controlMode\":\"" + control::mode_name(status.mode) + "\",\"transitioning\":" + (status.transitioning ? "true" : "false") +
	       ",\"allowHostControl\":" + (config.allow_host_control ? "true" : "false") + "}";
}

// --- The endpoint ---

HttpResponse serve(const HttpRequest &req, Devices &devices)
{
	// APIKeyMiddleware.
	if (!control::require_write(control::Mode::mcp).empty()) return gin_json(403, "MCP service is disabled");
	Config config;
	std::string error;
	if (!load_config(config, error)) {
		log::error("failed to load MCP config for authentication: {}", error);
		return gin_json(500, "MCP auth unavailable");
	}
	std::string authorization = req.header("Authorization");
	std::string token;
	if (authorization.rfind("Bearer ", 0) == 0) token = json::trim_space(authorization.substr(7));
	if (token.empty() || config.api_key.empty() || token.size() != config.api_key.size() ||
	    CRYPTO_memcmp(token.data(), config.api_key.data(), token.size()) != 0)
		return gin_json(401, "invalid MCP API key");

	// http.CrossOriginProtection.
	if (req.method != "GET" && req.method != "HEAD" && req.method != "OPTIONS") {
		std::string site = req.header("Sec-Fetch-Site");
		if (!site.empty() && site != "same-origin" && site != "none")
			return text_error(403, "cross-origin request detected from Sec-Fetch-Site header");
		if (site.empty()) {
			std::string origin = req.header("Origin");
			if (!origin.empty()) {
				std::string host;
				if (auto p = origin.find("://"); p != std::string::npos) {
					host = origin.substr(p + 3);
					host = host.substr(0, host.find_first_of("/?#"));
					if (auto at = host.rfind('@'); at != std::string::npos) host = host.substr(at + 1);
				}
				if (host != req.host)
					return text_error(403, "cross-origin request detected, and/or browser is out of date: Sec-Fetch-Site is missing, and Origin does not match Host");
			}
		}
	}

	// The SDK's DNS rebinding protection: a request that reached a loopback address must name a loopback host.
	if (req.loopback && !loopback_host(req.host)) return text_error(403, "Forbidden: invalid Host header " + json::go_quote(req.host));

	std::string version = req.header("Mcp-Protocol-Version");
	if (!version.empty() && std::find(supported_versions.begin(), supported_versions.end(), version) == supported_versions.end() &&
	    version < version_new) {
		std::string list;
		for (const auto &v : supported_versions) list += (list.empty() ? "" : ",") + v;
		return text_error(400, "Bad Request: Unsupported protocol version (supported versions: " + list + ")");
	}
	if (req.method != "POST") {
		auto r = text_error(405, "Method Not Allowed");
		r.allow = "POST";
		return r;
	}
	if (base_media_type(req.header("Content-Type")) != "application/json")
		return text_error(415, "Content-Type must be 'application/json'");
	bool json_ok = false, stream_ok = false;
	for (const auto &value : req.header_values("Accept")) {
		std::size_t start = 0;
		while (start <= value.size()) {
			auto comma = value.find(',', start);
			std::string token = json::trim_space(value.substr(start, comma == std::string::npos ? std::string::npos : comma - start));
			std::string base = json::to_lower(json::trim_space(token.substr(0, token.find(';'))));
			if (base == "application/json" || base == "application/*") json_ok = true;
			if (base == "text/event-stream" || base == "text/*") stream_ok = true;
			if (base == "*/*") json_ok = stream_ok = true;
			if (comma == std::string::npos) break;
			start = comma + 1;
		}
	}
	if (!json_ok || !stream_ok) return text_error(400, "Accept must contain both 'application/json' and 'text/event-stream'");
	if (req.body.size() > max_request_body) return text_error(413, "request body exceeds " + std::to_string(max_request_body) + " bytes");
	if (!req.header_values("Last-Event-ID").empty()) return text_error(400, "can't send Last-Event-ID for POST request");
	if (req.body.empty()) return text_error(400, "POST requires a non-empty body");

	// readBatch: an array of messages, else one message (the decoder reads one value and ignores what follows).
	std::string_view body = req.body;
	std::vector<Message> messages;
	bool batch = false;
	{
		Value whole;
		auto err = json::parse(body, whole);
		if (!err && (whole.is_array() || whole.is_null())) {
			batch = true;
			if (whole.arr.empty()) return text_error(400, "malformed payload: empty batch");
			for (const auto &item : whole.arr) {
				Message m;
				std::string e;
				if (!decode_message(body, item, item.offset, item.end, m, e)) return text_error(400, "malformed payload: " + e);
				messages.push_back(std::move(m));
			}
		} else {
			// One message: a value followed by anything.
			Value one;
			std::size_t end = body.size();
			auto e1 = json::parse(body, one);
			if (e1 && e1->message.find("after top-level value") != std::string::npos) {
				end = e1->offset;
				e1 = json::parse(body.substr(0, end), one);
			}
			if (e1) return text_error(400, "malformed payload: unmarshaling jsonrpc message: " + syntax_error(body, *e1));
			Message m;
			std::string e;
			if (!decode_message(body.substr(0, end), one, 0, end, m, e)) return text_error(400, "malformed payload: " + e);
			messages.push_back(std::move(m));
		}
	}
	std::string effective = version.empty() ? version_default : version;
	if (batch && effective >= "2025-06-18")
		return text_error(400, "JSON-RPC batching is not supported in 2025-06-18 and later (request version: " + effective + ")");

	bool calls = false;
	for (const auto &m : messages) {
		if (!m.request) continue;
		auto it = method_infos().find(m.method);
		if (it == method_infos().end()) return text_error(400, "JSON RPC not handled: " + json::go_quote(m.method) + " unsupported");
		bool is_call = m.id.kind != Id::Kind::none;
		if (it->second.notification && is_call) return text_error(400, "invalid request: unexpected id for " + json::go_quote(m.method));
		if (!it->second.notification && !is_call) return text_error(400, "invalid request: missing id for " + json::go_quote(m.method));
		if (!it->second.missing_params_ok && !m.params) return text_error(400, "invalid request: missing required \"params\"");
		// The 2026-07-28 protocol: its calls carry the version in _meta and in the header.
		std::string meta_version;
		if (m.params && m.params->is_object())
			if (auto meta = m.params->get("_meta"); meta && meta->is_object())
				if (auto v = meta->get("io.modelcontextprotocol/protocolVersion"); v && v->is_string()) meta_version = v->str;
		if ((is_call && effective >= version_new) || !meta_version.empty()) {
			HttpResponse r;
			r.status = 400;
			r.content_type = "application/json";
			if (version.empty())
				r.body = response_error(m.id, -32020, "Mcp-Protocol-Version header is required for requests carrying \"io.modelcontextprotocol/protocolVersion\"");
			else if (meta_version.empty())
				r.body = response_error(m.id, -32602, "missing or invalid _meta field \"io.modelcontextprotocol/protocolVersion\"");
			else if (version != meta_version)
				r.body = response_error(m.id, -32020, "Mcp-Protocol-Version header " + json::go_quote(version) +
				                                          " does not match request io.modelcontextprotocol/protocolVersion " + json::go_quote(meta_version));
			else
				r.body = {};
			if (!r.body.empty()) return r;
		}
		if (is_call) calls = true;
	}
	if (!batch && messages.size() == 1 && effective >= version_new && messages[0].request) {
		std::string method = req.header("Mcp-Method");
		HttpResponse r;
		r.status = 400;
		r.content_type = "application/json";
		if (method.empty()) r.body = response_error(messages[0].id, -32020, "missing required Mcp-Method header");
		else if (method != messages[0].method)
			r.body = response_error(messages[0].id, -32020, "header mismatch: Mcp-Method header value '" + method +
			                                                     "' does not match body value '" + messages[0].method + "'");
		if (!r.body.empty()) return r;
	}
	if (!calls) {
		HttpResponse r;
		r.status = 202;
		return r;
	}

	Session session{devices, tool_set(host_control_allowed(), devices.text_available && devices.text_available(), devices), body};
	std::vector<std::string> responses;
	std::set<std::string> answered;  // a batch's calls by id: a repeated id is answered once
	bool listen = false;
	for (const auto &m : messages) {
		if (!m.request || m.id.kind == Id::Kind::none) continue;
		if (!answered.insert(m.id.json()).second) continue;
		listen = listen || m.method == "subscriptions/listen";
		responses.push_back(handle_call(session, m, method_infos().at(m.method)));
	}
	HttpResponse r;
	r.content_type = "application/json";
	r.cache_control = "no-cache, no-transform";
	if (listen && !batch && !responses.empty()) {
		// subscriptions/listen always answers on an event stream.
		r.content_type = "text/event-stream";
		r.body = "event: message\ndata: " + responses[0] + "\n\n";
		return r;
	}
	if (batch) {
		r.body = "[";
		for (std::size_t i = 0; i < responses.size(); i++) r.body += (i ? "," : "") + responses[i];
		r.body += "]";
	} else {
		r.body = responses.empty() ? std::string() : responses[0];
	}
	return r;
}

} // namespace nkos::ai::mcp
