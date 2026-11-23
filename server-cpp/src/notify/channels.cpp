#include "notify/channels.hpp"

#include "common/base64.hpp"

#include <openssl/evp.h>
#include <openssl/hmac.h>

#include <algorithm>
#include <charconv>
#include <set>

namespace nkos::notify {

namespace {

constexpr char user_agent[] = "NanoKVM";

std::string trim_right(std::string s, char c)
{
	while (!s.empty() && s.back() == c) s.pop_back();
	return s;
}

std::string method_op(std::string_view method)
{
	// url.Error's Op: "Post", "Put".
	std::string op;
	for (std::size_t i = 0; i < method.size(); i++)
		op += static_cast<char>(i == 0 ? method[i] : (method[i] >= 'A' && method[i] <= 'Z' ? method[i] + 32 : method[i]));
	return op;
}

// do: the answer as Go's notifications read it.
Result answer_result(const HttpRequest &req, const HttpAnswer &a)
{
	if (!a.error.empty()) return Error{method_op(req.method) + ": " + a.error};
	if (a.status >= 200 && a.status < 300) return std::nullopt;
	// A cut could split a secret that redact then misses; an answer this long is an error page anyway.
	std::string body = a.body.size() > max_answer ? "" : a.body;
	Error e{"HTTP " + std::to_string(a.status) + ": " + gostd::trim_space(body)};
	e.permanent = a.status >= 400 && a.status < 500 && a.status != 429 && a.status != 408;
	return e;
}

// request + do: a request to target with the content type, the User-Agent and the headers given.
Result send_request(const Channel &ch, const Poster &post, std::string method, const std::string &target,
                    std::string content_type, std::string body, std::vector<std::pair<std::string, std::string>> header = {})
{
	if (!gostd::url::parse(target)) return permanent("invalid address");
	HttpRequest req{std::move(method), target, {{"Content-Type", std::move(content_type)}, {"User-Agent", user_agent}},
	                std::move(body), ch.flag("skipVerify")};
	for (auto &h : header) req.headers.push_back(std::move(h));
	return answer_result(req, post(req));
}

Result post_json(const Channel &ch, const Poster &post, const std::string &target, const json::Out &payload,
                 std::vector<std::pair<std::string, std::string>> header = {})
{
	return send_request(ch, post, "POST", target, "application/json", json::marshal(payload), std::move(header));
}

struct Form {
	std::vector<std::pair<std::string, std::string>> fields;
	std::string file_field, file_name, file;
	bool has_file = false;

	Result post(const Channel &ch, const Poster &poster, const std::string &target) const
	{
		gostd::Multipart w;
		for (const auto &[k, v] : fields) w.field(k, v);
		if (has_file) w.file(file_field, file_name, file);
		std::string type = w.form_data_content_type();
		return send_request(ch, poster, "POST", target, type, w.close());
	}
};

using json::Out;

Result send_telegram(const Channel &ch, const Rendered &r, Delivery *d, const Poster &post)
{
	std::string api = trim_right(ch.get("apiUrl"), '/');
	if (api.empty()) api = "https://api.telegram.org";
	std::string base = api + "/bot" + ch.get("token") + "/";
	std::string text = r.title + "\n" + r.text();
	std::string thread = ch.get("threadId");
	std::string destination = base + std::string(1, '\0') + ch.get("chatId") + std::string(1, '\0') + thread;
	if (!r.image.empty() && (!d || d->photo_to != destination)) {
		// A caption holds 1024 characters: a longer text follows the picture.
		std::string caption = gostd::rune_count(text) > 1024 ? r.title : text;
		Form f{{{"chat_id", ch.get("chatId")}, {"caption", caption}}, "photo", "screen.jpg", r.image, true};
		if (!thread.empty()) f.fields.emplace_back("message_thread_id", thread);
		Result err = f.post(ch, post, base + "sendPhoto");
		if (err || caption == text) return err;
		if (d) d->photo_to = destination;
	}
	Out payload = Out::object()
	                  .set("chat_id", Out::str(ch.get("chatId")))
	                  .set("text", Out::str(truncate(text, 4096)))
	                  .set("disable_web_page_preview", Out::boolean(true));
	if (!thread.empty()) {
		// strconv.ParseInt: an optional sign, then digits; 0 when it fails.
		std::int64_t id = 0;
		std::string_view digits = thread;
		if (!digits.empty() && digits[0] == '+') digits.remove_prefix(1);
		if (std::from_chars(digits.data(), digits.data() + digits.size(), id).ptr != digits.data() + digits.size()) id = 0;
		payload.set("message_thread_id", Out::number(id));
	}
	return post_json(ch, post, base + "sendMessage", payload.sort_keys());
}

std::string hmac_sha256_hex(const std::string &key, const std::string &data)
{
	unsigned char mac[EVP_MAX_MD_SIZE];
	unsigned int len = 0;
	HMAC(EVP_sha256(), key.data(), static_cast<int>(key.size()), reinterpret_cast<const unsigned char *>(data.data()),
	     data.size(), mac, &len);
	static const char hex[] = "0123456789abcdef";
	std::string out;
	for (unsigned int i = 0; i < len; i++) {
		out += hex[mac[i] >> 4];
		out += hex[mac[i] & 15];
	}
	return out;
}

Result send_webhook(const Channel &ch, const Rendered &r, const Poster &post)
{
	Out payload = Out::object()
	                  .set("event", Out::str(r.kind))
	                  .set("severity", Out::str(severity_name(r.severity)))
	                  .set("title", Out::str(r.title))
	                  .set("text", Out::str(r.body))
	                  .set("device", Out::str(r.device))
	                  .set("time", Out::str(gostd::format_rfc3339(r.time, true)));
	if (!r.image.empty()) payload.set("image", Out::str(base64::encode(r.image)));
	std::string body = json::marshal(payload);
	std::vector<std::pair<std::string, std::string>> header = {{"X-Nanokvm-Event", r.kind}};
	if (auto secret = ch.get("hmacSecret"); !secret.empty())
		header.emplace_back("X-Nanokvm-Signature", "sha256=" + hmac_sha256_hex(secret, body));
	if (auto auth = ch.get("authorization"); !auth.empty()) header.emplace_back("Authorization", auth);
	return send_request(ch, post, "POST", ch.get("url"), "application/json", body, std::move(header));
}

Result send_teams(const Channel &ch, const Rendered &r, const Poster &post)
{
	Out title = Out::object()
	                .set("type", Out::str("TextBlock"))
	                .set("text", Out::str(r.title))
	                .set("weight", Out::str("Bolder"))
	                .set("size", Out::str("Medium"))
	                .set("wrap", Out::boolean(true));
	if (r.severity != Severity::info) title.set("color", Out::str("Attention"));
	Out body = Out::array()
	               .add(std::move(title.sort_keys()))
	               .add(std::move(Out::object().set("type", Out::str("TextBlock")).set("text", Out::str(r.body)).set("wrap", Out::boolean(true)).sort_keys()))
	               .add(std::move(Out::object()
	                                  .set("type", Out::str("TextBlock"))
	                                  .set("text", Out::str(r.device + " · " + gostd::format_datetime_zone(r.time)))
	                                  .set("isSubtle", Out::boolean(true))
	                                  .set("size", Out::str("Small"))
	                                  .set("wrap", Out::boolean(true))
	                                  .sort_keys()));
	Out card = Out::object()
	               .set("$schema", Out::str("http://adaptivecards.io/schemas/adaptive-card.json"))
	               .set("type", Out::str("AdaptiveCard"))
	               .set("version", Out::str("1.4"))
	               .set("body", std::move(body));
	Out attachment = Out::object().set("contentType", Out::str("application/vnd.microsoft.card.adaptive")).set("content", std::move(card.sort_keys()));
	Out payload = Out::object().set("type", Out::str("message")).set("attachments", Out::array().add(std::move(attachment.sort_keys())));
	return post_json(ch, post, ch.get("url"), payload.sort_keys());
}

Result send_discord(const Channel &ch, const Rendered &r, const Poster &post)
{
	Out message = Out::object()
	                  .set("content", Out::str(truncate("**" + r.title + "**\n" + r.text(), 2000)))
	                  .set("allowed_mentions", Out::object().set("parse", Out::strings(std::vector<std::string>{"users", "roles"})));
	message.sort_keys();
	if (r.image.empty()) return post_json(ch, post, ch.get("url"), message);
	Form f{{{"payload_json", json::marshal(message)}}, "files[0]", "screen.jpg", r.image, true};
	return f.post(ch, post, ch.get("url"));
}

Result send_ntfy(const Channel &ch, const Rendered &r, const Poster &post)
{
	int priority = r.severity == Severity::critical ? 5 : r.severity == Severity::warning ? 4 : 3;
	Out payload = Out::object()
	                  .set("topic", Out::str(ch.get("topic")))
	                  .set("title", Out::str(r.title))
	                  .set("message", Out::str(r.text()))
	                  .set("priority", Out::number(priority))
	                  .set("tags", Out::strings(std::vector<std::string>{"nanokvm", r.kind}));
	std::vector<std::pair<std::string, std::string>> header;
	if (auto token = ch.get("token"); !token.empty()) header.emplace_back("Authorization", "Bearer " + token);
	return post_json(ch, post, trim_right(ch.get("server"), '/') + "/", payload.sort_keys(), std::move(header));
}

Result send_gotify(const Channel &ch, const Rendered &r, const Poster &post)
{
	int priority = r.severity == Severity::critical ? 8 : r.severity == Severity::warning ? 5 : 2;
	Out payload = Out::object().set("title", Out::str(r.title)).set("message", Out::str(r.text())).set("priority", Out::number(priority));
	return post_json(ch, post, trim_right(ch.get("server"), '/') + "/message", payload.sort_keys(),
	                 {{"X-Gotify-Key", ch.get("token")}});
}

Result send_pushover(const Channel &ch, const Rendered &r, const Poster &post)
{
	Form f{{{"token", ch.get("appToken")},
	        {"user", ch.get("userKey")},
	        {"title", truncate(r.title, 250)},
	        {"message", truncate(r.text(), 1024)},
	        {"priority", r.severity == Severity::critical ? "1" : "0"}}};
	if (auto device = ch.get("device"); !device.empty()) f.fields.emplace_back("device", device);
	if (!r.image.empty()) {
		f.file_field = "attachment";
		f.file_name = "screen.jpg";
		f.file = r.image;
		f.has_file = true;
	}
	return f.post(ch, post, "https://api.pushover.net/1/messages.json");
}

Result send_matrix(const Channel &ch, const Rendered &r, Delivery *d, const Poster &post)
{
	// The transaction id makes a retry of a request whose answer was lost a no-op.
	std::string txn = d ? d->id : new_id();
	std::string target = trim_right(ch.get("homeserver"), '/') + "/_matrix/client/v3/rooms/" +
	                     gostd::url::path_escape(ch.get("roomId")) + "/send/m.room.message/nanokvm-" + txn;
	Out body = Out::object().set("msgtype", Out::str("m.text")).set("body", Out::str(r.title + "\n" + r.text()));
	return send_request(ch, post, "PUT", target, "application/json", json::marshal(body.sort_keys()),
	                    {{"Authorization", "Bearer " + ch.get("accessToken")}});
}

} // namespace

bool is_http_type(std::string_view t)
{
	return t == "telegram" || t == "webhook" || t == "slack" || t == "teams" || t == "googlechat" || t == "discord" ||
	       t == "ntfy" || t == "gotify" || t == "pushover" || t == "matrix";
}

Result send_http(const Channel &ch, const Rendered &r, Delivery *d, const Poster &post)
{
	const std::string &t = ch.type;
	if (t == "telegram") return send_telegram(ch, r, d, post);
	if (t == "webhook") return send_webhook(ch, r, post);
	if (t == "slack")
		return post_json(ch, post, ch.get("url"), Out::object().set("text", Out::str(r.title + "\n" + r.text())));
	if (t == "teams") return send_teams(ch, r, post);
	if (t == "googlechat")
		return post_json(ch, post, ch.get("url"), Out::object().set("text", Out::str("*" + r.title + "*\n" + r.text())));
	if (t == "discord") return send_discord(ch, r, post);
	if (t == "ntfy") return send_ntfy(ch, r, post);
	if (t == "gotify") return send_gotify(ch, r, post);
	if (t == "pushover") return send_pushover(ch, r, post);
	if (t == "matrix") return send_matrix(ch, r, d, post);
	return permanent("unknown channel type " + gostd::quote(t));
}

std::string truncate(std::string_view text, std::size_t n)
{
	if (gostd::rune_count(text) <= n) return std::string(text);
	std::string out;
	std::size_t count = 0;
	for (std::size_t i = 0; i < text.size() && count < n - 1; count++) {
		auto [r, len] = gostd::decode_rune(text, i);
		gostd::append_rune(out, r);
		i += len;
	}
	return out + "…";
}

std::string hello_name()
{
	std::string name = device_name();
	return name.find_first_of(" \t") != std::string::npos ? "nanokvm" : name;
}

std::string email_message(const gostd::mail::Address &from, const std::vector<std::string> &to, const Rendered &r,
                          const std::string &boundary, const std::string &message_id)
{
	std::string buf;
	auto header = [&](std::string_view key, std::string_view value) {
		buf.append(key);
		buf += ": ";
		buf.append(value);
		buf += "\r\n";
	};
	std::string domain = "nanokvm";
	if (auto at = from.address.rfind('@'); at != std::string::npos) domain = from.address.substr(at + 1);
	std::string list;
	for (std::size_t i = 0; i < to.size(); i++) list += (i ? ", " : "") + to[i];
	header("From", from.string());
	header("To", list);
	header("Subject", gostd::mime::q_encode("utf-8", "[" + r.device + "] " + r.title));
	header("Date", gostd::format_rfc1123z(r.time));
	auto secs = std::chrono::floor<std::chrono::seconds>(r.time.time_since_epoch()).count();
	header("Message-ID", "<" + (message_id.empty() ? new_id() : message_id) + "." + std::to_string(secs) + "@" + domain + ">");
	// Out-of-office replies are not sent to it (RFC 3834).
	header("Auto-Submitted", "auto-generated");
	header("MIME-Version", "1.0");
	std::string text;
	{
		std::string t = r.text(), crlf;
		for (char c : t) {
			if (c == '\n') crlf += "\r\n";
			else crlf += c;
		}
		text = gostd::mime::quoted_printable(crlf);
	}
	if (r.image.empty()) {
		header("Content-Type", "text/plain; charset=utf-8");
		header("Content-Transfer-Encoding", "quoted-printable");
		buf += "\r\n";
		buf += text;
		return buf;
	}
	gostd::Multipart w(boundary);
	header("Content-Type", "multipart/mixed; boundary=" + w.boundary());
	buf += "\r\n";
	w.part({{"Content-Type", "text/plain; charset=utf-8"}, {"Content-Transfer-Encoding", "quoted-printable"}}, text);
	std::string encoded = base64::encode(r.image), lines;
	std::size_t i = 0;
	for (; encoded.size() - i > 76; i += 76) lines += encoded.substr(i, 76) + "\r\n";
	lines += encoded.substr(i) + "\r\n";
	w.part({{"Content-Type", "image/jpeg"},
	        {"Content-Transfer-Encoding", "base64"},
	        {"Content-Disposition", "attachment; filename=\"screen.jpg\""}},
	       lines);
	return buf + w.close();
}

std::string syslog_message(const Rendered &r, const std::string &protocol)
{
	constexpr int facility = 3;  // daemon
	int severity = r.severity == Severity::critical ? 2 : r.severity == Severity::warning ? 4 : 6;
	std::string joined = r.title + ": " + r.body, flat;
	for (char c : joined) {
		if (c == '\n') flat += " | ";
		else flat += c;
	}
	std::string text;
	for (const auto &w : gostd::fields(flat)) text += (text.empty() ? "" : " ") + w;
	// HOSTNAME of RFC 5424: printable ASCII without spaces.
	std::string host;
	for (std::size_t i = 0; i < r.device.size();) {
		auto [c, n] = gostd::decode_rune(r.device, i);
		if (c > 32 && c < 127) host += static_cast<char>(c);
		i += n;
	}
	if (host.empty()) host = "-";
	std::string msg = "<" + std::to_string(facility * 8 + severity) + ">1 " + gostd::format_rfc3339(r.time, false) + " " +
	                  host + " nanokvm - " + r.kind + " - " + text;
	if (protocol != "udp") msg = std::to_string(msg.size()) + " " + msg;
	return msg;
}

std::vector<std::string> secret_forms(const Channel &ch)
{
	std::vector<std::string> forms;
	std::set<std::string> seen;
	auto add = [&](const std::string &s) {
		if (!s.empty() && s != "/" && seen.insert(s).second) forms.push_back(s);
	};
	if (const ChannelType *t = channel_type(ch.type)) {
		for (const auto &f : t->fields) {
			std::string value = ch.get(f.key);
			if (!f.secret || value.empty()) continue;
			add(value);
			add(gostd::url::path_escape(value));
			add(gostd::url::query_escape(value));
			if (f.kind != "url") continue;
			auto u = gostd::url::parse(value);
			if (!u) continue;
			add(u->request_uri());
			add(u->escaped_path());
			add(u->path);
			add(u->raw_query);
			for (std::size_t start = 0;;) {
				auto slash = u->path.find('/', start);
				std::string part = u->path.substr(start, slash == std::string::npos ? std::string::npos : slash - start);
				if (part.size() >= 8) add(part);
				if (slash == std::string::npos) break;
				start = slash + 1;
			}
			for (const auto &[key, values] : gostd::url::parse_query(u->raw_query))
				for (const auto &v : values)
					if (v.size() >= 8) {
						add(v);
						add(gostd::url::query_escape(v));
					}
		}
	}
	std::stable_sort(forms.begin(), forms.end(), [](const auto &a, const auto &b) { return a.size() > b.size(); });
	return forms;
}

Error redact(const Channel &ch, Error e)
{
	std::string text = e.text;
	for (const auto &secret : secret_forms(ch)) {
		std::string out;
		for (std::size_t i = 0;;) {
			auto at = text.find(secret, i);
			if (at == std::string::npos) {
				out.append(text, i);
				break;
			}
			out.append(text, i, at - i);
			out += "***";
			i = at + secret.size();
		}
		text = std::move(out);
	}
	e.text = truncate(text, 400);
	return e;
}

} // namespace nkos::notify
