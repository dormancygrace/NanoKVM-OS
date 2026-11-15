// SPDX-License-Identifier: GPL-2.0-only
/*
 * The small core's program: jobs from nanokvm_c906l's ring (../c906l.h).
 *
 * It runs in machine mode with physical addresses. It reads the jobs and the
 * buffers they name and writes only its own memory and the slots' output
 * buffers. Idle, it reads the mailbox register, not memory, so it takes no
 * DDR bandwidth.
 *
 * GHASH: one 8-bit table of 4 KiB per key (Shoup's method, as lib/crypto's
 * C906 GHASH with one table), built here from H and kept for the last KEYS
 * keys. The C906L has 16 KiB of L1 data cache: one table per key leaves room
 * for the other direction's key and the data (two keys: 32 us for 1408
 * bytes, against 36 us with two tables per key).
 */
#include <stdint.h>
typedef uint64_t u64;
typedef int64_t s64;
typedef uint8_t u8;
#include "c906l.h"

#define LINE 64
#define KEYS 4
#define DCACHE_IPA(a) __asm__ volatile("mv a0, %0\n .long 0x02a5000b" :: "r"(a) : "a0", "memory")
#define DCACHE_CPA(a) __asm__ volatile("mv a0, %0\n .long 0x0295000b" :: "r"(a) : "a0", "memory")
#define SYNC_S() __asm__ volatile(".long 0x0190000b" ::: "memory")

void ghash1(u64 acc[2], const u64 *tables, const u8 *data, unsigned long nblocks);

static u64 tables[KEYS][512] __attribute__((aligned(LINE)));
static u64 keys[KEYS][2];
static unsigned int key_next;

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

static u64 be64(const u8 *p)
{
	u64 v = 0;
	for (int i = 0; i < 8; i++)
		v = v << 8 | p[i];
	return v;
}

static void mulx(u64 *hi, u64 *lo)
{
	u64 c = *lo & 1;
	*lo = *hi << 63 | *lo >> 1;
	*hi = *hi >> 1 ^ (c ? 0xe100000000000000ULL : 0);
}

/* The table of H: entry b is b * H, bit 7 of b the lowest power (gen_ghash_table.py prepare_tables()). */
static const u64 *table_of(const u8 *h)
{
	u64 hi = be64(h), lo = be64(h + 8);
	unsigned int k;

	for (k = 0; k < KEYS; k++)
		if (keys[k][0] == hi && keys[k][1] == lo)
			return tables[k];
	k = key_next++ % KEYS;
	keys[k][0] = hi;
	keys[k][1] = lo;
	u64 *tl = tables[k], *th = tl + 256;
	tl[0] = th[0] = 0;
	for (int i = 0x80; i; i >>= 1) {
		tl[i] = lo;
		th[i] = hi;
		mulx(&hi, &lo);
	}
	for (int i = 2; i < 256; i <<= 1)
		for (int j = 1; j < i; j++) {
			tl[i + j] = tl[i] ^ tl[j];
			th[i + j] = th[i] ^ th[j];
		}
	return tables[k];
}

static void ghash(const struct c906l_job *j, struct c906l_result *r)
{
	u64 acc[2] = { j->ghash.acc[0], j->ghash.acc[1] }, len = j->ghash.len, whole = len & ~15ULL;
	const u8 *p = (const u8 *)(uintptr_t)j->ghash.addr;
	const u64 *t = table_of(j->ghash.h);
	u8 last[16];

	invalidate(p, len);
	if (whole)
		ghash1(acc, t, p, whole / 16);
	if (len != whole) {
		for (int b = 0; b < 16; b++)
			last[b] = b < (int)(len - whole) ? p[whole + b] : 0;
		ghash1(acc, t, last, 1);
	}
	ghash1(acc, t, j->ghash.lens, 1);
	r->acc[0] = acc[0];
	r->acc[1] = acc[1];
	r->value = 0;
}

static void raise_big(void)
{
	volatile u8 *m = (volatile u8 *)0x01900000UL;
	SYNC_S();
	m[C906L_MBOX_SET] = C906L_TO_BIG;
}

void main(u8 *mem)
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
			if (j->type == C906L_JOB_GHASH) {
				ghash(j, r);
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
