#include "ai/files.hpp"

#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <random>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::ai {

std::string errno_text(int e)
{
	std::string s = std::strerror(e);
	if (s.size() > 1 && s[0] >= 'A' && s[0] <= 'Z' && !(s[1] >= 'A' && s[1] <= 'Z')) s[0] = static_cast<char>(s[0] - 'A' + 'a');
	return s;
}

int read_file(const std::string &path, std::string &out)
{
	out.clear();
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return errno;
	char buf[8192];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			int e = errno;
			::close(fd);
			return e;
		}
		if (n == 0) break;
		out.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return 0;
}

std::string save_file(const std::string &path, std::string_view data, const std::string &temp_prefix, const std::string &what)
{
	auto slash = path.rfind('/');
	std::string dir = slash == std::string::npos ? "." : slash == 0 ? "/" : path.substr(0, slash);
	for (std::size_t p = 1; p <= dir.size(); p++) {
		if (p != dir.size() && dir[p] != '/') continue;
		std::string part = dir.substr(0, p);
		if (::mkdir(part.c_str(), 0755) != 0 && errno != EEXIST)
			return "create " + what + " directory: mkdir " + part + ": " + errno_text(errno);
	}
	std::random_device rd;
	std::string tmp;
	int fd = -1;
	for (int attempt = 0; attempt < 10000 && fd < 0; attempt++) {
		tmp = dir + "/" + temp_prefix + std::to_string(rd());
		fd = ::open(tmp.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd < 0 && errno != EEXIST) return "create temporary " + what + ": open " + tmp + ": " + errno_text(errno);
	}
	if (fd < 0) return "create temporary " + what + ": open " + tmp + ": " + errno_text(EEXIST);
	struct Remove {
		std::string path;
		~Remove() { ::unlink(path.c_str()); }
	} remove{tmp};
	auto fail = [&](const char *step, int e) {
		::close(fd);
		return std::string(step) + " " + what + ": " + tmp + ": " + errno_text(e);
	};
	if (::fchmod(fd, 0600) != 0) return fail("set temporary", errno);
	std::size_t done = 0;
	while (done < data.size()) {
		ssize_t n = ::write(fd, data.data() + done, data.size() - done);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) return fail("write temporary", errno);
		done += static_cast<std::size_t>(n);
	}
	if (::fsync(fd) != 0) return fail("sync temporary", errno);
	if (::close(fd) != 0) return "close temporary " + what + ": " + errno_text(errno);
	if (::rename(tmp.c_str(), path.c_str()) != 0) return "replace " + what + ": rename " + tmp + " " + path + ": " + errno_text(errno);
	::chmod(path.c_str(), 0600);
	int dfd = ::open(dir.c_str(), O_RDONLY | O_CLOEXEC);
	if (dfd >= 0) {
		if (::fsync(dfd) != 0) {
			int e = errno;
			::close(dfd);
			return "sync " + what + " directory: " + errno_text(e);
		}
		::close(dfd);
	}
	return {};
}

std::string write_file(const std::string &path, std::string_view data, unsigned mode)
{
	int fd = ::open(path.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, mode);
	if (fd < 0) return "open " + path + ": " + errno_text(errno);
	std::size_t done = 0;
	while (done < data.size()) {
		ssize_t n = ::write(fd, data.data() + done, data.size() - done);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) {
			int e = errno;
			::close(fd);
			return "write " + path + ": " + errno_text(e);
		}
		done += static_cast<std::size_t>(n);
	}
	if (::close(fd) != 0) return "close " + path + ": " + errno_text(errno);
	return {};
}

} // namespace nkos::ai
