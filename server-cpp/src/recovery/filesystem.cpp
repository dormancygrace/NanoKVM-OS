// Settings files under a root, as the Go server's recovery/filesystem.go: every path component opened with
// O_NOFOLLOW (an in-root link must not alias another setting or a protected file), bounded regular files only,
// writes through a private scratch file, fsync and rename, with the directory synced.

#include "recovery/internal.hpp"

#include "osupdate/goerr.hpp"

#include <dirent.h>
#include <fcntl.h>
#include <linux/magic.h>
#include <linux/openat2.h>
#include <openssl/sha.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <sys/syscall.h>
#include <unistd.h>

#include <algorithm>
#include <cerrno>

namespace nkos::recovery {

namespace {

using goerr::errno_text;

constexpr int dir_flags = O_DIRECTORY | O_RDONLY | O_CLOEXEC | O_NOFOLLOW;

struct Fd {
	int fd = -1;
	Fd() = default;
	explicit Fd(int f) : fd(f) {}
	Fd(const Fd &) = delete;
	Fd &operator=(const Fd &) = delete;
	~Fd()
	{
		if (fd >= 0) ::close(fd);
	}
};

// An error of parent(): errno (0 with text set for "unsafe relative path").
struct PathErr {
	int err = 0;
	std::string text;
	std::string message() const { return text.empty() ? errno_text(err) : text; }
};

// parent: the directory holding name's last component, opened without following links; created (0700) on
// the way when create is set.
int parent(const std::string &root, const std::string &name, bool create, std::string &base, PathErr &perr)
{
	if (name.empty() || go_clean(name) != name || name[0] == '/' || name.rfind("../", 0) == 0) {
		perr.text = "unsafe relative path";
		return -1;
	}
	int fd = ::open(root.c_str(), dir_flags);
	if (fd < 0) {
		perr.err = errno;
		return -1;
	}
	std::vector<std::string> parts;
	std::size_t start = 0;
	for (;;) {
		auto slash = name.find('/', start);
		if (slash == std::string::npos) break;
		parts.push_back(name.substr(start, slash - start));
		start = slash + 1;
	}
	base = name.substr(start);
	for (const auto &part : parts) {
		int next = ::openat(fd, part.c_str(), dir_flags);
		int e = next < 0 ? errno : 0;
		if (next < 0 && e == ENOENT && create) {
			e = ::mkdirat(fd, part.c_str(), 0700) == 0 ? 0 : errno;
			if (e == 0) e = ::fsync(fd) == 0 ? 0 : errno;
			if (e == 0 || e == EEXIST) {
				next = ::openat(fd, part.c_str(), dir_flags);
				e = next < 0 ? errno : 0;
			}
		}
		::close(fd);
		if (e) {
			perr.err = e;
			return -1;
		}
		fd = next;
	}
	return fd;
}

bool read_all(int fd, std::size_t limit, std::string &data, int &err)
{
	char buf[16384];
	for (;;) {
		std::size_t want = std::min(sizeof buf, limit + 1 - data.size());
		if (want == 0) return true;
		ssize_t n = ::read(fd, buf, want);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			err = errno;
			return false;
		}
		if (n == 0) return true;
		data.append(buf, static_cast<std::size_t>(n));
	}
}

} // namespace

// path.Clean for slash-separated paths.
std::string go_clean(const std::string &p)
{
	if (p.empty()) return ".";
	bool rooted = p[0] == '/';
	std::vector<std::string> out;
	std::size_t i = 0;
	while (i < p.size()) {
		auto slash = p.find('/', i);
		std::string part = p.substr(i, slash == std::string::npos ? std::string::npos : slash - i);
		i = slash == std::string::npos ? p.size() : slash + 1;
		if (part.empty() || part == ".") continue;
		if (part == "..") {
			if (!out.empty() && out.back() != "..") out.pop_back();
			else if (!rooted) out.push_back("..");
			continue;
		}
		out.push_back(part);
	}
	std::string r = rooted ? "/" : "";
	for (std::size_t k = 0; k < out.size(); k++) r += (k ? "/" : "") + out[k];
	if (r.empty()) return ".";
	return r;
}

std::optional<File> read(const std::string &root, const std::string &name, std::size_t limit, Error &error)
{
	error.clear();
	std::string base;
	PathErr perr;
	Fd dir(parent(root, name, false, base, perr));
	if (dir.fd < 0) {
		if (perr.text.empty() && perr.err == ENOENT) return std::nullopt;
		error = perr.message();
		return std::nullopt;
	}
	int n = ::openat(dir.fd, base.c_str(), O_RDONLY | O_CLOEXEC | O_NOFOLLOW | O_NONBLOCK);
	if (n < 0 && errno == ELOOP && name == "etc/localtime") {
		char target[4096];
		ssize_t size = ::readlinkat(dir.fd, base.c_str(), target, sizeof target);
		if (size < 0) {
			error = errno_text(errno);
			return std::nullopt;
		}
		std::string dest(target, static_cast<std::size_t>(size));
		if (dest.empty() || dest[0] != '/') dest = go_clean("/etc/" + dest);
		dest = go_clean(dest);
		if (dest.rfind("/usr/share/zoneinfo/", 0) != 0) {
			error = "unsafe time zone link";
			return std::nullopt;
		}
		Fd r(::open(root.c_str(), O_DIRECTORY | O_RDONLY | O_CLOEXEC));
		if (r.fd < 0) {
			error = goerr::path_error("open", root, errno);
			return std::nullopt;
		}
		std::string rel = dest.substr(1);
		open_how how{};
		how.flags = O_RDONLY | O_CLOEXEC;
		how.resolve = RESOLVE_BENEATH | RESOLVE_NO_MAGICLINKS;
		int f = static_cast<int>(::syscall(SYS_openat2, r.fd, rel.c_str(), &how, sizeof how));
		if (f < 0) {
			int e = errno;
			error = e == EXDEV ? "openat " + rel + ": path escapes from parent" : goerr::path_error("openat", rel, e);
			return std::nullopt;
		}
		Fd file(f);
		std::string data;
		int err = 0;
		if (!read_all(file.fd, limit, data, err) || data.size() > limit) {
			error = "invalid time zone data";
			return std::nullopt;
		}
		return File{name, std::move(data), 0644};
	}
	if (n < 0) {
		if (errno == ENOENT) return std::nullopt;
		error = errno_text(errno);
		return std::nullopt;
	}
	Fd file(n);
	struct stat st{};
	if (::fstat(file.fd, &st) != 0) {
		error = goerr::path_error("stat", base, errno);
		return std::nullopt;
	}
	if (!S_ISREG(st.st_mode) || static_cast<std::size_t>(st.st_size) > limit) {
		error = "not a bounded regular file: " + name;
		return std::nullopt;
	}
	std::string data;
	int err = 0;
	if (!read_all(file.fd, limit, data, err)) {
		error = goerr::path_error("read", base, err);
		return std::nullopt;
	}
	if (data.size() > limit) {
		error = "file grew beyond limit";
		return std::nullopt;
	}
	return File{name, std::move(data), static_cast<std::uint32_t>(st.st_mode & 0777)};
}

Error write(const std::string &root, const std::string &name, const File *value)
{
	std::string base;
	PathErr perr;
	Fd dir(parent(root, name, value != nullptr, base, perr));
	if (dir.fd < 0) {
		if (!value && perr.text.empty() && perr.err == ENOENT) return "";
		return perr.message();
	}
	struct stat st{};
	int e = ::fstatat(dir.fd, base.c_str(), &st, AT_SYMLINK_NOFOLLOW) == 0 ? 0 : errno;
	if (e == 0 && !S_ISREG(st.st_mode) && !(name == "etc/localtime" && S_ISLNK(st.st_mode))) return "unsafe destination: " + name;
	if (e != 0 && e != ENOENT) return errno_text(e);
	if (!value) {
		if (e == ENOENT) return "";
		if (::unlinkat(dir.fd, base.c_str(), 0) != 0) return errno_text(errno);
		return ::fsync(dir.fd) == 0 ? "" : errno_text(errno);
	}
	// A deterministic private scratch name bounds leftovers after power loss.
	unsigned char sum[SHA256_DIGEST_LENGTH];
	SHA256(reinterpret_cast<const unsigned char *>(base.data()), base.size(), sum);
	static const char hex[] = "0123456789abcdef";
	std::string tmp = ".nkos-recovery-";
	for (int k = 0; k < 8; k++) tmp += hex[sum[k] >> 4], tmp += hex[sum[k] & 15];
	struct stat scratch{};
	if (::fstatat(dir.fd, tmp.c_str(), &scratch, AT_SYMLINK_NOFOLLOW) == 0) {
		if (!S_ISREG(scratch.st_mode)) return "unsafe recovery scratch file";
		if (::unlinkat(dir.fd, tmp.c_str(), 0) != 0) return errno_text(errno);
	} else if (errno != ENOENT) {
		return errno_text(errno);
	}
	int n = ::openat(dir.fd, tmp.c_str(), O_CREAT | O_EXCL | O_WRONLY | O_CLOEXEC | O_NOFOLLOW, 0600);
	if (n < 0) return errno_text(errno);
	Error err;
	const std::string &data = value->data ? *value->data : std::string();
	std::size_t off = 0;
	while (off < data.size()) {
		ssize_t w = ::write(n, data.data() + off, data.size() - off);
		if (w < 0 && errno == EINTR) continue;
		if (w < 0) {
			err = goerr::path_error("write", tmp, errno);
			break;
		}
		off += static_cast<std::size_t>(w);
	}
	if (err.empty()) {
		struct statfs fs{};
		if (::fstatfs(n, &fs) != 0) err = errno_text(errno);
		// VFAT stores no Unix modes; its mount permissions apply to /boot.
		else if (fs.f_type != MSDOS_SUPER_MAGIC && ::fchmod(n, value->mode) != 0) err = goerr::path_error("chmod", tmp, errno);
	}
	if (err.empty() && ::fsync(n) != 0) err = goerr::path_error("sync", tmp, errno);
	if (::close(n) != 0 && err.empty()) err = goerr::path_error("close", tmp, errno);
	if (err.empty() && ::renameat(dir.fd, tmp.c_str(), dir.fd, base.c_str()) != 0) err = errno_text(errno);
	if (err.empty() && ::fsync(dir.fd) != 0) err = errno_text(errno);
	::unlinkat(dir.fd, tmp.c_str(), 0);
	return err;
}

std::optional<std::vector<File>> inventory(const std::string &root, Error &error)
{
	std::vector<std::string> names = settings_list();
	std::size_t visited = 0;
	std::function<Error(const std::string &)> walk = [&](const std::string &d) -> Error {
		std::string base;
		PathErr perr;
		Fd parent_fd(parent(root, d, false, base, perr));
		if (parent_fd.fd < 0) {
			if (perr.text.empty() && perr.err == ENOENT) return "";
			return perr.message();
		}
		int n = ::openat(parent_fd.fd, base.c_str(), dir_flags);
		if (n < 0) {
			if (errno == ENOENT) return "";
			return errno_text(errno);
		}
		DIR *dir = ::fdopendir(n);
		if (!dir) {
			int e = errno;
			::close(n);
			return errno_text(e);
		}
		struct Entry {
			std::string name;
			bool is_dir;
		};
		std::vector<Entry> entries;
		errno = 0;
		while (dirent *ent = ::readdir(dir)) {
			std::string entry = ent->d_name;
			if (entry == "." || entry == "..") continue;
			bool is_dir = ent->d_type == DT_DIR;
			if (ent->d_type == DT_UNKNOWN) {
				struct stat st{};
				is_dir = ::fstatat(::dirfd(dir), entry.c_str(), &st, AT_SYMLINK_NOFOLLOW) == 0 && S_ISDIR(st.st_mode);
			}
			entries.push_back({entry, is_dir});
			if (entries.size() > max_files) break;
		}
		::closedir(dir);
		if (entries.size() > max_files) return "too many settings files";
		for (const auto &entry : entries) {
			visited++;
			if (visited > max_files) return "too many settings entries";
			std::string name = d + "/" + entry.name;
			if (entry.is_dir) {
				if (std::count(name.begin(), name.end(), '/') > 12) return "settings tree too deep";
				if (auto e = walk(name); !e.empty()) return e;
			} else {
				names.push_back(name);
			}
			if (names.size() > max_files) return "too many settings files";
		}
		return "";
	};
	for (const auto &d : settings_trees())
		if (auto e = walk(d); !e.empty()) return error = d + ": " + e, std::nullopt;
	std::sort(names.begin(), names.end());
	std::vector<File> files;
	std::size_t total = 0;
	for (const auto &name : names) {
		Error e;
		auto f = read(root, name, max_file, e);
		if (!e.empty()) return error = name + ": " + e, std::nullopt;
		if (f) {
			total += f->data ? f->data->size() : 0;
			if (total > max_data) return error = "settings exceed archive limit", std::nullopt;
			files.push_back(std::move(*f));
		}
	}
	return files;
}

Error retire_pending(const std::string &root)
{
	std::string base;
	PathErr perr;
	Fd dir(parent(root, pending_path, false, base, perr));
	if (dir.fd < 0) return perr.message();
	if (::renameat(dir.fd, base.c_str(), dir.fd, "failed.json") != 0) return errno_text(errno);
	return ::fsync(dir.fd) == 0 ? "" : errno_text(errno);
}

// Only the known acme.sh program tree goes, never its parent or configuration; nothing is followed, and links or
// other non-regular entries refuse the whole deletion before anything is deleted.
Error remove_acme_binaries(const std::string &root)
{
	struct Entry {
		std::string name;
		bool dir;
	};
	std::vector<Entry> paths;
	std::function<Error(const std::string &)> collect = [&](const std::string &name) -> Error {
		std::string base;
		PathErr perr;
		Fd p(parent(root, name, false, base, perr));
		if (p.fd < 0) {
			if (perr.text.empty() && perr.err == ENOENT) return "";
			return perr.message();
		}
		int n = ::openat(p.fd, base.c_str(), dir_flags);
		if (n < 0) {
			if (errno == ENOENT) return "";
			return errno_text(errno);
		}
		DIR *d = ::fdopendir(n);
		if (!d) {
			int e = errno;
			::close(n);
			return errno_text(e);
		}
		std::vector<std::pair<std::string, unsigned char>> entries;
		while (dirent *ent = ::readdir(d)) {
			std::string entry = ent->d_name;
			if (entry == "." || entry == "..") continue;
			unsigned char type = ent->d_type;
			if (type == DT_UNKNOWN) {
				struct stat st{};
				if (::fstatat(::dirfd(d), entry.c_str(), &st, AT_SYMLINK_NOFOLLOW) == 0)
					type = S_ISDIR(st.st_mode) ? DT_DIR : S_ISREG(st.st_mode) ? DT_REG : DT_LNK;
			}
			entries.emplace_back(entry, type);
			if (entries.size() >= 4097) break;
		}
		::closedir(d);
		paths.push_back({name, true});
		for (const auto &[entry, type] : entries) {
			if (paths.size() > 4096 || std::count(name.begin(), name.end(), '/') > 12) return "excessive acme.sh program tree";
			std::string child = name + "/" + entry;
			if (type == DT_DIR) {
				if (auto e = collect(child); !e.empty()) return e;
			} else {
				if (type != DT_REG) return "unsafe acme.sh program entry";
				paths.push_back({child, false});
			}
		}
		return "";
	};
	if (auto e = collect("etc/kvm/acme/bin"); !e.empty()) return e;
	for (std::size_t i = paths.size(); i-- > 0;) {
		const auto &item = paths[i];
		if (!item.dir) {
			if (auto e = write(root, item.name, nullptr); !e.empty()) return e;
			continue;
		}
		std::string base;
		PathErr perr;
		Fd p(parent(root, item.name, false, base, perr));
		if (p.fd < 0) return perr.message();
		if (::unlinkat(p.fd, base.c_str(), AT_REMOVEDIR) != 0) return errno_text(errno);
		if (::fsync(p.fd) != 0) return errno_text(errno);
	}
	return "";
}

} // namespace nkos::recovery
