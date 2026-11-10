// SPDX-License-Identifier: GPL-2.0
/*
 * pmuprof: a sampling profiler for the C906 without CONFIG_PERF_EVENTS.
 * A high-resolution timer (about 1 kHz, jittered) records where it interrupted:
 * kernel pc, or user pc with the process. With each sample it also books the
 * instructions and cache misses counted since the previous sample to the
 * interrupted task and mode. A pc that waits for a cache line is sampled in
 * proportion to the wait, so the profile is of time, misses included.
 *
 * /proc/pmuprof:
 *   T samples idle lost
 *   P tgid mode comm samples instructions l1i-miss l1d-read-miss jtlb-miss
 *   k pc samples
 *   U tgid pc ra samples    (ra: the caller, when pc is in a leaf function)
 */
#include <linux/module.h>
#include <linux/proc_fs.h>
#include <linux/seq_file.h>
#include <linux/hrtimer.h>
#include <linux/sched.h>
#include <linux/vmalloc.h>
#include <asm/irq_regs.h>
#include <asm/ptrace.h>
#include <asm/csr.h>
#include <asm/sbi.h>

struct ev { const char *name; unsigned long idx, data; };
static const struct ev evs[] = {
	{ "instructions",    0x00002, 0 },
	{ "l1i-miss",        0x20000, 0x02 },
	{ "l1d-read-miss",   0x20000, 0x0d },
	{ "jtlb-miss",       0x20000, 0x05 },
};
#define NEV ARRAY_SIZE(evs)
static long ctr[NEV];
static unsigned int csrno[NEV];
static unsigned long last[NEV];

#define C(n) case 0xc00 + n: return csr_read(0xc00 + n);
static unsigned long read_csr(unsigned int n)
{
	switch (n) {
	C(0) C(1) C(2) C(3) C(4) C(5) C(6) C(7) C(8) C(9) C(10) C(11) C(12) C(13) C(14) C(15)
	C(16) C(17) C(18) C(19) C(20) C(21) C(22) C(23) C(24) C(25) C(26) C(27) C(28) C(29) C(30) C(31)
	}
	return 0;
}

#define NB (1 << 16)
struct bkt { u64 key; u32 n; };
static struct bkt *tab;
struct ubkt { u64 pc, ra; u32 tgid, n; };
static struct ubkt *utab;
#define NT 128
struct task { int tgid, mode; char comm[TASK_COMM_LEN]; u64 n, d[NEV]; };
static struct task tasks[NT];
static u64 total, idle, lost;
static struct hrtimer timer;
static u32 lfsr = 0x1234567;
static unsigned int period_us = 1000;
module_param(period_us, uint, 0444);

static enum hrtimer_restart tick(struct hrtimer *h)
{
	struct pt_regs *regs = get_irq_regs();
	unsigned long d[NEV], v;
	int i, user, tgid;
	u64 key;
	u32 slot;

	for (i = 0; i < NEV; i++) {
		v = ctr[i] >= 0 ? read_csr(csrno[i]) : 0;
		d[i] = v - last[i];
		last[i] = v;
	}
	lfsr = (lfsr >> 1) ^ (-(lfsr & 1u) & 0xd0000001u);
	hrtimer_forward_now(h, ns_to_ktime((u64)(period_us - 64 + (lfsr & 127)) * 1000));
	if (!regs)
		return HRTIMER_RESTART;
	total++;
	user = user_mode(regs);
	tgid = current->tgid;
	if (!user && is_idle_task(current))
		idle++;
	for (i = 0; i < NT; i++) {
		struct task *t = &tasks[i];

		if (!t->n) {
			t->tgid = tgid;
			t->mode = user;
			memcpy(t->comm, current->group_leader->comm, TASK_COMM_LEN);
		}
		if (t->tgid == tgid && t->mode == user) {
			int j;

			t->n++;
			for (j = 0; j < NEV; j++)
				t->d[j] += d[j];
			break;
		}
	}
	if (user) {
		key = (regs->epc ^ (regs->ra * 0x9e3779b97f4a7c15ULL) ^ ((u64)tgid << 50)) * 0xff51afd7ed558ccdULL;
		slot = (u32)(key >> 48);
		for (i = 0; i < 64; i++, slot = (slot + 1) & (NB - 1)) {
			struct ubkt *b = &utab[slot];

			if (!b->n) {
				b->pc = regs->epc;
				b->ra = regs->ra;
				b->tgid = tgid;
			}
			if (b->pc == regs->epc && b->ra == regs->ra && b->tgid == tgid) {
				b->n++;
				return HRTIMER_RESTART;
			}
		}
		lost++;
		return HRTIMER_RESTART;
	}
	key = regs->epc & 0xffffffffULL;
	slot = (u32)((key * 0x9e3779b97f4a7c15ULL) >> 48);
	for (i = 0; i < 64; i++, slot = (slot + 1) & (NB - 1)) {
		if (!tab[slot].n)
			tab[slot].key = key;
		if (tab[slot].key == key) {
			tab[slot].n++;
			return HRTIMER_RESTART;
		}
	}
	lost++;
	return HRTIMER_RESTART;
}

static int show(struct seq_file *m, void *v)
{
	int i;

	seq_printf(m, "T %llu %llu %llu\n", total, idle, lost);
	for (i = 0; i < NT && tasks[i].n; i++)
		seq_printf(m, "P %d %c %s %llu %llu %llu %llu %llu\n", tasks[i].tgid, tasks[i].mode ? 'u' : 'k',
			   tasks[i].comm, tasks[i].n, tasks[i].d[0], tasks[i].d[1], tasks[i].d[2], tasks[i].d[3]);
	for (i = 0; i < NB; i++) {
		if (tab[i].n)
			seq_printf(m, "k ffffffff%08llx %u\n", tab[i].key, tab[i].n);
		if (utab[i].n)
			seq_printf(m, "U %u %llx %llx %u\n", utab[i].tgid, utab[i].pc, utab[i].ra, utab[i].n);
	}
	return 0;
}

static int __init pmuprof_init(void)
{
	struct sbiret r;
	unsigned long mask;
	int i;

	tab = vzalloc(NB * sizeof(*tab));
	utab = vzalloc(NB * sizeof(*utab));
	if (!tab || !utab) {
		vfree(tab);
		vfree(utab);
		return -ENOMEM;
	}
	r = sbi_ecall(SBI_EXT_PMU, SBI_EXT_PMU_NUM_COUNTERS, 0, 0, 0, 0, 0, 0);
	if (r.error) {
		vfree(tab);
		vfree(utab);
		return -ENODEV;
	}
	mask = r.value >= BITS_PER_LONG ? ~0UL : (1UL << r.value) - 1;
	for (i = 0; i < NEV; i++) {
		ctr[i] = -1;
		r = sbi_ecall(SBI_EXT_PMU, SBI_EXT_PMU_COUNTER_CFG_MATCH, 0, mask,
			      SBI_PMU_CFG_FLAG_CLEAR_VALUE | SBI_PMU_CFG_FLAG_AUTO_START,
			      evs[i].idx, evs[i].data, 0);
		if (r.error) {
			pr_warn("pmuprof: %s: no counter (%ld)\n", evs[i].name, r.error);
			continue;
		}
		ctr[i] = r.value;
		r = sbi_ecall(SBI_EXT_PMU, SBI_EXT_PMU_COUNTER_GET_INFO, ctr[i], 0, 0, 0, 0, 0);
		if (r.error || (r.value >> (BITS_PER_LONG - 1))) {
			ctr[i] = -1;
			continue;
		}
		csrno[i] = r.value & 0xfff;
		last[i] = read_csr(csrno[i]);
	}
	proc_create_single("pmuprof", 0444, NULL, show);
	hrtimer_setup(&timer, tick, CLOCK_MONOTONIC, HRTIMER_MODE_REL_HARD);
	hrtimer_start(&timer, ns_to_ktime((u64)period_us * 1000), HRTIMER_MODE_REL_HARD);
	return 0;
}

static void __exit pmuprof_exit(void)
{
	int i;

	hrtimer_cancel(&timer);
	remove_proc_entry("pmuprof", NULL);
	for (i = 0; i < NEV; i++)
		if (ctr[i] >= 0)
			sbi_ecall(SBI_EXT_PMU, SBI_EXT_PMU_COUNTER_STOP, ctr[i], 1,
				  SBI_PMU_STOP_FLAG_RESET, 0, 0, 0);
	vfree(tab);
	vfree(utab);
}

module_init(pmuprof_init);
module_exit(pmuprof_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("C906 sampling profiler: pc samples and miss counts per task, no perf");
