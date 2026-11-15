/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * What nanokvm_c906l and the small core's program (firmware/) share: the
 * layout of the program's memory, the jobs and the results. Words are
 * little-endian.
 *
 * The program's memory (C906L_MEM_SIZE) is mapped uncached by the driver; the
 * small core runs with its caches on, invalidates what the driver writes
 * before reading it and cleans what it writes. The job ring, the staging
 * buffers (job input) and the output buffers are memory of the driver's own,
 * written through its cache and cleaned (ring, staging) or invalidated
 * (output) around the small core's use. A job is in memory before its seq
 * word; a result's seq word is written last.
 *
 * After a job the driver raises mailbox channel 0, after a result the small
 * core raises channel 1. Only the driver writes the enable bits: channel 0
 * for the small core once, at the start, and channel 1 for the big core,
 * which its interrupt handler masks until the results are taken. The small
 * core only raises (SET) and clears its own (CLR).
 */
#ifndef C906L_H
#define C906L_H

#define C906L_MEM_SIZE		0x10000
#define C906L_PROGRAM_MAX	0xe000		/* code, data, key tables, stack */
#define C906L_RESULTS		0xe000		/* result lines */
#define C906L_RESULT_SIZE	64
#define C906L_STATUS		0xf000		/* status line, written by the small core */
#define C906L_CONFIG		0xf040		/* written by the driver before the start */
#define C906L_MAGIC		0x6309060c0de00b01ULL

#define C906L_SLOTS		32
#define C906L_SLOT_SIZE		128
#define C906L_STAGE		4096		/* staging buffer of a slot */
#define C906L_OUT		1536		/* output buffer of a slot */

/* Mailbox (0x01900000), byte registers: cpu 1 = big core, cpu 2 = small core. */
#define C906L_MBOX_EN(cpu)	((cpu) * 4)
#define C906L_MBOX_CLR(cpu)	(0x10 + (cpu) * 0x10)
#define C906L_MBOX_INT(cpu)	(0x18 + (cpu) * 0x10)
#define C906L_MBOX_SET		0x60
#define C906L_TO_SMALL		0x01		/* channel 0 */
#define C906L_TO_BIG		0x02		/* channel 1 */

#define C906L_JOB_GHASH		1

#ifndef __ASSEMBLER__
struct c906l_config {
	u64 ring;			/* physical address of the job ring */
};

struct c906l_job {
	u64 seq;
	u64 type;
	union {
		/*
		 * GHASH of len bytes at addr: acc (in the POLYVAL order of
		 * lib/crypto's struct ghash_ctx: lo, hi) continued over them,
		 * the last partial block padded with zeroes, then the 16 bytes
		 * of lens. Result: acc.
		 */
		struct {
			u8 h[16];	/* hash key, GHASH byte order */
			u64 acc[2];
			u8 lens[16];
			u64 addr, len;
		} ghash;
	};
};

struct c906l_result {
	u64 seq;
	s64 value;
	u64 acc[2];
	u64 cycles;			/* the job's, for statistics */
};

struct c906l_status {
	u64 magic;
	u64 jobs;
	u64 busy_cycles;
	u64 errors;			/* jobs refused: unknown type */
};
#endif
#endif
