#pragma once

// Go's path/filepath and os helpers the extensions family needs, with Go's semantics and error texts.

#include "osupdate/goerr.hpp"

#include <cerrno>
#include <fcntl.h>
#include <optional>
#include <string>
#include <string_view>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::extensions::gopath {

// filepath.Join of a clean directory and a name without separators.
inline std::string join(const std::string &dir, const std::string &name) { return dir == "/" ? "/" + name : dir + "/" + name; }

// filepath.Base.
inline std::string base(std::string_view path)
{
	if (path.empty()) return ".";
	while (path.size() > 1 && path.back() == '/') path.remove_suffix(1);
	if (path == "/") return "/";
	auto slash = path.rfind('/');
	if (slash != std::string_view::npos) path.remove_prefix(slash + 1);
	return std::string(path);
}

// filepath.Ext: from the last dot of the last element.
inline std::string ext(std::string_view path)
{
	for (std::size_t i = path.size(); i > 0 && path[i - 1] != '/'; i--)
		if (path[i - 1] == '.') return std::string(path.substr(i - 1));
	return "";
}

// filepath.Dir for an absolute clean path.
inline std::string dir(const std::string &path)
{
	auto slash = path.rfind('/');
	if (slash == std::string::npos) return ".";
	return slash == 0 ? "/" : path.substr(0, slash);
}

inline std::string path_error(std::string_view op, const std::string &path, int err) { return goerr::path_error(op, path, err); }

// os.ReadFile, with the errno of a failure.
inline std::optional<std::string> read_file(const std::string &path, int &err)
{
	err = 0;
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) {
		err = errno;
		return std::nullopt;
	}
	std::string data;
	char buf[16384];
	while (true) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			err = errno;
			::close(fd);
			return std::nullopt;
		}
		if (n == 0) break;
		data.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return data;
}

// os.MkdirAll: "" or Go's error text.
inline std::string mkdir_all(const std::string &path, mode_t mode)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) return S_ISDIR(st.st_mode) ? "" : path_error("mkdir", path, ENOTDIR);
	std::string up = dir(path);
	if (up != path && up != "/" && up != ".")
		if (auto e = mkdir_all(up, mode); !e.empty()) return e;
	if (::mkdir(path.c_str(), mode) != 0) {
		int e = errno;
		if (::lstat(path.c_str(), &st) == 0 && S_ISDIR(st.st_mode)) return "";
		return path_error("mkdir", path, e);
	}
	return "";
}

} // namespace nkos::extensions::gopath
