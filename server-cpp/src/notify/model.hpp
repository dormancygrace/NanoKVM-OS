#pragma once

// The notification settings and messages, as the Go server's service/notify: channels (a type, the events they
// take, the settings of their type, a template), templates (the admin's own words for some messages), the standard
// messages of every language of the web interface, the file /etc/kvm/notify/config.json (root only: it holds
// tokens and passwords), the checks of a save from the page, what the page is shown (secrets never leave the
// device), and an event put into words for a channel. Pure: no files but load/save, no network, any thread.

#include "notify/json.hpp"

#include <chrono>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::notify {

using Clock = std::chrono::system_clock;

// What an event is about; each channel picks the kinds it gets. "test" is the page's Test button.
inline constexpr std::string_view kind_power = "power", kind_hdmi = "hdmi", kind_security = "security",
                                  kind_screen = "screen", kind_test = "test";
// The kinds a channel can choose, in the page's order.
const std::vector<std::string> &kinds();
// The languages of the standard messages; the first is the default.
const std::vector<std::string> &languages();
// The variables of every message.
const std::vector<std::string> &common_vars();

enum class Severity { info, warning, critical };
std::string severity_name(Severity s);

// One notification: message is a key of the message specs, written by the channel's template or the standard
// message of the configured language with vars filled in. details go as they are (text read off the screen);
// image is a JPEG for the channels that carry one (empty: none).
struct Event {
	std::string kind;
	Severity severity = Severity::info;
	std::string message;
	std::map<std::string, std::string> vars;
	std::string details;
	std::string image;
	Clock::time_point time{};
};

struct Message {
	std::string title, body;
};

struct MessageSpec {
	std::string key, kind;
	std::vector<std::string> vars;
};
const std::vector<MessageSpec> &message_specs();
const MessageSpec *message_spec(std::string_view key);
// The message of the standard template in a language, else English, else the key as its title.
Message standard_message(const std::string &language, const std::string &key);

struct Template {
	std::string id, name;
	std::optional<std::map<std::string, Message>> messages;
};

struct Channel {
	std::string id, type, name;
	bool enabled = false;
	std::optional<std::vector<std::string>> events;
	std::optional<std::map<std::string, std::string>> settings;
	std::string templ;  // the template's id; "" the standard one

	// A setting, trimmed; "" when absent.
	std::string get(const std::string &key) const;
	bool flag(const std::string &key) const { return get(key) == "true"; }
	bool wants(std::string_view kind) const;
};

struct Config {
	std::string language = "en";
	std::optional<std::vector<Channel>> channels;
	std::optional<std::vector<Template>> templates;
	const Template *find_template(const std::string &id) const;
};

// A channel from the page: an empty secret field keeps the stored value, clear names the secret fields to empty,
// template_draft is the page's template for the Test button.
struct ChannelUpdate {
	Channel channel;
	std::optional<std::vector<std::string>> clear;
	std::optional<Template> template_draft;
};

struct Settings {
	std::string language;
	std::optional<std::vector<ChannelUpdate>> channels;
	std::optional<std::vector<Template>> templates;
};

struct PreviewRequest {
	std::string language;
	std::optional<Template> templ;
};

// One setting of a channel type, as the page draws it.
struct Field {
	std::string key, kind;
	bool secret = false, required = false;
	std::vector<std::string> options;
	std::string default_value, placeholder;
	std::vector<std::pair<std::string, std::vector<std::string>>> show;  // sorted by key
	int max_bytes = 0;
	bool shown(const std::map<std::string, std::string> &settings) const;
};

struct ChannelType {
	std::string type;
	std::vector<Field> fields;
	bool image = false;
};
const std::vector<ChannelType> &channel_types();
const ChannelType *channel_type(std::string_view name);

inline constexpr std::string_view oauth_google = "google", oauth_microsoft = "microsoft";

struct Provider {
	std::string id, host, port, security;
	std::vector<std::string> auth;
	std::string oauth, tenant;
};
const std::vector<Provider> &providers();
const Provider &provider_by_id(std::string_view id);

inline constexpr std::size_t max_channels = 16;
inline constexpr const char *config_path = "/etc/kvm/notify/config.json";

// ---- JSON ----

bool decode_config(const json::Value &v, Config &out);
json::Out encode_config(const Config &c);
bool decode_template(const json::Value &v, Template &out);
json::Out encode_template(const Template &t);
bool decode_channel_update(const json::Value &v, ChannelUpdate &out);
// The bodies of the page's requests, as gin's ShouldBindJSON reads them; false where it fails.
bool bind_settings(std::string_view body, Settings &out);
bool bind_channel_update(std::string_view body, ChannelUpdate &out);
bool bind_preview(std::string_view body, PreviewRequest &out);

// Load: the stored settings (defaults when there is no file); error describes an unreadable file (the defaults
// are used then).
Config load(const std::string &path, std::string &error);
// The bytes Save writes (json.MarshalIndent), or an error when they exceed the backup limit.
std::string config_bytes(const Config &c, std::string &error);

// ---- checks ----

// Config.Validate: "" when valid; fills in each field's default and drops the fields that do not apply.
std::string validate(Config &c);
std::string validate_channel(Channel &ch);
std::string validate_template(Template &t);
// withStoredSecrets: the secret fields left empty take the stored channel's values.
Channel with_stored_secrets(Channel ch, const std::vector<Channel> &stored);
// The addresses of a list separated by commas, semicolons or spaces.
std::vector<std::string> split_list(std::string_view value);
// A new channel id (16 hex digits).
std::string new_id();

// ---- what the page sees ----

// GET /api/notify/config: the settings with the secrets left out, the messages of the configured language, the
// channel types and providers. auth_errors: the failed sign-ins of channel ids.
json::Out status(const Config &c, const std::map<std::string, std::string> &auth_errors);

// ---- words ----

struct Rendered {
	std::string kind;
	Severity severity = Severity::info;
	std::string title, body, device;
	Clock::time_point time{};
	std::string image;
	// The body with the device and the time under it.
	std::string text() const;
};

// The host name the messages are signed with.
std::string device_name();
// An event in words for a channel type whose text is markup (escape: nullptr for plain text): the template's text
// where it has one, the standard message of language elsewhere.
using Escape = std::string (*)(std::string_view);
Rendered render(const Event &e, const std::string &language, const Template *templ, Escape escape = nullptr);
// The escaping of a channel type's markup (nullptr: plain text).
Escape markup(std::string_view channel_type);

// POST /api/notify/preview: every message with sample values; error set when the request is refused.
json::Out preview(PreviewRequest request, Clock::time_point now, std::string &error);

} // namespace nkos::notify
