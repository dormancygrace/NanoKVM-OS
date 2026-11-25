#pragma once

// The video settings pages: GET /api/vm/video/capabilities (choices and limits of the device and its monitor) and
// GET /api/vm/screen (the stream settings with what the capture measures). The device transport
// (/kvmapp/kvm/transport: direct, webrtc or mjpeg; direct when unset).

#include "http/router.hpp"

#include <string>

namespace nkos::video {

std::string stream_transport();
// GET /api/vm/video/capabilities as JSON (also the answer of POST /api/vm/video).
std::string video_capabilities_json();
void add_settings_routes(http::Router &router);
// POST /api/vm/screen, POST /api/vm/video (setters.cpp).
void add_setter_routes(http::Router &router);

} // namespace nkos::video
