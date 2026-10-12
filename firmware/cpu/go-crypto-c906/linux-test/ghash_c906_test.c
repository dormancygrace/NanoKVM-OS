// SPDX-License-Identifier: GPL-2.0-only
/*
 * Device qualification of the C906 GHASH (NanoKVM OS): the scalar function and
 * the five-block XTheadVector + scalar one, with the same assembly and key
 * preparation as lib/crypto/riscv/gf128hash.h. Checks them against a
 * bit-serial GHASH in process context and with bottom halves disabled, under
 * forced preemption (a CPU hog against a thread inside the vector section),
 * and the library entry points ghash_update()/ghash_final() of whichever
 * libgf128hash is loaded; then times the library and both functions. Loading
 * prints the results and fails with -EAGAIN on purpose, so nothing stays
 * loaded.
 */
#include <asm/simd.h>
#include <asm/vector.h>
#include <crypto/gf128hash.h>
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

static int cases = 20000;
module_param(cases, int, 0444);
static int bench_ms = 300;
module_param(bench_ms, int, 0444);
static int preempt_ms = 4000;
module_param(preempt_ms, int, 0444);

#define KP_WORDS	112	/* 40 scalar words, then 576 bytes of lane key parts */
#define GROUP		(5 * GHASH_BLOCK_SIZE)

asmlinkage void ghash_blocks_c906(struct polyval_elem *acc, const u64 kp[40],
				  const u8 *data, size_t nblocks);
asmlinkage void ghash_blocks_c906_v(struct polyval_elem *acc, const u64 kp[KP_WORDS],
				    const u8 *data, size_t ngroups);

/* a = a * b * x^-128, as polyval_mul_generic() (static in lib/crypto). */
static void dot(u64 a[2], const u64 b[2])
{
	u64 c[4] = {}, x;
	int i, k;

	for (i = 0; i < 128; i++) {
		int w = i / 64, s = i % 64;

		if (!((b[w] >> s) & 1))
			continue;
		for (k = 3; k >= 0; k--) {
			u64 v = 0;

			if (k - w >= 0 && k - w < 2)
				v = a[k - w] << s;
			if (s && k - w - 1 >= 0 && k - w - 1 < 2)
				v |= a[k - w - 1] >> (64 - s);
			c[k] ^= v;
		}
	}
	for (i = 0; i < 2; i++) {
		x = c[i];
		c[i + 1] ^= (x << 63) ^ (x << 62) ^ (x << 57);
		c[i + 2] ^= x ^ (x >> 1) ^ (x >> 2) ^ (x >> 7);
	}
	a[0] = c[2];
	a[1] = c[3];
}

/* Same as ghash_c906_preparekey() in lib/crypto/riscv/gf128hash.h. */
static void c906_preparekey(u64 kp[KP_WORDS], const struct polyval_elem *h)
{
	static const u64 holes[4] = {
		0x1111111111111111ULL, 0x2222222222222222ULL,
		0x4444444444444444ULL, 0x8888888888888888ULL,
	};
	u64 pw[6][2];
	u32 *lanes = (u32 *)&kp[40];
	int p, r, j, l;

	pw[1][0] = le64_to_cpu(h->lo);
	pw[1][1] = le64_to_cpu(h->hi);
	for (p = 2; p <= 5; p++) {
		pw[p][0] = pw[p - 1][0];
		pw[p][1] = pw[p - 1][1];
		dot(pw[p], pw[1]);
	}
	for (j = 0; j < 4; j++)
		kp[j] = holes[j];
	for (p = 0; p < 3; p++) {
		for (l = 0; l <= 4; l++) {
			/* l = 4: H for the scalar words; l = 0..3: H^(5-l) in lane l. */
			u64 *x = pw[l == 4 ? 1 : 5 - l];
			u64 part = p == 0 ? x[0] : p == 1 ? x[1] : x[0] ^ x[1];
			u64 ops[3] = { (u32)part, part >> 32, (u32)part ^ (part >> 32) };

			for (r = 0; r < 3; r++)
				for (j = 0; j < 4; j++) {
					if (l == 4)
						kp[4 + 4 * (3 * p + r) + j] = ops[r] & holes[j];
					else
						lanes[16 * (3 * p + r) + 4 * j + l] = ops[r] & (u32)holes[j];
				}
		}
	}
	memzero_explicit(pw, sizeof(pw));
}

struct c906_key {
	struct ghash_key key;	/* for ->h, in POLYVAL format */
	u64 kp[KP_WORDS];
};

static void c906_setkey(struct c906_key *k, const u8 raw[GHASH_BLOCK_SIZE])
{
	ghash_preparekey(&k->key, raw);
	c906_preparekey(k->kp, &k->key.h);
}

/* As ghash_blocks_arch(): five-block groups in the vector unit, the rest scalar. */
static void c906_blocks(const struct c906_key *k, struct polyval_elem *acc,
			const u8 *data, size_t nblocks, bool vector)
{
	if (vector && nblocks >= 5 && may_use_simd()) {
		size_t groups = nblocks / 5;

		kernel_vector_begin();
		ghash_blocks_c906_v(acc, k->kp, data, groups);
		kernel_vector_end();
		data += groups * GROUP;
		nblocks -= groups * 5;
	}
	if (nblocks)
		ghash_blocks_c906(acc, k->kp, data, nblocks);
}

static void c906_ghash(const struct c906_key *k, const u8 *data, size_t nblocks,
		       bool vector, u8 out[GHASH_BLOCK_SIZE])
{
	struct polyval_elem acc = {};

	c906_blocks(k, &acc, data, nblocks, vector);
	put_unaligned_be64(le64_to_cpu(acc.hi), out);
	put_unaligned_be64(le64_to_cpu(acc.lo), out + 8);
}

/* NIST SP 800-38D multiplication, one bit at a time: x = x * h. */
static void ref_mul(u8 x[16], const u8 h[16])
{
	u8 z[16] = {}, v[16];
	int i, k;

	memcpy(v, h, 16);
	for (i = 0; i < 128; i++) {
		u8 lsb = v[15] & 1;

		if ((x[i / 8] >> (7 - i % 8)) & 1)
			for (k = 0; k < 16; k++)
				z[k] ^= v[k];
		for (k = 15; k > 0; k--)
			v[k] = (v[k] >> 1) | (v[k - 1] << 7);
		v[0] >>= 1;
		if (lsb)
			v[0] ^= 0xe1;
	}
	memcpy(x, z, 16);
}

/* y = GHASH continued over len bytes, a last partial block zero-padded. */
static void ref_ghash(const u8 h[16], const u8 *data, size_t len, u8 y[16])
{
	size_t i, k;

	for (i = 0; i < len; i += 16) {
		for (k = 0; k < 16 && i + k < len; k++)
			y[k] ^= data[i + k];
		ref_mul(y, h);
	}
}

/* Random keys and messages: both functions, and the library, against ref_ghash(). */
static int check_random(int n, bool bh, bool lib)
{
	u8 *buf = kmalloc(1600 + 16, GFP_KERNEL), raw[16], want[16], whole[16], got[16];
	struct c906_key *k = kmalloc(sizeof(*k), GFP_KERNEL);
	struct ghash_ctx ctx;
	int i, fails = 0, vfails = 0, lib_fails = 0;

	if (!buf || !k) {
		kfree(buf);
		kfree(k);
		return -ENOMEM;
	}
	for (i = 0; i < n; i++) {
		size_t len = i < 64 ? 16 * (i % 12) : get_random_u32_below(i % 5 ? 1025 : 1601);
		unsigned int off = get_random_u32_below(16);	/* any alignment */
		size_t split = len ? get_random_u32_below(len + 1) : 0;

		get_random_bytes(raw, 16);
		get_random_bytes(buf + off, len);
		if (i % 97 == 0)
			memset(raw, 0xff, 16);
		if (i % 89 == 0)
			memset(buf + off, 0xff, len);
		c906_setkey(k, raw);
		memset(whole, 0, 16);
		ref_ghash(raw, buf + off, len & ~15, whole);
		memcpy(want, whole, 16);
		ref_ghash(raw, buf + off + (len & ~15), len & 15, want);

		if (bh)
			local_bh_disable();
		c906_ghash(k, buf + off, len / 16, false, got);
		if (memcmp(got, whole, 16) && fails++ < 3)
			pr_err("ghash_c906: scalar mismatch, %zu blocks, offset %u\n", len / 16, off);
		c906_ghash(k, buf + off, len / 16, true, got);
		if (memcmp(got, whole, 16) && vfails++ < 3)
			pr_err("ghash_c906: vector mismatch, %zu blocks, offset %u\n", len / 16, off);
		if (bh)
			local_bh_enable();

		if (lib) {
			ghash_init(&ctx, &k->key);
			ghash_update(&ctx, buf + off, split);
			ghash_update(&ctx, buf + off + split, len - split);
			ghash_final(&ctx, got);
			if (memcmp(got, want, 16) && lib_fails++ < 3)
				pr_err("ghash_c906: ghash_update() mismatch, %zu bytes split at %zu\n",
				       len, split);
		}
		cond_resched();
	}
	memzero_explicit(k, sizeof(*k));
	kfree(k);
	kfree(buf);
	pr_info("ghash_c906: %d cases%s against a bit-serial GHASH: scalar %d, vector %d%s failures\n",
		n, bh ? " with bottom halves disabled" : "", fails, vfails,
		lib ? (lib_fails ? ", ghash_update() some" : ", ghash_update() 0") : "");
	return fails + vfails + lib_fails;
}

/* Preemption inside the vector section: a CPU hog against a hashing thread. */
struct preempt_job {
	u64 deadline;
	const struct c906_key *k;
	const u8 *in;
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
		c906_ghash(j->k, j->in, j->len / 16, true, got);
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
	struct preempt_job j = { .len = 65520 };	/* 4095 blocks: 819 groups */
	struct c906_key *k = kmalloc(sizeof(*k), GFP_KERNEL);
	struct hog_job h;
	u8 raw[16];
	unsigned long sw = current->nivcsw;
	int fails = 0, rnd;
	u8 *in = vmalloc(j.len);

	if (!in || !k) {
		fails = -ENOMEM;
		goto out;
	}
	get_random_bytes(in, j.len);
	get_random_bytes(raw, 16);
	c906_setkey(k, raw);
	c906_ghash(k, in, j.len / 16, false, j.want);	/* the scalar function, checked above */
	j.k = k;
	j.in = in;
	init_completion(&j.done);
	init_completion(&h.done);
	h.deadline = j.deadline = ktime_get_ns() + (u64)preempt_ms * NSEC_PER_MSEC;
	if (IS_ERR(kthread_run(hog_fn, &h, "ghash_c906_hog")) ||
	    IS_ERR(kthread_run(victim_fn, &j, "ghash_c906_victim"))) {
		fails = -ECHILD;
		goto out;
	}
	rnd = check_random(1000, false, false);
	wait_for_completion(&j.done);
	wait_for_completion(&h.done);
	pr_info("ghash_c906: preemption: victim %lu calls of %u bytes, %lu involuntary switches, %lu mismatches; random cases under load: %d failures, %lu involuntary switches\n",
		j.calls, j.len, j.nivcsw, j.mismatches, rnd, current->nivcsw - sw);
	if (j.mismatches || rnd)
		fails = 1;
	if (!j.nivcsw)
		pr_warn("ghash_c906: preemption: no involuntary switch observed\n");
out:
	vfree(in);
	kfree_sensitive(k);
	return fails;
}

static u64 mbps(u64 bytes, u64 ns)
{
	return div64_u64(bytes * 1000, ns);
}

static u64 rate_lib(const u8 *buf, unsigned int len)
{
	struct ghash_key key;
	struct ghash_ctx ctx;
	u8 raw[16] = { 1 }, out[16];
	u64 start, end, bytes = 0;

	ghash_preparekey(&key, raw);
	start = ktime_get_ns();
	do {
		ghash_init(&ctx, &key);
		ghash_update(&ctx, buf, len);
		ghash_final(&ctx, out);
		bytes += len;
		end = ktime_get_ns();
	} while (end - start < (u64)bench_ms * NSEC_PER_MSEC);
	return mbps(bytes, end - start);
}

static u64 rate_c906(const u8 *buf, unsigned int len, bool vector)
{
	static struct c906_key k;
	u8 raw[16] = { 1 }, out[16];
	u64 start, end, bytes = 0;

	c906_setkey(&k, raw);
	start = ktime_get_ns();
	do {
		c906_ghash(&k, buf, len / 16, vector, out);
		bytes += len;
		end = ktime_get_ns();
	} while (end - start < (u64)bench_ms * NSEC_PER_MSEC);
	return mbps(bytes, end - start);
}

static int __init ghash_c906_init(void)
{
	static const unsigned int sizes[] = { 80, 160, 240, 400, 512, 1408, 4096, 16384 };
	u8 *buf;
	int i, err;

	if (!has_xtheadvector())
		return -ENODEV;
	err = check_random(cases, false, true);
	err |= check_random(cases / 4, true, false);
	err |= check_preemption();
	if (err)
		return -EINVAL;
	buf = vmalloc(16384);
	if (!buf)
		return -ENOMEM;
	memset(buf, 0x33, 16384);
	pr_info("ghash_c906: %6s %10s %10s %10s  (MB/s)\n", "bytes", "library", "scalar", "vector");
	for (i = 0; i < ARRAY_SIZE(sizes); i++)
		pr_info("ghash_c906: %6u %10llu %10llu %10llu\n", sizes[i],
			rate_lib(buf, sizes[i]), rate_c906(buf, sizes[i], false),
			rate_c906(buf, sizes[i], true));
	vfree(buf);
	return -EAGAIN;
}
module_init(ghash_c906_init);

MODULE_DESCRIPTION("C906 GHASH qualification");
MODULE_LICENSE("GPL");
