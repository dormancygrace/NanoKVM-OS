#define _POSIX_C_SOURCE 200809L
#include <signal.h>
#include <setjmp.h>
#include <stdio.h>
#include <time.h>
#include <sys/resource.h>
static sigjmp_buf recovery;
static void illegal(int signo) { (void)signo; siglongjmp(recovery, 1); }
static long long ns(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return (long long)t.tv_sec * 1000000000 + t.tv_nsec;
}
int main(void) {
    const struct rlimit lim = {0,0};
    setrlimit(RLIMIT_CORE, &lim);
    struct sigaction sa = {0};
    sa.sa_handler = illegal;
    sigemptyset(&sa.sa_mask);
    if (sigaction(SIGILL, &sa, 0)) return 2;
    if (sigsetjmp(recovery, 1)) {
        puts("fence.tso: SIGILL on this device/firmware");
        return 0;
    }
    __asm__ volatile (".word 0x8330000f" ::: "memory");
    puts("fence.tso: completed (native execution or firmware emulation)");
    for (int trial=0; trial<3; trial++) {
        long long begin = ns();
        for (int i=0;i<10000;i++) __asm__ volatile ("fence rw,rw" ::: "memory");
        long long normal = ns()-begin;
        begin=ns();
        for (int i=0;i<10000;i++) __asm__ volatile (".word 0x8330000f" ::: "memory");
        printf("trial=%d iterations=10000 fence_rw_rw_ns=%lld fence_tso_ns=%lld\n",trial,normal,ns()-begin);
    }
    return 0;
}
