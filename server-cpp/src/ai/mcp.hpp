#pragma once

// The MCP server of the Go server's router/mcp.go and service/mcp: POST /api/mcp speaks MCP (JSON-RPC 2.0 over the
// streamable HTTP transport, stateless, JSON answers) to external AI clients with the API key of /etc/kvm/mcp.json,
// while the AI control mode is mcp. Its tools type, press keys, move, click, drag and scroll the mouse, take
// screenshots, read the screen text and the host's state, and, while the administrator allows host control, press
// the power button, change the virtual disk's medium and send Wake-on-LAN. The answers are those of the Go MCP SDK
// (modelcontextprotocol/go-sdk with google/jsonschema-go) for the protocol versions up to 2025-11-25: the same
// status codes, messages, tool list, argument validation and results.
//
// This file is the protocol and the tools on top of Devices; mcp_routes.cpp connects them to the server.

#include "ai/hid_sink.hpp"
#include "http/router.hpp"
#include "input/control.hpp"

#include <functional>
#include <optional>
#include <string>
#include <vector>

namespace nkos::ai::mcp {

// --- /etc/kvm/mcp.json ---

inline std::string config_file = "/etc/kvm/mcp.json";

struct Config {
	std::string api_key;
	bool allow_host_control = false;
};
// loadConfig: an absent file is the empty config; error set otherwise.
bool load_config(Config &config, std::string &error);
// updateConfig with ensureAPIKey / regenerateAPIKey / the host control switch.
bool ensure_api_key(Config &config, std::string &error);
bool regenerate_api_key(Config &config, std::string &error);
// setHostControl: saved once the host control operations running are done.
bool set_host_control(bool allowed, Config &config, std::string &error);

// --- What the tools use ---

struct Snapshot {
	bool ok = false;
	int ret_code = 0;
	std::string message;
	int width = 0, height = 0;
	std::string jpeg;
};

struct ScreenBox {
	double x = 0, y = 0, w = 0, h = 0;
};
struct ScreenTextPiece {
	std::string text;
	double score = 0;
	ScreenBox box;
};
struct ScreenText {
	std::string language;
	int width = 0, height = 0;
	std::string text;
	std::vector<ScreenTextPiece> pieces;
	bool pieces_null = true;  // Go's nil slice (an empty reply has "pieces": [])
};

struct HostState {
	bool power_button = false, reset_button = false, known = false, on = false, disk_activity = false;
	std::string power_error;
	bool capture = false, signal = false;
	bool keys_known = false, caps_lock = false, num_lock = false, scroll_lock = false;
};

struct Devices {
	HidSink *hid = nullptr;
	// Snapshotter.Capture: quality 0 = default, timeout_ms nullopt = default. error set: the capture failed outright.
	std::function<Snapshot(int quality, std::optional<int> timeout_ms, std::string &error)> capture;
	// ScreenTextReader; available() false leaves screen_text out.
	std::function<bool()> text_available;
	std::function<std::vector<std::string>()> text_languages;
	std::function<bool(const std::string &language, const std::optional<ScreenBox> &area, ScreenText &out, std::string &error)> read_text;
	// HostStatusReader.
	std::function<HostState()> host_status;
	// HostControl: power runs a BMC power action ("On", "ForceOff", ...); an empty one leaves its tool out.
	std::function<std::string(const std::string &action)> power;
	std::function<std::vector<std::string>(std::string &error)> media_images;
	std::function<std::string(bool &cdrom, std::string &error)> media_mounted;
	std::function<std::string(const std::string &name, bool cdrom)> media_mount;
	std::function<std::string(const std::string &mac, const std::string &iface)> wake;
};

// --- The endpoint ---

struct HttpRequest {
	std::string method, body, host;
	// Request headers by name (case-insensitive lookup by the caller): first value, and all values.
	std::function<std::string(std::string_view)> header;
	std::function<std::vector<std::string>(std::string_view)> header_values;
	bool loopback = false;  // the connection came in on a loopback address
};

struct HttpResponse {
	int status = 200;
	std::string content_type, allow, cache_control, body;
};

// Any /api/mcp: the API key middleware, the cross-origin check and the MCP handler. Blocking (tools wait for the
// computer): never on the loop thread.
HttpResponse serve(const HttpRequest &request, Devices &devices);

// The management routes' data (GetMCPConfigRsp).
std::string config_json(const Config &config, const input::control::ModeStatus &status);

// --- The management routes (administrators; blocking: a switch waits for the operations running) ---

// What enabling MCP does to the other AI clients (router/mcp.go: PicoClaw's leases and runtime, the HID state) and
// who hears of the new mode. Each returns "" or its error.
struct Hooks {
	std::function<std::string()> preempt_picoclaw;
	std::function<std::string()> stop_picoclaw;
	std::function<std::string()> release_hid;
	std::function<void(const input::control::ModeStatus &)> on_control_change;
};
Hooks &hooks();

// GET /api/mcp/config, POST /api/mcp/config, POST /api/mcp/key/regenerate, POST /api/mcp/host-control.
void get_config(http::Request &request, http::Response &response);
void set_config(http::Request &request, http::Response &response);
void regenerate_key(http::Request &request, http::Response &response);
void set_host_control_route(http::Request &request, http::Response &response);

// The routes of router/mcp.go.
void add_routes(http::Router &router);

} // namespace nkos::ai::mcp
