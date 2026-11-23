#pragma once

// GET /api/storage/image, /api/storage/image/mounted, /api/storage/cdrom, /api/download/image/enabled (admin).

#include "http/router.hpp"

namespace nkos::usb {

void add_storage_read_routes(http::Router &router);

} // namespace nkos::usb
