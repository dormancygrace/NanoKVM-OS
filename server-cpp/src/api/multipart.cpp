#include "api/multipart.hpp"

#include <algorithm>
#include <cctype>

namespace nkos::api {

namespace {

std::string lower(std::string_view s)
{
	std::string out(s);
	std::transform(out.begin(), out.end(), out.begin(), [](unsigned char c) { return std::tolower(c); });
	return out;
}

std::string_view trim(std::string_view s)
{
	while (!s.empty() && (s.front() == ' ' || s.front() == '\t')) s.remove_prefix(1);
	while (!s.empty() && (s.back() == ' ' || s.back() == '\t' || s.back() == '\r')) s.remove_suffix(1);
	return s;
}

// A parameter of a header value (boundary=..., name="..."), unquoted; nullopt when absent.
std::optional<std::string> header_param(std::string_view value, std::string_view key)
{
	std::size_t i = value.find(';');
	if (i == std::string_view::npos) return std::nullopt;
	while (i < value.size()) {
		while (i < value.size() && (value[i] == ' ' || value[i] == '\t' || value[i] == ';')) i++;
		std::size_t k0 = i;
		while (i < value.size() && value[i] != '=' && value[i] != ';') i++;
		std::string k = lower(trim(value.substr(k0, i - k0)));
		std::string v;
		if (i < value.size() && value[i] == '=') {
			i++;
			if (i < value.size() && value[i] == '"') {
				for (i++; i < value.size() && value[i] != '"'; i++) {
					if (value[i] == '\\' && i + 1 < value.size()) i++;
					v += value[i];
				}
				if (i < value.size()) i++;
			} else {
				std::size_t v0 = i;
				while (i < value.size() && value[i] != ';') i++;
				v = std::string(trim(value.substr(v0, i - v0)));
			}
		}
		if (k == key) return v;
	}
	return std::nullopt;
}

// The last path element, as Go's filepath.Base (and "/" for a name of only slashes, "." for an empty one).
std::string base_name(std::string name)
{
	while (name.size() > 1 && name.back() == '/') name.pop_back();
	if (name == "/") return name;
	auto slash = name.rfind('/');
	if (slash != std::string::npos) name = name.substr(slash + 1);
	return name.empty() ? "." : name;
}

} // namespace

std::optional<std::vector<Part>> multipart(const http::Request &request)
{
	return multipart(request.header("content-type"), request.body);
}

std::optional<std::vector<Part>> multipart(std::string_view type, std::string_view body)
{
	if (lower(trim(type.substr(0, type.find(';')))) != "multipart/form-data") return std::nullopt;
	auto boundary = header_param(type, "boundary");
	if (!boundary || boundary->empty()) return std::nullopt;
	std::string delimiter = "--" + *boundary;
	std::size_t pos = body.find(delimiter);
	if (pos == std::string_view::npos) return std::nullopt;
	std::vector<Part> parts;
	while (true) {
		pos += delimiter.size();
		if (body.substr(pos, 2) == "--") return parts;  // the closing delimiter
		std::size_t line = body.find("\r\n", pos);
		if (line == std::string_view::npos) return std::nullopt;
		std::size_t headers_end = body.find("\r\n\r\n", line);
		if (headers_end == std::string_view::npos) {
			// A part without headers starts right after its delimiter line.
			if (body.substr(line + 2, 2) != "\r\n") return std::nullopt;
			headers_end = line;
		}
		Part part;
		std::string_view headers = body.substr(line + 2, headers_end >= line + 2 ? headers_end - line - 2 : 0);
		bool has_disposition = false;
		while (!headers.empty()) {
			std::size_t eol = headers.find("\r\n");
			std::string_view h = headers.substr(0, eol);
			headers = eol == std::string_view::npos ? std::string_view{} : headers.substr(eol + 2);
			std::size_t colon = h.find(':');
			if (colon == std::string_view::npos) continue;
			if (lower(trim(h.substr(0, colon))) != "content-disposition") continue;
			std::string_view value = trim(h.substr(colon + 1));
			if (lower(value.substr(0, value.find(';'))) != "form-data") continue;
			has_disposition = true;
			part.name = header_param(value, "name").value_or("");
			if (auto filename = header_param(value, "filename")) part.filename = base_name(*filename);
		}
		std::size_t data_start = headers_end + 4;
		std::size_t next = body.find("\r\n" + delimiter, data_start);
		if (next == std::string_view::npos) return std::nullopt;
		part.data = body.substr(data_start, next - data_start);
		if (has_disposition) parts.push_back(std::move(part));
		pos = next + 2;
	}
}

const Part *form_file(const std::vector<Part> &parts, std::string_view field)
{
	for (const auto &p : parts)
		if (p.name == field && p.filename) return &p;
	return nullptr;
}

std::optional<std::string> form_value(const std::vector<Part> &parts, std::string_view field)
{
	for (const auto &p : parts)
		if (p.name == field && !p.filename) return std::string(p.data);
	return std::nullopt;
}

} // namespace nkos::api
