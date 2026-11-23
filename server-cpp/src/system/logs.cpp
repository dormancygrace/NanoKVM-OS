#include "system/logs.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "system/log_sanitize.hpp"

#include <glaze/glaze.hpp>
#include <openssl/evp.h>

#include <array>
#include <atomic>
#include <cerrno>
#include <chrono>
#include <cstdio>
#include <ctime>
#include <deque>
#include <fcntl.h>
#include <format>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <sys/klog.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace nkos::system::logs {

namespace {

using namespace std::chrono_literals;
using Clock = std::chrono::steady_clock;

constexpr auto refresh_interval = 5s;
constexpr std::size_t max_cached = 3;
constexpr const char *archive_dir = "/var/log/nanokvm-boots";
constexpr std::size_t archive_file_limit = 2 * max_bytes;
constexpr std::size_t append_anchor_bytes = 4096;
const std::array<const char *, 3> sources = {"system", "kernel", "application"};
const std::array<const char *, 3> slots = {"current", "previous", "older"};

std::int64_t now_ms()
{
	return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch()).count();
}

std::string sha256_hex(std::string_view data)
{
	unsigned char sum[32];
	EVP_Digest(data.data(), data.size(), sum, nullptr, EVP_sha256(), nullptr);
	std::string hex;
	for (unsigned char b : sum) hex += std::format("{:02x}", b);
	return hex;
}

// --- the application ring -----------------------------------------------------------------------------------

struct Ring {
	std::mutex mutex;
	std::vector<char> data = std::vector<char>(max_bytes);
	std::size_t next = 0, size = 0;
	bool truncated = false;

	void write(std::string_view d)
	{
		std::lock_guard lock(mutex);
		std::size_t n = d.size();
		if (n > max_bytes) d = d.substr(n - max_bytes);
		if (size + n > max_bytes) truncated = true;
		std::size_t first = std::min(d.size(), max_bytes - next);
		std::copy(d.begin(), d.begin() + static_cast<long>(first), data.begin() + static_cast<long>(next));
		std::copy(d.begin() + static_cast<long>(first), d.end(), data.begin());
		next = (next + d.size()) % max_bytes;
		size = std::min(max_bytes, size + n);
	}
	std::pair<std::string, bool> tail()
	{
		std::lock_guard lock(mutex);
		std::string out(size, '\0');
		std::size_t start = (next + max_bytes - size) % max_bytes;
		std::size_t first = std::min(size, max_bytes - start);
		std::copy(data.begin() + static_cast<long>(start), data.begin() + static_cast<long>(start + first), out.begin());
		std::copy(data.begin(), data.begin() + static_cast<long>(size - first), out.begin() + static_cast<long>(first));
		return {out, truncated};
	}
};
Ring application;

// --- native stderr --------------------------------------------------------------------------------------------

std::atomic<int> original_stderr{-1};

std::string stamp()
{
	timespec ts{};
	clock_gettime(CLOCK_REALTIME, &ts);
	tm t{};
	localtime_r(&ts.tv_sec, &t);
	char buf[32];
	std::strftime(buf, sizeof buf, "%Y-%m-%d %H:%M:%S", &t);
	return std::format("{}.{:03}", buf, ts.tv_nsec / 1000000);
}

void read_native(int fd)
{
	constexpr std::size_t line_bytes = 4096, lines_per_second = 20;
	std::string line;
	bool cut = false;
	Clock::time_point window{};
	std::size_t written = 0, dropped = 0;
	auto emit = [&](std::string text) {
		auto t = Clock::now();
		if (t - window >= 1s) {
			if (dropped) application.write(std::format("[{}] [native] {} more lines dropped\n", stamp(), dropped));
			window = t;
			written = dropped = 0;
		}
		if (written >= lines_per_second) {
			dropped++;
			return;
		}
		written++;
		application.write(std::format("[{}] [native] {}\n", stamp(), text));
	};
	char buf[4096];
	for (;;) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) break;
		for (ssize_t i = 0; i < n; i++) {
			if (buf[i] == '\n') {
				while (!line.empty() && line.back() == '\r') line.pop_back();
				if (cut) line += " [cut]";
				emit(std::move(line));
				line.clear();
				cut = false;
			} else if (line.size() < line_bytes) {
				line += buf[i];
			} else {
				cut = true;
			}
		}
	}
	if (!line.empty()) emit(cut ? line + " [cut]" : line);
	::close(fd);
	restore_native_stderr();
}

// --- the collector ------------------------------------------------------------------------------------------

struct Snapshot {
	bool archiveAvailable = false;
	std::string boot, source, state;
	std::string content;
	int lines = 0;
	bool truncated = false;
	std::int64_t collectedAt = 0;
};

struct Cached {
	Snapshot snapshot;
	Clock::time_point at;
	std::string raw_digest;
	bool truncated = false, raw_valid = false;
};

class Archive;

struct {
	std::mutex mutex;
	std::map<std::string, Cached> cache;
	std::deque<Clock::time_point> misses;
	std::unique_ptr<Archive> archive;
} collector;

struct Read {
	std::optional<std::string> data;
	bool truncated = false;
};

bool regular_no_link(const std::string &path, struct stat &st) { return ::lstat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode); }

// The last limit bytes of a regular file (no link, the same file opened as checked).
std::optional<std::pair<std::string, bool>> read_tail(const std::string &path, std::size_t limit, bool &missing)
{
	struct stat before{};
	if (::lstat(path.c_str(), &before) != 0) {
		missing = errno == ENOENT;
		return std::nullopt;
	}
	if (!S_ISREG(before.st_mode)) return std::nullopt;
	int fd = ::open(path.c_str(), O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
	if (fd < 0) return std::nullopt;
	struct stat info{};
	if (::fstat(fd, &info) != 0 || !S_ISREG(info.st_mode) || info.st_ino != before.st_ino || info.st_dev != before.st_dev) {
		::close(fd);
		return std::nullopt;
	}
	std::size_t size = static_cast<std::size_t>(info.st_size), length = std::min(size, limit);
	std::string data(length, '\0');
	ssize_t n = ::pread(fd, data.data(), length, static_cast<off_t>(size - length));
	::close(fd);
	if (n < 0) return std::nullopt;
	data.resize(static_cast<std::size_t>(n));
	return std::pair{data, size > length};
}

bool has_system_log(const std::string &dir)
{
	struct stat st{};
	for (const char *name : {"messages", "messages.0", "messages.1"})
		if (::stat((dir + "/" + name).c_str(), &st) == 0) return true;
	return false;
}

// The newest files first within one budget, then in time order: BusyBox rotates messages into messages.0 (.1).
Read read_system()
{
	std::string dir = "/run/log";
	if (!has_system_log(dir)) dir = "/var/log";
	std::size_t remaining = max_bytes;
	std::vector<std::string> parts;
	bool truncated = false, found = false;
	for (const char *name : {"messages", "messages.0", "messages.1"}) {
		bool missing = false;
		auto part = read_tail(dir + "/" + name, remaining, missing);
		if (!part) {
			if (missing) continue;
			return {};
		}
		found = true;
		remaining -= part->first.size();
		truncated = truncated || part->second;
		parts.push_back(std::move(part->first));
		if (remaining == 0) break;
	}
	if (!found) return {};
	std::string combined;
	for (auto it = parts.rbegin(); it != parts.rend(); ++it) combined += *it;
	return {combined, truncated};
}

Read read_kernel()
{
	std::string data(max_bytes, '\0');
	int n = ::klogctl(3, data.data(), static_cast<int>(data.size()));  // SYSLOG_ACTION_READ_ALL: the ring stays
	if (n < 0) return {};
	data.resize(static_cast<std::size_t>(n));
	// A full read may begin halfway through an entry.
	return {data, static_cast<std::size_t>(n) == max_bytes};
}

void remember(const std::string &key, Cached value)
{
	if (!collector.cache.count(key) && collector.cache.size() >= max_cached) {
		auto oldest = collector.cache.begin();
		for (auto it = collector.cache.begin(); it != collector.cache.end(); ++it)
			if (it->second.at < oldest->second.at) oldest = it;
		collector.cache.erase(oldest);
	}
	collector.cache[key] = std::move(value);
}

std::optional<Snapshot> archive_read(const std::string &slot, const std::string &source);

// nullopt: the read rate was exceeded. The caller holds collector.mutex.
std::optional<Snapshot> read_locked(const std::string &source, const std::string &boot, bool internal)
{
	auto now = Clock::now();
	std::string key = boot + "/" + source;
	auto cached = collector.cache.find(key);
	if (cached != collector.cache.end() && !internal && now - cached->second.at < refresh_interval)
		return cached->second.snapshot;
	// Six cache misses per five seconds across all clients.
	if (!internal) {
		while (!collector.misses.empty() && now - collector.misses.front() >= refresh_interval) collector.misses.pop_front();
		if (collector.misses.size() >= 6) return std::nullopt;
		collector.misses.push_back(now);
	}
	Snapshot s{false, boot, source, "unavailable", "", 0, false, now_ms()};
	if (boot != "current") {
		if (collector.archive)
			if (auto saved = archive_read(boot, source)) s = *saved;
		remember(key, {s, now, "", false, false});
		return s;
	}
	Read r;
	if (source == "system") r = read_system();
	else if (source == "application") {
		auto [data, truncated] = application.tail();
		r = {data, truncated};
	} else r = read_kernel();
	std::string digest;
	if (r.data) {
		digest = sha256_hex(*r.data);
		if (cached != collector.cache.end() && cached->second.raw_valid && cached->second.raw_digest == digest &&
		    cached->second.truncated == r.truncated) {
			// Fresh source I/O, the already redacted content.
			s = cached->second.snapshot;
			s.collectedAt = now_ms();
		} else {
			auto clean = sanitize_tail(*r.data, r.truncated);
			s.content = std::move(clean.content);
			s.lines = clean.lines;
			s.truncated = clean.truncated;
			s.state = s.content.empty() ? "empty" : "ready";
		}
	}
	// Failures are cached too: an absent source cannot cause a read storm.
	remember(key, {s, now, digest, r.truncated, r.data.has_value()});
	return s;
}

// --- the archive ----------------------------------------------------------------------------------------------

struct Window {
	std::int64_t offset = 0;
	int length = 0;
	std::string sha256;
};
struct ArchiveSource {
	int lines = 0;
	bool truncated = false;
	std::int64_t collectedAt = 0;
	std::optional<Window> window;
};
struct BootRecord {
	std::string bootId;
	std::int64_t startedAt = 0, savedAt = 0;
	std::optional<std::map<std::string, ArchiveSource>> sources;
};
struct BootInfo {
	std::string id;
	std::int64_t startedAt = 0, savedAt = 0;
};

bool valid_boot_id(const std::string &id)
{
	// [0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}
	if (id.size() != 36) return false;
	for (std::size_t i = 0; i < 36; i++) {
		bool dash = i == 8 || i == 13 || i == 18 || i == 23;
		if (dash ? id[i] != '-' : !((id[i] >= '0' && id[i] <= '9') || (id[i] >= 'a' && id[i] <= 'f'))) return false;
	}
	return true;
}

std::optional<std::string> read_file(const std::string &path, std::size_t limit)
{
	struct stat before{};
	if (!regular_no_link(path, before) || static_cast<std::size_t>(before.st_size) > limit) return std::nullopt;
	int fd = ::open(path.c_str(), O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
	if (fd < 0) return std::nullopt;
	struct stat info{};
	if (::fstat(fd, &info) != 0 || !S_ISREG(info.st_mode) || info.st_ino != before.st_ino) {
		::close(fd);
		return std::nullopt;
	}
	std::string data;
	char buf[65536];
	for (ssize_t n; (n = ::read(fd, buf, sizeof buf)) > 0;) {
		data.append(buf, static_cast<std::size_t>(n));
		if (data.size() > limit) {
			::close(fd);
			return std::nullopt;
		}
	}
	::close(fd);
	return data;
}

bool sync_dir(const std::string &dir)
{
	int fd = ::open(dir.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
	if (fd < 0) return false;
	bool ok = ::fsync(fd) == 0;
	::close(fd);
	return ok;
}

class Archive {
public:
	std::mutex mutex, flush_mutex;
	BootRecord record;
	std::map<std::string, std::string> digests;
	std::map<std::string, std::string> snapshots;  // the whole last snapshot per source, to verify the overlap
	bool failed = false;

	static std::unique_ptr<Archive> open(const std::string &boot_id, std::int64_t started_at)
	{
		if (!valid_boot_id(boot_id)) return nullptr;
		if (::mkdir(archive_dir, 0700) != 0 && errno != EEXIST) return nullptr;
		struct stat st{};
		if (::lstat(archive_dir, &st) != 0 || !S_ISDIR(st.st_mode)) return nullptr;
		if (::chmod(archive_dir, 0700) != 0) return nullptr;
		auto a = std::make_unique<Archive>();
		auto current = a->load_record("current");
		if (current && current->bootId != boot_id) {
			if (!a->remove_slot("older") || !a->move_slot("previous", "older") || !a->move_slot("current", "previous")) return nullptr;
			current.reset();
		}
		std::string dir = std::string(archive_dir) + "/current";
		if (::mkdir(dir.c_str(), 0700) != 0 && errno != EEXIST) return nullptr;
		if (!current) current = BootRecord{boot_id, started_at, 0, std::map<std::string, ArchiveSource>{}};
		a->record = *current;
		std::string meta;
		(void)glz::write_json(a->record, meta);
		if (!a->atomic_write("current/boot.json", meta) || !sync_dir(archive_dir)) return nullptr;
		return a;
	}

	std::optional<BootRecord> load_record(const std::string &slot)
	{
		std::string dir = std::string(archive_dir) + "/" + slot;
		struct stat st{};
		if (::lstat(dir.c_str(), &st) != 0 || !S_ISDIR(st.st_mode)) return std::nullopt;
		auto data = read_file(dir + "/boot.json", 4096);
		if (!data) return std::nullopt;
		BootRecord r;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(r, *data) || !valid_boot_id(r.bootId) || !r.sources)
			return std::nullopt;
		return r;
	}

	bool move_slot(const std::string &from, const std::string &to)
	{
		std::string a = std::string(archive_dir) + "/" + from, b = std::string(archive_dir) + "/" + to;
		struct stat st{};
		if (::lstat(a.c_str(), &st) != 0) return errno == ENOENT;
		if (!S_ISDIR(st.st_mode)) return false;
		return ::rename(a.c_str(), b.c_str()) == 0;
	}

	bool remove_slot(const std::string &slot)
	{
		std::string dir = std::string(archive_dir) + "/" + slot;
		struct stat st{};
		if (::lstat(dir.c_str(), &st) != 0) return errno == ENOENT;
		if (!S_ISDIR(st.st_mode)) return false;
		std::vector<std::string> names = {"boot.json", "boot.json.tmp"};
		for (const char *s : sources) names.push_back(std::string(s) + ".log"), names.push_back(std::string(s) + ".log.tmp");
		for (const auto &name : names)
			if (::unlink((dir + "/" + name).c_str()) != 0 && errno != ENOENT) return false;
		return ::rmdir(dir.c_str()) == 0;
	}

	bool atomic_write(const std::string &name, std::string_view data)
	{
		std::string path = std::string(archive_dir) + "/" + name, temp = path + ".tmp";
		if (::unlink(temp.c_str()) != 0 && errno != ENOENT) return false;
		int fd = ::open(temp.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		if (fd < 0) return false;
		bool ok = ::write(fd, data.data(), data.size()) == static_cast<ssize_t>(data.size()) && ::fsync(fd) == 0;
		ok = ::close(fd) == 0 && ok;
		if (!ok || ::rename(temp.c_str(), path.c_str()) != 0) {
			::unlink(temp.c_str());
			return false;
		}
		return sync_dir(path.substr(0, path.rfind('/')));
	}

	// Appends when the file stays within the limit; nullopt on failure, {size, false} when it would not fit.
	std::optional<std::pair<std::size_t, bool>> append_file(const std::string &name, std::string_view text)
	{
		std::string path = std::string(archive_dir) + "/" + name;
		struct stat before{};
		if (!regular_no_link(path, before)) return std::nullopt;
		int fd = ::open(path.c_str(), O_WRONLY | O_APPEND | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
		if (fd < 0) return std::nullopt;
		struct stat info{};
		if (::fstat(fd, &info) != 0 || !S_ISREG(info.st_mode) || info.st_ino != before.st_ino) {
			::close(fd);
			return std::nullopt;
		}
		std::size_t size = static_cast<std::size_t>(info.st_size) + text.size();
		if (size > archive_file_limit) {
			::close(fd);
			return std::pair{std::size_t{0}, false};
		}
		bool ok = true;
		if (!text.empty()) ok = ::write(fd, text.data(), text.size()) == static_cast<ssize_t>(text.size()) && ::fsync(fd) == 0;
		ok = ::close(fd) == 0 && ok;
		if (!ok) return std::nullopt;
		return std::pair{size, true};
	}

	// Brings current/<source>.log up to date: usually appends the lines after the last snapshot's end.
	std::optional<std::int64_t> write_source(const std::string &source, const std::string &content)
	{
		std::string name = "current/" + source + ".log";
		if (auto prev = snapshots.find(source); prev != snapshots.end() && !prev->second.empty()) {
			const std::string &previous = prev->second;
			std::string anchor = previous.substr(previous.size() - std::min(previous.size(), append_anchor_bytes));
			auto at = content.rfind(anchor);
			if (at != std::string::npos && content.find(anchor) == at) {
				std::size_t end = at + anchor.size();
				std::string_view overlap(content.data(), end);
				std::size_t begins = previous.size() - overlap.size();
				if (previous.size() >= overlap.size() && std::string_view(previous).ends_with(overlap) &&
				    (begins == 0 || previous[begins - 1] == '\n') && (end == content.size() || content[end] == '\n')) {
					std::string delta = end < content.size() ? content.substr(end + 1) + "\n" : "";
					auto r = append_file(name, delta);
					if (!r) {
						snapshots.erase(source);
						return std::nullopt;
					}
					if (r->second) {
						snapshots[source] = content;
						return static_cast<std::int64_t>(r->first) - static_cast<std::int64_t>(content.size() + 1);
					}
				}
			}
		}
		snapshots.erase(source);
		if (!atomic_write(name, content + "\n")) return std::nullopt;
		snapshots[source] = content;
		return 0;
	}

	bool save(const std::vector<Snapshot> &list)
	{
		std::lock_guard lock(mutex);
		bool ok = save_locked(list);
		failed = !ok;
		if (!ok) {
			digests.clear();
			snapshots.clear();
		}
		return ok;
	}

	bool save_locked(const std::vector<Snapshot> &list)
	{
		BootRecord next = record;
		auto next_digests = digests;
		bool changed = false;
		for (const auto &s : list) {
			if (s.state == "unavailable") continue;
			if (s.content.size() > max_bytes || s.boot != "current") return false;
			std::string digest = sha256_hex(s.content);
			if (auto d = digests.find(s.source); d != digests.end() && d->second == digest) continue;
			auto offset = write_source(s.source, s.content);
			if (!offset) return false;
			next_digests[s.source] = digest;
			(*next.sources)[s.source] = ArchiveSource{s.lines, s.truncated, s.collectedAt,
			                                          Window{*offset, static_cast<int>(s.content.size() + 1), digest}};
			next.savedAt = s.collectedAt;
			changed = true;
		}
		if (!changed) return true;
		std::string meta;
		(void)glz::write_json(next, meta);
		if (!atomic_write("current/boot.json", meta)) return false;
		record = next;
		digests = next_digests;
		return true;
	}

	std::vector<BootInfo> boots()
	{
		std::lock_guard lock(mutex);
		std::vector<BootInfo> out;
		for (const char *slot : slots)
			if (auto r = load_record(slot)) out.push_back({slot, r->startedAt, r->savedAt});
		return out;
	}

	bool healthy()
	{
		std::lock_guard lock(mutex);
		return !failed;
	}

	// The latest committed snapshot of the file (committedSource): the window when it still matches.
	static std::pair<std::string, bool> committed(std::string data, const std::optional<Window> &w)
	{
		if (!w) return {data + "\n", false};
		if (w->offset >= 0 && static_cast<std::size_t>(w->offset) <= data.size() && w->length > 0 &&
		    static_cast<std::size_t>(w->length) <= max_bytes + 1 &&
		    static_cast<std::size_t>(w->length) <= data.size() - static_cast<std::size_t>(w->offset)) {
			std::string candidate = data.substr(static_cast<std::size_t>(w->offset), static_cast<std::size_t>(w->length));
			if (candidate.back() == '\n' && sha256_hex(std::string_view(candidate).substr(0, candidate.size() - 1)) == w->sha256)
				return {candidate, false};
		}
		return {data, true};
	}

	std::optional<Snapshot> read(const std::string &slot, const std::string &source)
	{
		std::lock_guard lock(mutex);
		auto r = load_record(slot);
		if (!r) return std::nullopt;
		auto meta = r->sources->find(source);
		if (meta == r->sources->end()) return std::nullopt;
		auto data = read_file(std::string(archive_dir) + "/" + slot + "/" + source + ".log", archive_file_limit);
		if (!data) return std::nullopt;
		auto [text, recovered] = committed(std::move(*data), meta->second.window);
		auto clean = sanitize_tail(text, false);
		Snapshot s;
		s.source = source;
		s.boot = slot;
		s.state = clean.content.empty() ? "empty" : "ready";
		s.content = std::move(clean.content);
		s.lines = clean.lines;
		s.truncated = meta->second.truncated || clean.truncated || recovered;
		s.collectedAt = meta->second.collectedAt;
		return s;
	}
};

std::optional<Snapshot> archive_read(const std::string &slot, const std::string &source)
{
	return collector.archive->read(slot, source);
}

void flush_archives()
{
	Archive *archive;
	{
		std::lock_guard lock(collector.mutex);
		archive = collector.archive.get();
	}
	if (!archive) return;
	std::lock_guard flush(archive->flush_mutex);
	std::vector<Snapshot> list;
	{
		std::lock_guard lock(collector.mutex);
		for (const char *source : sources) list.push_back(*read_locked(source, "current", true));
	}
	if (!archive->save(list)) log::warn("save log archive failed");
}

void schedule_flush()
{
	loop::after(std::chrono::minutes(1), [] {
		loop::blocking(flush_archives);
		schedule_flush();
	});
}

std::string snapshot_json(const Snapshot &s)
{
	std::string out;
	(void)glz::write_json(s, out);
	return out;
}

// Go's url.ParseQuery: & separated, no ';', valid escapes.
bool parse_query(const std::string &raw, std::multimap<std::string, std::string> &out)
{
	auto unescape = [](std::string_view s, std::string &v) {
		for (std::size_t i = 0; i < s.size(); i++) {
			if (s[i] == '%') {
				if (i + 2 >= s.size() || !std::isxdigit(static_cast<unsigned char>(s[i + 1])) ||
				    !std::isxdigit(static_cast<unsigned char>(s[i + 2])))
					return false;
				v += static_cast<char>(std::stoi(std::string(s.substr(i + 1, 2)), nullptr, 16));
				i += 2;
			} else {
				v += s[i] == '+' ? ' ' : s[i];
			}
		}
		return true;
	};
	for (std::size_t start = 0; start <= raw.size();) {
		auto end = raw.find('&', start);
		if (end == std::string::npos) end = raw.size();
		std::string_view part(raw.data() + start, end - start);
		start = end + 1;
		if (part.empty()) {
			if (end == raw.size()) break;
			continue;
		}
		if (part.find(';') != std::string_view::npos) return false;
		auto eq = part.find('=');
		std::string key, value;
		if (!unescape(part.substr(0, eq), key)) return false;
		if (eq != std::string_view::npos && !unescape(part.substr(eq + 1), value)) return false;
		out.emplace(key, value);
		if (end == raw.size()) break;
	}
	return true;
}

} // namespace

void restore_native_stderr()
{
	int fd = original_stderr.load();
	if (fd >= 0) ::dup3(fd, 2, 0);
}

void capture_native_stderr()
{
	int p[2];
	if (::pipe2(p, O_CLOEXEC) != 0) return;
	int original = ::fcntl(2, F_DUPFD_CLOEXEC, 3);
	if (original < 0 || ::dup3(p[1], 2, 0) < 0) {
		::close(p[0]);
		::close(p[1]);
		if (original >= 0) ::close(original);
		return;
	}
	// A full pipe drops writes instead of blocking a native thread.
	::fcntl(2, F_SETFL, ::fcntl(2, F_GETFL) | O_NONBLOCK);
	::close(p[1]);
	original_stderr = original;
	std::thread(read_native, p[0]).detach();
}

void init()
{
	log::set_sink([](std::string_view line) { application.write(line); });
	std::string boot_id;
	if (FILE *f = std::fopen("/proc/sys/kernel/random/boot_id", "r")) {
		char buf[64] = {};
		if (std::fgets(buf, sizeof buf, f)) boot_id = buf;
		std::fclose(f);
	}
	while (!boot_id.empty() && (boot_id.back() == '\n' || boot_id.back() == ' ')) boot_id.pop_back();
	std::int64_t started = now_ms();
	if (FILE *f = std::fopen("/proc/uptime", "r")) {
		double up = 0;
		if (std::fscanf(f, "%lf", &up) == 1 && up >= 0) started -= static_cast<std::int64_t>(up * 1000);
		std::fclose(f);
	}
	auto archive = Archive::open(boot_id, started);
	if (!archive) {
		log::warn("persistent log archive is unavailable");
		return;
	}
	// Application messages of earlier starts in this boot come back.
	auto saved = archive->read("current", "application");
	{
		std::lock_guard lock(collector.mutex);
		collector.archive = std::move(archive);
	}
	if (saved && !saved->content.empty()) application.write(saved->content + "\n");
}

void start()
{
	loop::blocking(flush_archives);
	schedule_flush();
}

void flush() { flush_archives(); }

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/logs", auth::guarded([](http::Request &req, http::Response &res) {
		res.set_header("Cache-Control", "no-store");
		res.set_header("X-Content-Type-Options", "nosniff");
		std::multimap<std::string, std::string> query;
		bool ok = req.query.size() <= 128 && parse_query(req.query, query);
		for (const auto &[key, value] : query)
			if ((key != "source" && key != "boot") || query.count(key) != 1) ok = false;
		if (!ok) return res.json(400, R"({"code":-1,"msg":"invalid log query"})");
		std::string source = query.count("source") ? query.find("source")->second : "";
		std::string boot = query.count("boot") ? query.find("boot")->second : "";
		if (source.empty()) source = "system";
		if (boot.empty()) boot = "current";
		bool valid_source = source == "system" || source == "kernel" || source == "application";
		bool valid_boot = boot == "current" || boot == "previous" || boot == "older";
		if (!valid_source || !valid_boot) return res.json(400, R"({"code":-1,"msg":"unsupported log source"})");
		http::defer(req, res, [source, boot](http::Response &out) {
			std::optional<Snapshot> s;
			bool healthy = false;
			{
				std::lock_guard lock(collector.mutex);
				s = read_locked(source, boot, false);
			}
			if (!s) {
				out.set_header("Retry-After", "5");
				return out.json(429, R"({"code":-1,"msg":"log read rate exceeded"})");
			}
			if (collector.archive) healthy = collector.archive->healthy();
			s->archiveAvailable = healthy;
			out.json(200, R"({"code":0,"data":)" + snapshot_json(*s) + R"(,"msg":"success"})");
		});
	}, admin));
	router.add("GET", "/api/vm/logs/boots", auth::guarded([](http::Request &req, http::Response &res) {
		res.set_header("Cache-Control", "no-store");
		http::defer(req, res, [](http::Response &out) {
			std::vector<BootInfo> boots{{"current", 0, 0}};
			bool available = false;
			if (collector.archive) {
				boots = collector.archive->boots();
				available = collector.archive->healthy();
			}
			std::string list;
			(void)glz::write_json(boots, list);
			out.json(200, std::format(R"({{"code":0,"data":{{"archiveAvailable":{},"boots":{}}},"msg":"success"}})", available, list));
		});
	}, admin));
}

} // namespace nkos::system::logs
