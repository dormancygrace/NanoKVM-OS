// CPU-only contract test: real Capture/Frame/Sampler code with a fault-injected
// MMF provider. This does not qualify the ISP, DMA or hardware encoder.
#include "kvm_capture.hpp"
#include "kvm_frame_sampler.hpp"
#include "kvm_mmf.hpp"
#include <cassert>
#include <cstring>
#include <cstdio>
#include <memory>
#include <vector>

static int calls, released, shutdowns, adds, native_jpeg, copied_jpeg;
static bool opened[2], mirror, flip;
static int configured_format;
static int shutdown_error, delete_error;
static int init_error, vi_error, add_error, pop_error, jpg_push_error, jpg_pop_error, jpg_free_error;
static mmf_nv21_view_t supplied;
static uint8_t jpeg_data[] = {0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9};
int mmf_init() { ++calls; return init_error; }
int mmf_vi_init() { ++calls; return vi_error; }
int mmf_try_deinit(bool force) { assert(force); ++shutdowns; if (shutdown_error) return shutdown_error; for (auto &state : opened) state = false; return 0; }
int mmf_get_vi_unused_channel() { ++calls; return !opened[0] ? 0 : !opened[1] ? 1 : -1; }
void mmf_set_vi_hmirror(int ch, bool value) { assert(ch >= 0 && ch < 2); ++calls; mirror = value; }
void mmf_set_vi_vflip(int ch, bool value) { assert(ch >= 0 && ch < 2); ++calls; flip = value; }
int mmf_add_vi_channel(int ch, int w, int h, int fmt) {
    assert(ch >= 0 && ch < 2 && w > 0 && h > 0 && (fmt == nanokvm::nv21_format() || fmt == nanokvm::nv16_format()));
    configured_format = fmt;
    ++adds; ++calls; opened[ch] = !add_error; return add_error;
}
int mmf_del_vi_channel(int ch) { assert(ch >= 0 && ch < 2); if (delete_error) return delete_error; opened[ch] = false; return 0; }
bool mmf_vi_chn_is_open(int ch) { assert(ch >= 0 && ch < 2); return opened[ch]; }
int mmf_add_vi_channel_configured(int ch, int w, int h, int fmt, int count, int depth) {
    assert(count == 2 && depth == 1); return mmf_add_vi_channel(ch,w,h,fmt);
}
static int discarded;
int mmf_vi_drop_pending(int ch) { assert(ch >= 0 && ch < 2 && opened[ch]); ++discarded; return 0; }
int mmf_reset_vi_channel(int ch, int w, int h, int fmt) {
    const int result=mmf_del_vi_channel(ch); if(result)return result; return mmf_add_vi_channel(ch, w, h, fmt);
}
int mmf_vi_frame_pop_nv21(int ch, mmf_nv21_view_t *out) {
    assert(ch == 0 && opened[ch]); if (pop_error) return pop_error; *out = supplied; return 0;
}
void mmf_vi_frame_free(int ch) { assert(ch == 0); }
void mmf_vi_frame_release(int ch) { assert(ch == 0); ++released; }
int mmf_enc_jpg_push_vi_with_quality(int ch, int vi, int q) {
    assert(ch == 0 && vi == 0 && q == 80); ++native_jpeg; return jpg_push_error;
}
int mmf_enc_jpg_push_with_quality(int ch, uint8_t *data, int w, int h, int fmt, int q) {
    assert(ch == 0 && data && w == 8 && h == 4 && fmt == nanokvm::nv21_format() && q == 80);
    ++copied_jpeg; return jpg_push_error;
}
int mmf_enc_jpg_deinit(int ch) { assert(ch == 0); return 0; }
int mmf_enc_jpg_pop(int ch, uint8_t **data, int *len) {
    assert(ch == 0); *data = jpeg_data; *len = sizeof(jpeg_data); return jpg_pop_error;
}
int mmf_enc_jpg_free(int ch) { assert(ch == 0); return jpg_free_error; }

int main() {
    nanokvm::Capture capture(8, 4);
    assert(calls == 0); // No hardware constructors.
    assert(capture.hmirror(1) == 0 && capture.vflip(1) == 0 && calls == 0);
    assert(capture.get_channel() == -1 && !capture.read());
    assert(capture.restart(7, 4) != 0 && calls == 0);
    init_error = -1;
    assert(capture.restart(8, 4) != 0 && shutdowns == 1);
    init_error = 0; vi_error = -1;
    assert(capture.restart(8, 4) != 0 && shutdowns == 2);
    vi_error = 0; add_error = -1;
    assert(capture.restart(8, 4) != 0 && capture.get_channel() == -1 && shutdowns == 3);
    add_error = 0;
    assert(capture.restart(8, 4) == 0 && capture.get_channel() == 0 && mirror && flip);
    int previous_adds = adds;
    assert(capture.set_resolution(8, 4) == 0 && adds == previous_adds);
    assert(capture.hmirror(1) == 0 && capture.vflip(1) == 0 && adds == previous_adds);

    // Each format keeps its output and channel across interleaved reads.
    assert(capture.get_format() == nanokvm::nv21_format());
    assert(capture.set_resolution(8, 4, 12345) != 0 && adds == previous_adds);
    assert(capture.set_resolution(8, 4, nanokvm::nv16_format()) == 0 && adds == previous_adds + 1);
    assert(capture.get_channel() == 1 && capture.has_format(nanokvm::nv21_format()));
    assert(configured_format == nanokvm::nv16_format() && !capture.read());
    assert(capture.discard_other_pending() == 0 && discarded == 1);
    assert(capture.set_resolution(8, 4, nanokvm::nv21_format()) == 0);
    assert(capture.get_channel() == 0 && adds == previous_adds + 1);
    assert(capture.has_format(nanokvm::nv16_format()));
    delete_error = -72;
    assert(capture.close_format(nanokvm::nv16_format()) == delete_error);
    assert(capture.has_format(nanokvm::nv16_format()) && !capture.read());
    delete_error = 0;
    assert(capture.set_resolution(8, 4, nanokvm::nv21_format()) == 0);
    // Narrow peer on ch1 moves to ch0 when a wide producer needs ch1.
    assert(capture.close_format(nanokvm::nv21_format()) == 0);
    assert(capture.set_resolution(8,4,nanokvm::nv16_format()) == 0 && capture.get_channel() == 0);
    assert(capture.set_resolution(8,4,nanokvm::nv21_format()) == 0 && capture.get_channel() == 1);
    assert(capture.set_resolution(2560,1440,nanokvm::nv16_format()) == 0 && capture.get_channel() == 1);
    assert(capture.format_width(nanokvm::nv21_format()) == 8);
    assert(capture.set_resolution(2560,1440,nanokvm::nv21_format()) != 0); // One wide scaler.
    assert(capture.has_format(nanokvm::nv16_format()));
    assert(capture.close_format(nanokvm::nv16_format()) == 0);
    assert(capture.set_resolution(8,4,nanokvm::nv21_format()) == 0 && capture.get_channel() == 0);

    std::vector<uint8_t> pixels(48, 0);
    supplied = {pixels.data(), 48, 8, 4, 8, 8, 32};
    {
        std::unique_ptr<nanokvm::Nv21Frame> frame(capture.read());
        assert(frame && frame->data() == pixels.data() && frame->data_size() == 48 && released == 0);
        std::unique_ptr<nanokvm::JpegBuffer> jpeg(frame->to_jpeg(80));
        assert(jpeg && jpeg->data_size() == sizeof(jpeg_data) && native_jpeg == 1 && copied_jpeg == 0);
        jpeg_data[2] = 9;
        assert(jpeg->data()[2] == 1); // Encoded data survives VENC buffer reuse.
        jpg_push_error = -1; assert(!frame->to_jpeg(80)); jpg_push_error = 0;
        jpg_pop_error = -1; assert(!frame->to_jpeg(80)); jpg_pop_error = 0;
        jpg_free_error = -1; assert(!frame->to_jpeg(80)); jpg_free_error = 0;
    }
    assert(released == 1);

    // Different luma/chroma strides and a gap before chroma. Only visible
    // samples should reach the encoder; padding must never become image data.
    std::vector<uint8_t> padded(92, 0xee), expected;
    for (int y = 0; y < 6; ++y) for (int x = 0; x < 8; ++x) {
        uint8_t value = static_cast<uint8_t>(y * 8 + x);
        padded[y < 4 ? y * 12 + x : 64 + (y - 4) * 14 + x] = value;
        expected.push_back(value);
    }
    supplied = {padded.data(), 92, 12, 4, 12, 14, 64};
    {
        std::unique_ptr<nanokvm::Nv21Frame> frame(capture.read());
        assert(frame && frame->data() != padded.data() && frame->data_size() == expected.size());
        assert(memcmp(frame->data(), expected.data(), expected.size()) == 0 && released == 2);
        std::unique_ptr<nanokvm::JpegBuffer> jpeg(frame->to_jpeg(80));
        assert(jpeg && copied_jpeg == 1);
    }
    assert(released == 2); // Copied frame no longer owns the VI lease.
    supplied.length = 91; assert(!capture.read() && released == 3);
    supplied.length = 92; supplied.vu_offset = 30; assert(!capture.read() && released == 4);
    supplied.vu_offset = 64; supplied.height = 2; assert(!capture.read() && released == 5);
    pop_error = -1; assert(!capture.read() && released == 5); pop_error = 0;

    nanokvm::FrameSampler sampler;
    assert(sampler.changed(pixels.data(), 32, 8, 4)); // First all-black frame.
    assert(!sampler.changed(pixels.data(), 32, 8, 4));
    pixels[31] = 1; assert(sampler.changed(pixels.data(), 32, 8, 4));
    assert(!sampler.changed(pixels.data(), 32, 8, 4));
    assert(sampler.changed(pixels.data(), 32, 4, 8)); // Equal bytes, new geometry.
    assert(sampler.changed(pixels.data(), 31, 4, 8)); // Invalid input does not update cache.
    assert(!sampler.changed(pixels.data(), 32, 4, 8));
    assert(sampler.changed(nullptr, 32, 4, 8));
    std::vector<uint8_t> screen(1920 * 1080, 7);
    assert(sampler.changed(screen.data(), screen.size(), 1920, 1080));
    assert(!sampler.changed(screen.data(), screen.size(), 1920, 1080));
    screen[0] = 8; assert(sampler.changed(screen.data(), screen.size(), 1920, 1080));

    // Failed teardown must preserve the owner, and restart must not acquire
    // another MMF reference or create a replacement over live resources.
    shutdown_error = -71;
    int before_calls = calls, before_adds = adds;
    capture.shutdown();
    assert(capture.get_channel() == 0);
    assert(!capture.read());
    assert(capture.hmirror(0) == shutdown_error && capture.vflip(0) == shutdown_error);
    assert(capture.set_resolution(8, 4) == shutdown_error);
    assert(capture.restart(16, 8) == shutdown_error);
    assert(calls == before_calls && adds == before_adds);
    shutdown_error = 0;
    assert(capture.restart(8, 4) == 0);
    delete_error = -72;
    before_adds = adds;
    assert(capture.set_resolution(16, 8) == delete_error);
    assert(capture.get_channel() == 0 && adds == before_adds);
    assert(!capture.read());
    shutdown_error = -71;
    assert(capture.set_resolution(16, 8) == shutdown_error && adds == before_adds);
    delete_error = 0; shutdown_error = 0;
    assert(capture.set_resolution(8, 4) == 0 && adds == before_adds + 1);

    add_error = -1; assert(capture.set_resolution(16, 8) != 0 && capture.get_channel() == -1);
    add_error = 0; assert(capture.set_resolution(8, 4) == 0 && capture.get_channel() == 0);
    capture.shutdown(); assert(capture.get_channel() == -1);
    int old_shutdowns = shutdowns; capture.shutdown(); assert(shutdowns == old_shutdowns);
    // Initialization may have partially acquired global resources before
    // returning an error. Preserve retry ownership if its cleanup also fails.
    init_error = -73; shutdown_error = -74;
    assert(capture.restart(8, 4) == init_error);
    before_calls = calls;
    init_error = 0;
    assert(capture.restart(8, 4) == shutdown_error && calls == before_calls);
    assert(capture.set_resolution(8, 4) == shutdown_error && calls == before_calls);
    shutdown_error = 0;
    assert(capture.restart(8, 4) == 0);
    capture.shutdown();
    puts("PASS Capture/Frame/Sampler CPU contract: lifecycle errors, leases, plane strides, JPEG ownership, first/changed frames. MMF provider is mocked; no hardware qualification.");
}