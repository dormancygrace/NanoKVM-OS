/* callcount.c: LD_PRELOAD shim for OpenSSL built with -finstrument-functions and hooks.c: counts calls per function
 * and writes "module offset count" lines to $CYG_OUT.<pid> at exit and on SIGUSR2; SIGUSR1 clears the counts (a
 * running server: clear, apply the load, dump). A process that never called the libraries writes nothing. */
#define _GNU_SOURCE
#include <link.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <signal.h>
#include <unistd.h>

#define SLOTS (1 << 16)
static struct { uintptr_t fn; unsigned long n; } tab[SLOTS];

__attribute__((no_instrument_function)) void __cyg_profile_func_enter(void *fn, void *site)
{
	uintptr_t f = (uintptr_t)fn;
	unsigned h = (unsigned)((f >> 1) * 2654435761u) & (SLOTS - 1);
	(void)site;
	while (tab[h].fn && tab[h].fn != f) h = (h + 1) & (SLOTS - 1);
	tab[h].fn = f; tab[h].n++;
}
__attribute__((no_instrument_function)) void __cyg_profile_func_exit(void *fn, void *site) { (void)fn; (void)site; }

struct mod { uintptr_t lo, hi, base; char name[128]; };
static struct mod mods[32]; static int nmods;
__attribute__((no_instrument_function)) static int cb(struct dl_phdr_info *i, size_t size, void *data)
{
	(void)size; (void)data;
	for (int p = 0; p < i->dlpi_phnum && nmods < 32; p++) {
		if (i->dlpi_phdr[p].p_type != PT_LOAD || !(i->dlpi_phdr[p].p_flags & PF_X)) continue;
		mods[nmods].base = i->dlpi_addr;
		mods[nmods].lo = i->dlpi_addr + i->dlpi_phdr[p].p_vaddr;
		mods[nmods].hi = mods[nmods].lo + i->dlpi_phdr[p].p_memsz;
		snprintf(mods[nmods].name, sizeof(mods[nmods].name), "%s", i->dlpi_name && i->dlpi_name[0] ? i->dlpi_name : "main");
		nmods++;
	}
	return 0;
}
__attribute__((destructor, no_instrument_function)) static void dump(void)
{
	const char *out = getenv("CYG_OUT");
	char path[256];
	int any = 0;
	for (int s = 0; s < SLOTS && !any; s++) any = tab[s].n != 0;
	if (!any) return; /* children of a profiled server that never called the libraries */
	snprintf(path, sizeof path, "%s.%d", out ? out : "/var/tmp/cyg.out", (int)getpid());
	FILE *f = fopen(path, "w");
	if (!f) return;
	dl_iterate_phdr(cb, NULL);
	for (int s = 0; s < SLOTS; s++) {
		if (!tab[s].fn || !tab[s].n) continue;
		for (int m = 0; m < nmods; m++)
			if (tab[s].fn >= mods[m].lo && tab[s].fn < mods[m].hi) {
				fprintf(f, "%s %lx %lu\n", mods[m].name, (unsigned long)(tab[s].fn - mods[m].base), tab[s].n);
				break;
			}
	}
	fclose(f);
}

__attribute__((no_instrument_function)) static void on_usr2(int sig) { (void)sig; dump(); }
__attribute__((no_instrument_function)) static void on_usr1(int sig) { (void)sig; memset(tab, 0, sizeof tab); }
__attribute__((constructor, no_instrument_function)) static void init_signals(void)
{
	signal(SIGUSR2, on_usr2);
	signal(SIGUSR1, on_usr1);
}

/* The hooks inside the instrumented libraries bind locally and forward here. */
void cyg_count_enter(void *fn, void *site) __attribute__((alias("__cyg_profile_func_enter")));
