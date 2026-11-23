#pragma once

// GET /api/vm/dashboard: system telemetry, memory, board revision, application version.

#include "http/router.hpp"

namespace nkos::system {

// Starts the once-a-second CPU sampler (loop thread, after the server runs).
void start_dashboard();
void add_dashboard_routes(http::Router &router);

} // namespace nkos::system
