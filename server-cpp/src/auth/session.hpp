#pragma once

// Session checks of the API, as the Go server's middleware package: the token from "Authorization: Bearer" or the
// session cookie, the account's token version, the idle sign-out, the forced password change, roles and the
// recent password confirmation; the client address and scheme behind trusted proxies; the session cookies.
// Loop thread only.

#include "auth/jwt.hpp"
#include "http/router.hpp"

#include <functional>
#include <string>
#include <vector>

namespace nkos::auth {

inline constexpr const char *cookie_name = "nano-kvm-token";
inline constexpr const char *http_cookie_name = "nano-kvm-token-http";
inline constexpr int password_change_required_code = -10;
inline constexpr int recent_auth_required_code = -11;

// Sets request.auth for a valid session. Returns false otherwise; idle_over is set when the session was valid but
// is past its idle limit (its cookie should then be cleared).
bool authenticate(http::Request &request, bool &idle_over);

struct Guard {
	std::vector<std::string> roles;  // empty: any signed-in account
	bool recent_auth = false;        // RequireRecentAuth
	bool stream = false;             // a WebSocket or stream route (idle rechecks)
};

// CheckToken (+ RequireRole, RequireRecentAuth) around a handler.
http::Handler guarded(http::Handler handler, Guard guard = {});

// The client address behind trusted proxies (ClientAddress), and the external host and scheme (ExternalRequestAddress).
std::string client_address(const http::Request &request);
std::pair<std::string, std::string> external_address(const http::Request &request);

// The session cookie name for this request's scheme, and setting or clearing the cookies.
std::string session_cookie_name(const http::Request &request);
void set_session_cookie(const http::Request &request, http::Response &response, const std::string &token);
void clear_session_cookie(const http::Request &request, http::Response &response);

// Idle sign-out (/etc/kvm/idle-signout).
inline const std::vector<int> idle_limits{0, 300, 900, 1800, 3600, 14400};
int idle_limit_seconds();
std::optional<std::string> set_idle_limit(int seconds);
// limit and seconds left for the request's session; touch counts as activity.
std::pair<int, int> idle_status(const http::Request &request, bool touch);

// Recent password confirmation (5 minutes).
void mark_recent_auth(const http::Request &request);

// Ends every open session of username (streams, sockets) and runs the revoke hooks.
void revoke_user_sessions(const std::string &username);
void on_user_revoked(std::function<void(const std::string &)> hook);

} // namespace nkos::auth
