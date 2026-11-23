#include "notify/service.hpp"

#include "common/log.hpp"
#include "config/config.hpp"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <format>
#include <fstream>
#include <iterator>
#include <sys/random.h>
#include <sys/stat.h>

namespace nkos::notify {

namespace {

constexpr std::size_t queue_size = 32;
// An event with the same message and values within this time is dropped: a producer that repeats itself must not
// flood a chat.
constexpr auto repeat_window = std::chrono::minutes(1);
// Deliveries alive at once (each pool), and of one channel; login events leave a channel's last slot to the host's.
constexpr int max_deliveries = static_cast<int>(max_channels), max_channel_deliveries = 4;
constexpr std::size_t max_addresses = 32;
// The waits before the second, third and fourth attempts.
constexpr std::array<std::chrono::milliseconds, 3> retry_delays = {std::chrono::seconds(10), std::chrono::minutes(1),
                                                                   std::chrono::minutes(5)};

// The messages an outsider can repeat at will, with the time in which only one of them is sent.
std::optional<std::chrono::minutes> coalesced(const std::string &message)
{
	if (message == "security.lockout" || message == "security.attempt-limit") return std::chrono::minutes(10);
	return std::nullopt;
}

// The messages of the host's states, paired.
std::string opposite(const std::string &message)
{
	if (message == "power.on") return "power.off";
	if (message == "power.off") return "power.on";
	if (message == "hdmi.lost") return "hdmi.restored";
	if (message == "hdmi.restored") return "hdmi.lost";
	return "";
}

// fmt.Sprint(message, vars): the key of a repeat.
std::string repeat_key(const std::string &message, const std::map<std::string, std::string> &vars)
{
	std::string key = message + "map[";
	bool first = true;
	for (const auto &[k, v] : vars) {
		key += (first ? "" : " ") + k + ":" + v;
		first = false;
	}
	return key + "]";
}

int pool(std::string_view kind) { return kind == kind_security ? 1 : 0; }

int random_port()
{
	std::uint32_t v = 0;
	while (getrandom(&v, sizeof v, 0) != sizeof v) {
	}
	return static_cast<int>(v % 16383);
}

} // namespace

std::string save(const std::string &path, const Config &c)
{
	std::string error;
	std::string data = config_bytes(c, error);
	if (!error.empty()) return error;
	std::string dir = path.substr(0, path.rfind('/'));
	if (::mkdir(dir.c_str(), 0700) != 0 && errno != EEXIST) return "mkdir " + dir + ": " + std::strerror(errno);
	if (!config::atomic_write(path, data, 0600)) return "write " + path + ": " + std::strerror(errno);
	return "";
}

Service::Service(std::string path, std::string addresses_path)
	: path_(std::move(path)), addresses_path_(std::move(addresses_path))
{
	sleep = [](std::chrono::milliseconds d) {
		std::this_thread::sleep_for(d);
		return true;
	};
	spawn = [](std::function<void()> fn) { std::thread(std::move(fn)).detach(); };
	std::string error;
	config_ = load(path_, error);
	if (!error.empty()) log::error("notifications: {}", error);
	oauth.random_port = random_port;
	oauth.persist = [this](const std::string &id, const std::string &old, const std::string &next) {
		return replace_token(id, old, next);
	};
}

Config Service::config()
{
	std::lock_guard lock(mu_);
	return config_;
}

// replaceToken: stores a renewed refresh token in the channel of id if it still holds old. A failed save keeps the
// old token, which stays valid for a while.
bool Service::replace_token(const std::string &id, const std::string &old, const std::string &next_token)
{
	std::lock_guard lock(mu_);
	if (!config_.channels) return false;
	for (std::size_t i = 0; i < config_.channels->size(); i++) {
		const Channel &ch = (*config_.channels)[i];
		if (ch.id != id || ch.get("oauthToken") != old) continue;
		Config next = config_;
		auto &settings = (*next.channels)[i].settings;
		if (!settings) settings.emplace();
		(*settings)["oauthToken"] = next_token;
		if (auto e = save(path_, next); !e.empty()) {
			log::warn("notifications: keeping a renewed sign-in: {}", e);
			return false;
		}
		config_ = std::move(next);
		return true;
	}
	return false;
}

void Service::note_auth(const std::string &id, const Result &err)
{
	if (id.empty()) return;
	std::lock_guard lock(mu_);
	if (!err) auth_errors_.erase(id);
	else if (err->auth) auth_errors_[id] = err->text;
}

void Service::emit(Event e)
{
	if (e.time == Clock::time_point{}) e.time = now();
	std::lock_guard lock(queue_mu_);
	if (!started_) {
		started_ = true;
		std::thread([this] { run(); }).detach();
	}
	if (queue_.size() >= queue_size) {
		log::warn("notifications: queue full, dropped {}", e.message);
		return;
	}
	queue_.push_back(std::move(e));
	queue_cv_.notify_one();
}

void Service::run()
{
	for (;;) {
		Event e;
		{
			std::unique_lock lock(queue_mu_);
			queue_cv_.wait(lock, [this] { return !queue_.empty(); });
			e = std::move(queue_.front());
			queue_.pop_front();
		}
		dispatch(std::move(e));
	}
}

void Service::dispatch(Event e)
{
	Config config;
	bool repeated = false, held = false;
	{
		std::lock_guard lock(mu_);
		config = config_;
		std::string key = repeat_key(e.message, e.vars);
		auto t = now();
		std::erase_if(recent_, [&](const auto &r) { return t - r.second >= repeat_window; });
		repeated = recent_.contains(key);
		if (auto window = coalesced(e.message); window && !repeated) {
			// Each address an attacker gets locked out would be a message of its own: one goes out per window, the
			// next one counts the rest.
			Held &c = held_[e.message];
			held = t < c.until;
			if (held) {
				c.dropped++;
			} else {
				if (c.dropped > 0)
					e.details = std::format("{} more addresses blocked since {}.", c.dropped, gostd::format_clock(c.since));
				c = Held{t, t + *window, 0};
			}
		}
		if (!repeated && !held) {
			// A state change ends the repeats of the state before it.
			if (auto before = opposite(e.message); !before.empty()) recent_.erase(repeat_key(before, e.vars));
			recent_[key] = t;
		}
	}
	if (repeated || held) {
		log::debug("notifications: repeated {} held back", e.message);
		return;
	}
	// The journal gets what the channels would, whether or not a channel takes it: after the repeats are held
	// back, so that an outsider's logins cannot fill it.
	if (record && e.kind != kind_test) record(e);
	if (!config.channels) return;
	for (const auto &ch : *config.channels) {
		if (!ch.enabled || !ch.wants(e.kind)) continue;
		if (!admit(ch.id, e.kind)) {
			log::warn("notifications: {} \"{}\" is behind, dropped {}", ch.type, ch.name, e.message);
			continue;
		}
		spawn([this, ch, e] { deliver(ch, e); });
	}
}

bool Service::admit(const std::string &id, std::string_view kind)
{
	std::lock_guard lock(mu_);
	int limit = max_channel_deliveries - (kind == kind_security ? 1 : 0);
	if (active_[pool(kind)] >= max_deliveries || in_flight_[id] >= limit) {
		if (in_flight_[id] == 0) in_flight_.erase(id);
		return false;
	}
	active_[pool(kind)]++;
	in_flight_[id]++;
	return true;
}

void Service::release(const std::string &id, std::string_view kind)
{
	std::lock_guard lock(mu_);
	active_[pool(kind)]--;
	if (--in_flight_[id] <= 0) in_flight_.erase(id);
}

std::map<std::string, int> Service::in_flight()
{
	std::lock_guard lock(mu_);
	return in_flight_;
}

std::array<int, 2> Service::active()
{
	std::lock_guard lock(mu_);
	return active_;
}

std::optional<Channel> Service::channel(const std::string &id, std::string_view kind)
{
	std::lock_guard lock(mu_);
	if (config_.channels)
		for (const auto &ch : *config_.channels)
			if (ch.id == id && ch.enabled && ch.wants(kind)) return ch;
	return std::nullopt;
}

void Service::deliver(Channel ch, Event e)
{
	Delivery d{new_id()};
	for (std::size_t attempt = 0;; attempt++) {
		// By the settings of now: a retry after the channel changed type is escaped for its new type.
		Rendered r;
		{
			std::lock_guard lock(mu_);
			r = render(e, config_.language, config_.find_template(ch.templ), markup(ch.type));
		}
		Result err = send(ch, r, &d);
		if (!err) {
			note_auth(ch.id, err);
			break;
		}
		if (err->permanent || attempt >= retry_delays.size()) {
			log::warn("notifications: {} \"{}\": {}", ch.type, ch.name, err->text);
			note_auth(ch.id, err);
			break;
		}
		log::debug("notifications: {} \"{}\": {}; retrying", ch.type, ch.name, err->text);
		if (!sleep(retry_delays[attempt])) break;
		// The channel may have been deleted, turned off or changed meanwhile.
		auto now_ch = channel(ch.id, e.kind);
		if (!now_ch) break;
		ch = std::move(*now_ch);
	}
	release(ch.id, e.kind);
}

bool Service::wants(std::string_view kind)
{
	std::lock_guard lock(mu_);
	if (config_.channels)
		for (const auto &ch : *config_.channels)
			if (ch.enabled && ch.wants(kind)) return true;
	return false;
}

json::Out Service::status()
{
	std::lock_guard lock(mu_);
	return notify::status(config_, auth_errors_);
}

json::Out Service::preview(const PreviewRequest &request, std::string &error)
{
	return notify::preview(request, now(), error);
}

std::string Service::resolve(const ChannelUpdate &update, const std::vector<Channel> &stored, Channel &ch)
{
	ch = with_stored_secrets(update.channel, stored);
	if (update.clear)
		for (const auto &key : *update.clear) ch.settings->erase(key);
	if (auto e = oauth.claim(ch); !e.empty()) return e;
	if (ch.id.empty()) ch.id = new_id();
	if (auto e = validate_channel(ch); !e.empty()) return ch.name.empty() ? e : ch.name + ": " + e;
	return "";
}

std::string Service::configure(Settings settings)
{
	std::lock_guard lock(mu_);
	Config next;
	next.language = settings.language;
	next.channels.emplace();
	next.templates = settings.templates ? std::move(settings.templates) : std::vector<Template>{};
	std::vector<Channel> stored = config_.channels ? *config_.channels : std::vector<Channel>{};
	if (settings.channels) {
		for (const auto &update : *settings.channels) {
			Channel ch;
			if (auto e = resolve(update, stored, ch); !e.empty()) {
				std::string name = update.channel.name.empty() ? update.channel.type : update.channel.name;
				return name + ": " + e;
			}
			next.channels->push_back(std::move(ch));
		}
	}
	if (auto e = validate(next); !e.empty()) return e;
	if (auto e = save(path_, next); !e.empty()) return e;
	drop_sign_ins(config_, next);
	config_ = std::move(next);
	return "";
}

// dropSignIns: forgets the failures of the sign-ins the new settings no longer hold and revokes the Google ones that
// end, only when no channel keeps a Google sign-in (Google revokes the account's grant to the whole Cloud project).
// Holds mu_.
void Service::drop_sign_ins(const Config &old, const Config &next)
{
	std::map<std::string, std::string> tokens;
	bool google_kept = false;
	Error unused;
	if (next.channels)
		for (const auto &ch : *next.channels) {
			tokens[ch.id] = ch.get("oauthToken");
			auto client = client_of(ch, unused);
			if (client && !tokens[ch.id].empty() && client->kind == oauth_google) google_kept = true;
		}
	if (!old.channels) return;
	for (const auto &ch : *old.channels) {
		std::string token = ch.get("oauthToken");
		auto it = tokens.find(ch.id);
		if (token.empty() || (it != tokens.end() && it->second == token)) continue;
		auth_errors_.erase(ch.id);
		auto client = client_of(ch, unused);
		if (client && client->kind == oauth_google && !google_kept && oauth.revoke_later) oauth.revoke_later(*client, token);
	}
}

std::optional<FlowStart> Service::start_sign_in(const ChannelUpdate &update, Error &error)
{
	std::vector<Channel> stored;
	{
		std::lock_guard lock(mu_);
		if (config_.channels) stored = *config_.channels;
	}
	if (update.channel.type != "email") {
		error = Error{"only an e-mail channel signs in"};
		return std::nullopt;
	}
	return oauth.start(with_stored_secrets(update.channel, stored), error);
}

std::optional<FlowResult> Service::finish_sign_in(const std::string &id, const std::string &code, Error &error)
{
	return oauth.finish(id, code, error);
}

Result Service::test(const Channel &ch, const Template *templ)
{
	std::string language;
	{
		std::lock_guard lock(mu_);
		language = config_.language;
	}
	Event e;
	e.kind = std::string(kind_test);
	e.message = "test";
	e.time = now();
	return send(ch, render(e, language, templ), nullptr);
}

Result Service::test_channel(const ChannelUpdate &update)
{
	std::vector<Channel> stored;
	std::optional<Template> templ;
	{
		std::lock_guard lock(mu_);
		if (config_.channels) stored = *config_.channels;
		if (const Template *t = config_.find_template(update.channel.templ)) templ = *t;
	}
	// A new channel gets an id only to be checked: its sign-in failures are not kept.
	bool saved = !update.channel.id.empty();
	Channel ch;
	if (auto e = resolve(update, stored, ch); !e.empty()) return Error{e};
	if (!saved) ch.id.clear();
	if (update.template_draft) {
		Template copied = *update.template_draft;
		if (auto e = validate_template(copied); !e.empty()) return Error{e};
		templ = std::move(copied);
	}
	Result err = test(ch, templ ? &*templ : nullptr);
	note_auth(ch.id, err);
	return err;
}

bool Service::remember_address(const std::string &user, const std::string &address, std::string &error)
{
	std::lock_guard lock(addresses_mu_);
	error.clear();
	std::map<std::string, std::optional<std::vector<std::string>>> lists;
	std::string data;
	{
		std::ifstream in(addresses_path_, std::ios::binary);
		if (!in) {
			if (errno != ENOENT) {
				error = "open " + addresses_path_ + ": " + std::strerror(errno);
				return true;
			}
		} else {
			data.assign(std::istreambuf_iterator<char>(in), std::istreambuf_iterator<char>());
		}
	}
	if (!data.empty()) {
		json::Value v;
		bool ok = json::unmarshal(data, v) && (v.is_null() || v.kind == json::Value::Kind::object);
		if (ok && v.kind == json::Value::Kind::object) {
			for (const auto &[k, x] : v.members) {
				std::optional<std::vector<std::string>> list;
				if (!json::get(x, list)) {
					ok = false;
					break;
				}
				lists[k] = std::move(list);
			}
		}
		if (!ok) {
			log::warn("notifications: {} unreadable, starting afresh", addresses_path_);
			lists.clear();
		}
	}
	auto it = lists.find(user);
	bool seen = it != lists.end();
	std::vector<std::string> list = seen && it->second ? *it->second : std::vector<std::string>{};
	bool known = false;
	if (auto at = std::find(list.begin(), list.end(), address); at != list.end()) {
		// An address in use moves to the newest end; only one in the older half moves, so logins switching between
		// a few addresses do not write each time.
		std::size_t i = static_cast<std::size_t>(at - list.begin());
		if (static_cast<long>(i) >= static_cast<long>(list.size()) - static_cast<long>(max_addresses / 2)) return true;
		list.erase(at);
		known = true;
	}
	list.push_back(address);
	if (list.size() > max_addresses) list.erase(list.begin(), list.end() - max_addresses);
	lists[user] = list;
	known = known || !seen;
	json::Out out = json::Out::object();
	for (const auto &[k, l] : lists) out.set(k, json::Out::strings(l));
	std::string bytes = json::marshal(out);
	std::string dir = addresses_path_.substr(0, addresses_path_.rfind('/'));
	if (::mkdir(dir.c_str(), 0700) != 0 && errno != EEXIST) {
		error = "mkdir " + dir + ": " + std::strerror(errno);
		return known;
	}
	if (!config::atomic_write(addresses_path_, bytes, 0600)) error = "write " + addresses_path_ + ": " + std::strerror(errno);
	return known;
}

} // namespace nkos::notify
