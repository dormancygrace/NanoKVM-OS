#pragma once

// The screen watch's logic, as the Go server's service/vm/screen_watch.go (screenWatch.step and check, the picture
// helpers and the crash texts), apart from the device: everything it needs comes through hooks that answer through a
// callback, so the server runs them off the loop (video/screen_watch.cpp) and a check can answer at once.

#include "notify/model.hpp"
#include "video/videogate.hpp"

#include <chrono>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::video::screen_watch {

using Clock = std::chrono::steady_clock;
using Planes = std::vector<unsigned char>;

// The classifier's input (tpu/models/screen/synth.py INPUT).
inline constexpr int thumb_width = 224, thumb_height = 128;
// The screenshot sent with a crash.
inline constexpr int shot_width = 960;
// A stop screen shows for tens of seconds before Windows restarts, a panic stays.
inline constexpr std::chrono::seconds interval{10};
// A thumbnail differs from the last by more than this mean per byte: the screen changed. A ticking "% complete"
// stays under it.
inline constexpr double change_level = 2.0;
// The classifier's score for a stop screen or a console to be read.
inline constexpr double alarm_score = 0.7;
// A stop screen the classifier is this sure of is reported without its text while video streams to a page nobody
// looks at: the OCR then has to give way to the video (videogate).
inline constexpr double unread_bsod_score = 0.9;
// The least time between two OCR reads: a server console that scrolls and settles again and again must not keep the
// TPU busy.
inline constexpr std::chrono::minutes read_gap{2};
// A viewer counts as watching while a page is visible and keyboard or mouse input came within this long.
inline constexpr std::chrono::minutes attention_window{5};

// The classifier's reply.
struct Verdict {
	std::string cls;
	double score = 0;
	int ms = 0;
};
// The classifier's output (stdout, or the output with its diagnostics: the line of the reply); nullopt with error.
std::optional<Verdict> parse_verdict(std::string_view output, std::string &error);

// An OCR read: code 0 with the lines and the frame read (B, G and R planes of width x height, empty when not
// kept), else the read's error code and message (-2: somebody else was reading).
struct ReadOutcome {
	int code = 0;
	std::string message;
	std::string text;
	Planes planes;
	int width = 0, height = 0;
};

struct Hooks {
	std::function<bool(std::string_view kind)> wants;
	std::function<void(notify::Event)> emit;
	std::function<bool()> available;
	// The classifier's picture; nullopt without a signal.
	std::function<void(std::function<void(std::optional<Planes>)>)> thumb;
	// nullopt with the error.
	std::function<void(const Planes &, std::function<void(std::optional<Verdict>, std::string error)>)> classify;
	std::function<int()> viewers;
	// Admits a background read; cancelled when video starts (videogate). nullptr: not admitted.
	std::function<std::shared_ptr<videogate::Work>()> begin;
	// A picture of the screen to send with a crash, when the OCR cannot run; it must still match the thumbnail judged.
	std::function<void(const Planes &judged, std::function<void(std::optional<std::string>)>)> shot;
	// The OCR of the whole screen, with the frame it read.
	std::function<void(const std::shared_ptr<videogate::Work> &, std::function<void(ReadOutcome)>)> read;
	std::function<Clock::time_point()> now;
	// Runs work where it may take long (a picture shrunk, a JPEG), then `then` back on the watch's thread.
	std::function<void(std::function<void()> work, std::function<void()> then)> offload;
};

struct State {
	Planes last;
	bool pending = false;   // changed since the last verdict
	bool reported = false;  // the crash on the screen was reported
	bool deferred = false;  // a crash was left unread while somebody watched or video held the TPU
	bool judged = false;    // a verdict was logged since the start
	Clock::time_point read_at{};
};

class Watch {
public:
	explicit Watch(Hooks hooks) : h_(std::move(hooks)) {}
	// One look; done once it has ended (looks never overlap).
	void step(std::function<void()> done);
	const State &state() const { return s_; }

private:
	void reset();
	void judge(Planes planes, std::function<void()> done);
	void check(Verdict verdict, Planes judged, std::function<void()> done);
	void read_done(const Verdict &verdict, Planes judged, Clock::time_point previous, bool cancelled, ReadOutcome got,
	               std::function<void()> done);

	Hooks h_;
	State s_;
};

// The mean absolute difference per byte; 255 for pictures of different sizes or none.
double mean_difference(const Planes &a, const Planes &b);
// B, G and R planes scaled down to to_w x to_h, each pixel the mean of the block it covers (the classifier learnt from
// pictures scaled that way); a block of 4 or more on a side is sampled at every other row and column.
Planes shrink_planes(const Planes &planes, int width, int height, int to_w, int to_h);
// A JPEG of a frame at most shot_width wide, or "".
std::string screen_jpeg(const Planes &planes, int width, int height);

// Text that names a crash (Go's regexps screenBSODText and screenPanicText).
bool bsod_text(std::string_view text);
bool panic_text(std::string_view text);
// The lines of the screen from two before the first that names the crash, at most 20.
std::string crash_lines(std::string_view text, bool (*crash)(std::string_view));

} // namespace nkos::video::screen_watch
