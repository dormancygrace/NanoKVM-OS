#pragma once

// GET /api/vm/memory/status, POST /api/vm/memory/swap.

#include "http/router.hpp"

#include <optional>
#include <string>

namespace nkos::system {

void add_memory_routes(http::Router &router);
// GET /api/vm/memory/status's data, nullopt when the statistics cannot be read (blocking: file reads).
std::optional<std::string> memory_status_json();

} // namespace nkos::system
