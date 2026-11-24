#pragma once

// The native capture library (libkvm, kvm_vision.h), linked directly. H.264/H.265 access units come from the
// native capture thread (capture_worker.c): it paces the reads at the stream rate and assembles each access unit
// into one of two slots; its ready eventfd sits in the server's event loop, so a frame costs one wakeup of the
// loop thread and one copy. Loop thread only, except where noted.

#include "video/frame.hpp"

#include <cstdint>
#include <functional>
#include <memory>
#include <vector>
#include <string>
#include <utility>

namespace nkos::video::vision {

// kvmv_init with the saved settings applied (first capture mode, MJPEG chroma, frame detection). Before the
// server runs; false when video is unavailable.
bool init();
// Stops the capture thread and releases the capture (kvmv_deinit). After the event loop has ended, or right
// before the process replaces itself.
void deinit();
// The capture was released (deinit): the receiver must not be touched any more. Any thread.
bool closed();

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

// Operations that change the capture run on the device thread, in order; a running stream pauses for them (its
// read in flight ends first). done runs on the loop thread afterwards. HDMI capture on or off (kvmv_hdmi_control,
// about 0.3 s); the VPSS outputs parked or resumed (ok false: the library could not).
void set_hdmi(bool enable, std::function<void()> done = {});
void set_capture_parked(bool parked, std::function<void(bool ok)> done = {});
// Disabled outputs still own pools awaiting reclamation.
bool capture_reclaim_pending();
bool hdmi_signal_active();

// One JPEG read (kvmv_read_mjpeg_sink, kvmv_read_mjpeg_tiles_sink): the bytes start at offset, after
// frame_headroom bytes free for framing; result as kvm_vision.h (0 a JPEG, 5 unchanged, 6 a tile message, < 0 none).
struct Jpeg {
	std::shared_ptr<std::vector<unsigned char>> storage;
	std::size_t offset = 0, size = 0;
	int result = -1;
};
// Reads run on the device thread, in order with the operations above (beside a running H.26x stream, as in the
// library); done runs on the loop thread. Buffers come from a small pool and return to it when let go.
void read_mjpeg(std::uint16_t width, std::uint16_t height, std::uint16_t quality, std::function<void(Jpeg)> done);
void read_mjpeg_tiles(std::uint16_t width, std::uint16_t height, std::uint16_t quality, bool key,
                      std::uint16_t max_tiles, std::function<void(Jpeg)> done);
// The whole screen scaled by VPSS to width x height (even, no larger than the input) as B, G and R planes, from a
// channel of its own (kvmv_read_bgr_scaled, with the watermark); on the device thread, beside a running stream. done
// gets the native result (0 a picture) and the planes, on the loop thread.
void read_bgr_scaled(std::uint16_t width, std::uint16_t height, std::function<void(int, std::vector<unsigned char>)> done);
// The whole input unscaled (width x height: its size with even sides, up to Full HD) as B, G and R planes, from VPSS
// channel 2 (kvmv_read_bgr_planar); on the device thread, beside a running stream. done gets the native result (0 a
// picture) and the planes, on the loop thread. Hold a capture lease while reading.
void read_bgr_planar(std::uint16_t width, std::uint16_t height, std::function<void(int, std::vector<unsigned char>)> done);
// A planar read's result as the Go server's errors say it.
std::string planar_error(int result);
// Runs fn on the device thread with the stream paused (receiver maintenance: the EDID tool).
void run_paused(std::function<void()> fn);
// The MJPEG chroma (420 or 422; set_mjpeg_chroma with the stream paused); done gets the native result.
void set_mjpeg_chroma(std::uint16_t chroma, std::function<void(int result)> done);
// Lets the JPEG buffers go (no viewers left).
void release_jpeg_buffers();
// Frees what tiled reads keep (the IVE map, region channels) once nobody reads tiles.
void release_mjpeg_tiles();
// Frame detection: frames between change checks, 0 off (set_frame_detact; the stream pauses for it).
void set_frame_detect(std::uint8_t frames);

// The MJPEG chroma the VPSS produces (420 or 422) and why it is not the saved one ("" when it is): h264Capture,
// hardware, resolution, video, frameDetection; "recovery" while the video is off.
std::pair<int, std::string> mjpeg_chroma_status();

} // namespace nkos::video::vision
