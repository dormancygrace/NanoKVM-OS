#include "auth/routes.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/brute_force.hpp"
#include "auth/jwt.hpp"
#include "auth/password.hpp"
#include "auth/session.hpp"
#include "auth/store.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"

#include <glaze/glaze.hpp>

#include <chrono>
#include <cstdio>
#include <memory>
#include <sys/wait.h>
#include <unistd.h>

namespace nkos::auth {

namespace {

using namespace std::chrono_literals;
using http::Request;
using http::Response;

constexpr auto penalty = 1s;
constexpr const char *return_token_header = "x-nanokvm-return-token";

// What an asynchronous handler keeps of its request: the request object belongs to the connection.
struct Call {
	std::uint64_t id;
	Response response;  // headers prepared on the loop thread
	std::string cookie_name;
	bool secure_cookie;
	bool return_token;
	std::string client;  // login key
};

std::shared_ptr<Call> start(Request &req, Response &res)
{
	auto call = std::make_shared<Call>();
	call->id = req.id;
	call->response.headers = res.headers;  // the security headers set by the middleware
	call->cookie_name = session_cookie_name(req);
	call->secure_cookie = call->cookie_name == cookie_name;
	std::string flag = req.header(return_token_header);
	while (!flag.empty() && flag.back() == ' ') flag.pop_back();
	while (!flag.empty() && flag.front() == ' ') flag.erase(0, 1);
	for (auto &c : flag) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	call->return_token = flag == "true";
	call->client = login_key(client_address(req));
	res.pending = true;
	return call;
}

void finish(const std::shared_ptr<Call> &call) { http::complete(call->id, std::move(call->response)); }

void finish_after(const std::shared_ptr<Call> &call, std::chrono::milliseconds delay)
{
	loop::after(delay, [call] { finish(call); });
}

void fail(const std::shared_ptr<Call> &call, std::chrono::milliseconds delay, int code, const std::string &msg)
{
	api::err(call->response, code, msg);
	finish_after(call, delay);
}

// set_session_cookie with what the call kept.
void set_cookie(Call &call, const std::string &token)
{
	auto line = [](const std::string &name, const std::string &value, int max_age, bool secure) {
		std::string l = name + "=" + value + "; Path=/";
		if (max_age > 0) l += "; Max-Age=" + std::to_string(max_age);
		else if (max_age < 0) l += "; Max-Age=0";
		l += "; HttpOnly";
		if (secure) l += "; Secure";
		return l + "; SameSite=Strict";
	};
	if (call.secure_cookie) call.response.add_header("Set-Cookie", line(http_cookie_name, "", -1, true));
	else call.response.add_header("Set-Cookie", line(cookie_name, "", -1, false));
	call.response.add_header("Set-Cookie", line(call.cookie_name, token,
	                                            static_cast<int>(config::get().jwt.refresh_token_duration), call.secure_cookie));
}

struct LoginRsp {
	std::string token;
};

// finishLogin: the record of the client cleared, a token, the cookie.
void finish_login(const std::shared_ptr<Call> &call, const User &user)
{
	clear_login_attempt(call->client, user.username);
	std::string token = generate(user.username, user.token_version);
	set_cookie(*call, token);
	if (call->return_token) api::ok(call->response, LoginRsp{token});
	else api::ok(call->response);
	log::info("user logged in: {}", user.username);
	finish(call);
}

// loginFailedFor.
void login_failed(const std::shared_ptr<Call> &call, const std::string &username)
{
	if (auto refused = record_login_failure(call->client, username)) return fail(call, 2 * penalty, refused->code, refused->message);
	fail(call, 2 * penalty, -2, "invalid username or password");
}

std::optional<std::string> trimmed_field(const api::Params &p, const std::string &name, bool &ok)
{
	return p.string(name, ok);
}

void login(Request &req, Response &res)
{
	res.set_header("Cache-Control", "no-store");
	res.set_header("Vary", "X-NanoKVM-Return-Token");
	if (config::get().authentication == "disable") return api::ok(res);
	auto call = start(req, res);
	call->response.headers = res.headers;
	if (auto refused = check_login_attempt(call->client)) return fail(call, 3 * penalty, refused->code, refused->message);
	bool ok = true;
	auto params = api::Params::bind(req);
	std::optional<std::string> username, password, encoding, otp;
	if (params) {
		username = trimmed_field(*params, "username", ok);
		password = trimmed_field(*params, "password", ok);
		encoding = trimmed_field(*params, "passwordEncoding", ok);
		otp = trimmed_field(*params, "otp", ok);
	}
	if (!params || !ok || !username || username->empty() || !password || password->empty())
		return fail(call, 3 * penalty, -1, "invalid parameters");
	begin_login_attempt(call->client, [call, username = *username, password = *password, encoding = encoding.value_or(""),
	                                   otp = otp.value_or("")](std::optional<Refusal> refused) {
		if (refused) return fail(call, 3 * penalty, refused->code, refused->message);
		auto plain = decode_credential(password, encoding);
		if (!plain || plain->empty()) {
			release_login(call->client);
			return login_failed(call, "");
		}
		loop::work([call, username, plain = *plain, otp] {
			std::string error;
			auto user = store().authenticate(username, plain, error);
			loop::post([call, username, user, error, otp] {
				release_login(call->client);
				if (!error.empty()) {
					log::error("load account during login: {}", error);
					return fail(call, 2 * penalty, -3, "authentication unavailable");
				}
				if (!user) return login_failed(call, username);
				if (user->totp) {
					// Two-factor sign-in comes with the TOTP part of this family.
					log::warn("user {} has two-factor authentication, not handled yet", user->username);
					return fail(call, 2 * penalty, -3, "authentication unavailable");
				}
				finish_login(call, *user);
			});
		});
	});
}

void logout(Request &req, Response &res)
{
	const auto &conf = config::get();
	if (req.auth && conf.authentication != "disable" && conf.jwt.revoke_tokens_on_logout) {
		std::string error;
		if (!store().revoke(req.auth->username, error)) return api::err(res, -1, "failed to revoke session");
		revoke_user_sessions(req.auth->username);
		log::info("user logged out: {}", req.auth->username);
	}
	clear_session_cookie(req, res);
	api::ok(res);
}

struct AccountRsp {
	std::string username;
	std::string role;
	bool mustChangePassword = false;
	bool totpEnabled = false;
	std::optional<int> recoveryCodesLeft;  // omitempty
};

void account(Request &req, Response &res)
{
	if (!req.auth) return api::err(res, -1, "get account failed");
	AccountRsp data{req.auth->username, req.auth->role, req.auth->must_change_password, false, std::nullopt};
	std::string error;
	if (auto user = store().get(req.auth->username, error); user && user->totp) {
		data.totpEnabled = true;
		if (!user->totp->recovery_hashes.empty()) data.recoveryCodesLeft = static_cast<int>(user->totp->recovery_hashes.size());
	}
	api::ok(res, data);
}

struct PasswordUpdatedRsp {
	bool isUpdated = false;
};

void password_updated(Request &req, Response &res)
{
	if (config::get().authentication == "disable") return api::ok(res, PasswordUpdatedRsp{true});
	if (!req.auth) return api::err(res, -1, "invalid session");
	std::string error;
	auto user = store().get(req.auth->username, error);
	if (!user) return api::err(res, -2, "failed to get password state");
	api::ok(res, PasswordUpdatedRsp{!user->must_change_password});
}

// passwd root, the password twice on its input.
std::optional<std::string> change_root_password(const std::string &password)
{
	int in[2];
	if (pipe(in) != 0) return std::string("cannot start passwd");
	pid_t pid = fork();
	if (pid < 0) return std::string("cannot start passwd");
	if (pid == 0) {
		dup2(in[0], 0);
		close(in[0]);
		close(in[1]);
		execlp("passwd", "passwd", "root", static_cast<char *>(nullptr));
		_exit(127);
	}
	close(in[0]);
	std::string line = password + "\n";
	bool ok = write(in[1], line.data(), line.size()) == static_cast<ssize_t>(line.size());
	usleep(100000);
	ok = ok && write(in[1], line.data(), line.size()) == static_cast<ssize_t>(line.size());
	close(in[1]);
	int status = 0;
	waitpid(pid, &status, 0);
	if (!ok || !WIFEXITED(status) || WEXITSTATUS(status) != 0) {
		log::error("failed to change root password");
		return std::string("exit status " + std::to_string(WIFEXITED(status) ? WEXITSTATUS(status) : -1));
	}
	return std::nullopt;
}

// changeUserPassword: the device owner's password is root's too. Runs on the worker.
std::optional<User> change_user_password(const std::string &username, const std::string &password,
                                         const std::function<std::optional<std::string>(const User &)> &expect, std::string &error)
{
	if (auto e = validate_password(password)) return error = *e, std::nullopt;
	auto user = store().get(username, error);
	if (!user) return std::nullopt;
	std::function<std::optional<std::string>()> after;
	if (user->system_account && user->role == "admin" && user->enabled) after = [password] { return change_root_password(password); };
	return store().set_password(username, password, expect, after, error);
}

struct ChangePasswordRsp {
	bool signedIn = false;
	std::optional<std::string> token;
};

void change_password(Request &req, Response &res)
{
	bool ok = true;
	auto params = api::Params::bind(req);
	std::optional<std::string> next, current, encoding;
	if (params) {
		next = params->string("password", ok);
		current = params->string("currentPassword", ok);
		encoding = params->string("passwordEncoding", ok);
	}
	if (!params || !ok || !next || next->empty()) return api::err(res, -1, "invalid parameters");
	if (!req.auth) return api::err(res, -2, "invalid session");
	auto call = start(req, res);
	bool forced = req.auth->must_change_password;
	std::string username = req.auth->username;
	std::uint64_t version = req.auth->token_version;
	bool has_token = req.auth->has_token;
	auto decoded = decode_credential(*next, encoding.value_or(""));

	auto apply = [call, username, forced, version, has_token, decoded] {
		if (!decoded || decoded->empty()) {
			api::err(call->response, -5, "invalid password");
			return finish(call);
		}
		loop::work([call, username, forced, version, has_token, password = *decoded] {
			std::string error;
			std::function<std::optional<std::string>(const User &)> expect;
			if (forced)
				expect = [version, has_token](const User &u) -> std::optional<std::string> {
					if (!has_token || !u.must_change_password || u.token_version != version) return std::string("stale");
					return std::nullopt;
				};
			auto user = change_user_password(username, password, expect, error);
			loop::post([call, username, forced, user, error] {
				if (!user && error == "stale") {
					api::err(call->response, -2, "invalid session");
					return finish(call);
				}
				if (!user) {
					api::err(call->response, -5, error);
					return finish(call);
				}
				revoke_user_sessions(username);
				log::info("password changed for user: {}", username);
				auto clear = [&] {
					bool secure = call->secure_cookie;
					for (const char *name : {cookie_name, http_cookie_name})
						call->response.add_header("Set-Cookie", std::string(name) + "=; Path=/; Max-Age=0; HttpOnly" +
						                                            (secure ? "; Secure" : "") + "; SameSite=Strict");
				};
				if (!forced) {
					clear();
					api::ok(call->response);
					return finish(call);
				}
				std::string token = generate(user->username, user->token_version);
				call->response.set_header("Cache-Control", "no-store");
				set_cookie(*call, token);
				ChangePasswordRsp data{true, std::nullopt};
				if (call->return_token) data.token = token;
				api::ok(call->response, data);
				finish(call);
			});
		});
	};

	if (forced) return apply();
	auto plain = decode_credential(current.value_or(""), encoding.value_or(""));
	if (!plain || plain->empty()) {
		api::err(call->response, -3, "current password is required");
		return finish(call);
	}
	begin_login_attempt(call->client, [call, username, current = *plain, apply](std::optional<Refusal> refused) {
		if (refused) return fail(call, 3 * penalty, refused->code, refused->message);
		loop::work([call, username, current, apply] {
			std::string error;
			auto user = store().authenticate(username, current, error);
			loop::post([call, username, user, error, apply] {
				release_login(call->client);
				if (!error.empty()) return fail(call, 0ms, -4, "authentication unavailable");
				if (!user) {
					if (auto refused = record_login_failure(call->client, username))
						return fail(call, 2 * penalty, refused->code, refused->message);
					return fail(call, 2 * penalty, -3, "current password is incorrect");
				}
				apply();
			});
		});
	});
}

struct IdleRsp {
	bool available = false;
	int limit = 0;
	int left = 0;
	std::vector<int> limits;
};

IdleRsp idle_answer(const Request &req, bool touch)
{
	auto [limit, left] = idle_status(req, touch);
	return IdleRsp{config::get().authentication != "disable", limit, left, idle_limits};
}

void set_idle(Request &req, Response &res)
{
	bool ok = true;
	auto params = api::Params::bind(req);
	std::optional<long long> limit;
	if (params) limit = params->integer("limit", ok);
	if (!params || !ok || !limit) return api::err(res, -1, "invalid arguments");
	if (auto e = set_idle_limit(static_cast<int>(*limit))) return api::err(res, -2, *e);
	api::ok(res, idle_answer(req, false));
}

struct UserInfo {
	std::string username;
	std::string role;
	bool enabled = false;
	std::optional<bool> systemAccount;
	std::optional<bool> totpEnabled;
};
struct ListUsersRsp {
	std::vector<UserInfo> users;
};

void list_users(Request &, Response &res)
{
	std::string error;
	auto users = store().list(error);
	if (!users) return api::err(res, -1, "failed to load users");
	ListUsersRsp data;
	for (const auto &u : *users) {
		UserInfo info{u.username, u.role, u.enabled, std::nullopt, std::nullopt};
		if (u.system_account) info.systemAccount = true;
		if (u.totp) info.totpEnabled = true;
		data.users.push_back(info);
	}
	api::ok(res, data);
}

void create_user(Request &req, Response &res)
{
	bool ok = true;
	auto params = api::Params::bind(req);
	std::optional<std::string> username, password, encoding, role;
	if (params) {
		username = params->string("username", ok);
		password = params->string("password", ok);
		encoding = params->string("passwordEncoding", ok);
		role = params->string("role", ok);
	}
	if (!params || !ok || !username || username->empty() || !password || password->empty() || !role || role->empty())
		return api::err(res, -1, "invalid parameters");
	auto plain = decode_credential(*password, encoding.value_or(""));
	if (!plain || plain->empty()) return api::err(res, -2, "invalid password");
	if (auto e = validate_password(*plain)) return api::err(res, -2, *e);
	auto call = start(req, res);
	loop::work([call, username = *username, plain = *plain, role = *role] {
		std::string error;
		bool created = store().create(username, plain, role, error);
		loop::post([call, created, error, username, role] {
			if (!created) api::err(call->response, -3, error);
			else {
				api::ok(call->response);
				log::info("user created: {} ({})", username, role);
			}
			finish(call);
		});
	});
}

void update_user(Request &req, Response &res)
{
	bool ok = true;
	auto params = api::Params::bind(req);
	UserPatch patch;
	if (params) {
		patch.username = params->string("username", ok);
		patch.role = params->string("role", ok);
		patch.enabled = params->boolean("enabled", ok);
	}
	if (!params || !ok || (!patch.username && !patch.role && !patch.enabled)) return api::err(res, -1, "invalid parameters");
	std::string username = req.param("username"), error;
	bool changed = false;
	auto updated = store().update(req.auth ? req.auth->username : "", username, patch, changed, error);
	if (!updated) return api::err(res, -2, error);
	if (changed) revoke_user_sessions(username);
	api::ok(res);
	log::info("user updated: {}", updated->username);
}

void delete_user(Request &req, Response &res)
{
	std::string username = req.param("username"), error;
	if (!store().remove(req.auth ? req.auth->username : "", username, error)) return api::err(res, -1, error);
	revoke_user_sessions(username);
	api::ok(res);
	log::info("user deleted: {}", username);
}

void change_user_password_route(Request &req, Response &res)
{
	bool ok = true;
	auto params = api::Params::bind(req);
	std::optional<std::string> password, encoding;
	if (params) {
		password = params->string("password", ok);
		encoding = params->string("passwordEncoding", ok);
	}
	if (!params || !ok || !password || password->empty()) return api::err(res, -1, "invalid parameters");
	std::string username = req.param("username"), error;
	auto user = store().get(username, error);
	if (!user) return api::err(res, -2, error);
	if (user->system_account) return api::err(res, -3, "the device owner must change its own password");
	auto plain = decode_credential(*password, encoding.value_or(""));
	if (!plain || plain->empty()) return api::err(res, -4, "invalid password");
	auto call = start(req, res);
	loop::work([call, username, plain = *plain] {
		std::string error;
		auto changed = change_user_password(username, plain, {}, error);
		loop::post([call, username, changed, error] {
			if (!changed) api::err(call->response, -4, error);
			else {
				revoke_user_sessions(username);
				api::ok(call->response);
				log::info("password reset for user: {}", username);
			}
			finish(call);
		});
	});
}

} // namespace

void add_routes(http::Router &r)
{
	r.add("POST", "/api/auth/login", login);
	r.add("POST", "/api/auth/login/totp", [](Request &, Response &res) {
		// No challenges exist until two-factor sign-in is ported.
		res.set_header("Cache-Control", "no-store");
		api::err(res, -8, "the login expired; sign in again");
	});
	r.add("GET", "/api/auth/password", guarded(password_updated));
	r.add("GET", "/api/auth/account", guarded(account));
	r.add("POST", "/api/auth/password", guarded(change_password));
	r.add("POST", "/api/auth/logout", guarded(logout));
	r.add("GET", "/api/auth/idle", guarded([](Request &req, Response &res) { api::ok(res, idle_answer(req, false)); }));
	r.add("POST", "/api/auth/activity", guarded([](Request &req, Response &res) { api::ok(res, idle_answer(req, true)); }));

	Guard admin{{"admin"}};
	Guard admin_recent{{"admin"}, true};
	r.add("POST", "/api/auth/idle", guarded(set_idle, admin));
	r.add("GET", "/api/auth/users", guarded(list_users, admin));
	r.add("POST", "/api/auth/users", guarded(create_user, admin_recent));
	r.add("PUT", "/api/auth/users/:username", guarded(update_user, admin_recent));
	r.add("DELETE", "/api/auth/users/:username", guarded(delete_user, admin_recent));
	r.add("POST", "/api/auth/users/:username/password", guarded(change_user_password_route, admin_recent));
}

} // namespace nkos::auth
