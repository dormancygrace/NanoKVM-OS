#pragma once

// Network standby in the server, as the Go server's router/standby.go: the admission gate that closes finite writes
// while a shutdown is prepared, the shutdown route (prepare, commit, cancel) that stages the RAM helper
// (/usr/sbin/nkos-standby) and hands init over to it, and the standby page served by the server until then.

#include "http/router.hpp"
#include "standby/core.hpp"

#include <chrono>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

namespace nkos::standby {

// standbyAdmission: right after the USB host gate, before every other middleware and route. Mutating requests are
// counted until their response is final; streams (WebSocket upgrades, MJPEG) only need the gate open.
void add_admission(http::Router &router);

// standbyRouter: drops a stale preparation of an earlier process, then GET /standby, /standby.js, /standby/status,
// POST /standby/start and POST /api/vm/system/shutdown (administrators). Right after add_admission.
void add_routes(http::Router &router);

// Pending: why package operations must wait (a shutdown is being prepared), or "".
Error pending();

// --- for the differential check ---------------------------------------------------------------------------------

class Gate {
public:
	bool enter(bool mutating);
	bool enter_stream();
	void leave(bool mutating);
	// Closes admission, then waits for the running mutating requests (30 s); sweep runs at every check.
	Error begin(const std::function<void()> &sweep = {});
	void cancel();
	int active();

private:
	std::mutex mu_;
	int active_ = 0;
	bool closed_ = false;
};
Gate &requests();
extern std::chrono::milliseconds begin_wait;
// What the shutdown route's Begin runs at each check: a sweep of requests closed before their answer.
extern std::function<void()> begin_sweep;

// The shutdown route behind its guards: the request as it reads it.
struct ShutdownCall {
	std::string body, host, client, user, proto;
	bool tls = false;
	long long http_port = 80, https_port = 443;
	std::string cert_pem, key_pem;
};
void answer_shutdown(const ShutdownCall &call, http::Response &out);
// The standby page as served now (Go's handler variable); the request as the page's handler reads it.
Reply serve_page(const HttpIn &in);
// middleware.RejectCrossOriginWrites for a write: true when the request is refused.
bool cross_origin_write(const http::Request &req);

} // namespace nkos::standby
