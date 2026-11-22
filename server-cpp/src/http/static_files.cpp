#include "http/static_files.hpp"

#include <algorithm>
#include <cctype>
#include <ctime>
#include <map>
#include <sys/stat.h>

namespace nkos::http {

namespace {

// path.Clean("/" + p): absolute, no "." or ".." segments, no repeated or trailing slash (except "/").
std::string clean(std::string_view p)
{
	std::vector<std::string_view> parts;
	size_t i = 0;
	while (i <= p.size()) {
		size_t j = p.find('/', i);
		if (j == std::string_view::npos) j = p.size();
		std::string_view seg = p.substr(i, j - i);
		if (seg == "..") {
			if (!parts.empty()) parts.pop_back();
		} else if (!seg.empty() && seg != ".") {
			parts.push_back(seg);
		}
		i = j + 1;
	}
	std::string out;
	for (auto s : parts) {
		out += '/';
		out += s;
	}
	return out.empty() ? "/" : out;
}

bool stat_path(const std::string &path, struct stat &st) { return ::stat(path.c_str(), &st) == 0; }

std::string http_date(time_t t)
{
	tm g{};
	gmtime_r(&t, &g);
	char buf[64];
	std::strftime(buf, sizeof buf, "%a, %d %b %Y %H:%M:%S GMT", &g);
	return buf;
}

time_t parse_http_date(const std::string &s)
{
	tm g{};
	if (!strptime(s.c_str(), "%a, %d %b %Y %H:%M:%S GMT", &g)) return -1;
	return timegm(&g);
}

// assets.go encodingQualities: coding -> q (0..1000), invalid weights count as 0, a repeated prohibition wins.
std::map<std::string, int> encoding_qualities(const std::string &header)
{
	std::map<std::string, int> values;
	auto trim = [](std::string_view s) {
		while (!s.empty() && (s.front() == ' ' || s.front() == '\t')) s.remove_prefix(1);
		while (!s.empty() && (s.back() == ' ' || s.back() == '\t')) s.remove_suffix(1);
		return s;
	};
	auto quality = [](std::string_view v) {
		auto dot = v.find('.');
		std::string_view whole = v.substr(0, dot), fraction = dot == std::string_view::npos ? "" : v.substr(dot + 1);
		if ((whole != "0" && whole != "1") || fraction.size() > 3) return 0;
		int q = 0, scale = 100;
		for (char d : fraction) {
			if (d < '0' || d > '9' || (whole == "1" && d != '0')) return 0;
			q += (d - '0') * scale;
			scale /= 10;
		}
		return whole == "1" ? 1000 : q;
	};
	std::string_view rest = header;
	while (true) {
		auto comma = rest.find(',');
		std::string_view item = rest.substr(0, comma);
		auto semi = item.find(';');
		std::string coding(trim(item.substr(0, semi)));
		std::ranges::transform(coding, coding.begin(), [](unsigned char c) { return std::tolower(c); });
		if (!coding.empty()) {
			int q = 1000;
			bool weighted = false;
			std::string_view params = semi == std::string_view::npos ? "" : item.substr(semi + 1);
			while (!params.empty() || semi != std::string_view::npos) {
				auto next = params.find(';');
				std::string_view param = params.substr(0, next);
				auto eq = param.find('=');
				std::string key(trim(param.substr(0, eq)));
				std::ranges::transform(key, key.begin(), [](unsigned char c) { return std::tolower(c); });
				if (eq == std::string_view::npos || key != "q" || weighted) {
					q = 0;
					break;
				}
				q = quality(trim(param.substr(eq + 1)));
				weighted = true;
				if (next == std::string_view::npos) break;
				params = params.substr(next + 1);
			}
			auto it = values.find(coding);
			if (it == values.end() || q < it->second) values[coding] = q;
		}
		if (comma == std::string_view::npos) break;
		rest = rest.substr(comma + 1);
	}
	return values;
}

std::string join(const std::vector<std::string> &values)
{
	std::string out;
	for (const auto &v : values) {
		if (!out.empty()) out += ',';
		out += v;
	}
	return out;
}

} // namespace

std::string_view mime_type(std::string_view path)
{
	static const std::pair<std::string_view, std::string_view> types[] = {
		{".html", "text/html; charset=utf-8"}, {".js", "text/javascript; charset=utf-8"},
		{".mjs", "text/javascript; charset=utf-8"}, {".css", "text/css; charset=utf-8"},
		{".json", "application/json"}, {".svg", "image/svg+xml"}, {".png", "image/png"},
		{".jpg", "image/jpeg"}, {".jpeg", "image/jpeg"}, {".gif", "image/gif"}, {".webp", "image/webp"},
		{".ico", "image/vnd.microsoft.icon"}, {".woff", "font/woff"}, {".woff2", "font/woff2"},
		{".ttf", "font/ttf"}, {".wasm", "application/wasm"}, {".txt", "text/plain; charset=utf-8"},
		{".xml", "text/xml; charset=utf-8"}, {".pdf", "application/pdf"}, {".webmanifest", "application/manifest+json"},
		{".exe", "application/vnd.microsoft.portable-executable"}, {".sh", "text/x-sh; charset=utf-8"},
		{".map", "application/json"},
	};
	auto dot = path.rfind('.');
	auto slash = path.rfind('/');
	if (dot == std::string_view::npos || (slash != std::string_view::npos && dot < slash)) return {};
	std::string ext(path.substr(dot));
	std::ranges::transform(ext, ext.begin(), [](unsigned char c) { return std::tolower(c); });
	for (auto &[e, t] : types)
		if (e == ext) return t;
	return {};
}

Handler index_no_store()
{
	return [](Request &req, Response &res) {
		if (req.path == "/" || req.path == "/index.html") {
			res.set_header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0");
			res.set_header("Pragma", "no-cache");
			res.set_header("Expires", "0");
		}
	};
}

Handler assets(std::string root)
{
	return [root](Request &req, Response &res) {
		std::string name = clean(req.path);
		if (!name.starts_with("/assets/") || (req.method != "GET" && req.method != "HEAD")) return;
		std::string plain = root + name;
		struct stat st{};
		if (!stat_path(plain, st) || S_ISDIR(st.st_mode)) return;  // never cache a missing file
		auto quality = encoding_qualities(join(req.header_values("accept-encoding")));
		std::string file = plain, coding;
		int best = 0;
		if (auto it = quality.find("identity"); it != quality.end()) best = it->second;
		for (auto [candidate, suffix] : {std::pair{"br", ".br"}, std::pair{"gzip", ".gz"}}) {
			auto it = quality.find(candidate);
			int q = it != quality.end() ? it->second : (quality.contains("*") ? quality["*"] : 0);
			if (q <= 0 || q < best || (q == best && !coding.empty())) continue;
			struct stat cs{};
			if (stat_path(plain + suffix, cs) && !S_ISDIR(cs.st_mode)) {
				file = plain + suffix;
				coding = candidate;
				best = q;
			}
		}
		res.add_header("Vary", "Accept-Encoding");
		auto identity = quality.find("identity");
		auto wildcard = quality.find("*");
		if (coding.empty() && ((identity != quality.end() && identity->second == 0) ||
		                       (identity == quality.end() && wildcard != quality.end() && wildcard->second == 0))) {
			res.status = 406;
			res.done = true;
			return;
		}
		res.set_header("Cache-Control", "public, max-age=31536000, immutable");
		if (coding.empty()) return;  // the plain file: static_files serves it
		res.file = file;
		res.file_encoding = coding;
		res.content_type = mime_type(name);
		res.done = true;
	};
}

Handler static_files(std::string root)
{
	return [root](Request &req, Response &res) {
		if (!req.path.starts_with("/") || (req.method != "GET" && req.method != "HEAD")) return;
		std::string name = clean(req.path);
		std::string full = root + (name == "/" ? "" : name);
		struct stat st{};
		if (!stat_path(full, st)) return;  // absent: the routes decide
		// net/http FileServer: ".../index.html" redirects to the directory, a directory needs a trailing slash.
		if (req.path.ends_with("/index.html")) {
			res.redirect(301, "./");
			return;
		}
		if (S_ISDIR(st.st_mode)) {
			if (!req.path.ends_with("/")) {
				auto slash = req.path.rfind('/');
				res.redirect(301, req.path.substr(slash + 1) + "/");
				return;
			}
			full += "/index.html";
			if (!stat_path(full, st) || S_ISDIR(st.st_mode)) {
				res.text(403, "403 Forbidden");  // no directory listings
				return;
			}
		}
		std::string modified = http_date(st.st_mtime);
		std::string since = req.header("if-modified-since");
		if (!since.empty() && req.header("if-none-match").empty()) {
			time_t t = parse_http_date(since);
			if (t >= 0 && st.st_mtime <= t) {
				res.status = 304;
				res.set_header("Last-Modified", modified);
				res.done = true;
				return;
			}
		}
		res.set_header("Last-Modified", modified);
		res.file = full;
		res.content_type = mime_type(full);
		if (res.content_type.empty()) res.content_type = "application/octet-stream";
		res.done = true;
	};
}

} // namespace nkos::http
