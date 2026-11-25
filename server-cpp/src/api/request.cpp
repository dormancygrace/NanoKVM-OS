#include "api/request.hpp"

#include <glaze/glaze.hpp>

#include <cmath>
#include <cstdlib>

namespace nkos::api {

namespace {

std::string lower(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return s;
}

void parse_form(std::string_view text, std::map<std::string, std::string> &out)
{
	while (!text.empty()) {
		auto amp = text.find('&');
		std::string_view pair = text.substr(0, amp);
		if (!pair.empty()) {
			auto eq = pair.find('=');
			std::string key = http::url_decode(pair.substr(0, eq));
			if (!out.contains(key)) out[key] = eq == std::string_view::npos ? "" : http::url_decode(pair.substr(eq + 1));
		}
		if (amp == std::string_view::npos) break;
		text = text.substr(amp + 1);
	}
}

} // namespace

std::optional<Params> Params::bind(const http::Request &request)
{
	Params p;
	std::string type = lower(request.header("content-type"));
	if (auto semi = type.find(';'); semi != std::string::npos) type.resize(semi);
	while (!type.empty() && type.back() == ' ') type.pop_back();
	if (request.method == "GET" || request.method == "HEAD") {
		std::map<std::string, std::string> form;
		parse_form(request.query, form);
		p.form_ = true;
		for (auto &[k, v] : form) p.fields_[k] = Field{Kind::string, v};
		return p;
	}
	if (type == "application/json") {
		glz::generic value;
		if (glz::read_json(value, request.body)) return std::nullopt;
		auto *object = value.get_if<glz::generic::object_t>();
		if (!object) return std::nullopt;
		for (auto &[key, member] : *object) {
			Field f{Kind::other, {}};
			if (auto *s = member.get_if<std::string>()) f = Field{Kind::string, *s};
			else if (auto *b = member.get_if<bool>()) f = Field{Kind::boolean, {}, *b};
			else if (auto *n = member.get_if<double>()) f = Field{Kind::number, {}, false, *n};
			else if (member.get_if<std::nullptr_t>()) continue;  // null leaves the field unset
			p.fields_[key] = f;
		}
		return p;
	}
	// Gin's form binding: the query and a urlencoded body (multipart forms come with uploads).
	std::map<std::string, std::string> form;
	if (type == "application/x-www-form-urlencoded") parse_form(request.body, form);
	parse_form(request.query, form);
	p.form_ = true;
	for (auto &[k, v] : form) p.fields_[k] = Field{Kind::string, v};
	return p;
}

std::optional<std::string> Params::string(const std::string &name, bool &ok) const
{
	auto it = fields_.find(name);
	if (it == fields_.end()) return std::nullopt;
	if (it->second.kind != Kind::string) {
		ok = false;
		return std::nullopt;
	}
	return it->second.text;
}

std::optional<bool> Params::boolean(const std::string &name, bool &ok) const
{
	auto it = fields_.find(name);
	if (it == fields_.end()) return std::nullopt;
	if (it->second.kind == Kind::boolean) return it->second.flag;
	if (form_ && it->second.kind == Kind::string) {
		const auto &t = it->second.text;
		if (t.empty()) return false;
		if (t == "1" || t == "t" || t == "T" || t == "true" || t == "TRUE" || t == "True") return true;
		if (t == "0" || t == "f" || t == "F" || t == "false" || t == "FALSE" || t == "False") return false;
	}
	ok = false;
	return std::nullopt;
}

std::optional<long long> Params::integer(const std::string &name, bool &ok) const
{
	auto it = fields_.find(name);
	if (it == fields_.end()) return std::nullopt;
	if (it->second.kind == Kind::number && std::trunc(it->second.number) == it->second.number)
		return static_cast<long long>(it->second.number);
	if (form_ && it->second.kind == Kind::string) {
		char *end = nullptr;
		long long v = std::strtoll(it->second.text.c_str(), &end, 10);
		if (!it->second.text.empty() && *end == '\0') return v;
	}
	ok = false;
	return std::nullopt;
}

} // namespace nkos::api
