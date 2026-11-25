#pragma once

// GET /api/vm/bluetooth, POST /api/vm/bluetooth/enable and /disable (admin).

#include "http/router.hpp"

namespace nkos::system {

void add_bluetooth_routes(http::Router &router);

} // namespace nkos::system
