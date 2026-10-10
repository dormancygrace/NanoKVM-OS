// VENC access-unit copy benchmark through the public libkvm API.
//
//   venc-copy-bench [frames [width height [codec [kbps [gop [fps [out.h26x]]]]]]]
//   defaults:        600     2560  1440   h265   4500  60    60
//
// Needs the capture to itself: stop NanoKVM-Server first. Per access unit it
// reports the CPU copy of the borrowed VENC packs (first copy, then the same
// bytes again), a heap-to-heap copy of the same size for reference, the
// process CPU time, and the mmap/munmap calls that the media libraries make.
// The executable interposes mmap and munmap; musl's own allocator uses its
// internal entry points and is not counted.
//
// Reading the result:
// - copy MB/s far below the heap reference, and the second copy as slow as
//   the first, means the pack mapping is uncached;
// - mmap/AU and munmap/AU of about 1 (one pack per delta frame) are the
//   per-frame pack mappings of CVI_VENC_GetStream/ReleaseStream; with
//   persistent pack windows they drop to about 0.
// With out.h26x the stream is written for an offline decode check, e.g.
// ffmpeg -v error -i out.h265 -f null -
// Progress goes to stderr; a watchdog prints every thread's wait channel and
// system call when nothing moves for 15 s, and exits with status 3 at 120 s.
// NANOKVM_VENC_PACK_WINDOWS=0 makes a patched libvenc map packs per frame.
#include "kvm_vision.h"

#include <cerrno>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <dirent.h>
#include <pthread.h>
#include <sys/mman.h>
#include <sys/resource.h>
#include <sys/syscall.h>
#include <time.h>
#include <unistd.h>

static unsigned long mmap_calls, mmap_shared_fd, munmap_calls;

extern "C" void *mmap(void *addr, size_t length, int prot, int flags, int fd, off_t offset)
{
	__atomic_add_fetch(&mmap_calls, 1, __ATOMIC_RELAXED);
	if (fd >= 0 && (flags & MAP_SHARED))
		__atomic_add_fetch(&mmap_shared_fd, 1, __ATOMIC_RELAXED);
	return reinterpret_cast<void *>(syscall(SYS_mmap, addr, length, prot, flags, fd, offset));
}

extern "C" int munmap(void *addr, size_t length)
{
	__atomic_add_fetch(&munmap_calls, 1, __ATOMIC_RELAXED);
	return static_cast<int>(syscall(SYS_munmap, addr, length));
}

static uint64_t now_ns(clockid_t clock = CLOCK_MONOTONIC)
{
	timespec t{};
	clock_gettime(clock, &t);
	return static_cast<uint64_t>(t.tv_sec) * 1000000000u + static_cast<uint64_t>(t.tv_nsec);
}

static constexpr size_t kMaxAccessUnit = 8u << 20;

struct State {
	uint8_t *first, *second, *reference;
	uint32_t total;
	bool bad;
	uint64_t copy1_ns, copy2_ns, bytes;
};

static int sink(uintptr_t context, const uint8_t *data, uint32_t size, uint32_t offset, uint32_t total)
{
	State &s = *reinterpret_cast<State *>(context);
	if (!data || !size || total > kMaxAccessUnit || offset > total || size > total - offset) {
		s.bad = true;
		return -1;
	}
	const uint64_t t0 = now_ns();
	memcpy(s.first + offset, data, size);
	const uint64_t t1 = now_ns();
	memcpy(s.second + offset, data, size);
	const uint64_t t2 = now_ns();
	s.copy1_ns += t1 - t0;
	s.copy2_ns += t2 - t1;
	s.bytes += size;
	s.total = total;
	return 0;
}

// Annex-B start code and a zero forbidden bit at the front of the unit.
static bool plausible(const uint8_t *p, uint32_t n)
{
	if (n >= 5 && !p[0] && !p[1] && !p[2] && p[3] == 1) return !(p[4] & 0x80);
	if (n >= 4 && !p[0] && !p[1] && p[2] == 1) return !(p[3] & 0x80);
	return false;
}

static double rate(uint64_t bytes, uint64_t ns) { return ns ? bytes * 1e3 / ns : 0; }

// Watchdog: when the bench makes no progress for 15 s it prints the phase and,
// for every thread, its name, state, kernel wait channel and current system
// call; after 120 s it exits with status 3.
static const char *volatile phase = "start";
static volatile uint64_t progress_ns;

static void mark(const char *name)
{
	phase = name;
	progress_ns = now_ns();
	fprintf(stderr, "[bench] %s\n", name);
}

static void read_line(const char *path, char *buf, size_t size)
{
	buf[0] = 0;
	FILE *f = fopen(path, "r");
	if (!f) return;
	if (!fgets(buf, static_cast<int>(size), f)) buf[0] = 0;
	fclose(f);
	buf[strcspn(buf, "\n")] = 0;
}

static void dump_threads()
{
	DIR *dir = opendir("/proc/self/task");
	if (!dir) return;
	while (dirent *e = readdir(dir)) {
		if (e->d_name[0] == '.') continue;
		char path[320], comm[64], wchan[96], syscall_line[192], stat[512];
		snprintf(path, sizeof(path), "/proc/self/task/%s/comm", e->d_name);
		read_line(path, comm, sizeof(comm));
		snprintf(path, sizeof(path), "/proc/self/task/%s/wchan", e->d_name);
		read_line(path, wchan, sizeof(wchan));
		snprintf(path, sizeof(path), "/proc/self/task/%s/syscall", e->d_name);
		read_line(path, syscall_line, sizeof(syscall_line));
		snprintf(path, sizeof(path), "/proc/self/task/%s/stat", e->d_name);
		read_line(path, stat, sizeof(stat));
		const char *state = strrchr(stat, ')');
		fprintf(stderr, "[watchdog] tid %s %s state %c wchan %s syscall %s\n", e->d_name, comm,
			state && state[1] ? state[2] : '?', wchan, syscall_line);
	}
	closedir(dir);
}

static void *watchdog(void *)
{
	uint64_t reported = 0;
	for (;;) {
		sleep(5);
		const uint64_t idle = now_ns() - progress_ns;
		if (idle > 120000000000ull) {
			fprintf(stderr, "[watchdog] no progress for 120 s in phase %s, exiting\n", phase);
			dump_threads();
			_exit(3);
		}
		if (idle > 15000000000ull && now_ns() - reported > 30000000000ull) {
			fprintf(stderr, "[watchdog] no progress for %llu s in phase %s\n",
				(unsigned long long)(idle / 1000000000u), phase);
			dump_threads();
			reported = now_ns();
		}
	}
	return nullptr;
}

static uint64_t cpu_us(const rusage &r, bool system)
{
	const timeval &t = system ? r.ru_stime : r.ru_utime;
	return static_cast<uint64_t>(t.tv_sec) * 1000000u + static_cast<uint64_t>(t.tv_usec);
}

int main(int argc, char **argv)
{
	const int frames = argc > 1 ? atoi(argv[1]) : 600;
	const int width = argc > 3 ? atoi(argv[2]) : 2560, height = argc > 3 ? atoi(argv[3]) : 1440;
	const int codec = argc > 4 && !strcmp(argv[4], "h264") ? 1 : 2;
	const int kbps = argc > 5 ? atoi(argv[5]) : 4500;
	const int gop = argc > 6 ? atoi(argv[6]) : 60;
	const int fps = argc > 7 ? atoi(argv[7]) : 60;
	const char *out_path = argc > 8 ? argv[8] : nullptr;
	if (frames <= 0 || width <= 0 || height <= 0 || kbps < 500 || kbps > 20000 || gop < 1 || gop > 100
		|| fps < 1 || fps > 120) {
		fprintf(stderr, "usage: %s [frames [width height [h264|h265 [kbps [gop [fps [out]]]]]]]\n", argv[0]);
		return 2;
	}
	setvbuf(stdout, nullptr, _IOLBF, 0);

	State s{};
	s.first = static_cast<uint8_t *>(aligned_alloc(64, kMaxAccessUnit));
	s.second = static_cast<uint8_t *>(aligned_alloc(64, kMaxAccessUnit));
	s.reference = static_cast<uint8_t *>(aligned_alloc(64, kMaxAccessUnit));
	if (!s.first || !s.second || !s.reference) return 1;
	// Fault the destinations in now, so the copies time reads, not page faults.
	memset(s.first, 0, kMaxAccessUnit);
	memset(s.second, 0, kMaxAccessUnit);
	memset(s.reference, 0, kMaxAccessUnit);
	FILE *out = out_path ? fopen(out_path, "wb") : nullptr;
	if (out_path && !out) {
		perror(out_path);
		return 1;
	}

	pthread_t watchdog_thread;
	mark("kvmv_init");
	pthread_create(&watchdog_thread, nullptr, watchdog, nullptr);
	kvmv_init(0);
	set_frame_detact(0);
	mark("kvmv_hdmi_control");
	kvmv_hdmi_control(1);
	const char *windows_env = getenv("NANOKVM_VENC_PACK_WINDOWS");
	printf("venc-copy-bench %dx%d %s %d kbit/s gop %d fps %d, %d access units, NANOKVM_VENC_PACK_WINDOWS=%s\n",
		width, height, codec == 2 ? "h265" : "h264", kbps, gop, fps, frames, windows_env ? windows_env : "(unset)");

	// Warm up: encoder creation, first IDR and the first pack mappings.
	int warm = 0, errors = 0;
	mark("warm-up");
	for (uint64_t start = now_ns(); warm < 2 * fps && now_ns() - start < 20000000000ull;) {
		s.total = 0;
		if (kvmv_read_video_sink(width, height, codec, kbps, gop, fps, sink, reinterpret_cast<uintptr_t>(&s)) >= 0
			&& s.total) {
			if (!warm) mark("first access unit");
			progress_ns = now_ns();
			++warm;
		} else {
			usleep(20000);
		}
	}
	mark("measure");
	if (warm < 2 * fps) {
		fprintf(stderr, "no stable video stream (HDMI input?), %d units in warm-up\n", warm);
		kvmv_hdmi_control(0);
		kvmv_deinit();
		return 1;
	}

	s.copy1_ns = s.copy2_ns = s.bytes = 0;
	uint64_t units = 0, keys = 0, bad = 0, ref_ns = 0, read_ns = 0;
	rusage r0{}, r1{};
	getrusage(RUSAGE_SELF, &r0);
	const unsigned long mmap0 = mmap_calls, shared0 = mmap_shared_fd, munmap0 = munmap_calls;
	const uint64_t wall0 = now_ns();
	while (units < static_cast<uint64_t>(frames)) {
		s.total = 0;
		s.bad = false;
		const uint64_t t0 = now_ns();
		const int ret = kvmv_read_video_sink(width, height, codec, kbps, gop, fps, sink,
			reinterpret_cast<uintptr_t>(&s));
		read_ns += now_ns() - t0;
		if (ret < 0 || !s.total || s.bad) {
			if (++errors > 50) break;
			usleep(20000);
			continue;
		}
		const uint64_t t1 = now_ns();
		memcpy(s.reference, s.first, s.total);
		ref_ns += now_ns() - t1;
		if (memcmp(s.first, s.second, s.total) || !plausible(s.first, s.total)) ++bad;
		if (ret == IMG_VIDEO_TYPE_KEY) ++keys;
		if (out && fwrite(s.first, 1, s.total, out) != s.total) {
			perror(out_path);
			break;
		}
		++units;
		progress_ns = now_ns();
	}
	const uint64_t wall = now_ns() - wall0;
	getrusage(RUSAGE_SELF, &r1);
	const double n = units ? static_cast<double>(units) : 1;
	printf("units %llu (key %llu, implausible %llu, errors %d) in %.2f s = %.1f AU/s\n",
		(unsigned long long)units, (unsigned long long)keys, (unsigned long long)bad, errors, wall / 1e9,
		units / (wall / 1e9));
	printf("bytes/AU %.0f\n", s.bytes / n);
	printf("copy1  %8.2f us/AU  %8.1f MB/s   (borrowed VENC packs, after libkvm's keyframe scan)\n",
		s.copy1_ns / n / 1e3, rate(s.bytes, s.copy1_ns));
	printf("copy2  %8.2f us/AU  %8.1f MB/s   (same packs again)\n", s.copy2_ns / n / 1e3,
		rate(s.bytes, s.copy2_ns));
	printf("heap   %8.2f us/AU  %8.1f MB/s   (heap to heap, same size, reference)\n", ref_ns / n / 1e3,
		rate(s.bytes, ref_ns));
	printf("read   %8.2f ms/AU  (kvmv_read_video_sink wall time, includes waiting for the encoder)\n",
		read_ns / n / 1e6);
	printf("cpu    %8.1f us/AU user, %8.1f us/AU system\n", (cpu_us(r1, false) - cpu_us(r0, false)) / n,
		(cpu_us(r1, true) - cpu_us(r0, true)) / n);
	printf("mmap   %8.3f /AU (shared fd %.3f /AU), munmap %.3f /AU\n", (mmap_calls - mmap0) / n,
		(mmap_shared_fd - shared0) / n, (munmap_calls - munmap0) / n);
	if (out) fclose(out);
	mark("deinit");
	kvmv_hdmi_control(0);
	kvmv_deinit();
	return units == static_cast<uint64_t>(frames) && !bad ? 0 : 1;
}
