#include "system/notify.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/time.hpp"
#include "notify_messages.inc"

#include <glaze/glaze.hpp>

#include <ctime>
#include <format>
#include <map>
#include <mutex>
#include <string>
#include <unistd.h>
#include <vector>

namespace nkos::system::notify {

namespace {

struct Message {
	std::string title, body;
};

using Catalogue = std::map<std::string, Message>;

// The standard messages of every language (the web interface's), parsed once.
const std::map<std::string, Catalogue> &messages()
{
	static const std::map<std::string, Catalogue> all = [] {
		std::map<std::string, Catalogue> out;
		for (const auto &[language, json] : catalogue::files) {
			Catalogue c;
			if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(c, json))
				log::error("notification messages {}: {}", language, glz::format_error(e, json));
			out[std::string(language)] = std::move(c);
		}
		return out;
	}();
	return all;
}

const std::vector<std::string> languages = {"en", "ca", "cz", "da", "de", "es", "fr", "he", "hu", "id", "it", "ja", "ko",
                                            "nb", "nl", "pl", "pt_br", "ru", "se", "th", "tr", "uk", "vi", "zh", "zh_tw"};

// The message of a language, falling back to English, then to the key as its title.
Message standard_message(const std::string &language, const std::string &key)
{
	const auto &all = messages();
	for (const std::string &lang : {language, languages[0]}) {
		auto c = all.find(lang);
		if (c == all.end()) continue;
		if (auto m = c->second.find(key); m != c->second.end()) return m->second;
	}
	return {key, ""};
}

// Replaces each {name} of vars (names of a-z); other braces stay as written.
std::string fill(const std::string &text, const std::map<std::string, std::string> &vars)
{
	std::string out;
	for (std::size_t i = 0; i < text.size();) {
		if (text[i] == '{') {
			std::size_t j = i + 1;
			while (j < text.size() && text[j] >= 'a' && text[j] <= 'z') j++;
			if (j > i + 1 && j < text.size() && text[j] == '}') {
				if (auto v = vars.find(text.substr(i + 1, j - i - 1)); v != vars.end()) {
					out += v->second;
					i = j + 1;
					continue;
				}
				out += text.substr(i, j + 1 - i);
				i = j + 1;
				continue;
			}
		}
		out += text[i++];
	}
	return out;
}

// One rune: its code point and length (invalid bytes as U+FFFD, one byte).
std::pair<char32_t, std::size_t> rune_at(const std::string &s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	auto cont = [&](std::size_t k) { return i + k < s.size() && (static_cast<unsigned char>(s[i + k]) & 0xC0) == 0x80; };
	if (c < 0x80) return {c, 1};
	if ((c >> 5) == 6 && cont(1)) return {static_cast<char32_t>((c & 0x1F) << 6 | (s[i + 1] & 0x3F)), 2};
	if ((c >> 4) == 14 && cont(1) && cont(2))
		return {static_cast<char32_t>((c & 0x0F) << 12 | (s[i + 1] & 0x3F) << 6 | (s[i + 2] & 0x3F)), 3};
	if ((c >> 3) == 30 && cont(1) && cont(2) && cont(3))
		return {static_cast<char32_t>((c & 0x07) << 18 | (s[i + 1] & 0x3F) << 12 | (s[i + 2] & 0x3F) << 6 | (s[i + 3] & 0x3F)), 4};
	return {0xFFFD, 1};
}

// unicode.IsSpace.
bool space(char32_t r)
{
	switch (r) {
	case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xA0: case 0x1680: case 0x2028:
	case 0x2029: case 0x202F: case 0x205F: case 0x3000:
		return true;
	}
	return r >= 0x2000 && r <= 0x200A;
}

// strings.Join(strings.Fields(s), " "): a title stays one line whatever a variable holds.
std::string one_line(const std::string &s)
{
	std::string out;
	bool pending = false;
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = rune_at(s, i);
		if (space(r)) {
			pending = !out.empty();
		} else {
			if (pending) out += ' ';
			pending = false;
			out.append(s, i, n);
		}
		i += n;
	}
	return out;
}

std::string device_name()
{
	char name[256] = {};
	if (::gethostname(name, sizeof name - 1) != 0) return "NanoKVM";
	std::string n = name;
	if (n.find_first_not_of(" \t\r\n") == std::string::npos) return "NanoKVM";
	return n;
}

// Go's "2006-01-02 15:04:05 MST" in the local zone.
std::string event_time(std::chrono::system_clock::time_point t)
{
	std::time_t secs = std::chrono::system_clock::to_time_t(t);
	std::tm tm{};
	localtime_r(&secs, &tm);
	char buf[64];
	std::strftime(buf, sizeof buf, "%Y-%m-%d %H:%M:%S %Z", &tm);
	return buf;
}

struct JournalEntryJson {
	std::uint64_t seq;
	std::string time, kind, severity, title, body;
};

} // namespace

std::pair<std::string, std::string> describe(const std::string &language, const journal::Entry &e)
{
	std::string lang = language;
	if (std::find(languages.begin(), languages.end(), lang) == languages.end()) lang = languages[0];
	Message m = standard_message(lang, e.message);
	std::map<std::string, std::string> vars = {{"device", device_name()}, {"time", event_time(e.time)}, {"details", e.details}};
	for (const auto &[name, value] : e.vars) vars[name] = value;
	std::string body = fill(m.body, vars);
	if (!e.details.empty() && m.body.find("{details}") == std::string::npos) {
		if (!body.empty()) body += "\n\n";
		body += vars["details"];
	}
	return {one_line(fill(m.title, vars)), body};
}

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/journal", auth::guarded([](http::Request &req, http::Response &res) {
		int limit = 0;
		std::string text = req.query_value("limit");
		if (!text.empty() && text.find_first_not_of("0123456789") == std::string::npos && text.size() < 10) limit = std::stoi(text);
		std::string lang = req.query_value("lang");
		std::vector<JournalEntryJson> entries;
		for (const auto &e : journal::entries(limit)) {
			auto [title, body] = describe(lang, e);
			entries.push_back({e.seq, rfc3339nano(e.time), e.kind, e.severity, title, body});
		}
		std::string list;
		(void)glz::write_json(entries, list);
		res.set_header("Cache-Control", "no-store");
		api::send(res, 0, "success", std::format(R"({{"entries":{},"kept":{}}})", list, journal::max_entries));
	}, auth::Guard{{"admin"}}));
}

} // namespace nkos::system::notify
