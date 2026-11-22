#pragma once

#include "http/router.hpp"

namespace nkos::video::direct {

// GET /api/stream/video/direct (WebSocket).
void add_routes(http::Router &router);

} // namespace nkos::video::direct
