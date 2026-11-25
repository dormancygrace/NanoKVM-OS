// An image on an SMB share, as the Go server's remotemedia/smb.go, smb_linux.go and smb_relay_linux.go. The kernel's
// CIFS client reads the file inside a child process (this same binary, started with --nanokvm-smb-reader), so that
// a stalled filesystem call cannot hold up the NBD and USB teardown: the child mounts the share privately (fsopen,
// never attached to the mount namespace), opens the one file beneath it and reads blocks into memory it shares with
// this process (a sealed memfd holding the read cache's two buffers). The credentials reach it over a private
// socket and go to the kernel as separate fsconfig values. The child's TCP connection goes through a relay here,
// whose closing breaks a kernel request at once.

#include "storage/internal.hpp"
#include "storage/storage.hpp"

#include "common/log.hpp"
#include "common/process.hpp"

#include <glaze/glaze.hpp>

#include <cerrno>
#include <climits>
#include <csignal>
#include <cstring>
#include <fcntl.h>
#include <linux/mount.h>
#include <linux/openat2.h>
#include <netdb.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/mman.h>
#include <sys/prctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

namespace nkos::storage {

namespace {

constexpr auto smb_timeout = std::chrono::seconds(10);
constexpr const char *smb_helper_arg = "--nanokvm-smb-reader";
enum : std::uint32_t { smb_ok, smb_unavailable, smb_mount_failed, smb_open_failed, smb_changed, smb_read_failed };
const char *cifs_ccm_only = "/sys/module/cifs/parameters/ccm_only";
const char *cifs_ccm_refused = "/sys/module/cifs/parameters/ccm_refused";

Error smb_status_error(std::uint32_t status)
{
	switch (status) {
	case smb_unavailable: return Error::of("Kernel CIFS support is unavailable; update NanoKVM OS");
	case smb_mount_failed: return Error::of("Could not connect or authenticate to the signed SMB2/3 share");
	case smb_open_failed: return Error::of("Could not open a regular SMB ISO inside the share");
	case smb_changed: return Error::of("Remote image changed; disconnect and reconnect it");
	default: return Error::of("SMB ISO read failed or returned an incomplete block");
	}
}

// System errors can carry the remote path or server details: only the context's own end is passed on.
Error smb_error(const Context &ctx, const char *message)
{
	if (ctx.canceled()) return Error::of("context canceled", "context");
	return Error::of(message);
}

bool has_control(std::string_view s)
{
	for (char32_t r : go::runes(s))
		if (go::is_control(r)) return true;
	return false;
}

void put32(unsigned char *p, std::uint32_t v)
{
	for (int i = 0; i < 4; i++) p[i] = static_cast<unsigned char>(v >> (24 - 8 * i));
}
void put64(unsigned char *p, std::uint64_t v)
{
	put32(p, static_cast<std::uint32_t>(v >> 32));
	put32(p + 4, static_cast<std::uint32_t>(v));
}
std::uint32_t get32(const unsigned char *p)
{
	return static_cast<std::uint32_t>(p[0]) << 24 | static_cast<std::uint32_t>(p[1]) << 16 |
	       static_cast<std::uint32_t>(p[2]) << 8 | p[3];
}
std::uint64_t get64(const unsigned char *p) { return static_cast<std::uint64_t>(get32(p)) << 32 | get32(p + 4); }

// Reads or writes all of n bytes before the deadline (time_point::max: none). false on error, end or timeout.
bool io_full(int fd, void *buf, std::size_t n, bool write, SteadyClock::time_point deadline)
{
	auto *p = static_cast<char *>(buf);
	while (n > 0) {
		if (deadline != SteadyClock::time_point::max()) {
			auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - SteadyClock::now()).count();
			if (left <= 0) return false;
			pollfd pfd{fd, static_cast<short>(write ? POLLOUT : POLLIN), 0};
			int r = ::poll(&pfd, 1, static_cast<int>(std::min<long long>(left, INT_MAX)));
			if (r < 0 && errno == EINTR) continue;
			if (r <= 0) return false;
		}
		ssize_t k = write ? ::send(fd, p, n, MSG_NOSIGNAL) : ::read(fd, p, n);
		if (k < 0 && errno == EINTR) continue;
		if (k <= 0) return false;
		p += k;
		n -= static_cast<std::size_t>(k);
	}
	return true;
}

struct MountRequest {
	std::string url, username, password, domain, Address;
};

} // namespace

} // namespace nkos::storage

template <>
struct glz::meta<nkos::storage::MountRequest> {
	using T = nkos::storage::MountRequest;
	static constexpr auto value = object("url", &T::url, "username", &T::username, "password", &T::password, "domain",
	                                     &T::domain, "Address", &T::Address);
};

namespace nkos::storage {

Error parse_smb_options(const SmbOptions &o, std::string &address, std::string &share, std::string &name)
{
	Error invalid = Error::of("Use smb://server/share/path/image.iso without embedded credentials, a query or a fragment");
	auto u = go::parse_url(o.url);
	if (!u || u->scheme != "smb" || u->hostname().empty() || u->has_user || !u->opaque.empty() || !u->raw_query.empty() ||
	    u->force_query || !u->fragment.empty() || o.url.size() > 4096 || !go::valid_utf8(o.url) || !go::valid_utf8(u->path))
		return invalid;
	std::string port = u->port();
	if (port.empty()) port = "445";
	long long n = 0;
	bool numeric = !port.empty() && port.size() < 19;
	for (char c : port) numeric = numeric && c >= '0' && c <= '9';
	if (numeric) n = std::stoll(port);
	std::string host = u->hostname();
	if (!numeric || n < 1 || n > 65535 || host.find_first_of("%\\") != std::string::npos) return invalid;
	std::string path = u->path.starts_with("/") ? u->path.substr(1) : u->path;
	std::vector<std::string> parts;
	for (std::size_t start = 0;;) {
		auto slash = path.find('/', start);
		parts.push_back(path.substr(start, slash == std::string::npos ? std::string::npos : slash - start));
		if (slash == std::string::npos) break;
		start = slash + 1;
	}
	if (parts.size() < 2) return invalid;
	for (const auto &part : parts)
		if (part.empty() || part == "." || part == ".." || part.find_first_of("\\:") != std::string::npos ||
		    part.ends_with(".") || part.ends_with(" ") || has_control(part))
			return invalid;
	const std::string &file = parts.back();
	if (!go::equal_fold(go::ext(file), ".iso") || file.size() > 200) return Error::of("Select an ISO file on the SMB share");
	std::string both = o.username + o.domain;
	if (o.username.empty() || o.username.size() >= 256 || o.domain.size() >= 256 || o.password.size() > 1024 ||
	    !go::valid_utf8(o.username) || !go::valid_utf8(o.domain) || !go::valid_utf8(o.password) ||
	    both.find_first_of("\\/:") != std::string::npos || has_control(both) || o.password.find('\0') != std::string::npos)
		return Error::of("Enter an SMB username and optional domain in their separate fields");
	address = go::join_host_port(host, port);
	share = parts[0];
	name.clear();
	for (std::size_t i = 1; i < parts.size(); i++) name += (i > 1 ? "/" : "") + parts[i];
	return {};
}

Error validate_smb_options(const SmbOptions &o)
{
	std::string a, s, n;
	return parse_smb_options(o, a, s, n);
}

// --- The reader process ---

namespace {

int sys_fsopen(const char *fs, unsigned flags) { return static_cast<int>(::syscall(SYS_fsopen, fs, flags)); }
int sys_fsconfig(int fd, unsigned cmd, const char *key, const void *value, int aux)
{
	return static_cast<int>(::syscall(SYS_fsconfig, fd, cmd, key, value, aux));
}
int sys_fsmount(int fd, unsigned flags, unsigned attrs) { return static_cast<int>(::syscall(SYS_fsmount, fd, flags, attrs)); }
int sys_openat2(int dir, const char *path, open_how *how)
{
	return static_cast<int>(::syscall(SYS_openat2, dir, path, how, sizeof *how));
}

// The open file, or a status.
std::uint32_t mount_smb_once(const MountRequest &o, int &file)
{
	std::string address, share, name;
	if (parse_smb_options({o.url, o.username, o.password, o.domain}, address, share, name)) return smb_open_failed;
	auto colon = o.Address.rfind(':');
	if (colon == std::string::npos || o.Address.substr(0, colon) != "127.0.0.1") return smb_mount_failed;
	std::string ip = "127.0.0.1", port = o.Address.substr(colon + 1);
	int fs = sys_fsopen("cifs", FSOPEN_CLOEXEC);
	if (fs < 0 && (errno == ENODEV || errno == ENOENT)) {
		if (process::run({"modprobe", "cifs"}, {.timeout = std::chrono::seconds(5)}).ok()) fs = sys_fsopen("cifs", FSOPEN_CLOEXEC);
	}
	if (fs < 0) return smb_unavailable;
	const std::pair<const char *, std::string> options[] = {
		{"source", "//" + ip + "/" + share}, {"ip", ip}, {"port", port}, {"username", o.username},
		{"password", o.password}, {"domain", o.domain}, {"vers", "default"}, {"sec", "ntlmsspi"}, {"cache", "none"},
		{"actimeo", "0"}, {"rsize", std::to_string(max_read)}, {"echo_interval", "5"}, {"iocharset", "utf8"}};
	for (const auto &[key, value] : options) {
		// An empty value is left out: an empty password or the default domain, never a guest login.
		if (value.empty()) continue;
		if (sys_fsconfig(fs, FSCONFIG_SET_STRING, key, value.c_str(), 0) != 0) {
			::close(fs);
			return smb_mount_failed;
		}
	}
	for (const char *flag : {"soft", "nosharesock", "nodfs"})
		if (sys_fsconfig(fs, FSCONFIG_SET_FLAG, flag, nullptr, 0) != 0) {
			::close(fs);
			return smb_mount_failed;
		}
	if (sys_fsconfig(fs, FSCONFIG_CMD_CREATE, nullptr, nullptr, 0) != 0) {
		::close(fs);
		return smb_mount_failed;
	}
	int mnt = sys_fsmount(fs, FSMOUNT_CLOEXEC, MOUNT_ATTR_RDONLY | MOUNT_ATTR_NOSUID | MOUNT_ATTR_NODEV | MOUNT_ATTR_NOEXEC);
	::close(fs);
	if (mnt < 0) return smb_mount_failed;
	open_how how{};
	how.flags = O_RDONLY | O_CLOEXEC;
	how.resolve = RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS;
	int fd = sys_openat2(mnt, name.c_str(), &how);
	::close(mnt);
	if (fd < 0) return smb_open_failed;
	file = fd;
	return smb_ok;
}

// The kernel offers AES-128-CCM alone for SMB 3.1.1 encryption (the crypto engine runs all of it); a share that
// insists on another cipher refuses the mount, which the kernel notes in ccm_refused, and only then is the mount
// tried once more with the upstream offer.
std::uint32_t mount_smb_file(const MountRequest &o, int &file)
{
	(void)go::write_file(cifs_ccm_refused, "N", 0);
	std::uint32_t status = mount_smb_once(o, file);
	if (status == smb_ok) return status;
	std::string refused;
	if (go::read_file(cifs_ccm_refused, refused) || refused.empty() || refused[0] != 'Y') return status;
	if (go::write_file(cifs_ccm_only, "N", 0)) return status;
	status = mount_smb_once(o, file);
	(void)go::write_file(cifs_ccm_only, "Y", 0);
	return status;
}

bool shared_range(std::size_t shared, std::uint32_t at, std::uint32_t length)
{
	return length != 0 && length <= max_read && at % max_read + length <= max_read &&
	       static_cast<std::uint64_t>(at) + length <= shared;
}

// Answers a read request (file offset 8 bytes, length 4, offset in the shared memory 4; big-endian) with a 4-byte
// status once the block is in the shared memory.
int serve_smb_helper(int peer, char *shared, std::size_t shared_size)
{
	unsigned char header[16];
	if (!io_full(peer, header, 4, false, SteadyClock::time_point::max())) return 1;
	std::uint32_t length = get32(header);
	if (length == 0 || length > 8192) return 1;
	std::string request(length, '\0');
	if (!io_full(peer, request.data(), length, false, SteadyClock::time_point::max())) return 1;
	MountRequest o;
	auto parsed = glz::read<glz::opts{.error_on_unknown_keys = false}>(o, request);
	std::fill(request.begin(), request.end(), '\0');
	if (parsed) return 1;
	int file = -1;
	std::uint32_t status = mount_smb_file(o, file);
	std::fill(o.password.begin(), o.password.end(), '\0');
	std::uint64_t size = 0;
	struct stat st{};
	if (file >= 0) {
		if (::fstat(file, &st) != 0 || !S_ISREG(st.st_mode) || st.st_size < 0) status = smb_open_failed;
		else size = static_cast<std::uint64_t>(st.st_size);
	}
	put32(header, status);
	put64(header + 4, status == smb_ok ? size : 0);
	if (!io_full(peer, header, 12, true, SteadyClock::time_point::max()) || status != smb_ok) return 1;
	for (;;) {
		if (!io_full(peer, header, 16, false, SteadyClock::time_point::max())) return 1;
		std::uint64_t offset = get64(header);
		std::uint32_t len = get32(header + 8), at = get32(header + 12);
		if (!shared_range(shared_size, at, len) || offset > size || len > size - offset) return 1;
		status = smb_ok;
		// cache=none and actimeo=0: the metadata is the server's, so a changed file is noticed.
		struct stat now{};
		if (::fstat(file, &now) != 0) {
			status = smb_read_failed;
		} else if (now.st_size < 0 || static_cast<std::uint64_t>(now.st_size) != size || now.st_mtim.tv_sec != st.st_mtim.tv_sec ||
		           now.st_mtim.tv_nsec != st.st_mtim.tv_nsec) {
			status = smb_changed;
		} else {
			std::size_t got = 0;
			while (got < len) {
				ssize_t n = ::pread(file, shared + at + got, len - got, static_cast<off_t>(offset + got));
				if (n < 0 && errno == EINTR) continue;
				if (n <= 0) break;
				got += static_cast<std::size_t>(n);
			}
			if (got != len) status = smb_read_failed;
		}
		put32(header, status);
		if (!io_full(peer, header, 4, true, SteadyClock::time_point::max()) || status != smb_ok) return 1;
	}
}

// --- The relay ---

// Connects to host:port before the deadline (net.Dialer.DialContext).
int dial(const std::string &address, SteadyClock::time_point deadline, const Context &ctx)
{
	auto colon = address.rfind(':');
	std::string host = address.substr(0, colon), port = address.substr(colon + 1);
	if (host.size() >= 2 && host.front() == '[') host = host.substr(1, host.size() - 2);
	addrinfo hints{}, *list = nullptr;
	hints.ai_socktype = SOCK_STREAM;
	if (::getaddrinfo(host.c_str(), port.c_str(), &hints, &list) != 0) return -1;
	int result = -1;
	for (addrinfo *a = list; a && result < 0 && !ctx.done(); a = a->ai_next) {
		int fd = ::socket(a->ai_family, a->ai_socktype | SOCK_CLOEXEC | SOCK_NONBLOCK, a->ai_protocol);
		if (fd < 0) continue;
		if (::connect(fd, a->ai_addr, a->ai_addrlen) != 0 && errno != EINPROGRESS) {
			::close(fd);
			continue;
		}
		bool connected = false;
		while (!ctx.canceled()) {
			auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - SteadyClock::now()).count();
			if (left <= 0) break;
			pollfd p{fd, POLLOUT, 0};
			int r = ::poll(&p, 1, static_cast<int>(std::min<long long>(left, 100)));
			if (r < 0 && errno != EINTR) break;
			if (r > 0) {
				int err = 0;
				socklen_t len = sizeof err;
				::getsockopt(fd, SOL_SOCKET, SO_ERROR, &err, &len);
				connected = err == 0;
				break;
			}
		}
		if (!connected) {
			::close(fd);
			continue;
		}
		int flags = ::fcntl(fd, F_GETFL);
		::fcntl(fd, F_SETFL, flags & ~O_NONBLOCK);
		result = fd;
	}
	::freeaddrinfo(list);
	return result;
}

class Relay {
public:
	// The listener's port, or -1.
	int start(const std::string &address, std::shared_ptr<Context> opening)
	{
		upstream_ = dial(address, opening->deadline(), *opening);
		if (upstream_ < 0) return -1;
		listener_ = ::socket(AF_INET, SOCK_STREAM | SOCK_CLOEXEC, 0);
		sockaddr_in in{};
		in.sin_family = AF_INET;
		in.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
		socklen_t len = sizeof in;
		if (listener_ < 0 || ::bind(listener_, reinterpret_cast<sockaddr *>(&in), sizeof in) != 0 ||
		    ::listen(listener_, 4) != 0 || ::getsockname(listener_, reinterpret_cast<sockaddr *>(&in), &len) != 0) {
			close();
			return -1;
		}
		address_ = address;
		opening_ = opening;
		thread_ = std::thread([this] { run(); });
		return ntohs(in.sin_port);
	}

	// Ends the opening phase: no more connections are taken.
	void stop_listening()
	{
		std::lock_guard lock(mu_);
		if (listener_ >= 0) ::shutdown(listener_, SHUT_RDWR);
		listening_ = false;
	}

	void close()
	{
		std::lock_guard lock(mu_);
		closed_ = true;
		listening_ = false;
		for (int fd : {listener_, upstream_, client_})
			if (fd >= 0) ::shutdown(fd, SHUT_RDWR);
	}

	~Relay()
	{
		close();
		if (thread_.joinable()) thread_.join();
		for (int fd : {listener_, upstream_, client_})
			if (fd >= 0) ::close(fd);
	}

private:
	void run()
	{
		for (bool second = false;; second = true) {
			int client = -1;
			for (;;) {
				{
					std::lock_guard lock(mu_);
					if (closed_ || !listening_) return;
				}
				pollfd p{listener_, POLLIN, 0};
				if (::poll(&p, 1, 100) > 0) {
					client = ::accept4(listener_, nullptr, nullptr, SOCK_CLOEXEC);
					break;
				}
				if (opening_->done()) stop_listening();
			}
			if (client < 0) return;
			int upstream = -1;
			if (second) {
				stop_listening();
				upstream = dial(address_, opening_->deadline(), *opening_);
				if (upstream < 0) {
					::close(client);
					return;
				}
			}
			{
				std::lock_guard lock(mu_);
				if (closed_) {
					::close(client);
					if (upstream >= 0) ::close(upstream);
					return;
				}
				if (second) {
					if (upstream_ >= 0) ::close(upstream_);
					upstream_ = upstream;
				}
				if (client_ >= 0) ::close(client_);
				client_ = client;
				upstream = upstream_;
			}
			pump(client, upstream);
			if (second || opening_->done()) return;
		}
	}

	// Copies both ways until either side ends.
	static void pump(int a, int b)
	{
		std::vector<char> buf(64 << 10);
		pollfd p[2] = {{a, POLLIN, 0}, {b, POLLIN, 0}};
		for (;;) {
			if (::poll(p, 2, -1) < 0) {
				if (errno == EINTR) continue;
				break;
			}
			bool end = false;
			for (int i = 0; i < 2 && !end; i++) {
				if (!(p[i].revents & (POLLIN | POLLHUP | POLLERR))) continue;
				ssize_t n = ::read(p[i].fd, buf.data(), buf.size());
				if (n <= 0 || !io_full(p[1 - i].fd, buf.data(), static_cast<std::size_t>(n), true, SteadyClock::time_point::max()))
					end = true;
			}
			if (end) break;
		}
		::shutdown(a, SHUT_RDWR);
		::shutdown(b, SHUT_RDWR);
	}

	std::mutex mu_;
	int listener_ = -1, upstream_ = -1, client_ = -1;
	bool closed_ = false, listening_ = true;
	std::string address_;
	std::shared_ptr<Context> opening_;
	std::thread thread_;
};

// One reader at a time: a timed-out kernel call may take time to end after SIGKILL.
std::mutex reader_slot_mu;
std::condition_variable reader_slot_cv;
bool reader_slot_busy = false;

class SmbSource : public Source {
public:
	~SmbSource() override
	{
		close();
		window.reset();
		if (waiter.joinable()) waiter.join();
		if (shared) ::munmap(shared, 2 * max_read);
		if (conn >= 0) ::close(conn);
	}

	Error read_block(std::uint64_t offset, std::uint32_t length, std::span<const char> &data) override
	{
		return window->read(offset, length, data);
	}

	// Closing the reader fails a pending request, so the read and the prefetch return before the window is joined.
	void close() override
	{
		std::call_once(closed, [this] {
			if (lifetime) lifetime->cancel();
			close_reader();
			if (window) window->close();
		});
	}

	// EOF lets an idle reader close its file and mount while the server is still reachable; a blocked call gets
	// 100 ms, then the relay closes under it and the reader is killed.
	void close_reader()
	{
		std::call_once(reader_closed, [this] {
			if (conn >= 0) ::shutdown(conn, SHUT_RDWR);
			if (pid <= 0) {
				if (relay) relay->close();
				return;
			}
			if (done.wait_until(SteadyClock::now() + std::chrono::milliseconds(100))) {
				relay->close();
				return;
			}
			relay->close();
			{
				// Only a child not yet reaped: its number could belong to another process after.
				std::lock_guard lock(exit_mu);
				if (!exited) ::kill(pid, SIGKILL);
			}
			(void)done.wait_until(SteadyClock::now() + std::chrono::milliseconds(100));
		});
	}

	std::mutex exit_mu;
	bool exited = false;
	std::shared_ptr<Context> lifetime;
	std::unique_ptr<Relay> relay;
	std::unique_ptr<Window> window;
	char *shared = nullptr;
	int conn = -1;
	pid_t pid = -1;
	std::thread waiter;
	Event done;
	std::once_flag closed, reader_closed;
	unsigned char read_header[16];
};

} // namespace

int run_smb_helper_if_requested(int argc, char **argv)
{
	if (argc != 2 || std::strcmp(argv[1], smb_helper_arg) != 0) return -1;
	void *shared = ::mmap(nullptr, 2 * max_read, PROT_READ | PROT_WRITE, MAP_SHARED, 4, 0);
	::close(4);
	if (shared == MAP_FAILED) ::_exit(1);
	int status = serve_smb_helper(3, static_cast<char *>(shared), 2 * max_read);
	::close(3);
	::_exit(status);
}

std::unique_ptr<Source> open_smb(const std::shared_ptr<Context> &ctx, const SmbOptions &o, Error &error)
{
	std::string address, share, name;
	if ((error = parse_smb_options(o, address, share, name))) return nullptr;
	auto opening = Context::make(ctx, SteadyClock::now() + smb_timeout);
	{
		std::unique_lock lock(reader_slot_mu);
		bool free = reader_slot_cv.wait_until(lock, opening->deadline(), [&] { return !reader_slot_busy || opening->canceled(); });
		if (!free || reader_slot_busy) {
			error = smb_error(*opening, "The previous SMB reader is still shutting down; try again");
			return nullptr;
		}
		reader_slot_busy = true;
	}
	bool started = false;
	struct Slot {
		bool &started;
		~Slot()
		{
			if (started) return;
			std::lock_guard lock(reader_slot_mu);
			reader_slot_busy = false;
			reader_slot_cv.notify_all();
		}
	} slot{started};
	auto s = std::make_unique<SmbSource>();
	s->relay = std::make_unique<Relay>();
	int port = s->relay->start(address, opening);
	if (port < 0) {
		error = smb_error(*opening, "SMB connection failed or timed out");
		return nullptr;
	}
	// The memory of the two window buffers: allocated here, its size sealed, so that neither process can fault on
	// its mapping.
	int memory = static_cast<int>(::syscall(SYS_memfd_create, "smb-window", MFD_CLOEXEC | MFD_ALLOW_SEALING));
	bool ok = memory >= 0 && ::posix_fallocate(memory, 0, 2 * max_read) == 0 &&
	          ::fcntl(memory, F_ADD_SEALS, F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL) == 0;
	void *shared = ok ? ::mmap(nullptr, 2 * max_read, PROT_READ | PROT_WRITE, MAP_SHARED, memory, 0) : MAP_FAILED;
	if (shared == MAP_FAILED) {
		if (memory >= 0) ::close(memory);
		error = Error::of("Could not start the SMB reader");
		return nullptr;
	}
	s->shared = static_cast<char *>(shared);
	int fds[2];
	if (::socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, fds) != 0) {
		::close(memory);
		error = Error::of("Could not start the SMB reader");
		return nullptr;
	}
	s->conn = fds[0];
	char exe[PATH_MAX];
	ssize_t n = ::readlink("/proc/self/exe", exe, sizeof exe - 1);
	if (n <= 0) {
		::close(fds[1]);
		::close(memory);
		error = Error::of("Could not locate the SMB reader");
		return nullptr;
	}
	exe[n] = '\0';
	int null = ::open("/dev/null", O_RDWR | O_CLOEXEC);
	pid_t parent = ::getpid();
	pid_t pid = ::fork();
	if (pid == 0) {
		process::cloexec_inherited();
		// The descriptors land on 3 and 4 whatever their numbers now (dup2 leaves the new ones open on exec).
		int peer = ::fcntl(fds[1], F_DUPFD_CLOEXEC, 10), mem = ::fcntl(memory, F_DUPFD_CLOEXEC, 10);
		if (peer < 0 || mem < 0 || ::dup2(peer, 3) < 0 || ::dup2(mem, 4) < 0) ::_exit(127);
		if (null >= 0) {
			::dup2(null, 0);
			::dup2(null, 1);
			::dup2(null, 2);
		}
		::prctl(PR_SET_PDEATHSIG, SIGKILL);
		if (::getppid() != parent) ::_exit(127);
		char *args[] = {exe, const_cast<char *>(smb_helper_arg), nullptr};
		::execv(exe, args);
		::_exit(127);
	}
	if (null >= 0) ::close(null);
	::close(fds[1]);
	::close(memory);
	if (pid < 0) {
		error = Error::of("Could not start the SMB reader");
		return nullptr;
	}
	started = true;
	s->pid = pid;
	SmbSource *raw = s.get();
	s->waiter = std::thread([raw, pid] {
		// The child is seen to end first and reaped after, so that no kill reaches a reused number.
		siginfo_t info{};
		while (::waitid(P_PID, static_cast<id_t>(pid), &info, WEXITED | WNOWAIT) < 0 && errno == EINTR) {
		}
		{
			std::lock_guard lock(raw->exit_mu);
			raw->exited = true;
		}
		int status = 0;
		while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
		}
		{
			std::lock_guard lock(reader_slot_mu);
			reader_slot_busy = false;
			reader_slot_cv.notify_all();
		}
		raw->done.fire();
	});
	// The opening's end (its deadline or a cancel) closes the reader until the source owns it.
	std::atomic<bool> claimed{false};
	auto opening_end = std::make_shared<Event>();
	std::thread watch([raw, opening, opening_end, &claimed] {
		while (!opening_end->wait_until(SteadyClock::now() + std::chrono::milliseconds(50)))
			if (opening->done()) {
				if (!claimed.exchange(true)) raw->close_reader();
				return;
			}
	});
	auto fail = [&](Error e) {
		claimed.exchange(true);
		opening_end->fire();
		watch.join();
		raw->close_reader();
		s->relay->stop_listening();
		error = std::move(e);
		return nullptr;
	};
	MountRequest request{o.url, o.username, o.password, o.domain, "127.0.0.1:" + std::to_string(port)};
	std::string body;
	(void)glz::write_json(request, body);
	if (body.size() > 8192) return fail(Error::of("Invalid SMB image options"));
	unsigned char header[12];
	put32(header, static_cast<std::uint32_t>(body.size()));
	bool sent = io_full(s->conn, header, 4, true, opening->deadline()) &&
	            io_full(s->conn, body.data(), body.size(), true, opening->deadline());
	std::fill(body.begin(), body.end(), '\0');
	if (!sent || !io_full(s->conn, header, 12, false, opening->deadline()))
		return fail(smb_error(*opening, "SMB connection failed or timed out"));
	if (std::uint32_t status = get32(header); status != smb_ok) return fail(smb_status_error(status));
	std::uint64_t size = get64(header + 4);
	if (auto e = validate_image(size)) return fail(e);
	// The opening's timer stops before the source owns the reader.
	if (claimed.exchange(true) || opening->done()) {
		opening_end->fire();
		watch.join();
		raw->close_reader();
		s->relay->stop_listening();
		error = smb_error(*opening, "SMB connection timed out");
		return nullptr;
	}
	opening_end->fire();
	watch.join();
	s->relay->stop_listening();
	s->lifetime = Context::make(ctx);
	s->name = go::base(name);
	s->size = size;
	(void)s->lifetime->on_cancel([raw] { raw->close_reader(); });
	auto lifetime = s->lifetime;
	// The reader answers one request at a time and cannot cancel one, so a read after a seek first waits for the
	// prefetch; the deadline counts from the start of that NBD read, keeping both below the NBD timeout. A request
	// left without an answer closes the reader for good.
	auto fetch = [raw, lifetime](const std::shared_ptr<Context> &, SteadyClock::time_point started, std::uint64_t offset,
	                             std::span<char> p) -> Error {
		std::ptrdiff_t at = p.data() - raw->shared;
		if (at < 0 || static_cast<std::size_t>(at) >= 2 * max_read ||
		    !shared_range(2 * max_read, static_cast<std::uint32_t>(at), static_cast<std::uint32_t>(p.size())))
			return Error::of("SMB read buffer is outside the reader memory");
		unsigned char *h = raw->read_header;
		put64(h, offset);
		put32(h + 8, static_cast<std::uint32_t>(p.size()));
		put32(h + 12, static_cast<std::uint32_t>(at));
		auto deadline = started + smb_timeout;
		if (!io_full(raw->conn, h, 16, true, deadline) || !io_full(raw->conn, h, 4, false, deadline)) {
			raw->close_reader();
			return smb_error(*lifetime, "SMB ISO read failed or timed out");
		}
		if (std::uint32_t status = get32(h); status != smb_ok) return smb_status_error(status);
		return {};
	};
	s->window = std::make_unique<Window>(lifetime, fetch, s->shared, s->shared + max_read);
	s->window->size = size;
	return s;
}

} // namespace nkos::storage
