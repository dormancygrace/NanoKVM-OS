#include "video/screen_watch_core.hpp"

#include "common/image.hpp"
#include "common/log.hpp"
#include "notify/gostd.hpp"

#include <glaze/glaze.hpp>

#include <algorithm>
#include <cstdint>
#include <format>
#include <map>

namespace nkos::video::screen_watch {

struct VerdictJson {
	std::string cls;
	double score = 0;
	std::map<std::string, double> scores;
	int ms = 0;
};

} // namespace nkos::video::screen_watch

template <>
struct glz::meta<nkos::video::screen_watch::VerdictJson> {
	using T = nkos::video::screen_watch::VerdictJson;
	static constexpr auto value = object("class", &T::cls, "score", &T::score, "scores", &T::scores, "ms", &T::ms);
};

namespace nkos::video::screen_watch {

namespace {

namespace gostd = notify::gostd;

std::vector<char32_t> runes(std::string_view s)
{
	std::vector<char32_t> out;
	out.reserve(s.size());
	for (std::size_t i = 0; i < s.size();) {
		auto [r, n] = gostd::decode_rune(s, i);
		out.push_back(r);
		i += n;
	}
	return out;
}

// RE2's \b: ASCII word characters on one side only.
bool word(char32_t r)
{
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_';
}
bool boundary(const std::vector<char32_t> &t, std::size_t i)
{
	bool before = i > 0 && word(t[i - 1]);
	bool after = i < t.size() && word(t[i]);
	return before != after;
}

// (?i): a letter matches either case, and the runes that fold to it (U+212A KELVIN SIGN to k, U+017F LONG S to s).
bool fold_equal(char32_t r, char c)
{
	if (c >= 'a' && c <= 'z') {
		return r == char32_t(c) || r == char32_t(c - 'a' + 'A') || (c == 'k' && r == 0x212A) || (c == 's' && r == 0x17F);
	}
	return r == char32_t(static_cast<unsigned char>(c));
}

// [A-Z] and [A-Z0-9] under (?i).
bool letter(char32_t r) { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == 0x212A || r == 0x17F; }
bool letter_or_digit(char32_t r) { return letter(r) || (r >= '0' && r <= '9'); }

struct Literal {
	std::string_view text;  // lower case
	bool word_start = false, word_end = false;
};

bool literal_at(const std::vector<char32_t> &t, std::size_t i, const Literal &l)
{
	if (l.word_start && !boundary(t, i)) return false;
	std::size_t j = i;
	for (char c : l.text) {
		if (j >= t.size() || !fold_equal(t[j], c)) return false;
		j++;
	}
	return !l.word_end || boundary(t, j);
}

// \b[A-Z]{2,}(_[A-Z0-9]{2,}){2,}\b at i: a stop code such as CRITICAL_PROCESS_DIED.
bool stop_code_at(const std::vector<char32_t> &t, std::size_t i)
{
	if (!boundary(t, i)) return false;
	std::size_t j = i;
	while (j < t.size() && letter(t[j])) j++;
	if (j - i < 2) return false;
	int groups = 0;
	while (j < t.size() && t[j] == '_') {
		std::size_t start = ++j;
		while (j < t.size() && letter_or_digit(t[j])) j++;
		if (j - start < 2) return false;
		if (++groups >= 2)
			for (std::size_t end = start + 2; end <= j; end++)
				if (boundary(t, end)) return true;
	}
	return false;
}

bool any_literal(std::string_view text, std::initializer_list<Literal> literals, bool stop_code)
{
	auto t = runes(text);
	for (std::size_t i = 0; i <= t.size(); i++) {
		for (const auto &l : literals)
			if (literal_at(t, i, l)) return true;
		if (stop_code && stop_code_at(t, i)) return true;
	}
	return false;
}

std::vector<std::string_view> split_lines(std::string_view s)
{
	std::vector<std::string_view> out;
	for (std::size_t start = 0;;) {
		auto nl = s.find('\n', start);
		if (nl == std::string_view::npos) {
			out.push_back(s.substr(start));
			return out;
		}
		out.push_back(s.substr(start, nl - start));
		start = nl + 1;
	}
}

std::string format_score(double score) { return std::format("{:.2f}", score); }

} // namespace

std::optional<Verdict> parse_verdict(std::string_view output, std::string &error)
{
	std::string line = gostd::trim_space(output);
	for (auto part : split_lines(output)) {
		auto trimmed = gostd::trim_space(part);
		if (trimmed.starts_with("{\"class\"")) {
			line = std::move(trimmed);
			break;
		}
	}
	VerdictJson v;
	if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(v, line)) {
		error = "classifier output: " + glz::format_error(e, line);
		return std::nullopt;
	}
	return Verdict{v.cls, v.score, v.ms};
}

bool bsod_text(std::string_view text)
{
	return any_literal(text,
	                   {{"stop code"}, {"stopcode"}, {"ran into a problem"}, {"shut down to prevent damage"},
	                    {"*** stop"}, {"***stop"}, {"windows.com/stopcode"}},
	                   true);
}

bool panic_text(std::string_view text)
{
	return any_literal(text,
	                   {{"kernel panic"}, {"not syncing"}, {"oops", true, true}, {"bug: ", true, false},
	                    {"general protection fault"}, {"call trace"}, {"soft lockup"}, {"hard lockup"},
	                    {"unable to handle kernel"}, {"unable to handle page"}},
	                   false);
}

std::string crash_lines(std::string_view text, bool (*crash)(std::string_view))
{
	std::string trimmed = gostd::trim_space(text);
	auto lines = split_lines(trimmed);
	std::size_t from = 0;
	for (std::size_t i = 0; i < lines.size(); i++) {
		if (crash(lines[i])) {
			from = i >= 2 ? i - 2 : 0;
			break;
		}
	}
	std::string out;
	for (std::size_t i = from; i < lines.size() && i < from + 20; i++) {
		if (i > from) out += "\n";
		out += lines[i];
	}
	return out;
}

double mean_difference(const Planes &a, const Planes &b)
{
	if (a.size() != b.size() || a.empty()) return 255;
	std::int64_t sum = 0;
	for (std::size_t i = 0; i < a.size(); i++) {
		int d = int(a[i]) - int(b[i]);
		sum += d < 0 ? -d : d;
	}
	return double(sum) / double(a.size());
}

Planes shrink_planes(const Planes &planes, int width, int height, int to_w, int to_h)
{
	Planes out(std::size_t(to_w) * std::size_t(to_h) * 3);
	std::size_t n = std::size_t(width) * std::size_t(height);
	int step = width >= 4 * to_w && height >= 4 * to_h ? 2 : 1;
	for (int c = 0; c < 3; c++) {
		const unsigned char *src = planes.data() + std::size_t(c) * n;
		unsigned char *dst = out.data() + std::size_t(c) * std::size_t(to_w) * std::size_t(to_h);
		for (int oy = 0; oy < to_h; oy++) {
			int y0 = oy * height / to_h, y1 = std::max(y0 + 1, (oy + 1) * height / to_h);
			for (int ox = 0; ox < to_w; ox++) {
				int x0 = ox * width / to_w, x1 = std::max(x0 + 1, (ox + 1) * width / to_w);
				long sum = 0, count = 0;
				for (int y = y0; y < y1; y += step) {
					const unsigned char *row = src + std::size_t(y) * std::size_t(width);
					for (int x = x0; x < x1; x += step) {
						sum += row[x];
						count++;
					}
				}
				dst[std::size_t(oy) * std::size_t(to_w) + std::size_t(ox)] = static_cast<unsigned char>(sum / count);
			}
		}
	}
	return out;
}

std::string screen_jpeg(const Planes &planes, int width, int height)
{
	if (width <= 0 || planes.size() != std::size_t(width) * std::size_t(height) * 3) return "";
	const Planes *source = &planes;
	Planes small;
	if (width > shot_width) {
		int to_h = height * shot_width / width;
		small = shrink_planes(planes, width, height, shot_width, to_h);
		source = &small;
		width = shot_width;
		height = to_h;
	}
	std::size_t n = std::size_t(width) * std::size_t(height);
	if (n == 0) return "";
	std::vector<unsigned char> rgb(n * 3);
	for (std::size_t i = 0; i < n; i++) {
		rgb[i * 3] = (*source)[2 * n + i];
		rgb[i * 3 + 1] = (*source)[n + i];
		rgb[i * 3 + 2] = (*source)[i];
	}
	return image::encode_jpeg(rgb.data(), width, height, width * 3, image::Pixels::rgb, 80).value_or("");
}

void Watch::reset()
{
	s_.last.clear();
	s_.pending = s_.reported = s_.deferred = false;
}

void Watch::step(std::function<void()> done)
{
	if (!h_.wants(notify::kind_screen) || !h_.available()) {
		reset();
		return done();
	}
	h_.thumb([this, done = std::move(done)](std::optional<Planes> planes) mutable {
		if (!planes) {
			reset();
			return done();
		}
		if (s_.last.empty() || mean_difference(s_.last, *planes) > change_level) {
			// Changed (or the first look): judge it once it holds still.
			s_.last = std::move(*planes);
			s_.pending = true;
			s_.deferred = false;
			return done();
		}
		if (!s_.pending) {
			// A crash left while somebody watched is judged again once nobody does.
			if (!s_.deferred || h_.viewers() > 0) return done();
			// Pending until a verdict: a classifier that fails now tries again.
			s_.deferred = false;
			s_.pending = true;
		}
		judge(std::move(*planes), std::move(done));
	});
}

void Watch::judge(Planes planes, std::function<void()> done)
{
	auto thumb = std::make_shared<Planes>(std::move(planes));
	h_.classify(*thumb, [this, thumb, done = std::move(done)](std::optional<Verdict> verdict, std::string error) mutable {
		if (!verdict) {
			// Tried again at the next look (the TPU may be reading the screen).
			log::warn("screen watch: {}", error);
			return done();
		}
		s_.pending = false;
		bool alarm = (verdict->cls == "bsod" || verdict->cls == "console") && verdict->score >= alarm_score;
		if (!s_.judged || alarm) {
			log::info("screen watch: {} {} ({} ms)", verdict->cls, format_score(verdict->score), verdict->ms);
			s_.judged = true;
		} else {
			log::debug("screen watch: {} {} ({} ms)", verdict->cls, format_score(verdict->score), verdict->ms);
		}
		if (!alarm) {
			s_.reported = false;
			return done();
		}
		if (s_.reported) return done();
		check(std::move(*verdict), std::move(*thumb), std::move(done));
	});
}

// Reads a suspect screen, the thumbnail judged, and reports a crash it names. Somebody who watches the video sees it:
// then nothing is read or reported, and a viewer who comes during the read cancels it. A read that failed is tried
// again while the screen stays (at once when the TPU was busy, after the gap otherwise).
void Watch::check(Verdict verdict, Planes judged, std::function<void()> done)
{
	if (h_.viewers() > 0) {
		s_.deferred = true;
		return done();
	}
	auto now = h_.now();
	if (s_.read_at != Clock::time_point{} && now - s_.read_at < read_gap) {
		// Read too recently: judge it again when the gap is over.
		s_.pending = true;
		return done();
	}
	auto work = h_.begin();
	if (!work) {
		// Video holds the TPU, for a page nobody looks at. The OCR must not take the video's memory, so a sure stop
		// screen goes out unread with its picture; anything else waits until the video stops.
		if (verdict.cls == "bsod" && verdict.score >= unread_bsod_score) {
			h_.shot(judged, [this, done = std::move(done)](std::optional<std::string> shot) mutable {
				if (shot && h_.viewers() == 0) {
					s_.reported = true;
					notify::Event e;
					e.kind = std::string(notify::kind_screen);
					e.severity = notify::Severity::critical;
					e.message = "screen.bsod";
					e.details = "The text was not read: video is streaming to a page nobody is looking at.";
					e.image = std::move(*shot);
					h_.emit(std::move(e));
					return done();
				}
				s_.deferred = true;
				done();
			});
			return;
		}
		s_.deferred = true;
		return done();
	}
	auto previous = s_.read_at;
	s_.read_at = now;
	h_.read(work, [this, work, verdict = std::move(verdict), judged = std::move(judged), previous,
	               done = std::move(done)](ReadOutcome got) mutable {
		bool cancelled = work->cancelled();
		work->end();
		read_done(verdict, std::move(judged), previous, cancelled, std::move(got), std::move(done));
	});
}

void Watch::read_done(const Verdict &verdict, Planes judged, Clock::time_point previous, bool cancelled,
                      ReadOutcome got, std::function<void()> done)
{
	if (cancelled) {
		// A viewer came; the read did not count.
		s_.read_at = previous;
		s_.deferred = true;
		log::info("screen watch: OCR gave way to a viewer");
		return done();
	}
	if (got.code != 0) {
		if (got.code == -2) s_.read_at = previous;  // somebody else was reading: no read happened
		log::warn("screen watch: OCR: {}", got.message);
		s_.pending = true;
		return done();
	}
	// The text and the picture sent are those of the frame the OCR read; a frame that is no longer the screen judged
	// is judged anew. The comparison, the crash text and the JPEG run off the watch's thread.
	struct Result {
		bool changed = false;
		Planes thumb;
		std::string message, details, image;
	};
	auto result = std::make_shared<Result>();
	bool bsod = verdict.cls == "bsod";
	h_.offload(
		[result, bsod, judged = std::move(judged), got = std::move(got)] {
			if (!got.planes.empty()) {
				result->thumb = shrink_planes(got.planes, got.width, got.height, thumb_width, thumb_height);
				if (mean_difference(result->thumb, judged) > change_level) {
					result->changed = true;
					return;
				}
			}
			if (bsod_text(got.text) && bsod) {
				result->message = "screen.bsod";
				result->details = crash_lines(got.text, bsod_text);
			} else if (panic_text(got.text)) {
				result->message = "screen.panic";
				result->details = crash_lines(got.text, panic_text);
			} else {
				return;
			}
			result->image = screen_jpeg(got.planes, got.width, got.height);
		},
		[this, result, done = std::move(done)] {
			if (result->changed) {
				log::info("screen watch: the screen changed before it was read");
				s_.last = std::move(result->thumb);
				s_.pending = true;
				return done();
			}
			if (!result->message.empty()) {
				s_.reported = true;
				notify::Event e;
				e.kind = std::string(notify::kind_screen);
				e.severity = notify::Severity::critical;
				e.message = std::move(result->message);
				e.details = std::move(result->details);
				e.image = std::move(result->image);
				h_.emit(std::move(e));
			}
			done();
		});
}

} // namespace nkos::video::screen_watch
