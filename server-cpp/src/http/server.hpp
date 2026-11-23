#pragma once

// The listeners, on one libwebsockets event loop: HTTPS (HTTP/2 and HTTP/1.1 by ALPN) and HTTP, which redirects to
// HTTPS (307, as the Go server) unless the configuration serves plain HTTP. TLS 1.3 prefers ChaCha20-Poly1305: on
// the C906 it costs less than AES-GCM (measured, .workspace/tasks/cpp-migration/bench/RESULTS.md).

#include "config/config.hpp"
#include "http/router.hpp"

#include <cstdint>
#include <functional>
#include <optional>
#include <string>

namespace nkos::http {

// Runs until SIGTERM/SIGINT; returns the exit status. on_ready runs on the loop thread once the listeners exist,
// on_stop when a signal ended the loop, before the connections are closed.
int run(const config::Config &config, const Router &router, std::function<void()> on_ready = {},
        std::function<void()> on_stop = {});

// The interface the request's connection arrived on, as the kernel saw it (IP_PKTINFO of its last packet), not the
// destination address the peer chose; nullopt when it cannot be read. Loop thread, during the request.
std::optional<std::string> ingress_interface(const Request &request);

// A descriptor on the event loop (an eventfd, a pipe, a pty). The server takes it over: lws closes it once the watch
// ends, by unwatch_fd or because the far end hung up (closed then runs, and the watch is gone). The callbacks run
// on the loop thread and may end their own watch. 0 is no watch (watch_fd failed: the fd stays the caller's).
// Loop thread, while the server runs.
using FdWatch = std::uintptr_t;
struct FdCallbacks {
	std::function<void()> readable;
	std::function<void()> writable;  // after want_writable_fd, once
	std::function<void()> closed;
};
FdWatch watch_fd(int fd, std::function<void()> on_readable);
FdWatch watch_fd(int fd, FdCallbacks callbacks);
// Stops reading (readable is not called) until resumed: the reader's consumer is full.
void pause_fd(FdWatch watch, bool paused);
void want_writable_fd(FdWatch watch);
// Ends the watch without calling closed; the descriptor is closed soon after, so the caller must not use it again.
// after_close runs once it is closed (a lock on the device is gone then).
void unwatch_fd(FdWatch watch, std::function<void()> after_close = {});

} // namespace nkos::http
