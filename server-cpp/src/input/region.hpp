#pragma once

// The part of the picture that absolute pointer input maps to (a remote desktop inside the HDMI picture, a VM
// window), as the Go server's service/vm/input_region.go: off, auto, or manual with saved original resolutions and
// manual regions, one of them selected. /etc/kvm/input-region.json.

#include "http/router.hpp"

namespace nkos::input {

// GET/POST /api/vm/input-region.
void add_region_routes(http::Router &router);

} // namespace nkos::input
