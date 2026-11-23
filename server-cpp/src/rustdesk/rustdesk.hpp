#pragma once

// RustDesk remote access, as the Go server's router/rustdesk.go and service/rustdesk: the settings of the
// nanokvm-rustdesk daemon (/etc/nanokvm-rustdesk/config.json), its status, its OpenRC service and runlevel link, and
// the USB composition it needs. The daemon reaches the device's video, audio and input through the bridge's local
// sockets (/run/nanokvm-rustdesk/*.sock), which are open while RustDesk is on.

#include "http/router.hpp"

#include <functional>
#include <memory>
#include <string>

namespace nkos::rustdesk {

inline constexpr const char *package = "nanokvm-rustdesk";

// The daemon's side of the device: the local sockets (media, control, WebRTC, audio) and the input sessions of
// remote clients. start() opens them (an error text, or empty), stop() closes them, expire() ends idle input
// sessions; audio_enabled() is whether the device's audio is on.
struct Bridge {
	virtual ~Bridge() = default;
	virtual std::string start() = 0;
	virtual void stop() = 0;
	virtual void expire() {}
	virtual bool audio_enabled() = 0;
};

// The bridge in use (the default one refuses to start: see routes.cpp). Before start().
void set_bridge(std::shared_ptr<Bridge> bridge);
// The bridge to the daemon (bridge.cpp, webrtc_bridge.cpp): media.sock, control.sock, webrtc.sock, audio.sock.
std::shared_ptr<Bridge> make_bridge();

// The USB composition for RustDesk, as service/vm's EnableRemoteAccessUSB (its preset, with or without audio),
// EnsureRemoteAccessUSB (audio added while it runs) and RestoreUSBAfterRemoteAccess (the composition it replaced).
// Each returns an error text, or empty. Until set, each refuses with an error.
struct UsbHooks {
	std::function<std::string(bool with_audio)> enable;
	std::function<std::string(bool with_audio)> ensure;
	std::function<std::string()> restore;
};
void set_usb_hooks(UsbHooks hooks);

// GET /api/rustdesk/status, PUT /api/rustdesk/config, POST /api/rustdesk/:action (administrators).
void add_routes(http::Router &router);

// rustdeskRouter's background work: the bridge follows the setting every 2 s (Bridge.Monitor), and a configuration
// whose WebRTC option no longer matches the installed daemon is rewritten (Service.AcceptWebRTC). After
// switch_off_stale, where Go's router runs it.
void start();
void stop();

// apkpkg.Selected: whether /etc/apk/world names the package (with or without a version constraint, a repository
// tag or a file pin). An error text in error when the world cannot be read.
bool apk_selected(const std::string &name, std::string &error);

// SwitchOffStale: turns RustDesk off in its configuration (and nothing else) when the configuration says on but is
// left from an add-on that was removed: selected is false, or the service has no runlevel link. Error text or empty.
std::string switch_off_stale(bool selected);

} // namespace nkos::rustdesk
