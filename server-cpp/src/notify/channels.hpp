#pragma once

// What each channel type sends, as the Go server's service/notify/channels.go, email.go and syslog.go: the HTTP
// requests of the chat and push services and webhooks (built here, sent by the poster given), the e-mail message,
// the syslog line, and the error a failed send reports (the channel's secrets taken out of it).

#include "notify/gostd.hpp"
#include "notify/model.hpp"

#include <functional>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace nkos::notify {

// A failed send. permanent: a retry cannot help (a wrong token, a refused address); auth: the channel's sign-in
// failed and the page shows it.
struct Error {
	std::string text;
	bool permanent = false;
	bool auth = false;
};
using Result = std::optional<Error>;  // nullopt: sent

inline Error permanent(std::string text) { return {std::move(text), true, false}; }

inline constexpr std::size_t max_answer = 4096;

struct HttpRequest {
	std::string method, url;
	std::vector<std::pair<std::string, std::string>> headers;  // names as Go writes them (canonical)
	std::string body;
	bool skip_verify = false;
	std::size_t max_body = max_answer + 1;  // bytes of the answer kept; the rest is not read
};

// The answer: status 0 and error set when no answer came (the transport's error text). body: at most the first
// max_body bytes.
struct HttpAnswer {
	int status = 0;
	std::string body;
	std::string error;
};

using Poster = std::function<HttpAnswer(const HttpRequest &)>;

// What the attempts of one delivery share: its id (Matrix's transaction id) and the Telegram destination that got
// the picture, so a retry does not repeat what an earlier attempt got through.
struct Delivery {
	std::string id;
	std::string photo_to;
};

// Sends to a channel of an HTTP type (all but email and syslog) through post; delivery is null for a test.
Result send_http(const Channel &ch, const Rendered &r, Delivery *delivery, const Poster &post);
bool is_http_type(std::string_view type);

// truncate: at most n characters, the last one "…" when cut.
std::string truncate(std::string_view text, std::size_t n);

// The e-mail of an event (headers, quoted-printable text, the picture attached). boundary and message_id: empty
// for random ones.
std::string email_message(const gostd::mail::Address &from, const std::vector<std::string> &to, const Rendered &r,
                          const std::string &boundary = {}, const std::string &message_id = {});
// The EHLO name.
std::string hello_name();

// The RFC 5424 line of an event; over TCP and TLS with its length in front (RFC 6587).
std::string syslog_message(const Rendered &r, const std::string &protocol);

// The channel's secret values as an answer may quote them, longest first.
std::vector<std::string> secret_forms(const Channel &ch);
// The error with the channel's secrets replaced by "***", at most 400 characters.
Error redact(const Channel &ch, Error e);

} // namespace nkos::notify
