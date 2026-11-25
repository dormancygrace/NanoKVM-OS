#include "auth/brute_force.hpp"
#include "notify/notify.hpp"

#include "auth/store.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"

#include <arpa/inet.h>
#include <chrono>
#include <cstring>
#include <deque>
#include <map>
#include <set>

namespace nkos::auth {

namespace {

using Clock = std::chrono::system_clock;
using TimePoint = Clock::time_point;

struct Attempt {
	int failures = 0;
	TimePoint last_failed{};
	TimePoint lockout_end{};
	std::string failed_user;
	bool other_failures = false;
};

constexpr size_t max_records = 3000;
constexpr auto stale_window = std::chrono::minutes(30);
constexpr auto slot_wait = std::chrono::seconds(10);
const char *const saturation_message = "Too many failed login attempts, please try again later";
const char *const in_flight_message = "Another login attempt is in progress, please try again";
const char *const locked_message = "Account locked due to too many failed attempts, please try again later";

std::map<std::string, Attempt> attempts;
std::set<std::string> in_flight;
struct Waiter {
	std::string key;
	TimePoint deadline;
	std::function<void(std::optional<Refusal>)> done;
	bool finished = false;
};
std::deque<std::shared_ptr<Waiter>> waiters;

bool expired(const Attempt &a, TimePoint now)
{
	if (a.lockout_end != TimePoint{}) return now >= a.lockout_end;
	return now - a.last_failed > stale_window;
}

bool reclaimable(TimePoint now)
{
	for (auto &[k, a] : attempts)
		if (now >= a.lockout_end) return true;
	return false;
}

bool evict_one()
{
	auto now = Clock::now();
	bool reclaimed = false;
	std::string oldest;
	TimePoint oldest_at{};
	for (auto it = attempts.begin(); it != attempts.end();) {
		if (expired(it->second, now)) {
			it = attempts.erase(it);
			reclaimed = true;
			continue;
		}
		if (it->second.lockout_end == TimePoint{} && (oldest.empty() || it->second.last_failed < oldest_at))
			oldest = it->first, oldest_at = it->second.last_failed;
		++it;
	}
	if (reclaimed) return true;
	if (!oldest.empty()) {
		attempts.erase(oldest);
		return true;
	}
	return false;
}

void admit(const std::shared_ptr<Waiter> &w)
{
	w->finished = true;
	in_flight.insert(w->key);
	if (auto refused = check_login_attempt(w->key)) {
		release_login(w->key);
		w->done(refused);
		return;
	}
	w->done(std::nullopt);
}

} // namespace

std::string login_key(const std::string &address)
{
	unsigned char b[16];
	if (inet_pton(AF_INET6, address.c_str(), b) != 1) return address;
	static const unsigned char mapped[12] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff};
	if (std::memcmp(b, mapped, 12) == 0) return address;  // Is4In6
	std::memset(b + 8, 0, 8);
	char text[INET6_ADDRSTRLEN];
	inet_ntop(AF_INET6, b, text, sizeof text);
	return std::string(text) + "/64";
}

std::optional<Refusal> check_login_attempt(const std::string &key)
{
	const auto &conf = config::get();
	if (conf.security.login_lockout_duration <= 0) return std::nullopt;
	auto now = Clock::now();
	auto it = attempts.find(key);
	if (it != attempts.end()) {
		Attempt &a = it->second;
		if (now < a.lockout_end) {
			log::debug("login blocked for IP {}: too many failed attempts", key);
			return Refusal{-5, locked_message};
		}
		if (a.lockout_end != TimePoint{}) {
			a.failures = 0;
			a.lockout_end = {};
			a.failed_user.clear();
			a.other_failures = false;
		}
	} else if (attempts.size() >= max_records && !reclaimable(now)) {
		return Refusal{-5, saturation_message};
	}
	return std::nullopt;
}

void begin_login_attempt(const std::string &key, std::function<void(std::optional<Refusal>)> done)
{
	auto w = std::make_shared<Waiter>(Waiter{key, Clock::now() + slot_wait, std::move(done)});
	if (!in_flight.contains(key)) {
		admit(w);
		return;
	}
	waiters.push_back(w);
	loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(slot_wait), [w] {
		if (w->finished) return;
		w->finished = true;
		std::erase(waiters, w);
		w->done(Refusal{-5, in_flight_message});
	});
}

void release_login(const std::string &key)
{
	in_flight.erase(key);
	for (auto it = waiters.begin(); it != waiters.end(); ++it)
		if ((*it)->key == key && !(*it)->finished) {
			auto w = *it;
			waiters.erase(it);
			admit(w);
			return;
		}
}

std::optional<Refusal> record_login_failure(const std::string &key, const std::string &name)
{
	const auto &conf = config::get();
	if (conf.security.login_lockout_duration <= 0) return std::nullopt;
	auto now = Clock::now();
	auto it = attempts.find(key);
	if (it == attempts.end()) {
		if (attempts.size() >= max_records && !evict_one()) return Refusal{-5, saturation_message};
		it = attempts.emplace(key, Attempt{}).first;
	}
	Attempt &a = it->second;
	auto window = std::chrono::seconds(conf.security.login_lockout_duration);
	if (a.last_failed != TimePoint{} && now - a.last_failed > window) {
		a.failures = 0;
		a.failed_user.clear();
		a.other_failures = false;
	}
	std::string username = validate_username(name) ? "" : name;
	if (username.empty()) a.other_failures = true;
	else if (a.failures == 0) a.failed_user = username;
	else if (username != a.failed_user) a.other_failures = true;
	a.failures++;
	a.last_failed = now;
	if (a.failures >= conf.security.login_max_failures) {
		a.lockout_end = now + window;
		if (a.failures == conf.security.login_max_failures)
		{
			log::warn("login locked out for {}: {} failed attempts", key, a.failures);
			nkos::notify::login_locked_out(key, a.failures, (conf.security.login_lockout_duration + 59) / 60);
		}
	}
	return std::nullopt;
}

void clear_login_attempt(const std::string &key, const std::string &username)
{
	if (config::get().security.login_lockout_duration <= 0) return;
	auto it = attempts.find(key);
	if (it != attempts.end() && (it->second.other_failures || (it->second.failures > 0 && it->second.failed_user != username))) return;
	attempts.erase(key);
}

} // namespace nkos::auth
