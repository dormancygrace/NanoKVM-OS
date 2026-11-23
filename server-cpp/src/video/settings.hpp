#pragma once

// The video settings pages: GET /api/vm/video/capabilities (choices and limits of the device and its monitor) and
// GET /api/vm/screen (the stream settings with what the capture measures). The device transport
// (/kvmapp/kvm/transport: direct, webrtc or mjpeg; direct when unset).

#include "http/router.hpp"

#include <string>

namespace nkos::video {

std::string stream_transport();
void add_settings_routes(http::Router &router);

} // namespace nkos::video
