// PicoClaw's installation (runtime_install.go, runtime_release.go): the latest official release's archive and
// checksum file from GitHub, the SHA-256 checked, the binary taken from the tar.gz and installed as
// /usr/bin/picoclaw; progress in the runtime status while it runs in a thread of its own.

#include "ai/files.hpp"
#include "ai/picoclaw_internal.hpp"
#include "common/http_client.hpp"
#include "common/log.hpp"

#include <cerrno>
#include <cstring>
#include <climits>
#include <filesystem>
#include <fstream>
#include <openssl/evp.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <zlib.h>

namespace nkos::ai::picoclaw {

namespace {

constexpr const char *archive_name = "picoclaw_Linux_riscv64.tar.gz";
constexpr auto install_timeout = std::chrono::minutes(12);
constexpr auto download_timeout = std::chrono::minutes(10);

std::string status_text(int code)
{
	switch (code) {
	case 200: return "200 OK";
	case 301: return "301 Moved Permanently";
	case 302: return "302 Found";
	case 307: return "307 Temporary Redirect";
	case 400: return "400 Bad Request";
	case 403: return "403 Forbidden";
	case 404: return "404 Not Found";
	case 429: return "429 Too Many Requests";
	case 500: return "500 Internal Server Error";
	case 502: return "502 Bad Gateway";
	case 503: return "503 Service Unavailable";
	default: return std::to_string(code);
	}
}

struct Release {
	std::string tag, archive_url, checksum_url;
};

bool select_release(const std::string &location, Release &r, std::string &error)
{
	const std::string prefix = "https://github.com/sipeed/picoclaw/releases/download/";
	if (location.rfind(prefix, 0) != 0 || location.find_first_of("?#@") != std::string::npos) {
		error = "unexpected PicoClaw release URL";
		return false;
	}
	std::string rest = location.substr(prefix.size());
	auto slash = rest.find('/');
	std::string tag = rest.substr(0, slash), asset = slash == std::string::npos ? "" : rest.substr(slash + 1);
	if (slash == std::string::npos || asset != archive_name || tag.size() < 2 || tag[0] != 'v' || tag.find_first_of("\\?#") != std::string::npos) {
		error = "invalid PicoClaw release asset";
		return false;
	}
	r.tag = tag;
	r.archive_url = location;
	r.checksum_url = prefix + tag + "/picoclaw_" + tag.substr(1) + "_checksums.txt";
	return true;
}

void set_progress(const std::string &stage, int progress)
{
	runtime_update_install([&](InstallState &s) {
		s.stage = stage;
		s.progress = std::clamp(progress, 0, 100);
	});
}

std::string sha256_file(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return {};
	EVP_MD_CTX *ctx = EVP_MD_CTX_new();
	EVP_DigestInit_ex(ctx, EVP_sha256(), nullptr);
	char buf[65536];
	while (in) {
		in.read(buf, sizeof buf);
		if (in.gcount() > 0) EVP_DigestUpdate(ctx, buf, static_cast<std::size_t>(in.gcount()));
	}
	unsigned char md[32];
	unsigned int len = 0;
	EVP_DigestFinal_ex(ctx, md, &len);
	EVP_MD_CTX_free(ctx);
	static const char hex[] = "0123456789abcdef";
	std::string out;
	for (unsigned i = 0; i < len; i++) {
		out += hex[md[i] >> 4];
		out += hex[md[i] & 15];
	}
	return out;
}

bool valid_digest(const std::string &d)
{
	return d.size() == 64 && d.find_first_not_of("0123456789abcdefABCDEF") == std::string::npos;
}

// The binary "picoclaw" from the tar.gz into dir; its path, or error.
std::string extract(const std::string &archive, const std::string &dir, std::string &error)
{
	gzFile gz = gzopen(archive.c_str(), "rb");
	if (!gz) {
		error = "failed to open archive: open " + archive + ": " + errno_text(errno);
		return {};
	}
	struct Close {
		gzFile gz;
		~Close() { gzclose(gz); }
	} close{gz};
	auto read = [&](char *buf, std::size_t n) {
		std::size_t done = 0;
		while (done < n) {
			int r = gzread(gz, buf + done, static_cast<unsigned>(n - done));
			if (r <= 0) return done;
			done += static_cast<std::size_t>(r);
		}
		return done;
	};
	std::string long_name;
	for (;;) {
		char header[512];
		std::size_t got = read(header, sizeof header);
		if (got == 0) break;
		if (got != sizeof header) {
			error = "failed to extract archive: unexpected EOF";
			return {};
		}
		bool zero = true;
		for (char c : header)
			if (c) zero = false;
		if (zero) break;
		std::string name(header, strnlen(header, 100));
		std::string prefix(header + 345, strnlen(header + 345, 155));
		if (!prefix.empty() && std::string(header + 257, 5) == "ustar") name = prefix + "/" + name;
		if (!long_name.empty()) {
			name = long_name;
			long_name.clear();
		}
		unsigned long long size = std::strtoull(std::string(header + 124, 12).c_str(), nullptr, 8);
		char type = header[156];
		unsigned long long padded = (size + 511) / 512 * 512;
		if (type == 'L') {
			if (size > PATH_MAX) {
				error = "failed to extract archive: file name too long";
				return {};
			}
			std::string data(padded, '\0');
			if (read(data.data(), padded) != padded) {
				error = "failed to extract archive: unexpected EOF";
				return {};
			}
			long_name = std::string(data.c_str(), strnlen(data.c_str(), size));
			continue;
		}
		std::string base = name.substr(name.rfind('/') == std::string::npos ? 0 : name.rfind('/') + 1);
		if ((type == '0' || type == '\0') && base == "picoclaw") {
			if (size == 0 || size > 256ull * 1024 * 1024) {
				error = "invalid PicoClaw binary size";
				return {};
			}
			std::string out = dir + "/picoclaw";
			std::ofstream file(out, std::ios::binary | std::ios::trunc);
			if (!file) {
				error = "failed to create extracted binary: open " + out + ": " + errno_text(errno);
				return {};
			}
			std::vector<char> buf(65536);
			unsigned long long left = size;
			while (left > 0) {
				std::size_t n = static_cast<std::size_t>(std::min<unsigned long long>(left, buf.size()));
				if (read(buf.data(), n) != n) {
					error = "failed to extract picoclaw binary: unexpected EOF";
					return {};
				}
				if (!file.write(buf.data(), static_cast<std::streamsize>(n))) {
					error = "failed to write extracted binary: " + out;
					return {};
				}
				left -= n;
			}
			file.close();
			if (!file) {
				error = "failed to close extracted binary: " + out;
				return {};
			}
			if (::chmod(out.c_str(), 0755) != 0) {
				error = "failed to chmod extracted binary: " + errno_text(errno);
				return {};
			}
			return out;
		}
		// Skip the entry's data.
		std::vector<char> skip(65536);
		while (padded > 0) {
			std::size_t n = static_cast<std::size_t>(std::min<unsigned long long>(padded, skip.size()));
			if (read(skip.data(), n) != n) {
				error = "failed to extract archive: unexpected EOF";
				return {};
			}
			padded -= n;
		}
	}
	error = "picoclaw binary not found in archive";
	return {};
}

std::string install_binary(const std::string &source, const std::string &destination)
{
	std::string tmp = destination + ".tmp";
	std::error_code ec;
	std::filesystem::copy_file(source, tmp, std::filesystem::copy_options::overwrite_existing, ec);
	if (ec) {
		std::filesystem::remove(tmp, ec);
		return "failed to write destination binary: " + ec.message();
	}
	if (::chmod(tmp.c_str(), 0755) != 0) {
		std::filesystem::remove(tmp, ec);
		return "failed to set destination mode: chmod " + tmp + ": " + errno_text(errno);
	}
	if (::rename(tmp.c_str(), destination.c_str()) != 0) {
		int e = errno;
		std::filesystem::remove(tmp, ec);
		return "failed to install picoclaw binary: rename " + tmp + " " + destination + ": " + errno_text(e);
	}
	return {};
}

void run_install()
{
	auto deadline = std::chrono::steady_clock::now() + install_timeout;
	auto left = [&] {
		auto d = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now());
		return std::max(std::chrono::milliseconds(1), std::min<std::chrono::milliseconds>(d, download_timeout));
	};
	auto failed = [&](const std::string &message) {
		std::string status = std::chrono::steady_clock::now() >= deadline ? "install_timeout" : "install_failed";
		finish_install(false, status, message);
	};
	std::error_code ec;
	std::filesystem::remove_all(cache_dir, ec);
	if (!std::filesystem::create_directories(cache_dir, ec) && ec) return failed("failed to create cache directory: " + ec.message());
	struct Clean {
		~Clean()
		{
			std::error_code e;
			std::filesystem::remove_all(cache_dir, e);
		}
	} clean;
	set_progress("downloading", 5);
	http_client::Request head;
	head.method = "HEAD";
	head.url = download_url;
	head.follow_redirects = false;
	head.timeout = left();
	auto r = http_client::perform(head);
	if (r.status == 0) return failed("cannot resolve latest PicoClaw release: " + r.error);
	if (r.status != 302 && r.status != 307 && r.status != 301) return failed("cannot resolve latest PicoClaw release: " + status_text(r.status));
	Release release;
	std::string error;
	if (!select_release(r.header("location").value_or(""), release, error)) return failed(error);
	log::info("picoclaw install: selected official release {}", release.tag);
	http_client::Request sums;
	sums.url = release.checksum_url;
	sums.timeout = left();
	sums.max_body = 128 * 1024;
	auto s = http_client::perform(sums);
	if (s.status == 0 && !s.too_large) return failed("failed to download picoclaw checksum: " + s.error);
	if (s.status != 200) return failed("failed to download picoclaw checksum: unexpected status " + status_text(s.status));
	std::string digest;
	std::size_t start = 0;
	while (start < s.body.size() && digest.empty()) {
		auto nl = s.body.find('\n', start);
		std::string line = json::trim_space(s.body.substr(start, nl == std::string::npos ? std::string::npos : nl - start));
		start = nl == std::string::npos ? s.body.size() : nl + 1;
		std::vector<std::string> fields;
		std::size_t i = 0;
		while (i < line.size()) {
			while (i < line.size() && std::isspace(static_cast<unsigned char>(line[i]))) i++;
			std::size_t j = i;
			while (j < line.size() && !std::isspace(static_cast<unsigned char>(line[j]))) j++;
			if (j > i) fields.push_back(line.substr(i, j - i));
			i = j;
		}
		if (fields.size() < 2 || !valid_digest(fields[0])) continue;
		std::string name = fields.back();
		if (!name.empty() && name[0] == '*') name = name.substr(1);
		if (name == archive_name) digest = json::to_lower(fields[0]);
	}
	if (digest.empty()) return failed("failed to parse sha256 digest from checksum file");
	std::string archive = cache_dir + "/picoclaw.tar.gz";
	{
		std::ofstream file(archive, std::ios::binary | std::ios::trunc);
		if (!file) return failed("failed to create archive file: open " + archive + ": " + errno_text(errno));
		http_client::Request get;
		get.url = release.archive_url;
		get.timeout = left();
		std::int64_t total = -1, received = 0;
		get.on_headers = [&](int, const std::vector<std::pair<std::string, std::string>> &headers) {
			for (const auto &[k, v] : headers)
				if (k == "content-length") total = std::atoll(v.c_str());
			return true;
		};
		get.sink = [&](std::string_view data) {
			file.write(data.data(), static_cast<std::streamsize>(data.size()));
			received += static_cast<std::int64_t>(data.size());
			if (received > 128ll * 1024 * 1024) return false;
			int progress = 10;
			if (total > 0) progress = std::min(80, 10 + static_cast<int>(static_cast<double>(received) * 70 / static_cast<double>(total)));
			set_progress("downloading", progress);
			return static_cast<bool>(file);
		};
		auto a = http_client::perform(get);
		if (received > 128ll * 1024 * 1024) return failed("failed to save archive: PicoClaw archive exceeds size limit");
		if (a.status == 0) return failed("failed to download picoclaw: " + a.error);
		if (a.status != 200) return failed("failed to download picoclaw: unexpected status " + status_text(a.status));
	}
	set_progress("verifying", 82);
	std::string actual = sha256_file(archive);
	if (actual != digest) return failed("sha256 mismatch: got " + actual);
	set_progress("extracting", 85);
	std::string binary = extract(archive, cache_dir, error);
	if (binary.empty()) return failed(error);
	set_progress("installing", 95);
	if (auto e = install_binary(binary, binary_path); !e.empty()) return failed(e);
	finish_install(true, "installed", {});
}

} // namespace

void install_runtime(http::Request &, http::Response &res)
{
	std::string output = begin_install();
	if (output.empty()) {
		output = "picoclaw installation started";
		std::thread(run_install).detach();
	}
	write_success(res, install_result_json(output));
}

} // namespace nkos::ai::picoclaw
