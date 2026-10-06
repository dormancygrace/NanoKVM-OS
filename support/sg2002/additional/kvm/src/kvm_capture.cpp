#include "kvm_capture.hpp"
#include "kvm_mmf.hpp"
#include "internal/capture_rate.hpp"
#include "linux/cvi_comm_video.h"

#include <cerrno>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <new>
#include <time.h>

namespace nanokvm {
int nv21_format() { return PIXEL_FORMAT_NV21; }
int nv16_format() { return PIXEL_FORMAT_NV16; }
int Capture::get_format() const { return format_ < 0 ? nv21_format() : format_; }
void sleep_ms(unsigned milliseconds) {
    timespec remaining{static_cast<time_t>(milliseconds / 1000),
                       static_cast<long>(milliseconds % 1000) * 1000000L};
    while (nanosleep(&remaining, &remaining) != 0 && errno == EINTR) {}
}
JpegBuffer::~JpegBuffer() { free(bytes_); }
Nv21Frame::~Nv21Frame() {
    if (lease_ >= 0) mmf_vi_frame_release(lease_);
    else free(bytes_);
}
JpegBuffer *Nv21Frame::to_jpeg(int quality) {
    int result = lease_ >= 0
        ? mmf_enc_jpg_push_vi_with_quality(0, lease_, quality)
        : mmf_enc_jpg_push_with_quality(0, bytes_, width_, height_, nv21_format(), quality);
    if (result != 0) {
        mmf_enc_jpg_deinit(0);
        return nullptr;
    }
    uint8_t *encoded = nullptr;
    int length = 0;
    if (mmf_enc_jpg_pop(0, &encoded, &length) != 0 || !encoded || length <= 0) {
        mmf_enc_jpg_deinit(0);
        return nullptr;
    }
    auto *copy = static_cast<uint8_t *>(malloc(static_cast<size_t>(length)));
    if (copy) memcpy(copy, encoded, static_cast<size_t>(length));
    if (mmf_enc_jpg_free(0) != 0) {
        free(copy);
        mmf_enc_jpg_deinit(0);
        return nullptr;
    }
    if (!copy) return nullptr;
    auto *jpeg = new (std::nothrow) JpegBuffer(copy, static_cast<size_t>(length));
    if (!jpeg) free(copy);
    return jpeg;
}
static bool valid_size(int width, int height) {
    // NV21 requires complete chroma pairs. Keep the application/driver bounds
    // separate; this is the storage representation bound, not a hardware limit.
    return width > 0 && height > 0 && width <= UINT16_MAX && height <= UINT16_MAX
        && width % 2 == 0 && height % 2 == 0;
}
int Capture::get_channel() const {
    return channel_ >= 0 && mmf_vi_chn_is_open(channel_) ? channel_ : -1;
}
int Capture::output_index(int format) const { return format == nv16_format() ? 1 : 0; }
bool Capture::has_format(int format) const {
    const auto &output = outputs_[output_index(format)];
    return output.channel >= 0 && mmf_vi_chn_is_open(output.channel);
}
int Capture::format_width(int format) const {
    return has_format(format) ? outputs_[output_index(format)].width : 0;
}
bool Capture::matches_output(int format, int width, int height) const {
    const auto &output = outputs_[output_index(format)];
    return has_format(format) && output.width == width && output.height == height;
}
int Capture::close_format(int format) {
    auto &output = outputs_[output_index(format)];
    if (output.channel < 0) return 0;
    const int result = mmf_del_vi_channel(output.channel);
    if (result) { cleanup_error_ = result; return result; }
    if (channel_ == output.channel) channel_ = -1;
    output = Output{};
    return 0;
}
int Capture::discard_other_pending() {
    for (const auto &output : outputs_) {
        if (output.channel >= 0 && output.channel != channel_) {
            const int result = mmf_vi_drop_pending(output.channel);
            if (result) return result;
        }
    }
    return 0;
}
static unsigned video_pool_mib() {
    static int mib = -1;
    if (mib < 0) {
        uint8_t size[4] = {};
        FILE *file = fopen("/proc/device-tree/reserved-memory/ion/size", "rb");
        const size_t count = file ? fread(size, 1, sizeof(size), file) : 0;
        if (file) fclose(file);
        mib = count == sizeof(size)
            ? (int)((((uint32_t)size[0] << 24) | ((uint32_t)size[1] << 16)
                | ((uint32_t)size[2] << 8) | size[3]) >> 20) : 0;
    }
    return (unsigned)mib;
}

int Capture::open_output(int index, int width, int height) {
    // Only physical channel 1 produces real pixels beyond 1920 on this SoC.
    // Rehome a narrow peer if it occupies the wide-capable scaler.
    if (width > 1920 && mmf_vi_chn_is_open(1)) {
        const int peer = 1 - index;
        auto previous = outputs_[peer];
        if (previous.channel != 1 || previous.width > 1920) return -1;
        const int closed = close_format(peer ? nv16_format() : nv21_format());
        if (closed) return closed;
        const int moved = open_output(peer, previous.width, previous.height);
        if (moved) return moved;
    }
    const int ch = width > 1920 ? 1 : mmf_get_vi_unused_channel();
    if (ch < 0 || mmf_vi_chn_is_open(ch)) return -1;
    mmf_set_vi_hmirror(ch, mirror_);
    mmf_set_vi_vflip(ch, flip_);
    // When the encoder holds one buffer for about a whole frame period, VPSS
    // needs a third one or it drops input frames (3840x2160: 25 of 30 fps).
    // QHD keeps two buffers in a 64 MiB pool, where a third does not fit.
    const bool fast = (long)width * height * capture_rate_limit(width, height) > fast_pixel_rate;
    const bool room = width * height <= 1920 * 1088 || video_pool_mib() >= 96;
    const int buffers = fast && room ? 3 : 2;
    if (mmf_add_vi_channel_configured(ch, width, height, index ? nv16_format() : nv21_format(), buffers, 1)) return -1;
    outputs_[index] = {ch, width, height};
    return 0;
}
int Capture::open_channel() {
    const int index = output_index(get_format());
    const int result = open_output(index, width_, height_);
    channel_ = result ? -1 : outputs_[index].channel;
    return result;
}
int Capture::shutdown() {
    if (initialized_) {
        // Close encoders before their producer, including extra JPEG MMF refs.
        const int result = mmf_try_deinit(true);
        if (result != 0) {
            cleanup_error_ = result;
            return result;
        }
        initialized_ = false;
    }
    channel_ = -1;
    for (auto &output : outputs_) output = Output{};
    cleanup_error_ = 0;
    return 0;
}
int Capture::restart(int width, int height) {
    if (!valid_size(width, height)) return -1;
    const int stopped = shutdown();
    if (stopped != 0) return stopped;
    width_ = width;
    height_ = height;
    // Even a failed initialization can leave MMF resources to clean up.
    // Keep that ownership if teardown fails, so restart must finish it first.
    initialized_ = true;
    const int initialized = mmf_init();
    if (initialized != 0) {
        shutdown();
        return initialized;
    }
    if (mmf_vi_init() != 0 || open_channel() != 0) {
        shutdown();
        return -1;
    }
    return 0;
}
int Capture::set_resolution(int width, int height, int format) {
    if (!valid_size(width, height)) return -1;
    const int selected = format < 0 ? get_format() : format;
    if (selected != nv21_format() && selected != nv16_format()) return -1;
    if (!initialized_ || cleanup_error_ != 0) {
        // Complete failed teardown before changing the producer's format.
        const int stopped = shutdown();
        if (stopped != 0) return stopped;
        format_ = selected;
        return restart(width, height);
    }
    const int index = output_index(selected);
    auto &output = outputs_[index];
    if (output.channel >= 0 && (output.width != width || output.height != height)) {
        const int result = close_format(selected);
        if (result) return result;
    }
    width_ = width;
    height_ = height;
    format_ = selected;
    if (output.channel < 0 && open_channel() != 0) return -1;
    channel_ = output.channel;
    return 0;
}
int Capture::hmirror(int enable) {
    if (enable < 0) return mirror_;
    if (cleanup_error_ != 0) return cleanup_error_;
    if (mirror_ == (enable != 0)) return 0;
    mirror_ = enable != 0;
    for (const auto &output : outputs_) {
        if (output.channel < 0) continue;
        mmf_set_vi_hmirror(output.channel, mirror_);
        const int result = mmf_reset_vi_channel(output.channel, output.width, output.height,
                                               &output == &outputs_[1] ? nv16_format() : nv21_format());
        if (result) { cleanup_error_ = result; return result; }
    }
    return 0;
}
int Capture::vflip(int enable) {
    if (enable < 0) return flip_;
    if (cleanup_error_ != 0) return cleanup_error_;
    if (flip_ == (enable != 0)) return 0;
    flip_ = enable != 0;
    for (const auto &output : outputs_) {
        if (output.channel < 0) continue;
        mmf_set_vi_vflip(output.channel, flip_);
        const int result = mmf_reset_vi_channel(output.channel, output.width, output.height,
                                               &output == &outputs_[1] ? nv16_format() : nv21_format());
        if (result) { cleanup_error_ = result; return result; }
    }
    return 0;
}
Nv21Frame *Capture::read() {
    if (cleanup_error_ != 0 || get_channel() < 0 || get_format() != nv21_format()) return nullptr;
    mmf_nv21_view_t view{};
    if (mmf_vi_frame_pop_nv21(channel_, &view) != 0) return nullptr;
    const size_t rows = static_cast<size_t>(height_) * 3 / 2;
    if (!view.data || view.width < static_cast<unsigned>(width_)
        || view.height != static_cast<unsigned>(height_)
        || view.y_stride < static_cast<unsigned>(width_) || view.vu_stride < static_cast<unsigned>(width_)
        || static_cast<uint64_t>(view.y_stride) * height_ > view.vu_offset
        || static_cast<uint64_t>(view.vu_offset) + static_cast<uint64_t>(view.vu_stride) * (height_ / 2) > view.length) {
        mmf_vi_frame_release(channel_);
        return nullptr;
    }
    const size_t packed_size = static_cast<size_t>(width_) * rows;
    if (view.width == static_cast<unsigned>(width_) && view.y_stride == view.width
        && view.vu_stride == view.width && view.vu_offset == view.width * view.height) {
        auto *frame = new (std::nothrow) Nv21Frame(view.data, packed_size,
                                                width_, height_, channel_);
        if (!frame) mmf_vi_frame_release(channel_);
        else mmf_vi_frame_free(channel_); // Make the lease available to VENC.
        return frame;
    }
    // VB rows may have padding independently of active image width. Pack
    // visible pixels for the copied fallback, including every chroma row.
    auto *copy = static_cast<uint8_t *>(malloc(packed_size));
    if (copy) {
        for (int row = 0; row < height_; ++row)
            memcpy(copy + static_cast<size_t>(row) * width_, view.data + static_cast<size_t>(row) * view.y_stride, width_);
        for (int row = 0; row < height_ / 2; ++row)
            memcpy(copy + static_cast<size_t>(height_ + row) * width_,
                   view.data + view.vu_offset + static_cast<size_t>(row) * view.vu_stride, width_);
    }
    mmf_vi_frame_release(channel_);
    if (!copy) return nullptr;
    auto *frame = new (std::nothrow) Nv21Frame(copy, packed_size, width_, height_, -1);
    if (!frame) free(copy);
    return frame;
}
} // namespace nanokvm
