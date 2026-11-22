#pragma once

// The native capture library (libkvm, kvm_vision.h), linked directly. H.264/H.265 access units come from the
// native capture thread (capture_worker.c): it paces the reads at the stream rate and assembles each access unit
// into one of two slots; its ready eventfd sits in the server's event loop, so a frame costs one wakeup of the
// loop thread and one copy. Loop thread only, except where noted.

#include "video/frame.hpp"

#include <cstdint>
#include <functional>

namespace nkos::video::vision {

// kvmv_init with the saved settings applied (first capture mode, MJPEG chroma, frame detection). Before the
// server runs; false when video is unavailable.
bool init();
// Stops the capture thread and releases the capture (kvmv_deinit). After the event loop has ended.
void deinit();

struct CaptureParams {
	std::uint16_t width = 0, height = 0, bitrate = 0;
	std::uint8_t codec = 0, fps = 0;  // codec: 1 H.264, 2 H.265
	bool operator==(const CaptureParams &) const = default;
};

// Starts the paced stream; on_frame runs on the loop thread for each access unit (result < 0: no picture). One
// stream at a time.
bool start(const CaptureParams &params, std::function<void(Frame)> on_frame);
// Applies to the next read; unchanged parameters cost nothing.
void update(const CaptureParams &params);
// Stops the stream and waits for the read in flight; frames not taken are dropped.
void stop();

// Native requests that are safe beside a running stream.
void request_keyframe();
void set_capture_fps(int fps);

} // namespace nkos::video::vision
