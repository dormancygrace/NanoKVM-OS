#pragma once

// File helpers of the update and recovery code, with Go's error texts (os.ReadFile, os.Stat, os.MkdirAll).

#include "osupdate/goerr.hpp"

#include <cerrno>
#include <fcntl.h>
#include <optional>
#include <string>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::osupdate::files {

// os.ReadFile: the contents, or nullopt with err (errno) set.
inline std::optional<std::string> read(const std::string &path, int &err)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) {
		err = errno;
		return std::nullopt;
	}
	std::string data;
	char buf[16384];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n > 0) {
			data.append(buf, static_cast<std::size_t>(n));
			continue;
		}
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			err = errno;
			::close(fd);
			return std::nullopt;
		}
		break;
	}
	::close(fd);
	return data;
}

inline std::optional<std::string> read(const std::string &path)
{
	int err = 0;
	return read(path, err);
}

// os.ReadFile's error text.
inline std::string read_error(const std::string &path, int err)
{
	return goerr::path_error(err == EISDIR ? "read" : "open", path, err);
}

// os.Stat succeeds.
inline bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

// os.MkdirAll: "" on success, else Go's error.
inline std::string mkdir_all(const std::string &path, unsigned mode)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) {
		if (S_ISDIR(st.st_mode)) return "";
		return goerr::path_error("mkdir", path, ENOTDIR);
	}
	auto slash = path.find_last_of('/');
	if (slash != std::string::npos && slash > 0) {
		std::string parent = path.substr(0, slash);
		if (auto e = mkdir_all(parent, mode); !e.empty()) return e;
	}
	if (::mkdir(path.c_str(), mode) != 0) {
		int e = errno;
		if (::lstat(path.c_str(), &st) == 0 && S_ISDIR(st.st_mode)) return "";
		return goerr::path_error("mkdir", path, e);
	}
	return "";
}

// os.Remove: unlink, else rmdir; 0 on success, else the errno Go reports.
inline int remove(const std::string &path)
{
	if (::unlink(path.c_str()) == 0) return 0;
	int e = errno;
	if (::rmdir(path.c_str()) == 0) return 0;
	if (errno != ENOTDIR) e = errno;
	return e;
}

} // namespace nkos::osupdate::files
