#include "config/config_tls.hpp"

#include "config/yaml.hpp"

#include <algorithm>
#include <cerrno>
#include <cstdio>
#include <cstring>
#include <mutex>
#include <string_view>
#include <vector>

namespace nkos::config {

namespace {

std::optional<std::string> read_file(const std::string &path, int &err)
{
	FILE *f = std::fopen(path.c_str(), "rb");
	if (!f) {
		err = errno;
		return std::nullopt;
	}
	std::string data;
	char buf[4096];
	size_t n;
	while ((n = std::fread(buf, 1, sizeof buf, f)) > 0) data.append(buf, n);
	std::fclose(f);
	return data;
}

std::optional<std::string> scalar(const yaml::Value *v, bool &ok)
{
	if (!v) return std::nullopt;
	if (v->kind != yaml::Value::Kind::scalar) {
		ok = false;
		return std::nullopt;
	}
	return v->text;
}

// ---- the text edit: block-style YAML as yaml.v3 writes server.yaml ----

size_t indent_of(std::string_view line)
{
	size_t i = 0;
	while (i < line.size() && line[i] == ' ') i++;
	return i;
}

bool blank_or_comment(std::string_view line)
{
	size_t i = 0;
	while (i < line.size() && (line[i] == ' ' || line[i] == '\t' || line[i] == '\r')) i++;
	return i == line.size() || line[i] == '#';
}

bool document_marker(std::string_view line)
{
	return line.starts_with("---") || line.starts_with("...");
}

// The key of a "key: value" line at its indent and the position after the ':'; nullopt when the line is no
// mapping entry.
struct Entry {
	std::string key;
	size_t colon = 0;
};
std::optional<Entry> entry_of(std::string_view line)
{
	size_t i = indent_of(line);
	if (i >= line.size()) return std::nullopt;
	Entry e;
	char c = line[i];
	if (c == '"' || c == '\'') {
		size_t end = line.find(c, i + 1);
		if (end == std::string_view::npos) return std::nullopt;
		e.key = std::string(line.substr(i + 1, end - i - 1));
		size_t j = end + 1;
		while (j < line.size() && line[j] == ' ') j++;
		if (j >= line.size() || line[j] != ':') return std::nullopt;
		e.colon = j;
	} else {
		if (c == '-' || c == '?' || c == '[' || c == '{' || c == '#' || c == '&' || c == '*' || c == '!' || c == '|' ||
		    c == '>' || c == '%' || c == '@' || c == '`')
			return std::nullopt;
		size_t j = i;
		for (; j < line.size(); j++)
			if (line[j] == ':' && (j + 1 == line.size() || line[j + 1] == ' ' || line[j + 1] == '\t' || line[j + 1] == '\r')) break;
		if (j >= line.size()) return std::nullopt;
		size_t end = j;
		while (end > i && line[end - 1] == ' ') end--;
		e.key = std::string(line.substr(i, end - i));
		e.colon = j;
	}
	return e;
}

// The value written after "key:" on a line: empty, or only a comment.
bool empty_value(std::string_view line, size_t colon) { return blank_or_comment(line.substr(colon + 1)); }

// A string as a YAML scalar: plain when it reads back as the same string, double-quoted otherwise.
std::string yaml_string(const std::string &s)
{
	static const char *const special[] = {"true", "false", "yes", "no", "on", "off", "y", "n", "null", "~"};
	bool plain = !s.empty();
	for (size_t i = 0; plain && i < s.size(); i++) {
		char c = s[i];
		bool ok = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '/' || c == '_' ||
		          (i > 0 && ((c >= '0' && c <= '9') || c == '.' || c == '-'));
		if (!ok) plain = false;
	}
	if (plain) {
		std::string lower;
		for (char c : s) lower += static_cast<char>(c >= 'A' && c <= 'Z' ? c - 'A' + 'a' : c);
		for (const char *w : special)
			if (lower == w) plain = false;
	}
	if (plain) return s;
	std::string out = "\"";
	static const char hex[] = "0123456789ABCDEF";
	for (unsigned char c : s) {
		if (c == '"' || c == '\\') {
			out += '\\';
			out += static_cast<char>(c);
		} else if (c < 0x20 || c == 0x7f) {
			out += "\\x";
			out += hex[c >> 4];
			out += hex[c & 15];
		} else {
			out += static_cast<char>(c);
		}
	}
	return out + "\"";
}

struct Lines {
	std::vector<std::string> v;

	// [begin, end) of the entry whose key line is at i: up to the next line at column 0 that is not blank or a
	// comment, without the blank and comment lines that end it.
	size_t span_end(size_t i, size_t min_indent) const
	{
		size_t j = i + 1, last = i + 1;
		for (; j < v.size(); j++) {
			if (blank_or_comment(v[j])) continue;
			if (indent_of(v[j]) <= min_indent || (min_indent == 0 && document_marker(v[j]))) break;
			last = j + 1;
		}
		return last;
	}
};

std::optional<std::string> edit(std::string_view text, const std::string &proto, const Cert &cert)
{
	Lines l;
	for (size_t pos = 0; pos < text.size();) {
		size_t nl = text.find('\n', pos);
		if (nl == std::string_view::npos) nl = text.size();
		l.v.emplace_back(text.substr(pos, nl - pos));
		pos = nl + 1;
	}
	// Top-level entries; anything else at column 0 means the root is no block mapping.
	struct Top {
		size_t line;
		Entry entry;
	};
	auto top_entries = [&]() -> std::optional<std::vector<Top>> {
		std::vector<Top> tops;
		for (size_t i = 0; i < l.v.size(); i++) {
			const std::string &line = l.v[i];
			if (blank_or_comment(line) || indent_of(line) > 0 || document_marker(line)) continue;
			auto e = entry_of(line);
			if (!e) return std::nullopt;
			tops.push_back({i, *e});
		}
		if (tops.empty()) return std::nullopt;
		return tops;
	};
	auto find = [](const std::vector<Top> &tops, std::string_view key) -> const Top * {
		for (const auto &t : tops)
			if (t.entry.key == key) return &t;
		return nullptr;
	};
	auto tops = top_entries();
	if (!tops) return std::nullopt;
	// Trailing empty line of a file that ends with a newline: appended entries go before it.
	auto append_at = [&]() {
		size_t end = l.v.size();
		while (end > 0 && l.v[end - 1].empty()) end--;
		return end;
	};

	if (const Top *t = find(*tops, "proto")) {
		size_t end = l.span_end(t->line, 0);
		std::string line = l.v[t->line].substr(0, t->entry.colon + 1) + " " + yaml_string(proto);
		l.v.erase(l.v.begin() + static_cast<long>(t->line), l.v.begin() + static_cast<long>(end));
		l.v.insert(l.v.begin() + static_cast<long>(t->line), line);
	} else {
		l.v.insert(l.v.begin() + static_cast<long>(append_at()), "proto: " + yaml_string(proto));
	}

	tops = top_entries();
	if (!tops) return std::nullopt;
	const std::string crt_line = "crt: " + yaml_string(cert.crt), key_line = "key: " + yaml_string(cert.key);
	const Top *t = find(*tops, "cert");
	if (!t) {
		size_t at = append_at();
		l.v.insert(l.v.begin() + static_cast<long>(at), {"cert:", "    " + crt_line, "    " + key_line});
	} else {
		size_t begin = t->line, end = l.span_end(t->line, 0);
		bool block = empty_value(l.v[begin], t->entry.colon);
		size_t child_indent = 0;
		struct Child {
			std::string key;
			size_t line, end, colon;
		};
		std::vector<Child> children;
		for (size_t i = begin + 1; block && i < end; i++) {
			if (blank_or_comment(l.v[i])) continue;
			size_t ind = indent_of(l.v[i]);
			if (!child_indent) child_indent = ind;
			if (ind < child_indent) {
				block = false;
				break;
			}
			if (ind > child_indent) continue;
			auto e = entry_of(l.v[i]);
			if (!e) {
				block = false;
				break;
			}
			children.push_back({e->key, i, std::min(l.span_end(i, child_indent), end), e->colon});
		}
		if (!block || children.empty()) {
			// Not a block mapping (null, a scalar, a flow mapping): a new mapping, as mappingChild makes.
			std::string head = l.v[begin].substr(0, t->entry.colon + 1);
			l.v.erase(l.v.begin() + static_cast<long>(begin), l.v.begin() + static_cast<long>(end));
			l.v.insert(l.v.begin() + static_cast<long>(begin), {head, "    " + crt_line, "    " + key_line});
		} else {
			std::string pad(child_indent, ' ');
			const Child *crt = nullptr, *key = nullptr;
			for (const auto &c : children) {
				if (c.key == "crt" && !crt) crt = &c;
				if (c.key == "key" && !key) key = &c;
			}
			// Edits from the bottom up keep the earlier line numbers valid.
			size_t insert_at = children.back().end;
			std::vector<std::string> missing;
			if (!crt) missing.push_back(pad + crt_line);
			if (!key) missing.push_back(pad + key_line);
			l.v.insert(l.v.begin() + static_cast<long>(insert_at), missing.begin(), missing.end());
			std::vector<const Child *> present;
			if (crt) present.push_back(crt);
			if (key) present.push_back(key);
			std::sort(present.begin(), present.end(), [](const Child *a, const Child *b) { return a->line > b->line; });
			for (const Child *c : present) {
				std::string line = l.v[c->line].substr(0, c->colon + 1) + " " + yaml_string(c->key == "crt" ? cert.crt : cert.key);
				l.v.erase(l.v.begin() + static_cast<long>(c->line), l.v.begin() + static_cast<long>(c->end));
				l.v.insert(l.v.begin() + static_cast<long>(c->line), line);
			}
		}
	}
	std::string out;
	size_t end = l.v.size();
	while (end > 0 && l.v[end - 1].empty()) end--;
	for (size_t i = 0; i < end; i++) out += l.v[i] + "\n";
	return out;
}

// A document that is a sequence: yaml.v3 cannot decode it into the configuration (yaml::parse reads it as empty).
bool sequence_root(std::string_view text)
{
	for (size_t pos = 0; pos < text.size();) {
		size_t nl = text.find('\n', pos);
		std::string_view line = text.substr(pos, nl == std::string_view::npos ? std::string_view::npos : nl - pos);
		pos = nl == std::string_view::npos ? text.size() : nl + 1;
		if (blank_or_comment(line) || document_marker(line)) continue;
		size_t i = indent_of(line);
		return line[i] == '[' || (line[i] == '-' && (i + 1 == line.size() || line[i + 1] == ' ' || line[i + 1] == '\r'));
	}
	return false;
}

} // namespace

std::mutex &server_yaml_mutex()
{
	static std::mutex mutex;
	return mutex;
}

std::optional<TlsSettings> read_tls(const std::string &path)
{
	int err = 0;
	auto data = read_file(path, err);
	if (!data) return std::nullopt;
	std::string error;
	auto root = yaml::parse(*data, error);
	if (!root || sequence_root(*data)) return std::nullopt;
	TlsSettings s;
	s.cert.crt.clear();
	s.cert.key.clear();
	bool ok = true;
	if (auto p = scalar(root->get("proto"), ok)) s.proto = *p;
	if (const yaml::Value *c = root->get("cert")) {
		if (c->kind == yaml::Value::Kind::map) {
			if (auto v = scalar(c->get("crt"), ok)) s.cert.crt = *v;
			if (auto v = scalar(c->get("key"), ok)) s.cert.key = *v;
		} else if (!(c->kind == yaml::Value::Kind::scalar && c->text.empty())) {
			ok = false;
		}
	}
	if (!ok) return std::nullopt;
	return s;
}

bool update_tls(const std::string &proto, const Cert &cert, std::string &error, const std::string &path)
{
	std::lock_guard lock(server_yaml_mutex());
	int err = 0;
	auto data = read_file(path, err);
	if (!data) {
		error = "open " + path + ": " + std::strerror(err);
		return false;
	}
	std::string parse_error;
	if (!yaml::parse(*data, parse_error)) {
		error = "parse " + path + ": " + parse_error;
		return false;
	}
	auto out = edit(*data, proto, cert);
	if (!out) {
		error = "configuration root is not a mapping";
		return false;
	}
	// The edit must read back as what was set.
	auto check = yaml::parse(*out, parse_error);
	const yaml::Value *c = check ? check->get("cert") : nullptr;
	bool ok = true;
	if (!check || scalar(check->get("proto"), ok).value_or("") != proto || !c ||
	    scalar(c->get("crt"), ok).value_or("") != cert.crt || scalar(c->get("key"), ok).value_or("") != cert.key || !ok) {
		error = "configuration edit of " + path + " does not read back";
		return false;
	}
	if (!atomic_write(path, *out, 0600)) {
		error = "write " + path + ": " + std::strerror(errno);
		return false;
	}
	return true;
}

} // namespace nkos::config
