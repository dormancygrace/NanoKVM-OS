/* SPDX-License-Identifier: Apache-2.0 */
/* Load-order cases of the C906 vector gate (NanoKVM OS), against an
 * NKOS_CHACHA_TEST/NKOS_GHASH_TEST libcrypto loaded with dlopen:
 *   --worker  started with vector state off (re-executed so): a worker thread
 *             turns its own vector state on and loads libcrypto; the worker
 *             reaches the vector code, the main thread (still off) must not.
 *   --filter  a seccomp filter that kills prctl is installed before
 *             libcrypto is loaded; with the default environment (no opt-in)
 *             the process must survive on upstream code.
 * Build with -march=rv64gc (the program itself has no vector code). */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <pthread.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

static void check(int ok, const char *what)
{
    if (!ok) {
        fprintf(stderr, "FAIL %s\n", what);
        exit(2);
    }
}

static void *lib;
static unsigned long (*chacha_hits)(void), (*ghash_hits)(void);

static void load(void)
{
    lib = dlopen("libcrypto.so.3", RTLD_NOW | RTLD_GLOBAL);
    check(lib != NULL, "dlopen libcrypto");
    chacha_hits = (unsigned long (*)(void))dlsym(lib, "nkos_chacha_test_hits");
    ghash_hits = (unsigned long (*)(void))dlsym(lib, "nkos_ghash_test_hits");
    check(chacha_hits && ghash_hits, "instrumented library");
}

/* ChaCha20 over 1400 bytes and AES-128-GCM over 16 KiB through EVP. */
static void run(void)
{
    void *(*ctx_new)(void) = dlsym(lib, "EVP_CIPHER_CTX_new");
    void (*ctx_free)(void *) = dlsym(lib, "EVP_CIPHER_CTX_free");
    int (*init)(void *, const void *, const unsigned char *, const unsigned char *, const void *) =
        dlsym(lib, "EVP_EncryptInit_ex2");
    int (*update)(void *, unsigned char *, int *, const unsigned char *, int) = dlsym(lib, "EVP_EncryptUpdate");
    const void *(*chacha)(void) = dlsym(lib, "EVP_chacha20");
    const void *(*gcm)(void) = dlsym(lib, "EVP_aes_128_gcm");
    check(ctx_new && ctx_free && init && update && chacha && gcm, "EVP symbols");
    static unsigned char in[16384], out[16384 + 32];
    unsigned char key[32] = {1}, iv[16] = {2};
    int n;
    void *c = ctx_new();
    check(c && init(c, chacha(), key, iv, NULL) == 1 && update(c, out, &n, in, 1400) == 1 && n == 1400, "ChaCha20");
    ctx_free(c);
    c = ctx_new();
    check(c && init(c, gcm(), key, iv, NULL) == 1 && update(c, out, &n, in, 16384) == 1, "AES-GCM");
    ctx_free(c);
}

static void *worker(void *arg)
{
    (void)arg;
    check(prctl(69, 2L, 0L, 0L, 0L) == 0, "worker vector on");
    load();
    unsigned long c = chacha_hits(), g = ghash_hits();
    run();
    check(chacha_hits() > c && ghash_hits() > g, "worker reaches the vector code");
    return NULL;
}

static int worker_case(void)
{
    check((prctl(70, 0L, 0L, 0L, 0L) & 3) == 1, "started with vector state off");
    pthread_t t;
    check(pthread_create(&t, NULL, worker, NULL) == 0 && pthread_join(t, NULL) == 0, "worker");
    unsigned long c = chacha_hits(), g = ghash_hits();
    run();
    check(chacha_hits() == c && ghash_hits() == g, "main thread (vector off) stays on upstream code");
    check((prctl(70, 0L, 0L, 0L, 0L) & 3) == 1, "main thread still off");
    puts("PASS libcrypto loaded by a vector-on worker: the vector-off main thread stays on upstream code");
    return 0;
}

static int filter_case(void)
{
    check(getenv("NANOKVM_OPENSSL_CHACHA20") == NULL && getenv("NANOKVM_OPENSSL_GHASH") == NULL,
          "default environment");
    struct sock_filter code[] = {
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_prctl, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
    };
    struct sock_fprog prog = { (unsigned short)(sizeof(code) / sizeof(code[0])), code };
    check(prctl(PR_SET_NO_NEW_PRIVS, 1L, 0L, 0L, 0L) == 0, "no new privileges");
    check(prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog) == 0, "seccomp");
    load();
    run();
    check(chacha_hits() == 0 && ghash_hits() == 0, "upstream code without the opt-in");
    puts("PASS libcrypto loaded after a fatal prctl filter: default environment survives on upstream code");
    return 0;
}

int main(int argc, char **argv)
{
    if (argc > 1 && !strcmp(argv[1], "--worker-child"))
        return worker_case();
    if (argc > 1 && !strcmp(argv[1], "--filter"))
        return filter_case();
    if (argc > 1 && !strcmp(argv[1], "--worker")) {
        pid_t pid = fork();
        check(pid >= 0, "fork");
        if (!pid) {
            check(prctl(69, 4L, 0L, 0L, 0L) == 0, "next exec vector off");
            execl(argv[0], argv[0], "--worker-child", NULL);
            _exit(3);
        }
        int st;
        check(waitpid(pid, &st, 0) == pid, "wait");
        if (WIFSIGNALED(st)) {
            fprintf(stderr, "FAIL worker child killed by signal %d\n", WTERMSIG(st));
            return 2;
        }
        return WEXITSTATUS(st);
    }
    fprintf(stderr, "usage: verify-load --worker | --filter\n");
    return 2;
}
