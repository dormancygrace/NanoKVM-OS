#pragma once

// The part of DMTF Redfish that power management tools use (fence_redfish, Ansible's community.general.redfish_*,
// Metal3), as the Go server's internal/bmc/redfish: the service root, one computer system with its power state and
// reset action, its chassis and manager, login sessions, and, when the settings allow it, the virtual drive as
// VirtualMedia, and the host's sensors while its agent reports them. Redfish has its own account (the BMC account)
// and checks it itself: HTTP Basic or a session's X-Auth-Token. Any thread; serve() blocks for power and media
// actions.

#include "bmc/config.hpp"

#include <chrono>
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace nkos::bmc::redfish {

struct Info {
	std::string uuid;              // RFC 4122 form
	std::string firmware_version;  // NanoKVM OS release
};

// What is in the virtual drive.
struct Medium {
	bool inserted = false;
	std::string image;  // where it comes from, never with credentials or a query
	bool cd = false;    // a CD/DVD drive (read-only); a writable disk otherwise
	std::string via;    // ConnectedVia: "URI", "Applet" (a browser's file) or "Oem" (network boot)
};

struct MediaRequest {
	std::string image, username, password;
	std::optional<bool> write_protected;
};

// An error of the virtual drive shown to the client: in_use when something else holds the drive.
struct MediaError {
	bool in_use = false;
	std::string text;
	explicit operator bool() const { return !text.empty(); }
};

inline constexpr const char *media_in_use = "the virtual drive is in use: eject its medium first";

struct Media {
	virtual ~Media() = default;
	// Empty on success, else the error.
	virtual std::string medium(Medium &out) = 0;
	// Returns when the computer has the medium (at most timeout).
	virtual MediaError insert(const MediaRequest &request, std::chrono::milliseconds timeout) = 0;
	virtual MediaError eject() = 0;
};

// One reading of a host's sensor.
struct Reading {
	std::string id;       // the same across reports and restarts
	std::string name;
	std::string kind;     // "temperature", "fan", ... in that kind's unit
	std::string context;  // PhysicalContext ("CPU"), "" when Redfish has none
	double value = 0;
};

struct Sensors {
	virtual ~Sensors() = default;
	// on: the feature is on (the resources exist); fresh: the agent reports (else no readings).
	virtual void readings(std::vector<Reading> &out, bool &fresh, bool &on) = 0;
};

struct Request {
	std::string method, path, body;
	std::string peer;  // the client's address, without port
	std::map<std::string, std::string> headers;  // lower-case names, first values
	std::string header(const std::string &lower_name) const
	{
		auto it = headers.find(lower_name);
		return it == headers.end() ? "" : it->second;
	}
};

struct Response {
	int status = 200;
	std::vector<std::pair<std::string, std::string>> headers;
	std::string body;
	void set(const std::string &name, const std::string &value);
};

class Handler {
public:
	Handler(std::string username, std::string password, Info info, std::unique_ptr<Power> power);

	Response serve(const Request &request);

	// Hear of a client locked out after failures failed logins, and of a login (each client once while it keeps
	// logging in), for the security events.
	std::function<void(const std::string &addr, int failures, std::chrono::seconds lock)> on_lockout;
	std::function<void(const std::string &user, const std::string &addr)> on_login;
	// The virtual drive, when the settings offer it.
	std::shared_ptr<Media> media;
	// The host's sensors.
	std::shared_ptr<Sensors> sensors;

	// For checks: the clock (nanoseconds) and the random source of session IDs and tokens.
	std::function<std::int64_t()> now;
	std::function<void(unsigned char *, std::size_t)> random;

private:
	struct Session {
		std::string id, token;
		std::int64_t last_used = 0;
	};
	struct Failures {
		int count = 0;
		std::int64_t since = 0;
		bool reported = false;
		std::uint64_t serial = 0;  // tells a reservation from a replacement record
	};

	std::string client(const Request &r) const;
	std::uint64_t begin_login(const std::string &addr);
	void login_succeeded(const std::string &user, const std::string &addr, std::uint64_t reservation);
	void login_failed(const std::string &addr, std::uint64_t reservation);
	bool locked_out(const std::string &addr);
	void expire();
	bool credentials_match(const std::string &user, const std::string &password) const;
	bool authenticate(const Request &r, Response &w);
	bool sensors_on();
	std::string random_hex(std::size_t n);

	void get(const Request &r, Response &w, const std::function<std::string()> &body);
	void reset(const Request &r, Response &w);
	void create_session(const Request &r, Response &w);
	void session(const Request &r, Response &w, const std::string &id);
	void media_route(const Request &r, Response &w, const std::string &base, const std::string &at);
	void media_action(const Request &r, Response &w, const std::function<MediaError()> &action);
	void insert_media(const Request &r, Response &w);
	void sensors_route(const Request &r, Response &w, const std::string &at);

	std::string system_json();
	std::string chassis_json();
	std::string manager_json();
	std::string medium_json(const std::string &base);
	std::string session_collection();

	std::string username_, password_;
	Info info_;
	std::unique_ptr<Power> power_;
	std::mutex mu_;
	std::map<std::string, Session> sessions_;  // by token
	std::map<std::string, Failures> failed_;
	std::map<std::string, bool> logged_in_;
	std::uint64_t serial_ = 0;
};

} // namespace nkos::bmc::redfish
