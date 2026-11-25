#include "notify/model.hpp"

#include "notify/gostd.hpp"
#include "notify_messages.inc"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <fstream>
#include <iterator>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::notify {

namespace {

constexpr std::size_t max_value_bytes = 2048, max_templates = 16, max_title_bytes = 256, max_body_bytes = 2048,
                      max_config_bytes = 512u << 10, max_refresh_bytes = 8192;

bool contains(const std::vector<std::string> &list, std::string_view item)
{
	return std::find(list.begin(), list.end(), item) != list.end();
}

std::string fmt_size(std::size_t n) { return std::to_string(n); }

} // namespace

const std::vector<std::string> &kinds()
{
	static const std::vector<std::string> k = {"power", "hdmi", "security", "screen"};
	return k;
}

const std::vector<std::string> &languages()
{
	static const std::vector<std::string> l = {"en", "ca", "cz", "da", "de", "es", "fr", "he", "hu", "id", "it", "ja", "ko",
	                                           "nb", "nl", "pl", "pt_br", "ru", "se", "th", "tr", "uk", "vi", "zh", "zh_tw"};
	return l;
}

const std::vector<std::string> &common_vars()
{
	static const std::vector<std::string> v = {"device", "time", "details"};
	return v;
}

std::string severity_name(Severity s)
{
	switch (s) {
	case Severity::warning: return "warning";
	case Severity::critical: return "critical";
	default: return "info";
	}
}

const std::vector<MessageSpec> &message_specs()
{
	static const std::vector<MessageSpec> specs = {
		{"power.on", "power", {}},
		{"power.off", "power", {}},
		{"hdmi.lost", "hdmi", {}},
		{"hdmi.restored", "hdmi", {}},
		{"hdmi.power-cycle", "hdmi", {}},
		{"security.lockout", "security", {"failures", "address", "minutes"}},
		{"security.attempt-limit", "security", {"attempts", "address", "minutes"}},
		{"security.new-address", "security", {"user", "address"}},
		{"watchdog.agent-silent", "power", {"minutes"}},
		{"watchdog.host-silent", "power", {"minutes"}},
		{"watchdog.reset", "power", {"minutes"}},
		{"watchdog.cycle", "power", {"minutes"}},
		{"watchdog.failed", "power", {"minutes", "error"}},
		{"watchdog.unknown", "power", {"minutes", "error"}},
		{"screen.bsod", "screen", {}},
		{"screen.panic", "screen", {}},
		{"test", "test", {}},
	};
	return specs;
}

const MessageSpec *message_spec(std::string_view key)
{
	for (const auto &s : message_specs())
		if (s.key == key) return &s;
	return nullptr;
}

namespace {

bool decode_message(const json::Value &v, Message &m)
{
	return json::members(v, {"title", "body"}, [&](std::string_view f, const json::Value &x) {
		return json::get(x, f == "title" ? m.title : m.body);
	});
}

bool decode_messages(const json::Value &v, std::optional<std::map<std::string, Message>> &out)
{
	if (v.is_null()) {
		out.reset();
		return true;
	}
	if (v.kind != json::Value::Kind::object) return false;
	if (!out) out.emplace();
	for (const auto &[key, value] : v.members) {
		Message m;
		if (!decode_message(value, m)) return false;
		(*out)[key] = std::move(m);
	}
	return true;
}

// The standard messages of every language, embedded from the catalogues of the web interface.
const std::map<std::string, std::map<std::string, Message>> &catalogues()
{
	static const auto all = [] {
		std::map<std::string, std::map<std::string, Message>> out;
		for (const auto &[language, text] : system::notify::catalogue::files) {
			json::Value v;
			std::optional<std::map<std::string, Message>> c;
			if (json::unmarshal(text, v) && decode_messages(v, c) && c) out[std::string(language)] = std::move(*c);
		}
		return out;
	}();
	return all;
}

} // namespace

Message standard_message(const std::string &language, const std::string &key)
{
	const auto &all = catalogues();
	for (const std::string &lang : {language, languages()[0]}) {
		auto c = all.find(lang);
		if (c == all.end()) continue;
		if (auto m = c->second.find(key); m != c->second.end()) return m->second;
	}
	return {key, ""};
}

std::string Channel::get(const std::string &key) const
{
	if (!settings) return "";
	auto it = settings->find(key);
	return it == settings->end() ? "" : gostd::trim_space(it->second);
}

bool Channel::wants(std::string_view kind) const
{
	return kind == kind_test || (events && std::find(events->begin(), events->end(), kind) != events->end());
}

const Template *Config::find_template(const std::string &id) const
{
	if (!templates) return nullptr;
	for (const auto &t : *templates)
		if (t.id == id) return &t;
	return nullptr;
}

bool Field::shown(const std::map<std::string, std::string> &settings) const
{
	for (const auto &[key, values] : show) {
		auto it = settings.find(key);
		std::string v = it == settings.end() ? "" : it->second;
		if (!contains(values, v)) return false;
	}
	return true;
}

const std::vector<Provider> &providers()
{
	static const std::vector<Provider> p = {
		{"gmail", "smtp.gmail.com", "587", "starttls", {"oauth2", "password"}, "google", ""},
		{"microsoft365", "smtp.office365.com", "587", "starttls", {"oauth2", "password"}, "microsoft", "organizations"},
		{"outlook", "smtp-mail.outlook.com", "587", "starttls", {"oauth2"}, "microsoft", "consumers"},
		{"yahoo", "smtp.mail.yahoo.com", "465", "tls", {"password"}, "", ""},
		{"icloud", "smtp.mail.me.com", "587", "starttls", {"password"}, "", ""},
		{"zoho", "smtp.zoho.com", "465", "tls", {"password"}, "", ""},
		{"fastmail", "smtp.fastmail.com", "465", "tls", {"password"}, "", ""},
		{"yandex", "smtp.yandex.com", "465", "tls", {"password"}, "", ""},
		{"mailru", "smtp.mail.ru", "465", "tls", {"password"}, "", ""},
		{"proton", "smtp.protonmail.ch", "587", "starttls", {"password"}, "", ""},
		{"custom", "", "", "", {"password"}, "", ""},
	};
	return p;
}

const Provider &provider_by_id(std::string_view id)
{
	for (const auto &p : providers())
		if (p.id == id) return p;
	return providers().back();
}

const std::vector<ChannelType> &channel_types()
{
	static const std::vector<ChannelType> types = [] {
		using Show = std::vector<std::pair<std::string, std::vector<std::string>>>;
		const Show oauth_only = {{"auth", {"oauth2"}}};
		const Show password_only = {{"auth", {"password"}}};
		const Show google = {{"auth", {"oauth2"}}, {"provider", {"gmail"}}};
		const Show microsoft = {{"auth", {"oauth2"}}, {"provider", {"microsoft365", "outlook"}}};
		std::vector<std::string> ids;
		for (const auto &p : providers()) ids.push_back(p.id);
		auto f = [](std::string key, std::string kind) {
			Field x;
			x.key = std::move(key);
			x.kind = std::move(kind);
			return x;
		};
		auto secret = [](Field x) { x.secret = true; return x; };
		auto required = [](Field x) { x.required = true; return x; };
		auto ph = [](Field x, std::string p) { x.placeholder = std::move(p); return x; };
		auto def = [](Field x, std::string d) { x.default_value = std::move(d); return x; };
		auto opts = [](Field x, std::vector<std::string> o) { x.options = std::move(o); return x; };
		auto show = [](Field x, Show s) { x.show = std::move(s); return x; };
		const Field skip_verify = f("skipVerify", "bool");
		Field oauth_token = show(secret(f("oauthToken", "oauth")), oauth_only);
		oauth_token.max_bytes = static_cast<int>(max_refresh_bytes);
		return std::vector<ChannelType>{
			{"telegram",
			 {ph(required(secret(f("token", "text"))), "123456:ABC-DEF…"),
			  ph(required(f("chatId", "text")), "-1001234567890"), f("threadId", "number"),
			  ph(f("apiUrl", "url"), "https://api.telegram.org")},
			 true},
			{"email",
			 {def(opts(f("provider", "select"), ids), "custom"), ph(required(f("host", "text")), "smtp.example.com"),
			  def(f("port", "number"), "587"), def(opts(f("security", "select"), {"starttls", "tls", "none"}), "starttls"),
			  def(opts(f("auth", "select"), {"password", "oauth2"}), "password"), f("username", "text"),
			  show(secret(f("password", "text")), password_only), show(f("oauthClientId", "text"), oauth_only),
			  show(secret(f("oauthClientSecret", "text")), google), show(f("oauthTenant", "text"), microsoft), oauth_token,
			  show(f("oauthAccount", "hidden"), oauth_only), ph(required(f("from", "email")), "nanokvm@example.com"),
			  ph(required(f("to", "text")), "admin@example.com, ops@example.com"), skip_verify},
			 true},
			{"webhook",
			 {ph(required(f("url", "url")), "https://example.com/hooks/nanokvm"), secret(f("hmacSecret", "text")),
			  ph(secret(f("authorization", "text")), "Bearer …"), skip_verify},
			 true},
			{"slack", {ph(required(secret(f("url", "url"))), "https://hooks.slack.com/services/…"), skip_verify}, false},
			{"teams", {ph(required(secret(f("url", "url"))), "https://….powerplatform.com/…/invoke?…")}, false},
			{"googlechat", {ph(required(secret(f("url", "url"))), "https://chat.googleapis.com/v1/spaces/…")}, false},
			{"discord", {ph(required(secret(f("url", "url"))), "https://discord.com/api/webhooks/…")}, true},
			{"ntfy",
			 {def(f("server", "url"), "https://ntfy.sh"), required(secret(f("topic", "text"))),
			  ph(secret(f("token", "text")), "tk_…"), skip_verify},
			 false},
			{"gotify",
			 {ph(required(f("server", "url")), "https://gotify.example.com"), required(secret(f("token", "text"))), skip_verify},
			 false},
			{"pushover",
			 {required(secret(f("appToken", "text"))), required(secret(f("userKey", "text"))), f("device", "text")},
			 true},
			{"matrix",
			 {ph(required(f("homeserver", "url")), "https://matrix.example.com"), required(secret(f("accessToken", "text"))),
			  ph(required(f("roomId", "text")), "!abcdef:example.com"), skip_verify},
			 false},
			{"syslog",
			 {ph(required(f("host", "text")), "siem.example.com"), def(f("port", "number"), "514"),
			  def(opts(f("protocol", "select"), {"udp", "tcp", "tls"}), "udp"), skip_verify},
			 false},
		};
	}();
	return types;
}

const ChannelType *channel_type(std::string_view name)
{
	for (const auto &t : channel_types())
		if (t.type == name) return &t;
	return nullptr;
}

// ---- JSON ----

bool decode_template(const json::Value &v, Template &t)
{
	return json::members(v, {"id", "name", "messages"}, [&](std::string_view f, const json::Value &x) {
		if (f == "id") return json::get(x, t.id);
		if (f == "name") return json::get(x, t.name);
		return decode_messages(x, t.messages);
	});
}

json::Out encode_template(const Template &t)
{
	json::Out messages = json::Out::null();
	if (t.messages) {
		messages = json::Out::object();
		for (const auto &[key, m] : *t.messages)
			messages.set(key, json::Out::object().set("title", json::Out::str(m.title)).set("body", json::Out::str(m.body)));
	}
	return json::Out::object().set("id", json::Out::str(t.id)).set("name", json::Out::str(t.name)).set("messages", std::move(messages));
}

namespace {

bool decode_channel_field(std::string_view f, const json::Value &x, Channel &ch)
{
	if (f == "id") return json::get(x, ch.id);
	if (f == "type") return json::get(x, ch.type);
	if (f == "name") return json::get(x, ch.name);
	if (f == "enabled") return json::get(x, ch.enabled);
	if (f == "events") return json::get(x, ch.events);
	if (f == "settings") return json::get(x, ch.settings);
	return json::get(x, ch.templ);
}

#define NKOS_CHANNEL_FIELDS "id", "type", "name", "enabled", "events", "settings", "template"

bool decode_channel(const json::Value &v, Channel &ch)
{
	return json::members(v, {NKOS_CHANNEL_FIELDS},
	                     [&](std::string_view f, const json::Value &x) { return decode_channel_field(f, x, ch); });
}

template <typename T, typename F>
bool decode_list(const json::Value &v, std::optional<std::vector<T>> &out, F &&item)
{
	if (v.is_null()) {
		out.reset();
		return true;
	}
	if (v.kind != json::Value::Kind::array) return false;
	std::vector<T> list;
	for (const auto &x : v.items) {
		T t{};
		if (!item(x, t)) return false;
		list.push_back(std::move(t));
	}
	out = std::move(list);
	return true;
}

// The fields of a Channel, in order.
void encode_channel_fields(json::Out &o, const Channel &ch)
{
	o.set("id", json::Out::str(ch.id))
		.set("type", json::Out::str(ch.type))
		.set("name", json::Out::str(ch.name))
		.set("enabled", json::Out::boolean(ch.enabled))
		.set("events", json::Out::strings(ch.events))
		.set("settings", json::Out::string_map(ch.settings));
	if (!ch.templ.empty()) o.set("template", json::Out::str(ch.templ));
}

} // namespace

bool decode_channel_update(const json::Value &v, ChannelUpdate &u)
{
	return json::members(v, {NKOS_CHANNEL_FIELDS, "clear", "templateDraft"}, [&](std::string_view f, const json::Value &x) {
		if (f == "clear") return json::get(x, u.clear);
		if (f == "templateDraft") {
			if (x.is_null()) {
				u.template_draft.reset();
				return true;
			}
			if (!u.template_draft) u.template_draft.emplace();
			return decode_template(x, *u.template_draft);
		}
		return decode_channel_field(f, x, u.channel);
	});
}

bool decode_config(const json::Value &v, Config &c)
{
	return json::members(v, {"language", "channels", "templates"}, [&](std::string_view f, const json::Value &x) {
		if (f == "language") return json::get(x, c.language);
		if (f == "channels") return decode_list(x, c.channels, decode_channel);
		return decode_list(x, c.templates, decode_template);
	});
}

json::Out encode_config(const Config &c)
{
	json::Out channels = json::Out::null(), templates = json::Out::null();
	if (c.channels) {
		channels = json::Out::array();
		for (const auto &ch : *c.channels) {
			json::Out o = json::Out::object();
			encode_channel_fields(o, ch);
			channels.add(std::move(o));
		}
	}
	if (c.templates) {
		templates = json::Out::array();
		for (const auto &t : *c.templates) templates.add(encode_template(t));
	}
	return json::Out::object()
		.set("language", json::Out::str(c.language))
		.set("channels", std::move(channels))
		.set("templates", std::move(templates));
}

bool bind_settings(std::string_view body, Settings &s)
{
	json::Value v;
	if (!json::decode_first(body, v)) return false;
	return json::members(v, {"language", "channels", "templates"}, [&](std::string_view f, const json::Value &x) {
		if (f == "language") return json::get(x, s.language);
		if (f == "channels") return decode_list(x, s.channels, decode_channel_update);
		return decode_list(x, s.templates, decode_template);
	});
}

bool bind_channel_update(std::string_view body, ChannelUpdate &u)
{
	json::Value v;
	return json::decode_first(body, v) && decode_channel_update(v, u);
}

bool bind_preview(std::string_view body, PreviewRequest &p)
{
	json::Value v;
	if (!json::decode_first(body, v)) return false;
	return json::members(v, {"language", "template"}, [&](std::string_view f, const json::Value &x) {
		if (f == "language") return json::get(x, p.language);
		if (x.is_null()) {
			p.templ.reset();
			return true;
		}
		if (!p.templ) p.templ.emplace();
		return decode_template(x, *p.templ);
	});
}

Config load(const std::string &path, std::string &error)
{
	Config c;
	error.clear();
	std::ifstream in(path, std::ios::binary);
	if (!in) {
		if (errno == ENOENT) return c;
		error = "open " + path + ": " + std::strerror(errno);
		return c;
	}
	std::string data((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
	json::Value v;
	if (!json::unmarshal(data, v) || !decode_config(v, c)) {
		error = "read " + path + ": invalid JSON";
		return Config{};
	}
	if (!contains(languages(), c.language)) c.language = languages()[0];
	return c;
}

std::string config_bytes(const Config &c, std::string &error)
{
	std::string data = json::marshal_indent(encode_config(c));
	error.clear();
	if (data.size() > max_config_bytes)
		error = "notification settings are larger than the " + fmt_size(max_config_bytes) + "-byte backup limit";
	return data;
}

// ---- checks ----

namespace {

bool is_template_id(std::string_view id)
{
	if (id.empty() || id.size() > 32) return false;
	for (char c : id)
		if (!((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'))) return false;
	return true;
}

bool is_tenant(std::string_view t)
{
	auto alnum = [](char c) { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'); };
	if (t.empty() || t.size() > 64 || !alnum(t[0])) return false;
	for (char c : t.substr(1))
		if (!alnum(c) && c != '.' && c != '-') return false;
	return true;
}

// The {name}s (names of a-z) of text, in order.
std::vector<std::string> variables(std::string_view text)
{
	std::vector<std::string> out;
	for (std::size_t i = 0; i < text.size(); i++) {
		if (text[i] != '{') continue;
		std::size_t j = i + 1;
		while (j < text.size() && text[j] >= 'a' && text[j] <= 'z') j++;
		if (j > i + 1 && j < text.size() && text[j] == '}') {
			out.emplace_back(text.substr(i + 1, j - i - 1));
			i = j;
		}
	}
	return out;
}

std::vector<std::string> unknown_vars(std::string_view text, const MessageSpec &spec)
{
	std::vector<std::string> unknown;
	for (auto &name : variables(text))
		if (!contains(spec.vars, name) && !contains(common_vars(), name) && !contains(unknown, name)) unknown.push_back(name);
	return unknown;
}

// strconv.ParseInt(value, 10, 64).
std::optional<std::int64_t> parse_int(std::string_view s)
{
	bool neg = false;
	if (!s.empty() && (s[0] == '+' || s[0] == '-')) {
		neg = s[0] == '-';
		s.remove_prefix(1);
	}
	if (s.empty()) return std::nullopt;
	unsigned long long v = 0;
	for (char c : s) {
		if (c < '0' || c > '9') return std::nullopt;
		if (v > (1ull << 63) / 10) return std::nullopt;
		v = v * 10 + static_cast<unsigned>(c - '0');
		if (v > (1ull << 63)) return std::nullopt;
	}
	if (!neg && v > static_cast<unsigned long long>(INT64_MAX)) return std::nullopt;
	return neg ? static_cast<std::int64_t>(0 - v) : static_cast<std::int64_t>(v);
}

std::string check_value(const Field &f, const std::string &value)
{
	if (f.kind == "url") {
		auto u = gostd::url::parse(value);
		if (!u || (u->scheme != "http" && u->scheme != "https") || u->host.empty()) return "not an http or https address";
	} else if (f.kind == "email") {
		if (!gostd::mail::parse_address(value)) return "not an e-mail address";
	} else if (f.kind == "number") {
		auto n = parse_int(value);
		if (!n || *n < 0 || (f.key == "port" && (*n < 1 || *n > 65535))) return "not a valid number";
	} else if (f.kind == "select") {
		if (!contains(f.options, value)) return "not one of the choices";
	} else if (f.kind == "bool") {
		if (value != "true" && value != "false") return "not true or false";
	}
	if (value.find_first_of("\r\n") != std::string::npos) return "must be one line";
	return "";
}

std::string validate_email(std::map<std::string, std::string> &settings)
{
	auto get = [&](const char *key) {
		auto it = settings.find(key);
		return it == settings.end() ? std::string() : it->second;
	};
	for (const auto &to : split_list(get("to")))
		if (!gostd::mail::parse_address(to)) return "to: " + gostd::quote(to) + " is not an address";
	if (get("auth") != "oauth2") {
		if (!get("username").empty() && get("security") == "none") return "a login needs STARTTLS or TLS";
		return "";
	}
	const Provider &p = provider_by_id(get("provider"));
	if (p.oauth.empty()) return "OAuth sign-in is for Gmail and Microsoft accounts";
	if (get("oauthClientId").empty()) return "oauthClientId is required";
	if (p.oauth == oauth_google && get("oauthClientSecret").empty()) return "oauthClientSecret is required";
	if (get("security") == "none") return "a sign-in needs STARTTLS or TLS";
	if (p.oauth == oauth_microsoft) {
		if (get("oauthTenant").empty()) settings["oauthTenant"] = p.tenant;
		if (!is_tenant(settings["oauthTenant"])) return "oauthTenant: not a tenant ID or domain";
	}
	return "";
}

} // namespace

std::string validate_template(Template &t)
{
	t.name = gostd::trim_space(t.name);
	if (t.name.empty() || t.name.size() > 64) return "a template needs a name of at most 64 characters";
	if (!is_template_id(t.id)) return t.name + ": invalid id";
	std::map<std::string, Message> kept;
	if (t.messages) {
		// Go goes through the messages in no fixed order; here in the order of their keys.
		for (const auto &[key, original] : *t.messages) {
			const MessageSpec *spec = message_spec(key);
			if (!spec) return t.name + ": unknown message " + gostd::quote(key);
			Message m{gostd::trim_space(original.title), gostd::trim_space(original.body)};
			if (m.title.empty() && m.body.empty()) continue;
			if (m.title.size() > max_title_bytes || m.title.find_first_of("\r\n") != std::string::npos)
				return t.name + ": " + key + ": the title is one line of at most " + fmt_size(max_title_bytes) + " bytes";
			if (m.body.size() > max_body_bytes)
				return t.name + ": " + key + ": the text is longer than " + fmt_size(max_body_bytes) + " bytes";
			if (m.title.find("{details}") != std::string::npos)
				return t.name + ": " + key + ": {details} goes in the text, not the title";
			auto unknown = unknown_vars(m.title + " " + m.body, *spec);
			if (!unknown.empty()) {
				std::string list;
				for (std::size_t i = 0; i < unknown.size(); i++) list += (i ? "}, {" : "") + unknown[i];
				return t.name + ": " + key + ": unknown variable {" + list + "}";
			}
			kept[key] = std::move(m);
		}
	}
	t.messages = std::move(kept);
	return "";
}

std::string validate_channel(Channel &ch)
{
	const ChannelType *t = channel_type(ch.type);
	if (!t) return "unknown channel type " + gostd::quote(ch.type);
	ch.name = gostd::trim_space(ch.name);
	if (ch.name.size() > 64) return "the name is longer than 64 characters";
	if (ch.events)
		for (const auto &kind : *ch.events)
			if (!contains(kinds(), kind)) return "unknown event " + gostd::quote(kind);
	std::map<std::string, std::string> settings;
	for (const auto &f : t->fields) {
		if (!f.shown(settings)) continue;
		std::string value;
		if (ch.settings)
			if (auto it = ch.settings->find(f.key); it != ch.settings->end()) value = gostd::trim_space(it->second);
		if (value.empty()) value = f.default_value;
		if (value.empty()) {
			if (f.required) return f.key + " is required";
			continue;
		}
		std::size_t limit = f.max_bytes > 0 ? static_cast<std::size_t>(f.max_bytes) : max_value_bytes;
		if (value.size() > limit) return f.key + " is too long";
		if (auto e = check_value(f, value); !e.empty()) return f.key + ": " + e;
		settings[f.key] = value;
	}
	std::string error;
	if (ch.type == "email") error = validate_email(settings);
	ch.settings = std::move(settings);
	return error;
}

std::string validate(Config &c)
{
	if (!contains(languages(), c.language)) return "unknown language " + gostd::quote(c.language);
	std::size_t channels = c.channels ? c.channels->size() : 0, templates = c.templates ? c.templates->size() : 0;
	if (channels > max_channels) return "at most " + fmt_size(max_channels) + " channels";
	if (templates > max_templates) return "at most " + fmt_size(max_templates) + " templates";
	std::map<std::string, bool> ids, names;
	if (c.templates) {
		for (auto &t : *c.templates) {
			if (auto e = validate_template(t); !e.empty()) return e;
			std::string name = gostd::to_lower(t.name);
			if (ids[t.id] || names[name]) return "template " + gostd::quote(t.name) + " appears twice";
			ids[t.id] = names[name] = true;
		}
	}
	if (!c.channels) return "";
	for (const auto &ch : *c.channels)
		if (!ch.templ.empty() && !ids[ch.templ]) return ch.name + ": the channel's template does not exist";
	std::map<std::string, bool> seen;
	for (auto &ch : *c.channels) {
		if (seen[ch.id]) return "channel " + gostd::quote(ch.id) + " appears twice";
		seen[ch.id] = true;
		if (auto e = validate_channel(ch); !e.empty()) return (ch.name.empty() ? ch.type : ch.name) + ": " + e;
	}
	return "";
}

Channel with_stored_secrets(Channel ch, const std::vector<Channel> &stored)
{
	const ChannelType *t = channel_type(ch.type);
	std::map<std::string, std::string> settings = ch.settings ? *ch.settings : std::map<std::string, std::string>{};
	for (const auto &old : stored) {
		if (old.id != ch.id || old.type != ch.type || ch.id.empty() || !t) continue;
		for (const auto &f : t->fields) {
			if (!f.secret || !gostd::trim_space(settings[f.key]).empty()) continue;
			std::string value;
			if (old.settings)
				if (auto it = old.settings->find(f.key); it != old.settings->end()) value = it->second;
			settings[f.key] = value;
		}
	}
	ch.settings = std::move(settings);
	return ch;
}

std::vector<std::string> split_list(std::string_view value)
{
	std::vector<std::string> out;
	std::string cur;
	for (std::size_t i = 0; i < value.size();) {
		auto [r, n] = gostd::decode_rune(value, i);
		if (r == ',' || r == ';' || r == ' ') {
			if (!cur.empty()) out.push_back(cur), cur.clear();
		} else {
			cur.append(value, i, n);
		}
		i += n;
	}
	if (!cur.empty()) out.push_back(cur);
	return out;
}

std::string new_id() { return gostd::random_hex(8); }

// ---- what the page sees ----

json::Out status(const Config &c, const std::map<std::string, std::string> &auth_errors)
{
	using json::Out;
	Out channels = Out::array();
	if (c.channels) {
		for (const auto &ch : *c.channels) {
			const ChannelType *t = channel_type(ch.type);
			std::map<std::string, std::string> settings = ch.settings ? *ch.settings : std::map<std::string, std::string>{};
			std::vector<std::string> secrets_set;
			if (t)
				for (const auto &f : t->fields) {
					if (!f.secret) continue;
					if (auto it = settings.find(f.key); it != settings.end()) {
						if (!it->second.empty()) secrets_set.push_back(f.key);
						settings.erase(it);
					}
				}
			Channel view = ch;
			view.settings = std::move(settings);
			if (!view.events) view.events.emplace();
			Out o = Out::object();
			encode_channel_fields(o, view);
			o.set("secretsSet", Out::strings(secrets_set));
			if (auto it = auth_errors.find(ch.id); it != auth_errors.end() && !it->second.empty())
				o.set("authError", Out::str(it->second));
			channels.add(std::move(o));
		}
	}
	Out templates = Out::array();
	if (c.templates)
		for (const auto &t : *c.templates) templates.add(encode_template(t));
	Out messages = Out::array();
	for (const auto &spec : message_specs()) {
		Message m = standard_message(c.language, spec.key);
		messages.add(Out::object()
		                 .set("key", Out::str(spec.key))
		                 .set("kind", Out::str(spec.kind))
		                 .set("vars", Out::strings(spec.vars))
		                 .set("title", Out::str(m.title))
		                 .set("body", Out::str(m.body)));
	}
	Out types = Out::array();
	for (const auto &t : channel_types()) {
		Out fields = Out::array();
		for (const auto &f : t.fields) {
			Out o = Out::object().set("key", Out::str(f.key)).set("kind", Out::str(f.kind));
			if (f.secret) o.set("secret", Out::boolean(true));
			if (f.required) o.set("required", Out::boolean(true));
			if (!f.options.empty()) o.set("options", Out::strings(f.options));
			if (!f.default_value.empty()) o.set("default", Out::str(f.default_value));
			if (!f.placeholder.empty()) o.set("placeholder", Out::str(f.placeholder));
			if (!f.show.empty()) {
				Out show = Out::object();
				for (const auto &[k, v] : f.show) show.set(k, Out::strings(v));
				o.set("show", std::move(show.sort_keys()));
			}
			if (f.max_bytes) o.set("maxBytes", Out::number(f.max_bytes));
			fields.add(std::move(o));
		}
		types.add(Out::object().set("type", Out::str(t.type)).set("fields", std::move(fields)).set("image", Out::boolean(t.image)));
	}
	Out provs = Out::array();
	for (const auto &p : providers()) {
		Out o = Out::object()
		            .set("id", Out::str(p.id))
		            .set("host", Out::str(p.host))
		            .set("port", Out::str(p.port))
		            .set("security", Out::str(p.security))
		            .set("auth", Out::strings(p.auth));
		if (!p.oauth.empty()) o.set("oauth", Out::str(p.oauth));
		if (!p.tenant.empty()) o.set("tenant", Out::str(p.tenant));
		provs.add(std::move(o));
	}
	return Out::object()
		.set("language", Out::str(c.language))
		.set("languages", Out::strings(languages()))
		.set("channels", std::move(channels))
		.set("templates", std::move(templates))
		.set("messages", std::move(messages))
		.set("commonVars", Out::strings(common_vars()))
		.set("types", std::move(types))
		.set("events", Out::strings(kinds()))
		.set("providers", std::move(provs));
}

// ---- words ----

std::string Rendered::text() const { return body + "\n\n" + device + " · " + gostd::format_datetime_zone(time); }

std::string device_name()
{
	char name[256] = {};
	if (::gethostname(name, sizeof name - 1) != 0) return "NanoKVM";
	std::string n = name;
	if (gostd::trim_space(n).empty()) return "NanoKVM";
	return n;
}

namespace {

std::string fill(std::string_view text, const std::map<std::string, std::string> &vars)
{
	std::string out;
	for (std::size_t i = 0; i < text.size();) {
		if (text[i] == '{') {
			std::size_t j = i + 1;
			while (j < text.size() && text[j] >= 'a' && text[j] <= 'z') j++;
			if (j > i + 1 && j < text.size() && text[j] == '}') {
				auto v = vars.find(std::string(text.substr(i + 1, j - i - 1)));
				if (v != vars.end()) out += v->second;
				else out.append(text.substr(i, j + 1 - i));
				i = j + 1;
				continue;
			}
		}
		out += text[i++];
	}
	return out;
}

std::string escape_slack(std::string_view s) { return gostd::replace(s, {{"&", "&amp;"}, {"<", "&lt;"}, {">", "&gt;"}}); }
std::string escape_discord(std::string_view s)
{
	return gostd::replace(s, {{"\\", "\\\\"}, {"*", "\\*"}, {"_", "\\_"}, {"~", "\\~"}, {"`", "\\`"}, {"|", "\\|"},
	                          {">", "\\>"}, {"<", "\\<"}, {"[", "\\["}, {"]", "\\]"}, {"(", "\\("}, {")", "\\)"},
	                          {"#", "\\#"}, {"@", "\\@"}});
}
std::string escape_google_chat(std::string_view s) { return gostd::replace(s, {{"<", "<​"}, {"](", "]​("}}); }
std::string escape_teams(std::string_view s) { return gostd::replace(s, {{"](", "]​("}}); }

} // namespace

Escape markup(std::string_view type)
{
	if (type == "teams") return escape_teams;
	if (type == "slack") return escape_slack;
	if (type == "discord") return escape_discord;
	if (type == "googlechat") return escape_google_chat;
	return nullptr;
}

Rendered render(const Event &e, const std::string &language, const Template *templ, Escape escape)
{
	Message m = standard_message(language, e.message);
	if (templ && templ->messages) {
		if (auto own = templ->messages->find(e.message); own != templ->messages->end()) {
			if (!gostd::trim_space(own->second.title).empty()) m.title = own->second.title;
			if (!gostd::trim_space(own->second.body).empty()) m.body = own->second.body;
		}
	}
	std::string device = device_name();
	std::map<std::string, std::string> vars = {
		{"device", device}, {"time", gostd::format_datetime_zone(e.time)}, {"details", e.details}};
	for (const auto &[name, value] : e.vars) vars[name] = value;
	if (escape)
		for (auto &[name, value] : vars) value = escape(value);
	std::string body = fill(m.body, vars);
	if (!e.details.empty() && m.body.find("{details}") == std::string::npos) {
		if (!body.empty()) body += "\n\n";
		body += vars["details"];
	}
	std::string title;
	for (const auto &w : gostd::fields(fill(m.title, vars))) title += (title.empty() ? "" : " ") + w;
	return Rendered{e.kind, e.severity, title, body, device, e.time, e.image};
}

json::Out preview(PreviewRequest request, Clock::time_point now, std::string &error)
{
	error.clear();
	if (!contains(languages(), request.language)) {
		error = "unknown language";
		return json::Out::null();
	}
	if (request.templ) {
		if (request.templ->id.empty()) request.templ->id = "preview";
		if (auto e = validate_template(*request.templ); !e.empty()) {
			error = e;
			return json::Out::null();
		}
	}
	const std::map<std::string, Event> samples = {
		{"security.lockout", {.vars = {{"failures", "5"}, {"address", "203.0.113.7"}, {"minutes", "15"}}}},
		{"security.attempt-limit", {.vars = {{"attempts", "20"}, {"address", "203.0.113.7"}, {"minutes", "1"}}}},
		{"security.new-address", {.vars = {{"user", "admin"}, {"address", "203.0.113.7"}}}},
		{"screen.bsod", {.details = "Your PC ran into a problem and needs to restart.\nStop code: CRITICAL_PROCESS_DIED"}},
		{"screen.panic", {.details = "Kernel panic - not syncing: VFS: Unable to mount root fs on unknown-block(0,0)\n"
		                             "Call Trace:\n dump_stack+0x10"}},
	};
	json::Out out = json::Out::array();
	for (const auto &spec : message_specs()) {
		Event e;
		if (auto s = samples.find(spec.key); s != samples.end()) e = s->second;
		e.kind = spec.kind;
		e.message = spec.key;
		e.time = now;
		Rendered r = render(e, request.language, request.templ ? &*request.templ : nullptr);
		out.add(json::Out::object()
		            .set("key", json::Out::str(spec.key))
		            .set("title", json::Out::str(r.title))
		            .set("body", json::Out::str(r.body))
		            .set("text", json::Out::str(r.text())));
	}
	return out;
}

} // namespace nkos::notify
