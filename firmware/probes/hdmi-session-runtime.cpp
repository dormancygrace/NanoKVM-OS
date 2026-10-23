// Exclusive, bounded bench qualification. Stop nanokvm-app before running.
// capture h264|h265: Annex-B frames on fd 3, diagnostics on stdout.
// reset: three receiver reset/recovery cycles.
// edid TOOL PROFILE: pause detector, run programmer, resume and verify frames.
#include "kvm_vision.h"
#include <cerrno>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static double now() {
    timespec t{}; clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec + t.tv_nsec / 1e9;
}
struct Frames { FILE *out; size_t bytes = 0; };
static int collect(uintptr_t ctx, const uint8_t *data, uint32_t count,
    uint32_t offset, uint32_t total) {
    auto &f = *reinterpret_cast<Frames *>(ctx);
    if (!data || !count || offset > total || count > total - offset) return -1;
    f.bytes += count;
    return f.out && fwrite(data, 1, count, f.out) != count ? -1 : 0;
}
static bool frames(const char *phase, int codec, int count, FILE *out = nullptr) {
    int captured = 0, retries = 0;
    double start = now();
    Frames f{out};
    kvmv_request_keyframe();
    while ((captured < count || !kvmv_hdmi_signal_active()) && now() - start < 40) {
        int r = kvmv_read_video_sink_v2(1920, 1080, codec, 6000, 30,
            collect, reinterpret_cast<uintptr_t>(&f));
        if (r >= 0 && r != 5) ++captured;
        else { ++retries; usleep(20000); }
    }
    printf("PHASE %s codec=%d frames=%d bytes=%zu retries=%d signal=%u seconds=%.3f\n",
        phase, codec, captured, f.bytes, retries, kvmv_hdmi_signal_active(), now() - start);
    return captured >= count && f.bytes > 0 && kvmv_hdmi_signal_active();
}
static int program(const char *tool, const char *profile) {
    pid_t pid = fork();
    if (pid < 0) return -1;
    if (pid == 0) {
        execl(tool, tool, profile, static_cast<char *>(nullptr));
        _exit(127);
    }
    int status = 0;
    while (waitpid(pid, &status, 0) < 0) if (errno != EINTR) return -1;
    return WIFEXITED(status) && WEXITSTATUS(status) == 0 ? 0 : -1;
}
int main(int argc, char **argv) {
    setvbuf(stdout, nullptr, _IONBF, 0);
    if (argc < 2) return 2;
    kvmv_init(0);
    bool ok = kvmv_hdmi_control(1) == 0;
    if (ok && strcmp(argv[1], "capture") == 0 && argc == 3) {
        int codec = strcmp(argv[2], "h265") == 0 ? 2 : 1;
        FILE *out = fdopen(3, "wb");
        ok = out && frames("capture", codec, 90, out);
        if (out && fclose(out) != 0) ok = false;
    } else if (ok && strcmp(argv[1], "reset") == 0) {
        ok = frames("before-reset", 1, 15);
        for (int n = 0; ok && n < 3; ++n) {
            ok = kvmv_hdmi_control(0) == 0 && !kvmv_hdmi_signal_active();
            usleep(50000);
            ok = ok && kvmv_hdmi_control(1) == 0;
            if (ok) ok = frames("after-reset", 1, 30);
        }
    } else if (ok && strcmp(argv[1], "edid") == 0 && argc == 4) {
        ok = frames("before-edid", 1, 15);
        if (ok && kvmv_edid_maintenance(1) == 0) {
            ok = program(argv[2], argv[3]) == 0;
            ok = kvmv_edid_maintenance(0) == 0 && ok;
            if (ok) ok = frames("after-edid", 1, 60);
        } else ok = false;
    } else ok = false;
    kvmv_deinit();
    printf("RESULT status=%s\n", ok ? "PASS" : "FAIL");
    return ok ? 0 : 1;
}
