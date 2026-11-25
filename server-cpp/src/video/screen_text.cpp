#include "video/screen_text.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "config/config.hpp"
#include "osupdate/apkpkg.hpp"
#include "video/hdmi.hpp"
#include "video/screen.hpp"
#include "video/vision.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <charconv>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <format>
#include <memory>
#include <mutex>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::video::screen_text {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

// The TPU programs are in bin; the models, which are add-ons, in models.
const std::string root = "/usr/lib/nanokvm-tpu";
const std::string helper = root + "/bin/nanokvm-ocr";
const std::string models = root + "/models/";
const std::string detector = models + "ocr-det.cvimodel";
const std::string picture_file = "/run/nanokvm/screen-text.bgr";
const std::string off_file = "/etc/kvm/screen-text-off";
// The TPU driver is kept out at boot and loaded on first use; without its device the runtime aborts.
const std::string tpu_device = "/dev/cvi-tpu0";

constexpr auto read_limit = 90s;
// The TPU reads screens up to Full HD: larger inputs take video buffers the models need, and VPSS channel 2 takes
// at most 1920 on either side.
constexpr int max_pixels = 1920 * 1080, max_side = 1920;
// Added around a requested area, so a line the selection cuts at its edge is still detected whole.
constexpr int margin = 24;

const std::vector<std::string> languages = {"latin", "chinese", "cyrillic"};

const std::string detector_package = "nanokvm-model-ocr";
std::string language_package(const std::string &lang) { return detector_package + "-" + lang; }

constexpr glz::opts lenient{.error_on_unknown_keys = false};

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

bool regular(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode);
}

// The first language whose models are installed, or the first language when none is.
std::string default_language()
{
	for (const auto &lang : languages)
		if (exists(models + lang + "/ocr-rec.cvimodel")) return lang;
	return languages[0];
}

// The helper's arguments for a language: the recognizer's three widths are programs of one model; a surer model with
// the same dictionary reads again the lines the small one is unsure of. Empty with error set when it cannot.
std::vector<std::string> helper_args(std::string lang, std::string &error)
{
	if (lang.empty()) lang = default_language();
	auto it = std::find(languages.begin(), languages.end(), lang);
	if (it == languages.end()) {
		error = "unknown language " + gojson::quote(lang);
		return {};
	}
	// The directory name comes from the fixed list, never from the request.
	std::string dir = models + *it + "/";
	if (!exists(dir + "ocr-rec.cvimodel")) {
		error = "no models for " + lang;
		return {};
	}
	std::vector<std::string> args = {models + "ocr-det.cvimodel", dir + "ocr-rec.cvimodel:3", dir + "ocr-dict.txt"};
	if (exists(dir + "ocr-sure.cvimodel")) args.push_back(dir + "ocr-sure.cvimodel:3");
	return args;
}

bool language_installed(const std::string &lang)
{
	std::string error;
	return !helper_args(lang, error).empty();
}

// A language change waiting for the lock or holding it; the settings report whether there is one.
std::atomic<int> language_changes{0};
std::mutex language_mutex;

std::string settings_json()
{
	// Read before the models: read after them, a change ending in between would give the models from before it with
	// no change running.
	bool changing = language_changes.load() > 0;
	std::string out = std::format("{{\"enabled\":{},\"installed\":{},\"languages\":[", enabled() ? "true" : "false",
	                              installed() ? "true" : "false");
	for (std::size_t i = 0; i < languages.size(); i++) {
		if (i) out += ",";
		out += std::format("{{\"name\":{},\"installed\":{}}}", gojson::quote(languages[i]),
		                   language_installed(languages[i]) ? "true" : "false");
	}
	out += std::format("],\"changing\":{}}}", changing ? "true" : "false");
	return out;
}

// The planar picture for an input: the input itself, even sides; 0x0 over Full HD or wider or taller than 1920.
std::pair<int, int> picture_size(int width, int height)
{
	if (width * height > max_pixels || width > max_side || height > max_side) return {0, 0};
	return {width & ~1, height & ~1};
}

// ?crop=x,y,w,h, fractions (0..1) of the picture.
std::optional<std::array<double, 4>> parse_crop(std::string_view value, std::string &error)
{
	std::array<double, 4> f{};
	std::vector<std::string_view> parts;
	for (std::size_t start = 0;;) {
		auto comma = value.find(',', start);
		parts.push_back(value.substr(start, comma == std::string_view::npos ? std::string_view::npos : comma - start));
		if (comma == std::string_view::npos) break;
		start = comma + 1;
	}
	if (parts.size() != 4) {
		error = "crop is x,y,w,h";
		return std::nullopt;
	}
	for (std::size_t i = 0; i < 4; i++) {
		auto part = parts[i];
		while (!part.empty() && (part.front() == ' ' || part.front() == '\t' || part.front() == '\n' || part.front() == '\r'))
			part.remove_prefix(1);
		while (!part.empty() && (part.back() == ' ' || part.back() == '\t' || part.back() == '\n' || part.back() == '\r'))
			part.remove_suffix(1);
		// strconv.ParseFloat takes a leading plus sign; from_chars does not.
		if (part.size() > 1 && part.front() == '+' && part[1] != '+' && part[1] != '-') part.remove_prefix(1);
		double v = 0;
		auto [end, ec] = std::from_chars(part.data(), part.data() + part.size(), v);
		if (part.empty() || ec != std::errc() || end != part.data() + part.size() || std::isnan(v) || v < 0 || v > 1) {
			error = "crop values are fractions from 0 to 1";
			return std::nullopt;
		}
		f[i] = v;
	}
	if (f[2] <= 0 || f[3] <= 0) {
		error = "crop is empty";
		return std::nullopt;
	}
	return f;
}

// The area to read for a crop of a width x height picture: the fractions in pixels, the margin around them, within
// the picture, at least 32x32.
Area crop_area(const std::array<double, 4> &f, int width, int height)
{
	int x0 = static_cast<int>(std::floor(f[0] * width)) - margin;
	int y0 = static_cast<int>(std::floor(f[1] * height)) - margin;
	int x1 = static_cast<int>(std::ceil((f[0] + f[2]) * width)) + margin;
	int y1 = static_cast<int>(std::ceil((f[1] + f[3]) * height)) + margin;
	x0 = std::max(0, std::min(x0, width - 32));
	y0 = std::max(0, std::min(y0, height - 32));
	x1 = std::min(width, std::max(x1, x0 + 32));
	y1 = std::min(height, std::max(y1, y0 + 32));
	return Area{x0, y0, x1 - x0, y1 - y0};
}

std::vector<unsigned char> crop_planes(const std::vector<unsigned char> &planes, int width, int height, Area area)
{
	std::vector<unsigned char> out;
	out.reserve(std::size_t(3) * area.w * area.h);
	for (int c = 0; c < 3; c++) {
		const unsigned char *plane = planes.data() + std::size_t(c) * width * height;
		for (int y = area.y; y < area.y + area.h; y++) {
			const unsigned char *row = plane + std::size_t(y) * width + area.x;
			out.insert(out.end(), row, row + area.w);
		}
	}
	return out;
}

struct HelperBox {
	std::string text;
	double score = 0;
	int x = 0, y = 0, w = 0, h = 0;
	double slope = 0;
};

std::string trim(std::string_view s)
{
	auto space = [](char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'; };
	while (!s.empty() && space(s.front())) s.remove_prefix(1);
	while (!s.empty() && space(s.back())) s.remove_suffix(1);
	return std::string(s);
}

bool valid_json(const std::string &line)
{
	glz::generic value;
	return !glz::read_json(value, line);
}

// The helper's lines: the boxes, moved by dx, dy from the area into the picture, and the summary.
std::vector<Box> parse_output(const std::string &out, int dx, int dy, std::string &stats)
{
	std::vector<Box> boxes;
	std::size_t start = 0;
	while (start <= out.size()) {
		auto nl = out.find('\n', start);
		std::string line = trim(std::string_view(out).substr(start, nl == std::string::npos ? std::string::npos : nl - start));
		start = nl == std::string::npos ? out.size() + 1 : nl + 1;
		if (line.empty() || !valid_json(line)) continue;
		if (line.starts_with("{\"done\":")) {
			stats = line;
			continue;
		}
		HelperBox h;
		if (glz::read<lenient>(h, line)) continue;
		Box box{h.text, h.score, h.x + dx, h.y + dy, h.w, h.h, h.slope};
		// A line at the top edge read with its margin above the picture: the part inside it.
		if (box.y < 0) {
			box.h = std::max(1, box.h + box.y);
			box.y = 0;
		}
		boxes.push_back(std::move(box));
	}
	return boxes;
}

// Go's encoding/json float64.
std::string go_float(double v) { return gojson::number(v); }

bool busy = false;

struct SwitchBody {
	std::optional<bool> enabled;
};
struct LanguageBody {
	std::string language;
	std::optional<bool> installed;
};

Reply failure(int code, std::string message)
{
	Reply r;
	r.code = code;
	r.message = std::move(message);
	return r;
}

// The picture size for the current input, or the read's error: no signal, or an input over Full HD.
std::optional<Reply> input_error(int &width, int &height)
{
	auto [iw, ih] = capture_input_size();
	if (iw < 16 || ih < 16) return failure(-3, "no HDMI signal");
	std::tie(width, height) = picture_size(iw, ih);
	if (width == 0) return failure(-5, std::format("text recognition works up to 1920x1080; the input is {}x{}", iw, ih));
	return std::nullopt;
}

struct Read : std::enable_shared_from_this<Read> {
	Request request;
	std::string language;
	std::vector<std::string> args;
	std::function<void(Reply)> done;
	std::unique_ptr<hdmi::Lease> lease;
	Clock::time_point started, deadline, capture_start;
	int attempt = 0;
	int last_result = 0;
	// Set once the read is to stop: its web request's connection closed, or request.cancel was set. The loop
	// thread sets it (watch), since only that thread may ask about a request; the helper's thread only reads it.
	std::shared_ptr<std::atomic<bool>> stop = std::make_shared<std::atomic<bool>>(false);
	bool finished = false;

	// Loop thread, until the read finishes.
	void watch()
	{
		if (finished) return;
		if ((request.request_id && !http::request_open(request.request_id)) || (request.cancel && request.cancel->load())) {
			stop->store(true);
			return;
		}
		loop::after(100ms, [self = shared_from_this()] { self->watch(); });
	}

	void finish(Reply reply)
	{
		finished = true;
		lease.reset();
		busy = false;
		auto d = std::move(done);
		d(std::move(reply));
	}

	bool cancelled() const
	{
		return stop->load() || (request.cancel && request.cancel->load());
	}

	void begin()
	{
		auto wait = lease->ready_at() - Clock::now();
		if (wait > Clock::duration::zero()) {
			if (Clock::now() + wait >= deadline) return finish(failure(-3, "context deadline exceeded"));
			loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(wait) + 1ms, [self = shared_from_this()] {
				if (self->cancelled()) return self->finish(failure(-9, "the read was cancelled"));
				self->capture_start = Clock::now();
				self->try_read();
			});
			return;
		}
		capture_start = Clock::now();
		try_read();
	}

	// Six tries: VPSS gave no frame in time, a new input size was being detected, or the input changed size since it
	// was read (a capture started cold can take more than a second for its first frame).
	void try_read()
	{
		if (attempt >= 6) return picture_failed(last_result);
		attempt++;
		int width = 0, height = 0;
		if (auto e = input_error(width, height)) return finish(std::move(*e));
		vision::read_bgr_planar(static_cast<std::uint16_t>(width), static_cast<std::uint16_t>(height),
		                        [self = shared_from_this(), width, height](int r, std::vector<unsigned char> planes) {
			                        self->read_done(r, std::move(planes), width, height);
		                        });
	}

	void picture_failed(int r)
	{
		if (cancelled()) return finish(failure(-9, "the read was cancelled"));
		switch (r) {
		case -10:
			return finish(failure(-3, "no HDMI signal"));
		case -14:
			return finish(failure(-5, "text recognition works up to 1920x1080"));
		default:
			log::warn("screen text: {}", vision::planar_error(r));
			return finish(failure(-8, "the picture of the screen could not be taken"));
		}
	}

	void read_done(int r, std::vector<unsigned char> planes, int width, int height)
	{
		last_result = r;
		if (r == 0) {
			lease.reset();
			return recognize(std::move(planes), width, height);
		}
		auto pause = 100ms;
		if (r == -11 || r == -13) pause = 500ms;
		else if (r != -3) return picture_failed(r);
		if (Clock::now() + pause >= deadline) return picture_failed(r);
		loop::after(pause, [self = shared_from_this()] {
			if (self->cancelled()) return self->finish(failure(-9, "the read was cancelled"));
			self->try_read();
		});
	}

	void recognize(std::vector<unsigned char> whole, int width, int height)
	{
		auto captured = Clock::now() - capture_start;
		Area area{0, 0, width, height};
		std::vector<unsigned char> planes;
		if (request.crop) {
			area = crop_area(*request.crop, width, height);
			planes = crop_planes(whole, width, height, area);
		}
		const auto &picture = request.crop ? planes : whole;
		std::string input = std::format("P {} {}\n", area.w, area.h);
		input.append(reinterpret_cast<const char *>(picture.data()), picture.size());
		if (!request.keep_planes) whole.clear();
		loop::blocking([self = shared_from_this(), input = std::move(input), whole = std::move(whole), width, height, area,
		                captured]() mutable {
			Reply reply = self->run_helper(std::move(input), area);
			if (reply.code == 0) {
				reply.language = self->language;
				reply.width = width;
				reply.height = height;
				reply.area = area;
				reply.capture_ms = std::chrono::duration_cast<std::chrono::milliseconds>(captured).count();
				reply.total_ms = std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - self->capture_start).count();
				if (self->request.keep_planes) reply.planes = std::move(whole);
			}
			loop::post([self, reply = std::move(reply)]() mutable { self->finish(std::move(reply)); });
		});
	}

	// On a blocking thread.
	Reply run_helper(std::string input, Area area)
	{
		if (request.save_picture) {
			if (FILE *f = std::fopen(picture_file.c_str(), "wb")) {
				::fchmod(fileno(f), 0600);
				std::fwrite(input.data(), 1, input.size(), f);
				std::fclose(f);
			} else {
				log::warn("screen text: keep picture: open {}: failed", picture_file);
			}
		}
		auto remaining = [&] {
			return std::max(std::chrono::milliseconds(1),
			                std::chrono::duration_cast<std::chrono::milliseconds>(deadline - Clock::now()));
		};
		if (auto error = load_tpu_driver(remaining(), stop.get()); !error.empty()) {
			if (cancelled()) return failure(-9, "the read was cancelled");
			log::error("screen text: {}", error);
			return failure(-4, "text recognition failed");
		}
		std::vector<std::string> argv = {helper};
		argv.insert(argv.end(), args.begin(), args.end());
		process::Options o;
		o.timeout = remaining();
		o.input = std::move(input);
		// A closed connection ends the helper too, as the request context did in the Go server.
		o.cancel = stop.get();
		auto r = process::run(argv, o);
		if (!r.ok()) {
			if (cancelled()) return failure(-9, "the read was cancelled");
			log::error("screen text: {}: {}", r.timed_out ? "context deadline exceeded" : r.error.empty() ? std::format("exit status {}", r.exit_code) : r.error,
			           trim(r.output));
			return failure(-4, "text recognition failed");
		}
		Reply reply;
		reply.boxes = parse_output(r.output, area.x, area.y, reply.stats);
		return reply;
	}
};

} // namespace

const std::vector<std::string> &language_names() { return languages; }

bool enabled() { return !exists(off_file); }

bool installed()
{
	if (!regular(helper) || !exists(detector)) return false;
	for (const auto &lang : languages)
		if (language_installed(lang)) return true;
	return false;
}

bool available() { return enabled() && installed(); }

bool input_size(int &width, int &height, std::string &error)
{
	if (auto e = input_error(width, height)) {
		error = e->message;
		return false;
	}
	return true;
}

bool tpu_try_lock()
{
	if (busy) return false;
	busy = true;
	return true;
}

void tpu_unlock() { busy = false; }

std::string load_tpu_driver(std::chrono::milliseconds limit, const std::atomic<bool> *cancel)
{
	if (exists(tpu_device)) return "";
	process::Options o;
	o.timeout = std::min(std::chrono::milliseconds(10s), limit);
	o.cancel = cancel;
	auto r = process::run({"modprobe", "cv181x_tpu"}, o);
	if (r.ok()) return "";
	std::string why = r.timed_out || r.cancelled ? "signal: killed"
	                  : !r.error.empty()          ? r.error
	                  : r.signal                  ? std::format("signal {}", r.signal)
	                                              : std::format("exit status {}", r.exit_code);
	return std::format("cannot load the TPU driver: {}: {}", why, trim(r.output));
}

void read(Request request, std::function<void(Reply)> done)
{
	if (!enabled()) return done(failure(-7, "text recognition is off (Settings > AI > Text recognition)"));
	if (!installed()) return done(failure(-1, "no text recognition models are installed"));
	auto r = std::make_shared<Read>();
	r->language = request.language.empty() ? default_language() : request.language;
	std::string error;
	r->args = helper_args(r->language, error);
	if (r->args.empty()) return done(failure(-6, error));
	if (busy) return done(failure(-2, "the screen is being read"));
	int width = 0, height = 0;
	if (auto e = input_error(width, height)) return done(std::move(*e));
	busy = true;
	r->request = std::move(request);
	r->done = std::move(done);
	r->started = Clock::now();
	r->deadline = r->started + read_limit;
	r->lease = std::make_unique<hdmi::Lease>();
	r->watch();
	r->begin();
}

std::string reply_json(const Reply &reply)
{
	std::string out = std::format("{{\"language\":{},\"width\":{},\"height\":{},\"area\":{{\"x\":{},\"y\":{},\"w\":{},\"h\":{}}},\"boxes\":[",
	                              gojson::quote(reply.language), reply.width, reply.height, reply.area.x, reply.area.y,
	                              reply.area.w, reply.area.h);
	for (std::size_t i = 0; i < reply.boxes.size(); i++) {
		const auto &b = reply.boxes[i];
		if (i) out += ",";
		out += std::format("{{\"text\":{},\"score\":{},\"x\":{},\"y\":{},\"w\":{},\"h\":{},\"slope\":{}}}", gojson::quote(b.text),
		                   go_float(b.score), b.x, b.y, b.w, b.h, go_float(b.slope));
	}
	out += std::format("],\"stats\":{},\"capture_ms\":{},\"total_ms\":{}}}", reply.stats.empty() ? "null" : reply.stats,
	                   reply.capture_ms, reply.total_ms);
	return out;
}

std::string lines(const std::vector<Box> &boxes)
{
	auto centre = [](const Box &b) { return double(b.y) + double(b.h) / 2; };
	// b continues a: it starts past the middle of a, and their centres are within half the lower box's height.
	auto follows = [&](const Box &a, const Box &b) {
		return double(b.x) >= double(a.x) + double(a.w) / 2 && std::fabs(centre(a) - centre(b)) <= double(std::min(a.h, b.h)) / 2;
	};
	using Run = std::vector<Box>;
	std::vector<Run> runs;
	for (const auto &box : boxes) {
		if (!runs.empty() && follows(runs.back().back(), box)) runs.back().push_back(box);
		else runs.push_back(Run{box});
	}
	auto by_first = [&](const Run &a, const Run &b) { return centre(a[0]) < centre(b[0]); };
	std::stable_sort(runs.begin(), runs.end(), by_first);
	std::vector<Run> out;
	for (auto &run : runs) {
		bool joined = false;
		for (auto &line : out) {
			if (follows(line.back(), run[0])) {
				line.insert(line.end(), run.begin(), run.end());
				joined = true;
				break;
			}
		}
		if (!joined) {
			for (auto &line : out) {
				if (follows(run.back(), line[0])) {
					Run merged = run;
					merged.insert(merged.end(), line.begin(), line.end());
					line = std::move(merged);
					joined = true;
					break;
				}
			}
		}
		if (!joined) out.push_back(run);
	}
	std::stable_sort(out.begin(), out.end(), by_first);
	std::string text;
	for (std::size_t i = 0; i < out.size(); i++) {
		if (i) text += "\n";
		for (std::size_t j = 0; j < out[i].size(); j++) {
			if (j) text += " ";
			text += out[i][j].text;
		}
	}
	return text;
}

void add_routes(http::Router &router)
{
	const auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/screen/text", auth::guarded([](http::Request &req, http::Response &res) {
		Request r;
		r.language = req.query_value("lang");
		r.save_picture = req.query_value("save") == "1";
		r.request_id = req.id;
		if (std::string value = req.query_value("crop"); !value.empty()) {
			std::string error;
			auto f = parse_crop(value, error);
			if (!f) return api::err(res, -7, error);
			r.crop = *f;
		}
		res.pending = true;
		read(std::move(r), [id = req.id](Reply reply) {
			http::Response out;
			if (reply.code) api::err(out, reply.code, reply.message);
			else api::send(out, 0, "success", reply_json(reply));
			http::complete(id, std::move(out));
		});
	}, admin));
	router.add("GET", "/api/vm/screen/text/settings", auth::guarded([](http::Request &, http::Response &res) {
		api::send(res, 0, "success", settings_json());
	}, admin));
	router.add("POST", "/api/vm/screen/text/settings", auth::guarded([](http::Request &req, http::Response &res) {
		SwitchBody body;
		if (glz::read<lenient>(body, req.body) || !body.enabled) return api::err(res, -1, "enabled is required");
		bool ok;
		if (*body.enabled) ok = ::unlink(off_file.c_str()) == 0 || errno == ENOENT;
		else ok = config::atomic_write(off_file, "", 0644);
		if (!ok) {
			log::error("screen text: switch: cannot write {}", off_file);
			return api::err(res, -2, "the setting could not be saved");
		}
		api::send(res, 0, "success", settings_json());
	}, admin));
	router.add("POST", "/api/vm/screen/text/language", auth::guarded([](http::Request &req, http::Response &res) {
		LanguageBody body;
		if (glz::read<lenient>(body, req.body) || !body.installed ||
		    std::find(languages.begin(), languages.end(), body.language) == languages.end())
			return api::err(res, -1, "a language and installed are required");
		// apk runs for minutes: counted from before the lock until it is done, off the loop.
		language_changes++;
		http::defer(req, res, [lang = body.language, install = *body.installed](http::Response &out) {
			std::string error;
			{
				std::lock_guard lock(language_mutex);
				if (install) {
					// install_only installs nothing it is not given: the detector too.
					error = apkpkg::install_only({language_package(lang), detector_package});
				} else {
					std::vector<std::string> names = {language_package(lang)};
					bool other = std::any_of(languages.begin(), languages.end(),
					                         [&](const std::string &o) { return o != lang && language_installed(o); });
					if (!other) names.push_back(detector_package);
					error = apkpkg::remove(names);
				}
			}
			language_changes--;
			if (!error.empty()) {
				log::error("screen text: {} models: {}", lang, error);
				return api::err(out, -2, error);
			}
			api::send(out, 0, "success", settings_json());
		});
	}, admin));
}

} // namespace nkos::video::screen_text
