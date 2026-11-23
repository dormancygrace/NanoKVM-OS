#include "video/hdmi.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "video/vision.hpp"

#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <fstream>
#include <map>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::video::hdmi {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

constexpr const char *disable_file = "/etc/kvm/hdmi_disable";
constexpr const char *idle_timeout_file = "/etc/kvm/hdmi_idle_timeout";
constexpr int max_idle_timeout_minutes = 7 * 24 * 60;
constexpr auto warmup = 1s;
// Without viewers the outputs park after this, so a page reload does not park and resume them; before the first
// demand of this run there is no page to reload: at once.
constexpr auto park_delay = 20s;
constexpr auto first_park_delay = 0s;
// Disabled pools can stay busy until the last VPSS job returns its frame.
constexpr auto reclaim_delay = 1s;

std::map<std::string, int> viewers;
int viewer_total = 0;
int leases = 0;
Clock::time_point capture_ready_at{};
bool had_demand = false, parked = false, stopped_for_idle = false;
std::uint64_t generation = 0;  // timers of an older generation do nothing
bool park_timer = false, idle_timer = false;

bool has_demand() { return viewer_total > 0 || leases > 0; }

int idle_timeout_minutes()
{
	std::ifstream in(idle_timeout_file);
	if (!in) return 0;
	std::string text;
	std::getline(in, text);
	char *end = nullptr;
	long v = std::strtol(text.c_str(), &end, 10);
	while (end && (*end == ' ' || *end == '\n' || *end == '\r' || *end == '\t')) end++;
	if (text.empty() || (end && *end) || v < 0 || v > max_idle_timeout_minutes) {
		log::error("invalid hdmi idle timeout");
		return 0;
	}
	return static_cast<int>(v);
}

void park();

void schedule_park()
{
	if (has_demand() || disabled() || stopped_for_idle) return;
	auto delay = had_demand ? park_delay : first_park_delay;
	if (parked) {
		if (!vision::capture_reclaim_pending()) return;
		delay = reclaim_delay;
	}
	park_timer = true;
	loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(delay), [g = generation] {
		if (g != generation) return;
		park_timer = false;
		if (has_demand() || disabled() || stopped_for_idle) return;
		park();
	});
}

void unpark();

void park()
{
	vision::set_capture_parked(true, [](bool ok) {
		if (!ok) {
			if (parked) schedule_park();
			else log::info("video capture not parked; it keeps converting frames without viewers");
			return;
		}
		if (!parked) {
			parked = true;
			log::debug("parked video capture without viewers");
		}
		// A viewer that came while it parked: the device thread resumes the outputs after this.
		if (has_demand()) return unpark();
		schedule_park();
	});
}

// A frame read resumes parked outputs by itself; resuming here first keeps the first read of a viewer from paying.
void unpark()
{
	if (!parked) return;
	parked = false;
	vision::set_capture_parked(false, [](bool ok) {
		if (!ok) log::warn("failed to resume parked video capture; the next read retries");
	});
}

// Ready a warm-up after the capture is on; until then the estimate covers turning it on (about 0.3 s).
void mark_warming()
{
	capture_ready_at = Clock::now() + warmup + 500ms;
}

std::function<void()> warmed(std::function<void()> done)
{
	return [done = std::move(done)] {
		if (capture_ready_at != Clock::time_point{}) capture_ready_at = Clock::now() + warmup;
		if (done) done();
	};
}

void resume_capture()
{
	mark_warming();
	vision::set_hdmi(true, warmed({}));
	log::debug("resumed hdmi capture after viewer connected");
}

void schedule_idle()
{
	if (has_demand() || stopped_for_idle || disabled()) return;
	int minutes = idle_timeout_minutes();
	if (minutes == 0) return;
	idle_timer = true;
	loop::after(std::chrono::minutes(minutes), [g = generation, minutes] {
		if (g != generation) return;
		idle_timer = false;
		if (has_demand() || disabled()) return;
		vision::set_hdmi(false);
		capture_ready_at = {};
		stopped_for_idle = true;
		log::debug("disabled hdmi capture after {} minutes without viewers", minutes);
	});
}

void reconcile()
{
	generation++;
	park_timer = idle_timer = false;
	if (has_demand()) {
		had_demand = true;
		unpark();
		if (stopped_for_idle && !disabled()) resume_capture();
		stopped_for_idle = false;
		return;
	}
	schedule_park();
	schedule_idle();
}

void enable_capture(std::function<void()> done = {})
{
	mark_warming();
	vision::set_hdmi(true, warmed(std::move(done)));
	stopped_for_idle = false;
	generation++;
	park_timer = idle_timer = false;
	schedule_park();
	schedule_idle();
}

void disable_capture(std::function<void()> done = {})
{
	vision::set_hdmi(false, std::move(done));
	capture_ready_at = {};
	stopped_for_idle = false;
	generation++;
	park_timer = idle_timer = false;
}

struct HdmiState {
	int viewerCount = 0;
	bool enabled = false;
	bool signal = false;
	int idleTimeout = 0;
};

} // namespace

bool disabled()
{
	struct stat st{};
	if (::stat(disable_file, &st) == 0) return true;
	if (errno != ENOENT) log::error("failed to check hdmi disable file: {}", std::strerror(errno));
	return false;
}

void start()
{
	if (disabled()) disable_capture();
	else enable_capture();
	set_viewers("legacy", 0);
}

void set_viewers(const std::string &source, int count)
{
	if (count < 0) count = 0;
	auto &current = viewers[source];
	if (current == count) return;
	current = count;
	viewer_total = 0;
	for (auto &[name, n] : viewers) viewer_total += n;
	reconcile();
}

int viewer_count() { return viewer_total; }

Lease::Lease()
{
	leases++;
	reconcile();
	ready_at_ = capture_ready_at;
}

Lease::~Lease() { release(); }

void Lease::release()
{
	if (!held_) return;
	held_ = false;
	if (leases > 0) leases--;
	reconcile();
}

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/hdmi", auth::guarded([](http::Request &, http::Response &res) {
		HdmiState s;
		s.enabled = !disabled();
		s.viewerCount = viewer_total;
		s.signal = s.enabled && vision::hdmi_signal_active();
		s.idleTimeout = idle_timeout_minutes();
		api::ok(res, s);
	}));
	// Any user may restart capture, but never against an administrator's decision to keep it off.
	router.add("POST", "/api/vm/hdmi/reset", auth::guarded([](http::Request &req, http::Response &res) {
		if (disabled()) return api::err(res, -2, "HDMI capture is disabled");
		res.pending = true;
		disable_capture([id = req.id] {
			loop::after(1s, [id] {
				auto answer = [id] {
					http::Response done;
					api::ok(done);
					http::complete(id, std::move(done));
				};
				if (disabled()) return answer();
				enable_capture(answer);
			});
		});
	}));
	router.add("POST", "/api/vm/hdmi/enable", auth::guarded([](http::Request &, http::Response &res) {
		if (::unlink(disable_file) != 0) log::error("failed to remove hdmi disable file: {}", std::strerror(errno));
		enable_capture();
		api::ok(res);
	}, {{"admin"}}));
	router.add("POST", "/api/vm/hdmi/disable", auth::guarded([](http::Request &, http::Response &res) {
		int fd = ::open(disable_file, O_CREAT | O_RDONLY | O_CLOEXEC, 0644);
		if (fd < 0) log::error("failed to create hdmi disable file: {}", std::strerror(errno));
		else ::close(fd);
		disable_capture();
		api::ok(res);
	}, {{"admin"}}));
	router.add("POST", "/api/vm/hdmi/timeout", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<long long> minutes;
		if (params) {
			minutes = params->integer("minutes", ok);
			if (!minutes && ok) minutes = params->integer("Minutes", ok);
		}
		if (!params || !ok || minutes.value_or(0) < 0 || minutes.value_or(0) > max_idle_timeout_minutes)
			return api::err(res, -1, "invalid arguments");
		int m = static_cast<int>(minutes.value_or(0));
		std::ofstream(idle_timeout_file, std::ios::trunc) << m;
		generation++;
		park_timer = idle_timer = false;
		if (stopped_for_idle && !disabled()) {
			resume_capture();
			stopped_for_idle = false;
		}
		if (!has_demand() && !stopped_for_idle) schedule_idle();
		schedule_park();
		api::ok(res);
	}, {{"admin"}}));
}

} // namespace nkos::video::hdmi
