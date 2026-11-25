// The device's OLED, as the Go server's service/vm/oled.go and oled_layout.go: the sleep time, and the main page
// layout the web editor edits, compiled into the text format kvm_system reads (/etc/kvm/oled_layout, a preview in
// /run/nanokvm/oled_preview; the built-in one is /run/nanokvm/oled_default).

#include "system/oled.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "config/config.hpp"

#include <glaze/glaze.hpp>

#include <cerrno>
#include <cstdint>
#include <format>
#include <fstream>
#include <optional>
#include <set>
#include <sstream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <vector>

namespace nkos::system {

namespace {

constexpr const char *exist_file = "/etc/kvm/oled_exist";
constexpr const char *sleep_file = "/etc/kvm/oled_sleep";
constexpr const char *layout_file = "/etc/kvm/oled_layout";
constexpr const char *preview_file = "/run/nanokvm/oled_preview";
constexpr const char *default_file = "/run/nanokvm/oled_default";
constexpr std::size_t max_items = 128, max_text = 128, max_conditions = 4, max_bytes = 65536;
constexpr int max_pages = 4, low = -128, high = 255;

const std::set<std::string> conditions = {"eth", "wifi", "hdmi", "hid", "stream", "wifi_module", "atx",
                                          "page1", "page2", "page3", "page4"};
const std::set<std::string> variables = {"ip", "eth_ip", "wifi_ip", "hostname", "codec", "width",
                                         "height", "fps", "viewers", "uptime", "time", "date"};

struct Item {
	std::string type;
	int x = 0, y = 0, x2 = 0, y2 = 0, width = 0, height = 0, font = 0;
	std::string align;
	bool fill = false, off = false;
	std::string when, text, data;
};
struct Layout {
	std::string panel;
	int rotation = 0;
	std::string burnin;
	int roamWidth = 0, roamHeight = 0, pages = 0, pageSeconds = 0;
	std::vector<Item> items;
};
struct LayoutRequest {
	Layout layout;
};

std::optional<std::string> read(const char *path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

// strconv.Atoi: optional sign, decimal digits, in range.
std::optional<int> atoi(const std::string &s)
{
	std::size_t start = !s.empty() && (s[0] == '+' || s[0] == '-') ? 1 : 0;
	if (s.size() == start || s.size() > 18 || s.find_first_not_of("0123456789", start) != std::string::npos) return std::nullopt;
	long long v = std::stoll(s);
	if (v < INT32_MIN || v > INT32_MAX) return std::nullopt;
	return static_cast<int>(v);
}

// A {variable:width} has a width of -32 to 32.
bool widths_valid(const std::string &text)
{
	for (std::size_t i = 0; i < text.size(); i++) {
		if (text[i] != '{') continue;
		auto end = text.find('}', i);
		if (end == std::string::npos) continue;
		std::string inner = text.substr(i + 1, end - i - 1);
		auto colon = inner.find(':');
		std::string name = inner.substr(0, colon);
		if (!variables.count(name)) continue;
		if (colon != std::string::npos) {
			std::string number = inner.substr(colon + 1);
			std::size_t digits = number.starts_with("-") ? 1 : 0;
			bool shape = number.size() > digits && number.size() - digits <= 2 &&
			             number.find_first_not_of("0123456789", digits) == std::string::npos;
			auto width = atoi(number);
			if (!shape || !width || *width < -32 || *width > 32) return false;
		}
		i = end;
	}
	return true;
}

const char *flag(bool v) { return v ? "1" : "0"; }

std::optional<std::string> coordinates(std::initializer_list<int> values)
{
	for (int v : values)
		if (v < low || v > high) return "coordinate out of range";
	return std::nullopt;
}

std::optional<std::string> when_field(const std::string &when, std::string &out)
{
	if (when.empty()) return out = "-", std::nullopt;
	std::vector<std::string> names;
	for (std::size_t start = 0;;) {
		auto comma = when.find(',', start);
		names.push_back(when.substr(start, comma == std::string::npos ? std::string::npos : comma - start));
		if (comma == std::string::npos) break;
		start = comma + 1;
	}
	if (names.size() > max_conditions) return std::format("more than {} conditions", max_conditions);
	for (const auto &n : names)
		if (!conditions.count(n.starts_with("!") ? n.substr(1) : n)) return "unknown condition " + api::quote(n);
	out = when;
	return std::nullopt;
}

std::optional<std::string> compile_item(const Item &item, std::string &line)
{
	std::string when;
	if (auto e = when_field(item.when, when)) return e;
	if (item.type == "text") {
		if (item.font != 4 && item.font != 8 && item.font != 16) return "invalid font";
		if (item.align != "left" && item.align != "center" && item.align != "right") return "invalid alignment";
		if (item.width < 0 || item.width > 128) return "invalid text width";
		if (item.text.size() > max_text) return "text too long";
		for (unsigned char c : item.text)
			if (c < ' ' || c > '~') return "text must be printable ASCII";
		if (!widths_valid(item.text)) return "a variable width must be -32 to 32";
		if (auto e = coordinates({item.x, item.y})) return e;
		line = std::format("text {} {} {} {} {} {} {} {}", item.x, item.y, item.font, item.align, item.width, flag(!item.off), when, item.text);
		return std::nullopt;
	}
	if (item.type == "line") {
		if (auto e = coordinates({item.x, item.y, item.x2, item.y2})) return e;
		line = std::format("line {} {} {} {} {} {}", item.x, item.y, item.x2, item.y2, flag(!item.off), when);
		return std::nullopt;
	}
	if (item.type == "rect" || item.type == "image") {
		if (auto e = coordinates({item.x, item.y})) return e;
		if (item.width < 1 || item.width > 128 || item.height < 1 || item.height > 128) return "invalid size";
		if (item.type == "rect") {
			line = std::format("rect {} {} {} {} {} {} {}", item.x, item.y, item.width, item.height, flag(item.fill), flag(!item.off), when);
			return std::nullopt;
		}
		// hex.DecodeString, then the rows must fill the size exactly.
		std::string hex;
		bool valid = item.data.size() % 2 == 0;
		for (char c : item.data) {
			if (!std::isxdigit(static_cast<unsigned char>(c))) valid = false;
			hex += static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
		}
		if (!valid || hex.size() / 2 != static_cast<std::size_t>((item.width + 7) / 8 * item.height))
			return "image data does not match its size";
		line = std::format("image {} {} {} {} {} {} {}", item.x, item.y, item.width, item.height, flag(!item.off), when, hex);
		return std::nullopt;
	}
	return "unknown item " + api::quote(item.type);
}

std::optional<std::string> compile(const Layout &layout, std::string &out)
{
	if (layout.panel != "wide" && layout.panel != "narrow") return "unknown panel";
	if (layout.rotation % 90 != 0 || layout.rotation < 0 || layout.rotation > 270) return "invalid rotation";
	if (layout.items.size() > max_items) return std::format("more than {} items", max_items);
	out = std::format("nanokvm-oled 1 {}\nrotation {}\n", layout.panel, layout.rotation);
	if (layout.burnin.empty() || layout.burnin == "none" || layout.burnin == "shift") {
		out += std::format("burnin {}\n", layout.burnin.empty() ? "none" : layout.burnin);
	} else if (layout.burnin == "roam") {
		if (layout.roamWidth < 1 || layout.roamWidth > 128 || layout.roamHeight < 1 || layout.roamHeight > 128)
			return "invalid roaming block";
		out += std::format("burnin roam {} {}\n", layout.roamWidth, layout.roamHeight);
	} else {
		return "unknown burn-in protection";
	}
	if (layout.pages > 1) {
		if (layout.pages > max_pages || layout.pageSeconds < 1 || layout.pageSeconds > 60) return "invalid pages";
		out += std::format("pages {} {}\n", layout.pages, layout.pageSeconds);
	}
	for (std::size_t i = 0; i < layout.items.size(); i++) {
		std::string line;
		if (auto e = compile_item(layout.items[i], line)) return std::format("item {}: {}", i + 1, *e);
		out += line + "\n";
	}
	if (out.size() > max_bytes) return std::format("the layout is larger than {} bytes", max_bytes);
	return std::nullopt;
}

std::vector<std::string> split(const std::string &s, char sep)
{
	std::vector<std::string> out;
	for (std::size_t start = 0;;) {
		auto at = s.find(sep, start);
		out.push_back(s.substr(start, at == std::string::npos ? std::string::npos : at - start));
		if (at == std::string::npos) break;
		start = at + 1;
	}
	return out;
}

std::string parsed_when(const std::string &field) { return field == "-" ? "" : field; }

std::optional<Layout> parse(const std::string &text)
{
	Layout layout;
	bool header = false;
	for (auto line : split(text, '\n')) {
		if (line.ends_with("\r")) line.pop_back();
		if (line.empty() || line[0] == '#') continue;
		auto f = split(line, ' ');
		if (!header) {
			if (f.size() != 3 || f[0] != "nanokvm-oled" || f[1] != "1") return std::nullopt;
			layout.panel = f[2];
			header = true;
			continue;
		}
		auto numbers = [&](std::size_t count, std::vector<int> &values) {
			if (f.size() < count + 1) return false;
			for (std::size_t i = 0; i < count; i++) {
				auto v = atoi(f[i + 1]);
				if (!v) return false;
				values.push_back(*v);
			}
			return true;
		};
		std::vector<int> v;
		if (f[0] == "rotation") {
			if (!numbers(1, v)) return std::nullopt;
			layout.rotation = v[0];
		} else if (f[0] == "burnin") {
			if (f.size() < 2) return std::nullopt;
			layout.burnin = f[1];
			if (f[1] == "roam") {
				f.erase(f.begin());
				if (!numbers(2, v)) return std::nullopt;
				layout.roamWidth = v[0], layout.roamHeight = v[1];
			}
		} else if (f[0] == "pages") {
			if (!numbers(2, v) || f.size() != 3) return std::nullopt;
			layout.pages = v[0], layout.pageSeconds = v[1];
		} else if (f[0] == "text") {
			if (!numbers(3, v) || f.size() < 8) return std::nullopt;
			auto width = atoi(f[5]);
			if (!width) return std::nullopt;
			Item it{"text", v[0], v[1]};
			it.font = v[2];
			it.align = f[4];
			it.width = *width;
			it.off = f[6] == "0";
			it.when = parsed_when(f[7]);
			for (std::size_t i = 8; i < f.size(); i++) it.text += (i > 8 ? " " : "") + f[i];
			layout.items.push_back(it);
		} else if (f[0] == "line") {
			if (!numbers(4, v) || f.size() != 7) return std::nullopt;
			Item it{"line", v[0], v[1], v[2], v[3]};
			it.off = f[5] == "0";
			it.when = parsed_when(f[6]);
			layout.items.push_back(it);
		} else if (f[0] == "rect") {
			if (!numbers(4, v) || f.size() != 8) return std::nullopt;
			Item it{"rect", v[0], v[1]};
			it.width = v[2], it.height = v[3];
			it.fill = f[5] == "1";
			it.off = f[6] == "0";
			it.when = parsed_when(f[7]);
			layout.items.push_back(it);
		} else if (f[0] == "image") {
			if (!numbers(4, v) || f.size() != 8) return std::nullopt;
			Item it{"image", v[0], v[1]};
			it.width = v[2], it.height = v[3];
			it.off = f[5] == "0";
			it.when = parsed_when(f[6]);
			it.data = f[7];
			layout.items.push_back(it);
		} else {
			return std::nullopt;
		}
	}
	std::string compiled;
	if (!header || compile(layout, compiled)) return std::nullopt;
	return layout;
}

std::optional<Layout> read_layout(const char *path)
{
	auto data = read(path);
	if (!data || data->size() > max_bytes) return std::nullopt;
	return parse(*data);
}

// Go's JSON of proto.OLEDLayout (omitempty fields left out).
std::string layout_json(const Layout &l)
{
	std::string out = std::format(R"({{"panel":{},"rotation":{},"burnin":{})", api::quote(l.panel), l.rotation, api::quote(l.burnin));
	if (l.roamWidth) out += std::format(R"(,"roamWidth":{})", l.roamWidth);
	if (l.roamHeight) out += std::format(R"(,"roamHeight":{})", l.roamHeight);
	if (l.pages) out += std::format(R"(,"pages":{})", l.pages);
	if (l.pageSeconds) out += std::format(R"(,"pageSeconds":{})", l.pageSeconds);
	out += R"(,"items":[)";
	for (std::size_t i = 0; i < l.items.size(); i++) {
		const Item &it = l.items[i];
		out += (i ? "," : "") + std::format(R"({{"type":{},"x":{},"y":{})", api::quote(it.type), it.x, it.y);
		if (it.x2) out += std::format(R"(,"x2":{})", it.x2);
		if (it.y2) out += std::format(R"(,"y2":{})", it.y2);
		if (it.width) out += std::format(R"(,"width":{})", it.width);
		if (it.height) out += std::format(R"(,"height":{})", it.height);
		if (it.font) out += std::format(R"(,"font":{})", it.font);
		if (!it.align.empty()) out += R"(,"align":)" + api::quote(it.align);
		if (it.fill) out += R"(,"fill":true)";
		if (it.off) out += R"(,"off":true)";
		if (!it.when.empty()) out += R"(,"when":)" + api::quote(it.when);
		if (!it.text.empty()) out += R"(,"text":)" + api::quote(it.text);
		if (!it.data.empty()) out += R"(,"data":)" + api::quote(it.data);
		out += "}";
	}
	return out + "]}";
}

// The compiled layout of a request, for this device's panel; false with the answer set.
bool layout_request(http::Request &req, http::Response &res, std::string &text)
{
	LayoutRequest r;
	if (req.body.size() > 256 * 1024 || glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body)) {
		api::err(res, -1, "invalid arguments");
		return false;
	}
	auto built_in = read_layout(default_file);
	if (!built_in) {
		api::err(res, -2, "the OLED is not available");
		return false;
	}
	if (r.layout.panel != built_in->panel) {
		api::err(res, -1, "the layout is for the other panel");
		return false;
	}
	if (auto e = compile(r.layout, text)) {
		api::err(res, -1, "invalid layout: " + *e);
		return false;
	}
	return true;
}

bool write_run_or_etc(const char *path, const std::string &data)
{
	std::string p = path;
	::mkdir(p.substr(0, p.rfind('/')).c_str(), 0755);
	return config::atomic_write(path, data, 0644);
}

} // namespace

void add_oled_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/oled", auth::guarded([](http::Request &, http::Response &res) {
		struct stat st{};
		if (::stat(exist_file, &st) != 0) return api::send(res, 0, "success", R"({"exist":false,"sleep":0})");
		auto data = read(sleep_file);
		if (!data) return api::send(res, 0, "success", R"({"exist":true,"sleep":0})");
		std::string v = *data;
		auto a = v.find_first_not_of(" \t\r\n");
		v = a == std::string::npos ? "" : v.substr(a, v.find_last_not_of(" \t\r\n") - a + 1);
		auto sleep = atoi(v);
		if (!sleep) {
			log::error("failed to parse OLED");
			return api::err(res, -1, "failed to parse OLED config");
		}
		api::send(res, 0, "success", std::format(R"({{"exist":true,"sleep":{}}})", *sleep));
	}, admin));
	router.add("POST", "/api/vm/oled", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<long long> sleep;
		if (params) sleep = params->integer("sleep", ok);
		if (!params || !ok) return api::err(res, -1, "invalid arguments");
		long long s = sleep.value_or(0);
		static const std::set<long long> valid = {-1, 0, 15, 30, 60, 180, 300, 600, 1800, 3600};
		if (!valid.count(s)) return api::err(res, -1, "invalid OLED sleep duration");
		std::ofstream out(sleep_file, std::ios::trunc);
		if (!(out << s)) return api::err(res, -2, "failed to write data");
		api::ok(res);
	}, admin));
	router.add("GET", "/api/vm/oled/layout", auth::guarded([](http::Request &, http::Response &res) {
		auto built_in = read_layout(default_file);
		if (!built_in) return api::send(res, 0, "success", R"({"panel":"","builtIn":null,"saved":null})");
		std::string saved = "null";
		if (auto s = read_layout(layout_file); s && s->panel == built_in->panel) saved = layout_json(*s);
		api::send(res, 0, "success",
		          std::format(R"({{"panel":{},"builtIn":{},"saved":{}}})", api::quote(built_in->panel), layout_json(*built_in), saved));
	}, admin));
	router.add("POST", "/api/vm/oled/layout", auth::guarded([](http::Request &req, http::Response &res) {
		std::string text;
		if (!layout_request(req, res, text)) return;
		if (!write_run_or_etc(layout_file, text)) return api::err(res, -2, "failed to save the layout");
		api::ok(res);
	}, admin));
	router.add("DELETE", "/api/vm/oled/layout", auth::guarded([](http::Request &, http::Response &res) {
		if (::unlink(layout_file) != 0 && errno != ENOENT) return api::err(res, -2, "failed to remove the layout");
		api::ok(res);
	}, admin));
	router.add("POST", "/api/vm/oled/preview", auth::guarded([](http::Request &req, http::Response &res) {
		std::string text;
		if (!layout_request(req, res, text)) return;
		if (!write_run_or_etc(preview_file, text)) return api::err(res, -2, "failed to write the preview");
		api::ok(res);
	}, admin));
	router.add("DELETE", "/api/vm/oled/preview", auth::guarded([](http::Request &, http::Response &res) {
		if (::unlink(preview_file) != 0 && errno != ENOENT) return api::err(res, -2, "failed to end the preview");
		api::ok(res);
	}, admin));
}

} // namespace nkos::system
