#pragma once

// OAuth 2.0 sign-in of the e-mail channel (SMTP XOAUTH2), as the Go server's service/notify/oauth.go. The OAuth
// client is the user's own registration. Google allows no device flow for mail, so its sign-in is an authorization
// code with PKCE and a loopback address nothing listens on: the user pastes the address the browser ends on.
// Microsoft takes the device code flow of a public client. The refresh token stays on the device (in the channel's
// settings); access tokens live in memory only. Any thread.

#include "notify/channels.hpp"
#include "notify/model.hpp"

#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>

namespace nkos::notify {

struct OAuthClient {
	std::string kind, id, secret, tenant;
	std::string token_url() const;
};

// The client of a channel's settings; error when it is incomplete.
std::optional<OAuthClient> client_of(const Channel &ch, Error &error);

// The mailbox address of an ID token (not verified: it came from the token endpoint over TLS).
std::string account_of(const std::string &id_token);

// A sign-in for the page: a code to enter at an address (device), or an address to open whose final address the
// user pastes back (code).
struct FlowStart {
	std::string id, kind, url, user_code;
	std::int64_t interval = 0, expires_in = 0;
	json::Out json() const;
};

// pending while the device code waits for the user; else token is the handle the page puts in the oauthToken field
// and account the mailbox.
struct FlowResult {
	bool pending = false;
	std::int64_t interval = 0;
	std::string token, account;
	json::Out json() const;
};

inline constexpr std::string_view pending_prefix = "pending:";

class OAuth {
public:
	// Seams: the clock, the HTTP requests, the randomness (randomString(n) and the loopback port offset),
	// storing a renewed refresh token (id, old, new: whether it was stored), revoking in the background.
	std::function<Clock::time_point()> now = [] { return Clock::now(); };
	Poster post;
	std::function<std::string(std::size_t)> random = [](std::size_t n) { return gostd::random_base64url(n); };
	std::function<int()> random_port;
	std::function<bool(const std::string &, const std::string &, const std::string &)> persist;
	std::function<void(const OAuthClient &, const std::string &)> revoke_later;

	// A valid access token for the channel, renewed when it is about to expire.
	Result access_token(const Channel &ch, std::string &token);
	// Drops the access token after the mail server refused it.
	void forget(const Channel &ch);
	std::optional<FlowStart> start(const Channel &ch, Error &error);
	std::optional<FlowResult> finish(const std::string &id, const std::string &code, Error &error);
	// Puts the refresh token of a sign-in made on the page in place of its handle; "" or the error.
	std::string claim(Channel &ch);

	// The verifier, state and loopback address of a flow (the differential check).
	struct FlowInfo {
		std::string verifier, redirect, state, device_code;
		std::int64_t interval = 0;
	};
	std::optional<FlowInfo> flow_info(const std::string &id);

private:
	struct Entry {
		std::mutex mu;
		std::string base, refresh, access;
		Clock::time_point expiry{};
	};
	struct Flow {
		OAuthClient client;
		std::string verifier, redirect, state, device_code;
		std::int64_t interval = 0;
		Clock::time_point expires{};
	};
	struct Pending {
		std::string token;
		Clock::time_point expires{};
	};
	std::mutex mu_;
	std::map<std::string, std::shared_ptr<Entry>> entries_;
	std::map<std::string, Flow> flows_;
	std::map<std::string, Pending> pending_;

	void expire();
	void drop(const std::string &id);
};

// Revokes a Google refresh token (Microsoft has none for a public client); blocking.
void revoke(const OAuthClient &c, const std::string &refresh, const Poster &post);

// The XOAUTH2 initial response.
std::string xoauth2(const std::string &user, const std::string &token);

} // namespace nkos::notify
