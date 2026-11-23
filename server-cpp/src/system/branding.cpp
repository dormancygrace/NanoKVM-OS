#include "system/branding.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/image.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"

#include <glaze/glaze.hpp>
#include <openssl/evp.h>

#include <cerrno>
#include <format>
#include <fstream>
#include <sstream>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::system {

namespace {

constexpr const char *dir = "/etc/kvm/branding";
constexpr const char *banner_style_file = "/etc/kvm/banner-style";
constexpr const char *web_title_file = "/etc/kvm/web-title";
constexpr const char *default_button_color = "#45E9A0";

struct Config {
	std::optional<std::string> style, revision, logoRevision, faviconRevision;
	std::optional<bool> logoCustom, faviconCustom;
	std::optional<std::string> buttonColor, startPage;
};

std::optional<std::string> read_file(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

std::string trim(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

std::string sha256_hex(const std::string &data)
{
	unsigned char sum[32];
	EVP_Digest(data.data(), data.size(), sum, nullptr, EVP_sha256(), nullptr);
	std::string hex;
	for (unsigned char b : sum) hex += std::format("{:02x}", b);
	return hex;
}

// Older configurations: a "custom" style meant a custom logo; a favicon revision meant a custom favicon.
Config read_config()
{
	Config c;
	if (auto data = read_file(std::string(dir) + "/config.json")) (void)glz::read<glz::opts{.error_on_unknown_keys = false}>(c, *data);
	auto set = [](std::optional<std::string> &v) { if (v && v->empty()) v.reset(); };
	set(c.style), set(c.revision), set(c.logoRevision), set(c.faviconRevision), set(c.buttonColor), set(c.startPage);
	if (!c.logoCustom) {
		bool active = c.style.value_or("") == "custom";
		c.logoCustom = active;
		if (active && !c.logoRevision) c.logoRevision = c.revision;
	}
	if (!c.faviconCustom) c.faviconCustom = c.faviconRevision.has_value();
	return c;
}

bool save_config(const Config &c)
{
	::mkdir(dir, 0700);
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = true}>(c, out);
	std::string file = std::string(dir) + "/config.json";
	{
		std::ofstream f(file + ".tmp", std::ios::trunc | std::ios::binary);
		if (!(f << out)) return false;
	}
	::chmod((file + ".tmp").c_str(), 0600);
	return ::rename((file + ".tmp").c_str(), file.c_str()) == 0;
}

std::pair<std::string, bool> asset_status(const char *name, const std::optional<std::string> &revision, bool enabled)
{
	if (!enabled) return {"", false};
	auto data = read_file(std::string(dir) + "/" + name);
	if (!data) return {"", false};
	if (revision) return {*revision, true};
	return {sha256_hex(*data), true};
}

std::optional<std::string> normalize_color(std::string value)
{
	value = trim(value);
	for (auto &c : value) c = static_cast<char>(std::toupper(static_cast<unsigned char>(c)));
	if (value.size() != 7 || value[0] != '#') return std::nullopt;
	for (std::size_t i = 1; i < 7; i++)
		if (!std::isxdigit(static_cast<unsigned char>(value[i]))) return std::nullopt;
	return value;
}

std::string banner_style()
{
	std::string s = trim(read_file(banner_style_file).value_or(""));
	return s == "rainbow" ? "rainbow" : "default";
}

struct ImageMetadata {
	std::string start_page;
};

bool valid_start_page(const std::string &p) { return p == "overview" || p == "screen"; }

std::string start_page(const Config &c)
{
	if (c.startPage && valid_start_page(*c.startPage)) return *c.startPage;
	if (auto data = read_file("/boot/image.json")) {
		ImageMetadata image;
		if (!glz::read<glz::opts{.error_on_unknown_keys = false}>(image, *data) && valid_start_page(image.start_page))
			return image.start_page;
	}
	return "screen";
}

void status(http::Response &res)
{
	Config c = read_config();
	auto [logo_revision, logo_available] = asset_status("logo.png", c.logoRevision, *c.logoCustom);
	auto [favicon_revision, favicon_available] = asset_status("favicon.png", c.faviconRevision, *c.faviconCustom);
	auto color = normalize_color(c.buttonColor.value_or(""));
	std::string title = trim(read_file(web_title_file).value_or(""));
	res.set_header("Cache-Control", "no-store");
	// gin.H: keys in order.
	api::send(res, 0, "success",
	          std::format(R"({{"bannerStyle":{},"buttonColor":{},"customAvailable":{},"customButtonColor":{},"customFaviconAvailable":{},"customLogoAvailable":{},"faviconRevision":{},"logoRevision":{},"revision":{},"startPage":{},"style":{},"title":{}}})",
	                      api::quote(banner_style()), api::quote(color.value_or(default_button_color)), logo_available,
	                      color.has_value(), favicon_available, logo_available, api::quote(favicon_revision),
	                      api::quote(logo_revision), api::quote(logo_revision), api::quote(start_page(c)),
	                      api::quote(logo_available ? "custom" : "connection"), api::quote(title)));
}

void image(http::Request &req, http::Response &res, const char *name)
{
	auto data = read_file(std::string(dir) + "/" + name);
	if (!data) {
		res.status = 404;
		res.done = true;
		return;
	}
	std::string etag = "\"" + sha256_hex(*data) + "\"";
	res.set_header("Cache-Control", "no-cache");
	res.set_header("ETag", etag);
	res.set_header("X-Content-Type-Options", "nosniff");
	if (req.header("if-none-match") == etag) {
		res.status = 304;
		res.done = true;
		return;
	}
	res.status = 200;
	res.content_type = "image/png";
	res.body = std::move(*data);
	res.done = true;
}

void remove(http::Response &res, const char *name, bool favicon)
{
	std::string path = std::string(dir) + "/" + name;
	if (::unlink(path.c_str()) != 0 && errno != ENOENT) return api::err(res, -1, "Could not remove custom image");
	Config c = read_config();
	if (favicon) {
		c.faviconRevision.reset();
		c.faviconCustom = false;
	} else {
		c.logoRevision.reset();
		c.revision.reset();
		c.logoCustom = false;
	}
	if (!save_config(c)) return api::err(res, -1, "Could not reset branding");
	status(res);
}

// A PNG or JPEG of at most 1024 x 1024, made a fresh PNG (no metadata, no other chunks) off the loop; saved and
// answered on the loop, where the other branding changes run.
void upload(http::Request &req, http::Response &res, const char *name, bool favicon)
{
	if (req.body.size() > (2u << 20)) return api::err(res, -1, "Logo must be at most 2 MiB");
	res.pending = true;
	loop::blocking([id = req.id, body = std::move(req.body), name, favicon] {
		std::optional<std::string> png;
		if (auto size = image::size(body); size && size->width >= 1 && size->height >= 1 && size->width <= 1024 &&
		                                     size->height <= 1024)
			if (auto decoded = image::decode(body, 1024, 1024)) png = image::encode_png(*decoded);
		loop::post([id, png = std::move(png), name, favicon] {
			http::Response out;
			if (!png) {
				api::err(out, -1, "Use a PNG or JPEG image up to 1024 x 1024");
				return http::complete(id, std::move(out));
			}
			::mkdir(dir, 0700);
			std::string file = std::string(dir) + "/" + name;
			bool saved = false;
			{
				std::ofstream f(file + ".tmp", std::ios::trunc | std::ios::binary);
				saved = static_cast<bool>(f << *png);
			}
			::chmod((file + ".tmp").c_str(), 0600);
			if (!saved || ::rename((file + ".tmp").c_str(), file.c_str()) != 0) {
				api::err(out, -1, "Could not save logo");
				return http::complete(id, std::move(out));
			}
			Config c = read_config();
			if (favicon) {
				c.faviconRevision = sha256_hex(*png);
				c.faviconCustom = true;
			} else {
				c.logoRevision = sha256_hex(*png);
				c.logoCustom = true;
			}
			if (!save_config(c)) api::err(out, -1, "Could not save branding");
			else status(out);
			http::complete(id, std::move(out));
		});
	});
}

struct ColorReq {
	std::string color;
};
struct StyleReq {
	std::string style;
};
struct PageReq {
	std::string page;
};

} // namespace

void add_branding_routes(http::Router &router)
{
	router.add("GET", "/api/branding", [](http::Request &, http::Response &res) { status(res); });
	router.add("GET", "/api/branding/logo", [](http::Request &req, http::Response &res) { image(req, res, "logo.png"); });
	router.add("GET", "/api/branding/favicon", [](http::Request &req, http::Response &res) { image(req, res, "favicon.png"); });
	auth::Guard admin{{"admin"}};
	router.add("POST", "/api/branding/logo", auth::guarded([](http::Request &req, http::Response &res) { upload(req, res, "logo.png", false); }, admin));
	router.add("POST", "/api/branding/favicon", auth::guarded([](http::Request &req, http::Response &res) { upload(req, res, "favicon.png", true); }, admin));
	router.add("DELETE", "/api/branding/logo", auth::guarded([](http::Request &, http::Response &res) { remove(res, "logo.png", false); }, admin));
	router.add("DELETE", "/api/branding/favicon", auth::guarded([](http::Request &, http::Response &res) { remove(res, "favicon.png", true); }, admin));
	router.add("POST", "/api/branding/button-color", auth::guarded([](http::Request &req, http::Response &res) {
		ColorReq r;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body)) return api::err(res, -1, "Use a six-digit hex color");
		auto color = normalize_color(r.color);
		if (!color) return api::err(res, -1, "Use a six-digit hex color");
		Config c = read_config();
		c.buttonColor = *color;
		if (!save_config(c)) return api::err(res, -1, "Could not save button color");
		status(res);
	}, admin));
	router.add("DELETE", "/api/branding/button-color", auth::guarded([](http::Request &, http::Response &res) {
		Config c = read_config();
		c.buttonColor.reset();
		if (!save_config(c)) return api::err(res, -1, "Could not reset button color");
		status(res);
	}, admin));
	router.add("POST", "/api/branding/banner-style", auth::guarded([](http::Request &req, http::Response &res) {
		StyleReq r;
		std::string style;
		if (req.body.size() <= 1024 && !glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body)) style = trim(r.style);
		if (style != "default" && style != "rainbow") return api::err(res, -1, "Choose the default or rainbow banner");
		if (style == "default") {
			if (::unlink(banner_style_file) != 0 && errno != ENOENT) return api::err(res, -1, "Could not save banner style");
		} else if (!config::atomic_write(banner_style_file, style + "\n", 0644)) {
			return api::err(res, -1, "Could not save banner style");
		}
		status(res);
	}, admin));
	router.add("POST", "/api/branding/start-page", auth::guarded([](http::Request &req, http::Response &res) {
		PageReq r;
		if (req.body.size() > 1024 || glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body) || !valid_start_page(r.page))
			return api::err(res, -1, "Choose the overview or the screen");
		Config c = read_config();
		c.startPage = r.page;
		if (!save_config(c)) return api::err(res, -1, "Could not save the start page");
		status(res);
	}, admin));
}

} // namespace nkos::system
