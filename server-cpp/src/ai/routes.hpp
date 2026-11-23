#pragma once

// The AI families on the server: router/control.go (/api/ai/control), router/mcp.go (/api/mcp) and
// router/picoclaw.go (/api/picoclaw), with the devices, the HID writes, the capture leases and the PicoClaw gateway
// relay they use.

#include "http/router.hpp"

namespace nkos::ai {

// The routes; also makes manual input yield to a PicoClaw session. Before http::run.
void add_routes(http::Router &router);

// The plain-HTTP listener's handler while HTTPS serves (http::on_plain_http): PicoClaw's loopback routes for a
// loopback caller with the internal token; false for anything else (redirected).
bool plain_http(const http::Request &request, http::Response &response);

// config.EnsurePicoclawInternalToken (main.go): the token PicoClaw calls back with exists. Before the listeners.
bool ensure_internal_token();

// picoclaw.NewService: the runtime state wanted at the last start is restored (in a thread of its own). Once the
// server listens.
void start();

} // namespace nkos::ai
