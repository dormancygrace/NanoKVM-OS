#include "notify/oauth.hpp"

#include <openssl/sha.h>

#include <algorithm>
#include <format>

namespace nkos::notify {

namespace {

const std::string google_auth_url = "https://accounts.google.com/o/oauth2/v2/auth";
const std::string google_token_url = "https://oauth2.googleapis.com/token";
const std::string google_revoke_url = "https://oauth2.googleapis.com/revoke";
const std::string microsoft_url = "https://login.microsoftonline.com";

constexpr char google_scope[] = "https://mail.google.com/ openid email";
constexpr char microsoft_scope[] = "https://outlook.office.com/SMTP.Send offline_access openid email profile";
// An access token is renewed this long before it expires; a sign-in on the page and its result wait this long;
// at most max_flows sign-ins run.
constexpr auto token_slack = std::chrono::minutes(1);
constexpr auto flow_ttl = std::chrono::minutes(15);
constexpr auto pending_ttl = std::chrono::hours(1);
constexpr std::size_t max_flows = 4;

struct TokenResponse {
	std::string access_token, refresh_token, id_token, device_code, user_code, verification_uri, error, description;
	std::int64_t expires_in = 0, interval = 0;
};

bool decode_token_response(const json::Value &v, TokenResponse &t)
{
	return json::members(v,
	                     {"access_token", "refresh_token", "expires_in", "id_token", "device_code", "user_code",
	                      "verification_uri", "interval", "error", "error_description"},
	                     [&](std::string_view f, const json::Value &x) {
		                     if (f == "access_token") return json::get(x, t.access_token);
		                     if (f == "refresh_token") return json::get(x, t.refresh_token);
		                     if (f == "expires_in") return json::get(x, t.expires_in);
		                     if (f == "id_token") return json::get(x, t.id_token);
		                     if (f == "device_code") return json::get(x, t.device_code);
		                     if (f == "user_code") return json::get(x, t.user_code);
		                     if (f == "verification_uri") return json::get(x, t.verification_uri);
		                     if (f == "interval") return json::get(x, t.interval);
		                     if (f == "error") return json::get(x, t.error);
		                     return json::get(x, t.description);
	                     });
}

// oauthErr.Error: the error answer of a sign-in service in words.
std::string oauth_error_text(const std::string &code, std::string text)
{
	// Microsoft adds trace and correlation ids on further lines.
	if (auto nl = text.find_first_of("\r\n"); nl != std::string::npos) text = text.substr(0, nl);
	if (text.find("AADSTS7000218") != std::string::npos)
		return "the app registration must allow public client flows (Authentication > Advanced settings)";
	if (text.find("AADSTS50194") != std::string::npos) return "the app is for one organisation only: enter its tenant ID";
	if (code == "invalid_grant") return "the sign-in has expired or was revoked; sign in again";
	if (text.empty()) return code;
	return code + ": " + text;
}

// postForm: the answer of a token endpoint. oauth_code is the error code of an error answer.
struct FormAnswer {
	TokenResponse answer;
	Result error;
	std::string oauth_code;
};

FormAnswer post_form(const Poster &post, const std::string &target, const gostd::url::Values &form)
{
	FormAnswer out;
	HttpRequest req{"POST", target,
	                {{"Content-Type", "application/x-www-form-urlencoded"}, {"Accept", "application/json"}, {"User-Agent", "NanoKVM"}},
	                gostd::url::encode(form)};
	req.max_body = 64u << 10;
	HttpAnswer a = post(req);
	if (!a.error.empty()) {
		out.error = Error{a.error};
		return out;
	}
	json::Value v;
	if (!json::unmarshal(a.body, v) || !decode_token_response(v, out.answer)) {
		out.error = Error{std::format("HTTP {} from the sign-in service", a.status)};
		return out;
	}
	if (!out.answer.error.empty()) {
		out.oauth_code = out.answer.error;
		Error e{oauth_error_text(out.answer.error, out.answer.description)};
		e.permanent = a.status >= 400 && a.status < 500;
		out.error = e;
		return out;
	}
	if (a.status != 200) out.error = Error{std::format("HTTP {} from the sign-in service", a.status)};
	return out;
}

bool is_tenant(std::string_view t)
{
	auto alnum = [](char c) { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'); };
	if (t.empty() || t.size() > 64 || !alnum(t[0])) return false;
	for (char c : t.substr(1))
		if (!alnum(c) && c != '.' && c != '-') return false;
	return true;
}

std::string hash_of(const std::string &s)
{
	unsigned char sum[SHA256_DIGEST_LENGTH];
	SHA256(reinterpret_cast<const unsigned char *>(s.data()), s.size(), sum);
	static const char hex[] = "0123456789abcdef";
	std::string out;
	for (int i = 0; i < 8; i++) {
		out += hex[sum[i] >> 4];
		out += hex[sum[i] & 15];
	}
	return out;
}

// A saved channel by its id, one on the page by its token.
std::string entry_key(const Channel &ch) { return ch.id.empty() ? "t" + hash_of(ch.get("oauthToken")) : ch.id; }

} // namespace

std::string OAuthClient::token_url() const
{
	return kind == oauth_google ? google_token_url : microsoft_url + "/" + tenant + "/oauth2/v2.0/token";
}

std::optional<OAuthClient> client_of(const Channel &ch, Error &error)
{
	const Provider &p = provider_by_id(ch.get("provider"));
	OAuthClient c{p.oauth, ch.get("oauthClientId"), ch.get("oauthClientSecret"), ch.get("oauthTenant")};
	if (c.tenant.empty()) c.tenant = p.tenant;
	if (c.kind.empty()) error = permanent("OAuth sign-in is for Gmail and Microsoft accounts");
	else if (c.id.empty()) error = permanent("the OAuth client ID is missing");
	else if (c.kind == oauth_google && c.secret.empty()) error = permanent("the OAuth client secret is missing");
	else if (c.kind == oauth_microsoft && !is_tenant(c.tenant)) error = permanent("the tenant is not a tenant ID or domain");
	else return c;
	return std::nullopt;
}

std::string account_of(const std::string &id_token)
{
	auto a = id_token.find('.');
	if (a == std::string::npos) return "";
	auto b = id_token.find('.', a + 1);
	if (b == std::string::npos || id_token.find('.', b + 1) != std::string::npos) return "";
	auto payload = gostd::base64url_raw_decode(std::string_view(id_token).substr(a + 1, b - a - 1));
	if (!payload) return "";
	json::Value v;
	std::string email, preferred;
	if (!json::unmarshal(*payload, v) ||
	    !json::members(v, {"email", "preferred_username"}, [&](std::string_view f, const json::Value &x) {
		    return json::get(x, f == "email" ? email : preferred);
	    }))
		return "";
	// Microsoft often leaves email out; preferred_username is the address the account signs in with.
	return email.empty() ? preferred : email;
}

json::Out FlowStart::json() const
{
	json::Out o = json::Out::object().set("id", json::Out::str(id)).set("kind", json::Out::str(kind)).set("url", json::Out::str(url));
	if (!user_code.empty()) o.set("userCode", json::Out::str(user_code));
	if (interval) o.set("interval", json::Out::number(interval));
	return o.set("expiresIn", json::Out::number(expires_in));
}

json::Out FlowResult::json() const
{
	json::Out o = json::Out::object().set("pending", json::Out::boolean(pending));
	if (interval) o.set("interval", json::Out::number(interval));
	if (!token.empty()) o.set("token", json::Out::str(token));
	if (!account.empty()) o.set("account", json::Out::str(account));
	return o;
}

Result OAuth::access_token(const Channel &ch, std::string &token)
{
	std::string stored = ch.get("oauthToken");
	if (stored.empty() || stored.starts_with(pending_prefix)) return Error{"not signed in", true, true};
	Error err;
	auto client = client_of(ch, err);
	if (!client) return err;
	std::string key = entry_key(ch);
	std::shared_ptr<Entry> e;
	{
		std::lock_guard lock(mu_);
		auto &slot = entries_[key];
		if (!slot || slot->base != stored) {
			slot = std::make_shared<Entry>();
			slot->base = slot->refresh = stored;
		}
		e = slot;
	}
	std::lock_guard entry_lock(e->mu);
	if (!e->access.empty() && now() + token_slack < e->expiry) {
		token = e->access;
		return std::nullopt;
	}
	gostd::url::Values form = {{"grant_type", {"refresh_token"}}, {"client_id", {client->id}}, {"refresh_token", {e->refresh}}};
	if (client->kind == oauth_google) form["client_secret"] = {client->secret};
	else form["scope"] = {microsoft_scope};
	FormAnswer a = post_form(post, client->token_url(), form);
	if (!a.error && a.answer.access_token.empty()) a.error = Error{"the sign-in service sent no access token"};
	if (a.error) {
		if (a.oauth_code == "invalid_grant") {
			a.error->permanent = true;
			a.error->auth = true;
		}
		return a.error;
	}
	e->access = a.answer.access_token;
	e->expiry = now() + std::chrono::seconds(a.answer.expires_in);
	if (!a.answer.refresh_token.empty() && a.answer.refresh_token != e->refresh) {
		e->refresh = a.answer.refresh_token;
		if (!ch.id.empty() && persist && persist(ch.id, e->base, a.answer.refresh_token)) {
			std::lock_guard lock(mu_);
			e->base = a.answer.refresh_token;
		}
	}
	token = e->access;
	return std::nullopt;
}

void OAuth::forget(const Channel &ch)
{
	std::lock_guard lock(mu_);
	auto it = entries_.find(entry_key(ch));
	if (it != entries_.end() && it->second->base == ch.get("oauthToken")) entries_.erase(it);
}

void OAuth::expire()
{
	auto t = now();
	std::erase_if(flows_, [&](const auto &f) { return t > f.second.expires; });
	std::erase_if(pending_, [&](const auto &p) { return t > p.second.expires; });
}

void OAuth::drop(const std::string &id)
{
	std::lock_guard lock(mu_);
	flows_.erase(id);
}

std::optional<FlowStart> OAuth::start(const Channel &ch, Error &error)
{
	auto client = client_of(ch, error);
	if (!client) return std::nullopt;
	Flow f{*client};
	f.expires = now() + flow_ttl;
	FlowStart s{random(16), "code"};
	s.expires_in = std::chrono::duration_cast<std::chrono::seconds>(flow_ttl).count();
	if (client->kind == oauth_google) {
		f.verifier = random(32);
		f.state = random(16);
		f.redirect = std::format("http://127.0.0.1:{}/", 49152 + random_port());
		unsigned char challenge[SHA256_DIGEST_LENGTH];
		SHA256(reinterpret_cast<const unsigned char *>(f.verifier.data()), f.verifier.size(), challenge);
		gostd::url::Values query = {
			{"client_id", {client->id}},
			{"redirect_uri", {f.redirect}},
			{"response_type", {"code"}},
			{"scope", {google_scope}},
			{"access_type", {"offline"}},
			{"prompt", {"consent"}},
			{"state", {f.state}},
			{"code_challenge", {gostd::base64url_raw(std::string_view(reinterpret_cast<char *>(challenge), sizeof challenge))}},
			{"code_challenge_method", {"S256"}},
		};
		if (auto user = ch.get("username"); !user.empty()) query["login_hint"] = {user};
		s.url = google_auth_url + "?" + gostd::url::encode(query);
	} else {
		FormAnswer a = post_form(post, microsoft_url + "/" + client->tenant + "/oauth2/v2.0/devicecode",
		                         {{"client_id", {client->id}}, {"scope", {microsoft_scope}}});
		if (a.error) {
			error = *a.error;
			return std::nullopt;
		}
		if (a.answer.device_code.empty() || a.answer.user_code.empty()) {
			error = Error{"the sign-in service sent no device code"};
			return std::nullopt;
		}
		f.device_code = a.answer.device_code;
		f.interval = std::max<std::int64_t>(a.answer.interval, 5);
		if (a.answer.expires_in > 0) {
			f.expires = now() + std::chrono::seconds(a.answer.expires_in);
			s.expires_in = a.answer.expires_in;
		}
		s.kind = "device";
		s.url = a.answer.verification_uri;
		s.user_code = a.answer.user_code;
		s.interval = f.interval;
	}
	std::lock_guard lock(mu_);
	expire();
	while (flows_.size() >= max_flows) {
		auto oldest = std::min_element(flows_.begin(), flows_.end(),
		                               [](const auto &a, const auto &b) { return a.second.expires < b.second.expires; });
		flows_.erase(oldest);
	}
	flows_[s.id] = f;
	return s;
}

std::optional<FlowResult> OAuth::finish(const std::string &id, const std::string &code_text, Error &error)
{
	std::optional<Flow> found;
	{
		std::lock_guard lock(mu_);
		expire();
		if (auto it = flows_.find(id); it != flows_.end()) found = it->second;
	}
	if (!found) {
		error = Error{"the sign-in has expired; start it again"};
		return std::nullopt;
	}
	const Flow &f = *found;
	const OAuthClient &c = f.client;
	gostd::url::Values form;
	if (c.kind == oauth_google) {
		std::string code = gostd::trim_space(code_text);
		if (auto u = gostd::url::parse(code)) {
			auto q = gostd::url::parse_query(u->raw_query);
			if (q.contains("code") || q.contains("error")) {
				auto first = [&](const char *k) { return q.contains(k) ? q[k].front() : std::string(); };
				if (auto e = first("error"); !e.empty()) {
					drop(id);
					error = Error{"Google refused the sign-in: " + e};
					return std::nullopt;
				}
				if (first("state") != f.state) {
					error = Error{"the address belongs to another sign-in"};
					return std::nullopt;
				}
				code = first("code");
			}
		}
		if (code.empty()) {
			error = Error{"paste the address the browser opened after the sign-in"};
			return std::nullopt;
		}
		form = {{"grant_type", {"authorization_code"}}, {"code", {code}}, {"client_id", {c.id}},
		        {"client_secret", {c.secret}}, {"redirect_uri", {f.redirect}}, {"code_verifier", {f.verifier}}};
	} else {
		form = {{"grant_type", {"urn:ietf:params:oauth:grant-type:device_code"}}, {"client_id", {c.id}},
		        {"device_code", {f.device_code}}};
	}
	FormAnswer a = post_form(post, c.token_url(), form);
	if (a.error) {
		if (c.kind == oauth_microsoft && (a.oauth_code == "authorization_pending" || a.oauth_code == "slow_down")) {
			std::lock_guard lock(mu_);
			std::int64_t interval = f.interval;
			if (auto it = flows_.find(id); it != flows_.end()) {
				if (a.oauth_code == "slow_down") it->second.interval += 5;
				interval = it->second.interval;
			} else if (a.oauth_code == "slow_down") {
				interval += 5;
			}
			return FlowResult{true, interval};
		}
		drop(id);
		error = *a.error;
		return std::nullopt;
	}
	drop(id);
	if (a.answer.refresh_token.empty()) {
		error = Error{"the sign-in service sent no refresh token; remove the app's access from the account and sign in again"};
		return std::nullopt;
	}
	std::string handle = std::string(pending_prefix) + random(24);
	{
		std::lock_guard lock(mu_);
		while (pending_.size() >= max_channels) {
			auto oldest = std::min_element(pending_.begin(), pending_.end(),
			                               [](const auto &x, const auto &y) { return x.second.expires < y.second.expires; });
			pending_.erase(oldest);
		}
		pending_[handle] = {a.answer.refresh_token, now() + pending_ttl};
	}
	return FlowResult{false, 0, handle, account_of(a.answer.id_token)};
}

std::string OAuth::claim(Channel &ch)
{
	std::string handle = ch.get("oauthToken");
	if (!handle.starts_with(pending_prefix)) return "";
	std::lock_guard lock(mu_);
	expire();
	auto it = pending_.find(handle);
	if (it == pending_.end()) return "the sign-in has expired; sign in again";
	if (!ch.settings) ch.settings.emplace();
	(*ch.settings)["oauthToken"] = it->second.token;
	return "";
}

std::optional<OAuth::FlowInfo> OAuth::flow_info(const std::string &id)
{
	std::lock_guard lock(mu_);
	auto it = flows_.find(id);
	if (it == flows_.end()) return std::nullopt;
	return FlowInfo{it->second.verifier, it->second.redirect, it->second.state, it->second.device_code, it->second.interval};
}

void revoke(const OAuthClient &c, const std::string &refresh, const Poster &post)
{
	if (c.kind != oauth_google || refresh.empty()) return;
	HttpRequest req{"POST", google_revoke_url, {{"Content-Type", "application/x-www-form-urlencoded"}},
	                gostd::url::encode({{"token", {refresh}}})};
	post(req);
}

std::string xoauth2(const std::string &user, const std::string &token)
{
	return "user=" + user + "\x01" + "auth=Bearer " + token + "\x01\x01";
}

} // namespace nkos::notify
