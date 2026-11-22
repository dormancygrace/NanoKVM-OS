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
		int len = lws_hdr_total_length(wsi, t.index);
		if (len <= 0) return out;
		std::string v(static_cast<size_t>(len) + 1, '\0');
		if (lws_hdr_copy(wsi, v.data(), len + 1, t.index) < 0) return out;
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

void Router::dispatch(Request &request, Response &response) const
{
	for (const auto &m : middlewares_) {
		m(request, response);
		if (response.done) return;
	}
	for (const auto &r : routes_) {
		if (r.path != request.path) continue;
		if (r.method == request.method || (r.method == "GET" && request.method == "HEAD")) {
			r.handler(request, response);
			if (!response.done) response.done = true;
			return;
		}
	}
	// Gin's default for an unknown route.
	response.text(404, "404 page not found");
}

} // namespace nkos::http
