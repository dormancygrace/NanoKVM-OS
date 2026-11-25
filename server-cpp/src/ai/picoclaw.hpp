#pragma once

// PicoClaw, as the Go server's router/picoclaw.go and service/picoclaw: the AI agent runtime (/usr/bin/picoclaw,
// started by S96picoclaw) and what the server does for it. The web page sets the model (provider/model, API base,
// key: ~/.picoclaw/config.json and .security.yml), the agent profile (AGENT.md), whether the agent may use the
// NanoKVM's shell and files (host tools), lists and deletes its chat sessions, installs, starts and stops the
// runtime (wanted state in /etc/kvm/picoclaw-runtime.json, restored at start), and chats through a WebSocket relay
// to the gateway (ws://127.0.0.1:18790/pico/ws). The runtime calls back over loopback with the internal token
// (X-NanoKVM-Internal-Token): screenshots, HID actions, its MCP tools (kvm_screenshot, kvm_actions,
// kvm_screen_text) and image loading. Driving the computer needs the AI control mode picoclaw.
//
// The service is blocking code (files, commands, waits): its handlers run on the blocking pool; the WebSocket relay
// and timers come from picoclaw_routes.cpp through Platform.

#include "ai/hid_sink.hpp"
#include "ai/mcp.hpp"
#include "http/router.hpp"
#include "input/control.hpp"

#include <chrono>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace nkos::ai::picoclaw {

// Paths, variables so that the differential check can point them elsewhere.
inline std::string binary_path = "/usr/bin/picoclaw";
inline std::string cache_dir = "/root/.picoclaw-cache";
inline std::string etc_init_script = "/etc/init.d/S96picoclaw";
inline std::string kvmapp_script = "/kvmapp/system/init.d/S96picoclaw";
inline std::string runtime_intent_file = "/etc/kvm/picoclaw-runtime.json";
inline std::string internal_token_file = "/etc/kvm/.picoclaw_internal_token";
// The 30 s runtime check that the first status request starts (off in the differential check).
inline bool probe_loop = true;
inline constexpr const char *internal_token_header = "X-NanoKVM-Internal-Token";
inline constexpr const char *download_url = "https://github.com/sipeed/picoclaw/releases/latest/download/picoclaw_Linux_riscv64.tar.gz";

// config.GetPicoclawInternalToken: read or created (64 hex characters) at the first use.
bool internal_token(std::string &token, std::string &error);

// A link to the gateway: one side of the relay (upstream: the gateway, downstream: the web page).
struct Link {
	virtual ~Link() = default;
	// Sends a text message; false when the connection is gone.
	virtual bool send_text(const std::string &data) = 0;
	// Sends a close frame with code and reason and drops the connection.
	virtual void close(int code, const std::string &reason) = 0;
};

// The platform: devices and network as the service uses them (the server's in picoclaw_routes.cpp, fakes in the
// check).
struct Platform {
	HidSink *hid = nullptr;
	// One JPEG read (VisionReader.ReadMjpeg): result as kvm_vision.h (0 a picture, 5 unchanged, < 0 none).
	std::function<int(std::uint16_t width, std::uint16_t height, std::uint16_t quality, std::string &jpeg)> read_mjpeg;
	// The stream settings (common.GetScreen after CheckScreen): width, height and quality.
	std::function<void(std::uint16_t &width, std::uint16_t &height, std::uint16_t &quality)> screen;
	// AcquireHdmiCaptureLeaseForRead: a lease for a read; claim_fresh says whether the next picture must be dropped.
	// nullptr when cancelled.
	struct ReadLease {
		virtual ~ReadLease() = default;
		virtual bool claim_fresh() = 0;
	};
	std::function<std::unique_ptr<ReadLease>(const input::control::Cancel &cancel)> read_lease;
	// AcquireHdmiCaptureLease for a running task: released by the returned function.
	std::function<std::function<void()>()> task_lease;
	// A timer: fn runs after delay unless the returned cancel was called (time.AfterFunc).
	std::function<std::function<void()>(std::chrono::milliseconds delay, std::function<void()> fn)> after;
	// hid.ReleaseAllHIDStateBestEffort.
	std::function<std::string()> release_hid;
	// The screen text reader (MCP's).
	mcp::Devices *text = nullptr;
	// Commands (sh -c), with a timeout: exit code (-1 failed to run) and combined output.
	std::function<int(const std::string &command, std::chrono::milliseconds timeout, std::string &output, std::string &error)> run;
	// The gateway: a WebSocket to url with headers. Probe: 0 connected (and closed), else the HTTP status of a refused
	// handshake (-1 none) with error. Connect: the link, or nullptr with the status and error; on_message gets the
	// gateway's messages (binary: true), on_closed the end (close code, reason).
	std::function<int(const std::string &url, const std::string &token, std::chrono::milliseconds timeout, std::string &error)> probe;
	std::function<std::shared_ptr<Link>(const std::string &url, const std::string &token, std::chrono::milliseconds timeout,
	                                    std::function<void(const std::string &, bool)> on_message,
	                                    std::function<void(int, const std::string &)> on_closed, std::function<bool()> renew_lock, int &status, std::string &error)>
	    connect;
	// Something listens on host:port (the gateway's port taken by another program).
	std::function<bool(const std::string &host_port, std::chrono::milliseconds timeout)> port_in_use;
	// The server's HTTP port (config.yaml), for the MCP URL given to PicoClaw.
	int http_port = 80;
	// Unix milliseconds and a new UUID (messages to the gateway).
	std::function<long long()> now_ms;
	std::function<std::string()> uuid;
};
Platform &platform();

// Starts the restore of the wanted runtime state (NewService: in a thread of its own).
void start();

// --- Routes (http glue in picoclaw_routes.cpp; each runs on the blocking pool) ---

// Loopback, internal token.
void screenshot(http::Request &request, http::Response &response);
void actions(http::Request &request, http::Response &response);
void mcp_handler(http::Request &request, http::Response &response);
void load_image(http::Request &request, http::Response &response);
void get_runtime_session(http::Request &request, http::Response &response);
// Administrators.
void update_model_config(http::Request &request, http::Response &response);
void update_host_tools(http::Request &request, http::Response &response);
void update_agent_profile(http::Request &request, http::Response &response);
void list_sessions(http::Request &request, http::Response &response);
void get_session(http::Request &request, http::Response &response);
void delete_session(http::Request &request, http::Response &response);
void get_runtime_status(http::Request &request, http::Response &response);
void release_runtime_session(http::Request &request, http::Response &response);
void install_runtime(http::Request &request, http::Response &response);
void uninstall_runtime(http::Request &request, http::Response &response);
void start_runtime(http::Request &request, http::Response &response);
void stop_runtime(http::Request &request, http::Response &response);

// GET /api/picoclaw/gateway/ws: whether the relay may open (nullopt), or the refusal (status and JSON body).
struct Refusal {
	int status = 200;
	std::string body;
};
// Checks the mode and the session lock and registers the session (fast, on the loop thread): nullopt with the
// session's id, or the refusal.
std::optional<Refusal> admit_gateway(const std::string &requested, std::string &session_out);
// Dials the gateway for an admitted session (blocking); false when it is unavailable (the session is then closed
// with 4002).
bool connect_gateway(const std::string &session_id);
// Both (the refusal of a failed dial is an empty 200).
std::optional<Refusal> open_gateway(const std::string &requested, std::string &session_out);
// The web page's socket of an admitted session, before the dial (a failed dial closes it with 4002).
void set_downstream(const std::string &session_id, std::shared_ptr<Link> downstream);
// The downstream is up: messages flow both ways from now on.
void attach_downstream(const std::string &session_id, std::shared_ptr<Link> downstream);
// A message from the web page.
void downstream_message(const std::string &session_id, const std::string &data, bool binary);
// The web page went away (code 1000 when it closed normally).
void downstream_closed(const std::string &session_id, int code, const std::string &reason);
// The relay could not be opened after open_gateway succeeded (upgrade failed).
void abandon_gateway(const std::string &session_id);
// The configured relay settings: read timeout, ping interval, largest message (bytes).
void relay_settings(int &read_timeout_ms, int &ping_interval_ms, int &max_message_bytes);

// --- For the control mode and MCP (router/control.go, router/mcp.go) ---

std::string preempt_control_leases_for_mcp();
std::string stop_runtime_for_mcp();
int cancel_active_control_operations();
void preserve_runtime_for_chat_only(const std::string &source);
void publish_control_mode_changed(const input::control::ModeStatus &status, const std::string &source);
// RuntimeStatus as JSON (the control route's "runtime").
std::string runtime_status_json();
// A PicoClaw session holds the input: manual input yields (BlocksManualInput).
bool blocks_manual_input();
// SessionLock.Ensure: the session takes (or renews) the lock; false when another holds it.
bool take_session_lock(const std::string &session_id);

// The JSON of a ModeStatus of the control route ("data"/"control").
std::string mode_status_json(const input::control::ModeStatus &status);

// Forgets the service's state (the check starts each case afresh).
void reset_state();

} // namespace nkos::ai::picoclaw
