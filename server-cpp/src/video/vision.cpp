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
#include <string>
#include <unistd.h>

namespace nkos::video::vision {

namespace {

bool initialized = false;
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
	if (read_trimmed("/kvmapp/kvm/transport") == "mjpeg") return false;
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
	if (set_mjpeg_chroma(screen().mjpeg_chroma == 422 ? 1 : 0) != 0) log::error("failed to select saved MJPEG chroma");
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
	log::debug("stop kvm vision...");
}

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

bool capture_reclaim_pending() { return initialized && kvmv_capture_reclaim_pending() != 0; }

bool hdmi_signal_active() { return initialized && kvmv_hdmi_signal_active() != 0; }

} // namespace nkos::video::vision
