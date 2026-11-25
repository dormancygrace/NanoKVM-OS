// Image transfers into /data, as the Go server's service/download: a download from an http(s) URL with the image's
// curl (OpenSSL with ChaCha20 preferred; a lost connection continues from the bytes on the card when the server
// named the file's version with a strong ETag, also after a server restart, from a checkpoint in /run), and a
// browser's upload of an image (multipart, streamed to the card, with an optional SHA-256 to check). One transfer
// at a time, also against the other services' uploads (the sentinel /run/nanokvm-transfer-in-progress); 128 MiB
// stay free. A finished transfer is renamed into place only when no drive has that name.

#include "storage/internal.hpp"
#include "storage/storage.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "http/upload.hpp"

#include <glaze/glaze.hpp>
#include <openssl/evp.h>

#include <cerrno>
#include <climits>
#include <csignal>
#include <dirent.h>
#include <thread>
#include <cstring>
#include <fcntl.h>
#include <format>
#include <sys/prctl.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <sys/wait.h>
#include <unistd.h>

namespace nkos::storage {

// --- SHA-256, strconv.Quote ---

Sha256::Sha256() : ctx_(EVP_MD_CTX_new()) { EVP_DigestInit_ex(static_cast<EVP_MD_CTX *>(ctx_), EVP_sha256(), nullptr); }
Sha256::~Sha256() { EVP_MD_CTX_free(static_cast<EVP_MD_CTX *>(ctx_)); }
void Sha256::update(std::string_view data) { EVP_DigestUpdate(static_cast<EVP_MD_CTX *>(ctx_), data.data(), data.size()); }
std::string Sha256::digest()
{
	unsigned char out[32];
	unsigned int n = 0;
	EVP_DigestFinal_ex(static_cast<EVP_MD_CTX *>(ctx_), out, &n);
	return std::string(reinterpret_cast<char *>(out), n);
}
std::string Sha256::hex(std::string_view bytes)
{
	static const char digits[] = "0123456789abcdef";
	std::string out;
	for (unsigned char c : bytes) {
		out += digits[c >> 4];
		out += digits[c & 15];
	}
	return out;
}

namespace {

// unicode.IsPrint for the runes a quoted name meets: not the spaces other than ' ', not the controls, not the
// format characters, not private use and not the noncharacters.
bool printable(char32_t c)
{
	if (c < 0x20 || (c >= 0x7f && c <= 0xa0)) return false;
	if (go::is_space(c) && c != ' ') return false;
	static const std::pair<char32_t, char32_t> format[] = {
		{0xad, 0xad}, {0x600, 0x605}, {0x61c, 0x61c}, {0x6dd, 0x6dd}, {0x70f, 0x70f}, {0x890, 0x891}, {0x8e2, 0x8e2},
		{0x180e, 0x180e}, {0x200b, 0x200f}, {0x2028, 0x202e}, {0x2060, 0x206f}, {0xd800, 0xf8ff}, {0xfdd0, 0xfdef},
		{0xfeff, 0xfeff}, {0xfff0, 0xfffb}, {0xfffe, 0xffff}, {0x110bd, 0x110bd}, {0x110cd, 0x110cd}, {0x13430, 0x1343f},
		{0x1bca0, 0x1bca3}, {0x1d173, 0x1d17a}, {0xe0000, 0xe0fff}, {0xf0000, 0x10ffff}};
	for (auto [a, b] : format)
		if (c >= a && c <= b) return false;
	return (c & 0xfffe) != 0xfffe;
}

} // namespace

std::string go_quote(std::string_view s)
{
	static const char hex[] = "0123456789abcdef";
	std::string out = "\"";
	for (std::size_t i = 0; i < s.size();) {
		unsigned char b = static_cast<unsigned char>(s[i]);
		std::u32string one = go::runes(s.substr(i, std::min<std::size_t>(4, s.size() - i)));
		char32_t c = one.empty() ? 0xfffd : one[0];
		std::size_t n = c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
		// An invalid byte decodes as U+FFFD of width one; a real U+FFFD is three bytes.
		if (c == 0xfffd && s.substr(i, 3) != "\xef\xbf\xbd") n = 1;
		if (c == 0xfffd && n == 1 && b >= 0x80) {
			out += "\\x";
			out += hex[b >> 4];
			out += hex[b & 15];
		} else if (c == '"' || c == '\\') {
			out += '\\';
			out += static_cast<char>(c);
		} else if (c >= 0x20 && c < 0x7f) {
			out += static_cast<char>(c);
		} else if (c == '\a') out += "\\a";
		else if (c == '\b') out += "\\b";
		else if (c == '\f') out += "\\f";
		else if (c == '\n') out += "\\n";
		else if (c == '\r') out += "\\r";
		else if (c == '\t') out += "\\t";
		else if (c == '\v') out += "\\v";
		else if (c < 0x20 || c == 0x7f) {
			out += "\\x";
			out += hex[c >> 4];
			out += hex[c & 15];
		} else if (!printable(c)) {
			char buf[16];
			std::snprintf(buf, sizeof buf, c > 0xffff ? "\\U%08x" : "\\u%04x", static_cast<unsigned>(c));
			out += buf;
		} else {
			out.append(s.substr(i, n));
		}
		i += n;
	}
	return out + "\"";
}

// --- Policies ---

Error file_image_budget(int fd, std::int64_t &budget)
{
	struct statfs st{};
	if (::fstatfs(fd, &st) != 0) return Error::of("read free space: " + go::errno_text(errno));
	std::uint64_t available = static_cast<std::uint64_t>(st.f_bavail) * static_cast<std::uint64_t>(st.f_bsize);
	if (available <= image_free_reserve) return Error::of(err_image_space, "space");
	budget = static_cast<std::int64_t>(std::min<std::uint64_t>(available - image_free_reserve, INT64_MAX - 1));
	return {};
}

namespace {

Error budget_of(int fd, std::int64_t &budget)
{
	return image_write_budget ? image_write_budget(fd, budget) : file_image_budget(fd, budget);
}

bool valid_image_name(const std::string &s)
{
	if (s.empty()) return false;
	for (char c : s)
		if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'))
			return false;
	return true;
}

// strconv.ParseInt(s, 10, 64).
bool parse_int(std::string_view s, std::int64_t &v)
{
	bool negative = false;
	if (!s.empty() && (s[0] == '+' || s[0] == '-')) {
		negative = s[0] == '-';
		s.remove_prefix(1);
	}
	if (s.empty()) return false;
	std::uint64_t m = 0;
	for (char c : s) {
		if (c < '0' || c > '9') return false;
		if (m > (UINT64_MAX - 9) / 10) return false;
		m = m * 10 + static_cast<std::uint64_t>(c - '0');
	}
	if (m > (negative ? 9223372036854775808ull : 9223372036854775807ull)) return false;
	v = negative ? static_cast<std::int64_t>(0 - m) : static_cast<std::int64_t>(m);
	return true;
}

} // namespace

Error validate_image_filename(const std::string &filename)
{
	if (filename.empty()) return Error::of("no filename");
	if (go::base(filename) != filename || filename.find("..") != std::string::npos) return Error::of("invalid filename");
	std::string lower = go::to_lower(filename);
	if (!lower.ends_with(".iso") && !lower.ends_with(".img")) return Error::of("only .iso or .img files allowed");
	if (!valid_image_name(filename)) return Error::of("invalid filename");
	return {};
}

Error validate_raw_image_size(std::int64_t size)
{
	if (size < 512 || size % 512 != 0) return Error::of("raw disk image must contain complete 512-byte sectors");
	return {};
}

namespace {

// ISO and UDF volume recognition sequences start at sector 16; pure UDF has no CD001 descriptor, NSR02/NSR03 after
// BEA01 tells it.
bool optical_image(int fd)
{
	bool udf = false;
	for (int i = 0; i < 32; i++) {
		char id[5];
		if (::pread(fd, id, 5, 0x8001 + i * 2048) != 5) return false;
		std::string_view v(id, 5);
		if (v == "CD001") return true;
		if (v == "BEA01") udf = true;
		else if (v == "NSR02" || v == "NSR03") return udf;
		else if (v != "BOOT2" && v != "CDW02") return false;
	}
	return false;
}

} // namespace

Error validate_uploaded_image(const std::string &path, const std::string &name)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return Error::path("open", path, errno);
	Error err;
	if (go::equal_fold(go::ext(name), ".img")) {
		struct stat st{};
		if (::fstat(fd, &st) != 0) err = Error::path("stat", path, errno);
		else err = validate_raw_image_size(st.st_size);
	} else if (!optical_image(fd)) {
		err = Error::of("file is not an ISO 9660 or UDF optical image");
	}
	::close(fd);
	return err;
}

bool image_transfer_temp(const std::string &name)
{
	for (const char *prefix : {".nanokvm-upload-", ".nanokvm-download-"}) {
		if (!name.starts_with(prefix)) continue;
		std::string digits = name.substr(std::strlen(prefix));
		if (digits.empty() || digits.find_first_not_of("0123456789") != std::string::npos) continue;
		std::int64_t v;
		if (digits.size() <= 19 && parse_int(digits, v) && v <= 4294967295ll) return true;
		// More digits than fit: ParseUint fails unless they are leading zeros of a 32-bit value.
		auto first = digits.find_first_not_of('0');
		std::string significant = first == std::string::npos ? "0" : digits.substr(first);
		if (significant.size() <= 10 && parse_int(significant, v) && v <= 4294967295ll) return true;
	}
	return false;
}

Error parse_sha256(const std::string &value_, std::optional<std::string> &sum)
{
	std::string value = go::trim_space(value_);
	sum.reset();
	if (value.empty()) return {};
	if (value.size() != 64) return Error::of("invalid sha256sum");
	std::string out;
	for (std::size_t i = 0; i < 64; i += 2) {
		auto h = [](char c) -> int {
			if (c >= '0' && c <= '9') return c - '0';
			if (c >= 'a' && c <= 'f') return c - 'a' + 10;
			if (c >= 'A' && c <= 'F') return c - 'A' + 10;
			return -1;
		};
		int a = h(value[i]), b = h(value[i + 1]);
		if (a < 0 || b < 0) return Error::of("invalid sha256sum");
		out += static_cast<char>(a << 4 | b);
	}
	sum = out;
	return {};
}

bool strong_etag(const std::string &s)
{
	return s.size() >= 2 && s.front() == '"' && s.back() == '"' && s.find_first_of("\r\n") == std::string::npos;
}

std::string image_resume_json(const ImageResume &job)
{
	std::string out = "{\"url\":" + gojson::quote(job.url) + ",\"path\":" + gojson::quote(job.path) + ",\"filename\":" +
	                  gojson::quote(job.filename);
	if (!job.sha256.empty()) out += ",\"sha256\":" + gojson::quote(job.sha256);
	return out + ",\"etag\":" + gojson::quote(job.etag) + ",\"total\":" + std::to_string(job.total) + "}\n";
}

Error save_image_resume(const std::string &path, const ImageResume &job)
{
	int fd;
	std::string temp;
	if (auto e = go::create_temp(go::dir(path), ".nanokvm-resume-*", fd, temp)) return e;
	std::string data = image_resume_json(job);
	Error err;
	if (::write(fd, data.data(), data.size()) != static_cast<ssize_t>(data.size())) err = Error::path("write", temp, errno);
	if (::close(fd) != 0 && !err) err = Error::path("close", temp, errno);
	if (!err) err = go::rename(temp, path);
	::unlink(temp.c_str());
	return err;
}

namespace {

struct ResumeJson {
	std::optional<std::string> url, path, filename, sha256, etag;
	std::optional<std::int64_t> total;
};

} // namespace

} // namespace nkos::storage

template <>
struct glz::meta<nkos::storage::ResumeJson> {
	using T = nkos::storage::ResumeJson;
	static constexpr auto value = object("url", &T::url, "path", &T::path, "filename", &T::filename, "sha256", &T::sha256,
	                                     "etag", &T::etag, "total", &T::total);
};

namespace nkos::storage {

Error load_image_resume(const std::string &path, ImageResume &job)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return Error::path("open", path, errno);
	std::string data;
	char buf[4096];
	while (data.size() < 16384) {
		ssize_t n = ::read(fd, buf, std::min<std::size_t>(sizeof buf, 16384 - data.size()));
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) break;
		data.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	ResumeJson j;
	auto a = data.find_first_not_of(" \t\r\n");
	// json.Decoder: nothing at all is EOF.
	if (a == std::string::npos) return Error::of("EOF");
	if (data[a] != '{') return Error::of("invalid saved download");
	// The encoder ends the object with a newline; what follows it is not read.
	auto end = data.rfind('}');
	if (end == std::string::npos || glz::read<glz::opts{.error_on_unknown_keys = false}>(j, data.substr(0, end + 1)))
		return Error::of("invalid saved download");
	job = {j.url.value_or(""), j.path.value_or(""), j.filename.value_or(""), j.sha256.value_or(""), j.etag.value_or(""),
	       j.total.value_or(0)};
	auto u = go::parse_url(job.url);
	if (!u || (u->scheme != "http" && u->scheme != "https") || u->host.empty()) return Error::of("invalid saved download URL");
	std::string base = go::base(job.path);
	if (go::dir(job.path) != image_directory || !base.starts_with(".nanokvm-download-") || !image_transfer_temp(base) ||
	    go::clean(job.path) != job.path)
		return Error::of("invalid saved partial image path");
	// A job saved by an earlier version follows the policy of a new download.
	if (validate_image_filename(job.filename)) return Error::of("invalid saved image filename");
	std::optional<std::string> sum;
	if (auto e = parse_sha256(job.sha256, sum)) return e;
	if (!strong_etag(job.etag) || job.total <= 0) return Error::of("missing saved image validator");
	return {};
}

namespace {

// fmt.Sscanf(value, "bytes %d-%d/%d", ...): three integers, leading spaces before each skipped.
bool scan_content_range(std::string_view v, std::int64_t &start, std::int64_t &end, std::int64_t &total)
{
	if (!v.starts_with("bytes")) return false;
	v.remove_prefix(5);
	auto number = [&](std::int64_t &out) {
		while (!v.empty() && (v[0] == ' ' || v[0] == '\t')) v.remove_prefix(1);
		std::size_t n = 0;
		if (n < v.size() && (v[n] == '+' || v[n] == '-')) n++;
		std::size_t digits_start = n;
		while (n < v.size() && v[n] >= '0' && v[n] <= '9') n++;
		if (n == digits_start) return false;
		if (!parse_int(v.substr(0, n), out)) return false;
		v.remove_prefix(n);
		return true;
	};
	auto literal = [&](char c) {
		if (v.empty() || v[0] != c) return false;
		v.remove_prefix(1);
		return true;
	};
	return number(start) && literal('-') && number(end) && literal('/') && number(total);
}

} // namespace

ImageHeaders parse_image_headers(std::string_view data)
{
	ImageHeaders meta;
	data = data.substr(0, 1 << 20);
	for (std::size_t at = 0; at < data.size();) {
		auto nl = data.find('\n', at);
		// bufio.Scanner: a line longer than 64 KiB stops the scan; a final line without a newline counts.
		std::size_t len = (nl == std::string_view::npos ? data.size() : nl) - at;
		if (len > 64 * 1024) break;
		std::string_view line = data.substr(at, len);
		if (!line.empty() && line.back() == '\r') line.remove_suffix(1);
		at = nl == std::string_view::npos ? data.size() : nl + 1;
		if (line.starts_with("HTTP/")) {
			meta = {};
			auto f = go::fields(line);
			if (f.size() > 1) {
				std::int64_t v = 0;
				meta.status = parse_int(f[1], v) && v >= INT_MIN && v <= INT_MAX ? static_cast<int>(v) : 0;
			}
			continue;
		}
		auto colon = line.find(':');
		if (colon == std::string_view::npos) continue;
		std::string key = go::to_lower(line.substr(0, colon));
		std::string value = go::trim_space(line.substr(colon + 1));
		if (key == "etag") {
			meta.etag = value;
		} else if (key == "content-length") {
			std::int64_t n;
			if (parse_int(value, n) && n >= 0) meta.length = n;
		} else if (key == "content-range") {
			std::int64_t s, e, t;
			if (scan_content_range(value, s, e, t) && s >= 0 && e >= s && t > e) {
				meta.start = s;
				meta.total = t;
			}
		}
	}
	return meta;
}

ImageHeaders read_image_headers(const std::string &path)
{
	std::string data;
	if (go::read_file(path, data)) return {};
	return parse_image_headers(data);
}

std::vector<std::string> curl_image_args(const std::string &url, const std::string &headers, bool prefer_chacha)
{
	std::vector<std::string> args{"--disable", "--silent", "--show-error", "--fail", "--location", "--max-redirs", "10",
	                              "--connect-timeout", "10", "--speed-limit", "1", "--speed-time", "60",
	                              "--proto", "=http,https", "--proto-redir", "=http,https", "--tlsv1.2", "--dump-header", headers};
	if (go::to_lower(url).starts_with("https:")) {
		args.push_back("--proto-redir");
		args.push_back("=https");
	}
	if (prefer_chacha) {
		for (const char *a : {"--tls13-ciphers", "TLS_CHACHA20_POLY1305_SHA256", "--ciphers",
		                      "ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-CHACHA20-POLY1305"})
			args.push_back(a);
	}
	args.push_back("--url");
	args.push_back(url);
	return args;
}

bool curl_lost_connection(int code)
{
	switch (code) {
	case 6: case 7: case 18: case 28: case 35: case 52: case 56: case 92:
		return true;
	default:
		return false;
	}
}

std::string TransferProgress::percentage() const
{
	if (total <= 0) return {};
	double percent = static_cast<double>(bytes) / static_cast<double>(total) * 100;
	percent = std::clamp(percent, 0.0, 100.0);
	return std::format("{:.2f}%", percent);
}

TransferMeter::TransferMeter(std::int64_t offset, SteadyClock::time_point now) : previous_bytes_(offset), previous_time_(now) {}

TransferProgress TransferMeter::sample(std::int64_t bytes, std::int64_t total, SteadyClock::time_point now)
{
	std::lock_guard lock(mu_);
	double speed = 0;
	double elapsed = std::chrono::duration<double>(now - previous_time_).count();
	if (elapsed > 0 && bytes >= previous_bytes_) speed = static_cast<double>(bytes - previous_bytes_) / elapsed;
	previous_bytes_ = bytes;
	previous_time_ = now;
	return {bytes, total < 0 ? 0 : total, speed};
}

// --- The service ---

namespace {

const char *err_in_progress = "download in progress";
const char *err_sha_mismatch = "sha256 mismatch";

struct DownloadService {
	std::mutex mu;
	std::shared_ptr<Context> cancel;
	std::shared_ptr<Event> done;
	std::string status = "idle", file, percentage;
	TransferProgress progress;
};
DownloadService &downloads()
{
	static DownloadService s;
	return s;
}

std::shared_ptr<Event> begin_download(const std::string &file, std::shared_ptr<Context> cancel, Error &err)
{
	auto &s = downloads();
	std::lock_guard lock(s.mu);
	if (s.done) {
		err = Error::of(err_in_progress);
		return nullptr;
	}
	int fd = ::open(transfer_sentinel_path.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0644);
	if (fd < 0) {
		err = errno == EEXIST ? Error::of(err_in_progress)
		                      : Error::of("acquire transfer lock failed: " + go::path_error("open", transfer_sentinel_path, errno));
		return nullptr;
	}
	if (::close(fd) != 0) {
		::unlink(transfer_sentinel_path.c_str());
		err = Error::of("close transfer lock failed: " + go::path_error("close", transfer_sentinel_path, errno));
		return nullptr;
	}
	auto done = std::make_shared<Event>();
	s.cancel = std::move(cancel);
	s.done = done;
	s.status = "in_progress";
	s.file = file;
	s.percentage.clear();
	s.progress = {};
	return done;
}

void set_download_file(const std::shared_ptr<Event> &done, const std::string &file)
{
	auto &s = downloads();
	std::lock_guard lock(s.mu);
	if (s.done == done) s.file = file;
}

void set_download_progress(const std::shared_ptr<Event> &done, const TransferProgress &p)
{
	auto &s = downloads();
	std::lock_guard lock(s.mu);
	if (s.done == done) {
		s.percentage = p.percentage();
		s.progress = p;
	}
}

void finish_download(const std::shared_ptr<Event> &done, const std::string &status)
{
	auto &s = downloads();
	{
		std::lock_guard lock(s.mu);
		if (s.done != done) return;
		s.cancel = nullptr;
		s.done = nullptr;
		s.status = status;
		s.file.clear();
		s.percentage.clear();
		s.progress = {};
		::unlink(transfer_sentinel_path.c_str());
	}
	done->fire();
}

std::string status_json(const std::string &status, const std::string &file, const std::string &percentage,
                        const TransferProgress &p = {})
{
	return "{\"downloadedBytes\":" + std::to_string(p.bytes) + ",\"totalBytes\":" + std::to_string(p.total) +
	       ",\"bytesPerSecond\":" + go::json_float(p.bytes_per_second) + ",\"status\":" + gojson::quote(status) +
	       ",\"file\":" + gojson::quote(file) + ",\"percentage\":" + gojson::quote(percentage) + "}";
}

Error install(const std::function<bool()> &canceled, const std::string &temp, const std::string &filename)
{
	if (auto e = validate_image_filename(filename)) return e;
	// utils.JoinWithin: the name stays strictly inside the directory.
	std::string dir = go::clean(image_directory), dest = go::clean(dir + "/" + filename);
	if (!dest.starts_with(dir + "/")) return Error::of("path is outside its directory");
	return install_image(canceled, temp, dest);
}

// The curl child of one attempt: its standard output is the image (or a pipe when the bytes are hashed here).
struct Child {
	pid_t pid = -1;
	int pipe = -1;
};

Child spawn_curl(const std::vector<std::string> &args, int out_fd, bool piped)
{
	std::vector<std::string> argv{image_curl_path};
	argv.insert(argv.end(), args.begin(), args.end());
	std::vector<char *> raw;
	for (auto &a : argv) raw.push_back(a.data());
	raw.push_back(nullptr);
	int fds[2] = {-1, -1};
	if (piped && ::pipe2(fds, O_CLOEXEC) != 0) return {};
	int null = ::open("/dev/null", O_RDWR | O_CLOEXEC);
	pid_t parent = ::getpid();
	pid_t pid = ::fork();
	if (pid == 0) {
		process::cloexec_inherited();
		int out = piped ? fds[1] : out_fd;
		// Kill the transfer when the server goes; nice 10 keeps the card writes behind the interactive work.
		::prctl(PR_SET_PDEATHSIG, SIGKILL);
		if (::getppid() != parent) ::_exit(127);
		::setpriority(PRIO_PROCESS, 0, 10);
		if (::dup2(null, 0) < 0 || ::dup2(out, 1) < 0 || ::dup2(null, 2) < 0) ::_exit(127);
		::execv(raw[0], raw.data());
		::_exit(127);
	}
	if (null >= 0) ::close(null);
	if (piped) ::close(fds[1]);
	if (pid < 0) {
		if (piped) ::close(fds[0]);
		return {};
	}
	return {pid, piped ? fds[0] : -1};
}

// A download into dst from its current offset; resume (with the checkpoint) lets a lost connection continue.
Error curl_transfer(const std::shared_ptr<Context> &ctx, const std::string &url, int dst,
                    const std::optional<std::string> &expected, std::function<void(const TransferProgress &)> progress,
                    ImageResume *resume)
{
	off_t start = ::lseek(dst, 0, SEEK_CUR);
	if (start < 0) return Error{go::errno_text(errno), errno, {}};
	std::int64_t offset = start;
	std::mutex resume_mu;
	if (offset > 0 && (!resume || !strong_etag(resume->etag) || resume->total <= offset))
		return Error::of("partial image has no valid resume validator");
	int hfd;
	std::string header_path;
	if (auto e = go::create_temp("", "nanokvm-image-headers-*", hfd, header_path)) return e;
	::close(hfd);
	struct Remove {
		std::string path;
		~Remove() { ::unlink(path.c_str()); }
	} remove_headers{header_path};
	Sha256 hasher;
	if (expected && offset > 0) {
		std::vector<char> buf(256 << 10);
		for (std::int64_t at = 0; at < offset;) {
			if (ctx->canceled()) return Error::of("context canceled", "canceled");
			ssize_t n = ::pread(dst, buf.data(), static_cast<std::size_t>(std::min<std::int64_t>(offset - at, static_cast<std::int64_t>(buf.size()))), at);
			if (n < 0 && errno == EINTR) continue;
			if (n <= 0) return Error::of(n < 0 ? go::errno_text(errno) : "unexpected EOF");
			hasher.update(std::string_view(buf.data(), static_cast<std::size_t>(n)));
			at += n;
		}
		if (ctx->canceled()) return Error::of("context canceled", "canceled");
	}
	TransferMeter meter(offset);
	bool prefer_chacha = true;
	int idle = 0;
	for (;;) {
		// Free space changes while a transfer waits: the budget is for the rest.
		std::int64_t budget = 0;
		if (auto e = budget_of(dst, budget)) return e;
		if (offset > 0 && resume->total - offset > budget) return Error::of(err_image_space, "space");
		if (::truncate(header_path.c_str(), 0) != 0) return Error::path("truncate", header_path, errno);
		auto args = curl_image_args(url, header_path, prefer_chacha);
		args.push_back("--max-filesize");
		args.push_back(std::to_string(budget));
		if (offset > 0) {
			args.push_back("--continue-at");
			args.push_back(std::to_string(offset));
			args.push_back("--header");
			args.push_back("If-Range: " + resume->etag);
		}
		// One watcher per attempt: it reads the headers and fills resume, which the loop touches only between
		// attempts.
		Event stop_watch;
		std::int64_t attempt_offset = offset;
		std::thread watcher([&] {
			while (!stop_watch.wait_until(SteadyClock::now() + std::chrono::seconds(1))) {
				ImageHeaders meta = read_image_headers(header_path);
				std::int64_t total = meta.length;
				{
					std::lock_guard lock(resume_mu);
					if (attempt_offset > 0) {
						total = resume->total;
					} else if (resume && meta.status == 200 && strong_etag(meta.etag) && meta.length > 0 && resume->etag.empty()) {
						resume->etag = meta.etag;
						resume->total = meta.length;
						(void)save_image_resume(image_resume_path, *resume);
					}
				}
				struct stat st{};
				if (::fstat(dst, &st) == 0 && progress) progress(meter.sample(st.st_size, total, SteadyClock::now()));
			}
		});
		Child child = spawn_curl(args, dst, expected.has_value());
		Error copy_error;
		int status = 0;
		bool started = child.pid > 0;
		if (started) {
			// A cancel kills curl; only before it is reaped, when its number cannot belong to another process.
			auto reaped = std::make_shared<std::pair<std::mutex, bool>>();
			std::uint64_t kill_hook = ctx->on_cancel([pid = child.pid, reaped] {
				std::lock_guard lock(reaped->first);
				if (!reaped->second) ::kill(pid, SIGKILL);
			});
			if (child.pipe >= 0) {
				std::vector<char> buf(256 << 10);
				for (;;) {
					ssize_t n = ::read(child.pipe, buf.data(), buf.size());
					if (n < 0 && errno == EINTR) continue;
					if (n <= 0) break;
					std::string_view chunk(buf.data(), static_cast<std::size_t>(n));
					bool ok = true;
					for (std::size_t off = 0; off < chunk.size();) {
						ssize_t w = ::write(dst, chunk.data() + off, chunk.size() - off);
						if (w < 0 && errno == EINTR) continue;
						if (w <= 0) {
							copy_error = Error{go::errno_text(w < 0 ? errno : EIO), w < 0 ? errno : EIO, {}};
							ok = false;
							break;
						}
						off += static_cast<std::size_t>(w);
					}
					if (!ok) break;
					hasher.update(chunk);
				}
				::close(child.pipe);
			}
			siginfo_t info{};
			while (::waitid(P_PID, static_cast<id_t>(child.pid), &info, WEXITED | WNOWAIT) < 0 && errno == EINTR) {
			}
			{
				std::lock_guard lock(reaped->first);
				reaped->second = true;
			}
			while (::waitpid(child.pid, &status, 0) < 0 && errno == EINTR) {
			}
			ctx->stop(kill_hook);
		}
		stop_watch.fire();
		watcher.join();
		if (ctx->canceled()) return Error::of("context canceled", "canceled");
		ImageHeaders meta = read_image_headers(header_path);
		// What a resumed answer must be before its bytes belong to the image.
		bool continues = offset == 0 || (meta.status == 206 && meta.start == offset && meta.total == resume->total &&
		                                 meta.etag == resume->etag);
		struct stat st{};
		bool stat_ok = ::fstat(dst, &st) == 0;
		int stat_errno = stat_ok ? 0 : errno;
		bool exited = started && WIFEXITED(status);
		int exit_code = exited ? WEXITSTATUS(status) : -1;
		bool success = started && exited && exit_code == 0 && !copy_error;
		if (success) {
			if (offset == 0 && meta.status != 200) return Error::of("download response status " + std::to_string(meta.status));
			if (!continues) return Error::of("download resume response does not match partial image");
			if (!stat_ok) return Error{go::errno_text(stat_errno), stat_errno, {}};
			if (offset > 0 && st.st_size != resume->total) return Error::of("incomplete resumed image");
			if (expected && hasher.digest() != *expected) return Error::of(err_sha_mismatch, "sha256");
			return {};
		}
		std::string why;
		if (!started) why = "fork/exec " + image_curl_path + ": " + go::errno_text(ENOENT);
		else if (!exited || exit_code != 0) why = go::exit_text(exit_code, exited ? 0 : WTERMSIG(status));
		else why = "write: " + copy_error.text;
		Error failed = Error::of("image transfer failed: " + why);
		if (!started || !(exited && exit_code != 0) || !stat_ok) return failed;
		if (exit_code == 63) return Error::of(err_image_space, "space");
		std::int64_t size = st.st_size;
		// A server may offer only AES: a negotiation failure before any body byte is retried with OpenSSL's
		// verified default suites. Never a certificate failure, never a restarted answer onto a partial image.
		if (prefer_chacha && exit_code == 35 && size == offset) {
			prefer_chacha = false;
			continue;
		}
		// A lost connection continues from what is on the card when the server named the version it was sending.
		if (!curl_lost_connection(exit_code) || size == 0 || !resume) return failed;
		if (size > offset) {
			if (!continues) return Error::of("download resume response does not match partial image");
			if (offset == 0) {
				if (meta.status != 200 || !strong_etag(meta.etag) || meta.length <= size) return failed;
				std::lock_guard lock(resume_mu);
				resume->etag = meta.etag;
				resume->total = meta.length;
				(void)save_image_resume(image_resume_path, *resume);
			}
			if (size >= resume->total) return failed;
			offset = size;
			idle = 0;
		} else if (++idle >= image_retry_limit) {
			return failed;
		}
		auto delay = std::min(image_retry_delay * (1 << idle), image_retry_delay * 30);
		auto woken = std::make_shared<Event>();
		std::uint64_t wake = ctx->on_cancel([woken] { woken->fire(); });
		woken->wait_until(SteadyClock::now() + delay);
		ctx->stop(wake);
		if (ctx->canceled()) return Error::of("context canceled", "canceled");
	}
}

void complete_image_download(const std::shared_ptr<Event> &done, const Error &err)
{
	std::string status = "success";
	if (err.tag == "canceled") status = "idle";
	else if (err.tag == "sha256") status = "checksum_failed";
	else if (err) {
		status = "failed";
		log::error("Failed to download image: {}", err.text);
	}
	finish_download(done, status);
}

void download_remote_image(const std::shared_ptr<Context> &ctx, const std::shared_ptr<Event> &done, const std::string &url,
                           const std::optional<std::string> &expected, const std::string &filename)
{
	int fd;
	std::string temp;
	if (auto e = go::create_temp(image_directory, ".nanokvm-download-*", fd, temp))
		return complete_image_download(done, Error::of("create temporary image failed: " + e.text));
	ImageResume job{url, temp, filename, expected ? Sha256::hex(*expected) : "", "", 0};
	// A checkpoint is optional for downloading, but required for resumption.
	(void)save_image_resume(image_resume_path, job);
	Error err = curl_transfer(ctx, url, fd, expected, [done](const TransferProgress &p) { set_download_progress(done, p); }, &job);
	::unlink(image_resume_path.c_str());
	if (!err && !ctx->canceled() && ::fsync(fd) != 0) err = Error::path("sync", temp, errno);
	int close_errno = ::close(fd) == 0 ? 0 : errno;
	if (ctx->canceled()) err = Error::of("context canceled", "canceled");
	else if (!err && close_errno) err = Error::of("close downloaded image failed: " + go::path_error("close", temp, close_errno));
	if (!err) {
		err = install([ctx] { return ctx->canceled(); }, temp, filename);
		if (err && ctx->canceled()) err = Error::of("context canceled", "canceled");
		else if (err) err = Error::of("install downloaded image failed: " + err.text, err.tag);
	}
	::unlink(temp.c_str());
	if (err.tag == "canceled") log::debug("Image download canceled");
	complete_image_download(done, err);
}

bool json_request(const http::Request &req) { return json_content_type(req.header("content-type")); }

// --- POST /api/download/file ---

class ImageUpload : public http::BodySink, public std::enable_shared_from_this<ImageUpload> {
public:
	ImageUpload(std::uint64_t id, std::vector<std::pair<std::string, std::string>> headers, std::shared_ptr<Event> done,
	            std::optional<std::string> expected, std::int64_t content_length, std::int64_t request_limit)
		: id_(id), headers_(std::move(headers)), done_(std::move(done)), expected_(std::move(expected)),
		  content_length_(content_length), request_limit_(request_limit)
	{
	}

	bool init(std::string_view content_type)
	{
		std::weak_ptr<ImageUpload> weak = weak_from_this();
		auto stream = http::MultipartStream::make(content_type, {
			[weak](const std::string &name, const std::optional<std::string> &filename) {
				auto self = weak.lock();
				return self && self->begin_part(name, filename);
			},
			[weak](std::string_view data) {
				auto self = weak.lock();
				return self && self->part_data(data);
			},
			[weak] {
				auto self = weak.lock();
				return self && self->end_part();
			}});
		if (!stream) return false;
		stream_ = std::move(*stream);
		return true;
	}

	void data(std::string_view chunk) override
	{
		if (answered_ || file_done_) return;
		body_ += static_cast<std::int64_t>(chunk.size());
		if (body_ > request_limit_) {
			// http.MaxBytesReader: within the file's part a copy error, elsewhere a part that cannot be read.
			std::size_t keep = chunk.size() - static_cast<std::size_t>(body_ - request_limit_);
			if (keep) feed(chunk.substr(0, keep));
			if (answered_ || file_done_) return;
			return fail(in_file_ ? "http: request body too large" : "failed to read part");
		}
		feed(chunk);
	}

	void end(http::Request &) override
	{
		if (answered_ || file_done_) return;
		if (!stream_.finish()) return fail(in_file_ ? "unexpected EOF" : "failed to read part");
		fail("file is required");
	}

	void aborted() override
	{
		canceled_ = true;
		if (answered_) return;
		answered_ = true;
		stop_ticker();
		if (writer_) writer_->abort();
		auto self = shared_from_this();
		loop::blocking([self] {
			self->writer_.reset();
			if (!self->temp_.empty()) ::unlink(self->temp_.c_str());
			finish_download(self->done_, "idle");
		});
	}

private:
	void feed(std::string_view chunk)
	{
		if (!stream_.feed(chunk) && !answered_ && !file_done_) fail(in_file_ ? "unexpected EOF" : "failed to read part");
	}

	bool begin_part(const std::string &name, const std::optional<std::string> &filename)
	{
		if (answered_ || file_done_) return false;
		if (name != "file") {
			skipping_ = true;
			return true;
		}
		skipping_ = false;
		std::string file = filename.value_or("");
		if (auto e = validate_image_filename(file)) {
			fail(e.text);
			return false;
		}
		filename_ = file;
		set_download_file(done_, file);
		int fd;
		if (go::create_temp(image_directory, ".nanokvm-upload-*", fd, temp_)) {
			log::error("cannot create temporary file");
			temp_.clear();
			fail("cannot create temporary file");
			return false;
		}
		Error budget_error = budget_of(fd, budget_);
		::close(fd);
		if (budget_error) {
			fail(budget_error.text);
			return false;
		}
		std::string error;
		writer_ = http::FileWriter::open(temp_, 0, 0600, error);
		if (!writer_) {
			fail("cannot create temporary file");
			return false;
		}
		writer_->feeds(id_);
		in_file_ = true;
		if (expected_) hasher_ = std::make_unique<Sha256>();
		meter_ = std::make_unique<TransferMeter>(0);
		start_ticker();
		return true;
	}

	bool part_data(std::string_view data)
	{
		if (answered_) return false;
		if (skipping_ || !in_file_) return true;
		// The budget writer: what fits is written, then the space error.
		bool over = static_cast<std::int64_t>(data.size()) > budget_ - written_;
		std::string_view fits = over ? data.substr(0, static_cast<std::size_t>(budget_ - written_)) : data;
		if (!fits.empty()) writer_->write(fits);
		written_ += static_cast<std::int64_t>(fits.size());
		if (over) {
			fail(err_image_space);
			return false;
		}
		if (hasher_) hasher_->update(data);
		return true;
	}

	bool end_part()
	{
		if (answered_) return false;
		if (skipping_ || !in_file_) return true;
		in_file_ = false;
		file_done_ = true;
		stop_ticker();
		// The bytes are on the card (FileWriter syncs) before the rename publishes the name.
		bool mismatch = hasher_ && hasher_->digest() != *expected_;
		auto self = shared_from_this();
		if (mismatch) {
			writer_->abort();
			answered_ = true;
			loop::blocking([self] { self->answer("checksum_failed", Error::of(err_sha_mismatch)); });
			return true;
		}
		writer_->finish([self](const std::string &error) {
			if (self->canceled_) return;
			Error err;
			if (!error.empty()) err = Error::of("write " + self->temp_ + ": " + go::to_lower(error));
			self->answered_ = true;
			loop::blocking([self, err] {
				if (err) return self->answer("failed", err);
				if (auto e = validate_uploaded_image(self->temp_, self->filename_)) return self->answer("failed", e);
				// Cancelled (also while waiting for the image lock): nothing is published.
				if (auto e = install([self] { return self->canceled_.load(); }, self->temp_, self->filename_)) {
					if (self->canceled_) return self->answer("idle", Error{});
					return self->answer("failed", e);
				}
				self->answer("idle", Error{}, true);
			});
		});
		return true;
	}

	void fail(const std::string &message)
	{
		if (answered_) return;
		answered_ = true;
		stop_ticker();
		if (writer_) writer_->abort();
		auto self = shared_from_this();
		loop::blocking([self, message] { self->answer("failed", Error::of(message)); });
	}

	// Blocking thread: the temporary file goes, the status is final, the answer leaves.
	void answer(const std::string &status, const Error &err, bool ok = false)
	{
		writer_.reset();
		if (!temp_.empty()) ::unlink(temp_.c_str());
		finish_download(done_, canceled_ ? "idle" : status);
		if (canceled_) return;
		http::Response out;
		out.headers = headers_;
		if (ok) api::send(out, 0, "success", status_json("idle", "", ""));
		else api::err(out, -1, err.text.empty() ? "write failed" : err.text);
		http::complete(id_, std::move(out));
	}

	void start_ticker()
	{
		std::weak_ptr<ImageUpload> weak = weak_from_this();
		ticking_ = true;
		loop::after(std::chrono::seconds(1), [weak] {
			auto self = weak.lock();
			if (!self || !self->ticking_) return;
			set_download_progress(self->done_, self->meter_->sample(self->written_, self->content_length_, SteadyClock::now()));
			self->start_ticker();
		});
	}
	void stop_ticker() { ticking_ = false; }

	std::uint64_t id_;
	std::vector<std::pair<std::string, std::string>> headers_;
	std::shared_ptr<Event> done_;
	std::optional<std::string> expected_;
	std::int64_t content_length_, request_limit_, body_ = 0, budget_ = 0, written_ = 0;
	http::MultipartStream stream_ = *http::MultipartStream::make("multipart/form-data; boundary=x", {});
	std::string filename_, temp_;
	std::shared_ptr<http::FileWriter> writer_;
	std::unique_ptr<Sha256> hasher_;
	std::unique_ptr<TransferMeter> meter_;
	bool answered_ = false, in_file_ = false, skipping_ = false, file_done_ = false, ticking_ = false;
	std::atomic<bool> canceled_{false};
};

std::int64_t content_length(const http::Request &req)
{
	std::string v = req.header("content-length");
	std::int64_t n;
	if (v.empty() || !parse_int(v, n) || n < 0) return -1;
	return n;
}

std::shared_ptr<ImageUpload> begin_image_upload(http::Request &req, http::Response &res)
{
	std::optional<std::string> expected;
	if (auto e = parse_sha256(req.header("X-SHA256-Sum"), expected)) {
		api::err(res, -1, e.text);
		return nullptr;
	}
	Error err;
	auto done = begin_download("", nullptr, err);
	if (!done) {
		api::err(res, -1, err.text);
		return nullptr;
	}
	auto refuse = [&](const std::string &message) -> std::shared_ptr<ImageUpload> {
		finish_download(done, "failed");
		api::err(res, -1, message);
		return nullptr;
	};
	int dir = ::open(image_directory.c_str(), O_RDONLY | O_CLOEXEC);
	if (dir < 0) return refuse(go::path_error("open", image_directory, errno));
	std::int64_t budget = 0;
	err = budget_of(dir, budget);
	::close(dir);
	if (err) return refuse(err.text);
	// Room for the multipart headers, not for unlimited other fields.
	std::int64_t limit = budget;
	if (limit < INT64_MAX - (64 << 10)) limit += 64 << 10;
	std::int64_t length = content_length(req);
	if (length > limit) return refuse(err_image_space);
	auto upload = std::make_shared<ImageUpload>(req.id, res.headers, done, expected, length, limit);
	if (!upload->init(req.header("content-type"))) {
		log::error("invalid multipart data");
		return refuse("invalid multipart data");
	}
	return upload;
}

} // namespace

void start_downloads()
{
	// The old process is gone; a checked partial download continues.
	::unlink(transfer_sentinel_path.c_str());
	// cleanupImageTransfers: only os.CreateTemp's exact transfer names in /data's root, never other dotfiles; the
	// partial file of a checkpoint that can continue stays.
	std::string keep;
	ImageResume job;
	if (!load_image_resume(image_resume_path, job)) {
		struct stat st{};
		if (::lstat(job.path.c_str(), &st) == 0 && S_ISREG(st.st_mode) && st.st_size > 0 && st.st_size < job.total) keep = job.path;
	}
	if (DIR *d = ::opendir(image_directory.c_str())) {
		std::vector<std::string> names;
		while (dirent *e = ::readdir(d))
			if (image_transfer_temp(e->d_name)) names.push_back(e->d_name);
		::closedir(d);
		for (const auto &name : names) {
			std::string path = go::join(image_directory, name);
			struct stat st{};
			if (path == keep || ::lstat(path.c_str(), &st) != 0 || !S_ISREG(st.st_mode)) continue;
			if (auto e = remove_image(path)) log::warn("Cannot remove interrupted image transfer {}: {}", name, e.text);
		}
	}
	// resumeImageDownload
	Error err = load_image_resume(image_resume_path, job);
	if (err.sys == ENOENT) return;
	if (err) {
		log::warn("Saved image download cannot be resumed; partial file retained");
		::unlink(image_resume_path.c_str());
		return;
	}
	// A partial file replaced by a symbolic link is never followed.
	int fd = ::open(job.path.c_str(), O_RDWR | O_NOFOLLOW | O_CLOEXEC);
	if (fd < 0) {
		log::warn("Saved partial image is unavailable");
		return;
	}
	struct stat st{};
	if (::fstat(fd, &st) != 0 || !S_ISREG(st.st_mode) || st.st_size <= 0 || st.st_size >= job.total) {
		::close(fd);
		log::warn("Saved partial image has an invalid size; file retained");
		return;
	}
	if (::lseek(fd, 0, SEEK_END) < 0) {
		::close(fd);
		return;
	}
	auto ctx = Context::make();
	auto done = begin_download(job.url, ctx, err);
	if (!done) {
		::close(fd);
		return;
	}
	set_download_progress(done, {st.st_size, job.total, 0});
	std::thread([ctx, done, job, fd]() mutable {
		std::optional<std::string> expected;
		(void)parse_sha256(job.sha256, expected);
		Error err = curl_transfer(ctx, job.url, fd, expected, [done](const TransferProgress &p) { set_download_progress(done, p); }, &job);
		if (!err && ::fsync(fd) != 0) err = Error::path("sync", job.path, errno);
		int close_errno = ::close(fd) == 0 ? 0 : errno;
		if (!err && close_errno) err = Error::path("close", job.path, close_errno);
		if (!err) err = install([ctx] { return ctx->canceled(); }, job.path, job.filename);
		// A finished attempt has no checkpoint any more; a crash still keeps it.
		::unlink(job.path.c_str());
		::unlink(image_resume_path.c_str());
		complete_image_download(done, err);
	}).detach();
}

namespace {

// CheckImageUpload is advisory: the transfer checks its lock, space and destination again.
void check_upload(http::Request &req, http::Response &res)
{
	res.set_header("Cache-Control", "no-store");
	std::string name = req.query_value("name");
	if (auto e = validate_image_filename(name)) return api::err(res, -1, e.text);
	std::int64_t size = 0;
	if (!parse_int(req.query_value("size"), size) || size <= 0) return api::err(res, -1, "invalid image size");
	if (go::equal_fold(go::ext(name), ".img"))
		if (auto e = validate_raw_image_size(size)) return api::err(res, -1, e.text);
	http::defer(req, res, [name, size](http::Response &out) {
		bool busy;
		{
			auto &s = downloads();
			std::lock_guard lock(s.mu);
			busy = s.done != nullptr;
		}
		struct stat st{};
		int lock_errno = ::lstat(transfer_sentinel_path.c_str(), &st) == 0 ? 0 : errno;
		if (busy || lock_errno == 0) return api::err(out, -1, err_in_progress);
		if (lock_errno != ENOENT) return api::err(out, -1, go::path_error("lstat", transfer_sentinel_path, lock_errno));
		if (::access(image_directory.c_str(), W_OK) != 0) return api::err(out, -1, "image storage is not writable");
		int dir = ::open(image_directory.c_str(), O_RDONLY | O_CLOEXEC);
		if (dir < 0) return api::err(out, -1, go::path_error("open", image_directory, errno));
		std::int64_t budget = 0;
		Error e = budget_of(dir, budget);
		::close(dir);
		if (e) return api::err(out, -1, e.text);
		if (size > budget) return api::err(out, -1, err_image_space);
		if (auto u = check_image_unused(go::join(image_directory, name))) return api::err(out, -1, u.text);
		api::ok(out);
	});
}

} // namespace

void add_download_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("POST", "/api/download/image", auth::guarded([](http::Request &req, http::Response &res) {
		auto params = api::Params::bind(req);
		bool json = json_request(req), ok = true, ok2 = true;
		std::optional<std::string> file, sha;
		if (params) {
			file = params->string(json ? "file" : "File", ok);
			sha = params->string(json ? "sha256sum" : "SHA256Sum", ok2);
		}
		if (!params || !ok || !ok2 || !file || file->empty()) return api::err(res, -1, "invalid arguments");
		std::optional<std::string> expected;
		if (auto e = parse_sha256(sha.value_or(""), expected)) return api::err(res, -1, e.text);
		auto u = go::parse_url(*file);
		if (!u || (u->scheme != "http" && u->scheme != "https") || u->host.empty() || u->path.empty())
			return api::err(res, -1, "invalid url");
		// The name lands in /data: only images the storage page lists.
		std::string filename = go::base(u->path);
		if (auto e = validate_image_filename(filename)) return api::err(res, -1, "invalid url: " + e.text);
		auto ctx = Context::make();
		Error err;
		auto done = begin_download(*file, ctx, err);
		if (!done) return api::err(res, -1, err.text);
		std::thread([ctx, done, url = *file, expected, filename] {
			download_remote_image(ctx, done, url, expected, filename);
		}).detach();
		api::send(res, 0, "success", status_json("in_progress", *file, ""));
	}, admin));
	router.add("POST", "/api/download/image/cancel", auth::guarded([](http::Request &req, http::Response &res) {
		std::shared_ptr<Context> cancel;
		std::shared_ptr<Event> done;
		{
			auto &s = downloads();
			std::lock_guard lock(s.mu);
			cancel = s.cancel;
			done = s.done;
		}
		if (!cancel || !done) return api::err(res, -1, "no cancellable download in progress");
		cancel->cancel();
		http::defer(req, res, [done](http::Response &out) {
			if (done->wait_until(SteadyClock::now() + std::chrono::seconds(10))) return api::ok(out);
			api::err(out, -1, "cancel download timed out");
		});
	}, admin));
	router.add("GET", "/api/download/image/status", auth::guarded([](http::Request &, http::Response &res) {
		auto &s = downloads();
		std::lock_guard lock(s.mu);
		api::send(res, 0, "success", status_json(s.status, s.file, s.percentage, s.progress));
	}, admin));
	router.add("GET", "/api/download/image/enabled", auth::guarded([](http::Request &, http::Response &res) {
		// access(2) answers EROFS on a read-only mount without writing: a probe file would mark the exFAT volume
		// dirty until unmount.
		bool enabled = ::access(image_directory.c_str(), W_OK) == 0;
		api::send(res, 0, "success", std::string("{\"enabled\":") + (enabled ? "true" : "false") + "}");
	}, admin));
	router.add("GET", "/api/download/file/check", auth::guarded(check_upload, admin));
	auto upload = [](http::Request &req, http::Response &res) -> std::shared_ptr<http::BodySink> {
		return begin_image_upload(req, res);
	};
	router.upload("POST", "/api/download/file", auth::guarded_upload(upload, admin));
	// A request without a body never reaches the upload route.
	router.add("POST", "/api/download/file", auth::guarded([](http::Request &req, http::Response &res) {
		auto sink = begin_image_upload(req, res);
		if (!sink) return;
		res.pending = true;
		if (!req.body.empty()) sink->data(req.body);
		sink->end(req);
	}, admin));
}

} // namespace nkos::storage
