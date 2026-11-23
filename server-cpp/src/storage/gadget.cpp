// The virtual drive and the images of /data, as the Go server's service/storage/image.go and image_guard.go: which
// files may go on the drive, what is on it, putting an image in or taking it out (the drive's type and name, the
// reconnection the computer needs when they change), and the check that an image being replaced or deleted is in
// no drive. Also the cancellation tokens the remote sources share.

#include "storage/internal.hpp"

#include "common/log.hpp"
#include "input/hid.hpp"
#include "system/journal.hpp"

#include <algorithm>
#include <cerrno>
#include <climits>
#include <cstdlib>
#include <dirent.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>

namespace nkos::storage {

// --- Context, Event ---

std::shared_ptr<Context> Context::make(std::shared_ptr<Context> parent, SteadyClock::time_point deadline)
{
	auto c = std::make_shared<Context>();
	c->deadline_ = deadline;
	if (parent) {
		c->deadline_ = std::min(deadline, parent->deadline_);
		c->parent_ = parent;
		std::weak_ptr<Context> weak = c;
		c->parent_hook_ = parent->on_cancel([weak] {
			if (auto child = weak.lock()) child->cancel();
		});
	}
	return c;
}

void Context::cancel()
{
	std::vector<std::pair<std::uint64_t, std::function<void()>>> hooks;
	{
		std::lock_guard lock(mu_);
		if (canceled_) return;
		canceled_ = true;
		hooks.swap(hooks_);
	}
	for (auto &[id, f] : hooks) f();
	if (parent_) parent_->stop(parent_hook_);
}

bool Context::canceled() const
{
	std::lock_guard lock(mu_);
	return canceled_;
}

std::uint64_t Context::on_cancel(std::function<void()> f)
{
	{
		std::lock_guard lock(mu_);
		if (!canceled_) {
			hooks_.emplace_back(next_, std::move(f));
			return next_++;
		}
	}
	f();
	return 0;
}

bool Context::stop(std::uint64_t id)
{
	std::lock_guard lock(mu_);
	for (auto it = hooks_.begin(); it != hooks_.end(); ++it)
		if (it->first == id) {
			hooks_.erase(it);
			return true;
		}
	return false;
}

void Event::fire()
{
	std::vector<std::pair<std::uint64_t, std::function<void()>>> subs;
	{
		std::lock_guard lock(mu_);
		if (fired_) return;
		fired_ = true;
		subs.swap(subs_);
	}
	cv_.notify_all();
	std::lock_guard running(fire_mu_);
	for (auto &[id, f] : subs) f();
}

bool Event::fired() const
{
	std::lock_guard lock(mu_);
	return fired_;
}

void Event::wait()
{
	std::unique_lock lock(mu_);
	cv_.wait(lock, [this] { return fired_; });
}

bool Event::wait_until(SteadyClock::time_point t)
{
	std::unique_lock lock(mu_);
	return cv_.wait_until(lock, t, [this] { return fired_; });
}

std::uint64_t Event::subscribe(std::function<void()> f)
{
	{
		std::lock_guard lock(mu_);
		if (!fired_) {
			subs_.emplace_back(next_, std::move(f));
			return next_++;
		}
	}
	f();
	return 0;
}

void Event::unsubscribe(std::uint64_t id)
{
	{
		std::lock_guard lock(mu_);
		std::erase_if(subs_, [id](const auto &s) { return s.first == id; });
	}
	// A subscriber may be running on the firing thread: it returns before what it uses can go.
	std::lock_guard running(fire_mu_);
}

// --- Gadget ---

std::mutex &gadget_mutex()
{
	static std::mutex m;
	return m;
}

namespace {

std::string read_or_empty(const std::string &path)
{
	std::string data;
	(void)go::read_file(path, data);
	return data;
}

std::string trim_right_newlines(std::string s)
{
	while (!s.empty() && s.back() == '\n') s.pop_back();
	return s;
}

bool is_symlink(const std::string &path)
{
	struct stat st{};
	return ::lstat(path.c_str(), &st) == 0 && S_ISLNK(st.st_mode);
}

} // namespace

Error hid_mode(std::string &mode)
{
	std::string data;
	if (auto e = go::read_file(gadget_dir + "/bcdDevice", data)) return e;
	std::string key = go::trim_space(data);
	static const std::map<std::string, std::string> modes{
		{"0x0510", "normal"}, {"0x0511", "normal"}, {"0x0623", "hid-only"}, {"0x0624", "hid-only"},
		{"0x0710", "normal"}, {"0x0711", "normal"}, {"0x0720", "hid-only"}, {"0x0721", "hid-only"}};
	auto it = modes.find(key);
	if (it == modes.end()) return Error::of("invalid mode flag");
	mode = it->second;
	if (mode == "hid-only") {
		std::string config = gadget_dir + "/configs/c.1";
		DIR *d = ::opendir(config.c_str());
		if (!d) return Error::of("read USB configuration: " + go::path_error("open", config, errno));
		std::vector<std::string> names;
		while (dirent *e = ::readdir(d))
			if (std::string n = e->d_name; n != "." && n != "..") names.push_back(n);
		::closedir(d);
		std::sort(names.begin(), names.end());
		for (const auto &name : names) {
			std::string path = config + "/" + name;
			if (!is_symlink(path)) continue;
			char target[PATH_MAX];
			ssize_t n = ::readlink(path.c_str(), target, sizeof target - 1);
			if (n < 0) return Error::of("read USB function: " + go::path_error("readlink", path, errno));
			std::string function = go::base(std::string(target, static_cast<std::size_t>(n)));
			for (const char *p : {"rndis.", "ncm.", "mass_storage.", "uac1."})
				if (function.starts_with(p)) {
					mode = "normal";
					return {};
				}
		}
	}
	return {};
}

std::string inquiry(const std::string &kind)
{
	auto valid = [](const std::string &s, std::size_t limit) {
		if (s.size() > limit || go::trim_space(s) != s) return false;
		for (unsigned char b : s)
			if (b < 32 || b > 126) return false;
		return true;
	};
	auto read = [&](const char *name, std::size_t limit) -> std::string {
		int fd = ::open((usb_flags_dir + "/" + name).c_str(), O_RDONLY | O_CLOEXEC);
		if (fd < 0) return {};
		std::string data;
		char buf[64];
		while (data.size() < limit + 2) {
			ssize_t n = ::read(fd, buf, std::min(sizeof buf, limit + 2 - data.size()));
			if (n < 0 && errno == EINTR) continue;
			if (n < 0) {
				::close(fd);
				return {};
			}
			if (n == 0) break;
			data.append(buf, static_cast<std::size_t>(n));
		}
		::close(fd);
		if (data.ends_with('\n')) data.pop_back();
		return valid(data, limit) ? data : std::string();
	};
	std::string vendor = read("usb.storage.vendor", 8), image = read("usb.storage.image", 16),
	            setup = read("usb.storage.setup", 16), user = read("usb.storage.user", 16);
	if (vendor.empty()) vendor = "NanoKVM";
	if (setup.empty()) setup = "Setup Disk";
	if (user.empty()) user = "User Disk";
	std::string product = image;
	if (kind == "setup") product = setup;
	else if (kind == "user") product = user;
	else if (kind == "cdrom") product = product.empty() ? "USB CD/DVD-ROM" : product;
	else if (kind == "remote") product = product.empty() ? "Remote ISO" : product;
	else product = product.empty() ? "USB Mass Storage" : product;
	auto pad = [](std::string s, std::size_t width) {
		std::size_t runes = go::runes(s).size();
		if (runes < width) s.append(width - runes, ' ');
		return s;
	};
	return pad(vendor, 8) + pad(product, 16) + "0520";
}

Error require_active_usb_storage()
{
	std::string udc;
	if (auto e = go::read_file(usb_gadget_udc, udc)) return Error::of("read USB controller binding: " + e.text);
	if (go::trim_space(udc).empty()) return Error::of("USB gadget is not bound");
	struct stat st{};
	if (::lstat(usb_storage_link.c_str(), &st) != 0)
		return Error::of("read USB storage link: " + go::path_error("lstat", usb_storage_link, errno));
	if (!S_ISLNK(st.st_mode)) return Error::of("USB storage function is not linked");
	for (const auto *path : {&mount_device, &forced_eject_device, &ro_flag, &cdrom_flag, &inquiry_string})
		if (::stat(path->c_str(), &st) != 0)
			return Error::of("read USB storage attribute " + go::base(*path) + ": " + go::path_error("stat", *path, errno));
	return {};
}

Error eject_local_image(bool force)
{
	auto e = go::write_file(mount_device, "\n", 0666);
	if (!e || !force) return e;
	if (auto f = go::write_file(forced_eject_device, "1\n", 0666))
		return Error::of("normal eject failed: " + e.text + "; forced eject failed: " + f.text);
	return {};
}

std::string drive_identity()
{
	return go::trim_space(read_or_empty(cdrom_flag)) + "\n" + trim_right_newlines(read_or_empty(inquiry_string));
}

void DriveRestore::operator()() const
{
	for (std::size_t i = 0; i < paths.size(); i++)
		if (auto e = go::write_file(paths[i], old[i], 0666))
			log::error("put the drive's {} back: {}", go::base(paths[i]), e.text);
}

Error set_drive(bool cdrom, const std::string &name, bool &changed, DriveRestore &restore)
{
	std::string flag = cdrom ? "1" : "0";
	DriveRestore r;
	r.paths = {ro_flag, cdrom_flag, inquiry_string};
	std::string before = drive_identity();
	for (const auto &path : r.paths) r.old.push_back(trim_right_newlines(read_or_empty(path)));
	const std::string values[] = {flag, flag, name};
	for (std::size_t i = 0; i < 3; i++)
		if (auto e = go::write_file(r.paths[i], values[i], 0666)) {
			r();
			restore = {};
			changed = false;
			return Error::of(go::base(r.paths[i]) + ": " + e.text);
		}
	changed = drive_identity() != before;
	restore = std::move(r);
	return {};
}

Error reconnect_usb()
{
	std::string controller;
	if (auto e = go::read_file(usb_gadget_udc, controller)) return e;
	if (go::trim_space(controller).empty()) return Error::of("USB gadget is not connected");
	// The HID devices fail while the gadget is unbound and reopen with their next write; open() reopens them at
	// once after the rebind, as the Go server's OpenNoLock.
	struct Reopen {
		~Reopen() { input::hid::open(); }
	} reopen;
	if (auto e = go::write_file(usb_gadget_udc, "\n", 0600)) return e;
	std::this_thread::sleep_for(std::chrono::milliseconds(100));
	return go::write_file(usb_gadget_udc, controller, 0600);
}

bool dvd_supported() { return go::exists(go::dir(mount_device) + "/dvd"); }

Error validate_image(std::uint64_t size)
{
	if (size < 32768 || size > max_image_size || size % 2048 != 0)
		return Error::of("ISO size must be 2048-byte aligned, between 32 KiB and 512 GiB");
	return {};
}

Error validate_optical(std::uint64_t size, bool dvd)
{
	if (auto e = validate_image(size)) return e;
	if (size < 300 * 2048) return Error::of("CD/DVD images must be at least 600 KiB");
	if (size > max_optical_size) return Error::of("CD/DVD images cannot exceed 31.625 GiB");
	if (!dvd && size > legacy_optical_size) return Error::of("Update the device kernel to enable large DVD images");
	return {};
}

MountError mount_local_image(const std::string &file, bool cdrom, bool force)
{
	std::unique_lock h(gadget_mutex());
	if (auto e = require_active_usb_storage()) {
		log::warn("USB storage is unavailable: {}", e.text);
		return {-3, "Enable USB storage before mounting an image"};
	}
	if (!file.empty() && cdrom) {
		struct stat st{};
		if (::stat(file.c_str(), &st) != 0 || !S_ISREG(st.st_mode)) return {-2, "Cannot read the selected optical image"};
		if (auto e = validate_optical(static_cast<std::uint64_t>(st.st_size), dvd_supported())) return {-2, e.text};
	}
	bool replaced = !go::trim_space(read_or_empty(mount_device)).empty();
	if (auto e = eject_local_image(force)) {
		log::error("unmount file failed: {}", e.text);
		return {-4, "unmount image failed"};
	}
	// Without a file the drive stays empty (no medium), and stays what it was: an empty disk or CD drive.
	if (file.empty()) return {};
	std::string kind = cdrom ? "cdrom" : "disk";
	bool changed = false;
	DriveRestore restore;
	if (auto e = set_drive(cdrom, inquiry(kind), changed, restore)) {
		log::error("set the drive to {} failed: {}", kind, e.text);
		return {-2, "set drive mode failed"};
	}
	if (replaced && !changed) {
		// The computer must see the drive empty between two media; keyboard and mouse go on meanwhile.
		h.unlock();
		std::this_thread::sleep_for(medium_swap_pause);
		h.lock();
		if (auto e = require_active_usb_storage()) {
			log::warn("USB storage went away during the medium change: {}", e.text);
			return {-3, "Enable USB storage before mounting an image"};
		}
	}
	// The kernel refuses some files image_file accepts (an empty one).
	if (auto e = go::write_file(mount_device, file, 0666)) {
		log::error("mount file {} failed: {}", file, e.text);
		restore();
		return {-2, "mount image failed"};
	}
	if (!changed) return {};
	if (auto e = reconnect_usb()) {
		log::error("reconnect USB failed: {}", e.text);
		if (!eject_local_image(true)) restore();
		return {-2, "reconnect USB failed"};
	}
	return {};
}

Error image_file(const std::string &name, std::string &out)
{
	static const Error invalid = Error::of("invalid image");
	std::string path = go::clean(name);
	std::string lower = go::to_lower(path);
	if (!path.starts_with(image_directory + "/") || !(lower.ends_with(".iso") || lower.ends_with(".img"))) return invalid;
	std::string rest = path.substr(image_directory.size());
	for (std::size_t start = 0;;) {
		auto slash = rest.find('/', start);
		if (rest.compare(start, 1, ".") == 0) return invalid;
		if (slash == std::string::npos) break;
		start = slash + 1;
	}
	char resolved[PATH_MAX];
	if (!::realpath(path.c_str(), resolved) || path != resolved) return invalid;
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0 || !S_ISREG(st.st_mode)) return invalid;
	out = path;
	return {};
}

namespace {

// filepath.Walk below a directory already reported: lexical order, no link followed, hidden directories skipped.
Error walk(const std::string &dir, std::vector<std::string> &images, std::map<std::string, std::int64_t> &sizes)
{
	DIR *d = ::opendir(dir.c_str());
	if (!d) return Error::path("open", dir, errno);
	std::vector<std::string> names;
	errno = 0;
	while (dirent *e = ::readdir(d))
		if (std::string n = e->d_name; n != "." && n != "..") names.push_back(n);
	::closedir(d);
	std::sort(names.begin(), names.end());
	for (const auto &name : names) {
		std::string path = dir + "/" + name;
		struct stat st{};
		if (::lstat(path.c_str(), &st) != 0) return Error::path("lstat", path, errno);
		if (S_ISDIR(st.st_mode)) {
			if (name.starts_with(".")) continue;
			if (auto e = walk(path, images, sizes)) return e;
		} else {
			std::string file;
			if (!image_file(path, file)) {
				images.push_back(path);
				sizes[path] = st.st_size;
			}
		}
	}
	return {};
}

} // namespace

Error list_images(std::vector<std::string> &images, std::map<std::string, std::int64_t> &sizes)
{
	images.clear();
	sizes.clear();
	struct stat st{};
	if (::lstat(image_directory.c_str(), &st) != 0) return Error::path("lstat", image_directory, errno);
	if (!S_ISDIR(st.st_mode)) {
		std::string file;
		if (!image_file(image_directory, file)) {
			images.push_back(image_directory);
			sizes[image_directory] = st.st_size;
		}
		return {};
	}
	return walk(image_directory, images, sizes);
}

Error mounted_image(std::string &image)
{
	image.clear();
	std::string mode;
	if (hid_mode(mode)) return Error::of("get HID mode failed");
	if (mode == "hid-only") return {};
	std::string content;
	if (go::read_file(mount_device, content)) return Error::of("read failed");
	image = go::trim_space(content);
	// The eMMC partition of older images counts as no image.
	if (image == "/dev/mmcblk0p3") image.clear();
	return {};
}

namespace {

Error different_image(const std::string &path, const std::string &backing)
{
	if (backing.empty()) return {};
	Error busy = Error::of(go::base(path) + " is in use by USB storage; eject it before deleting or replacing it");
	if (go::equal_fold(go::clean(path), go::clean(backing))) return busy;
	// Hard links and other spellings of a name may be the same file.
	struct stat target{}, source{};
	if (::stat(path.c_str(), &target) != 0) {
		if (errno == ENOENT) return {};
		return Error::path("stat", path, errno);
	}
	if (::stat(backing.c_str(), &source) != 0) {
		if (errno == ENOENT) return {};
		return Error::path("stat", backing, errno);
	}
	if (target.st_dev == source.st_dev && target.st_ino == source.st_ino) return busy;
	return {};
}

void sync_image_dir(const std::string &path)
{
	std::string dir = go::dir(path);
	int fd = ::open(dir.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) {
		log::warn("sync directory of {}: {}", go::base(path), go::path_error("open", dir, errno));
		return;
	}
	if (::fsync(fd) != 0) log::warn("sync directory of {}: {}", go::base(path), go::path_error("sync", dir, errno));
	::close(fd);
}

} // namespace

Error image_unused(const std::string &path)
{
	std::string function = go::dir(go::dir(mount_device));
	DIR *d = ::opendir(function.c_str());
	if (!d && errno != ENOENT) return Error::of("read USB storage state: " + go::path_error("open", function, errno));
	std::vector<std::string> luns;
	if (d) {
		while (dirent *e = ::readdir(d)) {
			std::string n = e->d_name;
			struct stat st{};
			if (n.starts_with("lun.") && ::lstat((function + "/" + n).c_str(), &st) == 0 && S_ISDIR(st.st_mode))
				luns.push_back(n);
		}
		::closedir(d);
		std::sort(luns.begin(), luns.end());
	}
	for (const auto &lun : luns) {
		std::string data;
		if (auto e = go::read_file(function + "/" + lun + "/file", data)) return Error::of("read USB medium: " + e.text);
		if (auto e = different_image(path, go::trim_space(data))) return e;
	}
	for (const char *marker : {"usb.disk0", "usb.userdisk"}) {
		std::string data;
		if (auto e = go::read_file(usb_flags_dir + "/" + marker, data); e && e.sys != ENOENT)
			return Error::of("read USB configuration: " + e.text);
		if (auto e = different_image(path, go::trim_space(data))) return e;
	}
	return {};
}

Error check_image_unused(const std::string &path)
{
	std::lock_guard h(gadget_mutex());
	return image_unused(path);
}

Error install_image(const std::function<bool()> &canceled, const std::string &temp, const std::string &dest)
{
	{
		std::lock_guard h(gadget_mutex());
		if (auto e = image_unused(dest)) return e;
		if (canceled && canceled()) return Error::of("context canceled", "canceled");
		if (auto e = go::rename(temp, dest)) return e;
	}
	sync_image_dir(dest);
	return {};
}

Error remove_image(const std::string &path)
{
	{
		std::lock_guard h(gadget_mutex());
		if (auto e = image_unused(path)) return e;
		if (auto e = go::remove(path)) return e;
	}
	sync_image_dir(path);
	return {};
}

void record_medium(const std::string &actor, const std::string &image)
{
	if (image.empty()) return system::journal::action("action.media-ejected", {}, actor);
	system::journal::action("action.media-inserted", {{"image", go::base(image)}}, actor);
}

bool json_content_type(const std::string &content_type)
{
	std::string type = go::to_lower(content_type);
	return go::trim_space(type.substr(0, type.find(';'))) == "application/json";
}

Service &service()
{
	static Service s;
	return s;
}

} // namespace nkos::storage
