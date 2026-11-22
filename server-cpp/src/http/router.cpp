#include "http/router.hpp"

#include <libwebsockets.h>

#include <algorithm>
#include <cctype>

namespace nkos::http {

namespace {

bool iequals(std::string_view a, std::string_view b)
{
	return a.size() == b.size() &&
	       std::equal(a.begin(), a.end(), b.begin(), [](char x, char y) { return std::tolower((unsigned char)x) == std::tolower((unsigned char)y); });
}

// Headers with an lws token; any other name is read as a custom header.
struct Token {
	const char *name;
	lws_token_indexes index;
};
const Token tokens[] = {
	{"host", WSI_TOKEN_HOST},
	{"accept-encoding", WSI_TOKEN_HTTP_ACCEPT_ENCODING},
	{"accept", WSI_TOKEN_HTTP_ACCEPT},
	{"authorization", WSI_TOKEN_HTTP_AUTHORIZATION},
	{"cookie", WSI_TOKEN_HTTP_COOKIE},
	{"content-type", WSI_TOKEN_HTTP_CONTENT_TYPE},
	{"content-length", WSI_TOKEN_HTTP_CONTENT_LENGTH},
	{"origin", WSI_TOKEN_ORIGIN},
	{"referer", WSI_TOKEN_HTTP_REFERER},
	{"user-agent", WSI_TOKEN_HTTP_USER_AGENT},
	{"if-modified-since", WSI_TOKEN_HTTP_IF_MODIFIED_SINCE},
	{"if-none-match", WSI_TOKEN_HTTP_IF_NONE_MATCH},
	{"range", WSI_TOKEN_HTTP_RANGE},
	{"x-forwarded-for", WSI_TOKEN_X_FORWARDED_FOR},
	{"upgrade", WSI_TOKEN_UPGRADE},
	{"connection", WSI_TOKEN_CONNECTION},
};

} // namespace

std::string Request::header(std::string_view name) const
{
	auto values = header_values(name);
	return values.empty() ? std::string() : values.front();
}

std::vector<std::string> Request::header_values(std::string_view name) const
{
	std::vector<std::string> out;
	if (!wsi) return out;
	for (const auto &t : tokens) {
		if (!iequals(name, t.name)) continue;
		lws_token_indexes index = t.index;
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
	std::string custom;
	for (char c : name) custom += static_cast<char>(std::tolower((unsigned char)c));
	custom += ':';
	int len = lws_hdr_custom_length(wsi, custom.c_str(), static_cast<int>(custom.size()));
	if (len <= 0) return out;
	std::string v(static_cast<size_t>(len) + 1, '\0');
	if (lws_hdr_custom_copy(wsi, v.data(), len + 1, custom.c_str(), static_cast<int>(custom.size())) < 0) return out;
	v.resize(static_cast<size_t>(len));
	out.push_back(std::move(v));
	return out;
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
	routes_.push_back({std::string(method), std::string(path), std::move(handler)});
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

void Router::dispatch(Request &request, Response &response) const
{
	for (const auto &m : middlewares_) {
		m(request, response);
		if (response.done || response.pending) return;
	}
	for (const auto &r : routes_) {
		if (!match(r.path, request.path, request.params)) continue;
		if (r.method == request.method || (r.method == "GET" && request.method == "HEAD")) {
			r.handler(request, response);
			if (!response.done && !response.pending) response.done = true;
			return;
		}
	}
	// Gin's default for an unknown route.
	response.text(404, "404 page not found");
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
