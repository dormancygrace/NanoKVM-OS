// USB host speaker capture -> 20 ms length-prefixed Opus packets on stdout.
// The encoder runs on the SoC's small core (/dev/nanokvm-opus, nanokvm_c906l).
#include <tinyalsa/asoundlib.h>
#include "nanokvm_opus.h"
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <math.h>
#include <poll.h>
#include <sound/asound.h>
#include <time.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
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

// The gadget's "Capture Rate" control reads 0 while the host's playback is off, and the kernel
// notifies its changes: the helper waits for the host in poll() instead of failing and being
// started again every two seconds.
struct host_rate { int fd; struct snd_ctl_elem_id id; };

static int host_rate_open(struct host_rate *h, unsigned int card) {
    char path[32];
    snprintf(path, sizeof(path), "/dev/snd/controlC%u", card);
    h->fd = open(path, O_RDONLY | O_CLOEXEC);
    if (h->fd < 0) return -1;
    struct snd_ctl_elem_id ids[64];
    struct snd_ctl_elem_list list = {.space = 64, .pids = ids};
    if (ioctl(h->fd, SNDRV_CTL_IOCTL_ELEM_LIST, &list) < 0) return -1;
    for (unsigned int i = 0; i < list.used; i++) {
        if (strcmp((const char *)ids[i].name, "Capture Rate")) continue;
        h->id = ids[i];
        int subscribe = 1;
        return ioctl(h->fd, SNDRV_CTL_IOCTL_SUBSCRIBE_EVENTS, &subscribe);
    }
    errno = ENOENT;
    return -1;
}

static long host_rate_read(struct host_rate *h) {
    struct snd_ctl_elem_value value = {.id = h->id};
    if (ioctl(h->fd, SNDRV_CTL_IOCTL_ELEM_READ, &value) < 0) return -1;
    return value.value.integer.value[0];
}

// Subscribed before the first read, so a change between the read and poll() is not lost.
static int host_rate_wait(struct host_rate *h) {
    for (;;) {
        long rate = host_rate_read(h);
        if (rate < 0) return -1;
        if (rate > 0) return 0;
        struct pollfd p = {.fd = h->fd, .events = POLLIN};
        if (poll(&p, 1, -1) < 0) {
            if (errno == EINTR) continue;
            return -1;
        }
        struct snd_ctl_event events[8];
        if ((p.revents & POLLIN) && read(h->fd, events, sizeof(events)) < 0 && errno != EINTR) return -1;
    }
}

int main(int argc, char **argv) {
    if (argc != 2) return 2;
    pid_t parent = getppid();
    if (parent == 1 || prctl(PR_SET_PDEATHSIG, SIGTERM) < 0 || getppid() != parent) return 1;
    int from_stdin = strcmp(argv[1], "--encode-stdin") == 0;
    int benchmark = strcmp(argv[1], "--benchmark") == 0;
    unsigned int count = 0;
    long long encode_ns = 0;
    unsigned long card = 0;
    struct pcm *pcm = NULL;
    struct host_rate host = {.fd = -1};
    struct pcm_config config = {
        .channels = 2, .rate = 48000, .period_size = 960,
        .period_count = 4, .format = PCM_FORMAT_S16_LE,
    };
    if (!from_stdin && !benchmark) {
        char *end;
        card = strtoul(argv[1], &end, 10);
        if (*end || end == argv[1] || card > 255) return 2;
        if (host_rate_open(&host, (unsigned int)card) < 0) {
            fprintf(stderr, "USB audio: Capture Rate control: %s\n", strerror(errno));
            if (host.fd >= 0) close(host.fd);
            return 1;
        }
    }
    int status = 1;
    int encoder = open("/dev/nanokvm-opus", O_RDWR | O_CLOEXEC);
    struct nanokvm_opus_setup setup = { .bitrate = 192000, .complexity = 3, .vbr_constraint = 1 };
    if (encoder < 0 || ioctl(encoder, NANOKVM_OPUS_SETUP, &setup) < 0) {
        fprintf(stderr, "USB audio: Opus encoder: %s\n", strerror(errno));
        goto done;
    }
    short buffer[960 * 2];
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
                buffer[i*2] = (short)(6000*sin(2*3.141592653589793*440*phase));
                buffer[i*2+1] = (short)(6000*sin(2*3.141592653589793*880*phase));
            }
            filled = 960;
        }
        while (filled < 960) {
            if (from_stdin) {
                size_t n = fread(buffer + filled * 2, 4, 960 - filled, stdin);
                if (n == 0) { status = ferror(stdin) || filled != 0; goto done; }
                filled += n;
                continue;
            }
            if (!pcm) {
                if (host_rate_wait(&host) < 0) {
                    fprintf(stderr, "USB audio: Capture Rate control: %s\n", strerror(errno));
                    goto done;
                }
                pcm = pcm_open((unsigned int)card, 0, PCM_IN, &config);
                if (!pcm || !pcm_is_ready(pcm)) {
                    fprintf(stderr, "USB audio: %s\n", pcm ? pcm_get_error(pcm) : "open failed");
                    goto done;
                }
            }
            int frames = pcm_readi(pcm, buffer + filled * 2, 960 - filled);
            if (frames == -EPIPE && pcm_prepare(pcm) == 0) { filled = 0; continue; }
            if (frames <= 0) {
                // Only a host that stopped playing is waited for again; any other error ends the
                // helper as before.
                if (host_rate_read(&host) != 0) {
                    fprintf(stderr, "USB audio: %s\n", pcm_get_error(pcm));
                    goto done;
                }
                pcm_close(pcm);
                pcm = NULL;
                filled = 0;
                continue;
            }
            filled += (size_t)frames;
        }
        struct timespec before, after;
        if (benchmark) clock_gettime(CLOCK_PROCESS_CPUTIME_ID, &before);
        struct nanokvm_opus_encode frame = {
            .pcm = (unsigned long)buffer, .packet = (unsigned long)packet, .packet_size = sizeof(packet),
        };
        int bytes = ioctl(encoder, NANOKVM_OPUS_ENCODE, &frame);
        if (benchmark) {
            clock_gettime(CLOCK_PROCESS_CPUTIME_ID, &after);
            encode_ns += (after.tv_sec-before.tv_sec)*1000000000LL + after.tv_nsec-before.tv_nsec;
            count++;
        }
        if (bytes <= 0) { fprintf(stderr, "USB audio: Opus encoder: %s\n", strerror(errno)); goto done; }
        unsigned char header[2] = {(unsigned char)(bytes >> 8), (unsigned char)bytes};
        if (write_all(header, sizeof(header)) || write_all(packet, (size_t)bytes)) goto done;
    }
done:
    if (encoder >= 0) close(encoder);
    if (pcm) pcm_close(pcm);
    if (host.fd >= 0) close(host.fd);
    return status;
}
