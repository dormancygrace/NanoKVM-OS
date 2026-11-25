#pragma once

// GET/POST /api/vm/oled (sleep), GET/POST/DELETE /api/vm/oled/layout, POST/DELETE /api/vm/oled/preview (admin).

#include "http/router.hpp"

namespace nkos::system {

void add_oled_routes(http::Router &router);

} // namespace nkos::system
