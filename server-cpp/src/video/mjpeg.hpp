#pragma once

// MJPEG, as the Go server's service/stream/mjpeg: one capture loop for every viewer at the screen's rate (30 fps at
// most beside an H.26x stream). Classic viewers get a multipart/x-mixed-replace stream of whole JPEGs; tiled viewers
// get, over a WebSocket, messages of the tiles that changed (the first and every 5 s a whole picture). Identical
// pictures are not sent again (except every 5 s); a slow viewer gets only the newest picture; the JPEG quality
// steps down while viewers fall behind and climbs back while they keep up. MJPEG is refused while the HDMI input is
// 3840x2160. Loop thread.

#include "http/router.hpp"

namespace nkos::video::mjpeg {

// GET /api/stream/mjpeg, GET /api/stream/mjpeg/tiles (WebSocket), POST /api/stream/mjpeg/detect (admin),
// POST /api/stream/mjpeg/detect/stop.
void add_routes(http::Router &router);

} // namespace nkos::video::mjpeg
