#pragma once

// The CPU clock: GET/POST /api/vm/cpu-frequency, and the saved clock applied at start.

#include "http/router.hpp"

namespace nkos::system {

// Before the server listens (blocking: the driver is polled for up to 2 s per setting).
void apply_saved_cpu_frequency();
void add_cpufreq_routes(http::Router &router);

} // namespace nkos::system
