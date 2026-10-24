// Exclusive, bounded exercise of the production detector and capture library.
// Stop nanokvm-app first. Set NANOKVM_HDMI_IRQ_TRACE=1 for sample reasons.
// Never asserts reset, flashes firmware, or writes EDID or installed files.
#include "kvm_vision.h"
#include <csignal>
#include <cstdio>
#include <cinttypes>
#include <cstdlib>
#include <cstring>

static volatile sig_atomic_t stopped;
static void stop(int) { stopped = 1; }
static uint64_t now_ms() {
    timespec t{}; clock_gettime(CLOCK_MONOTONIC, &t);
    return uint64_t(t.tv_sec) * 1000 + t.tv_nsec / 1000000;
}
static unsigned read_size(const char *name) {
    char path[96]; std::snprintf(path, sizeof(path), "/run/nanokvm/%s", name);
    FILE *f = std::fopen(path, "r");
    unsigned value = 0;
    if (f) { if (std::fscanf(f, "%u", &value) != 1) value = 0; std::fclose(f); }
    return value;
}
static int discard(uintptr_t context, const uint8_t *data, uint32_t size,
                   uint32_t offset, uint32_t total) {
    if (!data || !size || offset > total || size > total - offset) return -1;
    *reinterpret_cast<uint64_t *>(context) += size;
    return 0;
}
int main(int argc, char **argv) {
    if (argc != 2 && argc != 3) return 2;
    const bool park_trial = argc == 3 && !std::strcmp(argv[2], "park");
    if (argc == 3 && !park_trial) return 2;
    unsigned seconds = std::strtoul(argv[1], nullptr, 10);
    if (seconds < 10 || seconds > 180 || (park_trial && seconds < 40)) return 2;
    setvbuf(stdout, nullptr, _IOLBF, 0);
    std::signal(SIGTERM, stop); std::signal(SIGINT, stop);
    kvmv_init(0);
    set_frame_detact(0);
    kvmv_hdmi_control(1); // normal capture admission; deassert only, never reset
    const uint64_t began = now_ms();
    uint64_t last_report = began, last_frame = began, bytes = 0, frames = 0, errors = 0;
    uint64_t previous_frames = 0;
    unsigned stalls = 0;
    bool parked = false;
    std::printf("READY duration_s=%u mono_ms=%" PRIu64 "\n", seconds, began);
    while (!stopped && now_ms() - began < uint64_t(seconds) * 1000) {
        const uint64_t elapsed = now_ms() - began;
        const bool should_park = park_trial && elapsed >= 5000 && elapsed < 30000;
        if (parked != should_park) {
            if (kvmv_set_capture_parked(should_park) != 0) { ++stalls; break; }
            parked = should_park;
            std::printf("PARK elapsed_ms=%" PRIu64 " parked=%u\n", elapsed, unsigned(parked));
        }
        const uint64_t before = bytes;
        int result = 0;
        if (parked) usleep(10000);
        else result = kvmv_read_video_sink_v2(1280, 720, 2, 2500, 30, discard,
                                             reinterpret_cast<uintptr_t>(&bytes));
        const uint64_t now = now_ms();
        if (parked) last_frame = now;
        if (result >= 0 && bytes > before) { ++frames; last_frame = now; }
        else if (!parked) { ++errors; usleep(10000); }
        if (now - last_report >= 1000) {
            std::printf("STATUS elapsed_ms=%" PRIu64 " input=%ux%u signal=%u frames=%" PRIu64
                        " delta=%" PRIu64 " errors=%" PRIu64 "\n",
                        now - began, read_size("width"), read_size("height"),
                        unsigned(kvmv_hdmi_signal_active()), frames, frames - previous_frames, errors);
            last_report = now; previous_frames = frames;
        }
        if (now - last_frame > 12000) { ++stalls; break; }
    }
    kvmv_deinit(); // joins detector and releases its GPIO request
    const bool ok = !stopped && !stalls && frames >= 100;
    std::printf("RESULT status=%s frames=%" PRIu64 " bytes=%" PRIu64
                " errors=%" PRIu64 " stalls=%u\n",
                ok ? "PASS" : "FAIL", frames, bytes, errors, stalls);
    return ok ? 0 : 1;
}
