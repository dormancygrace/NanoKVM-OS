/* hooks.c: profiling hooks linked into every instrumented OpenSSL binary (LDFLAGS). Inside a library they bind
 * locally, so they look up the counter of the LD_PRELOAD shim callcount.c (cyg_count_enter) once and forward to it;
 * without the shim they do nothing. */
#define _GNU_SOURCE
#include <dlfcn.h>
static void (*hook)(void *, void *);
static int looked;
__attribute__((weak, no_instrument_function)) void __cyg_profile_func_enter(void *fn, void *site)
{
	if (!looked) { hook = (void (*)(void *, void *))dlsym(RTLD_DEFAULT, "cyg_count_enter"); looked = 1; }
	if (hook) hook(fn, site);
}
__attribute__((weak, no_instrument_function)) void __cyg_profile_func_exit(void *fn, void *site)
{
	(void)fn;
	(void)site;
}
