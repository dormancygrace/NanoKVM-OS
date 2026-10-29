#include <atomic>
#include <cassert>
#include <cerrno>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstdio>
#include <mutex>
#include <pthread.h>
#include <thread>
#include "capture_park.hpp"
#include "geometry_gate.hpp"

pthread_mutex_t vi_mutex = PTHREAD_MUTEX_INITIALIZER;
std::mutex event_mutex;
std::condition_variable event;
bool detector_waiting = false;
thread_local bool detector = false;
bool reader_live = false, pending_video = false, jpeg_live = false;
int restarts = 0, retired = 0, shutdowns = 0;
int receiver_calls = 0, receiver_result = 0;
struct {
    int enc_video_init = 0, mmf_venc_chn = 0;
} kvm_venc;
struct {
    std::atomic<int> reopen_cam_flag{0};
    int vpss_width = 0, vpss_height = 0, reinit_flag = 0;
    int hdmi_res_err = 0, hdmi_res_type = 0, vi_detect_state = 0;
    uint8_t hdmi_cable_state = 1, hw_version = 'p', hdmi_version = 1;
} kvmv_cfg;
constexpr int default_vpss_width = 1920, default_vpss_height = 1080, KVMV_MAX_TRY_NUM = 3;
constexpr int IMG_VENC_ERROR = -8, ERROR_RES = 1, UNSUPPORT_RES = 2;
void *video_sink = &kvmv_cfg;
void debug(const char *, ...) {}
int mmf_venc_pending_vi(int) { return pending_video ? 0 : -1; }
int mmf_del_venc_channel(int) {
    assert(!reader_live && !jpeg_live);
    pending_video = false;
    ++retired;
    return 0;
}
struct MockCapture {
    bool enabled = true;
    int result = 0, shutdown_result = 0;
    int shutdown() {
        assert(!reader_live && !jpeg_live);
        ++shutdowns;
        if (shutdown_result) return shutdown_result;
        pending_video = false;
        enabled = false;
        return 0;
    }
    int restart(int, int) {
        assert(!reader_live && !jpeg_live && !pending_video);
        enabled = true;
        ++restarts;
        return result;
    }
    int set_enabled(bool value) { enabled = value; return 0; }
} capture;
MockCapture *cam = &capture;
nanokvm::CapturePark capture_park;
namespace nanokvm {
int receiver_kind(int kind) { return kind; }
int receiver_settle_ms(int) { return 250; }
void sleep_ms(int) {}
}
int control_hdmi_receiver(uint8_t) { ++receiver_calls; return receiver_result; }
int set_capture_parked_locked(bool parked) {
    return capture_park.set(parked, [] { return 0; }, [](bool e) { return cam->set_enabled(e); });
}
int observed_lock(pthread_mutex_t *mutex) {
    if (detector) {
        // Confirm actual contention, rather than depending on scheduler timing.
        assert(pthread_mutex_trylock(mutex) == EBUSY);
        std::lock_guard<std::mutex> lock(event_mutex);
        detector_waiting = true;
        event.notify_one();
    }
    return pthread_mutex_lock(mutex);
}
#define pthread_mutex_lock observed_lock
// PRODUCTION_FUNCTIONS
#undef pthread_mutex_lock

int main() {
    nanokvm::GeometryGate geometry(1920, 1080);
    for (bool jpeg : {false, true}) {
        pthread_mutex_lock(&vi_mutex);
        assert(capture_park.set(false, [] { return 0; }, [](bool e) { return cam->set_enabled(e); }) == 0);
        auto lease = geometry.lease(geometry.snapshot());
        assert(lease);
        reader_live = true;
        jpeg_live = jpeg;
        pending_video = true;
        kvm_venc.enc_video_init = 1;
        const int before = restarts;
        detector_waiting = false;
        std::thread thread([&] { detector = true; assert(restart_capture()); });
        {
            std::unique_lock<std::mutex> lock(event_mutex);
            assert(event.wait_for(lock, std::chrono::seconds(2), [] { return detector_waiting; }));
        }
        assert(restarts == before && pending_video);
        geometry.publish(1280, 720);
        lease.release();
        jpeg_live = reader_live = false;
        pthread_mutex_unlock(&vi_mutex);
        thread.join();
        assert(restarts == before + 1 && !pending_video && !kvm_venc.enc_video_init);
    }
    assert(retired == 2);
    pthread_mutex_lock(&vi_mutex);
    assert(capture_park.set(true, [] { return 0; }, [](bool e) { return cam->set_enabled(e); }) == 0);
    pthread_mutex_unlock(&vi_mutex);
    assert(restart_capture());
    assert(capture_park.parked() && !cam->enabled);
    pthread_mutex_lock(&vi_mutex);
    assert(capture_park.set(false, [] { return 0; }, [](bool e) { return cam->set_enabled(e); }) == 0);
    assert(cam->enabled);
    kvmv_cfg.reopen_cam_flag = 1;
    assert(check_kvmv(0) == 1); // read owner already holds vi_mutex
    cam->result = -1;
    kvmv_cfg.reopen_cam_flag = 1;
    assert(check_kvmv(0) == 1 && kvmv_cfg.reopen_cam_flag == 1);
    pthread_mutex_unlock(&vi_mutex);
    cam->result = 0;
    // A disable queued behind a frame must wait for that reader before MMF
    // retires a pre-submitted encoder frame and its producer.
    pthread_mutex_lock(&vi_mutex);
    reader_live = true;
    pending_video = true;
    kvm_venc.enc_video_init = 1;
    detector_waiting = false;
    std::thread stop([&] { detector = true; assert(kvmv_hdmi_control(0) == 0); });
    {
        std::unique_lock<std::mutex> lock(event_mutex);
        assert(event.wait_for(lock, std::chrono::seconds(2), [] { return detector_waiting; }));
    }
    assert(shutdowns == 0 && pending_video);
    reader_live = false;
    pthread_mutex_unlock(&vi_mutex);
    stop.join();
    assert(shutdowns == 1 && !pending_video && !kvm_venc.enc_video_init);
    assert(capture_stopped && !capture_shutdown_pending);
    const int before = restarts, calls = receiver_calls;
    assert(!restart_capture()); // detector already queued when disable won
    reset_hdmi_for_detection();
    assert(kvmv_set_capture_parked(0) == 0 && !cam->enabled);
    assert(kvmv_read_img_locked(1920, 1080, 0, 80) == -1);
    assert(service_stopped_capture() && shutdowns == 1);
    assert(restarts == before && receiver_calls == calls);

    for (int cycle = 0; cycle < 3; ++cycle) {
        assert(kvmv_hdmi_control(1) == 0 && !capture_stopped && cam->enabled);
        assert(restarts == before + cycle + 1);
        assert(!service_stopped_capture());
        assert(kvmv_read_img_locked(1920, 1080, 0, 80) == 0);
        pending_video = true;
        kvm_venc.enc_video_init = 1;
        assert(kvmv_hdmi_control(0) == 0 && capture_stopped);
        assert(!pending_video && !kvm_venc.enc_video_init);
    }
    // A busy output remains owned, no allocation while off, then the detector
    // retries cleanup. Repeated clean-off polls are allocation/teardown free.
    assert(kvmv_hdmi_control(1) == 0);
    cam->shutdown_result = -7;
    assert(kvmv_hdmi_control(0) == -7 && capture_shutdown_pending && capture_stopped);
    assert(!restart_capture());
    assert(service_stopped_capture() && capture_shutdown_pending);
    cam->shutdown_result = 0;
    assert(service_stopped_capture() && !capture_shutdown_pending);
    const int cleaned = shutdowns;
    assert(service_stopped_capture() && shutdowns == cleaned);
    // Failed receiver reset does not start capture. A failed MMF start keeps
    // its reopen request so the next reader can retry it.
    receiver_result = -2;
    assert(kvmv_hdmi_control(1) == -2 && capture_stopped);
    receiver_result = 0;
    cam->result = -3;
    assert(kvmv_hdmi_control(1) == -1 && !capture_stopped && kvmv_cfg.reopen_cam_flag);
    cam->result = 0;
    assert(restart_capture() && !kvmv_cfg.reopen_cam_flag);
    // Detector resets remain temporary and never tear down a running stream.
    const int running_shutdowns = shutdowns;
    reset_hdmi_for_detection();
    assert(shutdowns == running_shutdowns && !capture_stopped);
    pthread_mutex_destroy(&vi_mutex);
}
