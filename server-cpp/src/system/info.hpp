#pragma once

// GET /api/vm/info and GET /api/vm/hardware.

#include "http/router.hpp"

namespace nkos::system {

void add_info_routes(http::Router &router);

} // namespace nkos::system
