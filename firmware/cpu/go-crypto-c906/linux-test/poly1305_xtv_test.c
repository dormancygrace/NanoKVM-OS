// SPDX-License-Identifier: GPL-2.0-only
/*
 * Device qualification of the C906 vector Poly1305 (NanoKVM OS): the glue of
 * lib/crypto/riscv/poly1305.h with Cryptogams and the generated
 * poly1305-riscv64-xtheadvector.S, linked into this module, against the
 * poly1305_init/update/final of whichever libpoly1305 is loaded (Cryptogams
 * on a kernel without the patch). Random keys, lengths, alignments and update
 * splits in process context and with bottom halves disabled, all-ones keys
 * and messages, and a thread hashing inside the vector section against a CPU
 * hog; then times both. Loading prints the results and fails with -EAGAIN on
 * purpose, so nothing stays loaded.
 *
 * Build: copy lib/crypto/riscv/poly1305.h of the patched tree here as
 * poly1305-glue.h, the kernel build's lib/crypto/riscv/poly1305-core.S here,
 * and the generated poly1305-riscv64-xtheadvector.S (see Kbuild).
 */
#include <asm/simd.h>
#include <asm/vector.h>
#include <crypto/internal/poly1305.h>
#include <linux/bottom_half.h>
#include <linux/completion.h>
#include <linux/kernel.h>
#include <linux/kthread.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/random.h>
#include <linux/sched.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/unaligned.h>
#include <linux/vmalloc.h>

#include "poly1305-glue.h"

static int cases = 20000;
module_param(cases, int, 0444);
static int bench_ms = 300;
module_param(bench_ms, int, 0444);
static int preempt_ms = 4000;
module_param(preempt_ms, int, 0444);

/* lib/crypto/poly1305.c with this module's arch functions. */
static void t_init(struct poly1305_desc_ctx *desc, const u8 key[POLY1305_KEY_SIZE])
{
	desc->s[0] = get_unaligned_le32(key + 16);
	desc->s[1] = get_unaligned_le32(key + 20);
	desc->s[2] = get_unaligned_le32(key + 24);
	desc->s[3] = get_unaligned_le32(key + 28);
	desc->buflen = 0;
	poly1305_block_init(&desc->state, key);
}

static void t_update(struct poly1305_desc_ctx *desc, const u8 *src, unsigned int nbytes)
{
	if (desc->buflen + nbytes >= POLY1305_BLOCK_SIZE) {
		unsigned int bulk_len;

		if (desc->buflen) {
			unsigned int l = POLY1305_BLOCK_SIZE - desc->buflen;

			memcpy(&desc->buf[desc->buflen], src, l);
			src += l;
			nbytes -= l;
			poly1305_blocks(&desc->state, desc->buf, POLY1305_BLOCK_SIZE, 1);
			desc->buflen = 0;
		}
		bulk_len = round_down(nbytes, POLY1305_BLOCK_SIZE);
		nbytes %= POLY1305_BLOCK_SIZE;
		if (bulk_len) {
			poly1305_blocks(&desc->state, src, bulk_len, 1);
			src += bulk_len;
		}
	}
	if (nbytes) {
		memcpy(&desc->buf[desc->buflen], src, nbytes);
		desc->buflen += nbytes;
	}
}

static void t_final(struct poly1305_desc_ctx *desc, u8 *dst)
{
	if (unlikely(desc->buflen)) {
		desc->buf[desc->buflen++] = 1;
		memset(desc->buf + desc->buflen, 0, POLY1305_BLOCK_SIZE - desc->buflen);
		poly1305_blocks(&desc->state, desc->buf, POLY1305_BLOCK_SIZE, 0);
	}
	poly1305_emit(&desc->state.h, dst, desc->s);
	memzero_explicit(desc, sizeof(*desc));
}

static void t_mac(u8 out[16], const u8 key[32], const u8 *in, size_t len, size_t split)
{
	struct poly1305_desc_ctx d;

	t_init(&d, key);
	t_update(&d, in, split);
	t_update(&d, in + split, len - split);
	t_final(&d, out);
}

static void ref_mac(u8 out[16], const u8 key[32], const u8 *in, size_t len)
{
	struct poly1305_desc_ctx d;

	poly1305_init(&d, key);
	poly1305_update(&d, in, len);
	poly1305_final(&d, out);
}

static int check_random(int n, bool bh, bool extreme)
{
	u8 *buf = kmalloc(66000, GFP_KERNEL);
	u8 key[32], want[16], got[16];
	int i, fails = 0;

	if (!buf)
		return -ENOMEM;
	for (i = 0; i < n; i++) {
		size_t len = i < 256 ? 16 * i + (i & 15) :
			     i % 50 == 0 ? 65000 + get_random_u32_below(500) :
			     get_random_u32_below(i % 5 ? 2049 : 8193);
		unsigned int off = get_random_u32_below(16);
		size_t split = get_random_u32_below(4) ? 0 : get_random_u32_below(len + 1);

		get_random_bytes(key, 32);
		get_random_bytes(buf + off, len);
		if (extreme) {
			memset(key, 0xff, 32);
			memset(buf + off, 0xff, len);
		}
		ref_mac(want, key, buf + off, len);
		if (bh)
			local_bh_disable();
		t_mac(got, key, buf + off, len, split);
		if (bh)
			local_bh_enable();
		if (memcmp(got, want, 16)) {
			if (fails++ < 5)
				pr_err("poly1305_xtv: mismatch len %zu off %u split %zu bh %d extreme %d\n",
				       len, off, split, bh, extreme);
		}
	}
	kfree(buf);
	return fails;
}

/* Preemption inside the vector section: a CPU hog against a hashing thread. */
struct preempt_job {
	u64 deadline;
	const u8 *in, *key;
	u8 want[16];
	unsigned int len;
	unsigned long calls, mismatches, nivcsw;
	struct completion done;
};

static int victim_fn(void *arg)
{
	struct preempt_job *j = arg;
	unsigned long sw = current->nivcsw;
	u8 got[16];

	while (ktime_get_ns() < j->deadline) {
		t_mac(got, j->key, j->in, j->len, 0);
		if (memcmp(got, j->want, 16))
			j->mismatches++;
		j->calls++;
	}
	j->nivcsw = current->nivcsw - sw;
	complete(&j->done);
	return 0;
}

struct hog_job {
	u64 deadline;
	struct completion done;
};

static int hog_fn(void *arg)
{
	struct hog_job *h = arg;
	volatile u64 spin = 0;

	while (ktime_get_ns() < h->deadline)
		spin++;
	complete(&h->done);
	return 0;
}

static int check_preemption(void)
{
	struct preempt_job j = { .len = 65520 };
	struct hog_job h;
	u8 key[32];
	unsigned long sw = current->nivcsw;
	int fails = 0, rnd;
	u8 *in = vmalloc(j.len);

	if (!in)
		return -ENOMEM;
	get_random_bytes(in, j.len);
	get_random_bytes(key, 32);
	ref_mac(j.want, key, in, j.len);
	j.key = key;
	j.in = in;
	init_completion(&j.done);
	init_completion(&h.done);
	h.deadline = j.deadline = ktime_get_ns() + (u64)preempt_ms * NSEC_PER_MSEC;
	if (IS_ERR(kthread_run(hog_fn, &h, "poly1305_xtv_hog"))) {
		vfree(in);
		return -ECHILD;
	}
	if (IS_ERR(kthread_run(victim_fn, &j, "poly1305_xtv_victim"))) {
		/* Keep the stack job and module text alive until the hog exits. */
		wait_for_completion(&h.done);
		vfree(in);
		return -ECHILD;
	}
	rnd = check_random(1000, false, false);
	wait_for_completion(&j.done);
	wait_for_completion(&h.done);
	pr_info("poly1305_xtv: preemption: victim %lu calls of %u bytes, %lu involuntary switches, %lu mismatches; random cases under load: %d failures, %lu involuntary switches\n",
		j.calls, j.len, j.nivcsw, j.mismatches, rnd, current->nivcsw - sw);
	if (j.mismatches || rnd)
		fails = 1;
	if (!j.nivcsw)
		pr_warn("poly1305_xtv: preemption: no involuntary switch observed\n");
	vfree(in);
	return fails;
}

static u64 mbps(u64 bytes, u64 ns)
{
	return ns ? bytes * 1000 / ns : 0;
}

static u64 bench(bool lib, const u8 *key, const u8 *in, unsigned int len)
{
	u64 start, end, bytes = 0;
	u8 out[16];

	start = ktime_get_ns();
	do {
		int k;

		for (k = 0; k < 16; k++) {
			if (lib)
				ref_mac(out, key, in, len);
			else
				t_mac(out, key, in, len, 0);
		}
		bytes += 16 * len;
		end = ktime_get_ns();
	} while (end - start < (u64)bench_ms * NSEC_PER_MSEC);
	return mbps(bytes, end - start);
}

static int __init poly1305_xtv_init(void)
{
	static const unsigned int sizes[] = { 64, 256, 320, 480, 640, 1420, 4096, 16384, 65536 };
	int fails, i, r;
	u8 key[32];
	u8 *in;

	poly1305_mod_init_arch();
	if (!static_branch_likely(&have_xtheadvector)) {
		pr_err("poly1305_xtv: XTheadVector with VLEN >= 128 not available\n");
		return -ENODEV;
	}
	fails = check_random(cases, false, false);
	r = check_random(cases / 4, true, false);
	fails += r;
	r = check_random(400, false, true);
	fails += r;
	pr_info("poly1305_xtv: %d random cases (process context), %d with bottom halves disabled, 400 all-ones: %d failures\n",
		cases, cases / 4, fails);
	r = check_preemption();
	if (r)
		fails += r;
	in = vmalloc(65536 + 5);
	if (!in)
		return -ENOMEM;
	get_random_bytes(in, 65536 + 5);
	get_random_bytes(key, 32);
	{
		u64 t0, t1, t2;
		u64 r5[5] = { 1, 0, 0, 0, 0 }, h5[5] = { 0 };

		t0 = ktime_get_ns();
		for (i = 0; i < 1000; i++) {
			kernel_vector_begin();
			kernel_vector_end();
		}
		t1 = ktime_get_ns();
		for (i = 0; i < 1000; i++) {
			kernel_vector_begin();
			poly1305_blocks_xtheadvector(r5, in + 5, 1, h5);
			kernel_vector_end();
		}
		t2 = ktime_get_ns();
		pr_info("poly1305_xtv: kernel_vector_begin+end %llu ns, with one group %llu ns\n",
			(t1 - t0) / 1000, (t2 - t1) / 1000);
	}
	pr_info("poly1305_xtv: %6s %10s %10s  (MB/s, offset 5)\n", "bytes", "library", "vector");
	for (i = 0; i < ARRAY_SIZE(sizes); i++)
		pr_info("poly1305_xtv: %6u %10llu %10llu\n", sizes[i],
			bench(true, key, in + 5, sizes[i]), bench(false, key, in + 5, sizes[i]));
	vfree(in);
	pr_info("poly1305_xtv: %s\n", fails ? "FAIL" : "PASS");
	return -EAGAIN;
}
module_init(poly1305_xtv_init);

MODULE_DESCRIPTION("C906 vector Poly1305 qualification (NanoKVM OS)");
MODULE_LICENSE("GPL");
