#include <atomic>
#include <cassert>
#include <cerrno>
#include <chrono>
#include <condition_variable>
#include <cstdint>
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
int restarts = 0, retired = 0;
struct {
    int enc_video_init = 0, mmf_venc_chn = 0;
} kvm_venc;
struct {
    std::atomic<int> reopen_cam_flag{0};
    int vpss_width = 0, vpss_height = 0, reinit_flag = 0;
    uint8_t hdmi_cable_state = 1, hdmi_version = 1;
} kvmv_cfg;
bool clock_profile_ready = true;
namespace nanokvm {
bool video_clocks_ready(unsigned receiver) {
    assert(receiver <= 3);
    return clock_profile_ready;
}
}
constexpr int default_vpss_width = 1920, default_vpss_height = 1080, KVMV_MAX_TRY_NUM = 3;
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
    int result = 0;
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
    // A failed clock policy must not reopen capture or retire active leases.
    for (uint8_t receiver : {0, 3}) {
        kvmv_cfg.hdmi_version = receiver;
        clock_profile_ready = false;
        pending_video = true;
        kvm_venc.enc_video_init = 1;
        assert(!restart_capture_now());
        assert(kvmv_cfg.reopen_cam_flag == 1);
        assert(restarts == 0 && retired == 0 && pending_video && kvm_venc.enc_video_init);
    }
    clock_profile_ready = true;
    kvmv_cfg.hdmi_version = 1;
    pending_video = false;
    kvm_venc.enc_video_init = 0;
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
    pthread_mutex_destroy(&vi_mutex);
}
