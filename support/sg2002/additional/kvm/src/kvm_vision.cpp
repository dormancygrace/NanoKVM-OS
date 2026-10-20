#include "internal/capture_rate.hpp"
#include <utility>
#include <atomic>
#include <initializer_list>
#include "stream_size.hpp"
#include "capture_pacing.hpp"
#include "mjpeg_policy.hpp"
#include "geometry_gate.hpp"
#include "capture_park.hpp"
#include "kvm_vision.h"
#include "kvm_mmf.hpp"
#include "kvm_capture.hpp"
#include "kvm_i2c.hpp"
#include "kvm_frame_sampler.hpp"
#include <cstdarg>
#include "vi_state_shared.hpp"
#include "internal/vi_state_writer.hpp"
#include "internal/small_file.hpp"
#include "internal/file_stamp.hpp"

#include <cstdlib>
#include <cstring>
#include <errno.h>
#include <sys/stat.h>

#define default_venc_chn        1

#define VENC_MJPEG              0
#define VENC_H264               1
#define VENC_H265               2

#define KVMV_MAX_TRY_NUM	   	2
#define vi_min_width            32
#define vi_min_height           3
#define vi_max_width            3840
#define vi_max_height           2160


#define default_vi_width        1920
#define default_vi_height       1080
#define default_vpss_width      1920
#define default_vpss_height     1080
#define default_h264_fps        60
#define fresh_frame_discard_count 5

#define Try_rounds_HDMI_err_res 5

#define vi_width_path           "/run/nanokvm/width"
#define vi_height_path          "/run/nanokvm/height"
#define rejected_input_path     "/run/nanokvm/rejected_input"
#define rejected_input_tmp_path "/run/nanokvm/rejected_input.tmp"
#define hdmi_mode_path          "/etc/kvm/hdmi_mode"
#define hdmi_state_path         "/proc/lt_int"
#define hdmi_signal_file_path   "/run/nanokvm/state"
#define vi_state_publish_interval_ms 10000U
#define vi_detection_active_poll_ms 10U
#define vi_detection_idle_poll_ms 100U
#define hdmi_mode_max_age_ms 1000U
/* 3840x2160 H.265 holds 102.7 MiB of ION on a NanoKVM, H.264 no more, the
 * same after bitrate, fps and codec changes: the VI blocks, three NV21 VPSS
 * buffers, the reconstruction frames, an 8 MiB bitstream and codec tables.
 * MJPEG is not available at 2160p. The 144 MiB CMA region holds it. */
#define uhd_ion_mib             104U
/* 2560x1440 at 60 Hz H.265 with MJPEG holds 53.9 MiB; the portrait profiles
 * need less (no MJPEG at 1440x2560). The QHD modes provide 54 or 56 MiB. */
#define qhd_ion_mib             54U

#define LT6911_ADDR 	0x2B

pthread_mutex_t vi_mutex;
pthread_mutex_t hdmi_signal_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_t vi_detection_thread;
static uint8_t vi_detection_thread_started = 0;

static nanokvm::I2c LT6911_i2c(4);

struct kvmv_cfg_t {
	// The detected input size is not here: it is owned by vi_geometry (geometry_gate.hpp).
	uint16_t vpss_width = default_vpss_width;
	uint16_t vpss_height = default_vpss_height;
	uint8_t venc_type;
	uint8_t stream_stop;
	uint8_t frame_detact = 0;
    uint8_t reinit_flag = 1;
    std::atomic<uint8_t> reopen_cam_flag{0};   // set by detection, taken by check_kvmv
    uint8_t hdmi_cable_state = 0;
    uint8_t try_exit_thread = 0;
    uint8_t thread_is_running = 0;
    uint8_t hdmi_version = 0;
    uint8_t hw_version = 0;
    uint8_t hdmi_stop_flag = 0;
    uint8_t hdmi_reading_flag = 0;
    uint8_t hdmi_mode = 0;
    std::atomic<uint8_t> hdmi_res_type{0};     // written by detection, read by kvmv_read_img
    std::atomic<uint8_t> hdmi_res_err{0};
    uint8_t hdmi_try_rounds = 0;
    std::atomic<uint8_t> vi_detect_state{0};
    uint8_t fresh_frame_count = 0;
};

// What one read produced; the bytes went to the caller's sink.
struct kvmv_data_t {
    uint8_t img_data_type = 0;
    uint32_t img_data_size = 0;
};

struct kvm_venc_t {
    uint8_t mmf_venc_chn;
	uint8_t enc_video_init;
    mmf_venc_cfg_t kvm_venc_cfg;
};

static nanokvm::Capture capture(default_vpss_width, default_vpss_height);
static nanokvm::Capture *const cam = &capture;

kvmv_cfg_t kvmv_cfg;
// Detected HDMI input size: published by the detection thread, snapshotted
// once per pass by kvmv_read_img. See geometry_gate.hpp for the contract.
static nanokvm::GeometryGate vi_geometry(default_vi_width, default_vi_height);

uint8_t debug_en = 0;
uint8_t last_vi_state_code = 0;
uint32_t last_vi_state_refresh_ms = 0;
uint8_t hdmi_capture_enabled = 0;
uint8_t hdmi_signal_active = 0;

static void high_rate_log_request(uint16_t width, uint16_t height, uint8_t requested_fps,
    uint8_t effective_fps)
{
    if (requested_fps <= 60) return;

    static uint16_t last_width = 0;
    static uint16_t last_height = 0;
    static uint8_t last_requested_fps = 0;
    static uint8_t last_effective_fps = 0;
    if (last_width == width && last_height == height
        && last_requested_fps == requested_fps && last_effective_fps == effective_fps) {
        return;
    }
    last_width = width;
    last_height = height;
    last_requested_fps = requested_fps;
    last_effective_fps = effective_fps;
    fprintf(stderr,
        "[kvmv] high-rate request=%ux%u requested_fps=%u effective_fps=%u "
        "(VENC rate metadata; wall pacing unmeasured)\n",
        width, height, requested_fps, effective_fps);
    fflush(stderr);
}

void debug(const char *format, ...) __attribute__((format(printf, 1, 2)));

kvm_venc_t kvm_venc;

/* With no reader, VPSS kept converting every input frame into outputs that
   nobody took ("Can't acquire VB BLK" once both buffers were held). Parked
   outputs stay configured but disabled: their pools stay allocated, so
   resuming needs no video memory; reopening a channel after idle can
   exhaust the carveout (see kvmv_hdmi_control). */
static nanokvm::CapturePark capture_park;
static int set_capture_parked_locked(bool parked);

static bool restart_capture_now()
{
    // A pipelined frame leases a VPSS buffer; retire it before the restart.
    if (kvm_venc.enc_video_init && mmf_venc_pending_vi(kvm_venc.mmf_venc_chn) >= 0)
        mmf_del_venc_channel(kvm_venc.mmf_venc_chn);
    kvm_venc.enc_video_init = 0;
    const int result = cam->restart(default_vpss_width, default_vpss_height);
    kvmv_cfg.reopen_cam_flag = result != 0;
    if (result != 0) {
        debug("[kvmv]capture restart failed: %d\n", result);
        return false;
    }
    kvmv_cfg.vpss_width = default_vpss_width;
    kvmv_cfg.vpss_height = default_vpss_height;
    kvmv_cfg.reinit_flag = 1;
    return true;
}

// Caller holds vi_mutex, which protects frame and encoder lifetimes.
// CapturePark preserves the idle state when the outputs are recreated.
static bool restart_capture_locked()
{
    bool restarted = false;
    capture_park.restart([&] { restarted = restart_capture_now(); return restarted ? 0 : -1; },
                         [](bool enabled) { return cam->set_enabled(enabled); });
    return restarted;
}

// Detector and initialization entry point. Lock order: vi_mutex -> CapturePark.
static bool restart_capture()
{
    pthread_mutex_lock(&vi_mutex);
    const bool restarted = restart_capture_locked();
    pthread_mutex_unlock(&vi_mutex);
    return restarted;
}


static bool write_hdmi_signal_file(uint8_t active)
{
    char temp_path[] = "/run/nanokvm/.state.tmp.XXXXXX";
    const char *state = active != 0 ? "1\n" : "0\n";
    const size_t state_size = 2;
    int fd = mkstemp(temp_path);
    if(fd < 0){
        debug("[hdmi] failed to create state file: %d\n", errno);
        return false;
    }

    int failure = 0;
    if(fchmod(fd, 0644) != 0){
        failure = errno;
    }

    size_t written = 0;
    while(failure == 0 && written < state_size){
        ssize_t result = write(fd, state + written, state_size - written);
        if(result > 0){
            written += (size_t)result;
        } else if(result < 0 && errno == EINTR){
            continue;
        } else {
            failure = result < 0 ? errno : EIO;
        }
    }

    if(failure == 0 && fsync(fd) != 0){
        failure = errno;
    }
    if(close(fd) != 0 && failure == 0){
        failure = errno;
    }
    if(failure == 0 && rename(temp_path, hdmi_signal_file_path) != 0){
        failure = errno;
    }
    if(failure != 0){
        unlink(temp_path);
        debug("[hdmi] failed to publish state file: %d\n", failure);
        return false;
    }
    return true;
}

static void set_hdmi_signal_state(uint8_t active)
{
    pthread_mutex_lock(&hdmi_signal_mutex);
    uint8_t enabled = __atomic_load_n(&hdmi_capture_enabled, __ATOMIC_ACQUIRE);
    uint8_t next = enabled != 0 && active != 0 ? 1 : 0;
    uint8_t previous = __atomic_exchange_n(&hdmi_signal_active, next, __ATOMIC_ACQ_REL);
    if(previous != next){
        write_hdmi_signal_file(next);
    }
    pthread_mutex_unlock(&hdmi_signal_mutex);
}

static void set_hdmi_capture_enabled(uint8_t enabled)
{
    pthread_mutex_lock(&hdmi_signal_mutex);
    __atomic_store_n(&hdmi_capture_enabled, enabled != 0 ? 1 : 0, __ATOMIC_RELEASE);
    __atomic_store_n(&hdmi_signal_active, 0, __ATOMIC_RELEASE);
    write_hdmi_signal_file(0);
    pthread_mutex_unlock(&hdmi_signal_mutex);
}

static void set_hdmi_detection_state(uint8_t active)
{
    __atomic_store_n(&kvmv_cfg.hdmi_cable_state, active != 0 ? 1 : 0, __ATOMIC_RELEASE);
    set_hdmi_signal_state(active);
}

void debug(const char *format, ...)
{
    if(debug_en){
        va_list args;
        va_start(args, format);
        vprintf(format, args);
        va_end(args);
    }
}

uint8_t refresh_vi_state()
{
	// The driver read blocks for about a second; mark the deadline before it so
	// that the publication cadence measures wall-clock time, not read time plus it.
    last_vi_state_refresh_ms = vi_state_shared::monotonic_ms();
    last_vi_state_code = vi_state_shared::refresh();
    set_hdmi_detection_state(last_vi_state_code == 1 || last_vi_state_code >= 3);
    return last_vi_state_code;
}

int maxmin_data(int _max, int _min, int _data)
{
	if(_data > _max) return _max;
	if(_data < _min) return _min;
	return _data;
}

// ====HDMI RES==================================================

uint16_t hdmi_res_list[][2] = {
    {1920, 1080},
    {3840, 2160},
    {2560, 1440},
    {720, 1280},
    {1080, 1920},
    {1088, 1920},
    {1440, 2560},
    {1600, 900},
    {1440, 1080},
    {1440, 900},
    {1280, 1024},
    {1280, 960},
    {1280, 800},
    {1280, 720},
    {1152, 864},
    {1024, 768},
    {800, 600},
	{640, 480},
};

uint16_t hdmi_unsupported_res_list[][2] = {
    {1680, 1050},
    {1440, 1050},
    {1400, 1050},
    {1368, 768},
    {1366, 768},
    {720, 576},
};

/* return 0 : normal res;
 * return 1 : new res;
 * return 2 : unsupport res;
 * return 3 : unknow res;
 */
/* Whether the video memory of this boot holds a capture of this size. */
static bool res_fits_video_memory(uint16_t _width, uint16_t _height)
{
    if (_width > 1920 || _height > 1080) {
        // Extended capture profiles require the qualified carveout, including Auto.
        uint8_t ion_size[4] = {};
        FILE *ion = fopen("/proc/device-tree/reserved-memory/ion/size", "rb");
        size_t count = ion ? fread(ion_size, 1, sizeof(ion_size), ion) : 0;
        if (ion) fclose(ion);
        uint32_t bytes = ((uint32_t)ion_size[0] << 24) | ((uint32_t)ion_size[1] << 16)
            | ((uint32_t)ion_size[2] << 8) | ion_size[3];
        const uint32_t required_mib = (_width > 2560 || _height > 2560) ? uhd_ion_mib
            : qhd_ion_mib;
        if (count != sizeof(ion_size) || bytes < required_mib * 1024U * 1024U) return false;
        // Legacy CMA pools need the same slack as the server capability gate.
        if (required_mib == uhd_ion_mib && bytes < 144U * 1024U * 1024U
                && access("/proc/device-tree/reserved-memory/ion/reusable", F_OK) == 0)
            return false;
    }
    return true;
}

uint8_t check_res(uint16_t _width, uint16_t _height)
{
    if (!res_fits_video_memory(_width, _height)) return UNSUPPORT_RES;
    uint8_t i;
    for(i = 0; i < sizeof(hdmi_res_list)/4; i++){
        if(_width == hdmi_res_list[i][0] && _height == hdmi_res_list[i][1]) return NORMAL_RES;
    }
    for(i = 0; i < sizeof(hdmi_unsupported_res_list)/4; i++){
        if(_width == hdmi_unsupported_res_list[i][0] && _height == hdmi_unsupported_res_list[i][1]) return UNSUPPORT_RES;
    }
    return UNKNOWN_RES;
}

/* A size capture supports with enough video memory (the list check_res uses). */
static bool listed_res(uint16_t _width, uint16_t _height)
{
    for (size_t i = 0; i < sizeof(hdmi_res_list) / sizeof(hdmi_res_list[0]); i++) {
        if (_width == hdmi_res_list[i][0] && _height == hdmi_res_list[i][1]) return true;
    }
    return false;
}

/* An input that the video memory of this boot cannot hold is refused without
   publishing its size, so the server only saw "unsupported resolution". Its
   size is published here instead (0x0 removes the record) for a message that
   names the video memory mode it needs. Only the detection path calls this. */
static void publish_rejected_input(uint16_t _width, uint16_t _height)
{
    // One writer: detection thread (or init before that thread starts).
    // Readers must see both dimensions from the same detection, never a
    // truncated record or one side of the preceding input.
    static int published_width = -1, published_height = -1;
    if (published_width == _width && published_height == _height) return;
    if (_width && _height) {
        char text[32];
        std::snprintf(text, sizeof(text), "%u %u\n", _width, _height);
        if (!nanokvm::write_small_file(rejected_input_tmp_path, text) ||
            rename(rejected_input_tmp_path, rejected_input_path) != 0) {
            unlink(rejected_input_tmp_path);
            return; // Retry this same size on the next detection.
        }
    } else if (unlink(rejected_input_path) != 0 && errno != ENOENT) {
        return;
    }
    published_width = _width;
    published_height = _height;
}

void write_res_to_file(uint16_t _width, uint16_t _height)
{
    nanokvm::write_small_uint(vi_width_path, _width);
    nanokvm::write_small_uint(vi_height_path, _height);
}

// Only the detection thread reads/writes the mode and owns this stamp.
static nanokvm::FileStamp hdmi_mode_stamp;

int set_hdmi_mode(uint8_t _hdmi_mode)
{
    if(_hdmi_mode <= 2){
        const bool written = nanokvm::write_small_uint(hdmi_mode_path, _hdmi_mode, true);
        // Even a failed write may have truncated the file. Own writes must be
        // visible on the next pass despite coarse filesystem timestamps.
        hdmi_mode_stamp.valid = false;
        return written ? 1 : 0;
    } else {
        debug("[kvmv] Incorrect HDMI mode.\n");
        return 0;
    }
}

// Preserve the changed-mode return value used by the capture state machine.
int get_hdmi_mode(void)
{
    struct stat st;
    if(stat(hdmi_mode_path, &st) == 0){
        const uint32_t now_ms = vi_state_shared::monotonic_ms();
        if (!nanokvm::file_stamp_due(hdmi_mode_stamp, st, now_ms, hdmi_mode_max_age_ms)) {
            return 0;
        }
        // A one-second refresh also sees same-size rewrites with an unchanged
        // timestamp; polling otherwise only stats the small configuration file.
        FILE *fp = fopen(hdmi_mode_path, "r");
        if (fp == NULL) {
            hdmi_mode_stamp.valid = false;
            kvmv_cfg.hdmi_mode = 0;
            return 0;
        }
        uint8_t RW_Data[3] = {0};
        const size_t read_size = fread(RW_Data, sizeof(char), sizeof(RW_Data) - 1, fp);
        RW_Data[read_size] = 0;
        const bool read_failed = ferror(fp) != 0;
        const int close_result = fclose(fp);
        if (read_failed || close_result != 0) {
            hdmi_mode_stamp.valid = false;
            kvmv_cfg.hdmi_mode = 0;
            return 0;
        }
        nanokvm::file_stamp_record(hdmi_mode_stamp, st, now_ms);
        uint8_t tmp8 = atoi((char*)RW_Data);
        if(tmp8 > 2) {
            tmp8 = 0;
            set_hdmi_mode(0);
        }
        if(tmp8 != kvmv_cfg.hdmi_mode){
            kvmv_cfg.hdmi_mode = tmp8;
            debug("[kvmv] hdmi mode = %d\n", kvmv_cfg.hdmi_mode);
            return 1;
        } else {
            return 0;
        }
    }
    hdmi_mode_stamp.valid = false;
    kvmv_cfg.hdmi_mode = 0;
    return 0;
}

int get_manual_resolution(void)
{
    uint8_t RW_Data[35];
    FILE *fp;
    int file_size;
    uint16_t tmp_width, tmp_height;
    int res = 0;

    // get res
    if(access("/run/nanokvm/width", F_OK) == 0){
        fp = fopen("/run/nanokvm/width", "r");
        fseek(fp, 0, SEEK_END);
        file_size = ftell(fp);
        fseek(fp, 0, SEEK_SET);
        fread(RW_Data, sizeof(char), file_size, fp);
        fclose(fp);
        RW_Data[file_size] = 0;
        tmp_width = atoi((char*)RW_Data);
    } else {
        tmp_width = 1920;
    }
    if(access("/run/nanokvm/height", F_OK) == 0){
        fp = fopen("/run/nanokvm/height", "r");
        fseek(fp, 0, SEEK_END);
        file_size = ftell(fp);
        fseek(fp, 0, SEEK_SET);
        fread(RW_Data, sizeof(char), file_size, fp);
        fclose(fp);
        RW_Data[file_size] = 0;
        tmp_height = atoi((char*)RW_Data);
    } else {
        tmp_height = 1080;
    }

    // res min limit
    if(tmp_width < vi_min_width){
        tmp_width = vi_min_width;
        nanokvm::write_small_uint(vi_width_path, vi_min_width);
    }
    if(tmp_height < vi_min_height){
        tmp_height = vi_min_height;
        nanokvm::write_small_uint(vi_height_path, vi_min_height);
    }
    // res max limit
    if(tmp_width > vi_max_width){
        tmp_width = vi_max_width;
        nanokvm::write_small_uint(vi_width_path, vi_max_width);
    }
    // A size that the video memory cannot hold (3840x2160 on a pool under
    // 104 MiB) fails the capture start with "Out of memory"; the file can
    // still ask for it after the computer switched to such a mode. Start at
    // the default size and let detection report it.
    if(!res_fits_video_memory(tmp_width, tmp_height)){
        tmp_width = default_vi_width;
        tmp_height = default_vi_height;
        nanokvm::write_small_uint(vi_width_path, tmp_width);
        nanokvm::write_small_uint(vi_height_path, tmp_height);
    }
    const bool portrait = (tmp_width == 720 && tmp_height == 1280)
        || (tmp_width == 1080 && tmp_height == 1920)
        || (tmp_width == 1088 && tmp_height == 1920)
        || (tmp_width == 1440 && tmp_height == 2560);
    if(tmp_height > vi_max_height && !portrait){
        tmp_height = vi_max_height;
        nanokvm::write_small_uint(vi_height_path, vi_max_height);
    }

    // res change ? (against the last size published)
    if(vi_geometry.publish(tmp_width, tmp_height)){
        printf("[kvmk] get new resolution = %d x %d\n", tmp_width, tmp_height);
        res = 1;
    }
    return res;
}

uint8_t auto_try_res()
{
    uint8_t err_code;
    uint8_t auto_trying_times = 0;

    for (auto_trying_times = 0; auto_trying_times < sizeof(hdmi_res_list)/4; auto_trying_times++){
        err_code = refresh_vi_state();
        switch(err_code){
        case 0:
            // shouldn't be possible to run here
            restart_capture();
            printf("[kvmv] VI not init\n");
            break;
        case 1:
            printf("[kvmv] VI subsystem is normal\n");
            return 1;
            break;
        case 2:
            // HDMI not detected or resolution not supported; interval checks will continue
            printf("[kvmv] Cannot obtain HDMI input\n");
            auto_trying_times--;
            nanokvm::sleep_ms(1000);
            break;
        case 3: // width too small
        case 4: // width too large
        case 5: // height too small
        case 6: // height too large
            // CSI abnormal due to resolution error
            // The test list is short; sequential testing can be performed
            // Not with a size the video memory cannot hold: starting the
            // capture at it fails with "Out of memory" and ends the process.
            if (!res_fits_video_memory(hdmi_res_list[auto_trying_times][0], hdmi_res_list[auto_trying_times][1])) break;
            printf("[kvmv] Trying %d * %d res ..\n", hdmi_res_list[auto_trying_times][0], hdmi_res_list[auto_trying_times][1]);
            nanokvm::write_small_uint(vi_width_path, hdmi_res_list[auto_trying_times][0]);
            nanokvm::write_small_uint(vi_height_path, hdmi_res_list[auto_trying_times][1]);

            vi_geometry.publish(hdmi_res_list[auto_trying_times][0], hdmi_res_list[auto_trying_times][1]);
            printf("[kvmv] restart cam...\n");
            restart_capture();
            nanokvm::sleep_ms(50);
            break;
        case 7: // Unknown reason
            printf("[kvmv] CSI abnormal: Unknown reason\n");
            break;
        }
    }
    err_code = refresh_vi_state();
    if (err_code == 1 || err_code == 2) return err_code;
    return 0;
}

void lt6911_enable()
{
	uint8_t buf[2];

    if(kvmv_cfg.hdmi_version == 3){
        return;
    }

	buf[0] = 0xff;
	buf[1] = 0x80;
	if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return ;

	buf[0] = 0xee;
	buf[1] = 0x01;
	if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return ;

    if(kvmv_cfg.hdmi_version == 1){
        // disable watchdog
        buf[0] = 0x10;
        buf[1] = 0x00;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return ;
    }
}

void lt6911_disable()
{
	uint8_t buf[2];

    if(kvmv_cfg.hdmi_version == 3){
        return;
    }

	buf[0] = 0xff;
	buf[1] = 0x80;
	if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return ;

	buf[0] = 0xee;
	buf[1] = 0x00;
	if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return ;
}

uint8_t lt6911_get_hdmi_res()
{
	uint8_t buf[2];
	uint8_t revbuf[4];
	uint16_t Vactive;
	uint16_t Hactive;


    if(kvmv_cfg.hdmi_version == 0){
        // LT6911C
        buf[0] = 0xff;
        buf[1] = 0xd2;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return 0;

        buf[0] = 0x83;
        buf[1] = 0x11;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return 0;

        nanokvm::sleep_ms(5);

        // Vactive
        buf[0] = 0x96;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return 0;
        uint8_t dat0[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat0, sizeof(dat0))) return 0;

        // Hactive
        buf[0] = 0x8b;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return 0;
        uint8_t dat1[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat1, sizeof(dat1))) return 0;

        revbuf[0] = (uint8_t)dat0[0];
        revbuf[1] = (uint8_t)dat0[1];
        revbuf[2] = (uint8_t)dat1[0];
        revbuf[3] = (uint8_t)dat1[1];

        Vactive = (revbuf[0] << 8)|revbuf[1];
        Hactive = (revbuf[2] << 8)|revbuf[3];
        Hactive *= 2;

        debug("[hdmi]HDMI res modification event\n");
        debug("[hdmi]new res: %d * %d\n", Hactive, Vactive);


        if (Vactive != 0 && Hactive != 0){
            return 1;
        } else {
            return 0;
        }
    } else if (kvmv_cfg.hdmi_version == 1){
        // LT6911UXC
        buf[0] = 0xff;
        buf[1] = 0x86;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return 0;

        buf[0] = 0xff;
        buf[1] = 0x86;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return 0;

        // HDMI signal disappear/stable
        buf[0] = 0xA3;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return 0;
        uint8_t dat0[1];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat0, sizeof(dat0))) return 0;

        revbuf[0] = (uint8_t)dat0[0];

        debug("[hdmi]HDMI-UXC res modification event\n");

        if(revbuf[0] == 0x55)       return 1;
        else if(revbuf[0] == 0x88)  return 0;
        else return 0;
    } else if (kvmv_cfg.hdmi_version == 3){
        // LT6911D
        buf[0] = 0xff;
        buf[1] = 0xe0;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return 0;

        // Hactive
        buf[0] = 0x8c;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return 0;
        uint8_t dat0[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat0, sizeof(dat0))) return 0;

        // Vactive
        buf[0] = 0x8e;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return 0;
        uint8_t dat1[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat1, sizeof(dat1))) return 0;

        revbuf[0] = (uint8_t)dat0[0];
        revbuf[1] = (uint8_t)dat0[1];
        revbuf[2] = (uint8_t)dat1[0];
        revbuf[3] = (uint8_t)dat1[1];

        Hactive = ((revbuf[0] << 8)|revbuf[1]) * 2;
        Vactive = (revbuf[2] << 8)|revbuf[3];

        debug("[hdmi]HDMI-D res modification event\n");
        debug("[hdmi]new res: %d * %d\n", Hactive, Vactive);


        if(Hactive != 0 || Vactive != 0) return 1;
        else return 0;
    } else {
        return 0;
    }
}

uint8_t lt6911_get_csi_res(uint16_t *p_width, uint16_t *p_height)
{
	uint8_t buf[2];
	uint8_t revbuf[4];
    uint8_t res_type;
	static uint16_t old_Vactive;
	static uint16_t old_Hactive;
	uint16_t Vactive;
	uint16_t Hactive;

    if(kvmv_cfg.hdmi_version == 0){
        // LT6911C
        buf[0] = 0xff;
        buf[1] = 0xc2;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return UNKNOWN_RES;

        // Vactive
        buf[0] = 0x06;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return UNKNOWN_RES;
        uint8_t dat0[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat0, sizeof(dat0))) return UNKNOWN_RES;

        // Hactive
        buf[0] = 0x38;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return UNKNOWN_RES;
        uint8_t dat1[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat1, sizeof(dat1))) return UNKNOWN_RES;

        revbuf[0] = (uint8_t)dat0[0];
        revbuf[1] = (uint8_t)dat0[1];
        revbuf[2] = (uint8_t)dat1[0];
        revbuf[3] = (uint8_t)dat1[1];


        Vactive = (revbuf[0] << 8)|revbuf[1];
        Hactive = (revbuf[2] << 8)|revbuf[3];
    } else if(kvmv_cfg.hdmi_version == 1) {
        // LT6911UXC
        debug("[hdmi]UXC get csi res\n");
        buf[0] = 0xff;
        buf[1] = 0x85;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return UNKNOWN_RES;

        // Vactive
        buf[0] = 0xF0;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return UNKNOWN_RES;
        uint8_t dat0[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat0, sizeof(dat0))) return UNKNOWN_RES;

        // Hactive
        buf[0] = 0xEA;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return UNKNOWN_RES;
        uint8_t dat1[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat1, sizeof(dat1))) return UNKNOWN_RES;

        revbuf[0] = (uint8_t)dat0[0];
        revbuf[1] = (uint8_t)dat0[1];
        revbuf[2] = (uint8_t)dat1[0];
        revbuf[3] = (uint8_t)dat1[1];


        Vactive = (revbuf[0] << 8)|revbuf[1];
        Hactive = (revbuf[2] << 8)|revbuf[3];
    } else if(kvmv_cfg.hdmi_version == 3) {
        // LT6911D
        debug("[hdmi]D get csi res\n");
        buf[0] = 0xff;
        buf[1] = 0xe0;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 2)) return UNKNOWN_RES;

        // Vactive
        buf[0] = 0x8e;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return UNKNOWN_RES;
        uint8_t dat0[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat0, sizeof(dat0))) return UNKNOWN_RES;

        // Hactive
        buf[0] = 0x8c;
        if (!LT6911_i2c.writeto(LT6911_ADDR, buf, 1)) return UNKNOWN_RES;
        uint8_t dat1[2];
        if (!LT6911_i2c.readfrom(LT6911_ADDR, dat1, sizeof(dat1))) return UNKNOWN_RES;

        revbuf[0] = (uint8_t)dat0[0];
        revbuf[1] = (uint8_t)dat0[1];
        revbuf[2] = (uint8_t)dat1[0];
        revbuf[3] = (uint8_t)dat1[1];


        Vactive = (revbuf[0] << 8)|revbuf[1];
        Hactive = ((revbuf[2] << 8)|revbuf[3]) * 2;
    } else {
        return UNKNOWN_RES;
    }

    res_type = check_res(Hactive, Vactive);
    if(res_type == NORMAL_RES)
    {
        if(old_Hactive != Hactive || old_Vactive != Vactive){
            old_Hactive = Hactive;
            old_Vactive = Vactive;
            *p_width = Hactive;
            *p_height = Vactive;
            res_type = NEW_RES;
        }
    }

    switch (res_type)
    {
    case NORMAL_RES:
        debug("[hdmi] get res : %d * %d\n", Hactive, Vactive);
        publish_rejected_input(0, 0);
        break;
    case NEW_RES:
        printf("[hdmi] get new res : %d * %d\n", Hactive, Vactive);
        write_res_to_file(Hactive, Vactive);
        publish_rejected_input(0, 0);
        break;
    case UNSUPPORT_RES:
        printf("[hdmi] get unsupport res : %d * %d\n", Hactive, Vactive);
        // Only a listed size: another mode would not help an unlisted one.
        if (!res_fits_video_memory(Hactive, Vactive) && listed_res(Hactive, Vactive))
            publish_rejected_input(Hactive, Vactive);
        else
            publish_rejected_input(0, 0);
        break;
    case UNKNOWN_RES:
        printf("[hdmi] get unknown res : %d * %d\n", Hactive, Vactive);
        break;
    }

	return res_type;
}

/* The one place where the detection thread reports a new input size: it is
   read into locals and published through the gate; the size files are already
   written when lt6911_get_csi_res returns. Called by the detection thread, and
   by kvmv_init before that thread exists. */
static uint8_t detect_csi_res()
{
    uint16_t width = 0, height = 0;
    const uint8_t res_type = lt6911_get_csi_res(&width, &height);
    if (res_type == NEW_RES) vi_geometry.publish(width, height);
    kvmv_cfg.hdmi_res_type = res_type;
    return res_type;
}

// ==============================================================

void get_hdmi_version()
{
	FILE *fp;
	uint8_t RW_Data[2];
    system("/kvmapp/system/init.d/S15kvmhwd get_hdmi_version");
	if(access("/etc/kvm/hdmi_version", F_OK) == 0){
        fp = fopen("/etc/kvm/hdmi_version", "r");
        fread(RW_Data, sizeof(char), 2, fp);
        fclose(fp);
        if(RW_Data[0] == 'u'){
            // 6911uxc; S15kvmhwd publishes "ux"
            kvmv_cfg.hdmi_version = 1;
            debug("[hdmi]HDMI-UX exist!\n");
        } else if(RW_Data[0] == 'd'){
            // 6911d
            kvmv_cfg.hdmi_version = 3;
            debug("[hdmi]HDMI-D exist!\n");
        } else {
            // 6911c
            kvmv_cfg.hdmi_version = 0;
            debug("[hdmi]HDMI-C exist!\n");
        }
        RW_Data[0] = 0;
    } else {
        kvmv_cfg.hdmi_version = 0;
    }
}

void* vi_subsystem_detection(void *)
{
	FILE *fp;
    uint8_t tmp8;
    uint8_t rising_times = 0;
    uint8_t falling_times = 0;
    uint8_t auto_change_mode = 0;
    kvmv_cfg.thread_is_running = 1;
    // The vendor kernel's LT6911 edge counter. Mainline kernels have none and
    // nothing creates it later, so look once and otherwise poll the receiver.
    const bool hdmi_edge_counter = access(hdmi_state_path, F_OK) == 0;
    if (!hdmi_edge_counter) debug("[hdmi]%s absent, polling the receiver\n", hdmi_state_path);

    get_hdmi_version();

    // Refresh on elapsed time, not loop iterations whose work varies widely.
    last_vi_state_refresh_ms = vi_state_shared::monotonic_ms() - vi_state_publish_interval_ms;
    uint64_t last_hdmi_poll_ms = vi_state_shared::monotonic_ms() - 1000U;
    while(true)
    {
        if(__atomic_load_n(&kvmv_cfg.try_exit_thread, __ATOMIC_ACQUIRE) == 1)
            break;

        uint8_t get_new_hdmi_mode = get_hdmi_mode();
        uint8_t try_res;
        uint8_t err_code;
        uint8_t vi_state_refreshed = 0;
        uint8_t vi_state_due = vi_state_shared::monotonic_ms() - last_vi_state_refresh_ms >= vi_state_publish_interval_ms;
        uint8_t manual_vi_init_now =
            (kvmv_cfg.hdmi_mode == 2 &&
             (get_new_hdmi_mode == 1 || kvmv_cfg.vi_detect_state == 0));
        uint8_t defer_vi_state_refresh =
            (kvmv_cfg.hdmi_mode == 1 &&
             (kvmv_cfg.vi_detect_state == 1 || get_new_hdmi_mode == 1)) ||
            (kvmv_cfg.hdmi_mode == 2 &&
             (manual_vi_init_now || kvmv_cfg.vi_detect_state == 1));

        if (vi_state_due && !defer_vi_state_refresh) {
            refresh_vi_state();
            vi_state_refreshed = 1;
        }

        switch (kvmv_cfg.hdmi_mode){
        case 0:
            // Switching to Mode 0 requires restarting HDMI (effective only for PCIe version)
            // Handling of automatic detection situations
            if(get_new_hdmi_mode == 1){
                kvmv_cfg.vi_detect_state = 0;
                // reset hdmi_state
                if (hdmi_edge_counter) nanokvm::write_small_uint(hdmi_state_path, 0);
                // reset hdmi
                kvmv_hdmi_control(0);
                nanokvm::sleep_ms(10);
                kvmv_hdmi_control(1);
            }
            // Initialize VI with I2C information after HDMI insertion
            tmp8 = 0;
            bool hdmi_poll_fallback;
            hdmi_poll_fallback = false;
            if (hdmi_edge_counter) {
                fp = fopen(hdmi_state_path, "r+");
                if (fp != NULL) {
                    char events[3] = {0};
                    fread(events, 1, sizeof(events) - 1, fp);
                    tmp8 = static_cast<uint8_t>(atoi(events));
                    if (tmp8 != 0) fputs("0", fp);
                    fclose(fp);
                }
            } else {
                // Without the edge counter, re-read bridge geometry at a
                // bounded rate; the existing NEW_RES path requests capture
                // reconfiguration.
                uint64_t now = vi_state_shared::monotonic_ms();
                if (now - last_hdmi_poll_ms >= 1000U) {
                    last_hdmi_poll_ms = now;
                    hdmi_poll_fallback = true;
                    tmp8 = 11; // Check both C rising and UXC/D falling paths.
                }
            }
            {
                if(tmp8 != 0){
                    // count edge ints
                    rising_times = tmp8%10;   // RISING times
                    falling_times = tmp8/10;   // FALLING times

                    if(kvmv_cfg.hdmi_stop_flag != 1){
                        kvmv_cfg.hdmi_reading_flag = 1;
                        if(kvmv_cfg.hdmi_version == 0){
                            // LT6911C
                            if(rising_times != 0){
                                lt6911_enable();
                                if(lt6911_get_hdmi_res()){
                                    // hdmi get res
                                    debug("[hdmi] C HDMI cable insertion!\n");
                                    set_hdmi_detection_state(1);
                                    detect_csi_res();
                                    if (kvmv_cfg.hdmi_res_type == NEW_RES) kvmv_cfg.reopen_cam_flag = 1;
                                    else if (kvmv_cfg.hdmi_res_type == UNKNOWN_RES && !hdmi_poll_fallback){
                                        /* Move HDMI resolution modification directly
                                            to mode1 to solve deadlock problem */
                                        auto_change_mode = 1;
                                        set_hdmi_mode(1);
                                    }
                                } else {
                                    // HDMI res = 0*0/x*0
                                    debug("[hdmi] C HDMI cable unplugged!\n");
                                    set_hdmi_detection_state(0);
                                }
                                lt6911_disable();
                            }
                        } else if (kvmv_cfg.hdmi_version == 1){
                            // LT6911UXC
                                debug("[hdmi] UXC int\n");
                            if(falling_times != 0){
                                debug("[hdmi] UXC int && \n");
                                lt6911_enable();
                                if(lt6911_get_hdmi_res()){
                                    // hdmi get res
                                    debug("[hdmi] UXC HDMI cable insertion!\n");
                                    set_hdmi_detection_state(1);
                                    detect_csi_res();
                                    if (kvmv_cfg.hdmi_res_type == NEW_RES) kvmv_cfg.reopen_cam_flag = 1;
                                    else if (kvmv_cfg.hdmi_res_type == UNKNOWN_RES && !hdmi_poll_fallback){
                                        /* Move HDMI resolution modification directly
                                            to mode1 to solve deadlock problem */
                                        auto_change_mode = 1;
                                        set_hdmi_mode(1);
                                    }
                                } else {
                                    // HDMI res = 0*0/x*0
                                    debug("[hdmi] UXC HDMI cable unplugged!\n");
                                    set_hdmi_detection_state(0);
                                }
                                lt6911_disable();
                            }
                        } else if (kvmv_cfg.hdmi_version == 3){
                            // LT6911D
                                debug("[hdmi] D int\n");
                            if(falling_times != 0){
                                debug("[hdmi] D int && \n");
                                lt6911_enable();
                                if(lt6911_get_hdmi_res()){
                                    // hdmi get res
                                    debug("[hdmi] D HDMI cable insertion!\n");
                                    set_hdmi_detection_state(1);
                                    detect_csi_res();
                                    if (kvmv_cfg.hdmi_res_type == NEW_RES) kvmv_cfg.reopen_cam_flag = 1;
                                    else if (kvmv_cfg.hdmi_res_type == UNKNOWN_RES && !hdmi_poll_fallback){
                                        /* Move HDMI resolution modification directly
                                            to mode1 to solve deadlock problem */
                                        auto_change_mode = 1;
                                        set_hdmi_mode(1);
                                    }
                                } else {
                                    // HDMI res = 0*0/x*0
                                    debug("[hdmi] D HDMI cable unplugged!\n");
                                    set_hdmi_detection_state(0);
                                }
                                lt6911_disable();
                            }
                        } else {
                            debug("[hdmi] Chip not supported for reading \n");
                        }
                        kvmv_cfg.hdmi_reading_flag = 0;
                    }
                }
            }
            break;
        /* Mode 1 & 2 will disable API access to the image during detection,
           and will output -4: Modifying image resolution, please wait.*/
        case 1: // Automatically trying common resolutions
            /* kvmv_cfg.vi_detect_state :
             * 0: HDMI standard mode, detection program does not interfere with the camera
             * 1: Preparing / Testing in progress
             * 2: Test completed: Suitable resolution found,
             */

            if(get_new_hdmi_mode == 1){
                kvmv_cfg.vi_detect_state = 1;
            }
            if(kvmv_cfg.vi_detect_state == 1){
                try_res = auto_try_res();
                if (try_res == 1) {
                    kvmv_cfg.hdmi_res_err = NORMAL_RES;
                    kvmv_cfg.hdmi_try_rounds = 0;
                    kvmv_cfg.vi_detect_state = 2;
                } else if (try_res == 0) {
                    /*  There may be a situation where the correct resolution cannot be recognized
                        after one round of detection. By default, Try_rounds_HDMI_err_res rounds will be detected,
                        and if it cannot be detected, jump to the next mode */
                    kvmv_cfg.hdmi_res_err = ERROR_RES;
                    kvmv_cfg.hdmi_try_rounds++;
                    if(kvmv_cfg.hdmi_try_rounds >= Try_rounds_HDMI_err_res){
                        kvmv_cfg.hdmi_try_rounds = 0;
                        kvmv_cfg.vi_detect_state = 1;
                        if(auto_change_mode == 1){
                            auto_change_mode = 0;
                            set_hdmi_mode(0);
                        } else {
                            set_hdmi_mode(2);
                        }
                    }
                } else if (try_res == 2) {
                    kvmv_cfg.hdmi_try_rounds = 0;
                    // Cannot obtain HDMI input / No signal on HDMI
                }
            } else if (kvmv_cfg.vi_detect_state == 2){
                // Low-frequency detection of HDMI status, no log output
                if (vi_state_refreshed && last_vi_state_code != 1) {
                    kvmv_cfg.vi_detect_state = 1;
                }
            } else {
                kvmv_cfg.vi_detect_state = 1;
            }
            break;
        case 2:
            // Manually initialize VI.
            if (manual_vi_init_now) {
                // Initialize immediately when entering manual mode instead of
                // waiting for the next periodic state refresh.
                kvmv_cfg.vi_detect_state = 1;
            }
            if(manual_vi_init_now || vi_state_due){
                if (kvmv_cfg.vi_detect_state == 1){
                    // detect_res
                    if (get_manual_resolution()) {
                        debug("[kvmv] restart cam...\n");
                        restart_capture();
                    }

                    // Sample after a manual resolution change.
                    err_code = refresh_vi_state();
                    switch(err_code){
                    case 0:
                        debug("[kvmv] VI not init\n");
                        break;
                    case 1:
                        debug("[kvmv] HDMI and CSI status are normal\n");
                        kvmv_cfg.vi_detect_state = 2;
                        break;
                    case 2:
                        debug("[kvmv] HDMI abnormal\n");
                        break;
                    case 3:
                        debug("[kvmv] CSI abnormal: width too small\n");
                        break;
                    case 4:
                        debug("[kvmv] CSI abnormal: width too large\n");
                        break;
                    case 5:
                        debug("[kvmv] CSI abnormal: height too small\n");
                        break;
                    case 6:
                        debug("[kvmv] CSI abnormal: height too large\n");
                        break;
                    case 7:
                        debug("[kvmv] CSI abnormal: Unknown reason\n");
                        break;
                    }
                } else if (kvmv_cfg.vi_detect_state == 2){
                    // detection of HDMI status, no log output
                    err_code = last_vi_state_code;
                    if (err_code != 1) kvmv_cfg.vi_detect_state = 1;
                } else {
                    kvmv_cfg.vi_detect_state = 1;
                }
            }
            break;

        default:
            debug("Non-existent hdmi state = %d\n", kvmv_cfg.hdmi_mode);
            break;
        }

		const uint32_t poll_interval_ms =
			(kvmv_cfg.hdmi_mode == 0 || kvmv_cfg.vi_detect_state == 2)
				? vi_detection_idle_poll_ms
				: vi_detection_active_poll_ms;
		nanokvm::sleep_ms(poll_interval_ms);
    }
    kvmv_cfg.thread_is_running = 0;
    return NULL;
}

uint8_t frame_changed(nanokvm::Nv21Frame *raw)
{
    static nanokvm::FrameSampler sampler;
    return raw == nullptr || sampler.changed(raw->data(), raw->data_size(), raw->width(), raw->height());
}

// Per-calling-thread intent; actual encoder access remains serialized by vi_mutex.
static thread_local kvmv_video_sink video_sink = nullptr;
static thread_local uintptr_t video_sink_context = 0;

bool jpg_dump(kvmv_data_t* dump_to, nanokvm::JpegBuffer *raw)
{
    if(dump_to == NULL || raw == NULL || raw->data() == NULL || raw->data_size() == 0
            || raw->data_size() > UINT32_MAX || video_sink == nullptr){
        return false;
    }
    dump_to->img_data_size = raw->data_size();
    dump_to->img_data_type = VENC_MJPEG;
    return video_sink(video_sink_context, raw->data(), dump_to->img_data_size,
                      0, dump_to->img_data_size) == 0;
}

// Protected by vi_mutex; GUI intent is applied by the next capture, never by
// the HTTP thread. Wide video keeps channel 1; native MJPEG fits channel 0.
static bool mjpeg_422_disabled = false;
static unsigned mjpeg_422_frame_failures = 0;
static int mjpeg_chroma_choice = -1; // Legacy environment default until explicitly selected.
static uint64_t mjpeg_last_video_ms = 0, mjpeg_last_jpeg_ms = 0;
static unsigned mjpeg_last_video_width = 0, mjpeg_last_jpeg_width = 0;
static bool mjpeg_resolution_limited = false;
// MJPEG's own output next to a video stream: a little above the rate the
// server reads it at (mjpegNextToVideoFPS 27, next to a stream wider than
// 1920 mjpegNextToWideVideoFPS 25).
static constexpr int mjpeg_next_to_video_fps = 30;
static constexpr int mjpeg_next_to_wide_video_fps = 27;
static uint64_t mjpeg_monotonic_ms()
{
    timespec time{};
    clock_gettime(CLOCK_MONOTONIC, &time);
    return static_cast<uint64_t>(time.tv_sec) * 1000 + time.tv_nsec / 1000000;
}
// The environment switches, read on first use (-1: not read yet). The
// server's environment does not change at runtime; callers hold vi_mutex.
static int mjpeg_422_env = -1, mjpeg_force_copy_env = -1;
static bool mjpeg_422_requested()
{
    if (mjpeg_chroma_choice >= 0) return mjpeg_chroma_choice == 1;
    if (mjpeg_422_env < 0) {
        const char *requested = getenv("NANOKVM_MJPEG_422");
        mjpeg_422_env = !requested || strcmp(requested, "0") != 0;
    }
    return mjpeg_422_env;
}
static bool mjpeg_video_reader_active(uint64_t now_ms)
{
    return nanokvm::reader_active(mjpeg_last_video_ms, now_ms);
}
static bool mjpeg_jpeg_reader_active(uint64_t now_ms)
{
    return nanokvm::reader_active(mjpeg_last_jpeg_ms, now_ms);
}
static bool mjpeg_force_copy()
{
    if (mjpeg_force_copy_env < 0) {
        const char *copy = getenv("NANOKVM_DIAGNOSTIC_FORCE_COPY");
        mjpeg_force_copy_env = copy && strcmp(copy, "1") == 0;
    }
    return mjpeg_force_copy_env;
}
static void disable_mjpeg_422(const char *reason)
{
    if (!mjpeg_422_disabled) {
        fprintf(stderr, "[kvmv] MJPEG 4:2:2 fallback until explicit retry or restart: %s\n", reason);
        mjpeg_422_disabled = true;
    }
}
static nanokvm::StreamSize mjpeg_parallel_size(nanokvm::StreamSize size, uint8_t codec,
                                              bool frame_detect, uint64_t now_ms)
{
    if (codec != VENC_MJPEG) return size;
    mjpeg_resolution_limited = mjpeg_422_requested() && !mjpeg_422_disabled
        && !frame_detect && !mjpeg_force_copy() && size.width > 1920
        && mjpeg_last_video_width > 1920 && mjpeg_video_reader_active(now_ms);
    return mjpeg_resolution_limited ? nanokvm::limit_stream_width(size, 1920) : size;
}

static int mjpeg_capture_format(uint8_t codec, bool frame_detect, uint64_t now_ms, unsigned width)
{
    if (codec == VENC_H264 || codec == VENC_H265) {
        mjpeg_last_video_ms = now_ms;
        mjpeg_last_video_width = width;
    } else if (codec == VENC_MJPEG) {
        mjpeg_last_jpeg_ms = now_ms;
        mjpeg_last_jpeg_width = width;
    }
    // In 4:2:0 capture (H.264 straight from capture) MJPEG keeps its own
    // output too: full-range BT.601 for JPEG decoders, the VPSS converting
    // the capture's 4:2:0 (its chroma resolution stays 4:2:0).
    if (!mjpeg_422_requested() || mjpeg_422_disabled || codec != VENC_MJPEG
        || frame_detect || mjpeg_force_copy()
        || (width > 1920 && mjpeg_last_video_width > 1920 && mjpeg_video_reader_active(now_ms)))
        return nanokvm::nv21_format();
    return nanokvm::nv16_format();
}
int set_mjpeg_chroma(uint8_t enable_422)
{
    if (enable_422 > 1) return -1;
    pthread_mutex_lock(&vi_mutex);
    mjpeg_chroma_choice = enable_422;
    mjpeg_422_disabled = false; // An explicit GUI retry can recover without a reboot.
    mjpeg_422_frame_failures = 0;
    pthread_mutex_unlock(&vi_mutex);
    return 0;
}
uint8_t get_mjpeg_chroma_status()
{
    pthread_mutex_lock(&vi_mutex);
    uint8_t state = 0;
    const bool capture_420 = mmf_vi_yuv420_active() != 0;
    if (cam->has_format(nanokvm::nv16_format()) && mjpeg_422_requested() && !capture_420
        && !mjpeg_422_disabled && kvmv_cfg.frame_detact == 0) state |= MJPEG_CHROMA_ACTIVE_422;
    if (capture_420) state |= MJPEG_CHROMA_CAPTURE_420;
    if (mjpeg_422_disabled) state |= MJPEG_CHROMA_FALLBACK_ERROR;
    if (mjpeg_resolution_limited && mjpeg_video_reader_active(mjpeg_monotonic_ms()))
        state |= MJPEG_CHROMA_LIMIT_WIDTH;
    if (mjpeg_last_jpeg_width > 1920 && mjpeg_last_video_width > 1920
        && mjpeg_video_reader_active(mjpeg_monotonic_ms())) state |= MJPEG_CHROMA_SHARED_VIDEO;
    if (kvmv_cfg.frame_detact != 0) state |= MJPEG_CHROMA_FRAME_DETECT;
    if (mjpeg_force_copy()) state |= MJPEG_CHROMA_FORCE_COPY;
    pthread_mutex_unlock(&vi_mutex);
    return state;
}

// Called with vi_mutex held. On MJPEG's own output (outside_lock) the frame
// is the JPEG reader's alone: vi_mutex is released after the submit, so the
// video stream reads on while the JPEG encodes, and the function returns
// with vi_mutex released. mmf_jpg_hold keeps the encoder and the frame from
// a teardown meanwhile. Otherwise vi_mutex stays held throughout.
static int8_t frame_to_jpeg(int vi_ch, kvmv_data_t* dump_to, uint16_t quality, bool outside_lock)
{
    mmf_jpg_hold();
    int8_t result = IMG_MJPEG_TYPE;
    int ret = mmf_enc_jpg_push_vi_with_quality(0, vi_ch, quality);
    if (outside_lock) pthread_mutex_unlock(&vi_mutex);
    uint8_t *data = NULL;
    int data_size = 0;
    if (ret != 0) {
        result = IMG_VENC_ERROR;
    } else if ((ret = mmf_enc_jpg_pop(0, &data, &data_size)) != 0 || data == NULL || data_size <= 0) {
        mmf_enc_jpg_deinit(0);
        result = IMG_VENC_ERROR;
    } else {
        // The sink copies before the encoder buffer is returned.
        int copied = video_sink(video_sink_context, data, data_size, 0, data_size);
        dump_to->img_data_size = data_size;
        dump_to->img_data_type = VENC_MJPEG;
        if (mmf_enc_jpg_free(0) != 0) {
            mmf_enc_jpg_deinit(0);
            dump_to->img_data_size = 0;
            dump_to->img_data_type = 0;
            result = IMG_VENC_ERROR;
        } else if (copied != 0) {
            result = IMG_BUFFER_FULL;
        }
    }
    mmf_vi_frame_release(vi_ch);
    mmf_jpg_unhold();
    return result;
}

uint8_t kvmvenc_fps = default_h264_fps;

int init_venc_video(uint16_t width, uint16_t height, uint16_t bitrate, uint8_t codec,
	uint8_t fps)
{
	mmf_venc_cfg_t cfg{};
	cfg.type = codec == VENC_H265 ? 1 : 2; // MMF/PAYLOAD_TYPE_E: 1 h265, 2 h264
    cfg.w = width;
    cfg.h = height;
    cfg.fmt = nanokvm::nv21_format();
	cfg.intput_fps = fps;
	cfg.output_fps = fps;
	cfg.bitrate = bitrate;

    kvm_venc.mmf_venc_chn = default_venc_chn;
	kvm_venc.enc_video_init = 0;
    kvm_venc.kvm_venc_cfg = cfg;

	int ret = mmf_del_venc_channel(kvm_venc.mmf_venc_chn);
	if (ret != 0) {
		fprintf(stderr,
			"[kvmv] VENC teardown failed: channel=%d codec=%d bitrate=%u error=%#x\n",
			kvm_venc.mmf_venc_chn, codec, bitrate, ret);
		fflush(stderr);
		return ret;
	}
	ret = mmf_add_venc_channel_v2(kvm_venc.mmf_venc_chn, &kvm_venc.kvm_venc_cfg);
	if (ret != 0) {
		fprintf(stderr,
			"[kvmv] VENC initialization failed: channel=%d codec=%d "
			"size=%ux%u bitrate=%u error=%#x\n",
			kvm_venc.mmf_venc_chn, codec, width, height, bitrate, ret);
		fflush(stderr);
		return ret;
	}
	kvm_venc.enc_video_init = 1;
	return 0;
}

// Classifies a pack by its first VCL NAL unit: 1 for an IDR/IRAP picture, 0
// for any other picture, -1 when the pack holds only parameter sets or SEI.
// All slices of a picture share the IDR/IRAP type, so the scan stops at the
// first slice header instead of walking the whole slice payload for the
// start codes that emulation prevention guarantees are absent.
static int annexb_picture_type(const uint8_t *data, int size, uint8_t codec)
{
	for (int i = 0; i + 3 < size;) {
		int prefix_size = 0;
		if (i + 4 < size && data[i] == 0 && data[i + 1] == 0 && data[i + 2] == 0 && data[i + 3] == 1) {
			prefix_size = 4;
		} else if (data[i] == 0 && data[i + 1] == 0 && data[i + 2] == 1) {
			prefix_size = 3;
		}
		if (prefix_size == 0) {
			i++;
			continue;
		}

		int nal = i + prefix_size;
		if (nal >= size) {
			break;
		}
		if (codec == VENC_H264) {
			uint8_t nal_type = data[nal] & 0x1f;
			if (nal_type >= 1 && nal_type <= 5) {
				return nal_type == 5;
			}
		}
		if (codec == VENC_H265) {
			uint8_t nal_type = (data[nal] >> 1) & 0x3f;
			if (nal_type < 32) {
				return nal_type >= 16 && nal_type <= 21;
			}
		}
		i = nal + 1;
	}
	return -1;
}

int video_stream_dump(kvmv_data_t *dump_to, mmf_stream_t *dump_from, uint8_t codec)
{
	if (dump_from->count < 1 || dump_from->count > 8) {
		debug("[kvmv]invalid venc pack count: %d\n", dump_from->count);
		return IMG_VENC_ERROR;
	}

	uint32_t total_size = 0;
	int picture = -1;
	for (int i = 0; i < dump_from->count; i++) {
		if (dump_from->data[i] == NULL || dump_from->data_size[i] <= 0) {
			debug("[kvmv]invalid venc pack %d\n", i);
			return IMG_VENC_ERROR;
		}
		if ((uint32_t)dump_from->data_size[i] > UINT32_MAX - total_size) {
			debug("[kvmv]venc frame is too large\n");
			return IMG_VENC_ERROR;
		}
		total_size += dump_from->data_size[i];
		if (picture < 0) {
			picture = annexb_picture_type(dump_from->data[i], dump_from->data_size[i], codec);
		}
	}
	bool keyframe = picture == 1;

	// CVITEK normally emits SPS/PPS/IDR as three H.264 packs and
	// VPS/SPS/PPS/IDR as four H.265 packs. Keep that as a fallback for SDK
	// builds that do not expose Annex-B prefixes in every pack.
	keyframe = keyframe || (codec == VENC_H264 && dump_from->count >= 3) ||
		(codec == VENC_H265 && dump_from->count >= 4);

	uint32_t offset = 0;
	for (int i = 0; i < dump_from->count; i++) {
		if (video_sink(video_sink_context, dump_from->data[i],
		               dump_from->data_size[i], offset, total_size) != 0) {
			dump_to->img_data_size = 0;
			return IMG_BUFFER_FULL;
		}
		offset += dump_from->data_size[i];
	}
	dump_to->img_data_size = total_size;
	dump_to->img_data_type = keyframe ? IMG_H264_TYPE_IF : IMG_H264_TYPE_PF;
	return dump_to->img_data_type;
}

void set_frame_detact(uint8_t _frame_detact)
{
    uint8_t frame_detact = maxmin_data(100, 0, (int)_frame_detact);
    kvmv_cfg.frame_detact = frame_detact;
    debug("[kvmv] set_frame_detact = %d\n", kvmv_cfg.frame_detact);
    kvmv_cfg.stream_stop = 0;
}

// Cross-thread callers only enqueue intent; VENC is touched under vi_mutex.
static std::atomic<bool> requested_keyframe{false};

/*
 * The stream rate the server will request (0: unknown). When the HDMI input
 * is faster, VPSS drops the surplus input frames before converting them, so a
 * 30 fps stream of a 60 Hz source does half the VPSS work and DDR traffic.
 * Applied under vi_mutex by kvmv_read_img, at most once per second.
 */
static std::atomic<int> capture_target_fps{0};
// The nominal HDMI input rate (0: unknown), updated with the capture rate.
static std::atomic<int> capture_input_fps{0};
int kvmv_set_capture_fps(uint8_t fps)
{
    capture_target_fps.store(fps, std::memory_order_relaxed);
    return 0;
}

static void apply_capture_rate(void)
{
    static uint64_t last_ms = 0;
    const uint64_t now = vi_state_shared::monotonic_ms();
    if (now - last_ms < 1000) return;
    last_ms = now;
    const int input_fps = mmf_vi_input_fps();
    static nanokvm::InputRateFilter input_rate;
    capture_input_fps.store(input_rate.update(input_fps), std::memory_order_relaxed);
    mmf_vpss_set_rate(input_fps, capture_target_fps.load(std::memory_order_relaxed));
    static int published_fps = -1;
    if (input_fps != published_fps) {
        nanokvm::write_small_uint("/run/nanokvm/input_fps", input_fps);
        published_fps = input_fps;
    }
}
void kvmv_request_keyframe(void) {
 requested_keyframe.store(true, std::memory_order_relaxed);
}

static uint8_t video_output_fps(const nanokvm::GeometrySnapshot &src, int width, int height, uint8_t fps)
{
    const int limit = nanokvm::capture_stream_rate_limit(src.width, src.height, width, height);
    if (fps > limit) fps = limit;
    // Never configure the encoder above the rate that arrives: a 60 Hz input
    // with a 100 fps setting (a Cube keeps 60 Hz) would get the rate control
    // and the pre-submit pacing of 100 fps.
    const int input = capture_input_fps.load(std::memory_order_relaxed);
    if (input > 0 && fps > input) fps = input;
    return fps;
}

static bool video_encoder_matches(const nanokvm::GeometrySnapshot &src, int width, int height,
	uint16_t bitrate, uint8_t codec, uint8_t fps)
{
	return kvm_venc.enc_video_init == 1 && width == kvm_venc.kvm_venc_cfg.w &&
		height == kvm_venc.kvm_venc_cfg.h && bitrate == kvm_venc.kvm_venc_cfg.bitrate &&
		(codec == VENC_H265 ? 1 : 2) == kvm_venc.kvm_venc_cfg.type &&
		video_output_fps(src, width, height, fps) == kvm_venc.kvm_venc_cfg.output_fps;
}

/*
 * At a high pixel rate one frame takes the encoder about a whole frame
 * period (3840x2160@30, 2560x1440@60, 1920x1080@120). After the
 * stream of frame N is out, submit the newest queued VPSS frame right away so
 * the hardware encodes it while frame N is copied and delivered; the next read
 * collects it (see kvmv_read_img). Wait briefly for a frame that VPSS is about
 * to finish, so it does not wait for the caller instead; otherwise the next
 * read acquires one as usual. Not while a JPEG reader is active: the lease
 * would block its reads (see nanokvm::presubmit_next_video_frame).
 */
static void submit_next_video_frame(int vi_ch, int width, int height)
{
	if (!nanokvm::presubmit_next_video_frame((long)width * height * kvm_venc.kvm_venc_cfg.output_fps,
			nanokvm::fast_pixel_rate, mjpeg_jpeg_reader_active(mjpeg_monotonic_ms()))) return;
	int len = 0, w = 0, h = 0, format = 0;
	if (mmf_vi_frame_try_pop_native(vi_ch, 5, &len, &w, &h, &format) != 0) return;
	if (w != width || h != height || format != nanokvm::nv21_format()
		|| mmf_venc_push_vi(kvm_venc.mmf_venc_chn, vi_ch) != 0)
		mmf_vi_frame_release(vi_ch);
}

// frame_to_video restarted the capture for another capture mode; no frame.
static constexpr int8_t IMG_CAPTURE_RESTARTED = -100;

int8_t frame_to_video(const nanokvm::GeometrySnapshot &src, uint8_t *data, int width, int height,
	int format, int vi_ch, kvmv_data_t *ret_stream, uint16_t bitrate, uint8_t codec,
	uint8_t fps, bool submitted = false)
{
	fps = video_output_fps(src, width, height, fps);
	int8_t ret = 0;
	mmf_stream_t stream = {};
	if (!video_encoder_matches(src, width, height, bitrate, codec, fps)) {
		debug("[kvmv]init video codec=%d %dx%d bitrate=%d fps=%d\n",
			codec, width, height, bitrate, fps);
		if (init_venc_video(width, height, bitrate, codec, fps) != 0) {
			return -1;
		}
	}

	// The capture mode follows the video encoder (4:2:0 for H.264 straight
	// from capture, see kvm_mmf). The VI takes it only when it starts, so a
	// change restarts the capture; the caller then reads again.
	if (mmf_vi_yuv420_pending()) {
		if (vi_ch >= 0 && !submitted) mmf_vi_frame_release(vi_ch);
		debug("[kvmv]capture restart for the %s capture mode\n",
			mmf_vi_yuv420_active() ? "4:2:2" : "4:2:0");
		restart_capture_locked();
		return IMG_CAPTURE_RESTARTED;
	}

 if (requested_keyframe.exchange(false, std::memory_order_relaxed)) {
  // A failed request leaves the natural GOP cadence intact.
  mmf_venc_request_idr(kvm_venc.mmf_venc_chn);
 }

	int push_ret = submitted ? 0 : vi_ch >= 0
		? mmf_venc_push_vi(kvm_venc.mmf_venc_chn, vi_ch)
		: mmf_venc_push(kvm_venc.mmf_venc_chn, data, width, height, format);
	if (push_ret) {
		mmf_del_venc_channel(kvm_venc.mmf_venc_chn);
		if (vi_ch >= 0) {
			mmf_vi_frame_release(vi_ch);
		}
		kvm_venc.enc_video_init = 0;
		debug("[kvmv]mmf venc push failed!\n");
		return -1;
	}
	if (mmf_venc_pop(kvm_venc.mmf_venc_chn, &stream)) {
		mmf_venc_free(kvm_venc.mmf_venc_chn);
		mmf_del_venc_channel(kvm_venc.mmf_venc_chn);
		kvm_venc.enc_video_init = 0;
		debug("[kvmv]mmf venc pop failed!\n");
		return -1;
	}

	ret = video_stream_dump(ret_stream, &stream, codec);
	mmf_venc_free(kvm_venc.mmf_venc_chn);
	if (ret >= 0 && vi_ch >= 0) submit_next_video_frame(vi_ch, width, height);
	debug("[kvmv]Frame size = %d; dump ret = %d\n", ret_stream->img_data_size, ret);
	return ret;
}

void kvmv_init(uint8_t _debug_info_en)
{
    pthread_mutex_init(&vi_mutex, NULL);
    if(_debug_info_en == 0) debug_en = 0;
    else                    debug_en = 1;

    // The HDMI source may already be stable before the detection thread starts.
    // Read UXC dimensions before MMF allocates VI pools; waiting for a new edge
    // can leave the provisional FHD sensor geometry active on a QHD input.
    // The later detection thread still handles hot-plug/resolution changes.
    get_hdmi_version();
    if (kvmv_cfg.hdmi_version == 1) {
        lt6911_enable();
        if (lt6911_get_hdmi_res()) {
            detect_csi_res();
            set_hdmi_detection_state(1);
        }
        lt6911_disable();
    }

    cam->hmirror(1);
    cam->vflip(1);
    restart_capture();
    __atomic_store_n(&kvmv_cfg.try_exit_thread, 0, __ATOMIC_RELEASE);

    if(kvmv_cfg.thread_is_running == 1 || vi_detection_thread_started == 1){
        debug("[kvmv]thread is running!\r\n");
    } else {
        if (0 != pthread_create(&vi_detection_thread, NULL, vi_subsystem_detection, NULL)) {
            debug("[kvmv]create vi_subsystem_detection thread failed!\r\n");
        } else {
            vi_detection_thread_started = 1;
        }
    }
}

uint8_t check_kvmv(uint8_t _try_num)
{
    if(__atomic_load_n(&kvmv_cfg.hdmi_cable_state, __ATOMIC_ACQUIRE) == 0){
        debug("[kvmv]HDMI Cable not exist!\n");
        return 0;
    }
    if(_try_num >= KVMV_MAX_TRY_NUM){
        debug("[kvmv]try_num >= KVMV_MAX_TRY_NUM!\n");
        return 0;
    }
    if(kvmv_cfg.reopen_cam_flag.exchange(0) == 1){
        // Called by kvmv_read_img with vi_mutex already held.
        restart_capture_locked();
        debug("[kvmv]vi size changed, try again\n");
        return 1;
    }
    debug("[kvmv]just try again\n");
    return 1;
}

/* A step that creates, reconfigures or submits to the JPEG channel is admitted
   against the pass snapshot and runs under a lease on it (geometry_gate.hpp):
   the checked size stays valid until the lease is released, and a size that was
   published after the snapshot makes the step stale. Other codecs do not reach
   the JPEG encoder, whose wedge at a 4K input is what the gate prevents; they
   use the snapshot as it is. */
static nanokvm::JpegAdmission admit_capture_step(uint8_t type, const nanokvm::GeometrySnapshot &geometry)
{
    if (type != VENC_MJPEG)
        return {nanokvm::JpegVerdict::Allowed, nanokvm::GeometryGate::Lease()};
    return nanokvm::admit_jpeg_step(vi_geometry, geometry);
}

/*
 * One capture pass: encode the frame for the requested size and codec and hand
 * the bytes to the calling thread's sink (kvmv_read_video_sink_v2 or
 * kvmv_read_mjpeg_sink). _qlty is the MJPEG quality (20-99) or the H.264/H.265
 * bitrate (500-20000 Kbit/s). Return codes are listed in kvm_vision.h.
 */
static int kvmv_read_img_locked(uint16_t _width, uint16_t _height, uint8_t _type, uint16_t _qlty);

// JPEG readers (the MJPEG stream, screenshots, MCP) take turns: one encodes
// outside vi_mutex (frame_to_jpeg), and another must not take its output's
// frame slot meanwhile. Lock order: jpeg_reader_mutex -> vi_mutex -> the JPEG
// hold (kvm_mmf). Video reads take vi_mutex only.
static pthread_mutex_t jpeg_reader_mutex = PTHREAD_MUTEX_INITIALIZER;

static int kvmv_read_img(uint16_t _width, uint16_t _height, uint8_t _type, uint16_t _qlty)
{
    if (_type != VENC_MJPEG) return kvmv_read_img_locked(_width, _height, _type, _qlty);
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    ts.tv_sec += 1;
    if (pthread_mutex_timedlock(&jpeg_reader_mutex, &ts) != 0) return -5;
    const int result = kvmv_read_img_locked(_width, _height, _type, _qlty);
    pthread_mutex_unlock(&jpeg_reader_mutex);
    return result;
}

static int kvmv_read_img_locked(uint16_t _width, uint16_t _height, uint8_t _type, uint16_t _qlty)
{
    if (video_sink == nullptr) return IMG_VENC_ERROR;
    static uint8_t frame_undetact_count = 0;
    debug("[kvmv]kvmv_read_img type = %d...\n", _type);
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    ts.tv_sec += 1;
    int mutex_res = pthread_mutex_timedlock(&vi_mutex, &ts);
    if(mutex_res != 0){
        return -5;
    }
    if (set_capture_parked_locked(false) != 0) {
        pthread_mutex_unlock(&vi_mutex);
        return IMG_VENC_ERROR;
    }
    /* Auto-detection remains in vi_detect_state == 1 while HDMI is absent so
       that a newly connected source is discovered.  Report the cable state
       before that transient state; otherwise callers see an endless -4
       ("Modifying image resolution") even though no signal is present. */
    if (__atomic_load_n(&kvmv_cfg.hdmi_cable_state, __ATOMIC_ACQUIRE) == 0){
        pthread_mutex_unlock(&vi_mutex);
        return -1;
    }
    if (kvmv_cfg.hdmi_res_err == ERROR_RES){
        pthread_mutex_unlock(&vi_mutex);
        return -7;
    }
    if (kvmv_cfg.hdmi_res_type == UNSUPPORT_RES){
        pthread_mutex_unlock(&vi_mutex);
        return -6;
    }
    if (kvmv_cfg.vi_detect_state == 1){
        pthread_mutex_unlock(&vi_mutex);
        return -4;
    }
    uint8_t try_num = 0;
    // A pass whose geometry went stale starts again from a new snapshot; it does
    // not count as a failed try (check_kvmv would restart the capture).
    constexpr unsigned max_stale_passes = 4;
    unsigned stale_passes = 0;
    bool geometry_stale = false;
    do {
        geometry_stale = false;
        /* One snapshot of the detected input size per pass: everything below
           (stream size, MJPEG allowed or not, the frame rate limit) derives from
           it, nothing reads the live size again. */
        const nanokvm::GeometrySnapshot geometry = vi_geometry.snapshot();
        /* Authoritative MJPEG gate (the server file check is only a fast path).
           It runs on every pass, before any VPSS/JPEG channel is configured:
           a 4K input wedges the JPEG encoder. The steps that create or submit to
           the channel below are admitted again, under a lease on this snapshot. */
        if (_type == VENC_MJPEG && !nanokvm::mjpeg_input_allowed(geometry.width, geometry.height)) {
            pthread_mutex_unlock(&vi_mutex);
            return IMG_MJPEG_INPUT_BLOCKED;
        }
        const uint64_t capture_now = mjpeg_monotonic_ms();
        const auto requested_output = nanokvm::stream_size(geometry.width, geometry.height, _width, _height);
        const auto output = mjpeg_parallel_size(requested_output, _type, kvmv_cfg.frame_detact != 0, capture_now);
        // H.264 encodes up to 2560x1440 (1080x1920 in portrait): above that
        // the encoder corrupts the picture or rejects it. Refuse before any
        // channel is set up; larger pictures need H.265.
        if (_type == VENC_H264 && !nanokvm::h264_output_allowed(output)) {
            pthread_mutex_unlock(&vi_mutex);
            return IMG_VENC_ERROR;
        }
        if (!output.width || !output.height) {
            pthread_mutex_unlock(&vi_mutex);
            return IMG_VENC_ERROR;
        }
        const int capture_format = mjpeg_capture_format(_type, kvmv_cfg.frame_detact != 0,
                                                         capture_now, output.width);
        int retired = 0;
        if (capture_format == nanokvm::nv16_format() && !mjpeg_video_reader_active(capture_now)) {
            // Release the provisional or expired NV21 output before a wide JPEG.
            retired = kvm_venc.enc_video_init ? mmf_del_venc_channel(kvm_venc.mmf_venc_chn) : 0;
            if (!retired) { kvm_venc.enc_video_init = 0; retired = cam->close_format(nanokvm::nv21_format()); }
        }
        const bool jpeg_active = mjpeg_jpeg_reader_active(capture_now);
        const bool remove_nv16 = !mjpeg_422_requested() || mjpeg_422_disabled
            || kvmv_cfg.frame_detact != 0 || mjpeg_force_copy()
            || (_type == VENC_MJPEG && capture_format == nanokvm::nv21_format())
            || ((_type == VENC_H264 || _type == VENC_H265)
                && (!jpeg_active || (output.width > 1920 && cam->format_width(nanokvm::nv16_format()) > 1920)));
        if (!retired && remove_nv16 && cam->has_format(nanokvm::nv16_format())) {
            retired = mmf_enc_jpg_deinit(0);
            if (!retired) retired = cam->close_format(nanokvm::nv16_format());
        }
        if (retired) {
            pthread_mutex_unlock(&vi_mutex);
            return IMG_VENC_ERROR;
        }
        if (!cam->matches_output(capture_format, output.width, output.height)) {
            // Allocate producer surfaces before encoder reference pools. This
            // avoids fragmentation on a live resolution/format change.
            retired = mmf_enc_jpg_deinit(0);
            if (!retired && kvm_venc.enc_video_init) retired = mmf_del_venc_channel(kvm_venc.mmf_venc_chn);
            if (!retired) { kvm_venc.enc_video_init = 0; retired = mmf_trim_idle_copy_buffers(); }
            if (retired) {
                pthread_mutex_unlock(&vi_mutex);
                return IMG_VENC_ERROR;
            }
        }
        if (cam->get_channel() < 0 || kvmv_cfg.vpss_width != output.width ||
                kvmv_cfg.vpss_height != output.height || cam->get_format() != capture_format) {
            // The VPSS output the JPEG channel will use is configured under the lease.
            nanokvm::JpegAdmission resize_step = admit_capture_step(_type, geometry);
            if (resize_step.verdict == nanokvm::JpegVerdict::Blocked) {
                pthread_mutex_unlock(&vi_mutex);
                return IMG_MJPEG_INPUT_BLOCKED;
            }
            if (resize_step.verdict == nanokvm::JpegVerdict::Stale) {
                geometry_stale = true;
                continue;
            }
            if (cam->set_resolution(output.width, output.height, capture_format) != 0) {
                if (capture_format == nanokvm::nv16_format()) {
                    disable_mjpeg_422("VPSS configuration failed");
                    continue; // Preserve a working video output and retry JPEG as NV21.
                }
                kvmv_cfg.reopen_cam_flag = 1;
                pthread_mutex_unlock(&vi_mutex);
                return IMG_VENC_ERROR;
            }
            kvmv_cfg.vpss_width = output.width;
            kvmv_cfg.vpss_height = output.height;
            kvmv_cfg.reinit_flag = 1;
        }

        static uint16_t reported_width = 0, reported_height = 0;
        if (reported_width != output.width || reported_height != output.height) {
            for (const auto &value : {std::pair<const char *, unsigned>("/run/nanokvm/stream_width", output.width),
                    std::pair<const char *, unsigned>("/run/nanokvm/stream_height", output.height)}) {
                FILE *status = fopen(value.first, "w");
                if (status) { fprintf(status, "%u", value.second); fclose(status); }
            }
            reported_width = output.width;
            reported_height = output.height;
        }

        static nanokvm::StreamSize reported_jpeg{}, reported_video{};
        auto &reported_codec = _type == VENC_MJPEG ? reported_jpeg : reported_video;
        if (reported_codec.width != output.width || reported_codec.height != output.height) {
            const char *paths[] = {_type == VENC_MJPEG ? "/run/nanokvm/mjpeg_width" : "/run/nanokvm/video_width",
                                  _type == VENC_MJPEG ? "/run/nanokvm/mjpeg_height" : "/run/nanokvm/video_height"};
            const unsigned values[] = {output.width, output.height};
            for (int index=0; index<2; ++index) {
                FILE *status = fopen(paths[index], "w");
                if (status) { fprintf(status, "%u", values[index]); fclose(status); }
            }
            reported_codec = output;
        }
        if (kvmv_cfg.reinit_flag == 1) {
            if (cam->hmirror(1) != 0 || cam->vflip(1) != 0) {
                kvmv_cfg.reopen_cam_flag = 1;
                pthread_mutex_unlock(&vi_mutex);
                return IMG_VENC_ERROR;
            }
            kvmv_cfg.reinit_flag = 0;
        }
        // A paused consumer must not fill its queue and stall the shared input.
        if (cam->discard_other_pending(_type != VENC_MJPEG && mjpeg_jpeg_reader_active(capture_now)) != 0) {
            pthread_mutex_unlock(&vi_mutex);
            return IMG_VENC_ERROR;
        }
        // The video encoders read limited-range BT.709, JPEG decoders full-range
        // BT.601: NV21 is JFIF while only MJPEG reads it, BT.709 as soon as a
        // video read arrives and until video has been quiet for the reader window.
        // The driver drops the queued frames; skip the one in flight.
        if (capture_format == nanokvm::nv21_format()) {
            const bool jfif = _type == VENC_MJPEG && !mjpeg_video_reader_active(capture_now);
            const int colour = mmf_vi_chn_set_jfif(cam->get_channel(), jfif);
            if (colour < 0) {
                pthread_mutex_unlock(&vi_mutex);
                return IMG_VENC_ERROR;
            }
            if (colour > 0 && kvmv_cfg.fresh_frame_count == 0)
                kvmv_cfg.fresh_frame_count = 1;
        }
        // MJPEG's own output next to a video stream: a little above the rate
        // the server reads it at, so the VPSS converts few frames that are
        // dropped. The read waits for its frame outside vi_mutex. Any other
        // output runs at the capture rate.
        const bool own_jpeg = _type == VENC_MJPEG && capture_format == nanokvm::nv16_format();
        if (cam->get_channel() >= 0 && mmf_vpss_set_chn_max_rate(cam->get_channel(),
                !own_jpeg || !mjpeg_video_reader_active(capture_now) ? 0
                : mjpeg_last_video_width > 1920 ? mjpeg_next_to_wide_video_fps : mjpeg_next_to_video_fps) != 0) {
            pthread_mutex_unlock(&vi_mutex);
            return IMG_VENC_ERROR;
        }
        apply_capture_rate();
        nanokvm::Nv21Frame *img = NULL;
        int native_vi_ch = -1;
        int native_len = 0;
        int native_width = 0;
        int native_height = 0;
        int native_format = 0;
        // Collect a frame that the previous read already submitted when this
        // read asks for the same stream; otherwise retire it first.
        bool submitted = false;
        const int pending_vi = kvm_venc.enc_video_init ? mmf_venc_pending_vi(kvm_venc.mmf_venc_chn) : -1;
        if (pending_vi >= 0) {
            if ((_type == VENC_H264 || _type == VENC_H265) && _type == kvmv_cfg.venc_type
                    && pending_vi == cam->get_channel() && capture_format == nanokvm::nv21_format()
                    && kvmv_cfg.fresh_frame_count == 0
                    && video_encoder_matches(geometry, output.width, output.height,
                        maxmin_data(20000, 500, (int)_qlty), _type, kvmvenc_fps)) {
                submitted = true;
                native_vi_ch = pending_vi;
                native_width = output.width;
                native_height = output.height;
                native_format = capture_format;
            } else if (mmf_del_venc_channel(kvm_venc.mmf_venc_chn) == 0) {
                kvm_venc.enc_video_init = 0;
            } else {
                pthread_mutex_unlock(&vi_mutex);
                return IMG_VENC_ERROR;
            }
        }
        if (submitted) {
        } else if (_type == VENC_H264 || _type == VENC_H265 ||
                (_type == VENC_MJPEG && kvmv_cfg.frame_detact == 0)) {
            native_vi_ch = cam->get_channel();
            int native_result;
            if (_type == VENC_MJPEG && capture_format == nanokvm::nv16_format()) {
                // MJPEG's own output: wait for its frame outside vi_mutex, so
                // the video stream reads on meanwhile. A teardown waits for
                // the JPEG hold; one between the hold and vi_mutex again
                // releases the frame or deletes the output: start again
                // rather than count it as a failed 4:2:2 read. (A video read
                // meanwhile switches cam's current format, not this output.)
                mmf_jpg_hold();
                pthread_mutex_unlock(&vi_mutex);
                native_result = mmf_vi_frame_pop_own(native_vi_ch, &native_len, &native_width,
                        &native_height, &native_format);
                mmf_jpg_unhold();
                pthread_mutex_lock(&vi_mutex);
                if (native_result == 0 ? !mmf_vi_frame_leased(native_vi_ch)
                        : !cam->has_format(nanokvm::nv16_format())) {
                    native_vi_ch = -1;
                    geometry_stale = true;
                    continue;
                }
            } else if (_type == VENC_MJPEG) {
                // MJPEG leases its own frame where H.264 reads 4:2:0 capture directly.
                native_result = mmf_vi_frame_pop_jpeg(native_vi_ch, &native_len, &native_width,
                        &native_height, &native_format);
            } else {
                native_result = mmf_vi_frame_pop_native(native_vi_ch, &native_len, &native_width,
                        &native_height, &native_format);
            }
            // Another reader can select a different output while MJPEG waits
            // outside vi_mutex. Validate against this pass, not that selection.
            if (native_result != 0 || native_width != output.width
                    || native_height != output.height || native_format != capture_format) {
                if (native_result == 0) mmf_vi_frame_release(native_vi_ch);
                native_vi_ch = -1;
                if (capture_format == nanokvm::nv16_format()) {
                    // First frames may time out while the HDMI receiver warms.
                    // Retry before treating missing frames as a format failure.
                    if (native_result != 0 && ++mjpeg_422_frame_failures < 3) continue;
                    fprintf(stderr,"[kvmv] NV16 frame rejected: ch=%d result=%#x got=%dx%d fmt=%d expected=%ux%u fmt=%d\n", cam->get_channel(), native_result,native_width,native_height,native_format,output.width,output.height,capture_format);
                    disable_mjpeg_422("native VPSS frame unavailable or invalid");
                    continue; // Reconfigure to NV21 before attempting its copied path.
                }
                img = cam->read();
            }
        } else {
            img = cam->read();
        }
        if (native_vi_ch >= 0 && native_format == nanokvm::nv16_format()) mjpeg_422_frame_failures = 0;

        if(img != NULL || native_vi_ch >= 0){
            if(kvmv_cfg.fresh_frame_count != 0){
                // Do not restart VI after HDMI idle. Reopening the MMF
                // channel can exhaust the carveout heap when the detector
                // thread is also transitioning. Consume queued frames
                // instead; the camera buffer contains at most three frames.
                kvmv_cfg.fresh_frame_count--;
                if (native_vi_ch >= 0) {
                    mmf_vi_frame_release(native_vi_ch);
                } else {
                    delete img;
                }
                continue;
            }

            // frame detect
            if(_type == VENC_MJPEG && kvmv_cfg.frame_detact != 0){
                if(kvmv_cfg.stream_stop == 0){
                    frame_undetact_count++;
                } else {
                    frame_undetact_count = kvmv_cfg.frame_detact;
                }

                if(frame_undetact_count == kvmv_cfg.frame_detact){
                    frame_undetact_count = 0;

                    if(frame_changed(img) == 0){
                        debug("[kvmv]frame not changed...\n");
                        kvmv_cfg.stream_stop = 1;
                        delete img;
                        pthread_mutex_unlock(&vi_mutex);
                        return 5;
                    } else {
                        kvmv_cfg.stream_stop = 0;
                    }
                }
            }
        } else {
            delete img;
            debug("[kvmv]can`t get img...\n");
            continue;
        }

        // img exist
        // The input may have changed to 4K while the frame was prepared; the
        // JPEG channel must not be created or submitted to at that size. The
        // lease stays held to the end of the pass, through frame_to_jpeg and
        // to_jpeg, so the size checked here is the size they run at.
        nanokvm::JpegAdmission encode_step = admit_capture_step(_type, geometry);
        if (encode_step.verdict != nanokvm::JpegVerdict::Allowed) {
            if (native_vi_ch >= 0) mmf_vi_frame_release(native_vi_ch);
            else delete img;
            if (encode_step.verdict == nanokvm::JpegVerdict::Blocked) {
                pthread_mutex_unlock(&vi_mutex);
                return IMG_MJPEG_INPUT_BLOCKED;
            }
            geometry_stale = true;
            continue;
        }
        // Encode
        if (kvmv_cfg.venc_type != _type && mmf_trim_idle_copy_buffers() != 0) {
            if (native_vi_ch >= 0) mmf_vi_frame_release(native_vi_ch);
            else delete img;
            pthread_mutex_unlock(&vi_mutex);
            return IMG_VENC_ERROR;
        }

        kvmv_cfg.venc_type = _type;

        if(kvmv_cfg.venc_type == VENC_MJPEG){
            kvmv_data_t frame;
            if (native_vi_ch >= 0) {
                const bool own_output = native_format == nanokvm::nv16_format();
                int ret = frame_to_jpeg(native_vi_ch, &frame,
                    maxmin_data(99, 20, (int)_qlty), own_output);
                if (own_output) pthread_mutex_lock(&vi_mutex);
                if (ret < 0 && own_output && ret == IMG_VENC_ERROR)
                    disable_mjpeg_422("native JPEG encoding failed");
                pthread_mutex_unlock(&vi_mutex);
                return ret;
            }
            nanokvm::JpegBuffer *jpg = img->to_jpeg(maxmin_data(99, 20, (int)_qlty));
            if(jpg == NULL || !jpg_dump(&frame, jpg)){
                delete jpg;
			    delete img;
                debug("[kvmv]JPEG encoding or the sink failed\n");
                pthread_mutex_unlock(&vi_mutex);
                return IMG_BUFFER_FULL;
            }
            delete jpg;
			delete img;
            pthread_mutex_unlock(&vi_mutex);
            return IMG_MJPEG_TYPE;
		} else if (kvmv_cfg.venc_type == VENC_H264 || kvmv_cfg.venc_type == VENC_H265){
            int ret;
            kvmv_data_t frame;
			uint8_t *frame_data = img == NULL ? NULL : (uint8_t *)img->data();
			int frame_width = img == NULL ? native_width : img->width();
			int frame_height = img == NULL ? native_height : img->height();
			int frame_format = img == NULL ? native_format : img->format();
			ret = frame_to_video(geometry, frame_data, frame_width, frame_height, frame_format,
				native_vi_ch, &frame, maxmin_data(20000, 500, (int)_qlty),
				kvmv_cfg.venc_type, kvmvenc_fps, submitted);
			delete img;
            // A new capture in the other mode: read from it.
            if (ret == IMG_CAPTURE_RESTARTED) continue;
            if(ret < 0){
                if (native_vi_ch >= 0) mmf_vi_frame_release(native_vi_ch);
                if (cam->has_format(nanokvm::nv16_format()) && !mjpeg_422_disabled) {
                    disable_mjpeg_422("video encoder failed with separate outputs");
                    // Retry through the existing NV21 output on the next read.
                    // Never close a producer until its JPEG lease is retired.
                    if (mmf_enc_jpg_deinit(0) == 0) cam->close_format(nanokvm::nv16_format());
                }
                pthread_mutex_unlock(&vi_mutex);
                return ret;
            }
            pthread_mutex_unlock(&vi_mutex);
            return ret;
        }
    } while (geometry_stale ? ++stale_passes <= max_stale_passes : check_kvmv(try_num++));
    pthread_mutex_unlock(&vi_mutex);
    // The input kept changing: report the resolution change, not a missing image.
    return geometry_stale ? -4 : IMG_NOT_EXIST;
}

static int kvmv_read_video(uint16_t _width, uint16_t _height, uint8_t _codec,
	uint16_t _bitrate, uint8_t _fps)
{
	if (_codec != VENC_H264 && _codec != VENC_H265) {
		return IMG_VENC_ERROR;
	}

    // Source geometry comes from the pass snapshot in kvmv_read_img.
    const int fps_limit = nanokvm::capture_rate_limit(_width, _height);
	kvmvenc_fps = maxmin_data(fps_limit, 10, (int)_fps);
	if (_width != 0 && _height != 0) {
		high_rate_log_request(_width, _height, _fps, kvmvenc_fps);
	}
	return kvmv_read_img(_width, _height, _codec,
		maxmin_data(20000, 500, (int)_bitrate));
}

int kvmv_read_video_sink_v2(uint16_t width, uint16_t height, uint8_t codec,
    uint16_t bitrate, uint8_t fps, kvmv_video_sink sink, uintptr_t context)
{
    if (sink == nullptr || video_sink != nullptr) return IMG_VENC_ERROR;
    video_sink = sink;
    video_sink_context = context;
    int result = kvmv_read_video(width, height, codec, bitrate, fps);
    video_sink = nullptr;
    video_sink_context = 0;
    return result;
}

int kvmv_read_mjpeg_sink(uint16_t width, uint16_t height, uint16_t quality,
    kvmv_video_sink sink, uintptr_t context)
{
    if (sink == nullptr || video_sink != nullptr) return IMG_VENC_ERROR;
    video_sink = sink;
    video_sink_context = context;
    int result = kvmv_read_img(width, height, VENC_MJPEG, quality);
    video_sink = nullptr;
    video_sink_context = 0;
    return result;
}

void kvmv_deinit()
{
    __atomic_store_n(&kvmv_cfg.try_exit_thread, 1, __ATOMIC_RELEASE);
    if (vi_detection_thread_started == 1) {
        pthread_join(vi_detection_thread, NULL);
        vi_detection_thread_started = 0;
    }

    set_hdmi_capture_enabled(0);
    /*
     * kvmv owns the whole MMF process lifetime. JPEG takes an additional MMF
     * reference while active, so a balanced single decrement can leave all
     * global JPEG/VENC/VB pools alive when Direct was selected last. There are
     * no workers left at this point; force the process-wide teardown. MMF
     * closes encoders before the camera channel so an encoder cannot retain a
     * frame whose VI producer has already been destroyed.
     */
    cam->shutdown();
    pthread_mutex_destroy(&vi_mutex);
}

static int set_owned_hdmi_reset(bool asserted)
{
    const char *path = "/sys/bus/platform/devices/hdmi-reset/asserted";
    FILE *file = fopen(path, "w");
    if (!file) { perror("HDMI reset open"); return -1; }
    const char value = asserted ? '1' : '0';
    const bool written = fwrite(&value, 1, 1, file) == 1;
    const int closed = fclose(file);
    if (!written || closed != 0) { perror("HDMI reset write"); return -1; }
    return 0;
}

// Pause the HDMI detector and capture without touching board-specific GPIO.
// Called under the server maintenance/capture mutex for Cube EDID writes.
int kvmv_edid_maintenance(uint8_t pause)
{
    static uint8_t previous_capture = 0;
    if (pause) {
        previous_capture = __atomic_load_n(&hdmi_capture_enabled, __ATOMIC_ACQUIRE);
        set_hdmi_capture_enabled(0);
        kvmv_cfg.hdmi_stop_flag = 1;
        const uint64_t deadline = vi_state_shared::monotonic_ms() + 2000;
        while (kvmv_cfg.hdmi_reading_flag == 1) {
            if (vi_state_shared::monotonic_ms() >= deadline) {
                kvmv_cfg.hdmi_stop_flag = 0;
                set_hdmi_capture_enabled(previous_capture);
                return -1;
            }
            nanokvm::sleep_ms(10);
        }
    } else {
        kvmv_cfg.hdmi_stop_flag = 0;
        kvmv_cfg.fresh_frame_count = fresh_frame_discard_count;
        set_hdmi_capture_enabled(previous_capture);
    }
    return 0;
}

int kvmv_hdmi_control(uint8_t _en)
{
    if(_en == 0){
        set_hdmi_capture_enabled(0);
    }

    if(kvmv_cfg.hw_version == 0){

        FILE *fp;
	    uint8_t RW_Data[2];
        if(access("/etc/kvm/hw", F_OK) == 0){
            fp = fopen("/etc/kvm/hw", "r");
            fread(RW_Data, sizeof(char), 1, fp);
            fclose(fp);
            switch(RW_Data[0]){
                case 'a':
                case 'b':
                case 'p':
                    kvmv_cfg.hw_version = RW_Data[0];
                    break;
                default :
                    kvmv_cfg.hw_version = 'a';
            }
        }
    }
    if(kvmv_cfg.hw_version != 'p'){
        debug("[kvmv]Hardware not support!\n");
        if(_en != 0){
            set_hdmi_capture_enabled(1);
        }
        return -1;
    }
    if(_en == 0){
        kvmv_cfg.hdmi_stop_flag = 1;
        while(kvmv_cfg.hdmi_reading_flag == 1) nanokvm::sleep_ms(10);
        if (set_owned_hdmi_reset(true) != 0) return -1;
        return 0;
    } else {
        if (set_owned_hdmi_reset(false) != 0) return -1;
        kvmv_cfg.hdmi_stop_flag = 0;

        // Keep the existing VI channel and consume queued frames after idle.
        // Reopening it here or from kvmv_read_img can exhaust the carveout
        // heap while the HDMI detector is transitioning.
        kvmv_cfg.fresh_frame_count = fresh_frame_discard_count;
        set_hdmi_capture_enabled(1);
        return 0;
    }
    return -1;
}

/* Caller holds vi_mutex. Parking deletes the video and JPEG encoder
   channels: a frame pre-submitted to the video encoder leases a VPSS buffer
   until collected, the next viewer starts with a new channel and an IDR
   anyway, and the encoder memory is free while nobody watches. Disabling
   releases the queued frames, so the first frame after resuming is fresh. */
static int set_capture_parked_locked(bool parked)
{
    return capture_park.set(parked, [] {
        if (kvm_venc.enc_video_init) {
            const int retired = mmf_del_venc_channel(kvm_venc.mmf_venc_chn);
            if (retired != 0) return retired;
            kvm_venc.enc_video_init = 0;
        }
        return mmf_enc_jpg_deinit(0);
    }, [](bool enabled) { return cam->set_enabled(enabled); });
}

int kvmv_set_capture_parked(uint8_t parked)
{
    pthread_mutex_lock(&vi_mutex);
    const int result = set_capture_parked_locked(parked != 0);
    pthread_mutex_unlock(&vi_mutex);
    if (result != 0) debug("[kvmv]capture %s failed: %d\n", parked ? "park" : "resume", result);
    return result;
}

uint8_t kvmv_hdmi_signal_active()
{
    uint8_t enabled = __atomic_load_n(&hdmi_capture_enabled, __ATOMIC_ACQUIRE);
    uint8_t active = __atomic_load_n(&hdmi_signal_active, __ATOMIC_ACQUIRE);
    return enabled != 0 && active != 0 ? 1 : 0;
}
