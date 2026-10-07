//go:build teststub && cgo

#include "kvm_vision.h"

/* Scripted stand-in for the libkvm sink readers used by the worker tests.
   Each pack is lent from a scratch "vendor" buffer that is overwritten as
   soon as the sink returns, as ReleaseStream would recycle it. */
static pthread_mutex_t fake_lock = PTHREAD_MUTEX_INITIALIZER;
static uint8_t fake_data[4096];
static uint32_t fake_sizes[8];
static int fake_count, fake_result, fake_sink_result, fake_offset_skew;
static uint32_t fake_total_skew;
static uint16_t fake_args[6];

void nk_fake_capture_script(const uint8_t *data, const uint32_t *sizes, int count,
        int result, uint32_t total_skew, int offset_skew) {
    pthread_mutex_lock(&fake_lock);
    uint32_t used = 0;
    for (int i = 0; i < count && i < 8; i++) {
        memcpy(fake_data + used, data + used, sizes[i]);
        fake_sizes[i] = sizes[i];
        used += sizes[i];
    }
    fake_count = count;
    fake_result = result;
    fake_total_skew = total_skew;
    fake_offset_skew = offset_skew;
    pthread_mutex_unlock(&fake_lock);
}

/* width, height, codec, rate, gop, fps of the last read and the last
   nonzero sink result. */
int nk_fake_capture_last(uint16_t *args) {
    pthread_mutex_lock(&fake_lock);
    memcpy(args, fake_args, sizeof(fake_args));
    int result = fake_sink_result;
    pthread_mutex_unlock(&fake_lock);
    return result;
}

static int fake_read(uint16_t width, uint16_t height, uint8_t codec, uint16_t rate,
        uint8_t gop, uint8_t fps, kvmv_video_sink sink, uintptr_t context) {
    pthread_mutex_lock(&fake_lock);
    const uint16_t args[6] = {width, height, codec, rate, gop, fps};
    memcpy(fake_args, args, sizeof(args));
    fake_sink_result = 0;
    uint32_t total = fake_total_skew, used = 0;
    for (int i = 0; i < fake_count; i++) total += fake_sizes[i];
    int result = fake_result;
    for (int i = 0; i < fake_count; i++) {
        uint8_t vendor[sizeof(fake_data)];
        memcpy(vendor, fake_data + used, fake_sizes[i]);
        uint32_t offset = used + (i > 0 ? fake_offset_skew : 0);
        /* The worker sink blocks until Go takes the pack; do not hold the
           script lock across it. */
        pthread_mutex_unlock(&fake_lock);
        int sunk = sink(context, vendor, fake_sizes[i], offset, total);
        memset(vendor, 0xee, fake_sizes[i]);
        pthread_mutex_lock(&fake_lock);
        used += fake_sizes[i];
        if (sunk != 0) {
            fake_sink_result = sunk;
            result = IMG_BUFFER_FULL;
            break;
        }
    }
    pthread_mutex_unlock(&fake_lock);
    return result;
}

int kvmv_read_video_sink(uint16_t width, uint16_t height, uint8_t codec,
        uint16_t bitrate, uint8_t gop, uint8_t fps, kvmv_video_sink sink, uintptr_t context) {
    return fake_read(width, height, codec, bitrate, gop, fps, sink, context);
}

int kvmv_read_mjpeg_sink(uint16_t width, uint16_t height, uint16_t quality,
        kvmv_video_sink sink, uintptr_t context) {
    return fake_read(width, height, 0, quality, 0, 0, sink, context);
}
