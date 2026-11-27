/* hooks.c: profiling hooks linked into every instrumented OpenSSL binary (LDFLAGS). Inside a library they bind
 * locally, so they look up the counter of the LD_PRELOAD shim callcount.c (cyg_count_enter) and forward to it;
 * without the shim they do nothing. Any thread may make the first call: each one that finds the pointer unset looks
 * up the same symbol and stores the same value. */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdatomic.h>

typedef void hook_fn(void *, void *);
__attribute__((no_instrument_function)) static void no_shim(void *fn, void *site)
{
	(void)fn;
	(void)site;
}
static _Atomic(hook_fn *) hook;

__attribute__((weak, no_instrument_function)) void __cyg_profile_func_enter(void *fn, void *site)
{
	hook_fn *h = atomic_load_explicit(&hook, memory_order_relaxed);
	if (!h) {
		h = (hook_fn *)dlsym(RTLD_DEFAULT, "cyg_count_enter");
		if (!h) h = no_shim;
		atomic_store(&hook, h);
	}
	h(fn, site);
}
__attribute__((weak, no_instrument_function)) void __cyg_profile_func_exit(void *fn, void *site)
{
	(void)fn;
	(void)site;
}
