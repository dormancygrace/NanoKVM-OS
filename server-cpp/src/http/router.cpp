#include "http/router.hpp"

#include "common/loop.hpp"

#include <libwebsockets.h>

#include <algorithm>
#include <cctype>
#include <unordered_map>

namespace nkos::http {

namespace {

bool iequals(std::string_view a, std::string_view b)
{
	return a.size() == b.size() &&
	       std::equal(a.begin(), a.end(), b.begin(), [](char x, char y) { return std::tolower((unsigned char)x) == std::tolower((unsigned char)y); });
}

// The header names lws keeps as tokens, from lws itself (lws_token_to_string): lws stores such a header only under
// its token, so reading it as a custom header finds nothing (X-Auth-Token, Content-Range, ...). Any other name is a
// custom header. Pseudo-headers (":authority") and request-line tokens ("get ") are not header names.
const std::unordered_map<std::string, lws_token_indexes> &known_headers()
{
	static const auto map = [] {
		std::unordered_map<std::string, lws_token_indexes> m;
		for (int i = 0; i < WSI_TOKEN_COUNT; i++) {
			auto *s = reinterpret_cast<const char *>(lws_token_to_string(static_cast<lws_token_indexes>(i)));
			if (!s || !*s || *s == ':') continue;
			std::string name(s);
			if (name.find(' ') != std::string::npos) continue;
			if (!name.empty() && name.back() == ':') name.pop_back();
			for (auto &c : name) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
			m.emplace(std::move(name), static_cast<lws_token_indexes>(i));
		}
		return m;
	}();
	return map;
}

} // namespace

std::string Request::header(std::string_view name) const
{
	auto values = header_values(name);
	return values.empty() ? std::string() : values.front();
}

std::vector<std::string> Request::header_values(std::string_view name) const
{
	std::vector<std::string> out;
	if (!wsi) {
		for (const auto &[k, v] : header_copy)
			if (iequals(k, name)) out.push_back(v);
		return out;
	}
	std::string lower;
	for (char c : name) lower += static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	if (auto it = known_headers().find(lower); it != known_headers().end()) {
		lws_token_indexes index = it->second;
		// HTTP/2 carries the host as :authority.
		if (index == WSI_TOKEN_HOST && lws_hdr_total_length(wsi, WSI_TOKEN_HOST) <= 0) index = WSI_TOKEN_HTTP_COLON_AUTHORITY;
		int len = lws_hdr_total_length(wsi, index);
		if (len <= 0) return out;
		std::string v(static_cast<size_t>(len) + 1, '\0');
		if (lws_hdr_copy(wsi, v.data(), len + 1, index) < 0) return out;
		v.resize(static_cast<size_t>(len));
		out.push_back(std::move(v));
		return out;
	}
	std::string custom = lower + ':';
	int len = lws_hdr_custom_length(wsi, custom.c_str(), static_cast<int>(custom.size()));
	if (len <= 0) return out;
	std::string v(static_cast<size_t>(len) + 1, '\0');
	if (lws_hdr_custom_copy(wsi, v.data(), len + 1, custom.c_str(), static_cast<int>(custom.size())) < 0) return out;
	v.resize(static_cast<size_t>(len));
	out.push_back(std::move(v));
	return out;
}

Request Request::detached(std::initializer_list<std::string_view> names) const
{
	Request copy = *this;
	copy.header_copy.clear();
	for (auto name : names)
		for (auto &v : header_values(name)) copy.header_copy.emplace_back(std::string(name), std::move(v));
	copy.wsi = nullptr;
	return copy;
}

void Response::set_header(std::string_view name, std::string_view value)
{
	for (auto &h : headers)
		if (iequals(h.first, name)) {
			h.second = value;
			return;
		}
	headers.emplace_back(name, value);
}

void Response::add_header(std::string_view name, std::string_view value) { headers.emplace_back(name, value); }

void Response::text(int s, std::string_view b, std::string_view type)
{
	status = s;
	body = b;
	content_type = type;
	done = true;
}

void Response::json(int s, std::string b)
{
	status = s;
	body = std::move(b);
	content_type = "application/json; charset=utf-8";
	done = true;
}

void Response::redirect(int s, std::string_view location)
{
	status = s;
	set_header("Location", location);
	done = true;
}

void Router::use(Handler middleware) { middlewares_.push_back(std::move(middleware)); }

void Router::add(std::string_view method, std::string_view path, Handler handler)
{
	routes_.push_back({std::string(method), std::string(path), std::move(handler), nullptr});
}

void Router::add(std::string_view method, std::string_view path, CheckedHandler handler)
{
	routes_.push_back({std::string(method), std::string(path), std::move(handler.handler), std::move(handler.check)});
}

bool Router::admits(Request &request) const
{
	std::map<std::string, std::string> params;
	for (const auto &r : routes_) {
		if (r.method != request.method || !match(r.path, request.path, params)) continue;
		return !r.check || r.check(request);
	}
	// API endpoints are registered routes. Dispatch an unknown method or path from its headers;
	// middleware-owned endpoints elsewhere (including Redfish) still receive their bodies.
	return !request.path.starts_with("/api/");
}

void Router::set_body_limit(std::function<std::size_t(const Request &)> limit) { body_limit_ = std::move(limit); }

std::size_t Router::body_limit(const Request &request) const
{
	return body_limit_ ? body_limit_(request) : default_body_limit;
}

std::string url_decode(std::string_view s)
{
	std::string out;
	for (size_t i = 0; i < s.size(); i++) {
		char c = s[i];
		if (c == '+') {
			out += ' ';
		} else if (c == '%' && i + 2 < s.size() && std::isxdigit((unsigned char)s[i + 1]) &&
		           std::isxdigit((unsigned char)s[i + 2])) {
			out += static_cast<char>(std::stoi(std::string(s.substr(i + 1, 2)), nullptr, 16));
			i += 2;
		} else {
			out += c;
		}
	}
	return out;
}

std::string Request::param(std::string_view name) const
{
	auto it = params.find(std::string(name));
	return it == params.end() ? std::string() : it->second;
}

std::string Request::query_value(std::string_view name) const
{
	std::string_view rest = query;
	while (!rest.empty()) {
		auto amp = rest.find('&');
		std::string_view pair = rest.substr(0, amp);
		auto eq = pair.find('=');
		if (url_decode(pair.substr(0, eq)) == name)
			return eq == std::string_view::npos ? "" : url_decode(pair.substr(eq + 1));
		if (amp == std::string_view::npos) break;
		rest = rest.substr(amp + 1);
	}
	return {};
}

bool Router::match(const std::string &pattern, const std::string &path, std::map<std::string, std::string> &params)
{
	if (pattern.find(':') == std::string::npos) return pattern == path;
	size_t i = 0, j = 0;
	std::map<std::string, std::string> found;
	while (i < pattern.size() && j < path.size()) {
		if (pattern[i] == ':') {
			size_t pe = pattern.find('/', i);
			size_t se = path.find('/', j);
			std::string name = pattern.substr(i + 1, pe == std::string::npos ? std::string::npos : pe - i - 1);
			std::string value = path.substr(j, se == std::string::npos ? std::string::npos : se - j);
			if (value.empty()) return false;
			found[name] = value;
			i = pe == std::string::npos ? pattern.size() : pe;
			j = se == std::string::npos ? path.size() : se;
			continue;
		}
		if (pattern[i] != path[j]) return false;
		i++;
		j++;
	}
	if (i != pattern.size() || j != path.size()) return false;
	params = std::move(found);
	return true;
}

void Router::after(std::function<void(const Request &, const Response &)> hook) { after_.push_back(std::move(hook)); }

void Router::finished(const Request &request, const Response &response) const
{
	for (const auto &hook : after_) hook(request, response);
}

void Router::dispatch(Request &request, Response &response) const
{
	for (const auto &m : middlewares_) {
		m(request, response);
		if (response.done || response.pending) return;
	}
	for (const auto &r : routes_) {
		if (!match(r.path, request.path, request.params)) continue;
		if (r.method == request.method || (r.method == "GET" && request.method == "HEAD")) {
			request.route = r.path;
			r.handler(request, response);
			if (!response.done && !response.pending) response.done = true;
			return;
		}
	}
	// A plain GET to a WebSocket route: the Go handlers upgraded at once, and gorilla/websocket refused a request
	// that is not an upgrade with 400, its status text and Sec-Websocket-Version.
	if (request.method == "GET" || request.method == "HEAD") {
		std::map<std::string, std::string> params;
		for (const auto &ws_route : ws_routes_)
			if (match(ws_route.first, request.path, params)) {
				response.set_header("Sec-Websocket-Version", "13");
				response.set_header("X-Content-Type-Options", "nosniff");
				response.text(400, "Bad Request\n", "text/plain; charset=utf-8");
				return;
			}
	}
	// Gin's default for an unknown route.
	response.text(404, "404 page not found");
}

void defer(Request &request, Response &res, std::function<void(Response &)> fn)
{
	res.pending = true;
	loop::blocking([id = request.id, headers = res.headers, fn = std::move(fn)] {
		Response out;
		out.headers = headers;
		fn(out);
		complete(id, std::move(out));
	});
}

void Router::upload(std::string_view method, std::string_view path, UploadRoute route)
{
	uploads_.push_back({std::string(method), std::string(path), std::move(route)});
}

bool Router::is_upload(const std::string &method, const std::string &path) const
{
	std::map<std::string, std::string> params;
	for (const auto &u : uploads_)
		if (u.method == method && match(u.path, path, params)) return true;
	return false;
}

std::shared_ptr<BodySink> Router::dispatch_upload(Request &request, Response &response) const
{
	for (const auto &m : middlewares_) {
		m(request, response);
		if (response.done || response.pending) return nullptr;
	}
	for (const auto &u : uploads_) {
		if (u.method != request.method || !match(u.path, request.path, request.params)) continue;
		request.route = u.path;
		auto sink = u.route(request, response);
		if (!sink && !response.done && !response.pending) response.done = true;
		return sink;
	}
	response.text(404, "404 page not found");
	return nullptr;
}

void Router::ws(std::string_view path, std::function<std::shared_ptr<WsHandler>(Request &, Response &)> route)
{
	ws_routes_.emplace_back(std::string(path), std::move(route));
}

std::shared_ptr<WsHandler> Router::dispatch_ws(Request &request, Response &response) const
{
	for (const auto &m : middlewares_) {
		m(request, response);
		if (response.done || response.pending) return nullptr;
	}
	for (const auto &[path, route] : ws_routes_) {
		if (!match(path, request.path, request.params)) continue;
		auto handler = route(request, response);
		if (!handler && !response.done) response.text(400, "Bad Request");
		return handler;
	}
	response.text(404, "404 page not found");
	return nullptr;
}

} // namespace nkos::http
