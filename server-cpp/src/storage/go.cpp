// Ports of the Go standard library behaviour listed in storage/go.hpp, kept to what the storage family needs.

#include "storage/go.hpp"

#include <arpa/inet.h>
#include <cerrno>
#include <charconv>
#include <cmath>
#include <csignal>
#include <dirent.h>
#include <ctime>
#include <fcntl.h>
#include <random>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::storage::go {

namespace {

// The messages of Go's syscall package (zerrors_linux_riscv64.go).
struct ErrnoText {
	int code;
	const char *text;
};
constexpr ErrnoText errno_texts[] = {
	{1, "operation not permitted"},
	{2, "no such file or directory"},
	{3, "no such process"},
	{4, "interrupted system call"},
	{5, "input/output error"},
	{6, "no such device or address"},
	{7, "argument list too long"},
	{8, "exec format error"},
	{9, "bad file descriptor"},
	{10, "no child processes"},
	{11, "resource temporarily unavailable"},
	{12, "cannot allocate memory"},
	{13, "permission denied"},
	{14, "bad address"},
	{15, "block device required"},
	{16, "device or resource busy"},
	{17, "file exists"},
	{18, "invalid cross-device link"},
	{19, "no such device"},
	{20, "not a directory"},
	{21, "is a directory"},
	{22, "invalid argument"},
	{23, "too many open files in system"},
	{24, "too many open files"},
	{25, "inappropriate ioctl for device"},
	{26, "text file busy"},
	{27, "file too large"},
	{28, "no space left on device"},
	{29, "illegal seek"},
	{30, "read-only file system"},
	{31, "too many links"},
	{32, "broken pipe"},
	{33, "numerical argument out of domain"},
	{34, "numerical result out of range"},
	{35, "resource deadlock avoided"},
	{36, "file name too long"},
	{37, "no locks available"},
	{38, "function not implemented"},
	{39, "directory not empty"},
	{40, "too many levels of symbolic links"},
	{42, "no message of desired type"},
	{43, "identifier removed"},
	{44, "channel number out of range"},
	{45, "level 2 not synchronized"},
	{46, "level 3 halted"},
	{47, "level 3 reset"},
	{48, "link number out of range"},
	{49, "protocol driver not attached"},
	{50, "no CSI structure available"},
	{51, "level 2 halted"},
	{52, "invalid exchange"},
	{53, "invalid request descriptor"},
	{54, "exchange full"},
	{55, "no anode"},
	{56, "invalid request code"},
	{57, "invalid slot"},
	{59, "bad font file format"},
	{60, "device not a stream"},
	{61, "no data available"},
	{62, "timer expired"},
	{63, "out of streams resources"},
	{64, "machine is not on the network"},
	{65, "package not installed"},
	{66, "object is remote"},
	{67, "link has been severed"},
	{68, "advertise error"},
	{69, "srmount error"},
	{70, "communication error on send"},
	{71, "protocol error"},
	{72, "multihop attempted"},
	{73, "RFS specific error"},
	{74, "bad message"},
	{75, "value too large for defined data type"},
	{76, "name not unique on network"},
	{77, "file descriptor in bad state"},
	{78, "remote address changed"},
	{79, "can not access a needed shared library"},
	{80, "accessing a corrupted shared library"},
	{81, ".lib section in a.out corrupted"},
	{82, "attempting to link in too many shared libraries"},
	{83, "cannot exec a shared library directly"},
	{84, "invalid or incomplete multibyte or wide character"},
	{85, "interrupted system call should be restarted"},
	{86, "streams pipe error"},
	{87, "too many users"},
	{88, "socket operation on non-socket"},
	{89, "destination address required"},
	{90, "message too long"},
	{91, "protocol wrong type for socket"},
	{92, "protocol not available"},
	{93, "protocol not supported"},
	{94, "socket type not supported"},
	{95, "operation not supported"},
	{96, "protocol family not supported"},
	{97, "address family not supported by protocol"},
	{98, "address already in use"},
	{99, "cannot assign requested address"},
	{100, "network is down"},
	{101, "network is unreachable"},
	{102, "network dropped connection on reset"},
	{103, "software caused connection abort"},
	{104, "connection reset by peer"},
	{105, "no buffer space available"},
	{106, "transport endpoint is already connected"},
	{107, "transport endpoint is not connected"},
	{108, "cannot send after transport endpoint shutdown"},
	{109, "too many references: cannot splice"},
	{110, "connection timed out"},
	{111, "connection refused"},
	{112, "host is down"},
	{113, "no route to host"},
	{114, "operation already in progress"},
	{115, "operation now in progress"},
	{116, "stale file handle"},
	{117, "structure needs cleaning"},
	{118, "not a XENIX named type file"},
	{119, "no XENIX semaphores available"},
	{120, "is a named type file"},
	{121, "remote I/O error"},
	{122, "disk quota exceeded"},
	{123, "no medium found"},
	{124, "wrong medium type"},
	{125, "operation canceled"},
	{126, "required key not available"},
	{127, "key has expired"},
	{128, "key has been revoked"},
	{129, "key was rejected by service"},
	{130, "owner died"},
	{131, "state not recoverable"},
	{132, "operation not possible due to RF-kill"},
	{133, "memory page has hardware error"},
};

} // namespace

std::string errno_text(int err)
{
	for (const auto &e : errno_texts)
		if (e.code == err) return e.text;
	return "errno " + std::to_string(err);
}

std::string path_error(std::string_view op, std::string_view path, int err)
{
	std::string out(op);
	out += ' ';
	out += path;
	out += ": ";
	out += errno_text(err);
	return out;
}

std::string exit_text(int exit_code, int signal)
{
	if (signal == 0) return "exit status " + std::to_string(exit_code);
	// Go's signal names (syscall's signal table) for the signals a command meets here.
	switch (signal) {
	case SIGKILL: return "signal: killed";
	case SIGTERM: return "signal: terminated";
	case SIGINT: return "signal: interrupt";
	case SIGHUP: return "signal: hangup";
	case SIGQUIT: return "signal: quit";
	case SIGABRT: return "signal: aborted";
	case SIGSEGV: return "signal: segmentation fault";
	case SIGBUS: return "signal: bus error";
	case SIGPIPE: return "signal: broken pipe";
	case SIGILL: return "signal: illegal instruction";
	case SIGFPE: return "signal: floating point exception";
	default: return "signal: signal " + std::to_string(signal);
	}
}

std::string clean(std::string_view path)
{
	if (path.empty()) return ".";
	bool rooted = path[0] == '/';
	std::size_t n = path.size();
	std::string out;
	std::size_t r = 0, dotdot = 0;
	if (rooted) {
		out += '/';
		r = dotdot = 1;
	}
	while (r < n) {
		if (path[r] == '/') {
			r++;
		} else if (path[r] == '.' && (r + 1 == n || path[r + 1] == '/')) {
			r++;
		} else if (path[r] == '.' && path[r + 1] == '.' && (r + 2 == n || path[r + 2] == '/')) {
			r += 2;
			if (out.size() > dotdot) {
				// Back to the slash before the last element, which goes too.
				std::size_t w = out.size() - 1;
				while (w > dotdot && out[w] != '/') w--;
				out.resize(w);
			} else if (!rooted) {
				if (!out.empty()) out += '/';
				out += "..";
				dotdot = out.size();
			}
		} else {
			if ((rooted && out.size() != 1) || (!rooted && !out.empty())) out += '/';
			for (; r < n && path[r] != '/'; r++) out += path[r];
		}
	}
	return out.empty() ? "." : out;
}

std::string base(std::string_view path)
{
	if (path.empty()) return ".";
	while (!path.empty() && path.back() == '/') path.remove_suffix(1);
	auto slash = path.rfind('/');
	if (slash != std::string_view::npos) path = path.substr(slash + 1);
	return path.empty() ? "/" : std::string(path);
}

std::string dir(std::string_view path)
{
	auto slash = path.rfind('/');
	return clean(slash == std::string_view::npos ? std::string_view{} : path.substr(0, slash + 1));
}

std::string ext(std::string_view path)
{
	for (std::size_t i = path.size(); i-- > 0 && path[i] != '/';)
		if (path[i] == '.') return std::string(path.substr(i));
	return {};
}

std::string join(std::string_view a, std::string_view b)
{
	if (a.empty()) return b.empty() ? std::string() : clean(b);
	if (b.empty()) return clean(a);
	return clean(std::string(a) + "/" + std::string(b));
}

namespace {

// utf8.DecodeRuneInString: the rune and its length; (U+FFFD, 1) for an invalid sequence.
std::pair<char32_t, std::size_t> decode(std::string_view s, std::size_t i)
{
	auto b = [&](std::size_t k) { return static_cast<unsigned char>(s[i + k]); };
	unsigned char c = b(0);
	if (c < 0x80) return {c, 1};
	std::size_t left = s.size() - i;
	auto cont = [&](std::size_t k, unsigned lo = 0x80, unsigned hi = 0xbf) { return k < left && b(k) >= lo && b(k) <= hi; };
	if (c >= 0xc2 && c <= 0xdf) {
		if (cont(1)) return {static_cast<char32_t>((c & 0x1f) << 6 | (b(1) & 0x3f)), 2};
	} else if (c >= 0xe0 && c <= 0xef) {
		unsigned lo = c == 0xe0 ? 0xa0 : 0x80, hi = c == 0xed ? 0x9f : 0xbf;
		if (cont(1, lo, hi) && cont(2)) return {static_cast<char32_t>((c & 0x0f) << 12 | (b(1) & 0x3f) << 6 | (b(2) & 0x3f)), 3};
	} else if (c >= 0xf0 && c <= 0xf4) {
		unsigned lo = c == 0xf0 ? 0x90 : 0x80, hi = c == 0xf4 ? 0x8f : 0xbf;
		if (cont(1, lo, hi) && cont(2) && cont(3))
			return {static_cast<char32_t>((c & 0x07) << 18 | (b(1) & 0x3f) << 12 | (b(2) & 0x3f) << 6 | (b(3) & 0x3f)), 4};
	}
	return {0xfffd, 1};
}


} // namespace

bool valid_utf8(std::string_view s)
{
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode(s, i);
		if (r == 0xfffd && n == 1 && static_cast<unsigned char>(s[i]) >= 0x80) return false;
		i += n;
	}
	return true;
}

std::u32string runes(std::string_view s)
{
	std::u32string out;
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode(s, i);
		out += r;
		i += n;
	}
	return out;
}

bool is_control(char32_t r) { return r < 0x20 || (r >= 0x7f && r <= 0x9f); }

bool is_space(char32_t r)
{
	switch (r) {
	case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xa0:
	case 0x1680: case 0x2028: case 0x2029: case 0x202f: case 0x205f: case 0x3000:
		return true;
	default:
		return r >= 0x2000 && r <= 0x200a;
	}
}

void remove_all(const std::string &path)
{
	struct stat st{};
	if (::lstat(path.c_str(), &st) != 0) return;
	if (S_ISDIR(st.st_mode)) {
		if (DIR *d = ::opendir(path.c_str())) {
			std::vector<std::string> names;
			while (dirent *e = ::readdir(d))
				if (std::string n = e->d_name; n != "." && n != "..") names.push_back(n);
			::closedir(d);
			for (const auto &n : names) remove_all(path + "/" + n);
		}
		::rmdir(path.c_str());
		return;
	}
	::unlink(path.c_str());
}

std::size_t utf16_length(std::string_view s)
{
	std::size_t n = 0;
	for (char32_t r : runes(s)) n += r >= 0x10000 ? 2 : 1;
	return n;
}

std::string trim_space(std::string_view s)
{
	std::size_t start = 0;
	while (start < s.size()) {
		auto [r, n] = decode(s, start);
		if (!is_space(r)) break;
		start += n;
	}
	std::size_t end = s.size();
	while (end > start) {
		// The last rune: back to its first byte.
		std::size_t at = end - 1;
		while (at > start && (static_cast<unsigned char>(s[at]) & 0xc0) == 0x80 && end - at < 4) at--;
		auto [r, n] = decode(s, at);
		if (at + n != end) {
			// Not a whole rune ending here: an invalid byte, which is no space.
			break;
		}
		if (!is_space(r)) break;
		end = at;
	}
	return std::string(s.substr(start, end - start));
}

std::vector<std::string> fields(std::string_view s)
{
	std::vector<std::string> out;
	std::string cur;
	bool in = false;
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode(s, i);
		if (is_space(r)) {
			if (in) out.push_back(std::move(cur));
			cur.clear();
			in = false;
		} else {
			cur.append(s.substr(i, n));
			in = true;
		}
		i += n;
	}
	if (in) out.push_back(std::move(cur));
	return out;
}

namespace {

// unicode.SimpleFold's orbits reduced to one representative, for the letters whose orbits reach outside ASCII
// (K: U+212A Kelvin sign, s: U+017F long s) and plain ASCII; other runes fold by the C library's tables.
char32_t fold(char32_t r)
{
	if (r >= 'A' && r <= 'Z') return r + 32;
	if (r == 0x212a) return 'k';
	if (r == 0x17f) return 's';
	if (r == 0x2126) return 0x3c9;  // Ohm sign, omega
	if (r == 0x212b) return 0xe5;   // Angstrom sign, a with ring
	if (r >= 0xc0 && r <= 0xde && r != 0xd7) return r + 32;
	if (r >= 0x391 && r <= 0x3a9 && r != 0x3a2) return r + 32;
	if (r >= 0x410 && r <= 0x42f) return r + 32;
	if (r >= 0x400 && r <= 0x40f) return r + 80;
	return r;
}

} // namespace

bool equal_fold(std::string_view a, std::string_view b)
{
	auto ra = runes(a), rb = runes(b);
	if (ra.size() != rb.size()) return false;
	for (std::size_t i = 0; i < ra.size(); i++)
		if (ra[i] != rb[i] && fold(ra[i]) != fold(rb[i])) return false;
	return true;
}

std::string to_lower(std::string_view s)
{
	// The callers compare the result with ASCII: only the runes that lower into ASCII change (the ASCII capitals,
	// U+212A Kelvin sign to 'k' and U+0130 dotted capital I to 'i', by unicode.ToLower's simple mapping).
	std::string out;
	out.reserve(s.size());
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode(s, i);
		if (r >= 'A' && r <= 'Z') out += static_cast<char>(r + 32);
		else if (r == 0x212a) out += 'k';
		else if (r == 0x130) out += 'i';
		else out.append(s.substr(i, n));
		i += n;
	}
	return out;
}

Error remove(const std::string &path)
{
	if (::unlink(path.c_str()) == 0) return {};
	int e = errno;
	if (::rmdir(path.c_str()) == 0) return {};
	int e1 = errno;
	if (e1 != ENOTDIR) e = e1;
	return Error::path("remove", path, e);
}

Error rename(const std::string &from, const std::string &to)
{
	struct stat target{};
	if (::lstat(to.c_str(), &target) == 0 && S_ISDIR(target.st_mode)) {
		struct stat source{};
		int e = 0;
		if (::lstat(from.c_str(), &source) != 0) e = errno;
		else if (to == from || target.st_dev != source.st_dev || target.st_ino != source.st_ino) e = EEXIST;
		if (e) return {"rename " + from + " " + to + ": " + errno_text(e), e, {}};
	}
	if (::rename(from.c_str(), to.c_str()) == 0) return {};
	int e = errno;
	return {"rename " + from + " " + to + ": " + errno_text(e), e, {}};
}

Error read_file(const std::string &path, std::string &data)
{
	data.clear();
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return Error::path("open", path, errno);
	char buf[8192];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			int e = errno;
			::close(fd);
			return Error::path("read", path, e);
		}
		if (n == 0) break;
		data.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return {};
}

Error write_file(const std::string &path, std::string_view data, unsigned mode)
{
	int fd = ::open(path.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, mode);
	if (fd < 0) return Error::path("open", path, errno);
	std::size_t off = 0;
	while (off < data.size()) {
		ssize_t n = ::write(fd, data.data() + off, data.size() - off);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) {
			int e = n < 0 ? errno : EIO;
			::close(fd);
			return Error::path("write", path, e);
		}
		off += static_cast<std::size_t>(n);
	}
	if (::close(fd) != 0) return Error::path("close", path, errno);
	return {};
}

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

bool lexists(const std::string &path)
{
	struct stat st{};
	return ::lstat(path.c_str(), &st) == 0;
}

Error create_temp(const std::string &dir_, const std::string &pattern, int &fd, std::string &path)
{
	std::string dir = dir_.empty() ? "/tmp" : dir_;
	std::string prefix = pattern, suffix;
	if (auto star = pattern.rfind('*'); star != std::string::npos) {
		prefix = pattern.substr(0, star);
		suffix = pattern.substr(star + 1);
	}
	prefix = (!dir.empty() && dir.back() == '/' ? dir : dir + "/") + prefix;
	static thread_local std::mt19937 rng(std::random_device{}());
	for (int attempt = 0;; attempt++) {
		std::string name = prefix + std::to_string(static_cast<std::uint32_t>(rng())) + suffix;
		fd = ::open(name.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd >= 0) {
			path = name;
			return {};
		}
		int e = errno;
		if (e == EEXIST && attempt + 1 < 10000) continue;
		if (e == EEXIST) return Error::path("createtemp", prefix + "*" + suffix, EEXIST);
		return Error::path("open", name, e);
	}
}

namespace {

enum Encoding : unsigned {
	encode_path = 1,
	encode_path_segment = 2,
	encode_host = 4,
	encode_zone = 8,
	encode_user_password = 16,
	encode_query_component = 32,
	encode_fragment = 64,
};

bool should_escape(unsigned char c, Encoding mode)
{
	if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) return false;
	if (mode == encode_host || mode == encode_zone) {
		switch (c) {
		case '!': case '$': case '&': case '\'': case '(': case ')': case '*': case '+': case ',': case ';': case '=':
		case ':': case '[': case ']': case '<': case '>': case '"':
			return false;
		}
	}
	switch (c) {
	case '-': case '_': case '.': case '~':
		return false;
	case '$': case '&': case '+': case ',': case '/': case ':': case ';': case '=': case '?': case '@':
		switch (mode) {
		case encode_path: return c == '?';
		case encode_path_segment: return c == '/' || c == ';' || c == ',' || c == '?';
		case encode_user_password: return c == '@' || c == '/' || c == '?' || c == ':';
		case encode_query_component: return true;
		case encode_fragment: return false;
		default: break;
		}
	}
	if (mode == encode_fragment) {
		switch (c) {
		case '!': case '(': case ')': case '*':
			return false;
		}
	}
	return true;
}

bool ishex(char c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'); }
unsigned char unhex(char c)
{
	auto u = static_cast<unsigned char>(c);
	return static_cast<unsigned char>(9 * (u >> 6) + (u & 15));
}

std::optional<std::string> unescape(std::string_view s, Encoding mode)
{
	std::size_t n = 0;
	bool has_plus = false;
	for (std::size_t i = 0; i < s.size();) {
		switch (s[i]) {
		case '%':
			n++;
			if (i + 2 >= s.size() || !ishex(s[i + 1]) || !ishex(s[i + 2])) return std::nullopt;
			if (mode == encode_host && unhex(s[i + 1]) < 8 && s.substr(i, 3) != "%25") return std::nullopt;
			if (mode == encode_zone) {
				unsigned char v = static_cast<unsigned char>(unhex(s[i + 1]) << 4 | unhex(s[i + 2]));
				if (s.substr(i, 3) != "%25" && v != ' ' && should_escape(v, encode_host)) return std::nullopt;
			}
			i += 3;
			break;
		case '+':
			has_plus = mode == encode_query_component;
			i++;
			break;
		default:
			if ((mode == encode_host || mode == encode_zone) && static_cast<unsigned char>(s[i]) < 0x80 &&
			    should_escape(static_cast<unsigned char>(s[i]), mode))
				return std::nullopt;
			i++;
		}
	}
	if (n == 0 && !has_plus) return std::string(s);
	std::string t;
	for (std::size_t i = 0; i < s.size(); i++) {
		if (s[i] == '%') {
			t += static_cast<char>(unhex(s[i + 1]) << 4 | unhex(s[i + 2]));
			i += 2;
		} else if (s[i] == '+') {
			t += mode == encode_query_component ? ' ' : '+';
		} else {
			t += s[i];
		}
	}
	return t;
}

std::string escape(std::string_view s, Encoding mode)
{
	static const char upper[] = "0123456789ABCDEF";
	std::string t;
	for (unsigned char c : s) {
		if (c == ' ' && mode == encode_query_component) {
			t += '+';
		} else if (should_escape(c, mode)) {
			t += '%';
			t += upper[c >> 4];
			t += upper[c & 15];
		} else {
			t += static_cast<char>(c);
		}
	}
	return t;
}

bool valid_encoded(std::string_view s, Encoding mode)
{
	for (unsigned char c : s) {
		switch (c) {
		case '!': case '$': case '&': case '\'': case '(': case ')': case '*': case '+': case ',': case ';': case '=':
		case ':': case '@': case '[': case ']': case '%':
			break;
		default:
			if (should_escape(c, mode)) return false;
		}
	}
	return true;
}

bool valid_optional_port(std::string_view port)
{
	if (port.empty()) return true;
	if (port[0] != ':') return false;
	for (char c : port.substr(1))
		if (c < '0' || c > '9') return false;
	return true;
}

bool valid_userinfo(std::string_view s)
{
	for (char32_t r : runes(s)) {
		if ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) continue;
		switch (r) {
		case '-': case '.': case '_': case ':': case '~': case '!': case '$': case '&': case '\'': case '(': case ')':
		case '*': case '+': case ',': case ';': case '=': case '%': case '@':
			continue;
		default:
			return false;
		}
	}
	return true;
}

// netip.ParseAddr for the inside of brackets: an IPv6 address (with an optional zone), or IPv4.
enum class Addr { invalid, v4, v6 };
Addr parse_addr(const std::string &s)
{
	unsigned char buf[16];
	if (s.find(':') == std::string::npos) return ::inet_pton(AF_INET, s.c_str(), buf) == 1 ? Addr::v4 : Addr::invalid;
	std::string addr = s;
	if (auto pct = s.find('%'); pct != std::string::npos) {
		if (pct + 1 == s.size()) return Addr::invalid;
		addr = s.substr(0, pct);
	}
	return ::inet_pton(AF_INET6, addr.c_str(), buf) == 1 ? Addr::v6 : Addr::invalid;
}

std::optional<std::string> parse_host(std::string_view scheme, std::string_view host)
{
	auto open = host.rfind('[');
	if (open != std::string_view::npos && open > 0) return std::nullopt;
	if (open == 0) {
		auto close = host.rfind(']');
		if (close == std::string_view::npos) return std::nullopt;
		std::string_view colon_port = host.substr(close + 1);
		if (!valid_optional_port(colon_port)) return std::nullopt;
		auto port = unescape(colon_port, encode_host);
		if (!port) return std::nullopt;
		std::string_view hostname = host.substr(1, close - 1);
		std::string unescaped;
		if (auto zone = hostname.find("%25"); zone != std::string_view::npos) {
			auto h = unescape(hostname.substr(0, zone), encode_host);
			auto z = unescape(hostname.substr(zone), encode_zone);
			if (!h || !z) return std::nullopt;
			unescaped = *h + *z;
		} else {
			auto h = unescape(hostname, encode_host);
			if (!h) return std::nullopt;
			unescaped = *h;
		}
		Addr a = parse_addr(unescaped);
		if (a != Addr::v6) return std::nullopt;
		return "[" + unescaped + "]" + *port;
	}
	if (auto i = host.find(':'); i != std::string_view::npos) {
		auto last = host.rfind(':');
		if (last != i && scheme != "http" && scheme != "https") i = last;
		if (!valid_optional_port(host.substr(i))) return std::nullopt;
	}
	return unescape(host, encode_host);
}

std::pair<std::string, std::string> split_host_port(std::string_view host_port)
{
	std::string host(host_port), port;
	auto colon = host.rfind(':');
	if (colon != std::string::npos && valid_optional_port(std::string_view(host).substr(colon))) {
		port = host.substr(colon + 1);
		host = host.substr(0, colon);
	}
	if (host.size() >= 2 && host.front() == '[' && host.back() == ']') host = host.substr(1, host.size() - 2);
	return {host, port};
}

} // namespace

std::optional<Url> parse_url(std::string_view raw)
{
	std::string_view u = raw, frag;
	bool has_frag = false;
	if (auto hash = raw.find('#'); hash != std::string_view::npos) {
		u = raw.substr(0, hash);
		frag = raw.substr(hash + 1);
		has_frag = true;
	}
	for (char c : u)
		if (static_cast<unsigned char>(c) < ' ' || c == 0x7f) return std::nullopt;
	Url url;
	if (u == "*") {
		url.path = "*";
	} else {
		// getScheme
		std::string_view rest = u;
		for (std::size_t i = 0; i < u.size(); i++) {
			char c = u[i];
			if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) continue;
			if ((c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.') {
				if (i == 0) break;
				continue;
			}
			if (c == ':') {
				if (i == 0) return std::nullopt;
				url.scheme = to_lower(u.substr(0, i));
				rest = u.substr(i + 1);
			}
			break;
		}
		std::size_t questions = 0;
		for (char c : rest) questions += c == '?';
		if (!rest.empty() && rest.back() == '?' && questions == 1) {
			url.force_query = true;
			rest.remove_suffix(1);
		} else if (auto q = rest.find('?'); q != std::string_view::npos) {
			url.raw_query = std::string(rest.substr(q + 1));
			rest = rest.substr(0, q);
		}
		bool done = false;
		if (!rest.starts_with("/")) {
			if (!url.scheme.empty()) {
				url.opaque = std::string(rest);
				done = true;
			} else {
				auto segment = rest.substr(0, rest.find('/'));
				if (segment.find(':') != std::string_view::npos) return std::nullopt;
			}
		}
		if (!done) {
			if ((!url.scheme.empty() || !rest.starts_with("///")) && rest.starts_with("//")) {
				std::string_view authority = rest.substr(2);
				rest = {};
				if (auto slash = authority.find('/'); slash != std::string_view::npos) {
					rest = authority.substr(slash);
					authority = authority.substr(0, slash);
				}
				auto at = authority.rfind('@');
				auto host = parse_host(url.scheme, at == std::string_view::npos ? authority : authority.substr(at + 1));
				if (!host) return std::nullopt;
				url.host = *host;
				if (at != std::string_view::npos) {
					std::string_view userinfo = authority.substr(0, at);
					if (!valid_userinfo(userinfo)) return std::nullopt;
					auto colon = userinfo.find(':');
					if (!unescape(userinfo.substr(0, colon), encode_user_password)) return std::nullopt;
					if (colon != std::string_view::npos && !unescape(userinfo.substr(colon + 1), encode_user_password))
						return std::nullopt;
					url.has_user = true;
				}
			}
			auto path = unescape(rest, encode_path);
			if (!path) return std::nullopt;
			url.path = *path;
			if (escape(url.path, encode_path) != rest) url.raw_path = std::string(rest);
		}
	}
	if (has_frag && !frag.empty()) {
		auto f = unescape(frag, encode_fragment);
		if (!f) return std::nullopt;
		url.fragment = *f;
	}
	return url;
}

std::string Url::hostname() const { return split_host_port(host).first; }
std::string Url::port() const { return split_host_port(host).second; }

std::string Url::escaped_path() const
{
	if (!raw_path.empty() && valid_encoded(raw_path, encode_path)) {
		auto p = unescape(raw_path, encode_path);
		if (p && *p == path) return raw_path;
	}
	if (path == "*") return "*";
	return escape(path, encode_path);
}

std::string Url::request_uri() const
{
	std::string result = opaque;
	if (result.empty()) {
		result = escaped_path();
		if (result.empty()) result = "/";
	} else if (result.starts_with("//")) {
		result = scheme + ":" + result;
	}
	if (force_query || !raw_query.empty()) result += "?" + raw_query;
	return result;
}

std::string Url::string() const
{
	std::string buf;
	if (!scheme.empty()) buf += scheme + ":";
	if (!opaque.empty()) {
		buf += opaque;
	} else {
		if (!scheme.empty() || !host.empty() || has_user) {
			if (!host.empty() || !path.empty() || has_user) buf += "//";
			buf += escape(host, encode_host);
		}
		std::string p = escaped_path();
		if (!p.empty() && p[0] != '/' && !host.empty()) buf += '/';
		if (buf.empty()) {
			auto colon = p.find(':');
			if (colon != std::string::npos && p.substr(0, colon).find('/') == std::string::npos) buf += "./";
		}
		buf += p;
	}
	if (force_query || !raw_query.empty()) buf += "?" + raw_query;
	if (!fragment.empty()) buf += "#" + escape(fragment, encode_fragment);
	return buf;
}

std::string escape_path(std::string_view s) { return escape(s, encode_path); }

std::string join_host_port(std::string_view host, std::string_view port)
{
	if (host.find(':') != std::string_view::npos) return "[" + std::string(host) + "]:" + std::string(port);
	return std::string(host) + ":" + std::string(port);
}

namespace {

bool tspecial(unsigned char c)
{
	return std::string_view("()<>@,;:\\\"/[]?=").find(static_cast<char>(c)) != std::string_view::npos;
}
bool token_char(unsigned char c) { return c > 0x20 && c < 0x7f && !tspecial(c); }
bool token(std::string_view s)
{
	if (s.empty()) return false;
	for (unsigned char c : s)
		if (!token_char(c)) return false;
	return true;
}

} // namespace

std::string format_media_type(std::string_view type, std::string_view key, std::string_view value)
{
	static const char upper[] = "0123456789ABCDEF";
	std::string b;
	auto slash = type.find('/');
	if (slash == std::string_view::npos) {
		if (!token(type)) return {};
		b += to_lower(type);
	} else {
		if (!token(type.substr(0, slash)) || !token(type.substr(slash + 1))) return {};
		b += to_lower(type.substr(0, slash)) + "/" + to_lower(type.substr(slash + 1));
	}
	b += "; ";
	if (!token(key)) return {};
	b += to_lower(key);
	bool need = false;
	for (char32_t r : runes(value))
		if ((r < ' ' || r > '~') && r != '\t') need = true;
	if (need) {
		b += "*=utf-8''";
		for (unsigned char ch : value) {
			if (ch <= ' ' || ch >= 0x7f || ch == '*' || ch == '\'' || ch == '%' || tspecial(ch)) {
				b += '%';
				b += upper[ch >> 4];
				b += upper[ch & 15];
			} else {
				b += static_cast<char>(ch);
			}
		}
		return b;
	}
	b += '=';
	if (token(value)) return b + std::string(value);
	b += '"';
	for (char c : value) {
		if (c == '"' || c == '\\') b += '\\';
		b += c;
	}
	return b + '"';
}

std::string rfc3339(std::int64_t seconds)
{
	std::time_t t = static_cast<std::time_t>(seconds);
	std::tm tm{};
	::gmtime_r(&t, &tm);
	char buf[40];
	std::strftime(buf, sizeof buf, "%Y-%m-%dT%H:%M:%SZ", &tm);
	return buf;
}

std::string json_float(double value)
{
	if (!std::isfinite(value)) return "null";
	double a = std::fabs(value);
	char buf[64];
	if (a != 0 && (a < 1e-6 || a >= 1e21)) {
		auto r = std::to_chars(buf, buf + sizeof buf, value, std::chars_format::scientific);
		std::string s(buf, r.ptr);
		// Go writes e-07 as e-7.
		std::size_t n = s.size();
		if (n >= 4 && s[n - 4] == 'e' && s[n - 3] == '-' && s[n - 2] == '0') s.erase(n - 2, 1);
		return s;
	}
	auto r = std::to_chars(buf, buf + sizeof buf, value, std::chars_format::fixed);
	return std::string(buf, r.ptr);
}

} // namespace nkos::storage::go
