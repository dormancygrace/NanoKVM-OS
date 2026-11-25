#pragma once

// The web interface's branding, as the Go server's service/branding: a custom logo and favicon
// (/etc/kvm/branding/*.png), the button color, the banner style, the start page (overview or screen; else the
// image's default from /boot/image.json). Uploading a new logo or favicon is not here yet: it decodes and re-encodes
// the image.

#include "http/router.hpp"

namespace nkos::system {

void add_branding_routes(http::Router &router);

} // namespace nkos::system
