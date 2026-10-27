/* SPDX-License-Identifier: Apache-2.0 */
/* Differential verification against the RFC 8439 scalar construction. */
#define _GNU_SOURCE
#include <openssl/evp.h>
#include <dlfcn.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <stddef.h>
#include <errno.h>
#include <sys/wait.h>
#include <unistd.h>

static void check(int yes, const char *where)
{
    if (!yes) { fprintf(stderr, "FAIL %s\n", where); exit(2); }
}
static uint32_t load32(const unsigned char *p)
{
    return (uint32_t)p[0] | (uint32_t)p[1]<<8 | (uint32_t)p[2]<<16 | (uint32_t)p[3]<<24;
}
static uint32_t rot(uint32_t v, int n) { return v<<n | v>>(32-n); }
#define QR(a,b,c,d) do { a+=b; d=rot(d^a,16); c+=d; b=rot(b^c,12); \
                         a+=b; d=rot(d^a,8); c+=d; b=rot(b^c,7); } while (0)
static void reference(unsigned char *out, const unsigned char *in, size_t len,
                      const unsigned char key[32], const unsigned char iv[16])
{
    uint32_t state[16] = {0x61707865,0x3320646e,0x79622d32,0x6b206574};
    for (int i=0;i<8;i++) state[4+i]=load32(key+4*i);
    for (int i=0;i<4;i++) state[12+i]=load32(iv+4*i);
    while (len) {
        uint32_t x[16]; memcpy(x,state,sizeof x);
        for (int n=0;n<10;n++) {
            QR(x[0],x[4],x[8],x[12]); QR(x[1],x[5],x[9],x[13]);
            QR(x[2],x[6],x[10],x[14]); QR(x[3],x[7],x[11],x[15]);
            QR(x[0],x[5],x[10],x[15]); QR(x[1],x[6],x[11],x[12]);
            QR(x[2],x[7],x[8],x[13]); QR(x[3],x[4],x[9],x[14]);
        }
        for (int i=0;i<16;i++) x[i]+=state[i];
        size_t n=len<64?len:64;
        for (size_t i=0;i<n;i++) out[i]=in[i]^(unsigned char)(x[i/4]>>(8*(i%4)));
        if (++state[12]==0) ++state[13]; /* OpenSSL raw ChaCha uses a 64-bit counter. */
        in+=n;out+=n;len-=n;
    }
}
static void run_cases(unsigned seed)
{
    const size_t sizes[]={0,1,31,63,64,65,255,256,319,320,321,639,640,641,
                          1024,1400,4095,4096,4097,16384,65536};
    const uint64_t counters[]={0,1,0xfffffffa,0xfffffffe,UINT64_MAX-3};
    unsigned char key[32],iv[16];
    unsigned char *src=malloc(65568),*want=malloc(65568),*buf=malloc(65568);
    EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new(),*copy=EVP_CIPHER_CTX_new();
    check(src&&want&&buf&&ctx&&copy,"allocate");
    for (int i=0;i<32;i++) key[i]=(unsigned char)(seed+i*7);
    for (int i=0;i<16;i++) iv[i]=(unsigned char)(seed+i*11);
    for (int i=0;i<65568;i++) src[i]=(unsigned char)(seed+i*13);
    for (size_t z=0;z<sizeof sizes/sizeof sizes[0];z++)
    for (size_t k=0;k<sizeof counters/sizeof counters[0];k++)
    for (int off=0;off<16;off++) {
        size_t len=sizes[z],split=(z*17+off)%321;
        if (split>len) split=len;
        for (int i=0;i<8;i++) iv[i]=(unsigned char)(counters[k]>>(8*i));
        reference(want,src+off,len,key,iv);
        memset(buf,0xa5,65568);memcpy(buf+off,src+off,len);
        check(EVP_EncryptInit_ex2(ctx,EVP_chacha20(),key,iv,NULL)==1,"init");
        int n=0;
        check(EVP_EncryptUpdate(ctx,buf+off,&n,buf+off,(int)split)==1&&n==(int)split,"prefix");
        check(EVP_CIPHER_CTX_copy(copy,ctx)==1,"dupctx");
        check(EVP_EncryptUpdate(copy,buf+off+split,&n,buf+off+split,(int)(len-split))==1&&n==(int)(len-split),"in-place suffix");
        check(!memcmp(want,buf+off,len),"differential/counter/split/copy");
        for (int i=0;i<off;i++) check(buf[i]==0xa5,"prefix guard");
        for (size_t i=off+len;i<off+len+16;i++) check(buf[i]==0xa5,"suffix guard");
    }
    EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_CTX_free(copy);free(src);free(want);free(buf);
}
static void verify_hits(int vector_on)
{
    unsigned long (*hits)(void) = (unsigned long (*)(void))dlsym(RTLD_DEFAULT, "nkos_chacha_test_hits");
    check(hits != NULL, "test build assembly counter export");
    const int sizes[] = {0,1,319,320,512,639,640,641,768,1024,1400,16384};
    unsigned char key[32] = {0}, iv[16] = {0}, in[16384] = {0}, out[16384];
    EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new(); check(ctx != NULL, "counter ctx");
    const char *override = getenv("NANOKVM_OPENSSL_CHACHA20");
    int enabled = vector_on && override && !strcmp(override, "auto");
    for (size_t i = 0; i < sizeof(sizes)/sizeof(sizes[0]); ++i) {
        int got; check(EVP_EncryptInit_ex2(ctx, EVP_chacha20(), key, iv, NULL) == 1, "counter init");
        unsigned long before = hits();
        check(EVP_EncryptUpdate(ctx, out, &got, in, sizes[i]) == 1 && got == sizes[i], "counter update");
        check((hits() > before) == (enabled && sizes[i] >= 640), "actual assembly-entry hits");
    }
    EVP_CIPHER_CTX_free(ctx);
    puts("PASS actual assembly-entry counts at threshold boundaries");
}

/* OpenSSL and the provider are loaded before the filter. No cipher call has
 * happened yet. Kill on file opens or another hardware probe; permit or deny
 * the per-call thread-control query in separate children.
 */
static void sandbox_case(int allow_prctl)
{
    EVP_CIPHER *cipher = EVP_CIPHER_fetch(NULL, "CHACHA20", NULL);
    check(cipher != NULL, "sandbox provider load"); EVP_CIPHER_free(cipher);
    struct sock_filter code[] = {
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_openat, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS),
#ifdef __NR_open
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_open, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS),
#endif
#ifdef __NR_openat2
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_openat2, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS),
#endif
#ifdef __NR_riscv_hwprobe
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_riscv_hwprobe, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS),
#endif
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_prctl, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, allow_prctl < 0 ? SECCOMP_RET_KILL_PROCESS :
            allow_prctl ? SECCOMP_RET_ALLOW : (SECCOMP_RET_ERRNO | EPERM)),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
    };
    struct sock_fprog prog = { (unsigned short)(sizeof(code)/sizeof(code[0])), code };
    check(prctl(PR_SET_NO_NEW_PRIVS, 1L, 0L, 0L, 0L) == 0, "sandbox no new privs");
    check(prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog) == 0, "sandbox install");
    verify_hits(allow_prctl == 1);
}
static void verify_sandbox(void)
{
    fflush(NULL);
    for (int allow = 0; allow <= 1; ++allow) {
        pid_t pid = fork(); check(pid >= 0, "sandbox fork");
        if (!pid) { sandbox_case(allow); _exit(0); }
        int status;
        check(waitpid(pid, &status, 0) == pid && WIFEXITED(status) && WEXITSTATUS(status) == 0,
              "sandbox first cipher: no file/probe syscalls; denied prctl falls back");
    }
    puts("PASS seccomp first cipher, with permitted and denied thread-control query");
}

static void *worker(void *v)
{
    unsigned long id=(unsigned long)v;
    if (id == 2) { verify_hits(0); return NULL; }
    if (id == 3) { check(prctl(69,2L,0L,0L,0L)==0,"counter thread vector on"); verify_hits(1); return NULL; }
    /* Called in an exec child initially OFF. One thread enables its own unit. */
    if (id==1) check(prctl(69,2L,0L,0L,0L)==0,"thread vector on");
    run_cases((unsigned)id+19);
    long mode=prctl(70,0L,0L,0L,0L);
    check(mode>=0&&(mode&3)==(id==1?2:1),"per-thread control");
    return NULL;
}
int main(int argc,char **argv)
{
    if (argc > 1 && !strcmp(argv[1], "--hard-sandbox")) {
        check(getenv("NANOKVM_OPENSSL_CHACHA20") == NULL, "default policy child");
        sandbox_case(-1);
        puts("PASS default policy with fatal prctl/open/hwprobe filter");
        return 0;
    }
    if (argc>1&&!strcmp(argv[1],"--off-child")) {
        check((prctl(70,0L,0L,0L,0L)&3)==1,"exec vector off");
        pthread_t proof;
        if (argc > 2 && !strcmp(argv[2], "--hits")) {
            for (unsigned long i = 2; i <= 3; ++i) {
                check(pthread_create(&proof, NULL, worker, (void *)i) == 0, "counter thread create");
                check(pthread_join(proof, NULL) == 0, "counter thread join");
            }
        }
        pthread_t threads[2];
        for(unsigned long i=0;i<2;i++) check(pthread_create(&threads[i],NULL,worker,(void *)i)==0,"thread create");
        for(int i=0;i<2;i++) check(pthread_join(threads[i],NULL)==0,"thread join");
        puts("PASS vector-off/vector-on threads");return 0;
    }
    int instrumented = argc > 2 && !strcmp(argv[2], "--hits");
    if (instrumented) {
        pid_t child = fork(); check(child >= 0, "hard sandbox fork");
        if (!child) {
            check(unsetenv("NANOKVM_OPENSSL_CHACHA20") == 0, "clear opt-in");
            execl(argv[0], argv[0], "--hard-sandbox", NULL); _exit(3);
        }
        int status;
        check(waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0,
              "default policy survives fatal syscall filter");
        verify_sandbox(); verify_hits(1);
    }
    run_cases(7);
    puts("PASS 1680 raw ChaCha cases: offsets, counters, split, in-place, context copy");
    if (argc>1&&!strcmp(argv[1],"--native")) {
        pid_t pid=fork();check(pid>=0,"fork");
        if (!pid) { check(prctl(69,4L,0L,0L,0L)==0,"next exec off");execl(argv[0],argv[0],"--off-child",instrumented ? "--hits" : "",NULL);_exit(3); }
        int status;check(waitpid(pid,&status,0)==pid&&WIFEXITED(status)&&WEXITSTATUS(status)==0,"child");
    }
    return 0;
}
