#include "video/preview.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/base64.hpp"
#include "common/image.hpp"
#include "common/loop.hpp"
#include "system/failsafe.hpp"
#include "video/hdmi.hpp"
#include "video/screen.hpp"
#include "video/vision.hpp"
#include "video/watermark.hpp"

#include <algorithm>
#include <chrono>
#include <format>
#include <memory>
#include <vector>

namespace nkos::video::preview {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

constexpr int max_width = 640, max_height = 360, quality = 60;
constexpr auto max_age = 5s;
// A state without a picture is asked again sooner: a capture that was just switched on has one in a moment.
constexpr auto retry_age = 1s;
constexpr auto timeout = 4s;
constexpr int attempts_alone = 6;
constexpr auto retry = 200ms;

struct Preview {
	std::string state;  // ok, disabled, no-signal, unavailable; empty: none yet
	std::string jpeg;   // base64
	int width = 0, height = 0;
};

Preview last;
Clock::time_point last_at;
std::uint64_t last_capture = 0;
// Requests waiting for the picture being taken.
std::vector<std::uint64_t> waiting;
bool taking = false;

std::uint64_t capture_generation() { return hdmi::capture_switches() + watermark::changes(); }

std::string json(const Preview &p)
{
	if (p.state != "ok") return std::format("{{\"state\":\"{}\"}}", p.state);
	return std::format("{{\"state\":\"ok\",\"jpeg\":\"{}\",\"width\":{},\"height\":{}}}", p.jpeg, p.width, p.height);
}

void answer(std::uint64_t id, const Preview &p)
{
	http::Response out;
	out.set_header("Cache-Control", "no-store");
	api::send(out, 0, "success", json(p));
	http::complete(id, std::move(out));
}

// The input size fitted into the bounds, keeping its shape, with even sides; never larger than the input.
std::pair<int, int> fit(int width, int height)
{
	int w = width, h = height;
	if (w <= 0 || h <= 0) return {max_width, max_height};
	if (w > max_width) {
		h = h * max_width / w;
		w = max_width;
	}
	if (h > max_height) {
		w = w * max_height / h;
		h = max_height;
	}
	return {std::max(w & ~1, 16), std::max(h & ~1, 16)};
}

// Go's color.RGBToYCbCr.
void ycbcr(int r, int g, int b, unsigned char &y, unsigned char &cb, unsigned char &cr)
{
	y = static_cast<unsigned char>((19595 * r + 38470 * g + 7471 * b + (1 << 15)) >> 16);
	int c = -11056 * r - 21712 * g + 32768 * b + (257 << 15);
	cb = static_cast<unsigned char>((static_cast<std::uint32_t>(c) & 0xff000000u) == 0 ? c >> 16 : ~(c >> 31));
	c = 32768 * r - 27440 * g - 5328 * b + (257 << 15);
	cr = static_cast<unsigned char>((static_cast<std::uint32_t>(c) & 0xff000000u) == 0 ? c >> 16 : ~(c >> 31));
}

// A JPEG of B, G and R planes: each chroma sample from the mean of its four pixels, as the Go server.
std::optional<std::string> to_jpeg(const std::vector<unsigned char> &planes, int width, int height)
{
	std::size_t n = static_cast<std::size_t>(width) * height;
	if (width < 2 || height < 2 || width % 2 || height % 2 || planes.size() < 3 * n) return std::nullopt;
	const unsigned char *b = planes.data(), *g = b + n, *r = g + n;
	std::vector<unsigned char> Y(n), Cb(n / 4), Cr(n / 4);
	unsigned char unused;
	for (int y = 0; y < height; y += 2) {
		for (int x = 0; x < width; x += 2) {
			int sr = 0, sg = 0, sb = 0;
			for (std::size_t at : {std::size_t(y) * width + x, std::size_t(y) * width + x + 1, std::size_t(y + 1) * width + x,
			                       std::size_t(y + 1) * width + x + 1}) {
				unsigned char luma, u, v;
				ycbcr(r[at], g[at], b[at], luma, u, v);
				Y[at] = luma;
				sr += r[at];
				sg += g[at];
				sb += b[at];
			}
			std::size_t c = std::size_t(y / 2) * (width / 2) + x / 2;
			ycbcr((sr + 2) / 4, (sg + 2) / 4, (sb + 2) / 4, unused, Cb[c], Cr[c]);
		}
	}
	return image::encode_jpeg_yuv420(Y.data(), Cb.data(), Cr.data(), width, height, quality);
}

struct Take : std::enable_shared_from_this<Take> {
	std::unique_ptr<hdmi::Lease> lease;
	bool fresh_claim = false;  // the first picture after a wake is dropped
	int attempts = attempts_alone;
	int attempt = 0;
	Clock::time_point deadline;
	std::function<void(Preview)> done;

	void finish(Preview p)
	{
		lease.reset();
		auto d = std::move(done);
		d(std::move(p));
	}
	// The answer without a picture: with nothing on the input that is the reason.
	void none() { finish(Preview{vision::hdmi_signal_active() ? "unavailable" : "no-signal"}); }

	void try_read()
	{
		if (attempt >= attempts || Clock::now() >= deadline) return none();
		// A viewer that came while the last try waited.
		if (attempt > 0 && hdmi::viewer_count() > 0) return none();
		attempt++;
		auto [iw, ih] = capture_input_size();
		auto [w, h] = fit(iw, ih);
		vision::read_bgr_scaled(static_cast<std::uint16_t>(w), static_cast<std::uint16_t>(h),
		                        [self = shared_from_this(), w, h](int r, std::vector<unsigned char> planes) {
			                        self->read_done(r, std::move(planes), w, h);
		                        });
	}

	void read_done(int r, std::vector<unsigned char> planes, int w, int h)
	{
		switch (r) {
		case 0:
			if (fresh_claim && hdmi::claim_fresh_frame()) {
				fresh_claim = false;
				return try_read();
			}
			// Encoding a 640x360 picture takes tens of milliseconds: off the loop.
			loop::blocking([self = shared_from_this(), planes = std::move(planes), w, h] {
				auto jpeg = to_jpeg(planes, w, h);
				loop::post([self, jpeg = std::move(jpeg), w, h] {
					if (!jpeg) return self->finish(Preview{"unavailable"});
					self->finish(Preview{"ok", base64::encode(*jpeg), w, h});
				});
			});
			return;
		case -10:
			return finish(Preview{"no-signal"});
		case -3:   // timeout
		case -11:  // capture not ready
		case -13:  // input changed
			loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(retry), [self = shared_from_this()] { self->try_read(); });
			return;
		default:
			return none();
		}
	}
};

void take(std::function<void(Preview)> done)
{
	auto t = std::make_shared<Take>();
	t->lease = std::make_unique<hdmi::Lease>();
	t->fresh_claim = hdmi::needs_fresh_frame();
	// A read that gets no picture holds the capture for its whole wait, and a video session waits with it: next to
	// one there is a single try, and the next request is the next.
	t->attempts = hdmi::viewer_count() > 0 ? 1 : attempts_alone;
	t->deadline = Clock::now() + timeout;
	t->done = std::move(done);
	auto wait = t->lease->ready_at() - Clock::now();
	if (wait > Clock::duration::zero()) {
		if (wait >= timeout) return t->finish(Preview{"unavailable"});
		loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(wait) + 1ms, [t] { t->try_read(); });
	} else {
		t->try_read();
	}
}

void request(std::uint64_t id)
{
	std::uint64_t capture = capture_generation();
	if (last_capture != capture) {
		last_capture = capture;
		last = {};
	}
	if (hdmi::disabled()) return answer(id, Preview{"disabled"});
	// A watermark is saved and the library does not have it: a picture now would be without it.
	if (watermark::unapplied()) {
		last = {};
		return answer(id, Preview{"unavailable"});
	}
	auto age = last.state != "ok" ? retry_age : max_age;
	if (!last.state.empty() && Clock::now() - last_at < age) return answer(id, last);
	waiting.push_back(id);
	if (taking) return;
	taking = true;
	take([capture](Preview p) {
		taking = false;
		// Switched while it was read: the picture may be of the screen before.
		if (capture_generation() != capture) {
			p = Preview{"unavailable"};
		} else {
			last = p;
			last_at = Clock::now();
		}
		auto ids = std::move(waiting);
		waiting.clear();
		for (auto i : ids) answer(i, p);
	});
}

} // namespace

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/screen/preview", auth::guarded([](http::Request &req, http::Response &res) {
		if (!system::failsafe::require_video(res)) return;
		res.pending = true;
		request(req.id);
	}));
}

} // namespace nkos::video::preview
