// SPDX-License-Identifier: GPL-2.0-only
/*
 * The small core's program: jobs from nanokvm_c906l's ring (../c906l.h).
 *
 * It runs in machine mode with physical addresses. It reads the jobs and the
 * buffers they name and writes only its own memory and the slots' output
 * buffers. Idle, it reads the mailbox register, not memory, so it takes no
 * DDR bandwidth.
 *
 * Opus: libopus 1.6.1, float, plain C (no vector unit), built into the
 * program; C906L_OPUS_ENCODERS encoders in static memory, 48 kHz stereo,
 * OPUS_APPLICATION_AUDIO. A 20 ms frame of music at 192 kbit/s and
 * complexity 3 takes about 2 ms here, the same bytes as on the big core.
 */
#include <stdint.h>
#include <string.h>
#include "opus.h"
typedef uint64_t u64;
typedef int64_t s64;
typedef uint8_t u8;
#include "c906l.h"

#define LINE 64
#define DCACHE_IPA(a) __asm__ volatile("mv a0, %0\n .long 0x02a5000b" :: "r"(a) : "a0", "memory")
#define DCACHE_CPA(a) __asm__ volatile("mv a0, %0\n .long 0x0295000b" :: "r"(a) : "a0", "memory")
#define SYNC_S() __asm__ volatile(".long 0x0190000b" ::: "memory")

static u64 encoders[C906L_OPUS_ENCODERS][8192] __attribute__((aligned(LINE)));	/* 64 KiB each */
static u8 encoder_ready[C906L_OPUS_ENCODERS];

static inline u64 mcycle(void) { u64 v; __asm__ volatile("csrr %0, mcycle" : "=r"(v)); return v; }

static void invalidate(const void *p, u64 len)
{
	uintptr_t a = (uintptr_t)p & ~(uintptr_t)(LINE - 1), end = (uintptr_t)p + len;
	for (; a < end; a += LINE)
		DCACHE_IPA(a);
	SYNC_S();
}

static void clean(const void *p, u64 len)
{
	uintptr_t a = (uintptr_t)p & ~(uintptr_t)(LINE - 1), end = (uintptr_t)p + len;
	for (; a < end; a += LINE)
		DCACHE_CPA(a);
	SYNC_S();
}

static void job_opus_init(const struct c906l_job *j, struct c906l_result *r)
{
	u64 e = j->opus_init.encoder;
	OpusEncoder *enc = (OpusEncoder *)encoders[e];
	int err;

	encoder_ready[e] = 0;
	if (opus_encoder_get_size(2) > (int)sizeof(encoders[e])) {
		r->value = OPUS_INTERNAL_ERROR;
		return;
	}
	err = opus_encoder_init(enc, 48000, 2, OPUS_APPLICATION_AUDIO);
	if (err == OPUS_OK)
		err = opus_encoder_ctl(enc, OPUS_SET_BITRATE((int)j->opus_init.bitrate));
	if (err == OPUS_OK)
		err = opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY((int)j->opus_init.complexity));
	if (err == OPUS_OK)
		err = opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT((int)j->opus_init.vbr_constraint));
	encoder_ready[e] = err == OPUS_OK;
	r->value = err;
}

static void job_opus(const struct c906l_job *j, struct c906l_result *r)
{
	u64 e = j->opus.encoder;
	const int16_t *pcm = (const int16_t *)(uintptr_t)j->opus.pcm;
	u8 *out = (u8 *)(uintptr_t)j->opus.out;
	int n;

	if (!encoder_ready[e]) {
		r->value = OPUS_INVALID_STATE;
		return;
	}
	invalidate(pcm, C906L_OPUS_FRAME * 4);
	n = opus_encode((OpusEncoder *)encoders[e], pcm, C906L_OPUS_FRAME, out, (int)j->opus.out_size);
	if (n > 0)
		clean(out, n);
	r->value = n;
}

static void raise_big(void)
{
	volatile u8 *m = (volatile u8 *)0x01900000UL;
	SYNC_S();
	m[C906L_MBOX_SET] = C906L_TO_BIG;
}

void c906l_main(u8 *mem)
{
	volatile u8 *m = (volatile u8 *)0x01900000UL;
	struct c906l_status *st = (struct c906l_status *)(mem + C906L_STATUS);
	const struct c906l_config *config = (const struct c906l_config *)(mem + C906L_CONFIG);
	u8 *ring;
	u64 next = 1;

	invalidate(config, sizeof(*config));
	ring = (u8 *)(uintptr_t)config->ring;
	st->magic = C906L_MAGIC;
	clean(st, sizeof(*st));
	for (;;) {
		while (!(m[C906L_MBOX_INT(2)] & C906L_TO_SMALL))
			;
		/* Cleared before the ring is read: a job written after this raises it again. */
		m[C906L_MBOX_CLR(2)] = C906L_TO_SMALL;
		SYNC_S();
		for (;;) {
			struct c906l_job *j = (struct c906l_job *)(ring + (next % C906L_SLOTS) * C906L_SLOT_SIZE);
			struct c906l_result *r = (struct c906l_result *)(mem + C906L_RESULTS + (next % C906L_SLOTS) * C906L_RESULT_SIZE);

			invalidate(j, sizeof(*j));
			if (j->seq != next)
				break;
			u64 c0 = mcycle();
			if (j->type == C906L_JOB_OPUS_INIT && j->opus_init.encoder < C906L_OPUS_ENCODERS) {
				job_opus_init(j, r);
			} else if (j->type == C906L_JOB_OPUS_ENCODE && j->opus.encoder < C906L_OPUS_ENCODERS) {
				job_opus(j, r);
			} else {
				r->value = -1;
				st->errors++;
			}
			r->cycles = mcycle() - c0;
			st->jobs++;
			st->busy_cycles += r->cycles;
			SYNC_S();
			r->seq = next;
			clean(r, sizeof(*r));
			clean(st, sizeof(*st));
			raise_big();
			next++;
		}
	}
}
