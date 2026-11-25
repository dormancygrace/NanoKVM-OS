// The CPU clock, as the Go server's service/vm/cpufreq.go: the qualified cpufreq driver's state, the clock chosen
// (850 MHz to 1150 MHz; above 1000 MHz only at runtime unless applied at boot), and the start-time apply with the
// fallback when an overclocked start did not settle.

#include "system/cpufreq.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"

#include <glaze/glaze.hpp>

#include <dirent.h>
#include <fcntl.h>
#include <format>
#include <fstream>
#include <mutex>
#include <optional>
#include <sstream>
#include <string>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace nkos::system {

namespace {

using namespace std::chrono_literals;

constexpr const char *policy = "/sys/devices/system/cpu/cpufreq/policy0";
constexpr const char *preference_file = "/etc/kvm/cpufreq";
constexpr const char *runtime_file = "/run/nanokvm-cpufreq";
constexpr const char *thermal_root = "/sys/class/thermal";
constexpr const char *boot_flag = "/etc/kvm/cpufreq.boot";
constexpr const char *boot_pending = "/etc/kvm/cpufreq.boot-pending";
constexpr const char *boot_failed = "/etc/kvm/cpufreq.boot-failed";
constexpr auto boot_settle = std::chrono::minutes(2);

std::mutex mutex;

struct State {
	bool supported = false, throttled = false;
	int running = 0, target = 1000;
	std::vector<int> options;
	bool applyAtBoot = false, bootFallback = false;
};

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

std::optional<std::string> read_trimmed(const std::string &path)
{
	std::ifstream in(path);
	if (!in) return std::nullopt;
	std::string v((std::istreambuf_iterator<char>(in)), {});
	auto a = v.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return std::string();
	return v.substr(a, v.find_last_not_of(" \t\r\n") - a + 1);
}

std::optional<std::string> read_policy(const std::string &name) { return read_trimmed(std::string(policy) + "/" + name); }

std::optional<int> parse_int(const std::optional<std::string> &s)
{
	if (!s || s->empty()) return std::nullopt;
	std::size_t start = (*s)[0] == '-' || (*s)[0] == '+' ? 1 : 0;
	if (start == s->size() || s->find_first_not_of("0123456789", start) != std::string::npos) return std::nullopt;
	try {
		return std::stoi(*s);
	} catch (...) {
		return std::nullopt;
	}
}

std::vector<std::string> glob_dir(const std::string &dir, const std::string &prefix, bool digit_after)
{
	std::vector<std::string> out;
	if (DIR *d = ::opendir(dir.c_str())) {
		while (dirent *e = ::readdir(d)) {
			std::string name = e->d_name;
			if (!name.starts_with(prefix)) continue;
			if (digit_after && (name.size() == prefix.size() || !std::isdigit(static_cast<unsigned char>(name[prefix.size()]))))
				continue;
			out.push_back(dir + "/" + name);
		}
		::closedir(d);
	}
	std::sort(out.begin(), out.end());
	return out;
}

bool thermal_limited()
{
	for (const auto &zone : glob_dir(thermal_root, "thermal_zone", false)) {
		auto kind = read_trimmed(zone + "/type");
		if (!kind || (*kind != "nanokvm-cpu" && *kind != "sg2002-cpu")) continue;
		for (const auto &device : glob_dir(zone, "cdev", true)) {
			auto dkind = read_trimmed(device + "/type");
			if (!dkind || *dkind != "cpufreq-cpu0") continue;
			if (auto n = parse_int(read_trimmed(device + "/cur_state")); n && *n > 0) return true;
		}
	}
	return false;
}

bool valid(int mhz) { return mhz == 850 || mhz == 1000 || mhz == 1050 || mhz == 1075 || mhz == 1100 || mhz == 1125 || mhz == 1150; }

std::vector<int> options()
{
	auto available = read_policy("scaling_available_frequencies");
	if (!available) return {850, 1000};
	std::vector<int> out;
	std::istringstream in(*available);
	for (std::string v; in >> v;)
		if (auto khz = parse_int(v); khz && *khz % 1000 == 0 && valid(*khz / 1000)) out.push_back(*khz / 1000);
	return out;
}

State state()
{
	State s;
	s.applyAtBoot = exists(boot_flag);
	s.bootFallback = exists(boot_failed);
	if (auto n = parse_int(read_trimmed(preference_file)); n && (*n == 850 || *n == 1000 || (s.applyAtBoot && valid(*n))))
		s.target = *n;
	if (auto n = parse_int(read_trimmed(runtime_file)); n && valid(*n)) s.target = *n;
	auto driver = read_policy("scaling_driver");
	if (!driver || (*driver != "nanokvm-cpufreq" && *driver != "sg2002-cpufreq")) return s;
	auto khz = parse_int(read_policy("cpuinfo_cur_freq"));
	if (!khz || *khz <= 0) return s;
	s.running = *khz / 1000;
	s.throttled = thermal_limited();
	s.supported = true;
	s.options = options();
	return s;
}

bool write_sys(const std::string &path, const std::string &value)
{
	int fd = ::open(path.c_str(), O_WRONLY | O_TRUNC | O_CLOEXEC);
	if (fd < 0) return false;
	bool ok = ::write(fd, value.data(), value.size()) == static_cast<ssize_t>(value.size());
	return ::close(fd) == 0 && ok;
}

// "" on success, else the error.
std::string set_runtime(int mhz)
{
	auto opts = options();
	if (std::find(opts.begin(), opts.end(), mhz) == opts.end()) return "unsupported CPU frequency";
	std::string minimum = "850000";
	if (auto v = read_policy("cpuinfo_min_freq"); v && *v == "600000") minimum = *v;
	int ceiling = std::max(1000, mhz);
	std::pair<const char *, std::string> settings[] = {{"scaling_governor", "userspace"},
	                                                   {"scaling_min_freq", minimum},
	                                                   {"scaling_max_freq", std::to_string(ceiling * 1000)},
	                                                   {"scaling_setspeed", std::to_string(mhz * 1000)},
	                                                   {"scaling_max_freq", std::to_string(mhz * 1000)}};
	for (const auto &[name, value] : settings) {
		if (!write_sys(std::string(policy) + "/" + name, value)) return std::string(name) + ": write failed";
		std::string n = name;
		if (n == "scaling_min_freq" || n == "scaling_max_freq") {
			auto deadline = std::chrono::steady_clock::now() + 2s;
			for (;;) {
				auto actual = read_policy(n);
				if (!actual) return n + ": read failed";
				if (*actual == value || (n == "scaling_max_freq" && *actual == "850000" && thermal_limited())) break;
				if (std::chrono::steady_clock::now() > deadline) return n + " did not apply: " + *actual;
				std::this_thread::sleep_for(10ms);
			}
		}
	}
	State s = state();
	if (!s.supported || (s.running != mhz && !(s.throttled && s.running == 850)))
		return std::format("CPU clock readback is {} MHz, requested {}", s.running, mhz);
	return "";
}

// Through a temporary file, synced, renamed, and the directory synced.
bool persist_file(const std::string &path, int mhz)
{
	std::string dir = path.substr(0, path.rfind('/'));
	std::string temp = dir + "/.cpufreq-" + std::to_string(::getpid());
	int fd = ::open(temp.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
	if (fd < 0) return false;
	std::string v = std::to_string(mhz) + "\n";
	bool ok = ::write(fd, v.data(), v.size()) == static_cast<ssize_t>(v.size()) && ::fsync(fd) == 0;
	ok = ::close(fd) == 0 && ok;
	if (!ok || ::rename(temp.c_str(), path.c_str()) != 0) {
		::unlink(temp.c_str());
		return false;
	}
	int d = ::open(dir.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
	if (d < 0) return false;
	ok = ::fsync(d) == 0;
	::close(d);
	return ok;
}

std::string persist(int mhz, bool apply_at_boot)
{
	int boot = std::min(mhz, 1000);
	if (apply_at_boot) {
		boot = mhz;
		if (!persist_file(boot_flag, 1)) return "cannot save the boot setting";
	} else if (::unlink(boot_flag) != 0 && errno != ENOENT) {
		return "cannot remove the boot setting";
	}
	::unlink(boot_failed);
	if (!persist_file(preference_file, boot)) return "cannot save the CPU frequency";
	if (!persist_file(runtime_file, mhz)) return "cannot save the CPU frequency";
	return "";
}

std::string apply(int mhz, bool save, bool apply_at_boot)
{
	std::lock_guard lock(mutex);
	State before = state();
	if (!before.supported) return "qualified CPU frequency driver is unavailable";
	std::string error = set_runtime(mhz);
	if (error.empty() && save) error = persist(mhz, apply_at_boot);
	int restore = before.throttled ? before.target : before.running;
	if (!error.empty() && valid(restore))
		if (auto e = set_runtime(restore); !e.empty()) error += "\nrestore CPU clock: " + e;
	return error;
}

void settle_boot_pending(std::chrono::steady_clock::duration delay)
{
	std::thread([delay] {
		if (delay > std::chrono::steady_clock::duration::zero()) std::this_thread::sleep_for(delay);
		::unlink(boot_pending);
	}).detach();
}

std::string state_json(const State &s)
{
	std::string out;
	(void)glz::write_json(s, out);
	return out;
}

} // namespace

void apply_saved_cpu_frequency()
{
	State s = state();
	if (!s.supported) return;
	bool first_start = !exists(runtime_file);
	if (first_start && s.target > 1000) {
		if (exists(boot_pending)) {
			log::error("previous start at {} MHz did not settle; starting at 1000 MHz and turning off overclock at boot", s.target);
			bool ok = (::unlink(boot_flag) == 0 || errno == ENOENT) & persist_file(preference_file, 1000) &
			          persist_file(boot_failed, s.target) & (::unlink(boot_pending) == 0 || errno == ENOENT);
			if (!ok) log::error("record CPU frequency boot fallback failed");
			s.target = 1000;
		} else if (!persist_file(boot_pending, s.target)) {
			log::error("mark overclocked start, staying at 1000 MHz");
			s.target = 1000;
		} else {
			settle_boot_pending(boot_settle);
		}
	} else if (!first_start) {
		struct stat st{};
		if (::stat(boot_pending, &st) == 0) {
			auto age = std::chrono::system_clock::now() - std::chrono::system_clock::from_time_t(st.st_mtime);
			settle_boot_pending(std::chrono::duration_cast<std::chrono::steady_clock::duration>(boot_settle - age));
		}
	}
	if (auto e = apply(s.target, false, false); !e.empty()) {
		log::error("apply saved CPU frequency: {}", e);
		return;
	}
	if (first_start && !persist_file(runtime_file, s.target)) log::error("record applied CPU frequency failed");
}

void add_cpufreq_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/cpu-frequency", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::lock_guard lock(mutex);
			api::send(out, 0, "success", state_json(state()));
		});
	}, admin));
	router.add("POST", "/api/vm/cpu-frequency", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<long long> target;
		std::optional<bool> at_boot;
		if (params) {
			target = params->integer("target", ok);
			at_boot = params->boolean("applyAtBoot", ok);
		}
		if (!params || !ok || !valid(static_cast<int>(target.value_or(0)))) return api::err(res, -1, "invalid CPU frequency");
		int mhz = static_cast<int>(*target);
		bool apply_at_boot = at_boot.value_or(exists(boot_flag));
		http::defer(req, res, [mhz, apply_at_boot](http::Response &out) {
			if (auto e = apply(mhz, true, apply_at_boot); !e.empty()) return api::err(out, -2, e);
			api::ok(out);
		});
	}, admin));
}

} // namespace nkos::system
