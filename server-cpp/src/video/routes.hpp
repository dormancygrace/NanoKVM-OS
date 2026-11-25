#pragma once

// The video routes: the device codec (/api/stream/state) and the Direct stream.

#include "http/router.hpp"

namespace nkos::video {

void add_routes(http::Router &router);

} // namespace nkos::video
