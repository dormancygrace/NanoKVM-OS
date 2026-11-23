#pragma once

// webrtc.sock: the daemon's WebRTC data-channel transport (the Go server's service/rustdesk webrtc.go). The daemon
// sends an offer (a webrtc:// endpoint) and gets the answer, the DTLS fingerprints and a token; with the token it
// attaches a second connection that carries RustDesk messages over the data channel.

#include <memory>
#include <string>

namespace nkos::rustdesk {

struct RtcBridge {
	virtual ~RtcBridge() = default;
	// Ends the listener (its socket file goes), the connections and every session.
	virtual void close() = 0;
};

// Serves the listening descriptor fd (the socket at path).
std::unique_ptr<RtcBridge> start_rtc_bridge(int fd, const std::string &path);

} // namespace nkos::rustdesk
