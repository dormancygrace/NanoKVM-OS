#pragma once

// The WebRTC transport (GET /api/stream/video, a signalling WebSocket), as the Go server's service/stream/webrtc:
// the page offers a recvonly video (and audio) transceiver after the server sends "ice-servers"; the server answers
// with the device stream's codec (H.264 Main profile or H.265), trickles candidates both ways, and sends the shared
// H.264/H.265 stream to every connected, unpaused peer through its own writer (bounded queue, recovery from a key
// frame, key frame requests with backoff). libdatachannel (libjuice ICE, OpenSSL DTLS, libsrtp) carries the media.

#include "http/router.hpp"

namespace nkos::video::webrtc {

void add_routes(http::Router &router);

// Closes every connection (PeerConnections closed, writers joined) and cleans libdatachannel up. Once, on the loop
// thread, when the server stops: before the capture is released.
void stop();

} // namespace nkos::video::webrtc
