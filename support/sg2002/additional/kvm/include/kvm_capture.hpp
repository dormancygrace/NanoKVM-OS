#pragma once

#include <cstddef>
#include <cstdint>

namespace nanokvm {
int nv21_format();
void sleep_ms(unsigned milliseconds);

// Contiguous encoded bytes owned by the caller, independent of the VENC lease.
class JpegBuffer {
public:
    JpegBuffer(uint8_t *bytes, size_t length) : bytes_(bytes), length_(length) {}
    ~JpegBuffer();
    JpegBuffer(const JpegBuffer &) = delete;
    JpegBuffer &operator=(const JpegBuffer &) = delete;
    uint8_t *data() const { return bytes_; }
    size_t data_size() const { return length_; }
private:
    uint8_t *bytes_;
    size_t length_;
};

// The mapped VI lease stays live until encoding finishes or the frame is dropped.
class Nv21Frame {
public:
    Nv21Frame(uint8_t *bytes, size_t length, int width, int height, int lease)
        : bytes_(bytes), length_(length), width_(width), height_(height), lease_(lease) {}
    ~Nv21Frame();
    Nv21Frame(const Nv21Frame &) = delete;
    Nv21Frame &operator=(const Nv21Frame &) = delete;
    uint8_t *data() const { return bytes_; }
    size_t data_size() const { return length_; }
    int width() const { return width_; }
    int height() const { return height_; }
    int format() const { return nv21_format(); }
    JpegBuffer *to_jpeg(int quality);
private:
    uint8_t *bytes_;
    size_t length_;
    int width_, height_, lease_;
};

// NanoKVM owns the MMF process. Construction does not initialize any hardware.
// Callers serialize these operations and frame lifetimes with vi_mutex.
class Capture {
public:
    constexpr Capture(int width, int height) : width_(width), height_(height) {}
    Capture(const Capture &) = delete;
    Capture &operator=(const Capture &) = delete;
    int restart(int width, int height);
    int set_resolution(int width, int height);
    // Preserve ownership and report errors until MMF confirms teardown.
    int shutdown();
    int hmirror(int enable);
    int vflip(int enable);
    int get_channel() const;
    Nv21Frame *read();
private:
    int open_channel();
    int width_, height_;
    int channel_ = -1;
    int cleanup_error_ = 0;
    bool initialized_ = false, mirror_ = false, flip_ = false;
};
} // namespace nanokvm
