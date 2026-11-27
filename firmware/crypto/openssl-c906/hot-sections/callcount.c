/* callcount.c: LD_PRELOAD shim for OpenSSL built with -finstrument-functions and hooks.c: counts calls per function
 * and writes "module offset count" lines to $CYG_OUT.<pid> at exit and on SIGUSR2; SIGUSR1 clears the counts (a
 * running server: clear, apply the load, write). Build with -pthread.
 *
 * Any thread may count: a function claims its slot with a compare-and-swap and the count is an atomic add. A slot
 * keeps its function for the life of the process and clearing zeroes only the counts, so no call is lost or counted
 * for another function. The signal handlers only write a byte to a pipe; a thread of the shim, started by the first
 * counted call of the process, clears or writes on that. A process that counted nothing writes no file, and a child
 * of fork() starts from zero. The file appears under its name once it is complete.
 *
 * A signal returns before that thread has acted. The thread keeps $CYG_OUT.<pid>.state, one line
 * "cleared N written M", replaced whole: it appears when the thread starts, and N or M grows once a clear or a write
 * is complete (an empty sample removes the previous profile). A driver reads N, sends SIGUSR1 and waits
 * until N has grown before it starts the load; likewise with M around SIGUSR2. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <link.h>
#include <pthread.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

#define SLOTS (1 << 16)
static _Atomic uintptr_t slot_fn[SLOTS];
static atomic_ulong slot_n[SLOTS];
static atomic_int unstarted = 1;                          /* this process has no signal thread yet */
static atomic_int wake = -1;                              /* write end of the pipe to that thread */
static int wake_read = -1;
static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER; /* clear, write, fork */
static unsigned long cleared, written;                   /* requests the signal thread has completed */

static void start(void);

void __cyg_profile_func_enter(void *fn, void *site)
{
	uintptr_t f = (uintptr_t)fn;
	unsigned h = (unsigned)((f >> 1) * 2654435761u) & (SLOTS - 1);
	(void)site;
	if (atomic_load_explicit(&unstarted, memory_order_relaxed)) start();
	for (int probes = 0; probes < SLOTS; probes++, h = (h + 1) & (SLOTS - 1)) {
		uintptr_t cur = atomic_load_explicit(&slot_fn[h], memory_order_relaxed);
		if (cur == 0 && atomic_compare_exchange_strong(&slot_fn[h], &cur, f)) cur = f;
		if (cur == f) {
			atomic_fetch_add_explicit(&slot_n[h], 1, memory_order_relaxed);
			return;
		}
	}
}
void __cyg_profile_func_exit(void *fn, void *site) { (void)fn; (void)site; }
/* The hooks inside the instrumented libraries bind locally and forward here. */
void cyg_count_enter(void *fn, void *site) __attribute__((alias("__cyg_profile_func_enter")));

static void clear(void)
{
	for (int s = 0; s < SLOTS; s++) atomic_store(&slot_n[s], 0);
}

struct mod { uintptr_t lo, hi, base; char name[128]; };
struct mods { int n; struct mod m[32]; };
static int add_mod(struct dl_phdr_info *i, size_t size, void *data)
{
	struct mods *mods = data;
	(void)size;
	for (int p = 0; p < i->dlpi_phnum && mods->n < 32; p++) {
		struct mod *m = &mods->m[mods->n];
		if (i->dlpi_phdr[p].p_type != PT_LOAD || !(i->dlpi_phdr[p].p_flags & PF_X)) continue;
		m->base = i->dlpi_addr;
		m->lo = i->dlpi_addr + i->dlpi_phdr[p].p_vaddr;
		m->hi = m->lo + i->dlpi_phdr[p].p_memsz;
		snprintf(m->name, sizeof m->name, "%s", i->dlpi_name && i->dlpi_name[0] ? i->dlpi_name : "main");
		mods->n++;
	}
	return 0;
}

static void out_path(char *path, size_t size, const char *suffix)
{
	const char *out = getenv("CYG_OUT");
	snprintf(path, size, "%s.%d%s", out ? out : "/var/tmp/cyg.out", (int)getpid(), suffix);
}

/* Called with the lock held: tell a waiting driver what is complete. */
static void state(void)
{
	char path[256], tmp[264];
	out_path(path, sizeof path, ".state");
	snprintf(tmp, sizeof tmp, "%s.tmp", path);
	FILE *f = fopen(tmp, "w");
	if (!f) return;
	fprintf(f, "cleared %lu written %lu\n", cleared, written);
	if (fclose(f) == 0) rename(tmp, path);
}

/* Called with the lock held. */
static int dump(int requested)
{
	static unsigned long snap[SLOTS];
	char path[256], tmp[264];
	struct mods mods = { 0 };
	int any = 0;
	for (int s = 0; s < SLOTS; s++) any |= (snap[s] = atomic_load(&slot_n[s])) != 0;
	out_path(path, sizeof path, "");
	if (!any) return !requested || unlink(path) == 0 || errno == ENOENT;
	snprintf(tmp, sizeof tmp, "%s.tmp", path);
	FILE *f = fopen(tmp, "w");
	if (!f) return 0;
	dl_iterate_phdr(add_mod, &mods);
	for (int s = 0; s < SLOTS; s++) {
		uintptr_t fn = atomic_load(&slot_fn[s]);
		if (!snap[s]) continue;
		for (int m = 0; m < mods.n; m++)
			if (fn >= mods.m[m].lo && fn < mods.m[m].hi) {
				fprintf(f, "%s %lx %lu\n", mods.m[m].name, (unsigned long)(fn - mods.m[m].base), snap[s]);
				break;
			}
	}
	int ok = !ferror(f);
	if (fclose(f) == 0 && ok && rename(tmp, path) == 0) return 1;
	unlink(tmp);
	return 0;
}

static void *serve(void *arg)
{
	int fd = (int)(intptr_t)arg;
	pthread_mutex_lock(&lock);
	state();
	pthread_mutex_unlock(&lock);
	for (;;) {
		char c;
		ssize_t r = read(fd, &c, 1);
		if (r < 0 && errno == EINTR) continue;
		if (r != 1) break;
		pthread_mutex_lock(&lock);
		if (c == 'c') {
			clear();
			cleared++;
		} else if (dump(1)) {
			written++;
		}
		state();
		pthread_mutex_unlock(&lock);
	}
	close(fd);
	return NULL;
}

/* The first counted call of a process: the pipe and the thread that serves the signals. */
static void start(void)
{
	int expected = 1, fds[2];
	sigset_t all, old;
	pthread_t thread;
	if (!atomic_compare_exchange_strong(&unstarted, &expected, 0)) return;
	if (pipe2(fds, O_CLOEXEC)) return;
	fcntl(fds[1], F_SETFL, O_NONBLOCK);
	sigfillset(&all);
	pthread_sigmask(SIG_SETMASK, &all, &old); /* the thread takes no signals of the program */
	if (pthread_create(&thread, NULL, serve, (void *)(intptr_t)fds[0]) == 0) {
		pthread_detach(thread);
		wake_read = fds[0];
		atomic_store(&wake, fds[1]);
	} else {
		close(fds[0]);
		close(fds[1]);
	}
	pthread_sigmask(SIG_SETMASK, &old, NULL);
}

static void on_signal(int sig)
{
	int saved = errno, fd = atomic_load(&wake);
	char c = sig == SIGUSR1 ? 'c' : 'd';
	if (fd >= 0 && write(fd, &c, 1) < 0) { /* nothing to do in a handler */ }
	errno = saved;
}

static void before_fork(void) { pthread_mutex_lock(&lock); }
static void after_fork_parent(void) { pthread_mutex_unlock(&lock); }
/* The child has the parent's counts and pipe but not its thread: it starts from zero like a new process. */
static void after_fork_child(void)
{
	int fd = atomic_exchange(&wake, -1);
	if (fd >= 0) {
		close(fd);
		close(wake_read);
	}
	clear();
	cleared = written = 0;
	atomic_store(&unstarted, 1);
	pthread_mutex_unlock(&lock);
}

__attribute__((constructor)) static void init(void)
{
	struct sigaction sa = { .sa_handler = on_signal, .sa_flags = SA_RESTART };
	sigemptyset(&sa.sa_mask);
	sigaction(SIGUSR1, &sa, NULL);
	sigaction(SIGUSR2, &sa, NULL);
	pthread_atfork(before_fork, after_fork_parent, after_fork_child);
}

__attribute__((destructor)) static void at_exit(void)
{
	pthread_mutex_lock(&lock);
	dump(0);
	pthread_mutex_unlock(&lock);
}
