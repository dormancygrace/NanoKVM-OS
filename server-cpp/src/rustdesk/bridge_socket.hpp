#pragma once

// The local sockets of the RustDesk bridge: listeners that only the root-owned daemon may use (socket mode 0600 and
// SO_PEERCRED), and connections read and written with deadlines, as the Go server's net.Conn with bufio.Reader.

#include <chrono>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <string_view>
#include <thread>

namespace nkos::rustdesk::sock {

using Clock = std::chrono::steady_clock;
inline constexpr Clock::time_point no_deadline = Clock::time_point::max();

// A connected socket (non-blocking; every wait polls with its deadline). shutdown() from any thread wakes the
// waits, which then fail.
class Conn {
public:
	explicit Conn(int fd);
	~Conn();
	Conn(const Conn &) = delete;
	Conn &operator=(const Conn &) = delete;

	void shutdown();
	bool read_byte(std::uint8_t &out, Clock::time_point deadline);
	bool read_full(void *out, std::size_t n, Clock::time_point deadline);
	// bufio.Reader.ReadSlice('\n') over a buffer of size bytes: the line with its newline; false at an error, the
	// end, the deadline or a full buffer without a newline.
	bool read_line(std::string &line, std::size_t size, Clock::time_point deadline);
	bool write_all(std::string_view data, Clock::time_point deadline);

private:
	bool fill(Clock::time_point deadline);
	int fd_;
	std::string buf_;
	std::size_t pos_ = 0;
};

// listenSocket: a stale socket file is replaced, a live one or another file is an error (Go's texts). The socket
// is mode 0600. Returns the listening descriptor, or -1 with the error.
int listen_socket(const std::string &path, std::string &error);

// A listener's accept loop on a thread of its own: peers that are not root are closed, the others go to accepted.
// close() ends the loop, closes the listener and removes its socket file (as Go's UnixListener.Close).
class Listener {
public:
	Listener(int fd, std::string path, std::function<void(std::shared_ptr<Conn>)> accepted);
	~Listener();
	void close();

private:
	void run();
	int fd_, wake_;
	std::string path_;
	std::function<void(std::shared_ptr<Conn>)> accepted_;
	std::thread thread_;
	bool closed_ = false;
};

} // namespace nkos::rustdesk::sock
