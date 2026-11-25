#pragma once

// Outbound HTTP(S) with the system's libcurl (on nanokvm-openssl), as the Go server's net/http clients: redirects
// followed (at most 10; credentials only to the same host and never from HTTPS to HTTP), the system CA bundle,
// the proxy variables of the environment. Blocking: call it off the event loop (loop::blocking).

#include <chrono>
#include <cstdint>
#include <functional>
#include <optional>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace nkos::http_client {

struct Request {
	std::string method = "GET";
	std::string url;
	std::vector<std::pair<std::string, std::string>> headers;
	std::string body;
	std::chrono::milliseconds timeout{std::chrono::seconds(30)};  // the whole transfer
	std::chrono::milliseconds connect_timeout{std::chrono::seconds(15)};
	bool follow_redirects = true;
	// A Unix socket to connect to instead (the URL's host only names the request).
	std::string unix_socket;
	// Bytes of the response body kept in Response::body; more fails the request (too_large).
	std::size_t max_body = 16u << 20;
	// Receives the body instead of Response::body; false stops the transfer.
	std::function<bool(std::string_view)> sink;
	// Called once the headers are in, before any body: false stops the transfer.
	std::function<bool(int status, const std::vector<std::pair<std::string, std::string>> &headers)> on_headers;
	// Called with the bytes received so far and the expected total (-1 unknown); false stops the transfer.
	std::function<bool(std::int64_t now, std::int64_t total)> progress;
};

struct Response {
	int status = 0;                   // 0: no response (error says why)
	std::string body;
	std::vector<std::pair<std::string, std::string>> headers;  // of the last response, names lower-case
	std::string effective_url;
	std::string error;
	bool too_large = false;
	bool stopped = false;             // a callback stopped it
	bool ok() const { return status >= 200 && status < 300 && error.empty(); }
	std::optional<std::string> header(std::string_view name) const;
};

Response perform(const Request &request);

// A URL with its user information and query hidden, for logs (Go's URL.Redacted, query dropped).
std::string redacted(const std::string &url);

} // namespace nkos::http_client
