// Host contract test of libvenc's stream pack mappings (cvi_venc.c and
// devmem.c compiled for the host) against a fake encoder driver and a fake
// /dev/mem. Run by scripts/test-venc-pack-map.py.
//
//   venc-pack-map-contract windows   default mode: persistent pack windows
//   venc-pack-map-contract single    with NANOKVM_VENC_PACK_WINDOWS=0
//
// It checks that every pack pointer shows the bytes the fake encoder wrote,
// that ReleaseStream restores the driver's addresses, that windows replace
// the per-frame mappings, that every mapping is unmapped exactly once, and
// that two channels on two threads never block (SIGALRM ends a hang).
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <signal.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/syscall.h>
#include <unistd.h>

#include "cvi_venc.h"
#include <linux/cvi_vc_drv_ioctl.h>

CVI_S32 *log_levels;
CVI_CHAR const *log_name[8];

#define PHYS0		0x88000000ULL
#define PHYS_SIZE	(64ULL << 20)
#define KERNEL_VA(p)	((CVI_U8 *)(uintptr_t)(0xffffffd000000000ULL + (p)))
#define CHANNELS	2

static uint8_t *arena;			/* fake physical memory */
static int devmem_fd = -1;
static int device_fd[CHANNELS] = {-1, -1};
static pthread_mutex_t fake_lock = PTHREAD_MUTEX_INITIALIZER;
static unsigned long failures;

#define CHECK(cond, ...) do { if (!(cond)) { \
	__atomic_add_fetch(&failures, 1, __ATOMIC_RELAXED); \
	fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__); fprintf(stderr, __VA_ARGS__); \
	fputc('\n', stderr); } } while (0)

/* Live /dev/mem mappings, to catch double, partial and missing unmaps. */
#define MAX_LIVE 256
static struct { uint8_t *addr; size_t len; } live[MAX_LIVE];
static unsigned long devmem_mmaps, devmem_munmaps, window_mmaps;
static int fail_large_every;		/* fail every Nth window (read-only) mapping */
static unsigned long large_requests;

int open(const char *path, int flags, ...)
{
	(void)flags;
	int fd = (int)syscall(SYS_openat, AT_FDCWD, "/dev/null", O_RDWR | O_CLOEXEC);

	if (fd < 0)
		return fd;
	if (!strcmp(path, "/dev/mem")) {
		devmem_fd = fd;
	} else {
		int ch;

		if (sscanf(path, "/dev/" CVI_VC_DRV_ENCODER_DEV_NAME "%d", &ch) == 1 && ch >= 0 && ch < CHANNELS)
			device_fd[ch] = fd;
	}
	return fd;
}

void *mmap(void *addr, size_t len, int prot, int flags, int fd, off_t offset)
{
	if (fd < 0 || fd != devmem_fd)
		return (void *)syscall(SYS_mmap, addr, len, prot, flags, fd, offset);
	pthread_mutex_lock(&fake_lock);
	void *result = MAP_FAILED;
	CHECK(offset % 4096 == 0, "unaligned /dev/mem offset %#llx", (unsigned long long)offset);
	CHECK(flags & MAP_SHARED, "private /dev/mem mapping");
	if ((uint64_t)offset < PHYS0 || (uint64_t)offset + len > PHYS0 + PHYS_SIZE || !len) {
		/* Outside the fake RAM: a real kernel would map it, the test cannot. */
		CHECK(0, "mapping %#llx+%zu outside the fake RAM", (unsigned long long)offset, len);
		goto out;
	}
	if (prot == PROT_READ && fail_large_every && ++large_requests % fail_large_every == 0) {
		errno = ENOMEM;
		goto out;
	}
	for (int i = 0; i < MAX_LIVE; i++) {
		if (!live[i].addr) {
			/* Distinct mappings may alias in the arena; the bookkeeping keys on (addr, len). */
			live[i].addr = arena + (offset - PHYS0);
			live[i].len = len;
			result = live[i].addr;
			devmem_mmaps++;
			if (prot == PROT_READ)
				window_mmaps++;
			break;
		}
	}
	CHECK(result != MAP_FAILED, "too many live /dev/mem mappings");
out:
	pthread_mutex_unlock(&fake_lock);
	return result;
}

int munmap(void *addr, size_t len)
{
	if ((uint8_t *)addr < arena || (uint8_t *)addr >= arena + PHYS_SIZE)
		return (int)syscall(SYS_munmap, addr, len);
	pthread_mutex_lock(&fake_lock);
	int found = 0;

	for (int i = 0; i < MAX_LIVE; i++) {
		if (live[i].addr == addr && live[i].len == len) {
			live[i].addr = NULL;
			found = 1;
			devmem_munmaps++;
			break;
		}
	}
	CHECK(found, "munmap of %p+%zu that is not mapped", addr, len);
	pthread_mutex_unlock(&fake_lock);
	return found ? 0 : -1;
}

static int live_mappings(void)
{
	int n = 0;

	pthread_mutex_lock(&fake_lock);
	for (int i = 0; i < MAX_LIVE; i++)
		n += live[i].addr != NULL;
	pthread_mutex_unlock(&fake_lock);
	return n;
}

/* Fake encoder: H.265 on channel 0 (ES queue in a 1 MiB buffer, header copies
 * in kmalloc memory every 30th frame), JPEG on channel 1. */
typedef struct {
	uint64_t es_base, es_size, es_rd, kmalloc_base;
	unsigned frame;
	unsigned rng, seed;	/* random state; content seed of the current frame */
	VENC_PACK_S expected[8];
	unsigned packs;
	int outstanding;
} fake_chn_t;
static fake_chn_t fake[CHANNELS];

static uint8_t pattern(unsigned seed, uint32_t k) { return (uint8_t)(seed * 2654435761u + k * 31u + (k >> 8)); }

static void fill(uint64_t phys, uint32_t len, unsigned seed)
{
	for (uint32_t k = 0; k < len; k++)
		arena[phys - PHYS0 + k] = pattern(seed, k);
}

static unsigned rnd(unsigned *s) { *s = *s * 1103515245u + 12345u; return *s >> 8; }

static void add_pack(fake_chn_t *f, uint64_t phys, uint32_t len)
{
	VENC_PACK_S *p = &f->expected[f->packs++];

	memset(p, 0, sizeof(*p));
	p->u64PhyAddr = phys;
	p->pu8Addr = KERNEL_VA(phys);
	p->u32Len = len;
	fill(phys, len, f->seed * 16 + f->packs);
}

static void encode(int ch)
{
	fake_chn_t *f = &fake[ch];

	f->packs = 0;
	f->seed = rnd(&f->rng) | 1;
	if (ch == 1) {
		add_pack(f, f->es_base, 20000 + rnd(&f->rng) % 300000);
		return;
	}
	const int idr = f->frame++ % 30 == 0;
	const uint32_t len = idr ? 40000 + rnd(&f->rng) % 120000 : 1000 + rnd(&f->rng) % 30000;

	if (idr) {
		for (int h = 0; h < 3; h++) /* VPS, SPS, PPS copies at fresh kmalloc addresses */
			add_pack(f, f->kmalloc_base + (rnd(&f->rng) % (16u << 20) & ~63u), 10 + h * 25);
	}
	if (f->es_rd + len > f->es_base + f->es_size)
		f->es_rd = f->es_base;
	add_pack(f, f->es_rd, len);
	f->es_rd += (len + 255) & ~255u;
}

int ioctl(int fd, unsigned long request, ...)
{
	va_list ap;
	void *arg;
	int ch = -1;

	va_start(ap, request);
	arg = va_arg(ap, void *);
	va_end(ap);
	for (int i = 0; i < CHANNELS; i++)
		if (fd == device_fd[i])
			ch = i;
	if (ch < 0)
		return 0;
	fake_chn_t *f = &fake[ch];

	switch (request) {
	case CVI_VC_VENC_GET_STREAM: {
		VENC_STREAM_S *s = *(VENC_STREAM_S **)arg;

		encode(ch);
		memcpy(s->pstPack, f->expected, f->packs * sizeof(VENC_PACK_S));
		s->u32PackCount = f->packs;
		f->outstanding = 1;
		return 0;
	}
	case CVI_VC_VENC_RELEASE_STREAM: {
		VENC_STREAM_S *s = arg;

		for (unsigned i = 0; i < s->u32PackCount && i < f->packs; i++)
			CHECK(s->pstPack[i].pu8Addr == f->expected[i].pu8Addr,
			      "ch%d pack %u: release got %p, not the driver address", ch, i, s->pstPack[i].pu8Addr);
		f->outstanding = 0;
		return 0;
	}
	default:
		return 0;
	}
}

static void verify(int ch, const VENC_STREAM_S *s)
{
	fake_chn_t *f = &fake[ch];

	CHECK(s->u32PackCount == f->packs, "ch%d pack count %u != %u", ch, s->u32PackCount, f->packs);
	for (unsigned i = 0; i < s->u32PackCount; i++) {
		const VENC_PACK_S *p = &s->pstPack[i];

		CHECK(p->pu8Addr != NULL, "ch%d pack %u has no address", ch, i);
		if (!p->pu8Addr)
			continue;
		CHECK(p->pu8Addr == arena + (p->u64PhyAddr - PHYS0), "ch%d pack %u points to %p, expected %p",
		      ch, i, p->pu8Addr, arena + (p->u64PhyAddr - PHYS0));
		unsigned seed = f->seed * 16 + i + 1;

		for (uint32_t k = 0; k < p->u32Len; k += 509)
			if (p->pu8Addr[k] != pattern(seed, k)) {
				CHECK(0, "ch%d pack %u byte %u differs", ch, i, k);
				break;
			}
	}
}

static void run_channel(int ch, unsigned frames, int skip_release_every)
{
	VENC_PACK_S packs[16];
	VENC_STREAM_S stream;

	for (unsigned n = 0; n < frames; n++) {
		memset(&stream, 0, sizeof(stream));
		stream.pstPack = packs;
		CHECK(CVI_VENC_GetStream(ch, &stream, 1000) == CVI_SUCCESS, "ch%d GetStream failed", ch);
		verify(ch, &stream);
		if (skip_release_every && n % skip_release_every == 0)
			continue; /* caller bug: GetStream again without release */
		CHECK(CVI_VENC_ReleaseStream(ch, &stream) == CVI_SUCCESS, "ch%d ReleaseStream failed", ch);
	}
}

static void *thread_main(void *arg)
{
	run_channel((int)(intptr_t)arg, 20000, 0);
	return NULL;
}

int main(int argc, char **argv)
{
	const int windows = argc > 1 && !strcmp(argv[1], "windows");
	VENC_CHN_ATTR_S attr;

	alarm(120); /* a deadlock ends the test with SIGALRM */
	arena = aligned_alloc(4096, PHYS_SIZE);
	if (!arena)
		return 2;
	memset(&attr, 0, sizeof(attr));
	fake[0] = (fake_chn_t){ .es_base = 0x88123000, .es_size = 1 << 20, .es_rd = 0x88123000,
				.kmalloc_base = 0x8a000000, .rng = 7 };
	fake[1] = (fake_chn_t){ .es_base = 0x89400800, .es_size = 1 << 20, .kmalloc_base = 0x8b000000, .rng = 11 };
	for (int ch = 0; ch < CHANNELS; ch++)
		CHECK(CVI_VENC_CreateChn(ch, &attr) == CVI_SUCCESS, "CreateChn %d", ch);

	/* 1. One channel: content, restore, and how many mappings it costs. */
	unsigned long m0 = devmem_mmaps;

	run_channel(0, 3000, 0);
	const double per_frame = (double)(devmem_mmaps - m0) / 3000;

	printf("h265 3000 frames: %.3f /dev/mem mmaps per frame (%lu windows)\n", per_frame, window_mmaps);
	if (windows)
		CHECK(per_frame < 0.2, "windows still map %.3f times per frame", per_frame);
	else
		CHECK(per_frame > 1.0 && window_mmaps == 0, "single mode mapped %.3f per frame, %lu windows",
		      per_frame, window_mmaps);
	CHECK(devmem_mmaps - devmem_munmaps <= (windows ? 4 : 0), "%lu mappings left after release",
	      devmem_mmaps - devmem_munmaps);

	/* 2. Two channels on two threads. */
	pthread_t t[CHANNELS];

	for (int ch = 0; ch < CHANNELS; ch++)
		pthread_create(&t[ch], NULL, thread_main, (void *)(intptr_t)ch);
	for (int ch = 0; ch < CHANNELS; ch++)
		pthread_join(t[ch], NULL);
	printf("two threads x 20000 frames done\n");

	/* 3. Window mappings that fail fall back to single mappings. */
	fail_large_every = 3;
	run_channel(0, 2000, 0);
	fail_large_every = 0;

	/* 4. A stream that is never released is dropped by the next GetStream. */
	run_channel(0, 500, 7);
	{
		VENC_STREAM_S s = { 0 };

		CHECK(CVI_VENC_ReleaseStream(0, &s) == CVI_SUCCESS, "final release");
	}

	/* 5. Destroying the channels unmaps everything. */
	for (int ch = 0; ch < CHANNELS; ch++)
		CHECK(CVI_VENC_DestroyChn(ch) == CVI_SUCCESS, "DestroyChn %d", ch);
	CHECK(live_mappings() == 0, "%d /dev/mem mappings left after DestroyChn", live_mappings());
	CHECK(devmem_mmaps == devmem_munmaps, "mmap %lu != munmap %lu", devmem_mmaps, devmem_munmaps);

	printf("%s: mmap %lu, munmap %lu, windows %lu, failures %lu\n", windows ? "windows" : "single",
	       devmem_mmaps, devmem_munmaps, window_mmaps, failures);
	return failures ? 1 : 0;
}
