/* SPDX-License-Identifier: Apache-2.0 */
#define _GNU_SOURCE
#include <stdarg.h>
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#define open fake_open
#define close fake_close
#define ioctl fake_ioctl
#define getpid fake_getpid
#include "hwcrypto.c"
#undef open
#undef close
#undef ioctl
#undef getpid

static int mode, attempts, opens, closes, submits, next_fd = 37, pid = 100;
pid_t fake_getpid(void) {return pid;}
int fake_open(const char *path, int flags, ...)
{
    assert(!strcmp(path,"/dev/nanokvm-hwcrypto"));assert(flags&O_CLOEXEC);attempts++;
    if(mode==1||mode==10||mode==14){errno=mode==1?EACCES:mode==10?ENOENT:EMFILE;return -1;}
    opens++;return next_fd;
}
int fake_close(int fd){assert(fd==37||fd==38);closes++;return 0;}
int fake_ioctl(int fd,unsigned long cmd,...)
{
    assert(fd==next_fd);assert(cmd==HWCRYPTO_RUN);
    va_list ap;va_start(ap,cmd);struct hwcrypto_request *r=va_arg(ap,void*);va_end(ap);
    submits++;
    int hash=r->algorithm==5||r->algorithm==6;
    /* Like the driver: output first, then the request; nothing on error. */
    if(!hash)memset((void*)(uintptr_t)r->output,0xa7,r->length);
    if(mode==5||mode==6||mode==7||mode==11||mode==12||mode==15){
        errno=mode==5?EINTR:mode==6?EIO:mode==7?EFAULT:mode==11?EBADF:mode==12?EINVAL:ETIMEDOUT;return -1;
    }
    memset(r->state,0xb8,sizeof r->state);r->status=hash?2:1;r->output_length=hash?0:r->length;
    return 0;
}
static void reset(void){atomic_store(&failed,0);atomic_store(&shared,0);}
int main(void)
{
    unsigned char buf[32],other[32],state[32];memset(state,0x11,32);
    /* Absence/permission latch; EMFILE retries. */
    const int unavailable[]={1,10,14};
    for(size_t i=0;i<sizeof unavailable/sizeof *unavailable;i++){
        mode=unavailable[i];reset();assert(ossl_hwcrypto_begin(1)==-1);int n=attempts;
        if(mode==14){mode=0;int fd=ossl_hwcrypto_begin(1);assert(fd==37);ossl_hwcrypto_end(fd);assert(attempts==n+1);}
        else {assert(ossl_hwcrypto_begin(1)==-1);assert(attempts==n);}
    }
    /* One descriptor per process, kept across operations and algorithms. */
    reset();mode=0;int before=opens;
    for(unsigned alg=1;alg<=7;alg++){int fd=ossl_hwcrypto_begin(alg);assert(fd==37);ossl_hwcrypto_end(fd);}
    assert(opens==before+1&&closes==0);
    /* A forked child closes its inherited copy and opens its own. */
    pid=101;next_fd=38;assert(ossl_hwcrypto_begin(6)==38);assert(opens==before+2&&closes==1);
    assert(ossl_hwcrypto_begin(6)==38&&opens==before+2);

    const int run_modes[]={0,5,6,7,11,12,15};
    for(int alg=1;alg<=7;alg++)for(size_t i=0;i<sizeof run_modes/sizeof *run_modes;i++)for(int place=0;place<2;place++){
        mode=run_modes[i];reset();memset(buf,0x77,32);memset(other,0x55,32);
        unsigned char *out=place?buf:other;
        struct hwcrypto_request r={.version=1,.algorithm=alg,.length=32,
            .key_length=16,.input=(uintptr_t)buf,.output=(uintptr_t)out,
            .output_length=alg==5||alg==6?0:32};
        memcpy(r.state,state,32);struct hwcrypto_request saved=r;
        int fd=ossl_hwcrypto_begin((unsigned)alg);assert(fd==38);
        int ok=ossl_hwcrypto_run(fd,&r);assert(ok==(mode==0));
        assert(r.output==saved.output);
        for(int k=0;k<32;k++){
            if(ok)assert(r.state[k]==0xb8);
            if(alg==5||alg==6||!ok)assert(buf[k]==0x77); /* in-place input intact on failure */
            else assert(out[k]==0xa7);
        }
        if(!ok)assert(!memcmp(&saved,&r,sizeof r));
        if(mode==5||mode==7){mode=0;assert(ossl_hwcrypto_run(fd,&r));}
        else if(mode==11){assert(!atomic_load(&failed));mode=0;int n=opens;
            assert(ossl_hwcrypto_begin(1)==38&&opens==n+1);}          /* forgotten, not closed */
        else if(mode!=0){int n=submits;mode=0;assert(!ossl_hwcrypto_run(fd,&r));assert(submits==n);}
        ossl_hwcrypto_end(fd);
    }
    assert(submits==7*7*2+7*2*2);
    printf("PASS %d submissions across 7 algorithms, in place and out of place: process descriptor, "
           "fork, stale descriptor, transient retry, late-copy rollback, EIO/ETIMEDOUT latch\n",submits);
}
