#include "system/notify.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/time.hpp"
#include "notify/model.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <format>
#include <string>
#include <vector>

namespace nkos::system::notify {

namespace {

struct JournalEntryJson {
	std::uint64_t seq;
	std::string time, kind, severity, title, body;
};

} // namespace

std::pair<std::string, std::string> describe(const std::string &language, const journal::Entry &e)
{
	const auto &languages = nkos::notify::languages();
	std::string lang = std::find(languages.begin(), languages.end(), language) == languages.end() ? languages[0] : language;
	nkos::notify::Event event;
	event.message = e.message;
	event.vars = e.vars;
	event.details = e.details;
	event.time = e.time;
	auto r = nkos::notify::render(event, lang, nullptr);
	return {r.title, r.body};
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
