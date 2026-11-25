// The user disk, as the Go server's service/storage/userdisk*.go: a writable FAT32 image in a hidden directory of
// /data that the computer sees as a third drive (lun.2) when the USB composition attaches it, for BIOS update files
// and the like; and its files, reached through a private mount of the image (never the gadget's) while the computer
// does not have it. One operation at a time owns the image: a transfer holds it for as long as the client keeps
// the data moving (30 s without progress ends it).

#include "storage/internal.hpp"
#include "storage/storage.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"

#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "http/upload.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <future>
#include <linux/loop.h>
#include <linux/openat2.h>
#include <random>
#include <sys/ioctl.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <sys/syscall.h>
#include <unistd.h>

namespace nkos::storage {

namespace {

constexpr const char *user_disk_label = "NKVMDATA";
constexpr int user_disk_min_mib = 64, user_disk_max_mib = 4096;
constexpr int user_disk_sizes[] = {64, 128, 256, 512, 1024, 2048, 4096};
constexpr std::int64_t max_user_disk_file = (1ll << 32) - 1;
constexpr int user_disk_page_size = 200;
constexpr std::size_t max_opening_body = 1 << 20;
// Mounting waits for the blocking pool and the local disk. Client progress is timed only after open.
constexpr auto user_disk_open_timeout = std::chrono::minutes(5);
constexpr auto user_disk_stall = std::chrono::seconds(30);

const char *err_host = "eject the user disk on the computer, then turn it off in USB settings";
const char *err_busy = "the user disk is busy; wait for the current operation to finish";
const char *err_path = "invalid user disk path";
const char *err_space = "not enough free space on the user disk, or file exceeds the FAT32 limit";

// userDiskMu: a flag, since a transfer takes it on one thread and gives it back on another.
std::mutex disk_mu;
bool disk_busy = false;

bool try_lock_disk()
{
	std::lock_guard lock(disk_mu);
	if (disk_busy) return false;
	disk_busy = true;
	return true;
}

void unlock_disk()
{
	std::lock_guard lock(disk_mu);
	disk_busy = false;
}

bool regular(const std::string &path, struct stat &st) { return ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode); }

std::string trimmed(const std::string &path, Error &err)
{
	std::string data;
	err = go::read_file(path, data);
	return go::trim_space(data);
}

bool user_disk_attached()
{
	Error e;
	std::string v = trimmed(user_disk_lun, e);
	return !e && v == user_disk_image;
}

bool user_disk_enabled()
{
	Error e;
	std::string v = trimmed(user_disk_flag(), e);
	return !e && v == user_disk_image;
}

Error user_disk_mounted(bool &mounted)
{
	mounted = false;
	std::string data;
	if (auto e = go::read_file(mountinfo_path, data)) return e;
	std::string escaped;
	for (char c : user_disk_mountpoint) {
		switch (c) {
		case '\\': escaped += "\\134"; break;
		case ' ': escaped += "\\040"; break;
		case '\t': escaped += "\\011"; break;
		case '\n': escaped += "\\012"; break;
		default: escaped += c;
		}
	}
	for (std::size_t start = 0; start <= data.size();) {
		auto nl = data.find('\n', start);
		auto f = go::fields(std::string_view(data).substr(start, nl == std::string::npos ? std::string::npos : nl - start));
		if (f.size() > 4 && f[4] == escaped) {
			mounted = true;
			return {};
		}
		if (nl == std::string::npos) break;
		start = nl + 1;
	}
	return {};
}

// Fails closed after a failed unmount or a crash: a later file request may retire the stale mount, but the
// composition and a new format never ignore it.
Error user_disk_unmounted()
{
	bool mounted;
	if (auto e = user_disk_mounted(mounted)) return e;
	if (mounted) return Error::of(err_busy, "busy");
	return {};
}

std::string user_disk_size_flag(int mib) { return std::to_string(mib <= 256 ? 1 : 8); }

// os.Chmod/Truncate error texts.
Error create_user_disk(int size_mib)
{
	if (size_mib < user_disk_min_mib || size_mib > user_disk_max_mib)
		return Error::of("size must be between " + std::to_string(user_disk_min_mib) + " and " +
		                 std::to_string(user_disk_max_mib) + " MiB");
	struct stat st{};
	if (::stat(user_disk_dir.c_str(), &st) != 0 || !S_ISDIR(st.st_mode)) {
		if (::mkdir(user_disk_dir.c_str(), 0755) != 0 && errno != EEXIST) return Error::path("mkdir", user_disk_dir, errno);
	}
	int fd;
	std::string tmp;
	if (auto e = go::create_temp(user_disk_dir, ".user-disk-*", fd, tmp)) return e;
	struct Remove {
		std::string path;
		~Remove() { ::unlink(path.c_str()); }
	} remove{tmp};
	if (::ftruncate(fd, static_cast<off_t>(size_mib) * 1024 * 1024) != 0) {
		int e = errno;
		::close(fd);
		return Error::path("truncate", tmp, e);
	}
	if (::close(fd) != 0) return Error::path("close", tmp, errno);
	// BusyBox mkfs.vfat makes FAT32 whatever the size and accepts -F and -s without minding them.
	// A server that stops meanwhile kills the formatter and reaps it: the temporary image goes (remove, above) and
	// the previous disk stays, within the time the service is given to stop. Left running, the formatter would
	// outlive the server and leave its file.
	auto r = process::run({"mkfs.vfat", "-F", "32", "-s", user_disk_size_flag(size_mib), "-n", user_disk_label, tmp},
	                      {.timeout = std::chrono::hours(1), .cancel = loop::stop_flag()});
	if (r.cancelled) return Error::of("the server is stopping");
	if (!r.ok()) {
		std::string why = r.started ? go::exit_text(r.exit_code, r.signal) : "exec: \"mkfs.vfat\": executable file not found in $PATH";
		return Error::of("mkfs.vfat: " + why + ": " + go::trim_space(r.output));
	}
	if (::chmod(tmp.c_str(), 0644) != 0) return Error::path("chmod", tmp, errno);
	return go::rename(tmp, user_disk_image);
}

// The private mount: a loop device (cleared with the mount) of the image, mounted without devices, set-user-ID or
// execution, read-only unless writable. The root directory's descriptor, or an error.
Error mount_user_disk(bool writable, int &root)
{
	if (user_disk_mount_hook) return user_disk_mount_hook(writable, root);
	bool mounted;
	if (auto e = user_disk_mounted(mounted)) return e;
	// A process killed during a transfer leaves its mount: it goes before a new one.
	if (mounted && ::umount2(user_disk_mountpoint.c_str(), 0) != 0) return Error{go::errno_text(errno), errno, {}};
	{
		std::string dir;
		for (std::size_t at = 1; at != std::string::npos;) {
			at = user_disk_mountpoint.find('/', at + 1);
			dir = user_disk_mountpoint.substr(0, at);
			if (::mkdir(dir.c_str(), 0700) != 0 && errno != EEXIST) return Error::path("mkdir", dir, errno);
		}
	}
	struct stat st{};
	if (::lstat(user_disk_mountpoint.c_str(), &st) != 0) return Error::path("lstat", user_disk_mountpoint, errno);
	if (!S_ISDIR(st.st_mode)) return Error::of("invalid user disk mount point");
	int flags = writable ? O_RDWR : O_RDONLY;
	unsigned loop_flags = writable ? LO_FLAGS_AUTOCLEAR : (LO_FLAGS_AUTOCLEAR | LO_FLAGS_READ_ONLY);
	unsigned long mount_flags = MS_NODEV | MS_NOSUID | MS_NOEXEC | (writable ? 0 : MS_RDONLY);
	int image = ::open(user_disk_image.c_str(), flags | O_NOFOLLOW | O_CLOEXEC);
	if (image < 0) return Error::path("open", user_disk_image, errno);
	struct Close {
		int &fd;
		~Close()
		{
			if (fd >= 0) ::close(fd);
		}
	} close_image{image};
	if (::fstat(image, &st) != 0) return Error::path("stat", user_disk_image, errno);
	if (!S_ISREG(st.st_mode)) return Error::of("user disk is not a regular image");
	int control = ::open("/dev/loop-control", O_RDWR | O_CLOEXEC);
	if (control < 0) return Error::path("open", "/dev/loop-control", errno);
	Close close_control{control};
	int loop = -1;
	std::string loop_name;
	// LOOP_CTL_GET_FREE is advisory: another process may claim the device.
	for (int attempt = 0; attempt < 8; attempt++) {
		int number = ::ioctl(control, LOOP_CTL_GET_FREE);
		if (number < 0) return Error{go::errno_text(errno), errno, {}};
		loop_name = "/dev/loop" + std::to_string(number);
		loop = ::open(loop_name.c_str(), flags | O_CLOEXEC);
		if (loop < 0) return Error::path("open", loop_name, errno);
		loop_config config{};
		config.fd = static_cast<__u32>(image);
		config.info.lo_flags = loop_flags;
		if (::ioctl(loop, LOOP_CONFIGURE, &config) == 0) break;
		int e = errno;
		::close(loop);
		loop = -1;
		if (e != EBUSY) return Error{go::errno_text(e), e, {}};
	}
	if (loop < 0) return Error::of("no free loop device");
	// AUTOCLEAR releases the loop device on failure, or after the mount is gone.
	Close close_loop{loop};
	if (::mount(loop_name.c_str(), user_disk_mountpoint.c_str(), "vfat", mount_flags, "utf8=1,umask=0077") != 0)
		return Error::of("mount user disk: " + go::errno_text(errno));
	root = ::open(user_disk_mountpoint.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
	if (root < 0) {
		Error e = Error::path("open", user_disk_mountpoint, errno);
		::umount2(user_disk_mountpoint.c_str(), 0);
		return e;
	}
	return {};
}

// Never a lazy unmount: the composition must wait for a real flush.
Error unmount_user_disk(int root)
{
	if (user_disk_unmount_hook) return user_disk_unmount_hook(root);
	if (root >= 0) ::close(root);
	if (::umount2(user_disk_mountpoint.c_str(), 0) != 0) return Error{go::errno_text(errno), errno, {}};
	return {};
}

Error disk_space(int root, std::uint64_t &free, std::uint64_t &total)
{
	struct statfs st{};
	if (::fstatfs(root, &st) != 0) return Error{go::errno_text(errno), errno, {}};
	free = static_cast<std::uint64_t>(st.f_bavail) * static_cast<std::uint64_t>(st.f_bsize);
	total = static_cast<std::uint64_t>(st.f_blocks) * static_cast<std::uint64_t>(st.f_bsize);
	return {};
}

Error sys(const char *op, const std::string &name)
{
	return Error{std::string(op) + " " + name + ": " + go::errno_text(errno), errno, {}};
}

// os.Root: a name is resolved beneath the root directory; symbolic links inside it are followed, none leads out.
int sys_openat2(int dir, const std::string &path, std::uint64_t flags)
{
	open_how how{};
	how.flags = flags | O_CLOEXEC;
	how.resolve = RESOLVE_BENEATH;
	return static_cast<int>(::syscall(SYS_openat2, dir, path.c_str(), &how, sizeof how));
}

Error beneath_open(int root, const std::string &name, int flags, int &fd)
{
	fd = sys_openat2(root, name, static_cast<std::uint64_t>(flags));
	if (fd < 0) {
		int e = errno;
		return Error{"openat " + name + ": " + (e == EXDEV ? std::string("path escapes from parent") : go::errno_text(e)), e == EXDEV ? EIO : e, {}};
	}
	return {};
}

// The directory holding name (opened beneath root) and name's last element.
struct Parent {
	int fd = -1;
	std::string base;
	Parent() = default;
	Parent(const Parent &) = delete;
	Parent &operator=(const Parent &) = delete;
	~Parent()
	{
		if (fd >= 0) ::close(fd);
	}
};

Error parent_of(int root, const std::string &name, Parent &p)
{
	auto slash = name.rfind('/');
	p.base = slash == std::string::npos ? name : name.substr(slash + 1);
	return beneath_open(root, slash == std::string::npos ? "." : name.substr(0, slash), O_PATH | O_DIRECTORY, p.fd);
}

// Root.Lstat: 0, or the errno.
int root_lstat(int root, const std::string &name, struct stat &st)
{
	Parent p;
	if (auto e = parent_of(root, name, p)) return e.sys;
	return ::fstatat(p.fd, p.base.c_str(), &st, AT_SYMLINK_NOFOLLOW) == 0 ? 0 : errno;
}

// Root.Remove: unlink, then rmdir; the error of rmdir unless it says "not a directory".
Error root_remove(int root, const std::string &name)
{
	Parent p;
	if (auto e = parent_of(root, name, p)) return e;
	if (::unlinkat(p.fd, p.base.c_str(), 0) == 0) return {};
	int e = errno;
	if (::unlinkat(p.fd, p.base.c_str(), AT_REMOVEDIR) == 0) return {};
	int e1 = errno;
	if (e1 != ENOTDIR) e = e1;
	return Error{"remove " + name + ": " + go::errno_text(e), e, {}};
}

// The reason and code of fileError.
std::pair<int, const char *> file_error(const Error &err)
{
	int code = -3;
	const char *reason = "io";
	if (err.tag == "host") return {-2, "host"};
	if (err.tag == "busy") return {-6, "busy"};
	if (err.tag == "path") return {-1, "path"};
	if (err.sys == ENOTEMPTY) reason = "notEmpty";
	else if (err.sys == EEXIST) reason = "exists";
	else if (err.sys == ENOENT) reason = "missing";
	else if (err.tag == "space" || err.sys == ENOSPC) reason = "space";
	return {code, reason};
}

void answer_file_error(http::Response &res, const Error &err)
{
	auto [code, reason] = file_error(err);
	log::warn("user disk files ({}): {}", reason, err.text);
	api::err(res, code, reason);
}

Error path_error() { return Error::of(err_path, "path"); }

// errors.Join of the operation's and the unmount's errors: the first decides the answer.
Error joined(const Error &a, const Error &b)
{
	if (!a) return b;
	if (!b) return a;
	Error e = a;
	e.text += "\n" + b.text;
	return e;
}

// rand.Text: 26 characters of the base32 alphabet.
std::string rand_text()
{
	static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
	std::random_device rd;
	std::string s;
	for (int i = 0; i < 26; i++) s += alphabet[rd() % 32];
	return s;
}

bool query_present(const http::Request &req, std::string_view key)
{
	std::string_view rest = req.query;
	while (!rest.empty()) {
		auto amp = rest.find('&');
		std::string_view pair = rest.substr(0, amp);
		if (http::url_decode(pair.substr(0, pair.find('='))) == key) return true;
		if (amp == std::string_view::npos) break;
		rest = rest.substr(amp + 1);
	}
	return false;
}

// Runs fn on a thread of its own with the disk mounted and the lock taken; the lock and the mount go after.
using Action = std::function<Error(int root, const std::string &name, std::string &data)>;
void user_disk_files(http::Request &req, http::Response &res, bool writable, Action action)
{
	std::string name;
	if (auto e = user_disk_path(req.query_value("path"), name)) return answer_file_error(res, e);
	if (!try_lock_disk()) return answer_file_error(res, Error::of(err_busy, "busy"));
	http::defer(req, res, [writable, name, action = std::move(action)](http::Response &out) {
		struct Unlock {
			~Unlock() { unlock_disk(); }
		} unlock;
		if (auto e = user_disk_available()) return answer_file_error(out, e);
		int root = -1;
		if (auto e = mount_user_disk(writable, root)) return answer_file_error(out, e);
		std::string data;
		Error op = action(root, name, data);
		Error err = joined(op, unmount_user_disk(root));
		if (err) return answer_file_error(out, err);
		api::send(out, 0, "success", data);
	});
}

} // namespace

std::string user_disk_flag() { return usb_flags_dir + "/usb.userdisk"; }

Error user_disk_path(const std::string &value, std::string &name)
{
	if (value.empty() || value == ".") {
		name = ".";
		return {};
	}
	if (!go::valid_utf8(value) || value.starts_with("/") || value.size() > 4096) return path_error();
	for (std::size_t start = 0;;) {
		auto slash = value.find('/', start);
		std::string part = value.substr(start, slash == std::string::npos ? std::string::npos : slash - start);
		bool control = false;
		for (char32_t r : go::runes(part)) control = control || go::is_control(r);
		if (part.empty() || part == "." || part == ".." || go::utf16_length(part) > 255 || part.ends_with(".") ||
		    part.ends_with(" ") || part.find_first_of("\\:*?\"<>|") != std::string::npos || control)
			return path_error();
		if (slash == std::string::npos) break;
		start = slash + 1;
	}
	name = value;
	return {};
}

// A read failure is no permission to mount the image while the computer has it.
Error user_disk_available()
{
	for (const auto &marker : {user_disk_flag(), user_disk_lun}) {
		std::string data;
		if (auto e = go::read_file(marker, data); e && e.sys != ENOENT) return e;
		if (go::trim_space(data) == go::clean(user_disk_image)) return Error::of(err_host, "host");
	}
	return {};
}

std::string user_disk_state_json()
{
	std::string sizes;
	for (int s : user_disk_sizes) sizes += (sizes.empty() ? "" : ",") + std::to_string(s);
	struct stat st{};
	bool exists = regular(user_disk_image, st);
	std::string out = "{\"file\":" + gojson::quote(user_disk_image) + ",\"label\":\"" + user_disk_label + "\",\"sizes\":[" +
	                  sizes + "],\"exists\":" + (exists ? "true" : "false");
	if (exists) {
		if (std::int64_t mib = st.st_size / (1024 * 1024)) out += ",\"sizeMiB\":" + std::to_string(mib);
		out += ",\"modifiedAt\":" + gojson::quote(go::rfc3339(st.st_mtim.tv_sec));
	}
	out += ",\"enabled\":" + std::string(user_disk_enabled() ? "true" : "false") + ",\"attached\":" +
	       (user_disk_attached() ? "true" : "false") + "}";
	return out;
}

Error list_user_disk(int root, const std::string &name, int offset, std::string &json)
{
	int fd;
	if (auto e = beneath_open(root, name, O_RDONLY | O_DIRECTORY, fd)) return e;
	DIR *d = ::fdopendir(fd);
	if (!d) {
		::close(fd);
		return sys("open", name);
	}
	struct Entry {
		std::string name;
		bool directory;
		std::int64_t size;
		std::int64_t modified;
	};
	auto next_name = [&](std::string &n) -> bool {
		while (dirent *e = ::readdir(d)) {
			n = e->d_name;
			if (n != "." && n != "..") return true;
		}
		return false;
	};
	std::string n;
	for (int skip = 0; skip < offset && next_name(n); skip++) {
	}
	std::vector<std::string> names;
	while (static_cast<int>(names.size()) < user_disk_page_size && next_name(n)) names.push_back(n);
	bool more = next_name(n);
	std::vector<Entry> entries;
	for (const auto &e : names) {
		struct stat st{};
		if (::fstatat(::dirfd(d), e.c_str(), &st, AT_SYMLINK_NOFOLLOW) != 0) {
			Error err = sys("lstat", e);
			::closedir(d);
			return err;
		}
		if (!S_ISDIR(st.st_mode) && !S_ISREG(st.st_mode)) continue;
		entries.push_back({e, S_ISDIR(st.st_mode), st.st_size, st.st_mtim.tv_sec});
	}
	::closedir(d);
	std::sort(entries.begin(), entries.end(), [](const Entry &a, const Entry &b) {
		if (a.directory != b.directory) return a.directory;
		return a.name < b.name;
	});
	std::uint64_t free = 0, total = 0;
	Error space = disk_space(root, free, total);
	json = "{\"offset\":" + std::to_string(offset) + ",\"next\":" + (more ? "true" : "false") + ",\"path\":" +
	       gojson::quote(name) + ",\"entries\":[";
	for (std::size_t i = 0; i < entries.size(); i++) {
		const auto &e = entries[i];
		json += std::string(i ? "," : "") + "{\"name\":" + gojson::quote(e.name) + ",\"directory\":" +
		        (e.directory ? "true" : "false") + ",\"size\":" + std::to_string(e.size) + ",\"modifiedAt\":" +
		        gojson::quote(go::rfc3339(e.modified)) + "}";
	}
	json += "],\"free\":" + std::to_string(free) + ",\"total\":" + std::to_string(total) + "}";
	return space;
}

namespace {

// POST /api/storage/user-disk/files/upload: the raw body into a temporary file on the user disk (never into memory
// or /tmp), published only when complete; an existing file is never overwritten.
class UploadSink : public http::BodySink, public std::enable_shared_from_this<UploadSink> {
public:
	UploadSink(std::uint64_t id, std::vector<std::pair<std::string, std::string>> headers, std::string name, std::int64_t size)
		: id_(id), headers_(std::move(headers)), name_(std::move(name)), size_(size)
	{
	}

	// Keep a bounded prefix while the blocking pool mounts the disk and opens the file.
	void start()
	{
		// A peer may already have sent the initial HTTP/2 window or bytes beside the HTTP/1 headers.
		// No more stream credit is granted until the file is ready.
		if (!http::pause_request_body(id_, true)) {
			done_ = true;
			unlock_disk();
			return respond(Error::of("failed to pause upload"));
		}
		paused_ = true;
		last_data_ = SteadyClock::now();
		auto self = shared_from_this();
		loop::blocking([self] {
			Error err = self->open();
			loop::post([self, err] { self->opened(err); });
		});
		watch_stall();
	}

	void data(std::string_view chunk) override
	{
		if (done_) return;
		received_ += static_cast<std::int64_t>(chunk.size());
		if (!writer_) {
			if (chunk.size() > max_opening_body - pending_.size()) return fail(Error::of(err_busy, "busy"));
			pending_.append(chunk);
			return;
		}
		last_data_ = SteadyClock::now();
		// io.LimitReader(body, limit+1): one byte beyond the limit fails the upload.
		if (received_ > limit_) return fail(Error::of(err_space, "space"));
		writer_->write(chunk);
	}

	void end(http::Request &) override
	{
		ended_ = true;
		if (!done_ && writer_) finish();
	}

	void aborted() override
	{
		if (done_) return;
		done_ = true;
		if (writer_) writer_->abort();
		if (!opened_) return;  // opened() releases what the opening took
		auto self = shared_from_this();
		loop::blocking([self] { self->release(); });
	}

private:
	// Blocking thread.
	Error open()
	{
		if (auto e = user_disk_available()) return e;
		if (auto e = mount_user_disk(true, root_)) return e;
		if (name_ == ".") return path_error();
		if (auto e = parent_of(root_, name_, parent_)) return e;
		struct stat st{};
		if (::fstatat(parent_.fd, parent_.base.c_str(), &st, AT_SYMLINK_NOFOLLOW) == 0) return Error{"file exists", EEXIST, {}};
		if (errno != ENOENT) return sys("lstat", name_);
		std::uint64_t free = 0, total = 0;
		if (auto e = disk_space(root_, free, total)) return e;
		limit_ = static_cast<std::int64_t>(std::min<std::uint64_t>(free, max_user_disk_file));
		if (size_ > limit_) return Error::of(err_space, "space");
		temp_ = ".upload-" + rand_text();
		std::string error;
		// Through the parent's descriptor: the name never leaves the root.
		opening_writer_ = http::FileWriter::open("/proc/self/fd/" + std::to_string(parent_.fd) + "/" + temp_, O_CREAT | O_EXCL, 0600, error);
		if (!opening_writer_) return Error{"open " + temp_ + ": " + error, EIO, {}};
		temp_created_ = true;
		return {};
	}

	// Loop thread.
	void opened(const Error &err)
	{
		opened_ = true;
		auto self = shared_from_this();
		if (done_) {
			if (opening_writer_) opening_writer_->abort();
			loop::blocking([self] { self->release(); });
			return;
		}
		if (err) return fail(err);
		writer_ = std::move(opening_writer_);
		writer_->feeds(id_);
		last_data_ = SteadyClock::now();
		if (received_ > limit_) return fail(Error::of(err_space, "space"));
		if (!pending_.empty()) writer_->write(pending_);
		pending_.clear();
		pending_.shrink_to_fit();
		if (!resume_input()) return fail(Error::of("failed to resume upload"));
		if (ended_) finish();
	}

	bool resume_input()
	{
		bool paused = paused_;
		paused_ = false;
		return !paused || !http::request_open(id_) || http::pause_request_body(id_, false);
	}

	void finish()
	{
		done_ = true;
		auto self = shared_from_this();
		writer_->finish([self](const std::string &error) {
			Error err;
			if (!error.empty())
				err = Error{"write " + self->temp_ + ": " + error, error == "No space left on device" ? ENOSPC : EIO, {}};
			else if (self->size_ >= 0 && self->received_ != self->size_)
				err = Error::of("unexpected EOF");
			loop::blocking([self, err] { self->publish(err); });
		});
	}

	// Blocking thread: the existence check again (the disk lock covers the whole session, so neither the computer
	// nor another request can race it), then the rename.
	void publish(Error err)
	{
		writer_.reset();
		if (!err) {
			struct stat st{};
			if (::fstatat(parent_.fd, parent_.base.c_str(), &st, AT_SYMLINK_NOFOLLOW) == 0) err = Error{"file exists", EEXIST, {}};
			else if (errno != ENOENT) err = sys("lstat", name_);
			else if (::renameat(parent_.fd, temp_.c_str(), parent_.fd, parent_.base.c_str()) != 0) err = sys("rename", temp_);
		}
		answer(err);
	}

	// Loop thread.
	void fail(Error err)
	{
		done_ = true;
		resume_input();
		if (!opened_) {
			std::string{}.swap(pending_);
			// The opener owns the mount and file until opened() runs. It alone releases them.
			respond(err);
			return;
		}
		if (writer_) writer_->abort();
		auto self = shared_from_this();
		loop::blocking([self, err] { self->answer(err); });
	}

	// Blocking thread: the temporary file goes, the disk is unmounted and unlocked; the error of the unmount joins.
	Error release()
	{
		writer_.reset();
		opening_writer_.reset();
		if (temp_created_) ::unlinkat(parent_.fd, temp_.c_str(), 0);
		if (parent_.fd >= 0) ::close(parent_.fd);
		parent_.fd = -1;
		Error err;
		if (root_ >= 0) err = unmount_user_disk(root_);
		root_ = -1;
		unlock_disk();
		if (err) log::error("unmount user disk: {}", err.text);
		return err;
	}

	void answer(Error err)
	{
		respond(joined(err, release()));
	}

	void respond(const Error &err)
	{
		http::Response out;
		out.headers = headers_;
		if (err) answer_file_error(out, err);
		else api::ok(out);
		http::complete(id_, std::move(out));
	}

	// A transfer that makes no progress for 30 s fails and gives the disk back.
	void watch_stall()
	{
		std::weak_ptr<UploadSink> weak = weak_from_this();
		loop::after(std::chrono::seconds(5), [weak] {
			auto self = weak.lock();
			if (!self || self->done_) return;
			// Held for the disk, the client is not the one that stalls.
			if (self->writer_ && self->writer_->holding()) self->last_data_ = SteadyClock::now();
			auto elapsed = SteadyClock::now() - self->last_data_;
			if (!self->opened_ && elapsed > user_disk_open_timeout) {
				log::warn("user disk upload timed out opening destination");
				return self->fail(Error::of(err_busy, "busy"));
			}
			if (self->opened_ && elapsed > user_disk_stall) {
				log::warn("user disk upload stalled");
				return self->fail(Error::of("i/o timeout"));
			}
			self->watch_stall();
		});
	}

	std::uint64_t id_;
	bool paused_ = false;  // held while the destination opens
	std::vector<std::pair<std::string, std::string>> headers_;
	std::string name_, temp_, pending_;
	std::int64_t size_, limit_ = INT64_MAX, received_ = 0;
	int root_ = -1;
	bool opened_ = false, done_ = false, ended_ = false, temp_created_ = false;
	std::shared_ptr<http::FileWriter> writer_, opening_writer_;
	Parent parent_;
	SteadyClock::time_point last_data_;
};

std::int64_t content_length(const http::Request &req)
{
	std::string v = req.header("content-length");
	if (v.empty() || v.size() > 18 || v.find_first_not_of("0123456789") != std::string::npos) return -1;
	return std::stoll(v);
}

std::shared_ptr<UploadSink> begin_upload(http::Request &req, http::Response &res)
{
	std::string name;
	if (auto e = user_disk_path(req.query_value("path"), name)) {
		answer_file_error(res, e);
		return nullptr;
	}
	if (!try_lock_disk()) {
		answer_file_error(res, Error::of(err_busy, "busy"));
		return nullptr;
	}
	auto sink = std::make_shared<UploadSink>(req.id, res.headers, name, content_length(req));
	sink->start();
	return sink;
}

// The download of a file: its bytes go out through a stream, at most 1 MiB ahead of the client, the mount held
// until the last one is queued. No progress for 30 s ends it.
void stream_file(std::uint64_t id, std::vector<std::pair<std::string, std::string>> headers, int root, int fd,
                 std::int64_t size, const std::string &name, std::shared_ptr<http::Stream> stream,
                 std::shared_ptr<std::atomic<bool>> revoked)
{
	http::Response out;
	out.headers = std::move(headers);
	out.set_header("Content-Disposition", go::format_media_type("attachment", "filename", go::base(name)));
	out.content_type = "application/octet-stream";
	out.set_header("X-Content-Type-Options", "nosniff");
	out.set_header("Cache-Control", "no-store");
	out.set_header("Content-Length", std::to_string(size));
	out.stream = stream;
	http::complete(id, std::move(out));
	auto ask = [&](std::function<std::pair<bool, std::uint64_t>()> f) {
		auto p = std::make_shared<std::promise<std::pair<bool, std::uint64_t>>>();
		auto fut = p->get_future();
		loop::post([p, f = std::move(f)] { p->set_value(f()); });
		return fut.get();
	};
	std::vector<char> buf(256 << 10);
	std::uint64_t queued = 0, sent = 0;
	auto progress = SteadyClock::now();
	Error err;
	std::int64_t left = size;
	bool gone = false;
	while (left > 0 && !gone && !err) {
		ssize_t n = ::read(fd, buf.data(), static_cast<std::size_t>(std::min<std::int64_t>(left, static_cast<std::int64_t>(buf.size()))));
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) {
			err = n < 0 ? sys("read", name) : Error::of("unexpected EOF");
			break;
		}
		left -= n;
		std::string chunk(buf.data(), static_cast<std::size_t>(n));
		queued += static_cast<std::uint64_t>(n);
		loop::post([stream, chunk = std::move(chunk)]() mutable { stream->write(std::move(chunk)); });
		for (;;) {
			// Over as well when the client left before the response was attached (the stream then never closes),
			// and when the session that asked for the file ended.
			// ... and when the server is stopping: the transfer ends now, so that the disk is unmounted and
			// unlocked before the process exits.
			auto [closed, written] = ask([stream, id, revoked] {
				if (loop::stopping()) stream->abort();
				return std::pair<bool, std::uint64_t>(
				    stream->closed() || revoked->load() || loop::stopping() || !http::request_open(id), stream->written());
			});
			if (closed) {
				gone = true;
				break;
			}
			if (written != sent) {
				sent = written;
				progress = SteadyClock::now();
			}
			if (queued - sent < (1u << 20)) break;
			if (SteadyClock::now() - progress > user_disk_stall) {
				err = Error::of("i/o timeout");
				log::warn("user disk transfer stalled: {}", err.text);
				loop::post([stream] { stream->abort(); });
				break;
			}
			std::this_thread::sleep_for(std::chrono::milliseconds(20));
		}
	}
	if (!err && !gone) loop::post([stream] { stream->end(); });
	else if (err) loop::post([stream] { stream->abort(); });
	::close(fd);
	if (auto e = unmount_user_disk(root)) log::error("user disk transfer: {}", e.text);
	else if (err) log::error("user disk transfer: {}", err.text);
	unlock_disk();
}

} // namespace

namespace {

void list_files(http::Request &req, http::Response &res)
{
	std::string raw = query_present(req, "offset") ? req.query_value("offset") : "0";
	// strconv.Atoi
	std::string_view d = raw;
	bool negative = !d.empty() && d[0] == '-';
	if (!d.empty() && (d[0] == '-' || d[0] == '+')) d.remove_prefix(1);
	bool valid = !d.empty() && d.size() <= 18 && d.find_first_not_of("0123456789") == std::string_view::npos;
	long long offset = valid ? std::stoll(std::string(d)) * (negative ? -1 : 1) : -1;
	if (!valid || offset < 0 || offset > 1000000) return answer_file_error(res, path_error());
	res.set_header("Cache-Control", "no-store");
	user_disk_files(req, res, false, [offset](int root, const std::string &name, std::string &data) {
		return list_user_disk(root, name, static_cast<int>(offset), data);
	});
}

// existsOk=1 lets a folder upload create a tree that is already there; a file of that name is still an error.
void make_directory(http::Request &req, http::Response &res)
{
	bool exists_ok = req.query_value("existsOk") == "1";
	user_disk_files(req, res, true, [exists_ok](int root, const std::string &name, std::string &) -> Error {
		if (name == ".") return path_error();
		Parent p;
		if (auto e = parent_of(root, name, p)) return e;
		if (::mkdirat(p.fd, p.base.c_str(), 0700) == 0) return {};
		Error err = sys("mkdir", name);
		struct stat st{};
		if (exists_ok && err.sys == EEXIST && root_lstat(root, name, st) == 0 && S_ISDIR(st.st_mode)) return {};
		return err;
	});
}

void delete_file(http::Request &req, http::Response &res)
{
	user_disk_files(req, res, true, [](int root, const std::string &name, std::string &) -> Error {
		if (name == ".") return path_error();
		return root_remove(root, name);
	});
}

} // namespace

void add_user_disk_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/storage/user-disk", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) { api::send(out, 0, "success", user_disk_state_json()); });
	}, admin));
	router.add("POST", "/api/storage/user-disk/create", auth::guarded([](http::Request &req, http::Response &res) {
		auto params = api::Params::bind(req);
		bool ok = true;
		std::optional<long long> size;
		if (params) size = params->integer(json_content_type(req.header("content-type")) ? "sizeMiB" : "SizeMiB", ok);
		if (!params || !ok || !size || *size == 0 || *size < INT32_MIN || *size > INT32_MAX)
			return api::err(res, -1, "invalid arguments");
		res.pending = true;
		// On the pool, which the stopping server waits for: a format interrupted by the exit would leave its
		// temporary image behind and no disk.
		loop::blocking([id = req.id, headers = res.headers, size = static_cast<int>(*size)] {
			http::Response out;
			out.headers = headers;
			[&] {
				std::lock_guard lock(service().mu);
				if (!try_lock_disk()) return api::err(out, -6, err_busy);
				struct Unlock {
					~Unlock() { unlock_disk(); }
				} unlock;
				if (user_disk_unmounted()) return api::err(out, -6, "the user disk is mounted locally");
				if (user_disk_available()) return api::err(out, -2, "turn the user disk off before creating it anew");
				if (auto e = create_user_disk(size)) {
					log::error("create user disk: {}", e.text);
					return api::err(out, -3, e.text);
				}
				api::send(out, 0, "success", user_disk_state_json());
			}();
			http::complete(id, std::move(out));
		});
	}, admin));
	router.add("POST", "/api/storage/user-disk/delete", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::lock_guard lock(service().mu);
			if (!try_lock_disk()) return api::err(out, -6, err_busy);
			struct Unlock {
				~Unlock() { unlock_disk(); }
			} unlock;
			if (user_disk_unmounted()) return api::err(out, -6, "the user disk is mounted locally");
			if (user_disk_available()) return api::err(out, -2, "turn the user disk off before deleting it");
			if (auto e = go::remove(user_disk_image); e && e.sys != ENOENT) return api::err(out, -3, "remove failed");
			api::send(out, 0, "success", user_disk_state_json());
		});
	}, admin));
	router.add("GET", "/api/storage/user-disk/files", auth::guarded(list_files, admin));
	router.add("GET", "/api/storage/user-disk/files/download", auth::guarded([](http::Request &req, http::Response &res) {
		std::string name;
		if (auto e = user_disk_path(req.query_value("path"), name)) return answer_file_error(res, e);
		if (!try_lock_disk()) return answer_file_error(res, Error::of(err_busy, "busy"));
		res.pending = true;
		// The response follows its session for as long as it is open, also after the file's last byte is queued
		// for a slow client: sign-out, a revoked or changed account or the token's expiry aborts the stream here
		// on the loop and stops the reader. The registration ends when the stream closes.
		auto stream = std::make_shared<http::Stream>();
		auto revoked = std::make_shared<std::atomic<bool>>(false);
		auto session = auth::join_session(req, [stream, revoked] {
			revoked->store(true);
			stream->abort();
		});
		stream->on_close = [session] { auth::leave_session(session); };
		// On the pool, which the stopping server waits for: the reader holds the mount and the disk's lock, and
		// must have let them go before the process exits. One download runs at a time (the disk's lock).
		loop::blocking([id = req.id, headers = res.headers, name, stream, revoked, session] {
			// A stream that was never attached never closes: an error answered without it, or a client that
			// left first. This runs on the loop after the response of this thread, whichever it was.
			struct Leave {
				std::shared_ptr<http::Stream> stream;
				std::uint64_t session;
				~Leave()
				{
					loop::post([st = stream, s = session] {
						if (!st->wsi_) auth::leave_session(s);
					});
				}
			} leave{stream, session};
			auto fail = [&](Error e, int root) {
				if (root >= 0) e = joined(e, unmount_user_disk(root));
				unlock_disk();
				http::Response out;
				out.headers = headers;
				answer_file_error(out, e);
				http::complete(id, std::move(out));
			};
			if (auto e = user_disk_available()) return fail(e, -1);
			int root = -1;
			if (auto e = mount_user_disk(false, root)) return fail(e, -1);
			int fd;
			if (auto e = beneath_open(root, name, O_RDONLY, fd)) return fail(e, root);
			struct stat st{};
			if (::fstat(fd, &st) != 0) {
				Error e = sys("stat", name);
				::close(fd);
				return fail(e, root);
			}
			if (!S_ISREG(st.st_mode)) {
				::close(fd);
				return fail(path_error(), root);
			}
			stream_file(id, headers, root, fd, st.st_size, name, stream, revoked);
		});
	}, admin));
	auto upload = [](http::Request &req, http::Response &res) -> std::shared_ptr<http::BodySink> { return begin_upload(req, res); };
	router.upload("POST", "/api/storage/user-disk/files/upload", auth::guarded_upload(upload, admin));
	// A request without a body never reaches the upload route: an empty file.
	router.add("POST", "/api/storage/user-disk/files/upload", auth::guarded([](http::Request &req, http::Response &res) {
		auto sink = begin_upload(req, res);
		if (!sink) return;
		res.pending = true;
		if (!req.body.empty()) sink->data(req.body);
		sink->end(req);
	}, admin));
	router.add("POST", "/api/storage/user-disk/files/mkdir", auth::guarded(make_directory, admin));
	router.add("POST", "/api/storage/user-disk/files/delete", auth::guarded(delete_file, admin));
}

// --- For the other families ---

std::optional<std::function<void()>> lock_user_disk_for_composition(std::string &error)
{
	if (!try_lock_disk()) {
		error = err_busy;
		return std::nullopt;
	}
	if (auto e = user_disk_unmounted()) {
		unlock_disk();
		error = e.text;
		return std::nullopt;
	}
	return std::function<void()>(unlock_disk);
}

std::string user_disk_image_path() { return user_disk_image; }

bool user_disk_exists()
{
	struct stat st{};
	return regular(user_disk_image, st);
}

} // namespace nkos::storage
