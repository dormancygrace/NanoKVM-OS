#include "rustdesk/bridge_socket.hpp"

#include "usb/gotext.hpp"

#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <poll.h>
#include <sys/eventfd.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>

namespace nkos::rustdesk::sock {

namespace {

using usb::gotext::errno_text;

// Waits for events on fd until the deadline: false on a timeout.
bool wait(int fd, short events, Clock::time_point deadline)
{
	for (;;) {
		int timeout = -1;
		if (deadline != no_deadline) {
			auto left = std::chrono::ceil<std::chrono::milliseconds>(deadline - Clock::now()).count();
			if (left <= 0) return false;
			timeout = static_cast<int>(std::min<long long>(left, 60000));
		}
		struct pollfd p{fd, events, 0};
		int n = ::poll(&p, 1, timeout);
		if (n > 0) return true;
		if (n < 0 && errno != EINTR) return false;
	}
}

bool fill_address(const std::string &path, sockaddr_un &addr)
{
	addr = {};
	addr.sun_family = AF_UNIX;
	if (path.size() >= sizeof addr.sun_path) return false;
	std::memcpy(addr.sun_path, path.c_str(), path.size() + 1);
	return true;
}

// net.DialTimeout("unix", path, 200ms) succeeds: something serves the socket.
bool serving(const std::string &path)
{
	sockaddr_un addr;
	if (!fill_address(path, addr)) return false;
	int fd = ::socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
	if (fd < 0) return false;
	bool ok = ::connect(fd, reinterpret_cast<sockaddr *>(&addr), sizeof addr) == 0;
	if (!ok && errno == EINPROGRESS && wait(fd, POLLOUT, Clock::now() + std::chrono::milliseconds(200))) {
		int err = 0;
		socklen_t len = sizeof err;
		ok = ::getsockopt(fd, SOL_SOCKET, SO_ERROR, &err, &len) == 0 && err == 0;
	}
	::close(fd);
	return ok;
}

bool root_peer(int fd)
{
	struct ucred cred{};
	socklen_t len = sizeof cred;
	return ::getsockopt(fd, SOL_SOCKET, SO_PEERCRED, &cred, &len) == 0 && cred.uid == 0;
}

} // namespace

Conn::Conn(int fd) : fd_(fd) { ::fcntl(fd_, F_SETFL, ::fcntl(fd_, F_GETFL) | O_NONBLOCK); }
Conn::~Conn() { ::close(fd_); }
void Conn::shutdown() { ::shutdown(fd_, SHUT_RDWR); }

bool Conn::fill(Clock::time_point deadline)
{
	if (pos_ == buf_.size()) {
		buf_.clear();
		pos_ = 0;
	}
	char tmp[4096];
	for (;;) {
		ssize_t n = ::read(fd_, tmp, sizeof tmp);
		if (n > 0) {
			buf_.append(tmp, static_cast<std::size_t>(n));
			return true;
		}
		if (n == 0) return false;
		if (errno == EINTR) continue;
		if (errno != EAGAIN && errno != EWOULDBLOCK) return false;
		if (!wait(fd_, POLLIN, deadline)) return false;
	}
}

bool Conn::read_byte(std::uint8_t &out, Clock::time_point deadline) { return read_full(&out, 1, deadline); }

bool Conn::read_full(void *out, std::size_t n, Clock::time_point deadline)
{
	auto *p = static_cast<char *>(out);
	while (n > 0) {
		if (pos_ == buf_.size() && !fill(deadline)) return false;
		std::size_t take = std::min(n, buf_.size() - pos_);
		std::memcpy(p, buf_.data() + pos_, take);
		pos_ += take;
		p += take;
		n -= take;
	}
	return true;
}

bool Conn::read_line(std::string &line, std::size_t size, Clock::time_point deadline)
{
	for (;;) {
		std::size_t avail = buf_.size() - pos_;
		std::size_t nl = buf_.find('\n', pos_);
		if (nl != std::string::npos && nl - pos_ < size) {
			line.assign(buf_, pos_, nl + 1 - pos_);
			pos_ = nl + 1;
			return true;
		}
		if (avail >= size) return false;
		if (!fill(deadline)) return false;
	}
}

bool Conn::write_all(std::string_view data, Clock::time_point deadline)
{
	while (!data.empty()) {
		ssize_t n = ::send(fd_, data.data(), data.size(), MSG_NOSIGNAL);
		if (n > 0) {
			data.remove_prefix(static_cast<std::size_t>(n));
			continue;
		}
		if (n < 0 && errno == EINTR) continue;
		if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
			if (!wait(fd_, POLLOUT, deadline)) return false;
			continue;
		}
		return false;
	}
	return true;
}

int listen_socket(const std::string &path, std::string &error)
{
	struct stat st{};
	if (::lstat(path.c_str(), &st) == 0) {
		if (!S_ISSOCK(st.st_mode)) {
			error = path + " is not a socket";
			return -1;
		}
		if (serving(path)) {
			error = path + " is already serving";
			return -1;
		}
		if (::unlink(path.c_str()) != 0) {
			error = "remove " + path + ": " + errno_text(errno);
			return -1;
		}
	} else if (errno != ENOENT) {
		error = "lstat " + path + ": " + errno_text(errno);
		return -1;
	}
	sockaddr_un addr;
	if (!fill_address(path, addr)) {
		error = "listen unix " + path + ": bind: " + errno_text(EINVAL);
		return -1;
	}
	int fd = ::socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
	if (fd < 0) {
		error = "listen unix " + path + ": socket: " + errno_text(errno);
		return -1;
	}
	if (::bind(fd, reinterpret_cast<sockaddr *>(&addr), sizeof addr) != 0) {
		error = "listen unix " + path + ": bind: " + errno_text(errno);
		::close(fd);
		return -1;
	}
	if (::listen(fd, SOMAXCONN) != 0) {
		error = "listen unix " + path + ": listen: " + errno_text(errno);
		::close(fd);
		::unlink(path.c_str());
		return -1;
	}
	if (::chmod(path.c_str(), 0600) != 0) {
		error = "chmod " + path + ": " + errno_text(errno);
		::close(fd);
		::unlink(path.c_str());
		return -1;
	}
	return fd;
}

Listener::Listener(int fd, std::string path, std::function<void(std::shared_ptr<Conn>)> accepted)
	: fd_(fd), wake_(::eventfd(0, EFD_CLOEXEC | EFD_NONBLOCK)), path_(std::move(path)), accepted_(std::move(accepted))
{
	thread_ = std::thread([this] { run(); });
}

Listener::~Listener() { close(); }

void Listener::close()
{
	if (closed_) return;
	closed_ = true;
	std::uint64_t one = 1;
	(void)!::write(wake_, &one, sizeof one);
	if (thread_.joinable()) thread_.join();
	::close(fd_);
	::close(wake_);
	::unlink(path_.c_str());
}

void Listener::run()
{
	for (;;) {
		struct pollfd p[2] = {{fd_, POLLIN, 0}, {wake_, POLLIN, 0}};
		if (::poll(p, 2, -1) < 0) {
			if (errno == EINTR) continue;
			return;
		}
		if (p[1].revents) return;
		int c = ::accept4(fd_, nullptr, nullptr, SOCK_CLOEXEC | SOCK_NONBLOCK);
		if (c < 0) {
			// Out of descriptors or memory: a pause instead of a spin, the listener stays.
			if (errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR && errno != ECONNABORTED)
				std::this_thread::sleep_for(std::chrono::milliseconds(100));
			continue;
		}
		if (!root_peer(c)) {
			::close(c);
			continue;
		}
		accepted_(std::make_shared<Conn>(c));
	}
}

} // namespace nkos::rustdesk::sock
