#pragma once

// The device stream over RTSP, for players and recorders that open a URL (OBS, VLC, ffmpeg): rtsp://host:port/stream
// with the user "stream" and a generated password (Digest authentication). Off by default; an administrator turns
// it on and sets the port in /etc/kvm/rtsp.json. The server is live555 on a thread of its own; every client
// shares one subscription to the device stream (source.hpp), at the device codec, with RTP over the RTSP connection.

#include "http/router.hpp"

namespace nkos::video::rtsp {

// Starts the RTSP server when it is on. Loop thread, once the capture runs.
void start();
// Stops it and closes its clients. Loop thread.
void stop();

// GET and POST /api/stream/rtsp (administrators).
void add_routes(http::Router &router);

} // namespace nkos::video::rtsp
