#pragma once

// The notification service, as the Go server's notify.Service: the stored settings, the queue of events (32;
// producers are never delayed, a full queue drops), repeats held back (the same message and values within a minute;
// lockouts once per 10 minutes, the next one counting the rest), deliveries with retries (after 10 s, 1 min, 5 min;
// at most 16 alive, 4 per channel, login events in a pool of their own), the failed sign-ins the page shows, the
// page's saves, previews, sign-ins and tests, and the addresses users logged in from. Any thread.

#include "notify/channels.hpp"
#include "notify/model.hpp"
#include "notify/oauth.hpp"

#include <array>
#include <condition_variable>
#include <deque>
#include <functional>
#include <map>
#include <mutex>
#include <string>
#include <thread>

namespace nkos::notify {

class Service {
public:
	Service(std::string path, std::string addresses_path);
	Service(const Service &) = delete;
	Service &operator=(const Service &) = delete;

	// Seams: the clock, a send (redacted errors), the wait before a retry (false: give up), how a delivery runs
	// (default: a thread of its own), the journal.
	std::function<Clock::time_point()> now = [] { return Clock::now(); };
	std::function<Result(const Channel &, const Rendered &, Delivery *)> send;
	std::function<bool(std::chrono::milliseconds)> sleep;
	std::function<void(std::function<void()>)> spawn;
	std::function<void(const Event &)> record;
	OAuth oauth;

	// Queues an event; a full queue drops it.
	void emit(Event e);
	// Whether an enabled channel takes events of kind.
	bool wants(std::string_view kind);

	json::Out status();
	// Configure: "" or the error the page shows.
	std::string configure(Settings settings);
	json::Out preview(const PreviewRequest &request, std::string &error);
	std::optional<FlowStart> start_sign_in(const ChannelUpdate &update, Error &error);
	std::optional<FlowResult> finish_sign_in(const std::string &id, const std::string &code, Error &error);
	Result test_channel(const ChannelUpdate &update);

	// LoginSucceeded's file: whether address is known for user (also when it could not be stored); error set when
	// the file could not be read or written.
	bool remember_address(const std::string &user, const std::string &address, std::string &error);

	// dispatch: hands an event to every enabled channel that takes its kind (the queue's thread runs it).
	void dispatch(Event e);
	// The deliveries alive of each channel, and of each pool (the host's events, login events).
	std::map<std::string, int> in_flight();
	std::array<int, 2> active();
	Config config();

private:
	std::string path_, addresses_path_;
	std::mutex mu_;
	Config config_;
	std::map<std::string, Clock::time_point> recent_;
	struct Held {
		Clock::time_point since{}, until{};
		int dropped = 0;
	};
	std::map<std::string, Held> held_;
	std::array<int, 2> active_{};
	std::map<std::string, int> in_flight_;
	std::map<std::string, std::string> auth_errors_;

	std::mutex queue_mu_;
	std::condition_variable queue_cv_;
	std::deque<Event> queue_;
	bool started_ = false;
	std::mutex addresses_mu_;

	bool replace_token(const std::string &id, const std::string &old, const std::string &next);
	void note_auth(const std::string &id, const Result &err);
	bool admit(const std::string &id, std::string_view kind);
	void release(const std::string &id, std::string_view kind);
	void deliver(Channel ch, Event e);
	std::optional<Channel> channel(const std::string &id, std::string_view kind);
	void drop_sign_ins(const Config &old, const Config &next);
	std::string resolve(const ChannelUpdate &update, const std::vector<Channel> &stored, Channel &out);
	Result test(const Channel &ch, const Template *templ);
	void run();
};

// Save: the settings to path (directory 0700, file 0600, atomically); "" or the error.
std::string save(const std::string &path, const Config &c);

} // namespace nkos::notify
