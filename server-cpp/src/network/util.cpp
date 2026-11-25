// Go's standard library semantics the network family depends on (see util.hpp).
#include "common/json_depth.hpp"

#include "network/util.hpp"

#include "common/gojson.hpp"
#include "common/process.hpp"

#include <libwebsockets.h>

#include <algorithm>
#include <arpa/inet.h>
#include <cerrno>
#include <charconv>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <ifaddrs.h>
#include <linux/if_packet.h>
#include <net/if.h>
#include <netinet/in.h>
#include <poll.h>
#include <random>
#include <signal.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;

namespace nkos::network {

// --- strings --------------------------------------------------------------------------------------------------------

namespace {

// One UTF-8 rune at s[i]: the code point (0xFFFD for an invalid byte) and its length.
std::pair<char32_t, std::size_t> decode_rune(std::string_view s, std::size_t i)
{
	unsigned char c = static_cast<unsigned char>(s[i]);
	if (c < 0x80) return {c, 1};
	int n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 0;
	if (c >= 0xf5) n = 0;
	if (n == 0 || i + n > s.size()) return {0xfffd, 1};
	char32_t cp = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
	for (int k = 1; k < n; k++) {
		unsigned char cc = static_cast<unsigned char>(s[i + k]);
		if ((cc & 0xc0) != 0x80) return {0xfffd, 1};
		cp = (cp << 6) | (cc & 0x3f);
	}
	if ((n == 3 && (cp < 0x800 || (cp >= 0xd800 && cp <= 0xdfff))) || (n == 4 && (cp < 0x10000 || cp > 0x10ffff)))
		return {0xfffd, 1};
	return {cp, static_cast<std::size_t>(n)};
}

} // namespace

bool is_space_rune(char32_t r)
{
	switch (r) {
	case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xa0: case 0x1680: case 0x2028:
	case 0x2029: case 0x202f: case 0x205f: case 0x3000:
		return true;
	}
	return r >= 0x2000 && r <= 0x200a;
}

std::vector<std::string> fields(std::string_view s)
{
	std::vector<std::string> out;
	std::size_t start = std::string_view::npos;
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if (is_space_rune(r)) {
			if (start != std::string_view::npos) out.emplace_back(s.substr(start, i - start));
			start = std::string_view::npos;
		} else if (start == std::string_view::npos) {
			start = i;
		}
		i += n;
	}
	if (start != std::string_view::npos) out.emplace_back(s.substr(start));
	return out;
}

std::string trim_space(std::string_view s)
{
	std::size_t a = 0;
	while (a < s.size()) {
		auto [r, n] = decode_rune(s, a);
		if (!is_space_rune(r)) break;
		a += n;
	}
	std::size_t b = s.size();
	while (b > a) {
		// The rune that ends at b.
		std::size_t start = b - 1;
		while (start > a && (static_cast<unsigned char>(s[start]) & 0xc0) == 0x80 && b - start < 4) start--;
		auto [r, n] = decode_rune(s, start);
		if (start + n != b) {
			r = 0xfffd;
			start = b - 1;
		}
		if (!is_space_rune(r)) break;
		b = start;
	}
	return std::string(s.substr(a, b - a));
}

std::string trim_right(std::string_view s, std::string_view cutset)
{
	while (!s.empty() && cutset.find(s.back()) != std::string_view::npos) s.remove_suffix(1);
	return std::string(s);
}

std::string trim_left(std::string_view s, std::string_view cutset)
{
	while (!s.empty() && cutset.find(s.front()) != std::string_view::npos) s.remove_prefix(1);
	return std::string(s);
}

std::string trim_chars(std::string_view s, std::string_view cutset) { return trim_left(trim_right(s, cutset), cutset); }

std::vector<std::string> split(std::string_view s, std::string_view sep)
{
	std::vector<std::string> out;
	for (;;) {
		auto at = s.find(sep);
		if (at == std::string_view::npos) {
			out.emplace_back(s);
			return out;
		}
		out.emplace_back(s.substr(0, at));
		s.remove_prefix(at + sep.size());
	}
}

std::string join(const std::vector<std::string> &parts, std::string_view sep)
{
	std::string out;
	for (std::size_t i = 0; i < parts.size(); i++) {
		if (i) out += sep;
		out += parts[i];
	}
	return out;
}

bool valid_utf8(std::string_view s)
{
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if (r == 0xfffd && n == 1 && static_cast<unsigned char>(s[i]) >= 0x80) return false;
		i += n;
	}
	return true;
}

std::size_t rune_count(std::string_view s)
{
	std::size_t count = 0;
	for (std::size_t i = 0; i < s.size(); count++) i += decode_rune(s, i).second;
	return count;
}

std::string hex_encode(std::string_view s)
{
	static const char digits[] = "0123456789abcdef";
	std::string out;
	out.reserve(s.size() * 2);
	for (unsigned char c : s) {
		out += digits[c >> 4];
		out += digits[c & 15];
	}
	return out;
}

std::string go_quote(std::string_view s)
{
	static const char digits[] = "0123456789abcdef";
	std::string out = "\"";
	auto printable = [](char32_t r) {
		if (r < 0x20 || (r >= 0x7f && r < 0xa0)) return false;
		if (r == 0xa0 || r == 0xad || r == 0x1680 || (r >= 0x2000 && r <= 0x200f) || (r >= 0x2028 && r <= 0x202f) ||
		    (r >= 0x205f && r <= 0x2064) || r == 0x3000 || r == 0xfeff || (r >= 0xfff9 && r <= 0xfffb) || r == 0xfffe ||
		    r == 0xffff || (r >= 0xe000 && r <= 0xf8ff) || r >= 0xf0000)
			return false;
		return true;
	};
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = decode_rune(s, i);
		if (r == 0xfffd && n == 1 && static_cast<unsigned char>(s[i]) >= 0x80) {
			unsigned char c = static_cast<unsigned char>(s[i]);
			out += "\\x";
			out += digits[c >> 4];
			out += digits[c & 15];
			i++;
			continue;
		}
		i += n;
		if (r == '"' || r == '\\') {
			out += '\\';
			out += static_cast<char>(r);
			continue;
		}
		if (printable(r)) {
			out.append(s.substr(i - n, n));
			continue;
		}
		switch (r) {
		case '\a': out += "\\a"; continue;
		case '\b': out += "\\b"; continue;
		case '\f': out += "\\f"; continue;
		case '\n': out += "\\n"; continue;
		case '\r': out += "\\r"; continue;
		case '\t': out += "\\t"; continue;
		case '\v': out += "\\v"; continue;
		}
		char buf[16];
		if (r < ' ' || r == 0x7f) std::snprintf(buf, sizeof buf, "\\x%02x", static_cast<unsigned>(r));
		else if (r < 0x10000) std::snprintf(buf, sizeof buf, "\\u%04x", static_cast<unsigned>(r));
		else std::snprintf(buf, sizeof buf, "\\U%08x", static_cast<unsigned>(r));
		out += buf;
	}
	return out + "\"";
}

namespace {

int hex_value(char c)
{
	if (c >= '0' && c <= '9') return c - '0';
	if (c >= 'a' && c <= 'f') return c - 'a' + 10;
	if (c >= 'A' && c <= 'F') return c - 'A' + 10;
	return -1;
}

} // namespace

std::optional<std::string> hex_decode(std::string_view s)
{
	std::string out;
	// An odd length is an error after the pairs before it are checked; any bad digit fails the whole decode.
	for (std::size_t i = 0; i + 1 < s.size(); i += 2) {
		int a = hex_value(s[i]), b = hex_value(s[i + 1]);
		if (a < 0 || b < 0) return std::nullopt;
		out += static_cast<char>(a * 16 + b);
	}
	if (s.size() % 2) return std::nullopt;
	return out;
}

std::optional<long long> atoi(std::string_view s)
{
	if (s.empty()) return std::nullopt;
	bool negative = false;
	std::string_view digits = s;
	if (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-';
		digits.remove_prefix(1);
	}
	if (digits.empty()) return std::nullopt;
	unsigned long long value = 0;
	for (char c : digits) {
		if (c < '0' || c > '9') return std::nullopt;
		if (value > (static_cast<unsigned long long>(1) << 63) / 10 + 1) return std::nullopt;
		value = value * 10 + static_cast<unsigned>(c - '0');
		if (value > (static_cast<unsigned long long>(1) << 63)) return std::nullopt;
	}
	if (!negative && value > static_cast<unsigned long long>(INT64_MAX)) return std::nullopt;
	return negative ? static_cast<long long>(0 - value) : static_cast<long long>(value);
}

std::optional<unsigned long long> parse_uint(std::string_view s, int base, int bits)
{
	if (s.empty()) return std::nullopt;
	bool underscores = false;
	if (base == 0) {
		base = 10;
		if (s[0] == '0') {
			if (s.size() >= 3 && (s[1] == 'x' || s[1] == 'X')) base = 16, s.remove_prefix(2);
			else if (s.size() >= 3 && (s[1] == 'o' || s[1] == 'O')) base = 8, s.remove_prefix(2);
			else if (s.size() >= 3 && (s[1] == 'b' || s[1] == 'B')) base = 2, s.remove_prefix(2);
			else base = 8, s.remove_prefix(1);
		}
		underscores = true;
		if (s.empty() && base == 8) return 0;  // "0"
	}
	unsigned long long max = bits >= 64 ? ~0ull : (1ull << bits) - 1;
	unsigned long long value = 0;
	bool any = false;
	char last = 0;
	for (char c : s) {
		if (c == '_' && underscores) {
			if (last == '_' || !any) return std::nullopt;
			last = c;
			continue;
		}
		int d = hex_value(c);
		if (d < 0 || d >= base) return std::nullopt;
		if (value > (max - static_cast<unsigned>(d)) / static_cast<unsigned>(base)) return std::nullopt;
		value = value * static_cast<unsigned>(base) + static_cast<unsigned>(d);
		any = true;
		last = c;
	}
	if (!any || last == '_') return std::nullopt;
	return value;
}

std::optional<double> parse_float(std::string_view s)
{
	// Decimal forms only: [sign] digits [. digits] [e [sign] digits], or "inf"/"nan" words, which iw never prints.
	std::size_t i = 0;
	if (i < s.size() && (s[i] == '+' || s[i] == '-')) i++;
	std::size_t digits = 0;
	while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++, digits++;
	if (i < s.size() && s[i] == '.') {
		i++;
		while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++, digits++;
	}
	if (digits == 0) return std::nullopt;
	if (i < s.size() && (s[i] == 'e' || s[i] == 'E')) {
		i++;
		if (i < s.size() && (s[i] == '+' || s[i] == '-')) i++;
		std::size_t exp = 0;
		while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++, exp++;
		if (exp == 0) return std::nullopt;
	}
	if (i != s.size()) return std::nullopt;
	std::string_view body = s[0] == '+' ? s.substr(1) : s;
	double value = 0;
	auto [ptr, ec] = std::from_chars(body.data(), body.data() + body.size(), value);
	if (ec == std::errc::result_out_of_range) return std::nullopt;
	if (ec != std::errc() || ptr != body.data() + body.size()) return std::nullopt;
	return value;
}

std::string json_float(double f) { return gojson::number(f); }

// --- errors and files ------------------------------------------------------------------------------------------------

bool Error::not_exist() const { return code == ENOENT; }

Error make_error(std::string text) { return Error{0, std::move(text)}; }

Error wrap(const std::string &prefix, const Error &err) { return Error{err.code, prefix + ": " + err.text}; }

std::string errno_text(int code)
{
	switch (code) {
	case EPERM: return "operation not permitted";
	case ENOENT: return "no such file or directory";
	case ESRCH: return "no such process";
	case EINTR: return "interrupted system call";
	case EIO: return "input/output error";
	case ENXIO: return "no such device or address";
	case E2BIG: return "argument list too long";
	case ENOEXEC: return "exec format error";
	case EBADF: return "bad file descriptor";
	case EAGAIN: return "resource temporarily unavailable";
	case ENOMEM: return "cannot allocate memory";
	case EACCES: return "permission denied";
	case EBUSY: return "device or resource busy";
	case EEXIST: return "file exists";
	case EXDEV: return "invalid cross-device link";
	case ENODEV: return "no such device";
	case ENOTDIR: return "not a directory";
	case EISDIR: return "is a directory";
	case EINVAL: return "invalid argument";
	case ENFILE: return "too many open files in system";
	case EMFILE: return "too many open files";
	case ETXTBSY: return "text file busy";
	case EFBIG: return "file too large";
	case ENOSPC: return "no space left on device";
	case EROFS: return "read-only file system";
	case EMLINK: return "too many links";
	case EPIPE: return "broken pipe";
	case ENAMETOOLONG: return "file name too long";
	case ENOTEMPTY: return "directory not empty";
	case ELOOP: return "too many levels of symbolic links";
	case ECONNREFUSED: return "connection refused";
	case ETIMEDOUT: return "connection timed out";
	case EDQUOT: return "disk quota exceeded";
	case ENETUNREACH: return "network is unreachable";
	case EADDRINUSE: return "address already in use";
	}
	return "errno " + std::to_string(code);
}

Error path_error(const char *op, const std::string &path, int code)
{
	return Error{code, std::string(op) + " " + path + ": " + errno_text(code)};
}

Error join_errors(const std::vector<Error> &errs)
{
	Error out;
	for (const auto &e : errs) {
		if (!e) continue;
		if (out) out.text += "\n";
		else out.code = e.code;
		out.text += e.text;
	}
	return out;
}

std::optional<std::string> read_file(const std::string &path, Error *err)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) {
		if (err) *err = path_error("open", path, errno);
		return std::nullopt;
	}
	std::string data;
	char buf[8192];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0) {
			if (errno == EINTR) continue;
			if (err) *err = path_error("read", path, errno);
			::close(fd);
			return std::nullopt;
		}
		if (n == 0) break;
		data.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	if (err) *err = {};
	return data;
}

Error stat_path(const std::string &path)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0) return path_error("stat", path, errno);
	return {};
}

Error lstat_path(const std::string &path)
{
	struct stat st{};
	if (::lstat(path.c_str(), &st) != 0) return path_error("lstat", path, errno);
	return {};
}

bool exists(const std::string &path) { return !stat_path(path); }

bool is_dir(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && S_ISDIR(st.st_mode);
}

namespace {

Error write_all(int fd, std::string_view data, const std::string &path)
{
	while (!data.empty()) {
		ssize_t n = ::write(fd, data.data(), data.size());
		if (n < 0) {
			if (errno == EINTR) continue;
			return path_error("write", path, errno);
		}
		data.remove_prefix(static_cast<std::size_t>(n));
	}
	return {};
}

} // namespace

Error write_file(const std::string &path, std::string_view data, unsigned mode)
{
	int fd = ::open(path.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, mode);
	if (fd < 0) return path_error("open", path, errno);
	Error err = write_all(fd, data, path);
	if (::close(fd) != 0 && !err) err = path_error("close", path, errno);
	return err;
}

Error write_private_file(const std::string &path, std::string_view data)
{
	if (auto err = write_file(path, data, 0600)) return err;
	return chmod_path(path, 0600);
}

Error write_file_atomic(const std::string &path, std::string_view data)
{
	if (auto err = mkdir_all(dir_name(path), 0755)) return err;
	std::string tmp = path + ".tmp";
	int fd = ::open(tmp.c_str(), O_CREAT | O_WRONLY | O_TRUNC | O_CLOEXEC, 0600);
	if (fd < 0) return path_error("open", tmp, errno);
	Error err = write_all(fd, data, tmp);
	if (::fsync(fd) != 0 && !err) err = path_error("sync", tmp, errno);
	if (::close(fd) != 0 && !err) err = path_error("close", tmp, errno);
	if (err) {
		::unlink(tmp.c_str());
		return err;
	}
	return rename(tmp, path);
}

Error atomicfile_write(const std::string &path, std::string_view data, unsigned perm)
{
	std::string dir = dir_name(path);
	std::string name;
	int fd = -1;
	static thread_local std::mt19937 random{std::random_device{}()};
	for (int attempt = 0; attempt < 10000 && fd < 0; attempt++) {
		name = path_join(dir, "." + base_name(path) + ".tmp-" + std::to_string(random()));
		fd = ::open(name.c_str(), O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd < 0 && errno != EEXIST) return path_error("open", name, errno);
	}
	if (fd < 0) return path_error("open", name, EEXIST);
	Error err;
	if (::fchmod(fd, perm) != 0) err = path_error("chmod", name, errno);
	if (!err) err = write_all(fd, data, name);
	if (!err && ::fsync(fd) != 0) err = path_error("sync", name, errno);
	if (::close(fd) != 0 && !err) err = path_error("close", name, errno);
	if (!err) err = rename(name, path);
	if (err) {
		::unlink(name.c_str());
		return err;
	}
	int d = ::open(dir.c_str(), O_RDONLY | O_CLOEXEC);
	if (d < 0) return path_error("open", dir, errno);
	if (::fsync(d) != 0) err = path_error("sync", dir, errno);
	::close(d);
	return err;
}

Error mkdir(const std::string &path, unsigned mode)
{
	if (::mkdir(path.c_str(), mode) != 0) return path_error("mkdir", path, errno);
	return {};
}

Error mkdir_all(const std::string &path, unsigned mode)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) {
		if (S_ISDIR(st.st_mode)) return {};
		return path_error("mkdir", path, ENOTDIR);
	}
	std::size_t end = path.size();
	while (end > 0 && path[end - 1] == '/') end--;
	std::size_t slash = path.rfind('/', end ? end - 1 : 0);
	if (end > 0 && slash != std::string::npos && slash > 0) {
		std::string parent = path.substr(0, slash);
		while (parent.size() > 1 && parent.back() == '/') parent.pop_back();
		if (auto err = mkdir_all(parent, mode)) return err;
	}
	if (::mkdir(path.c_str(), mode) != 0) {
		int e = errno;
		if (::lstat(path.c_str(), &st) == 0 && S_ISDIR(st.st_mode)) return {};
		return path_error("mkdir", path, e);
	}
	return {};
}

Error chmod_path(const std::string &path, unsigned mode)
{
	if (::chmod(path.c_str(), mode) != 0) return path_error("chmod", path, errno);
	return {};
}

Error remove(const std::string &path)
{
	if (::unlink(path.c_str()) == 0) return {};
	int e = errno;
	if (::rmdir(path.c_str()) == 0) return {};
	int e1 = errno;
	if (e1 != ENOTDIR) e = e1;
	return path_error("remove", path, e);
}

Error remove_all(const std::string &path)
{
	struct stat st{};
	if (::lstat(path.c_str(), &st) != 0) {
		if (errno == ENOENT) return {};
		return path_error("lstat", path, errno);
	}
	if (S_ISDIR(st.st_mode)) {
		Error err;
		for (const auto &entry : read_dir(path))
			if (auto e = remove_all(path_join(path, entry.name)); e && !err) err = e;
		if (::rmdir(path.c_str()) != 0 && errno != ENOENT && !err) err = path_error("unlinkat", path, errno);
		return err;
	}
	if (::unlink(path.c_str()) != 0 && errno != ENOENT) return path_error("unlinkat", path, errno);
	return {};
}

Error rename(const std::string &from, const std::string &to)
{
	struct stat to_st{}, from_st{};
	if (::lstat(to.c_str(), &to_st) == 0 && S_ISDIR(to_st.st_mode)) {
		if (::lstat(from.c_str(), &from_st) != 0)
			return Error{errno, "rename " + from + " " + to + ": " + errno_text(errno)};
		if (to == from || to_st.st_dev != from_st.st_dev || to_st.st_ino != from_st.st_ino)
			return Error{EEXIST, "rename " + from + " " + to + ": " + errno_text(EEXIST)};
	}
	if (::rename(from.c_str(), to.c_str()) != 0) return Error{errno, "rename " + from + " " + to + ": " + errno_text(errno)};
	return {};
}

std::string dir_name(const std::string &path)
{
	auto slash = path.rfind('/');
	if (slash == std::string::npos) return ".";
	std::string dir = path.substr(0, slash);
	while (dir.size() > 1 && dir.back() == '/') dir.pop_back();
	return dir.empty() ? "/" : dir;
}

std::string base_name(const std::string &path)
{
	std::string p = path;
	while (p.size() > 1 && p.back() == '/') p.pop_back();
	auto slash = p.rfind('/');
	return slash == std::string::npos ? p : p.substr(slash + 1);
}

std::string path_join(const std::string &a, const std::string &b)
{
	if (a.empty()) return b;
	if (a.back() == '/') return a + b;
	return a + "/" + b;
}

std::vector<DirEntry> read_dir(const std::string &path, Error *err)
{
	std::vector<DirEntry> out;
	DIR *dir = ::opendir(path.c_str());
	if (!dir) {
		if (err) *err = path_error("open", path, errno);
		return out;
	}
	while (dirent *e = ::readdir(dir)) {
		std::string name = e->d_name;
		if (name == "." || name == "..") continue;
		DirEntry entry{name};
		struct stat st{};
		if (::lstat(path_join(path, name).c_str(), &st) == 0) {
			entry.dir = S_ISDIR(st.st_mode);
			entry.info_ok = true;
			entry.mtime_ns = static_cast<std::int64_t>(st.st_mtim.tv_sec) * 1000000000 + st.st_mtim.tv_nsec;
		} else {
			entry.dir = e->d_type == DT_DIR;
		}
		out.push_back(std::move(entry));
	}
	::closedir(dir);
	std::sort(out.begin(), out.end(), [](const DirEntry &a, const DirEntry &b) { return a.name < b.name; });
	if (err) *err = {};
	return out;
}

std::optional<std::string> eval_symlinks(const std::string &path)
{
	char resolved[PATH_MAX];
	if (!::realpath(path.c_str(), resolved)) return std::nullopt;
	return std::string(resolved);
}

// --- addresses -------------------------------------------------------------------------------------------------------

bool Addr::is4in6() const
{
	if (kind != 6) return false;
	for (int i = 0; i < 10; i++)
		if (b[i]) return false;
	return b[10] == 0xff && b[11] == 0xff;
}

Addr Addr::unmap() const
{
	if (!is4in6()) return *this;
	return addr4(b[12], b[13], b[14], b[15]);
}

bool Addr::operator==(const Addr &o) const { return kind == o.kind && b == o.b && zone == o.zone; }

bool Addr::unspecified() const
{
	if (!zone.empty()) return false;
	if (kind == 4) return !b[0] && !b[1] && !b[2] && !b[3];
	if (kind == 6) return std::all_of(b.begin(), b.end(), [](std::uint8_t x) { return x == 0; });
	return false;
}

bool Addr::loopback() const
{
	Addr a = unmap();
	if (a.kind == 4) return a.b[0] == 127;
	if (a.kind == 6) {
		for (int i = 0; i < 15; i++)
			if (a.b[i]) return false;
		return a.b[15] == 1;
	}
	return false;
}

bool Addr::multicast() const
{
	Addr a = unmap();
	if (a.kind == 4) return (a.b[0] & 0xf0) == 0xe0;
	if (a.kind == 6) return a.b[0] == 0xff;
	return false;
}

bool Addr::private_() const
{
	Addr a = unmap();
	if (a.kind == 4) return a.b[0] == 10 || (a.b[0] == 172 && (a.b[1] & 0xf0) == 16) || (a.b[0] == 192 && a.b[1] == 168);
	if (a.kind == 6) return (a.b[0] & 0xfe) == 0xfc;
	return false;
}

bool Addr::link_local_unicast() const
{
	Addr a = unmap();
	if (a.kind == 4) return a.b[0] == 169 && a.b[1] == 254;
	if (a.kind == 6) return a.b[0] == 0xfe && (a.b[1] & 0xc0) == 0x80;
	return false;
}

Addr Addr::next() const
{
	Addr a = *this;
	int len = kind == 4 ? 4 : 16;
	for (int i = len - 1; i >= 0; i--) {
		if (++a.b[i] != 0) return a;
	}
	return Addr{};  // overflowed
}

namespace {

std::string v4_string(const std::uint8_t *b)
{
	return std::to_string(b[0]) + "." + std::to_string(b[1]) + "." + std::to_string(b[2]) + "." + std::to_string(b[3]);
}

std::string v6_string(const std::array<std::uint8_t, 16> &b)
{
	auto group = [&](int i) { return static_cast<unsigned>(b[2 * i] << 8 | b[2 * i + 1]); };
	int zero_start = 255, zero_end = 255;
	for (int i = 0; i < 8; i++) {
		int j = i;
		while (j < 8 && group(j) == 0) j++;
		if (int l = j - i; l >= 2 && l > zero_end - zero_start) zero_start = i, zero_end = j;
	}
	std::string out;
	char buf[8];
	for (int i = 0; i < 8; i++) {
		if (i == zero_start) {
			out += "::";
			i = zero_end;
			if (i >= 8) break;
		} else if (i > 0) {
			out += ':';
		}
		std::snprintf(buf, sizeof buf, "%x", group(i));
		out += buf;
	}
	return out;
}

} // namespace

std::string Addr::str() const
{
	if (kind == 0) return "invalid IP";
	if (kind == 4) return v4_string(b.data());
	std::string out = is4in6() ? "::ffff:" + v4_string(b.data() + 12) : v6_string(b);
	if (!zone.empty()) out += "%" + zone;
	return out;
}

Addr addr4(std::uint8_t a, std::uint8_t b, std::uint8_t c, std::uint8_t d)
{
	Addr out;
	out.kind = 4;
	out.b[0] = a, out.b[1] = b, out.b[2] = c, out.b[3] = d;
	return out;
}

namespace {

bool parse_v4_fields(std::string_view s, std::uint8_t *out)
{
	int val = 0, pos = 0, dig = 0;
	for (std::size_t i = 0; i < s.size(); i++) {
		char c = s[i];
		if (c >= '0' && c <= '9') {
			if (dig == 1 && val == 0) return false;
			val = val * 10 + (c - '0');
			dig++;
			if (val > 255) return false;
		} else if (c == '.') {
			if (i == 0 || i == s.size() - 1 || s[i - 1] == '.') return false;
			if (pos == 3) return false;
			out[pos++] = static_cast<std::uint8_t>(val);
			val = 0;
			dig = 0;
		} else {
			return false;
		}
	}
	if (pos < 3) return false;
	out[3] = static_cast<std::uint8_t>(val);
	return true;
}

std::optional<Addr> parse_v6(std::string_view in)
{
	std::string_view s = in;
	std::string zone;
	if (auto pct = s.find('%'); pct != std::string_view::npos) {
		zone = std::string(s.substr(pct + 1));
		s = s.substr(0, pct);
		if (zone.empty()) return std::nullopt;
	}
	Addr ip;
	ip.kind = 6;
	ip.zone = zone;
	int ellipsis = -1;
	if (s.size() >= 2 && s[0] == ':' && s[1] == ':') {
		ellipsis = 0;
		s.remove_prefix(2);
		if (s.empty()) return ip;
	}
	int i = 0;
	while (i < 16) {
		std::size_t off = 0;
		std::uint32_t acc = 0;
		for (; off < s.size(); off++) {
			int d = hex_value(s[off]);
			if (d < 0) break;
			acc = (acc << 4) + static_cast<std::uint32_t>(d);
			if (off > 3) return std::nullopt;
			if (acc > 0xffff) return std::nullopt;
		}
		if (off == 0) return std::nullopt;
		if (off < s.size() && s[off] == '.') {
			if (ellipsis < 0 && i != 12) return std::nullopt;
			if (i + 4 > 16) return std::nullopt;
			if (!parse_v4_fields(s, ip.b.data() + i)) return std::nullopt;
			s = {};
			i += 4;
			break;
		}
		ip.b[i] = static_cast<std::uint8_t>(acc >> 8);
		ip.b[i + 1] = static_cast<std::uint8_t>(acc);
		i += 2;
		s.remove_prefix(off);
		if (s.empty()) break;
		if (s[0] != ':') return std::nullopt;
		if (s.size() == 1) return std::nullopt;
		s.remove_prefix(1);
		if (s[0] == ':') {
			if (ellipsis >= 0) return std::nullopt;
			ellipsis = i;
			s.remove_prefix(1);
			if (s.empty()) break;
		}
	}
	if (!s.empty()) return std::nullopt;
	if (i < 16) {
		if (ellipsis < 0) return std::nullopt;
		int n = 16 - i;
		for (int j = i - 1; j >= ellipsis; j--) ip.b[j + n] = ip.b[j];
		for (int j = ellipsis; j < ellipsis + n; j++) ip.b[j] = 0;
	} else if (ellipsis >= 0) {
		return std::nullopt;
	}
	return ip;
}

} // namespace

std::optional<Addr> parse_addr(std::string_view s)
{
	for (char c : s) {
		if (c == '.') {
			Addr a;
			a.kind = 4;
			if (!parse_v4_fields(s, a.b.data())) return std::nullopt;
			return a;
		}
		if (c == ':') return parse_v6(s);
		if (c == '%') return std::nullopt;
	}
	return std::nullopt;
}

Prefix Prefix::masked() const
{
	if (!valid()) return Prefix{};
	Prefix p;
	p.addr = addr;
	p.addr.zone.clear();
	p.bits = bits;
	int len = addr.kind == 4 ? 4 : 16;
	for (int i = 0; i < len; i++) {
		int keep = std::clamp(bits - i * 8, 0, 8);
		p.addr.b[i] &= static_cast<std::uint8_t>(keep == 0 ? 0 : 0xff << (8 - keep));
	}
	return p;
}

bool Prefix::contains(const Addr &a) const
{
	if (!valid() || !a.zone.empty() || a.kind != addr.kind) return false;
	Prefix other{a, bits};
	return other.masked().addr.b == masked().addr.b;
}

std::string Prefix::str() const
{
	if (!valid()) return "invalid Prefix";
	return addr.str() + "/" + std::to_string(bits);
}

std::optional<Prefix> parse_prefix(std::string_view s)
{
	auto slash = s.rfind('/');
	if (slash == std::string_view::npos) return std::nullopt;
	auto ip = parse_addr(s.substr(0, slash));
	if (!ip) return std::nullopt;
	if (ip->is6() && !ip->zone.empty()) return std::nullopt;
	std::string_view bits = s.substr(slash + 1);
	if (bits.size() > 1 && (bits[0] < '1' || bits[0] > '9')) return std::nullopt;
	auto n = atoi(bits);
	if (!n) return std::nullopt;
	int max = ip->is6() ? 128 : 32;
	if (*n < 0 || *n > max) return std::nullopt;
	return Prefix{*ip, static_cast<int>(*n)};
}

std::optional<IP16> parse_ip(std::string_view s)
{
	auto a = parse_addr(s);
	if (!a || !a->zone.empty()) return std::nullopt;
	IP16 out{};
	if (a->is4()) {
		out[10] = out[11] = 0xff;
		std::copy(a->b.begin(), a->b.begin() + 4, out.begin() + 12);
	} else {
		out = a->b;
	}
	return out;
}

bool ip_is_v4(const IP16 &ip)
{
	for (int i = 0; i < 10; i++)
		if (ip[i]) return false;
	return ip[10] == 0xff && ip[11] == 0xff;
}

std::string ip_string(const IP16 &ip)
{
	if (ip_is_v4(ip)) return v4_string(ip.data() + 12);
	return v6_string(ip);
}

bool parse_mac_ok(std::string_view s)
{
	if (s.size() < 14) return false;
	auto hex_pair = [&](std::size_t i) { return hex_value(s[i]) >= 0 && hex_value(s[i + 1]) >= 0; };
	if (s[2] == ':' || s[2] == '-') {
		if ((s.size() + 1) % 3 != 0) return false;
		std::size_t n = (s.size() + 1) / 3;
		if (n != 6 && n != 8 && n != 20) return false;
		for (std::size_t x = 0, i = 0; i < n; i++, x += 3) {
			if (!hex_pair(x)) return false;
			if (x + 2 < s.size() && s[x + 2] != s[2]) return false;
		}
		return true;
	}
	if (s[4] == '.') {
		if ((s.size() + 1) % 5 != 0) return false;
		std::size_t n = 2 * (s.size() + 1) / 5;
		if (n != 6 && n != 8 && n != 20) return false;
		for (std::size_t x = 0, i = 0; i < n; i += 2, x += 5) {
			if (!hex_pair(x) || !hex_pair(x + 2)) return false;
			if (x + 4 < s.size() && s[x + 4] != '.') return false;
		}
		return true;
	}
	return false;
}

bool split_host_port(std::string_view hostport, std::string &host, std::string &port)
{
	auto i = hostport.rfind(':');
	if (i == std::string_view::npos) return false;
	std::size_t j = 0, k = 0;
	if (hostport[0] == '[') {
		auto end = hostport.find(']');
		if (end == std::string_view::npos) return false;
		if (end + 1 == hostport.size()) return false;
		if (end + 1 != i) return false;
		host = std::string(hostport.substr(1, end - 1));
		j = 1;
		k = end + 1;
	} else {
		host = std::string(hostport.substr(0, i));
		if (host.find(':') != std::string::npos) return false;
	}
	if (hostport.substr(j).find('[') != std::string_view::npos) return false;
	if (hostport.substr(k).find(']') != std::string_view::npos) return false;
	port = std::string(hostport.substr(i + 1));
	return true;
}

std::string join_host_port(const std::string &host, const std::string &port)
{
	if (host.find(':') != std::string::npos) return "[" + host + "]:" + port;
	return host + ":" + port;
}

// --- interfaces ------------------------------------------------------------------------------------------------------

std::string IfAddr::str() const { return ip_string(ip) + "/" + std::to_string(prefix_len); }

std::string IfAddr::mask_str() const
{
	IP16 mask{};
	int len = v4 ? 4 : 16;
	for (int i = 0; i < len; i++) {
		int keep = std::clamp(prefix_len - i * 8, 0, 8);
		mask[static_cast<std::size_t>(i)] = static_cast<std::uint8_t>(keep == 0 ? 0 : 0xff << (8 - keep));
	}
	if (!v4) return ip_string(mask);
	// A 4-byte net.IP prints dotted.
	return v4_string(mask.data());
}

namespace {

int mask_bits(const std::uint8_t *b, int len)
{
	int bits = 0;
	for (int i = 0; i < len; i++) {
		std::uint8_t v = b[i];
		while (v & 0x80) bits++, v = static_cast<std::uint8_t>(v << 1);
		if (b[i] != 0xff) break;
	}
	return bits;
}

} // namespace

std::vector<Interface> interfaces(bool *ok)
{
	std::vector<Interface> out;
	ifaddrs *list = nullptr;
	if (::getifaddrs(&list) != 0) {
		if (ok) *ok = false;
		return out;
	}
	auto find = [&](const std::string &name) -> Interface & {
		for (auto &i : out)
			if (i.name == name) return i;
		Interface i;
		i.name = name;
		i.index = static_cast<int>(::if_nametoindex(name.c_str()));
		out.push_back(i);
		return out.back();
	};
	for (ifaddrs *a = list; a; a = a->ifa_next) {
		if (!a->ifa_name) continue;
		std::string name = a->ifa_name;
		if (!a->ifa_addr) {
			find(name).flags = a->ifa_flags;
			continue;
		}
		int family = a->ifa_addr->sa_family;
		if (family == AF_PACKET) {
			auto &i = find(name);
			auto *ll = reinterpret_cast<sockaddr_ll *>(a->ifa_addr);
			i.index = ll->sll_ifindex;
			i.flags = a->ifa_flags;
			std::string hw(reinterpret_cast<const char *>(ll->sll_addr), std::min<std::size_t>(ll->sll_halen, 8));
			if (std::any_of(hw.begin(), hw.end(), [](char c) { return c != 0; })) i.hwaddr = hw;
			continue;
		}
		if (family != AF_INET && family != AF_INET6) continue;
		// An IPv4 address with a label (eth0:1) belongs to the interface before the colon.
		if (auto colon = name.find(':'); colon != std::string::npos && family == AF_INET) name.resize(colon);
		auto &i = find(name);
		IfAddr addr;
		if (family == AF_INET) {
			auto *in = reinterpret_cast<sockaddr_in *>(a->ifa_addr);
			addr.v4 = true;
			addr.ip[10] = addr.ip[11] = 0xff;
			std::memcpy(addr.ip.data() + 12, &in->sin_addr, 4);
			if (a->ifa_netmask) addr.prefix_len = mask_bits(reinterpret_cast<const std::uint8_t *>(&reinterpret_cast<sockaddr_in *>(a->ifa_netmask)->sin_addr), 4);
		} else {
			auto *in6 = reinterpret_cast<sockaddr_in6 *>(a->ifa_addr);
			std::memcpy(addr.ip.data(), &in6->sin6_addr, 16);
			if (a->ifa_netmask) addr.prefix_len = mask_bits(reinterpret_cast<const std::uint8_t *>(&reinterpret_cast<sockaddr_in6 *>(a->ifa_netmask)->sin6_addr), 16);
		}
		i.addrs.push_back(addr);
	}
	::freeifaddrs(list);
	std::stable_sort(out.begin(), out.end(), [](const Interface &a, const Interface &b) { return a.index < b.index; });
	if (ok) *ok = true;
	return out;
}

std::optional<Interface> interface_by_name(const std::string &name)
{
	for (auto &i : interfaces())
		if (i.name == name) return i;
	return std::nullopt;
}

// --- commands --------------------------------------------------------------------------------------------------------

std::optional<std::string> look_path(const std::string &name)
{
	auto executable = [](const std::string &path) {
		struct stat st{};
		return ::stat(path.c_str(), &st) == 0 && !S_ISDIR(st.st_mode) && (st.st_mode & 0111) != 0;
	};
	if (name.find('/') != std::string::npos) return executable(name) ? std::optional(name) : std::nullopt;
	const char *path = ::getenv("PATH");
	for (const auto &dir : split(path ? path : "", ":")) {
		std::string candidate = path_join(dir.empty() ? "." : dir, name);
		if (executable(candidate)) return candidate;
	}
	return std::nullopt;
}

RunResult run(const std::vector<std::string> &argv, const RunOptions &options)
{
	RunResult r;
	if (argv.empty()) {
		r.error = "exec: no command";
		return r;
	}
	std::string program = argv[0];
	if (program.find('/') == std::string::npos) {
		auto found = look_path(program);
		if (!found) {
			r.error = "exec: \"" + program + "\": executable file not found in $PATH";
			return r;
		}
		program = *found;
	}
	int out[2] = {-1, -1}, err[2] = {-1, -1}, in[2] = {-1, -1}, report[2] = {-1, -1};
	if (::pipe2(out, O_CLOEXEC) || ::pipe2(err, O_CLOEXEC) || ::pipe2(in, O_CLOEXEC) || ::pipe2(report, O_CLOEXEC)) {
		r.error = errno_text(errno);
		for (int fd : {out[0], out[1], err[0], err[1], in[0], in[1], report[0], report[1]})
			if (fd >= 0) ::close(fd);
		return r;
	}
	std::vector<char *> args;
	for (const auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	std::vector<char *> env;
	for (char **e = environ; *e; e++) env.push_back(*e);
	for (const auto &e : options.env) env.push_back(const_cast<char *>(e.c_str()));
	env.push_back(nullptr);

	pid_t pid = ::fork();
	if (pid < 0) {
		r.error = "fork/exec " + program + ": " + errno_text(errno);
		for (int fd : {out[0], out[1], err[0], err[1], in[0], in[1], report[0], report[1]}) ::close(fd);
		return r;
	}
	if (pid == 0) {
		::dup2(in[0], 0);
		::dup2(out[1], 1);
		::dup2(options.combined ? out[1] : err[1], 2);
		struct sigaction sa{};
		sa.sa_handler = SIG_DFL;
		for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGUSR1, SIGUSR2}) ::sigaction(s, &sa, nullptr);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execve(program.c_str(), args.data(), env.data());
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	::close(out[1]);
	::close(err[1]);
	::close(in[0]);
	::close(report[1]);
	int exec_errno = 0;
	bool started = ::read(report[0], &exec_errno, sizeof exec_errno) != sizeof exec_errno;
	::close(report[0]);

	int in_fd = in[1];
	std::size_t written = 0;
	if (options.input.empty() || !started) {
		::close(in_fd);
		in_fd = -1;
	} else {
		::fcntl(in_fd, F_SETFL, O_NONBLOCK);
	}
	// As os/exec: the context's timeout kills the process (not what it started); the output is read until its pipes
	// close, and with a wait delay at most that long after the process ended.
	using clock = std::chrono::steady_clock;
	auto deadline = options.timeout ? clock::now() + *options.timeout : clock::time_point::max();
	bool out_open = true, err_open = true, killed = false, exited = false, delay_expired = false;
	int status = 0;
	clock::time_point ended{};
	while (out_open || err_open || in_fd >= 0) {
		auto now = clock::now();
		if (!exited) {
			pid_t w = ::waitpid(pid, &status, WNOHANG);
			if (w == pid) exited = true, ended = now;
		}
		if (!killed && !exited && now >= deadline) {
			::kill(pid, SIGKILL);
			killed = true;
		}
		if (exited && options.wait_delay && now - ended >= *options.wait_delay) {
			delay_expired = true;
			break;
		}
		// Poll in short steps while the process runs, to notice its end and the deadline.
		int wait = 100;
		if (exited && !options.wait_delay) wait = -1;
		pollfd fds[3];
		int n = 0, oi = -1, ei = -1, ii = -1;
		if (out_open) oi = n, fds[n++] = {out[0], POLLIN, 0};
		if (err_open) ei = n, fds[n++] = {err[0], POLLIN, 0};
		if (in_fd >= 0) ii = n, fds[n++] = {in_fd, POLLOUT, 0};
		if (::poll(fds, static_cast<nfds_t>(n), wait) < 0) {
			if (errno == EINTR) continue;
			break;
		}
		char buf[4096];
		if (oi >= 0 && fds[oi].revents) {
			ssize_t got = ::read(out[0], buf, sizeof buf);
			if (got > 0) r.out.append(buf, static_cast<std::size_t>(got));
			else if (got == 0 || (errno != EAGAIN && errno != EINTR)) out_open = false;
		}
		if (ei >= 0 && fds[ei].revents) {
			ssize_t got = ::read(err[0], buf, sizeof buf);
			if (got > 0) r.err_out.append(buf, static_cast<std::size_t>(got));
			else if (got == 0 || (errno != EAGAIN && errno != EINTR)) err_open = false;
		}
		if (ii >= 0 && fds[ii].revents) {
			ssize_t put = ::write(in_fd, options.input.data() + written, options.input.size() - written);
			if (put > 0) written += static_cast<std::size_t>(put);
			if (put < 0 && errno != EAGAIN && errno != EINTR) written = options.input.size();
			if (written >= options.input.size()) {
				::close(in_fd);
				in_fd = -1;
			}
		}
	}
	if (in_fd >= 0) ::close(in_fd);
	::close(out[0]);
	::close(err[0]);
	if (!exited) {
		// The pipes closed first; a process past its deadline is killed now.
		while (!exited) {
			pid_t w = ::waitpid(pid, &status, options.timeout ? WNOHANG : 0);
			if (w == pid) {
				exited = true;
				break;
			}
			if (w < 0 && errno != EINTR) break;
			if (w == 0) {
				if (!killed && clock::now() >= deadline) {
					::kill(pid, SIGKILL);
					killed = true;
				}
				::usleep(10000);
			}
		}
	}
	if (!started) {
		r.error = "fork/exec " + program + ": " + errno_text(exec_errno);
		return r;
	}
	if (WIFEXITED(status)) {
		r.exit_code = WEXITSTATUS(status);
		if (r.exit_code != 0) r.error = "exit status " + std::to_string(r.exit_code);
	} else if (WIFSIGNALED(status)) {
		int sig = WTERMSIG(status);
		r.error = std::string("signal: ") + (sig == SIGKILL ? "killed" : sig == SIGTERM ? "terminated" : ::strsignal(sig));
	}
	if (killed && r.error.empty()) r.error = "signal: killed";
	if (delay_expired && r.error.empty()) r.error = "exec: WaitDelay expired before I/O complete";
	return r;
}

std::vector<std::string> unprotected(const std::string &name, const std::vector<std::string> &args)
{
	std::vector<std::string> argv = {"/bin/sh", "-c",
	                                 "echo 0 >/proc/self/oom_score_adj; renice 0 \"$$\" >/dev/null; exec \"$0\" \"$@\"", name};
	argv.insert(argv.end(), args.begin(), args.end());
	return argv;
}

Error sync_files()
{
	auto r = run({"sync"});
	return r.ok() ? Error{} : make_error(r.error);
}

// --- JSON (encoding/json) --------------------------------------------------------------------------------------------

namespace {

struct JsonReader {
	std::string_view s;
	std::size_t i = 0;
	int depth = 0;

	void space()
	{
		while (i < s.size() && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r')) i++;
	}

	static void put_rune(std::string &out, char32_t r)
	{
		if (r < 0x80) {
			out += static_cast<char>(r);
		} else if (r < 0x800) {
			out += static_cast<char>(0xc0 | (r >> 6));
			out += static_cast<char>(0x80 | (r & 0x3f));
		} else if (r < 0x10000) {
			out += static_cast<char>(0xe0 | (r >> 12));
			out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
			out += static_cast<char>(0x80 | (r & 0x3f));
		} else {
			out += static_cast<char>(0xf0 | (r >> 18));
			out += static_cast<char>(0x80 | ((r >> 12) & 0x3f));
			out += static_cast<char>(0x80 | ((r >> 6) & 0x3f));
			out += static_cast<char>(0x80 | (r & 0x3f));
		}
	}

	int hex4(std::size_t at)
	{
		if (at + 4 > s.size()) return -1;
		int v = 0;
		for (std::size_t k = 0; k < 4; k++) {
			int d = hex_value(s[at + k]);
			if (d < 0) return -1;
			v = v * 16 + d;
		}
		return v;
	}

	bool string(std::string &out)
	{
		if (i >= s.size() || s[i] != '"') return false;
		i++;
		while (i < s.size()) {
			unsigned char c = static_cast<unsigned char>(s[i]);
			if (c == '"') {
				i++;
				return true;
			}
			if (c < 0x20) return false;
			if (c == '\\') {
				if (i + 1 >= s.size()) return false;
				char e = s[i + 1];
				i += 2;
				switch (e) {
				case '"': out += '"'; break;
				case '\\': out += '\\'; break;
				case '/': out += '/'; break;
				case 'b': out += '\b'; break;
				case 'f': out += '\f'; break;
				case 'n': out += '\n'; break;
				case 'r': out += '\r'; break;
				case 't': out += '\t'; break;
				case 'u': {
					int v = hex4(i);
					if (v < 0) return false;
					i += 4;
					char32_t r = static_cast<char32_t>(v);
					if (r >= 0xd800 && r < 0xe000) {
						// A surrogate: a high one followed by an escaped low one is one rune; anything else is U+FFFD.
						if (r < 0xdc00 && i + 1 < s.size() && s[i] == '\\' && s[i + 1] == 'u') {
							int low = hex4(i + 2);
							if (low >= 0xdc00 && low < 0xe000) {
								r = 0x10000 + ((r - 0xd800) << 10) + static_cast<char32_t>(low - 0xdc00);
								i += 6;
								put_rune(out, r);
								break;
							}
						}
						r = 0xfffd;
					}
					put_rune(out, r);
					break;
				}
				default:
					return false;
				}
				continue;
			}
			if (c < 0x80) {
				out += static_cast<char>(c);
				i++;
				continue;
			}
			auto [r, n] = decode_rune(s, i);
			if (r == 0xfffd && n == 1) put_rune(out, 0xfffd);
			else out.append(s.substr(i, n));
			i += n;
		}
		return false;
	}

	bool number(std::string &out)
	{
		std::size_t start = i;
		if (i < s.size() && s[i] == '-') i++;
		if (i >= s.size()) return false;
		if (s[i] == '0') {
			i++;
		} else if (s[i] >= '1' && s[i] <= '9') {
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
		} else {
			return false;
		}
		if (i < s.size() && s[i] == '.') {
			i++;
			std::size_t d = i;
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
			if (i == d) return false;
		}
		if (i < s.size() && (s[i] == 'e' || s[i] == 'E')) {
			i++;
			if (i < s.size() && (s[i] == '+' || s[i] == '-')) i++;
			std::size_t d = i;
			while (i < s.size() && s[i] >= '0' && s[i] <= '9') i++;
			if (i == d) return false;
		}
		out = std::string(s.substr(start, i - start));
		return true;
	}

	bool literal(std::string_view word)
	{
		if (s.substr(i, word.size()) != word) return false;
		i += word.size();
		return true;
	}

	bool value(JValue &v)
	{
		if (++depth > max_json_depth) return false;
		space();
		if (i >= s.size()) return false;
		char c = s[i];
		bool ok = false;
		if (c == '{') {
			v.kind = JValue::Kind::object;
			i++;
			space();
			if (i < s.size() && s[i] == '}') {
				i++;
				ok = true;
			} else {
				for (;;) {
					space();
					std::string key;
					if (!string(key)) break;
					space();
					if (i >= s.size() || s[i] != ':') break;
					i++;
					JValue member;
					if (!value(member)) break;
					v.members.emplace_back(std::move(key), std::move(member));
					space();
					if (i < s.size() && s[i] == ',') {
						i++;
						continue;
					}
					if (i < s.size() && s[i] == '}') {
						i++;
						ok = true;
					}
					break;
				}
			}
		} else if (c == '[') {
			v.kind = JValue::Kind::array;
			i++;
			space();
			if (i < s.size() && s[i] == ']') {
				i++;
				ok = true;
			} else {
				for (;;) {
					JValue item;
					if (!value(item)) break;
					v.items.push_back(std::move(item));
					space();
					if (i < s.size() && s[i] == ',') {
						i++;
						continue;
					}
					if (i < s.size() && s[i] == ']') {
						i++;
						ok = true;
					}
					break;
				}
			}
		} else if (c == '"') {
			v.kind = JValue::Kind::string;
			ok = string(v.text);
		} else if (c == 't') {
			v.kind = JValue::Kind::boolean;
			v.flag = true;
			ok = literal("true");
		} else if (c == 'f') {
			v.kind = JValue::Kind::boolean;
			ok = literal("false");
		} else if (c == 'n') {
			ok = literal("null");
		} else {
			v.kind = JValue::Kind::number;
			ok = number(v.text);
		}
		depth--;
		return ok;
	}
};

// strconv.ParseInt(lit, 10, 64) of a JSON number literal.
std::optional<long long> json_int(const std::string &literal)
{
	if (literal.find_first_of(".eE") != std::string::npos) return std::nullopt;
	return atoi(literal);
}

bool fold_equal(std::string_view a, std::string_view b)
{
	if (a.size() != b.size()) return false;
	for (std::size_t k = 0; k < a.size(); k++) {
		char x = a[k], y = b[k];
		if (x >= 'A' && x <= 'Z') x = static_cast<char>(x - 'A' + 'a');
		if (y >= 'A' && y <= 'Z') y = static_cast<char>(y - 'A' + 'a');
		if (x != y) return false;
	}
	return true;
}

// url.QueryUnescape for a form component; nullopt for a bad escape.
std::optional<std::string> form_unescape(std::string_view s)
{
	std::string out;
	for (std::size_t k = 0; k < s.size(); k++) {
		if (s[k] == '+') {
			out += ' ';
		} else if (s[k] == '%') {
			if (k + 2 >= s.size()) return std::nullopt;
			int a = hex_value(s[k + 1]), b = hex_value(s[k + 2]);
			if (a < 0 || b < 0) return std::nullopt;
			out += static_cast<char>(a * 16 + b);
			k += 2;
		} else {
			out += s[k];
		}
	}
	return out;
}

// url.ParseQuery into values; false when any pair is malformed (Request.ParseForm fails then).
bool parse_query(std::string_view text, std::map<std::string, std::vector<std::string>> &out)
{
	bool good = true;
	while (!text.empty()) {
		auto amp = text.find('&');
		std::string_view pair = text.substr(0, amp);
		text = amp == std::string_view::npos ? std::string_view{} : text.substr(amp + 1);
		if (pair.find(';') != std::string_view::npos) {
			good = false;
			continue;
		}
		if (pair.empty()) continue;
		auto eq = pair.find('=');
		auto key = form_unescape(pair.substr(0, eq));
		auto value = form_unescape(eq == std::string_view::npos ? std::string_view{} : pair.substr(eq + 1));
		if (!key || !value) {
			good = false;
			continue;
		}
		out[*key].push_back(*value);
	}
	return good;
}

std::optional<bool> parse_bool(const std::string &t)
{
	if (t == "1" || t == "t" || t == "T" || t == "true" || t == "TRUE" || t == "True") return true;
	if (t == "0" || t == "f" || t == "F" || t == "false" || t == "FALSE" || t == "False") return false;
	return std::nullopt;
}

} // namespace

std::optional<JValue> parse_json(std::string_view text)
{
	JsonReader reader{text};
	JValue v;
	if (!reader.value(v)) return std::nullopt;
	return v;
}

std::optional<JValue> parse_json_whole(std::string_view text)
{
	JsonReader reader{text};
	JValue v;
	if (!reader.value(v)) return std::nullopt;
	reader.space();
	if (reader.i != text.size()) return std::nullopt;
	return v;
}

std::string go_query(const std::string &query, const std::string &name)
{
	std::map<std::string, std::vector<std::string>> values;
	(void)parse_query(query, values);
	auto it = values.find(name);
	return it == values.end() || it->second.empty() ? "" : it->second.front();
}

RequestData request_data(const http::Request &req)
{
	return RequestData{req.method, req.header("content-type"), req.query, req.body};
}

Binder Binder::bind(const RequestData &data, bool json_only)
{
	Binder b;
	// c.ContentType(): up to the first space or semicolon, compared as written.
	std::string type = data.content_type;
	if (auto cut = type.find_first_of(" ;"); cut != std::string::npos) type.resize(cut);
	bool json = json_only || (data.method != "GET" && type == "application/json");
	if (json) {
		b.json_ = true;
		auto v = parse_json(data.body);
		if (!v) return b;
		// A JSON null leaves the struct as it is; anything else must be an object.
		if (v->kind != JValue::Kind::object && v->kind != JValue::Kind::null) return b;
		b.value_ = std::move(*v);
		b.ok_ = true;
		return b;
	}
	// Request.ParseForm: the url-encoded body of a POST, PUT or PATCH first, then the query. A multipart form (Gin's
	// FormMultipart) is not bound here: the web interface sends JSON.
	bool good = data.method == "GET" || type != "multipart/form-data";
	if (data.method == "POST" || data.method == "PUT" || data.method == "PATCH") {
		std::string media = data.content_type.substr(0, data.content_type.find(';'));
		media = trim_space(media);
		for (auto &c : media) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
		if (media == "application/x-www-form-urlencoded") good = parse_query(data.body, b.form_) && good;
	}
	std::map<std::string, std::vector<std::string>> query;
	good = parse_query(data.query, query) && good;
	for (auto &[k, v] : query) b.form_[k].insert(b.form_[k].end(), v.begin(), v.end());
	b.ok_ = good;
	return b;
}

const JValue *Binder::member(const std::string &name) const
{
	// Not used for the form; the last member whose key matches wins, as encoding/json assigns in order.
	const JValue *found = nullptr;
	for (const auto &[key, value] : value_.members)
		if (key == name || fold_equal(key, name)) found = &value;
	return found;
}

bool Binder::str(const std::string &json_name, const std::string &form_name, std::string &out)
{
	if (!json_) {
		auto it = form_.find(form_name);
		if (it == form_.end()) return false;
		out = it->second.empty() ? "" : it->second.front();
		return true;
	}
	const JValue *v = member(json_name);
	if (!v || v->kind == JValue::Kind::null) return false;
	if (v->kind != JValue::Kind::string) {
		ok_ = false;
		return false;
	}
	out = v->text;
	return true;
}

bool Binder::boolean(const std::string &json_name, const std::string &form_name, bool &out)
{
	if (!json_) {
		auto it = form_.find(form_name);
		if (it == form_.end()) return false;
		std::string t = it->second.empty() ? "" : it->second.front();
		if (t.empty()) t = "false";
		auto b = parse_bool(t);
		if (!b) {
			ok_ = false;
			return false;
		}
		out = *b;
		return true;
	}
	const JValue *v = member(json_name);
	if (!v || v->kind == JValue::Kind::null) return false;
	if (v->kind != JValue::Kind::boolean) {
		ok_ = false;
		return false;
	}
	out = v->flag;
	return true;
}

bool Binder::integer(const std::string &json_name, const std::string &form_name, long long &out)
{
	if (!json_) {
		auto it = form_.find(form_name);
		if (it == form_.end()) return false;
		std::string t = it->second.empty() ? "" : it->second.front();
		if (t.empty()) t = "0";
		auto n = atoi(t);
		if (!n) {
			ok_ = false;
			return false;
		}
		out = *n;
		return true;
	}
	const JValue *v = member(json_name);
	if (!v || v->kind == JValue::Kind::null) return false;
	std::optional<long long> n;
	if (v->kind == JValue::Kind::number) n = json_int(v->text);
	if (!n) {
		ok_ = false;
		return false;
	}
	out = *n;
	return true;
}

bool Binder::strings(const std::string &json_name, const std::string &form_name, std::vector<std::string> &out)
{
	if (!json_) {
		auto it = form_.find(form_name);
		if (it == form_.end()) return false;
		out = it->second;
		return true;
	}
	const JValue *v = member(json_name);
	if (!v) return false;
	if (v->kind == JValue::Kind::null) {
		out.clear();
		return false;
	}
	if (v->kind != JValue::Kind::array) {
		ok_ = false;
		return false;
	}
	out.clear();
	for (const auto &item : v->items) {
		if (item.kind == JValue::Kind::string) out.push_back(item.text);
		else if (item.kind == JValue::Kind::null) out.emplace_back();
		else {
			ok_ = false;
			out.emplace_back();
		}
	}
	return true;
}

std::optional<Binder> Binder::object(const std::string &json_name)
{
	if (!json_) return std::nullopt;
	const JValue *v = member(json_name);
	if (!v || v->kind == JValue::Kind::null) return std::nullopt;
	if (v->kind != JValue::Kind::object) {
		ok_ = false;
		return std::nullopt;
	}
	Binder b;
	b.json_ = true;
	b.ok_ = true;
	b.value_ = *v;
	return b;
}

// --- answers ---------------------------------------------------------------------------------------------------------

std::string quote(std::string_view s) { return gojson::quote(s); }

void send(http::Response &res, int code, std::string_view msg, std::string_view data_json)
{
	std::string body = "{\"code\":" + std::to_string(code) + ",\"msg\":" + quote(msg) + ",\"data\":";
	body += data_json.empty() ? std::string_view("null") : data_json;
	body += "}";
	res.json(200, std::move(body));
}

std::string json_strings(const std::vector<std::string> &list, bool nil_as_null)
{
	if (list.empty() && nil_as_null) return "null";
	std::string out = "[";
	for (std::size_t i = 0; i < list.size(); i++) out += (i ? "," : "") + quote(list[i]);
	return out + "]";
}

std::optional<IP16> local_address(const http::Request &req)
{
	if (!req.wsi) return std::nullopt;
	lws *net = lws_get_network_wsi(req.wsi);
	int fd = lws_get_socket_fd(net ? net : req.wsi);
	if (fd < 0) return std::nullopt;
	sockaddr_storage ss{};
	socklen_t len = sizeof ss;
	if (::getsockname(fd, reinterpret_cast<sockaddr *>(&ss), &len) != 0) return std::nullopt;
	IP16 ip{};
	if (ss.ss_family == AF_INET) {
		ip[10] = ip[11] = 0xff;
		std::memcpy(ip.data() + 12, &reinterpret_cast<sockaddr_in *>(&ss)->sin_addr, 4);
	} else if (ss.ss_family == AF_INET6) {
		std::memcpy(ip.data(), &reinterpret_cast<sockaddr_in6 *>(&ss)->sin6_addr, 16);
	} else {
		return std::nullopt;
	}
	return ip;
}

} // namespace nkos::network
