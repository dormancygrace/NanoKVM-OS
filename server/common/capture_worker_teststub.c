//go:build teststub && cgo

#include "kvm_vision.h"

/* Scripted stand-in for kvmv_read_video_sink used by the worker tests. Reads
   take queued frames in order; with blocking set, an empty queue waits like a
   VPSS frame wait, otherwise it reports IMG_NOT_EXIST. Each pack is lent from
   a scratch "vendor" buffer overwritten as soon as the sink returns, as
   ReleaseStream would recycle it. */
enum { FAKE_FRAMES = 32, FAKE_READS = 64, FAKE_BYTES = 4096 };
typedef struct {
    uint8_t data[FAKE_BYTES];
    uint32_t sizes[8];
    int count, result, offset_skew;
    uint32_t total_skew;
} fake_frame;
static pthread_mutex_t fake_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t fake_changed = PTHREAD_COND_INITIALIZER;
static fake_frame fake_queue[FAKE_FRAMES];
static int fake_head, fake_tail, fake_block, fake_waiting, fake_reads;
static uint16_t fake_args[FAKE_READS][6];
static int64_t fake_times[FAKE_READS];

void nk_fake_capture_reset(int block) {
    pthread_mutex_lock(&fake_lock);
    fake_head = fake_tail = fake_reads = 0;
    fake_block = block;
    pthread_mutex_unlock(&fake_lock);
}

int nk_fake_capture_push(const uint8_t *data, const uint32_t *sizes, int count,
        int result, uint32_t total_skew, int offset_skew) {
    pthread_mutex_lock(&fake_lock);
    if (fake_tail - fake_head >= FAKE_FRAMES || count > 8) {
        pthread_mutex_unlock(&fake_lock);
        return -1;
    }
    fake_frame *f = &fake_queue[fake_tail++ % FAKE_FRAMES];
    uint32_t used = 0;
    for (int i = 0; i < count; i++) {
        f->sizes[i] = sizes[i];
        used += sizes[i];
    }
    memcpy(f->data, data, used);
    f->count = count;
    f->result = result;
    f->total_skew = total_skew;
    f->offset_skew = offset_skew;
    pthread_cond_broadcast(&fake_changed);
    pthread_mutex_unlock(&fake_lock);
    return 0;
}

/* Ends blocking: waiting and later reads report IMG_NOT_EXIST. */
void nk_fake_capture_unblock(void) {
    pthread_mutex_lock(&fake_lock);
    fake_block = 0;
    pthread_cond_broadcast(&fake_changed);
    pthread_mutex_unlock(&fake_lock);
}

/* Number of reads started; copies the parameters (width, height, codec, rate,
   gop, fps) and CLOCK_MONOTONIC start of the first max of them. */
int nk_fake_capture_reads(uint16_t *args, int64_t *times, int max, int *waiting) {
    pthread_mutex_lock(&fake_lock);
    int reads = fake_reads;
    for (int i = 0; i < reads && i < max && i < FAKE_READS; i++) {
        memcpy(args + 6 * i, fake_args[i], sizeof(fake_args[i]));
        times[i] = fake_times[i];
    }
    *waiting = fake_waiting;
    pthread_mutex_unlock(&fake_lock);
    return reads;
}

int kvmv_read_video_sink(uint16_t width, uint16_t height, uint8_t codec,
        uint16_t bitrate, uint8_t gop, uint8_t fps, kvmv_video_sink sink, uintptr_t context) {
    struct timespec now;
    clock_gettime(CLOCK_MONOTONIC, &now);
    pthread_mutex_lock(&fake_lock);
    if (fake_reads < FAKE_READS) {
        const uint16_t args[6] = {width, height, codec, bitrate, gop, fps};
        memcpy(fake_args[fake_reads], args, sizeof(args));
        fake_times[fake_reads] = (int64_t)now.tv_sec * 1000000000 + now.tv_nsec;
    }
    fake_reads++;
    fake_waiting = 1;
    while (fake_block && fake_head == fake_tail)
        pthread_cond_wait(&fake_changed, &fake_lock);
    fake_waiting = 0;
    if (fake_head == fake_tail) {
        pthread_mutex_unlock(&fake_lock);
        return IMG_NOT_EXIST;
    }
    fake_frame frame = fake_queue[fake_head++ % FAKE_FRAMES];
    pthread_mutex_unlock(&fake_lock);

    uint32_t total = frame.total_skew, used = 0;
    for (int i = 0; i < frame.count; i++) total += frame.sizes[i];
    for (int i = 0; i < frame.count; i++) {
        uint8_t vendor[FAKE_BYTES];
        memcpy(vendor, frame.data + used, frame.sizes[i]);
        uint32_t offset = used + (i > 0 ? frame.offset_skew : 0);
        int sunk = sink(context, vendor, frame.sizes[i], offset, total);
        memset(vendor, 0xee, frame.sizes[i]);
        used += frame.sizes[i];
        if (sunk != 0) return IMG_BUFFER_FULL;
    }
    return frame.result;
}
