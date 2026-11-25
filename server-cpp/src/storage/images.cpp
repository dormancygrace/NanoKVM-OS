// The image routes of the virtual drive, as the Go server's service/storage/image.go: the images of /data the
// drive can take, the image on it, whether it is shown as an optical drive, putting one in or taking it out, and
// deleting one. Also the routes' entry point for the whole family.

#include "storage/internal.hpp"
#include "storage/storage.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"

#include <charconv>

namespace nkos::storage {

void add_remote_routes(http::Router &router);
void add_netboot_routes(http::Router &router);
void add_user_disk_routes(http::Router &router);
void add_download_routes(http::Router &router);
void cleanup_remote();
void cleanup_netboot();

namespace {

std::string web_actor(const http::Request &req) { return req.auth ? req.auth->username + " (web)" : std::string(); }

// mountImageLocked: callers hold the service lock.
MountError mount_image_locked(const std::string &name, bool cdrom, bool force, std::string &file)
{
	Service &s = service();
	if (s.remote) return {-2, "Disconnect remote media before mounting another image", true};
	if (s.netboot) return {-2, "Turn off network boot before mounting another image", true};
	if (force && !name.empty()) return {-1, "force is only valid when ejecting an image"};
	file.clear();
	if (!name.empty() && image_file(name, file)) return {-2, "invalid image"};
	return mount_local_image(file, cdrom, force);
}

std::string images_json(const std::vector<std::string> &images, const std::map<std::string, std::int64_t> &sizes)
{
	std::string files = "null";
	if (!images.empty()) {
		files = "[";
		for (std::size_t i = 0; i < images.size(); i++) files += (i ? "," : "") + gojson::quote(images[i]);
		files += "]";
	}
	std::string s = "{";
	bool first = true;
	for (const auto &[path, size] : sizes) {
		s += (first ? "" : ",") + gojson::quote(path) + ":" + std::to_string(size);
		first = false;
	}
	return "{\"files\":" + files + ",\"sizes\":" + s + "}}";
}

} // namespace

std::string images_json_for_check(const std::vector<std::string> &images, const std::map<std::string, std::int64_t> &sizes)
{
	return images_json(images, sizes);
}

void cdrom_answer(http::Response &out)
{
	std::string content;
	if (go::read_file(cdrom_flag, content)) return api::err(out, -1, "read failed");
	std::erase(content, '\n');
	// strconv.ParseInt(s, 10, 64): an optional sign, decimal digits, no overflow.
	std::string_view digits = content;
	bool negative = false;
	if (!digits.empty() && (digits[0] == '+' || digits[0] == '-')) {
		negative = digits[0] == '-';
		digits.remove_prefix(1);
	}
	std::uint64_t magnitude = 0;
	auto [end, ec] = std::from_chars(digits.data(), digits.data() + digits.size(), magnitude);
	bool valid = !digits.empty() && ec == std::errc() && end == digits.data() + digits.size() &&
	             magnitude <= (negative ? 9223372036854775808ull : 9223372036854775807ull);
	if (!valid) return api::err(out, -2, "parse failed");
	std::string value = magnitude == 0 ? "0" : (negative ? "-" : "") + std::to_string(magnitude);
	api::send(out, 0, "success", "{\"cdrom\":" + value + "}");
}

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	add_remote_routes(router);
	router.add("GET", "/api/storage/image", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::vector<std::string> images;
			std::map<std::string, std::int64_t> sizes;
			if (list_images(images, sizes)) return api::err(out, -2, "get images failed");
			api::send(out, 0, "success", images_json(images, sizes));
		});
	}, admin));
	router.add("GET", "/api/storage/image/mounted", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::string image;
			if (auto e = mounted_image(image)) return api::err(out, -2, e.text);
			api::send(out, 0, "success", "{\"file\":" + gojson::quote(image) + "}");
		});
	}, admin));
	router.add("POST", "/api/storage/image/mount", auth::guarded([](http::Request &req, http::Response &res) {
		auto params = api::Params::bind(req);
		bool ok = true, ok2 = true, ok3 = true;
		std::optional<std::string> name;
		std::optional<bool> cdrom, force;
		if (params) {
			name = params->string("file", ok);
			cdrom = params->boolean("cdrom", ok2);
			force = params->boolean("force", ok3);
		}
		if (!params || !ok || !ok2 || !ok3) return api::err(res, -1, "invalid arguments");
		std::string actor = web_actor(req);
		http::defer(req, res, [name = name.value_or(""), cdrom = cdrom.value_or(false), force = force.value_or(false),
		                       actor](http::Response &out) {
			std::string file;
			MountError e;
			{
				std::lock_guard lock(service().mu);
				e = mount_image_locked(name, cdrom, force, file);
			}
			if (e.conflict) return out.json(409, "{\"code\":" + std::to_string(e.code) + ",\"msg\":" + api::quote(e.msg) + "}");
			if (e) return api::err(out, e.code, e.msg);
			api::ok(out);
			record_medium(actor, file);
			log::debug("mount image {} success", file);
		});
	}, admin));
	router.add("GET", "/api/storage/cdrom", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, cdrom_answer);
	}, admin));
	router.add("POST", "/api/storage/image/delete", auth::guarded([](http::Request &req, http::Response &res) {
		auto params = api::Params::bind(req);
		bool ok = true;
		std::optional<std::string> name;
		if (params) name = params->string(json_content_type(req.header("content-type")) ? "file" : "File", ok);
		if (!params || !ok || !name || name->empty()) return api::err(res, -1, "invalid arguments");
		http::defer(req, res, [name = *name](http::Response &out) {
			std::lock_guard lock(service().mu);
			std::string file;
			if (image_file(name, file)) return api::err(out, -2, "invalid arguments");
			if (auto e = remove_image(file)) {
				log::error("failed to remove file {}: {}", file, e.text);
				return api::err(out, -3, e.text);
			}
			api::ok(out);
			log::debug("delete image {} success", file);
		});
	}, admin));
	add_netboot_routes(router);
	add_user_disk_routes(router);
	add_download_routes(router);
}

void cleanup_at_start()
{
	cleanup_netboot();
	cleanup_remote();
}

// --- For the other families ---

std::vector<std::string> images(std::string &error)
{
	std::vector<std::string> list;
	std::map<std::string, std::int64_t> sizes;
	if (auto e = list_images(list, sizes)) error = e.text;
	return list;
}

std::string mounted_image_file(bool &cdrom, std::string &error)
{
	cdrom = false;
	error.clear();
	if (require_active_usb_storage()) {
		error = "USB storage is off: enable it in Settings > USB Gadget";
		return {};
	}
	std::string image;
	if (auto e = mounted_image(image)) {
		error = e.text;
		return image;
	}
	if (image.empty()) return image;
	std::string flag;
	if (auto e = go::read_file(cdrom_flag, flag)) error = e.text;
	cdrom = go::trim_space(flag) == "1";
	return image;
}

std::string mount_image_file(const std::string &name, bool cdrom)
{
	std::string file;
	MountError e;
	{
		std::lock_guard lock(service().mu);
		e = mount_image_locked(name, cdrom, false, file);
	}
	if (!e) record_medium("API", name);
	return e.msg;
}

std::string insert_image_file(const std::string &name, bool cdrom, bool &drive_in_use)
{
	drive_in_use = false;
	std::lock_guard lock(service().mu);
	if (name.empty()) return "invalid image";
	std::string current;
	if (go::read_file(mount_device, current)) return "Cannot read USB storage state";
	if (!go::trim_space(current).empty()) {
		drive_in_use = true;
		return "the virtual drive is in use";
	}
	std::string file;
	MountError e = mount_image_locked(name, cdrom, false, file);
	drive_in_use = e.conflict;
	return e.msg;
}

} // namespace nkos::storage
