// USB host speaker capture -> 20 ms length-prefixed Opus packets on stdout.
#include <tinyalsa/asoundlib.h>
#include <opus.h>
#include <errno.h>
#include <signal.h>
#include <math.h>
#include <time.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <unistd.h>

static int write_all(const void *buffer, size_t size) {
    const char *p = buffer;
    while (size) {
        ssize_t n = write(STDOUT_FILENO, p, size);
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) return -1;
        p += n; size -= (size_t)n;
    }
    return 0;
}
int main(int argc, char **argv) {
    if (argc != 2) return 2;
    pid_t parent = getppid();
    if (parent == 1 || prctl(PR_SET_PDEATHSIG, SIGTERM) < 0 || getppid() != parent) return 1;
    int from_stdin = strcmp(argv[1], "--encode-stdin") == 0;
    int benchmark = strcmp(argv[1], "--benchmark") == 0;
    unsigned int count = 0;
    long long encode_ns = 0;
    struct pcm *pcm = NULL;
    if (!from_stdin && !benchmark) {
        char *end;
        unsigned long card = strtoul(argv[1], &end, 10);
        if (*end || end == argv[1] || card > 255) return 2;
        struct pcm_config config = {
            .channels = 2, .rate = 48000, .period_size = 960,
            .period_count = 4, .format = PCM_FORMAT_S16_LE,
        };
        pcm = pcm_open((unsigned int)card, 0, PCM_IN, &config);
        if (!pcm || !pcm_is_ready(pcm)) {
            fprintf(stderr, "USB audio: %s\n", pcm ? pcm_get_error(pcm) : "open failed");
            if (pcm) pcm_close(pcm);
            return 1;
        }
    }
    int error;
    OpusEncoder *encoder = opus_encoder_create(48000, 2, OPUS_APPLICATION_AUDIO, &error);
    int status = 1;
    if (!encoder || error != OPUS_OK) goto done;
    if (opus_encoder_ctl(encoder, OPUS_SET_BITRATE(192000)) != OPUS_OK ||
        opus_encoder_ctl(encoder, OPUS_SET_COMPLEXITY(3)) != OPUS_OK ||
        opus_encoder_ctl(encoder, OPUS_SET_VBR_CONSTRAINT(1)) != OPUS_OK) goto done;
    opus_int16 buffer[960 * 2];
    unsigned char packet[1275];
    for (;;) {
        size_t filled = 0;
        if (benchmark) {
            if (count == 500) {
                fprintf(stderr, "{\"audioSeconds\":10,\"encodeCpuSeconds\":%.6f,\"encodeCorePercent\":%.3f,\"bitrate\":192000,\"complexity\":3}\n", encode_ns / 1e9, encode_ns / 1e8);
                status = 0; goto done;
            }
            for (unsigned int i = 0; i < 960; i++) {
                double phase = (count * 960 + i) / 48000.0;
                buffer[i*2] = (opus_int16)(6000*sin(2*3.141592653589793*440*phase));
                buffer[i*2+1] = (opus_int16)(6000*sin(2*3.141592653589793*880*phase));
            }
            filled = 960;
        }
        while (filled < 960) {
            if (from_stdin) {
                size_t n = fread(buffer + filled * 2, 4, 960 - filled, stdin);
                if (n == 0) { status = ferror(stdin) || filled != 0; goto done; }
                filled += n;
            } else {
                int frames = pcm_readi(pcm, buffer + filled * 2, 960 - filled);
                if (frames == -EPIPE && pcm_prepare(pcm) == 0) { filled = 0; continue; }
                if (frames <= 0) { fprintf(stderr, "USB audio: %s\n", pcm_get_error(pcm)); goto done; }
                filled += (size_t)frames;
            }
        }
        struct timespec before, after;
        if (benchmark) clock_gettime(CLOCK_PROCESS_CPUTIME_ID, &before);
        int bytes = opus_encode(encoder, buffer, 960, packet, sizeof(packet));
        if (benchmark) {
            clock_gettime(CLOCK_PROCESS_CPUTIME_ID, &after);
            encode_ns += (after.tv_sec-before.tv_sec)*1000000000LL + after.tv_nsec-before.tv_nsec;
            count++;
        }
        if (bytes <= 0) goto done;
        unsigned char header[2] = {(unsigned char)(bytes >> 8), (unsigned char)bytes};
        if (write_all(header, sizeof(header)) || write_all(packet, (size_t)bytes)) goto done;
    }
done:
    if (encoder) opus_encoder_destroy(encoder);
    if (pcm) pcm_close(pcm);
    return status;
}
