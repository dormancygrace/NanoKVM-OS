// GET /api/vm/memory/status and POST /api/vm/memory/swap (admin), as the Go server's service/vm/memory-status.go:
// memory, cache and swap use, the zram and SD swap (S38memory configures them), the video memory pool.

#include "system/memory.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/process.hpp"
#include "video/screen.hpp"

#include <glaze/glaze.hpp>

#include <dirent.h>
#include <fstream>
#include <map>
#include <mutex>
#include <optional>
#include <sstream>
#include <string>
#include <sys/stat.h>

namespace nkos::system {

namespace {

constexpr const char *memory_service = "/etc/init.d/S38memory";

struct Swap {
	bool enabled = false, available = false;
	std::int64_t sizeMiB = 0;
	std::uint64_t usedBytes = 0;
	std::int64_t priority = 0;
	std::optional<std::uint64_t> memoryBytes, compressedBytes, originalBytes;  // omitempty
	std::optional<std::string> algorithm;
	bool autoSize = false;  // "auto"
	std::optional<std::int64_t> autoMiB;

	struct glaze {
		using T = Swap;
		static constexpr auto value = glz::object("enabled", &T::enabled, "available", &T::available, "sizeMiB", &T::sizeMiB,
		                                          "usedBytes", &T::usedBytes, "priority", &T::priority, "memoryBytes",
		                                          &T::memoryBytes, "compressedBytes", &T::compressedBytes, "originalBytes",
		                                          &T::originalBytes, "algorithm", &T::algorithm, "auto", &T::autoSize,
		                                          "autoMiB", &T::autoMiB);
	};
};
struct VideoMemory {
	int sizeMiB = 0;
	bool shared = false;
};
struct Status {
	VideoMemory videoMemory;
	std::uint64_t totalBytes = 0, availableBytes = 0, usedBytes = 0, cachedBytes = 0, swapTotalBytes = 0, swapUsedBytes = 0,
	              videoBytes = 0;
	Swap zram, sd;
};
struct SwapRequest {
	std::string kind;
	bool enabled = false;
	std::int64_t sizeMiB = 0;
};

std::optional<std::string> read(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

std::vector<std::string> fields(const std::string &line)
{
	std::vector<std::string> out;
	std::istringstream in(line);
	for (std::string f; in >> f;) out.push_back(f);
	return out;
}

std::optional<std::uint64_t> parse_u64(const std::string &s)
{
	if (s.empty() || s.find_first_not_of("0123456789") != std::string::npos) return std::nullopt;
	try {
		return std::stoull(s);
	} catch (...) {
		return std::nullopt;
	}
}

std::optional<std::int64_t> parse_i64(const std::string &s)
{
	if (s.empty()) return std::nullopt;
	std::size_t start = s[0] == '-' || s[0] == '+' ? 1 : 0;
	if (start == s.size() || s.find_first_not_of("0123456789", start) != std::string::npos) return std::nullopt;
	try {
		return std::stoll(s);
	} catch (...) {
		return std::nullopt;
	}
}

std::map<std::string, std::uint64_t> counters(const std::string &text)
{
	std::map<std::string, std::uint64_t> out;
	std::istringstream in(text);
	for (std::string line; std::getline(in, line);) {
		auto f = fields(line);
		if (f.size() < 2) continue;
		if (auto v = parse_u64(f[1])) {
			std::string key = f[0];
			if (key.ends_with(":")) key.pop_back();
			out[key] = *v * 1024;
		}
	}
	return out;
}

std::map<std::string, Swap> active_swaps(const std::string &text)
{
	std::map<std::string, Swap> out;
	std::istringstream in(text);
	for (std::string line; std::getline(in, line);) {
		auto f = fields(line);
		if (f.size() < 5 || f[0] == "Filename") continue;
		auto size = parse_i64(f[2]);
		auto used = parse_u64(f[3]);
		auto priority = parse_i64(f[4]);
		if (!size || !used || !priority || *size <= 0) continue;
		Swap s;
		s.enabled = s.available = true;
		s.sizeMiB = (*size + 1023) / 1024;
		s.usedBytes = *used * 1024;
		s.priority = *priority;
		out[f[0]] = s;
	}
	return out;
}

std::uint64_t value(const std::map<std::string, std::uint64_t> &m, const char *key)
{
	auto it = m.find(key);
	return it == m.end() ? 0 : it->second;
}

std::int64_t zram_auto_mib(const std::map<std::string, std::uint64_t> &c)
{
	constexpr std::uint64_t video = 104ull << 20;
	std::uint64_t cma = std::min(value(c, "CmaTotal"), value(c, "MemTotal"));
	std::uint64_t linux_bytes = value(c, "MemTotal") - cma;
	if (cma > video) linux_bytes += cma - video;
	return static_cast<std::int64_t>(linux_bytes / 2 >> 20);
}

bool zram_auto_default()
{
	std::string mode = read("/proc/device-tree/nanokvm,video-memory-mode").value_or("");
	while (!mode.empty() && mode.back() == '\0') mode.pop_back();
	return mode == "shared" || mode == "uhd";
}

bool valid_swap(const SwapRequest &r)
{
	if (r.kind == "zram") return r.sizeMiB == 0 || r.sizeMiB == 32 || r.sizeMiB == 64;
	if (r.kind == "sd") return r.sizeMiB == 128 || r.sizeMiB == 256 || r.sizeMiB == 512;
	return false;
}

bool zram_module_present()
{
	std::string release = read("/proc/sys/kernel/osrelease").value_or("");
	while (!release.empty() && (release.back() == '\n' || release.back() == ' ')) release.pop_back();
	std::string dir = "/lib/modules/" + release + "/kernel/drivers/block/zram";
	DIR *d = ::opendir(dir.c_str());
	if (!d) return false;
	bool found = false;
	while (dirent *e = ::readdir(d))
		if (std::string(e->d_name).starts_with("zram.ko")) found = true;
	::closedir(d);
	return found;
}

std::optional<Status> read_status()
{
	Status s;
	auto pool = video::booted_video_pool();
	s.videoMemory = {pool.mib, pool.reusable};
	auto meminfo = read("/proc/meminfo");
	if (!meminfo) return std::nullopt;
	auto c = counters(*meminfo);
	s.totalBytes = value(c, "MemTotal");
	if (s.totalBytes == 0) return std::nullopt;
	s.availableBytes = std::min(value(c, "MemAvailable"), s.totalBytes);
	s.usedBytes = s.totalBytes - s.availableBytes;
	std::uint64_t cached = value(c, "Cached") + value(c, "Buffers") + value(c, "SReclaimable");
	s.cachedBytes = cached - std::min(cached, value(c, "Shmem"));
	s.swapTotalBytes = value(c, "SwapTotal");
	s.swapUsedBytes = s.swapTotalBytes - std::min(s.swapTotalBytes, value(c, "SwapFree"));
	auto swaps = read("/proc/swaps");
	if (!swaps) return std::nullopt;
	auto active = active_swaps(*swaps);
	if (auto it = active.find("/dev/zram0"); it != active.end()) s.zram = it->second;
	if (auto it = active.find("/swapfile"); it != active.end()) s.sd = it->second;
	if (!s.zram.enabled) s.zram.sizeMiB = 64;
	if (!s.sd.enabled) s.sd.sizeMiB = 256;
	std::int64_t auto_mib = zram_auto_mib(c);
	if (auto_mib != 0) s.zram.autoMiB = auto_mib;
	s.zram.autoSize = zram_auto_default();
	std::istringstream conf(read("/etc/kvm/memory.conf").value_or(""));
	for (std::string line; std::getline(conf, line);) {
		auto eq = line.find('=');
		if (eq == std::string::npos) continue;
		std::string key = line.substr(0, eq), v = line.substr(eq + 1);
		if (key == "ZRAM_SIZE" && (v == "auto" || v == "128" || v == "162")) {
			s.zram.autoSize = true;
			continue;
		}
		std::int64_t size = parse_i64(v).value_or(0);
		if (key == "ZRAM_SIZE" && size > 0 && valid_swap({"zram", false, size})) {
			s.zram.autoSize = false;
			if (!s.zram.enabled) s.zram.sizeMiB = size;
		}
		if (key == "SD_SIZE" && !s.sd.enabled && valid_swap({"sd", false, size})) s.sd.sizeMiB = size;
	}
	if (s.zram.autoSize && !s.zram.enabled) s.zram.sizeMiB = auto_mib;
	bool helper = exists(memory_service);
	s.sd.available = helper;
	s.zram.available = helper && (exists("/sys/block/zram0") || exists("/sys/module/zram") || zram_module_present());
	s.zram.algorithm = "lz4";
	if (auto mm = read("/sys/block/zram0/mm_stat")) {
		auto f = fields(*mm);
		if (f.size() >= 3) {
			auto nonzero = [](std::optional<std::uint64_t> v) -> std::optional<std::uint64_t> {
				return v && *v ? v : std::nullopt;
			};
			s.zram.originalBytes = nonzero(parse_u64(f[0]));
			s.zram.compressedBytes = nonzero(parse_u64(f[1]));
			s.zram.memoryBytes = nonzero(parse_u64(f[2]));
		}
	}
	if (auto algo = read("/sys/block/zram0/comp_algorithm"))
		for (const auto &f : fields(*algo))
			if (f.starts_with("[")) {
				std::string a = f;
				std::erase(a, '[');
				std::erase(a, ']');
				s.zram.algorithm = a;
			}
	if (s.zram.algorithm && s.zram.algorithm->empty()) s.zram.algorithm.reset();
	if (auto ion = read("/sys/kernel/debug/ion/carveout/num_of_alloc_bytes")) {
		std::string v = *ion;
		while (!v.empty() && (v.back() == '\n' || v.back() == ' ')) v.pop_back();
		s.videoBytes = parse_u64(v).value_or(0);
	}
	return s;
}

std::string status_json(const Status &s)
{
	std::string out;
	(void)glz::write_json(s, out);
	return out;
}

void answer_status(http::Response &res)
{
	auto s = read_status();
	if (!s) return api::err(res, -1, "Failed to read memory statistics");
	api::send(res, 0, "success", status_json(*s));
}

} // namespace

std::optional<std::string> memory_status_json()
{
	auto s = read_status();
	if (!s) return std::nullopt;
	return status_json(*s);
}

void add_memory_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/memory/status", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) { answer_status(out); });
	}, admin));
	router.add("POST", "/api/vm/memory/swap", auth::guarded([](http::Request &req, http::Response &res) {
		SwapRequest r;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body) || !valid_swap(r))
			return api::err(res, -1, "Invalid swap type or size");
		http::defer(req, res, [r](http::Response &out) {
			static std::mutex mutation;
			std::lock_guard lock(mutation);
			std::string size = r.sizeMiB == 0 ? "auto" : std::to_string(r.sizeMiB);
			auto result = process::run({"sh", memory_service, "configure", r.kind == "zram" ? "zram" : "sd", r.enabled ? "1" : "0", size});
			if (!result.ok()) {
				std::string message = result.output;
				while (!message.empty() && std::isspace(static_cast<unsigned char>(message.back()))) message.pop_back();
				auto a = message.find_first_not_of(" \t\r\n");
				message = a == std::string::npos ? "" : message.substr(a);
				if (message.size() > 800) message = message.substr(message.size() - 800);
				if (message.empty()) message = "Failed to change swap";
				return api::err(out, -2, message);
			}
			answer_status(out);
		});
	}, admin));
}

} // namespace nkos::system

