#include "video/screen_watch.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "input/attention.hpp"
#include "notify/gostd.hpp"
#include "notify/notify.hpp"
#include "video/hdmi.hpp"
#include "video/screen_text.hpp"
#include "video/screen_watch_core.hpp"
#include "video/videogate.hpp"
#include "video/vision.hpp"

#include <format>
#include <sys/stat.h>

namespace nkos::video::screen_watch {

namespace {

using namespace std::chrono_literals;

const std::string helper = "/usr/lib/nanokvm-tpu/bin/nanokvm-screen";
const std::string model = "/usr/lib/nanokvm-tpu/models/screen.cvimodel";

constexpr auto planes_limit = 5s;
constexpr auto classify_limit = 20s;

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

struct Picture {
	Planes planes;
	int width = 0, height = 0;
};

// The last failure logged: a failure is logged once, not every look.
std::string planes_failure;

// screenPlanes: the screen at the input's size (the native read takes no other) while capture runs; nullopt without a
// signal or capture, and for an input over Full HD, which VPSS cannot hand out as planes.
struct PlanesRead : std::enable_shared_from_this<PlanesRead> {
	std::function<void(std::optional<Picture>)> done;
	std::unique_ptr<hdmi::BackgroundRead> read;
	Clock::time_point deadline;
	int attempt = 0;
	std::string error;

	void start()
	{
		deadline = Clock::now() + planes_limit;
		// Resume idle capture before checking its signal: the watch may have been enabled after capture stopped. This
		// read does not extend parking.
		read = hdmi::BackgroundRead::acquire();
		if (!read) return finish(std::nullopt);
		auto wait = read->ready_at() - Clock::now();
		if (wait <= Clock::duration::zero()) return check_signal();
		if (Clock::now() + wait >= deadline) return finish(std::nullopt);
		loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(wait) + 1ms,
		            [self = shared_from_this()] { self->check_signal(); });
	}

	// notifyHDMISignal: switched off or stopped for idling, capture reads no signal whatever the host sends.
	void check_signal()
	{
		if (hdmi::disabled() || hdmi::idle_stopped() || !vision::hdmi_signal_active()) return finish(std::nullopt);
		try_read();
	}

	void try_read()
	{
		int width = 0, height = 0;
		if (!screen_text::input_size(width, height, error)) return failed();
		vision::read_bgr_planar(static_cast<std::uint16_t>(width), static_cast<std::uint16_t>(height),
		                        [self = shared_from_this(), width, height](int r, std::vector<unsigned char> planes) {
			                        self->read_done(r, std::move(planes), width, height);
		                        });
	}

	void read_done(int r, Planes planes, int width, int height)
	{
		if (r == 0) {
			planes_failure.clear();
			return finish(Picture{std::move(planes), width, height});
		}
		error = vision::planar_error(r);
		// Only a frame late or a size being detected is worth another try.
		if (r != -3 && r != -11 && r != -13) return failed();
		if (Clock::now() + 300ms >= deadline) return failed();
		loop::after(300ms, [self = shared_from_this()] {
			if (++self->attempt >= 3) return self->failed();
			self->try_read();
		});
	}

	void failed()
	{
		if (error != planes_failure) {
			planes_failure = error;
			log::warn("screen watch: no picture of the screen: {}", error);
		}
		finish(std::nullopt);
	}

	void finish(std::optional<Picture> picture)
	{
		read.reset();
		auto d = std::move(done);
		d(std::move(picture));
	}
};

void screen_planes(std::function<void(std::optional<Picture>)> done)
{
	auto r = std::make_shared<PlanesRead>();
	r->done = std::move(done);
	r->start();
}

void offload(std::function<void()> work, std::function<void()> then)
{
	loop::blocking([work = std::move(work), then = std::move(then)]() mutable {
		work();
		loop::post(std::move(then));
	});
}

void thumb(std::function<void(std::optional<Planes>)> done)
{
	screen_planes([done = std::move(done)](std::optional<Picture> picture) mutable {
		if (!picture) return done(std::nullopt);
		auto out = std::make_shared<Planes>();
		auto p = std::make_shared<Picture>(std::move(*picture));
		offload([out, p] { *out = shrink_planes(p->planes, p->width, p->height, thumb_width, thumb_height); },
		        [out, done = std::move(done)] { done(std::move(*out)); });
	});
}

// screenShot: a screenshot only while it still matches the verdict's frame; a later desktop must not be sent as a
// crash.
void shot(const Planes &judged, std::function<void(std::optional<std::string>)> done)
{
	screen_planes([judged, done = std::move(done)](std::optional<Picture> picture) mutable {
		if (!picture) return done(std::nullopt);
		auto out = std::make_shared<std::optional<std::string>>();
		auto p = std::make_shared<Picture>(std::move(*picture));
		offload(
			[out, p, judged = std::move(judged)] {
				auto small = shrink_planes(p->planes, p->width, p->height, thumb_width, thumb_height);
				if (mean_difference(small, judged) > change_level) return;
				auto jpeg = screen_jpeg(p->planes, p->width, p->height);
				if (!jpeg.empty()) *out = std::move(jpeg);
			},
			[out, done = std::move(done)] { done(std::move(*out)); });
	});
}

// classifyScreen: the helper reads "P width height\n" and the planes, and writes its verdict as JSON.
void classify(const Planes &planes, std::function<void(std::optional<Verdict>, std::string)> done)
{
	// The TPU takes one model at a time here: never beside an OCR read.
	if (!screen_text::tpu_try_lock()) return done(std::nullopt, "the screen is being read");
	std::string input = std::format("P {} {}\n", thumb_width, thumb_height);
	input.append(reinterpret_cast<const char *>(planes.data()), planes.size());
	loop::blocking([input = std::move(input), done = std::move(done)]() mutable {
		auto deadline = Clock::now() + classify_limit;
		auto remaining = [&] {
			return std::max(1ms, std::chrono::duration_cast<std::chrono::milliseconds>(deadline - Clock::now()));
		};
		std::optional<Verdict> verdict;
		std::string error = screen_text::load_tpu_driver(remaining());
		if (error.empty()) {
			process::Options o;
			o.timeout = remaining();
			o.input = std::move(input);
			auto r = process::run({helper, model}, o);
			if (!r.ok()) {
				std::string why = r.timed_out ? "signal: killed"
				                  : !r.error.empty() ? r.error
				                  : r.signal         ? std::format("signal {}", r.signal)
				                                     : std::format("exit status {}", r.exit_code);
				error = std::format("{}: {}", why, notify::gostd::trim_space(r.output));
			} else {
				verdict = parse_verdict(r.output, error);
			}
		}
		loop::post([verdict = std::move(verdict), error = std::move(error), done = std::move(done)]() mutable {
			screen_text::tpu_unlock();
			done(std::move(verdict), std::move(error));
		});
	});
}

// readScreenLines: the OCR of the whole screen with the frame it read; cancelled when video holds the gate.
void read(const std::shared_ptr<videogate::Work> &work, std::function<void(ReadOutcome)> done)
{
	screen_text::Request request;
	request.keep_planes = true;
	request.cancel = work->cancel_flag();
	screen_text::read(std::move(request), [done = std::move(done)](screen_text::Reply reply) mutable {
		ReadOutcome out;
		out.code = reply.code;
		out.message = std::move(reply.message);
		if (reply.code == 0) {
			out.text = screen_text::lines(reply.boxes);
			out.planes = std::move(reply.planes);
			out.width = reply.width;
			out.height = reply.height;
		}
		done(std::move(out));
	});
}

// watchingViewers: the video viewers when somebody is actually looking at them, else 0: a crash then goes to the
// notifications.
int watching_viewers()
{
	int viewers = hdmi::viewer_count();
	if (viewers > 0 && input::attention::watching(Clock::now(), attention_window)) return viewers;
	return 0;
}

Watch *watch = nullptr;
// The ticks of Go's ticker: every interval from the start; one tick that came during a look is kept and starts the
// next look at once, others are dropped.
Clock::time_point first_tick;

Clock::time_point tick_after(Clock::time_point t)
{
	auto n = (t - first_tick) / interval + 1;
	return first_tick + n * interval;
}

void tick();

void looked(Clock::time_point started)
{
	auto now = Clock::now();
	auto next = tick_after(started);
	if (next <= now) return loop::post(tick);
	loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(next - now) + 1ms, tick);
}

void tick()
{
	auto started = Clock::now();
	watch->step([started] { looked(started); });
}

} // namespace

bool available() { return screen_text::available() && exists(helper) && exists(model); }

void start()
{
	if (watch) return;
	hdmi::set_keep_capture([] { return notify::wants(notify::kind_screen) && available(); });
	Hooks hooks;
	hooks.wants = notify::wants;
	hooks.emit = notify::emit;
	hooks.available = available;
	hooks.thumb = thumb;
	hooks.classify = classify;
	hooks.viewers = watching_viewers;
	hooks.begin = videogate::begin;
	hooks.shot = shot;
	hooks.read = read;
	hooks.now = Clock::now;
	hooks.offload = offload;
	watch = new Watch(std::move(hooks));
	first_tick = Clock::now();
	loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(interval), tick);
}

} // namespace nkos::video::screen_watch
