#include "config/yaml.hpp"

#define RYML_SINGLE_HDR_DEFINE_NOW
#include "common/yaml_parser.hpp"

#include <algorithm>
#include <cctype>
#include <charconv>

namespace nkos::yaml {

namespace {

std::string lower(std::string_view s)
{
	std::string out(s);
	std::ranges::transform(out, out.begin(), [](unsigned char c) { return std::tolower(c); });
	return out;
}

// YAML 1.2 core schema null, as yaml.v3 resolves it for a plain scalar.
bool is_null(const Value &v)
{
	return v.kind == Value::Kind::scalar && !v.quoted &&
	       (v.text.empty() || v.text == "~" || v.text == "null" || v.text == "Null" || v.text == "NULL");
}

std::shared_ptr<Value> build(ryml::ConstNodeRef node)
{
	auto v = std::make_shared<Value>();
	if (node.is_map()) {
		v->kind = Value::Kind::map;
		for (ryml::ConstNodeRef child : node.children()) {
			auto value = build(child);
			if (value->kind == Value::Kind::scalar && is_null(*value)) continue;
			v->map[lower(std::string_view(child.key().str, child.key().len))] = value;
		}
	} else if (node.is_seq()) {
		v->kind = Value::Kind::seq;
		for (ryml::ConstNodeRef child : node.children()) v->seq.push_back(build(child));
	} else if (node.has_val()) {
		auto s = node.val();
		v->text.assign(s.str, s.len);
		v->quoted = node.is_val_quoted();
	}
	return v;
}

// Viper: a dotted key ("cert.crt") is a nested path; applied after the keys are lower-cased.
void expand_dotted(Value &v)
{
	if (v.kind != Value::Kind::map) return;
	for (auto &[key, child] : v.map) expand_dotted(*child);
	std::vector<std::string> dotted;
	for (auto &[key, child] : v.map)
		if (key.find('.') != std::string::npos) dotted.push_back(key);
	for (const auto &key : dotted) {
		auto value = v.map[key];
		v.map.erase(key);
		Value *node = &v;
		std::string_view rest = key;
		for (;;) {
			auto dot = rest.find('.');
			std::string part(rest.substr(0, dot));
			if (dot == std::string_view::npos) {
				node->map[part] = value;
				break;
			}
			auto &next = node->map[part];
			if (!next || next->kind != Value::Kind::map) {
				next = std::make_shared<Value>();
				next->kind = Value::Kind::map;
			}
			node = next.get();
			rest = rest.substr(dot + 1);
		}
	}
}

// strconv.ParseInt(s, 0, 64): optional sign, 0x/0o/0b prefixes, a leading 0 is octal, underscores after a prefix.
std::optional<std::int64_t> parse_int(std::string_view s)
{
	if (s.empty()) return std::nullopt;
	bool negative = false;
	if (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-';
		s.remove_prefix(1);
	}
	int base = 10;
	if (s.size() > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) base = 16, s.remove_prefix(2);
	else if (s.size() > 2 && s[0] == '0' && (s[1] == 'o' || s[1] == 'O')) base = 8, s.remove_prefix(2);
	else if (s.size() > 2 && s[0] == '0' && (s[1] == 'b' || s[1] == 'B')) base = 2, s.remove_prefix(2);
	else if (s.size() > 1 && s[0] == '0') base = 8, s.remove_prefix(1);
	std::string digits;
	for (char c : s)
		if (c != '_') digits.push_back(c);
	if (digits.empty()) return std::nullopt;
	std::uint64_t value = 0;
	auto [end, ec] = std::from_chars(digits.data(), digits.data() + digits.size(), value, base);
	if (ec != std::errc{} || end != digits.data() + digits.size()) return std::nullopt;
	if (negative) {
		if (value > static_cast<std::uint64_t>(INT64_MAX) + 1) return std::nullopt;
		return static_cast<std::int64_t>(0 - value);
	}
	if (value > static_cast<std::uint64_t>(INT64_MAX)) return std::nullopt;
	return static_cast<std::int64_t>(value);
}

// strconv.ParseBool.
std::optional<bool> parse_bool(std::string_view s)
{
	if (s == "1" || s == "t" || s == "T" || s == "true" || s == "TRUE" || s == "True") return true;
	if (s == "0" || s == "f" || s == "F" || s == "false" || s == "FALSE" || s == "False") return false;
	return std::nullopt;
}

// A plain YAML 1.2 boolean, as yaml.v3 resolves it.
std::optional<bool> yaml_bool(const Value &v)
{
	if (v.quoted) return std::nullopt;
	if (v.text == "true" || v.text == "True" || v.text == "TRUE") return true;
	if (v.text == "false" || v.text == "False" || v.text == "FALSE") return false;
	return std::nullopt;
}

} // namespace

const Value *Value::get(std::string_view key) const
{
	if (kind != Kind::map) return nullptr;
	auto it = map.find(std::string(key));
	return it == map.end() ? nullptr : it->second.get();
}

const Value *Value::path(std::string_view dotted) const
{
	const Value *v = this;
	while (v && !dotted.empty()) {
		auto dot = dotted.find('.');
		v = v->get(dotted.substr(0, dot));
		dotted = dot == std::string_view::npos ? std::string_view{} : dotted.substr(dot + 1);
	}
	return v;
}

std::optional<Value> parse(std::string_view text, std::string &error)
{
	std::optional<Value> result;
	try {
		ryml::EventHandlerTree handler(parser_callbacks());
		ryml::Parser parser(&handler);
		ryml::Tree tree = ryml::parse_in_arena(&parser, ryml::csubstr(text.data(), text.size()));
		tree.resolve();  // aliases and << merges
		ryml::ConstNodeRef root = tree.crootref();
		if (root.is_stream() && root.num_children() > 0) root = root.first_child();
		if (root.is_map()) {
			auto v = build(root);
			expand_dotted(*v);
			result = std::move(*v);
		} else if (!root.has_val() || root.val().empty()) {
			Value empty;
			empty.kind = Value::Kind::map;
			result = std::move(empty);
		} else {
			error = "the top level is not a mapping";
		}
	} catch (const ParseError &e) {
		error = e.what();
	}
	return result;
}

std::optional<std::int64_t> as_int(const Value *v)
{
	if (!v || v->kind != Value::Kind::scalar) return std::nullopt;
	if (auto b = yaml_bool(*v)) return *b ? 1 : 0;
	if (auto i = parse_int(v->text)) return i;
	// A float written as such converts by truncation (mapstructure weak float -> int).
	if (!v->quoted) {
		double d = 0;
		auto [end, ec] = std::from_chars(v->text.data(), v->text.data() + v->text.size(), d);
		if (ec == std::errc{} && end == v->text.data() + v->text.size()) return static_cast<std::int64_t>(d);
	}
	return std::nullopt;
}

std::optional<std::uint64_t> as_uint(const Value *v)
{
	auto i = as_int(v);
	if (!i || *i < 0) return std::nullopt;
	return static_cast<std::uint64_t>(*i);
}

std::optional<bool> as_bool(const Value *v)
{
	if (!v || v->kind != Value::Kind::scalar) return std::nullopt;
	if (auto b = yaml_bool(*v)) return b;
	if (!v->quoted)
		if (auto i = parse_int(v->text)) return *i != 0;
	if (v->text.empty()) return false;  // mapstructure: an empty string is false
	return parse_bool(v->text);
}

std::optional<std::string> as_string(const Value *v)
{
	if (!v || v->kind != Value::Kind::scalar) return std::nullopt;
	if (auto b = yaml_bool(*v)) return *b ? "1" : "0";  // mapstructure weak bool -> string
	return v->text;
}

std::optional<std::vector<std::string>> as_strings(const Value *v)
{
	if (!v) return std::nullopt;
	std::vector<std::string> out;
	if (v->kind == Value::Kind::seq) {
		for (const auto &item : v->seq)
			if (auto s = as_string(item.get())) out.push_back(*s);
		return out;
	}
	if (v->kind != Value::Kind::scalar) return std::nullopt;
	// StringToSliceHookFunc(","): an empty string is an empty list.
	if (v->text.empty()) return out;
	std::string_view rest = v->text;
	for (;;) {
		auto comma = rest.find(',');
		out.emplace_back(rest.substr(0, comma));
		if (comma == std::string_view::npos) break;
		rest = rest.substr(comma + 1);
	}
	return out;
}

} // namespace nkos::yaml
