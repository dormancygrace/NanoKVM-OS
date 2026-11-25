// Remote block media, as the Go server's remotemedia package: the transmission phase of the Linux NBD protocol on
// a socket the kernel holds the other end of (/dev/nbd0 attached with ioctls, read-only, no network listener), and
// the bounded read cache of a network image (two buffers of 1 MiB: the one lent to the NBD writer, and one
// prefetch of the next block once reads are sequential).

#include "storage/internal.hpp"

#include "common/log.hpp"
#include "common/process.hpp"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <sys/file.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <unistd.h>

namespace nkos::storage {

namespace {

constexpr std::uint32_t request_magic = 0x25609513, reply_magic = 0x67446698;

std::uint32_t be32(const unsigned char *p)
{
	return static_cast<std::uint32_t>(p[0]) << 24 | static_cast<std::uint32_t>(p[1]) << 16 |
	       static_cast<std::uint32_t>(p[2]) << 8 | p[3];
}
std::uint64_t be64(const unsigned char *p) { return static_cast<std::uint64_t>(be32(p)) << 32 | be32(p + 4); }
void put32(unsigned char *p, std::uint32_t v)
{
	p[0] = static_cast<unsigned char>(v >> 24);
	p[1] = static_cast<unsigned char>(v >> 16);
	p[2] = static_cast<unsigned char>(v >> 8);
	p[3] = static_cast<unsigned char>(v);
}

Error read_full(int fd, void *buf, std::size_t n)
{
	auto *p = static_cast<char *>(buf);
	std::size_t got = 0;
	while (got < n) {
		ssize_t r = ::read(fd, p + got, n - got);
		if (r < 0 && errno == EINTR) continue;
		if (r < 0) return Error{go::errno_text(errno), errno, {}};
		if (r == 0) return Error::of(got == 0 ? "EOF" : "unexpected EOF");
		got += static_cast<std::size_t>(r);
	}
	return {};
}

Error write_all(int fd, const void *buf, std::size_t n)
{
	auto *p = static_cast<const char *>(buf);
	while (n > 0) {
		ssize_t w = ::send(fd, p, n, MSG_NOSIGNAL);
		if (w < 0 && errno == EINTR) continue;
		if (w < 0) return Error{go::errno_text(errno), errno, {}};
		if (w == 0) return Error::of("short write");
		p += w;
		n -= static_cast<std::size_t>(w);
	}
	return {};
}

std::string context_error(const Context &ctx)
{
	return ctx.canceled() ? "context canceled" : "context deadline exceeded";
}

} // namespace

Error nbd_serve(int fd, std::uint64_t size, const ReadBlock &read)
{
	unsigned char request[28];
	std::vector<char> discard;
	for (;;) {
		if (auto e = read_full(fd, request, sizeof request)) return e;
		if (be32(request) != request_magic) return Error::of("invalid NBD request magic");
		std::uint32_t command = be32(request + 4);
		std::uint64_t offset = be64(request + 16);
		std::uint32_t length = be32(request + 24);
		if (length > max_read) return Error::of("NBD request exceeds memory limit");
		if (command == 2) return {};  // NBD_CMD_DISC has no reply.
		std::uint32_t err = 0;
		std::span<const char> data;
		switch (command & 0xffff) {
		case 0:  // NBD_CMD_READ
			if (command != 0 || offset > size || length > size - offset) {
				err = 22;  // EINVAL; the subtraction avoids an overflow of offset.
			} else if (length != 0) {
				if (read(offset, length, data) || data.size() != length) {
					err = 5;
					data = {};
				}
			}
			break;
		case 1:  // A refused write is read whole, so that the requests after it keep their framing.
			discard.resize(length);
			// io.CopyN: a body that ends early is EOF.
			if (auto e = read_full(fd, discard.data(), length)) return e.text == "unexpected EOF" ? Error::of("EOF") : e;
			err = 30;  // EROFS
			break;
		default:
			err = 95;  // EOPNOTSUPP: no flush, trim or write-zeroes are advertised.
		}
		unsigned char reply[16];
		put32(reply, reply_magic);
		put32(reply + 4, err);
		std::memcpy(reply + 8, request + 8, 8);  // the handle is opaque
		if (auto e = write_all(fd, reply, sizeof reply)) return e;
		if (err == 0 && !data.empty())
			if (auto e = write_all(fd, data.data(), data.size())) return e;
	}
}

// --- The HTTP source's checks (remotemedia/http.go) ---

namespace {

bool all_digits(std::string_view s)
{
	if (s.empty()) return false;
	for (char c : s)
		if (c < '0' || c > '9') return false;
	return true;
}

// strconv.ParseUint of digits: the largest value and false on overflow.
bool parse_uint(std::string_view s, std::uint64_t &v)
{
	v = 0;
	for (char c : s) {
		std::uint64_t d = static_cast<std::uint64_t>(c - '0');
		if (v > (UINT64_MAX - d) / 10) {
			v = UINT64_MAX;
			return false;
		}
		v = v * 10 + d;
	}
	return true;
}

} // namespace

bool valid_http_url(const go::Url &u)
{
	if ((u.scheme != "http" && u.scheme != "https") || u.hostname().empty() || u.has_user || !u.fragment.empty() ||
	    !u.opaque.empty())
		return false;
	if (std::string p = u.port(); !p.empty()) {
		// strconv.Atoi of the digits validOptionalPort let through.
		auto first = p.find_first_not_of('0');
		std::string significant = first == std::string::npos ? "0" : p.substr(first);
		if (significant.size() > 5) return false;
		long n = std::stol(significant);
		return n > 0 && n <= 65535;
	}
	return true;
}

bool parse_content_range(std::string_view value, std::uint64_t &start, std::uint64_t &end, std::uint64_t &total)
{
	start = end = total = 0;
	if (!value.starts_with("bytes ")) return false;
	value.remove_prefix(6);
	auto dash = value.find('-');
	if (dash == std::string_view::npos) return false;
	auto slash = value.find('/', dash);
	if (slash == std::string_view::npos) return false;
	std::string_view a = value.substr(0, dash), b = value.substr(dash + 1, slash - dash - 1), c = value.substr(slash + 1);
	if (!all_digits(a) || !all_digits(b) || !all_digits(c)) return false;
	// The values parse in order; one that overflows stops there, as the Go code returns at its error.
	if (!parse_uint(a, start) || !parse_uint(b, end) || !parse_uint(c, total)) return false;
	return start <= end && end < total;
}

bool strong_http_etag(const std::string &s)
{
	if (s.size() < 2 || s.front() != '"' || s.back() != '"') return false;
	for (std::size_t i = 1; i + 1 < s.size(); i++) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		if (c < 0x21 || c == '"' || c == 0x7f) return false;
	}
	return true;
}

Error validate_http_options(const HttpOptions &o)
{
	auto u = go::parse_url(o.url);
	if (!u || !valid_http_url(*u) || o.url.size() > 4096)
		return Error::of("Use an HTTP or HTTPS image URL without embedded credentials or a fragment");
	if (o.username.size() > 256 || o.password.size() > 1024 || o.username.find_first_of(":\r\n") != std::string::npos ||
	    (o.username.empty() && !o.password.empty()))
		return Error::of("Invalid HTTP credentials");
	return {};
}

// --- The kernel attachment ---

namespace {

constexpr unsigned long nbd_set_sock = 0xab00, nbd_set_block_size = 0xab01, nbd_do_it = 0xab03,
                        nbd_clear_sock = 0xab04, nbd_set_size_blocks = 0xab07, nbd_disconnect = 0xab08,
                        nbd_set_timeout = 0xab09, nbd_set_flags = 0xab0a;

int nbd_ioctl(int fd, unsigned long cmd, unsigned long value)
{
	return ::ioctl(fd, cmd, value) == 0 ? 0 : errno;
}

std::string exec_error(const process::Result &r, const std::string &name)
{
	if (!r.started) return "exec: \"" + name + "\": executable file not found in $PATH";
	return go::exit_text(r.exit_code, r.signal);
}

bool nbd_connected()
{
	std::string pid;
	return !go::read_file(nbd_pid, pid) && !go::trim_space(pid).empty();
}

} // namespace

std::unique_ptr<NbdDevice> NbdDevice::open(std::uint64_t size, ReadBlock read, Error &error)
{
	if ((error = validate_image(size))) return nullptr;
	if (!go::exists(nbd_device)) {
		auto r = process::run({"modprobe", "nbd", "nbds_max=1", "max_part=0"}, {.timeout = std::chrono::seconds(5)});
		if (!r.ok()) {
			error = Error::of("NBD support unavailable: " + exec_error(r, "modprobe"));
			return nullptr;
		}
	}
	int file = ::open(nbd_device.c_str(), O_RDWR | O_CLOEXEC);
	if (file < 0) {
		error = Error::path("open", nbd_device, errno);
		return nullptr;
	}
	if (::flock(file, LOCK_EX | LOCK_NB) != 0) {
		::close(file);
		error = Error::of("remote media device is busy");
		return nullptr;
	}
	if (nbd_connected()) {
		::close(file);
		error = Error::of("NBD device is already connected");
		return nullptr;
	}
	int fds[2];
	if (::socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, fds) != 0) {
		error = Error{go::errno_text(errno), errno, {}};
		::close(file);
		return nullptr;
	}
	std::unique_ptr<NbdDevice> d(new NbdDevice);
	d->file_ = file;
	d->kernel_ = fds[0];
	d->user_ = fds[1];
	const std::pair<unsigned long, unsigned long> ops[] = {
		{nbd_set_block_size, 512}, {nbd_set_size_blocks, size / 512}, {nbd_set_timeout, 20},
		{nbd_set_flags, 3},  // HAS_FLAGS | READ_ONLY
		{nbd_set_sock, static_cast<unsigned long>(d->kernel_)}};
	for (auto [cmd, value] : ops)
		if (int e = nbd_ioctl(file, cmd, value)) {
			::close(d->user_);
			::close(d->kernel_);
			::close(file);
			d->file_ = d->kernel_ = d->user_ = -1;
			error = Error::of("configure NBD: " + go::errno_text(e));
			return nullptr;
		}
	int user = d->user_;
	d->serve_ = std::thread([user, size, read = std::move(read)] {
		// The kernel sees the socket closing as failed I/O; the owner handles the disconnect.
		(void)nbd_serve(user, size, read);
		::shutdown(user, SHUT_RDWR);
	});
	NbdDevice *raw = d.get();
	d->doit_ = std::thread([raw, file] {
		raw->doit_result_ = nbd_ioctl(file, nbd_do_it, 0);
		raw->stopped.fire();
	});
	auto deadline = SteadyClock::now() + std::chrono::seconds(3);
	while (SteadyClock::now() < deadline) {
		if (d->stopped.fired()) {
			int e = d->doit_result_;
			d->close();
			error = Error::of("NBD stopped before attachment: " + (e ? go::errno_text(e) : std::string("<nil>")));
			return nullptr;
		}
		if (nbd_connected()) {
			if (auto e = go::write_file(nbd_max_sectors, "1024", 0600)) {
				d->close();
				error = Error::of("bound NBD request size: " + e.text);
				return nullptr;
			}
			return d;
		}
		std::this_thread::sleep_for(std::chrono::milliseconds(10));
	}
	d->close();
	error = Error::of("NBD attachment timed out");
	return nullptr;
}

void NbdDevice::close()
{
	std::call_once(once_, [this] {
		if (user_ >= 0) ::shutdown(user_, SHUT_RDWR);
		if (file_ >= 0) (void)nbd_ioctl(file_, nbd_disconnect, 0);
		if (doit_.joinable()) doit_.join();
		if (file_ >= 0) (void)nbd_ioctl(file_, nbd_clear_sock, 0);
		if (kernel_ >= 0) ::close(kernel_);
		// The serving thread ends once its read callback returns: the owner cancelled the source first.
		if (serve_.joinable()) serve_.join();
		if (user_ >= 0) ::close(user_);
		if (file_ >= 0) ::close(file_);
		kernel_ = user_ = file_ = -1;
	});
}

NbdDevice::~NbdDevice() { close(); }

// --- The read cache ---

struct Window::Prefetch {
	std::uint64_t offset = 0;
	std::size_t count = 0;
	Error err;
	std::shared_ptr<Context> ctx;
	std::thread thread;
	Event done;
};

Window::Window(std::shared_ptr<Context> ctx, FetchBlock fetch, char *buffer0, char *buffer1)
	: buffers{buffer0, buffer1}, ctx_(std::move(ctx)), fetch_(std::move(fetch))
{
	if (!buffers[0]) {
		own_[0].reset(new char[max_read]);
		buffers[0] = own_[0].get();
	}
}

Window::~Window() { close(); }

Error Window::read(std::uint64_t offset, std::uint32_t length, std::span<const char> &data)
{
	std::lock_guard lock(mu_);
	if (ctx_->done()) return Error::of(context_error(*ctx_), "context");
	if (length == 0 || length > max_read || offset > size || length > size - offset)
		return Error::of("Invalid image read bounds");
	auto started = SteadyClock::now();
	if (offset == next_ && length >= (64u << 10)) streak_ = static_cast<std::uint8_t>(std::min(streak_ + 1, 2));
	else streak_ = 0;
	std::uint64_t end = offset + length;
	next_ = end;
	std::uint64_t window_end = start + valid;
	if (offset >= start && end <= window_end) {
		prefetch_next();
		data = {buffers[current_] + (offset - start), length};
		return {};
	}
	if (auto p = ahead_; p && end <= p->offset + p->count) {
		bool inside = offset >= p->offset;
		// NBD reads do not align with block ends: one that begins in the current block and ends in the next is
		// put together below.
		bool straddles = offset >= start && offset < window_end && window_end == p->offset;
		if (inside || straddles) {
			p->done.wait();
			Error err = p->err;
			if (!err && ctx_->done()) err = Error::of(context_error(*ctx_), "context");
			if (err) {
				discard();
				return err;
			}
		}
		if (inside) {
			discard();
			current_ ^= 1;
			start = p->offset;
			valid = p->count;
			prefetch_next();
			data = {buffers[current_] + (offset - start), length};
			return {};
		}
		if (straddles) {
			// The lease of the current buffer ended with this call: its tail moves to the front and the head of
			// the next block follows. That block stays whole until a read falls inside it.
			char *buf = buffers[current_];
			std::size_t n = valid - static_cast<std::size_t>(offset - start);
			std::memmove(buf, buf + (offset - start), n);
			std::memcpy(buf + n, buffers[current_ ^ 1], std::min<std::size_t>(length - n, max_read));
			start = offset;
			valid = length;
			data = {buf, length};
			return {};
		}
	}
	discard();
	valid = 0;
	std::span<char> target(buffers[current_], length);
	if (auto e = fetch_(ctx_, started, offset, target)) return e;
	if (ctx_->done()) return Error::of(context_error(*ctx_), "context");
	start = offset;
	valid = length;
	prefetch_next();
	data = {buffers[current_], length};
	return {};
}

void Window::prefetch_next()
{
	std::uint64_t offset = start + valid;
	if (streak_ < 2 || ahead_ || offset >= size) return;
	if (!buffers[1]) {
		own_[1].reset(new char[max_read]);
		buffers[1] = own_[1].get();
	}
	auto p = std::make_shared<Prefetch>();
	p->offset = offset;
	p->count = static_cast<std::size_t>(std::min<std::uint64_t>(max_read, size - offset));
	p->ctx = Context::make(ctx_);
	std::span<char> target(buffers[current_ ^ 1], p->count);
	ahead_ = p;
	p->thread = std::thread([this, p, target] {
		p->err = fetch_(p->ctx, SteadyClock::now(), p->offset, target);
		p->done.fire();
	});
}

void Window::discard()
{
	if (auto p = ahead_) {
		p->ctx->cancel();
		p->done.wait();
		if (p->thread.joinable()) p->thread.join();
		ahead_ = nullptr;
	}
}

void Window::close()
{
	std::lock_guard lock(mu_);
	discard();
}

} // namespace nkos::storage
