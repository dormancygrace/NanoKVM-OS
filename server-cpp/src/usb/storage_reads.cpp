// The virtual drive's read-only answers, as the Go server's service/storage/image.go and service/download: the
// images in /data the drive can take, the image on it, whether it is shown as an optical drive, and whether
// images can be stored. Mounting, deleting and downloads come with the storage family.

#include "usb/storage_reads.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "usb/composition.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <climits>
#include <dirent.h>
#include <fstream>
#include <map>
#include <optional>
#include <sstream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <vector>

namespace nkos::usb {

namespace {

constexpr const char *image_dir = "/data";
constexpr const char *lun = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0";

std::optional<std::string> read(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

// An image the drive may take: a regular .iso or .img under /data, no hidden directory on the way (those are the
// device's own, such as the user disk), no symbolic link (imageFile).
bool image_file(const std::string &path)
{
	std::string lower = path;
	for (auto &c : lower) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	if (!path.starts_with(std::string(image_dir) + "/") || !(lower.ends_with(".iso") || lower.ends_with(".img"))) return false;
	std::string rest = path.substr(std::string(image_dir).size());
	for (std::size_t start = 0; start <= rest.size();) {
		auto slash = rest.find('/', start);
		std::string part = rest.substr(start, slash == std::string::npos ? std::string::npos : slash - start);
		if (part.starts_with(".")) return false;
		if (slash == std::string::npos) break;
		start = slash + 1;
	}
	char resolved[PATH_MAX];
	if (!::realpath(path.c_str(), resolved) || path != resolved) return false;
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode);
}

// filepath.Walk: lexical order, depth first, no link followed; hidden directories below /data skipped.
bool walk(const std::string &dir, std::vector<std::string> &files, std::map<std::string, std::int64_t> &sizes)
{
	DIR *d = ::opendir(dir.c_str());
	if (!d) return false;
	std::vector<std::string> names;
	while (dirent *e = ::readdir(d)) {
		std::string n = e->d_name;
		if (n != "." && n != "..") names.push_back(n);
	}
	::closedir(d);
	std::sort(names.begin(), names.end());
	for (const auto &name : names) {
		std::string path = dir + "/" + name;
		struct stat st{};
		if (::lstat(path.c_str(), &st) != 0) return false;
		if (S_ISDIR(st.st_mode)) {
			if (name.starts_with(".")) continue;
			if (!walk(path, files, sizes)) return false;
		} else if (image_file(path)) {
			files.push_back(path);
			sizes[path] = st.st_size;
		}
	}
	return true;
}

} // namespace

void add_storage_read_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/storage/image", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::vector<std::string> files;
			std::map<std::string, std::int64_t> sizes;
			if (!walk(image_dir, files, sizes)) return api::err(out, -2, "get images failed");
			std::string f = "null", s;
			if (!files.empty()) (void)glz::write_json(files, f);
			(void)glz::write_json(sizes, s);
			api::send(out, 0, "success", "{\"files\":" + f + ",\"sizes\":" + s + "}");
		});
	}, admin));
	router.add("GET", "/api/storage/image/mounted", auth::guarded([](http::Request &, http::Response &res) {
		if (!read("/sys/kernel/config/usb_gadget/g0/bcdDevice")) return api::err(res, -2, "get HID mode failed");
		std::string image;
		if (gadget_mode() != "hid-only") {
			auto content = read(std::string(lun) + "/file");
			if (!content) return api::err(res, -2, "read failed");
			image = *content;
			auto a = image.find_first_not_of(" \t\r\n");
			image = a == std::string::npos ? "" : image.substr(a, image.find_last_not_of(" \t\r\n") - a + 1);
			// The eMMC partition of older images counts as no image.
			if (image == "/dev/mmcblk0p3") image.clear();
		}
		api::send(res, 0, "success", "{\"file\":" + api::quote(image) + "}");
	}, admin));
	router.add("GET", "/api/storage/cdrom", auth::guarded([](http::Request &, http::Response &res) {
		auto content = read(std::string(lun) + "/cdrom");
		if (!content) return api::err(res, -1, "read failed");
		std::string flag = *content;
		std::erase(flag, '\n');
		std::size_t start = !flag.empty() && (flag[0] == '-' || flag[0] == '+') ? 1 : 0;
		if (flag.size() == start || flag.find_first_not_of("0123456789", start) != std::string::npos || flag.size() > 19)
			return api::err(res, -2, "parse failed");
		api::send(res, 0, "success", "{\"cdrom\":" + std::to_string(std::stoll(flag)) + "}");
	}, admin));
	router.add("GET", "/api/download/image/enabled", auth::guarded([](http::Request &, http::Response &res) {
		// access(2) answers EROFS on a read-only mount without writing anything.
		bool enabled = ::access(image_dir, W_OK) == 0;
		api::send(res, 0, "success", std::string("{\"enabled\":") + (enabled ? "true" : "false") + "}");
	}, admin));
}

} // namespace nkos::usb
