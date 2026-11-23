#pragma once

// IPMI v2.0 over LAN (RMCP+, ipmitool's "lanplus") for power control, as the Go server's internal/ipmi: enough for
// `ipmitool chassis power status|on|off|cycle|reset|soft` and fence_ipmilan. Cipher suites 3 (RAKP-HMAC-SHA1,
// HMAC-SHA1-96, AES-CBC-128) and 17 (RAKP-HMAC-SHA256, HMAC-SHA256-128, AES-CBC-128); IPMI v1.5 sessions and
// suites without authentication, integrity or confidentiality are not served. RMCP+ hands a hash of the password to
// anyone who asks to log in (RAKP message 2): IPMI stays off unless the user turns it on.

#include "bmc/config.hpp"

#include <array>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <deque>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

namespace nkos::bmc::ipmi {

using Bytes = std::vector<std::uint8_t>;

// A login or a lockout, for the security notifications.
struct Event {
	std::string kind;  // "login" or "lockout"
	std::string user;  // the IPMI user name of a login
	std::string address;
	int attempts = 0, minutes = 0;  // a lockout: the logins counted and how long further ones are refused
};

// What the settings page shows of the running service.
struct Stats {
	int sessions = 0;   // established, unexpired sessions
	int locked_out = 0; // clients refused for too many logins
	std::string last_command;  // the last power command accepted ("power on", ...)
	std::int64_t last_command_at = 0;  // wall-clock nanoseconds; 0 before the first
};

class Server {
public:
	Server();
	~Server();
	Server(const Server &) = delete;
	Server &operator=(const Server &) = delete;

	std::string username, password;
	std::array<std::uint8_t, 16> guid{};
	std::uint8_t firmware_major = 0, firmware_minor = 0;  // Get Device ID
	std::shared_ptr<Power> power;
	std::function<void(const std::string &)> log;
	std::function<void(const Event &)> on_event;

	// For checks: the clock (wall-clock nanoseconds) and the random source.
	std::function<std::int64_t()> now;
	std::function<void(std::uint8_t *, std::size_t)> random;

	// Answers one packet from addr ("1.2.3.4:623" or "[::1]:623"); empty: no answer.
	Bytes handle(const Bytes &packet, const std::string &addr);
	Stats stats();
	// No more power actions or events: the workers end after those already accepted; waits for the events.
	void finish();
	// For checks: waits until the accepted power actions and events have run.
	void drain();

private:
	struct Suite {
		std::uint8_t id, auth, integrity;
		bool sha256;
		std::size_t rakp4_len, integ_len;
		Bytes mac(const Bytes &key, std::initializer_list<const Bytes *> parts) const;
	};
	struct Session {
		std::uint32_t managed_id = 0, console_id = 0;
		const Suite *suite = nullptr;
		bool active = false;
		std::uint8_t max_priv = 0, priv = 0, role = 0;
		Bytes rm = Bytes(16), rc = Bytes(16);
		Bytes name;
		bool named = false;
		Bytes sik, k1, k2;
		std::uint32_t seq_out = 0, seq_in = 0, seen = 0;
		std::int64_t last_used = 0;
		std::string addr;
		bool accept_seq(std::uint32_t seq);
	};
	struct Failures {
		int count = 0;
		std::int64_t since = 0;
	};
	struct Request {
		std::uint8_t net_fn = 0, cmd = 0, rq_seq_lun = 0, rq_addr = 0, rs_lun = 0;
		Bytes data;
		Bytes reply(std::uint8_t cc, const Bytes &data = {}) const;
	};
	static bool parse_message(const Bytes &m, Request &out);
	static const Suite *suite_for(std::uint8_t auth, std::uint8_t integrity, std::uint8_t conf);
	static std::pair<std::uint8_t, Bytes> cipher_suites(const Bytes &data);

	Bytes v15(const Bytes &p);
	Bytes v20(const Bytes &p, const std::string &addr);
	Bytes pre_session(std::uint8_t type, const Bytes &payload);
	Bytes pre_session_message(const Bytes &p);
	Bytes open_session(const Bytes &p, const std::string &addr);
	Bytes rakp1(const Bytes &p, const std::string &addr);
	Bytes rakp3(const Bytes &p, const std::string &addr);
	Bytes message(Session &se, const Bytes &p, std::uint32_t seq, std::size_t length);
	Bytes sealed(Session &se, const Bytes &msg);
	Bytes command(Session &se, const Request &req);
	Bytes chassis_control(Session &se, const Request &req);
	bool encrypt(const Bytes &key, const Bytes &plain, Bytes &out);
	Bytes user_key() const;
	bool locked_out(const std::string &addr);
	void record_failure(const std::string &addr);
	void evict_oldest_failure();
	void expire_every(std::int64_t interval);
	void expire();
	std::uint32_t new_id();
	void event(const Event &e);
	void logf(const std::string &message);

	std::mutex mu_;
	std::int64_t last_expire_ = 0;
	std::map<std::uint32_t, Session> sessions_;
	std::map<std::string, Failures> failed_;
	std::string last_command_;
	std::int64_t last_command_at_ = 0;

	// The power actions (one running, one waiting) and the events: each handled in order by a thread of its own,
	// which outlives the server while an action still runs.
	struct Work;
	std::shared_ptr<Work> work_;
};

} // namespace nkos::bmc::ipmi
