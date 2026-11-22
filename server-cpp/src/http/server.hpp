#pragma once

// The listeners, on one libwebsockets event loop: HTTPS (HTTP/2 and HTTP/1.1 by ALPN) and HTTP, which redirects to
// HTTPS (307, as the Go server) unless the configuration serves plain HTTP. TLS 1.3 prefers ChaCha20-Poly1305: on
// the C906 it costs less than AES-GCM (measured, .workspace/tasks/cpp-migration/bench/RESULTS.md).

#include "config/config.hpp"
#include "http/router.hpp"

#include <functional>

namespace nkos::http {

// Runs until SIGTERM/SIGINT; returns the exit status. on_ready runs on the loop thread once the listeners exist,
// on_stop when a signal ended the loop, before the connections are closed.
int run(const config::Config &config, const Router &router, std::function<void()> on_ready = {},
        std::function<void()> on_stop = {});

// Calls on_readable on the loop thread whenever fd (an eventfd or pipe owned by the caller, kept open while the
// server runs) is readable. Loop thread, while the server runs.
bool watch_fd(int fd, std::function<void()> on_readable);

} // namespace nkos::http
