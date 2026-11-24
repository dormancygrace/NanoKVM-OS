#include "video/vision.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/server.hpp"
#include "video/encoder.hpp"
#include "video/screen.hpp"

extern "C" {
#include "kvm_vision.h"
#include "video/capture_worker.h"
}

#include <atomic>
#include <chrono>
#include <cstring>
#include <fstream>
#include <format>
#include <string>
#include <unistd.h>

namespace nkos::video::vision {

namespace {

bool initialized = false;
std::atomic<bool> released{false};  // deinit ran
nk_capture_worker *worker = nullptr;
nk_capture_shared *shared = nullptr;
int ready_fd = -1, doorbell_fd = -1;
int next_slot = 0;  // the consumer's slot
bool running = false;
CaptureParams current;
std::function<void(Frame)> deliver;

std::string read_trimmed(const char *path)
{
	std::ifstream in(path);
	std::string s;
	std::getline(in, s);
	while (!s.empty() && (s.back() == ' ' || s.back() == '\r' || s.back() == '\t')) s.pop_back();
	return s;
}

bool frame_detect_enabled() { return read_trimmed("/etc/kvm/frame_detect") == "1"; }

int load(std::int32_t *p) { return __atomic_load_n(p, __ATOMIC_SEQ_CST); }
void store(std::int32_t *p, int v) { __atomic_store_n(p, v, __ATOMIC_SEQ_CST); }

bool slot_ready() { return load(&shared->slot[next_slot].state) == NK_SLOT_READY; }

// Copies the ready slot behind the transports' headroom and frees it; the doorbell wakes a capture thread that
// waits for this slot. Returns the frame with the native result (-2 for an incomplete access unit).
Frame take()
{
	nk_capture_slot &slot = shared->slot[next_slot];
	Frame frame;
	frame.result = slot.result;
	if (frame.result >= 0) {
		if (slot.size == 0 || slot.size != slot.total) {
			frame.result = -2;
		} else {
			frame.storage = frame_buffer(frame_headroom + slot.size);
			std::memcpy(frame.storage->data() + frame_headroom, slot.data, slot.size);
			frame.offset = frame_headroom;
			frame.size = slot.size;
		}
	}
	store(&slot.state, NK_SLOT_FREE);
	next_slot ^= 1;
	if (load(&shared->producer_waiting)) {
		std::uint64_t one = 1;
		(void)!::write(doorbell_fd, &one, sizeof one);
	}
	return frame;
}

// The ready eventfd fired. The consumer flag is published before the final check, so the capture thread signals
// exactly when this side may sleep; a stale signal only costs another check.
void on_ready()
{
	std::uint64_t count;
	(void)!::read(ready_fd, &count, sizeof count);
	for (;;) {
		while (running && slot_ready()) {
			Frame frame = take();
			if (deliver) deliver(std::move(frame));
		}
		if (!running) return;
		store(&shared->consumer_waiting, 1);
		if (!slot_ready()) return;
		store(&shared->consumer_waiting, 0);
	}
}

nk_capture_params native(const CaptureParams &p)
{
	return nk_capture_params{p.width, p.height, p.bitrate, p.codec, p.fps};
}

// The device's own stream will want 4:2:0 capture by the native mode rule (_mmf_vi_yuv420_rule): H.264 at the
// input size and rate. Native capture applies the rest of the rule at every start; a wrong guess costs one capture
// restart when the first stream starts.
bool first_capture_yuv420()
{
	if (read_trimmed("/kvmapp/kvm/transport") == "mjpeg") return false;  // StreamTransport
	auto selected = selected_codec();
	if (!selected || *selected != Codec::h264) return false;
	const Screen &s = screen();
	int input_fps = read_video_value("/run/nanokvm/input_fps");
	if (input_fps <= 0) input_fps = 60;
	auto thins = [](int input, int stream) { return stream < input - std::max(input / 20, 2); };
	auto [width, height] = capture_input_size();
	if (width <= 0 || height <= 0) return s.height == 0 && !thins(input_fps, s.fps);
	int stream_fps = std::min({s.fps, capture_rate_limit(width, height), capture_rate_limit(s.width, s.height)});
	return h264_allowed_for(width, height, s.height) && !stream_scaled(width, height, s.height) &&
	       !thins(input_fps, stream_fps);
}

} // namespace

bool init()
{
	kvmv_set_first_capture_yuv420(first_capture_yuv420() ? 1 : 0);
	kvmv_init(0);
	initialized = true;
	if (::set_mjpeg_chroma(screen().mjpeg_chroma == 422 ? 1 : 0) != 0) log::error("failed to select saved MJPEG chroma");
	set_frame_detact(frame_detect_enabled() ? 60 : 0);
	worker = nk_capture_create(&ready_fd, &doorbell_fd, &shared);
	if (!worker) {
		log::error("native capture worker unavailable");
		return false;
	}
	log::debug("kvm vision initialized");
	return true;
}

void deinit()
{
	if (!initialized) return;
	if (worker) {
		stop();
		nk_capture_destroy(worker);
		worker = nullptr;
	}
	kvmv_deinit();
	initialized = false;
	released = true;
	log::debug("stop kvm vision...");
}

bool closed() { return released; }

bool start(const CaptureParams &params, std::function<void(Frame)> on_frame)
{
	if (!worker || running) return false;
	static bool watched = false;
	if (!watched) {
		if (!http::watch_fd(ready_fd, on_ready)) {
			log::error("cannot watch the capture notification");
			return false;
		}
		watched = true;
	}
	deliver = std::move(on_frame);
	current = params;
	running = true;
	auto p = native(params);
	nk_capture_start(worker, &p);
	store(&shared->consumer_waiting, 1);
	if (slot_ready()) on_ready();
	return true;
}

void update(const CaptureParams &params)
{
	if (!running || params == current) return;
	current = params;
	auto p = native(params);
	nk_capture_update(worker, &p);
}

void stop()
{
	if (!running) return;
	running = false;
	nk_capture_stop(worker);
	while (slot_ready()) take();
	store(&shared->consumer_waiting, 0);
	deliver = nullptr;
}

void request_keyframe()
{
	if (initialized) kvmv_request_keyframe();
}

void set_capture_fps(int fps)
{
	if (initialized) kvmv_set_capture_fps(static_cast<std::uint8_t>(fps));
}

namespace {

// Holds the capture thread off the library for one operation (device thread; costs nothing without a stream).
struct Paused {
	Paused() { nk_capture_pause(worker); }
	~Paused() { nk_capture_resume(worker); }
};

} // namespace

void set_hdmi(bool enable, std::function<void()> done)
{
	if (!initialized || !worker) {
		if (done) done();
		return;
	}
	loop::device([enable, done = std::move(done)] {
		{
			Paused paused;
			if (kvmv_hdmi_control(enable ? 1 : 0) < 0) log::error("failed to set hdmi to {}", enable);
		}
		if (done) loop::post(done);
	});
}

void set_capture_parked(bool parked, std::function<void(bool)> done)
{
	if (!initialized || !worker) {
		if (done) done(false);
		return;
	}
	loop::device([parked, done = std::move(done)] {
		bool ok;
		{
			Paused paused;
			ok = kvmv_set_capture_parked(parked ? 1 : 0) == 0;
		}
		if (done) loop::post([done, ok] { done(ok); });
	});
}

namespace {

// JPEG buffers: reused once every holder (viewers, queues) let go. Device thread.
std::vector<std::shared_ptr<std::vector<unsigned char>>> jpeg_pool;

std::shared_ptr<std::vector<unsigned char>> jpeg_buffer()
{
	for (auto &b : jpeg_pool)
		if (b.use_count() == 1) return b;
	auto b = std::make_shared<std::vector<unsigned char>>();
	if (jpeg_pool.size() < 6) jpeg_pool.push_back(b);
	return b;
}

int jpeg_sink(std::uintptr_t context, const std::uint8_t *data, std::uint32_t size, std::uint32_t offset, std::uint32_t total)
{
	auto &buf = *reinterpret_cast<std::vector<unsigned char> *>(context);
	std::size_t need = frame_headroom + total;
	if (offset == 0 && buf.size() < need) buf.resize(need + need / 8);
	if (offset + static_cast<std::size_t>(size) > total || buf.size() < need) return 1;
	std::memcpy(buf.data() + frame_headroom + offset, data, size);
	return 0;
}

template <typename Read>
void read_jpeg(Read read, std::function<void(Jpeg)> done)
{
	if (!initialized) {
		loop::post([done = std::move(done)] { done({}); });
		return;
	}
	loop::device([read, done = std::move(done)] {
		Jpeg j;
		j.storage = jpeg_buffer();
		j.offset = frame_headroom;
		std::uint32_t total = 0;
		j.result = read(reinterpret_cast<std::uintptr_t>(j.storage.get()), &total);
		if (j.result == 0 || j.result == 6) j.size = total;
		else j.storage.reset();
		loop::post([done, j = std::move(j)]() mutable { done(std::move(j)); });
	});
}

// The total of the last frame the sink saw (device thread).
thread_local std::uint32_t jpeg_total = 0;

int jpeg_sink_total(std::uintptr_t context, const std::uint8_t *data, std::uint32_t size, std::uint32_t offset, std::uint32_t total)
{
	jpeg_total = total;
	return jpeg_sink(context, data, size, offset, total);
}

} // namespace

void read_mjpeg(std::uint16_t width, std::uint16_t height, std::uint16_t quality, std::function<void(Jpeg)> done)
{
	read_jpeg([=](std::uintptr_t ctx, std::uint32_t *total) {
		jpeg_total = 0;
		int r = kvmv_read_mjpeg_sink(width, height, quality, jpeg_sink_total, ctx);
		*total = jpeg_total;
		return r;
	}, std::move(done));
}

void read_mjpeg_tiles(std::uint16_t width, std::uint16_t height, std::uint16_t quality, bool key,
                      std::uint16_t max_tiles, std::function<void(Jpeg)> done)
{
	read_jpeg([=](std::uintptr_t ctx, std::uint32_t *total) {
		jpeg_total = 0;
		int r = kvmv_read_mjpeg_tiles_sink(width, height, quality, key ? 1 : 0, max_tiles, jpeg_sink_total, ctx);
		*total = jpeg_total;
		return r;
	}, std::move(done));
}

void read_bgr_scaled(std::uint16_t width, std::uint16_t height, std::function<void(int, std::vector<unsigned char>)> done)
{
	loop::device([=, done = std::move(done)]() mutable {
		std::vector<unsigned char> planes(std::size_t(width) * height * 3);
		int r = initialized ? kvmv_read_bgr_scaled(width, height, planes.data(), static_cast<std::uint32_t>(planes.size())) : -9;
		loop::post([r, planes = std::move(planes), done = std::move(done)]() mutable { done(r, std::move(planes)); });
	});
}

void read_bgr_planar(std::uint16_t width, std::uint16_t height, std::function<void(int, std::vector<unsigned char>)> done)
{
	loop::device([=, done = std::move(done)]() mutable {
		std::vector<unsigned char> planes(std::size_t(width) * height * 3);
		int r = initialized ? kvmv_read_bgr_planar(width, height, planes.data(), static_cast<std::uint32_t>(planes.size())) : -9;
		loop::post([r, planes = std::move(planes), done = std::move(done)]() mutable { done(r, std::move(planes)); });
	});
}

std::string planar_error(int result)
{
	switch (result) {
	case -9: return "planar screen pictures are unavailable";
	case -3: return "no picture of the screen in time";
	case -10: return "no HDMI signal";
	case -11: return "the capture is not ready";
	case -12: return "the capture cannot run at the input's resolution";
	case -13: return "the input size changed";
	case -14: return "the input is larger than Full HD";
	default: return std::format("no picture of the screen ({})", result);
	}
}

void run_paused(std::function<void()> fn)
{
	loop::device([fn = std::move(fn)] {
		if (initialized && worker) {
			Paused paused;
			fn();
		} else {
			fn();
		}
	});
}

void set_mjpeg_chroma(std::uint16_t chroma, std::function<void(int)> done)
{
	if (!initialized || (chroma != 420 && chroma != 422)) {
		loop::post([done] { done(-1); });
		return;
	}
	loop::device([chroma, done] {
		int result;
		{
			Paused paused;
			result = ::set_mjpeg_chroma(chroma == 422 ? 1 : 0);
		}
		loop::post([done, result] { done(result); });
	});
}

void release_jpeg_buffers()
{
	loop::device([] { jpeg_pool.clear(); });
}

void release_mjpeg_tiles()
{
	if (!initialized) return;
	loop::device([] { kvmv_mjpeg_tiles_release(); });
}

void set_frame_detect(std::uint8_t frames)
{
	if (!initialized) return;
	loop::device([frames] {
		Paused paused;
		set_frame_detact(frames);
	});
}

bool capture_reclaim_pending() { return initialized && kvmv_capture_reclaim_pending() != 0; }

bool hdmi_signal_active() { return initialized && kvmv_hdmi_signal_active() != 0; }

std::pair<int, std::string> mjpeg_chroma_status()
{
	if (!initialized) return {420, "recovery"};
	std::uint8_t state = get_mjpeg_chroma_status();
	int active = state & MJPEG_CHROMA_ACTIVE_422 ? 422 : 420;
	if (state & MJPEG_CHROMA_CAPTURE_420) return {active, "h264Capture"};
	if (state & MJPEG_CHROMA_FALLBACK_ERROR) return {active, "hardware"};
	if ((state & MJPEG_CHROMA_LIMIT_WIDTH) && active == 422) return {active, "resolution"};
	if (state & MJPEG_CHROMA_SHARED_VIDEO) return {active, "video"};
	if (state & MJPEG_CHROMA_FRAME_DETECT) return {active, "frameDetection"};
	return {active, ""};
}

} // namespace nkos::video::vision
