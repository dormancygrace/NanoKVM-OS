#include "common/http_client.hpp"

#include <curl/curl.h>

#include <algorithm>
#include <cctype>
#include <mutex>

namespace nkos::http_client {

namespace {

std::once_flag global_init;

struct Transfer {
	const Request *request;
	Response *response;
	bool headers_done = false;
	bool in_headers_of_last = true;
};

std::string lower(std::string s)
{
	std::transform(s.begin(), s.end(), s.begin(), [](unsigned char c) { return std::tolower(c); });
	return s;
}

std::string trim(std::string_view s)
{
	while (!s.empty() && (s.front() == ' ' || s.front() == '\t')) s.remove_prefix(1);
	while (!s.empty() && (s.back() == ' ' || s.back() == '\t' || s.back() == '\r' || s.back() == '\n')) s.remove_suffix(1);
	return std::string(s);
}

size_t on_header(char *data, size_t size, size_t count, void *user)
{
	auto *t = static_cast<Transfer *>(user);
	std::string_view line(data, size * count);
	if (line.rfind("HTTP/", 0) == 0) {
		// A new response (a redirect or 100 Continue before it): keep only the last one's headers.
		t->response->headers.clear();
		return size * count;
	}
	auto colon = line.find(':');
	if (colon != std::string_view::npos)
		t->response->headers.emplace_back(lower(trim(line.substr(0, colon))), trim(line.substr(colon + 1)));
	return size * count;
}

bool headers_ready(CURL *curl, Transfer *t)
{
	if (t->headers_done) return true;
	t->headers_done = true;
	long code = 0;
	curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &code);
	t->response->status = static_cast<int>(code);
	if (t->request->on_headers && !t->request->on_headers(t->response->status, t->response->headers)) {
		t->response->stopped = true;
		return false;
	}
	return true;
}

struct WriteContext {
	CURL *curl;
	Transfer *transfer;
};

size_t on_body(char *data, size_t size, size_t count, void *user)
{
	auto *w = static_cast<WriteContext *>(user);
	Transfer *t = w->transfer;
	size_t n = size * count;
	if (!headers_ready(w->curl, t)) return 0;
	if (t->request->sink) {
		if (!t->request->sink(std::string_view(data, n))) {
			t->response->stopped = true;
			return 0;
		}
		return n;
	}
	if (t->response->body.size() + n > t->request->max_body) {
		t->response->too_large = true;
		return 0;
	}
	t->response->body.append(data, n);
	return n;
}

int on_progress(void *user, curl_off_t dltotal, curl_off_t dlnow, curl_off_t, curl_off_t)
{
	auto *t = static_cast<Transfer *>(user);
	if (!t->request->progress) return 0;
	if (!t->request->progress(dlnow, dltotal > 0 ? dltotal : -1)) {
		t->response->stopped = true;
		return 1;
	}
	return 0;
}

} // namespace

std::optional<std::string> Response::header(std::string_view name) const
{
	std::string n = lower(std::string(name));
	for (const auto &[k, v] : headers)
		if (k == n) return v;
	return std::nullopt;
}

Response perform(const Request &request)
{
	std::call_once(global_init, [] { curl_global_init(CURL_GLOBAL_DEFAULT); });
	Response response;
	CURL *curl = curl_easy_init();
	if (!curl) {
		response.error = "cannot start an HTTP request";
		return response;
	}
	Transfer transfer{&request, &response};
	WriteContext write{curl, &transfer};
	curl_slist *headers = nullptr;
	for (const auto &[k, v] : request.headers) headers = curl_slist_append(headers, (k + ": " + v).c_str());
	// No "Expect: 100-continue" pause before a body.
	headers = curl_slist_append(headers, "Expect:");

	curl_easy_setopt(curl, CURLOPT_URL, request.url.c_str());
	curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
	curl_easy_setopt(curl, CURLOPT_PROTOCOLS_STR, "http,https");
	curl_easy_setopt(curl, CURLOPT_REDIR_PROTOCOLS_STR, "http,https");
	curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, request.follow_redirects ? 1L : 0L);
	curl_easy_setopt(curl, CURLOPT_MAXREDIRS, 10L);
	// Credentials go to the same host only (libcurl's default), as preserveBasicAuthRedirect.
	curl_easy_setopt(curl, CURLOPT_UNRESTRICTED_AUTH, 0L);
	curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS, static_cast<long>(request.timeout.count()));
	curl_easy_setopt(curl, CURLOPT_CONNECTTIMEOUT_MS, static_cast<long>(request.connect_timeout.count()));
	curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
	curl_easy_setopt(curl, CURLOPT_ACCEPT_ENCODING, "");
	curl_easy_setopt(curl, CURLOPT_HEADERFUNCTION, on_header);
	curl_easy_setopt(curl, CURLOPT_HEADERDATA, &transfer);
	curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, on_body);
	curl_easy_setopt(curl, CURLOPT_WRITEDATA, &write);
	curl_easy_setopt(curl, CURLOPT_XFERINFOFUNCTION, on_progress);
	curl_easy_setopt(curl, CURLOPT_XFERINFODATA, &transfer);
	curl_easy_setopt(curl, CURLOPT_NOPROGRESS, request.progress ? 0L : 1L);
	if (!request.unix_socket.empty()) curl_easy_setopt(curl, CURLOPT_UNIX_SOCKET_PATH, request.unix_socket.c_str());
	if (request.method == "GET") {
		curl_easy_setopt(curl, CURLOPT_HTTPGET, 1L);
	} else if (request.method == "HEAD") {
		curl_easy_setopt(curl, CURLOPT_NOBODY, 1L);
	} else {
		curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, request.method.c_str());
		if (!request.body.empty() || request.method == "POST" || request.method == "PUT" || request.method == "PATCH") {
			curl_easy_setopt(curl, CURLOPT_POSTFIELDS, request.body.data());
			curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE_LARGE, static_cast<curl_off_t>(request.body.size()));
		}
	}

	CURLcode code = curl_easy_perform(curl);
	long status = 0;
	curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &status);
	response.status = static_cast<int>(status);
	char *effective = nullptr;
	if (curl_easy_getinfo(curl, CURLINFO_EFFECTIVE_URL, &effective) == CURLE_OK && effective) response.effective_url = effective;
	if (code != CURLE_OK && !response.stopped) {
		response.error = response.too_large ? "response too large" : curl_easy_strerror(code);
		if (!response.too_large) response.status = response.status ? response.status : 0;
	}
	// A body-less answer never called the write callback.
	if (code == CURLE_OK && !transfer.headers_done && request.on_headers) {
		headers_ready(curl, &transfer);
	}
	curl_slist_free_all(headers);
	curl_easy_cleanup(curl);
	return response;
}

std::string redacted(const std::string &url)
{
	CURLU *u = curl_url();
	std::string out = url;
	if (u && curl_url_set(u, CURLUPART_URL, url.c_str(), CURLU_NON_SUPPORT_SCHEME) == CURLUE_OK) {
		curl_url_set(u, CURLUPART_PASSWORD, nullptr, 0);
		char *user = nullptr;
		if (curl_url_get(u, CURLUPART_USER, &user, 0) == CURLUE_OK && user) {
			curl_url_set(u, CURLUPART_USER, "xxxxx", 0);
			curl_free(user);
		}
		curl_url_set(u, CURLUPART_QUERY, nullptr, 0);
		char *s = nullptr;
		if (curl_url_get(u, CURLUPART_URL, &s, 0) == CURLUE_OK && s) {
			out = s;
			curl_free(s);
		}
	}
	if (u) curl_url_cleanup(u);
	return out;
}

} // namespace nkos::http_client
