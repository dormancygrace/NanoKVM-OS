#include "kvm_capture.hpp"
#include "kvm_mmf.hpp"
#include "linux/cvi_comm_video.h"

#include <cerrno>
#include <cstdlib>
#include <cstring>
#include <new>
#include <time.h>

namespace nanokvm {
int nv21_format() { return PIXEL_FORMAT_NV21; }
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
int Capture::open_channel() {
#ifdef NANOKVM_ENHANCED
    // In VPSS single mode channel 1 maps to SC_V1 (2880 pixels). Channel 0
    // maps to SC_D (1920 pixels) and would require the two-pass tile path.
    channel_ = width_ > 1920 ? (mmf_vi_chn_is_open(1) ? -1 : 1)
                             : mmf_get_vi_unused_channel();
#else
    channel_ = mmf_get_vi_unused_channel();
#endif
    if (channel_ < 0) return -1;
    mmf_set_vi_hmirror(channel_, mirror_);
    mmf_set_vi_vflip(channel_, flip_);
    if (mmf_add_vi_channel(channel_, width_, height_, nv21_format()) != 0) {
        channel_ = -1;
        return -1;
    }
    return 0;
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
int Capture::set_resolution(int width, int height) {
    if (!valid_size(width, height)) return -1;
    if (!initialized_ || cleanup_error_ != 0) return restart(width, height);
    if (get_channel() >= 0 && width == width_ && height == height_) return 0;
    if (channel_ >= 0) {
        const int result = mmf_del_vi_channel(channel_);
        if (result != 0) {
            cleanup_error_ = result;
            return result;
        }
        channel_ = -1;
    }
    width_ = width;
    height_ = height;
    return open_channel();
}
int Capture::hmirror(int enable) {
    if (enable < 0) return mirror_;
    if (cleanup_error_ != 0) return cleanup_error_;
    if (mirror_ == (enable != 0)) return 0;
    mirror_ = enable != 0;
    if (get_channel() < 0) return 0;
    mmf_set_vi_hmirror(channel_, mirror_);
    return mmf_reset_vi_channel(channel_, width_, height_, nv21_format());
}
int Capture::vflip(int enable) {
    if (enable < 0) return flip_;
    if (cleanup_error_ != 0) return cleanup_error_;
    if (flip_ == (enable != 0)) return 0;
    flip_ = enable != 0;
    if (get_channel() < 0) return 0;
    mmf_set_vi_vflip(channel_, flip_);
    return mmf_reset_vi_channel(channel_, width_, height_, nv21_format());
}
Nv21Frame *Capture::read() {
    if (cleanup_error_ != 0 || get_channel() < 0) return nullptr;
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
    // The MMF channel pads output width to DEFAULT_ALIGN. Retain the requested
    // visible width for the copied fallback, including every chroma row.
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
