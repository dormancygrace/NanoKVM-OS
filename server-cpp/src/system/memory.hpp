#pragma once

// GET /api/vm/memory/status, POST /api/vm/memory/swap.

#include "http/router.hpp"

namespace nkos::system {

void add_memory_routes(http::Router &router);

} // namespace nkos::system
