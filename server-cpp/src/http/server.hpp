#pragma once

// The listeners, on one libwebsockets event loop: HTTPS (HTTP/2 and HTTP/1.1 by ALPN) and HTTP, which redirects to
// HTTPS (307, as the Go server) unless the configuration serves plain HTTP. TLS 1.3 prefers ChaCha20-Poly1305: on
// the C906 it costs less than AES-GCM (measured, .workspace/tasks/cpp-migration/bench/RESULTS.md).

#include "config/config.hpp"
#include "http/router.hpp"

namespace nkos::http {

// Runs until SIGTERM/SIGINT; returns the exit status.
int run(const config::Config &config, const Router &router);

} // namespace nkos::http
