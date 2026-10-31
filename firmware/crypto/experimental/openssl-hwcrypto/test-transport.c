/* SPDX-License-Identifier: Apache-2.0 */
#define _GNU_SOURCE
#include <stdarg.h>
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#define open fake_open
#define close fake_close
#define fstat fake_fstat
#define ioctl fake_ioctl
#include "hwcrypto.c"
#undef open
#undef close
#undef fstat
#undef ioctl

static int mode, attempts, opens, closes, submits, infos;
int fake_open(const char *path, int flags, ...)
{
    assert(!strcmp(path,"/dev/nanokvm-hwcrypto"));assert(flags&O_CLOEXEC);attempts++;
    if(mode==1||mode==10||mode==14){errno=mode==1?EACCES:mode==10?ENOENT:EMFILE;return -1;}
    opens++;return 37;
}
int fake_close(int fd){assert(fd==37);closes++;return 0;}
int fake_fstat(int fd,struct stat *st){assert(fd==37);memset(st,0,sizeof *st);st->st_mode=mode==2?S_IFREG:S_IFCHR;return 0;}
int fake_ioctl(int fd,unsigned long cmd,...)
{
    assert(fd==37);va_list ap;va_start(ap,cmd);void *p=va_arg(ap,void*);va_end(ap);
    if(cmd==HWCRYPTO_INFO){
        infos++;
        if(mode==15){errno=EINTR;return -1;}
        struct hwcrypto_info *i=p;memset(i,0,sizeof *i);
        i->version=mode==3?99:1;i->algorithms=mode==16?1u<<6:0xfe;
        i->max_input=4096;i->max_output=8192;i->poisoned=mode==4;
        return 0;
    }
    assert(cmd==HWCRYPTO_RUN);submits++;
    struct hwcrypto_request *r=p;
    int hash=r->algorithm==5||r->algorithm==6;
    if(!hash)memset((void*)(uintptr_t)r->output,0xa7,r->length);
    memset(r->state,0xb8,sizeof r->state);r->status=hash?2:1;
    if(mode==5||mode==6||mode==7||mode==11||mode==12||mode==13){
        errno=mode==5?EINTR:mode==6?EIO:mode==7?EFAULT:mode==11?ENOTTY:mode==12?EINVAL:EMSGSIZE;return -1;
    }
    if(mode==8)r->status=0;
    if(mode==9)r->output_length=8193;
    return 0;
}
static void reset_cache(void)
{
    atomic_store(&failed,0);atomic_store(&cached_algorithms,0);
}
int main(void)
{
    unsigned char buf[32],state[32];memset(state,0x11,32);
    const int unavailable[]={1,2,3,4,10,14,15};
    for(size_t i=0;i<sizeof unavailable/sizeof *unavailable;i++){
        mode=unavailable[i];reset_cache();assert(ossl_hwcrypto_begin(1)==-1);int n=attempts;
        if(mode==14||mode==15){mode=0;int fd=ossl_hwcrypto_begin(1);assert(fd==37);ossl_hwcrypto_end(fd);assert(attempts==n+1);}
        else {assert(ossl_hwcrypto_begin(1)==-1);assert(attempts==n);}
    }
    reset_cache();mode=0;int before=infos;
    for(unsigned alg=1;alg<=7;alg++){int fd=ossl_hwcrypto_begin(alg);assert(fd==37);ossl_hwcrypto_end(fd);}
    assert(infos==before+1); /* INFO/driver mutex only on first successful probe. */
    reset_cache();mode=16;assert(ossl_hwcrypto_begin(1)==-1);before=attempts;
    assert(ossl_hwcrypto_begin(1)==-1&&attempts==before);
    int fd=ossl_hwcrypto_begin(6);assert(fd==37);ossl_hwcrypto_end(fd);

    const int run_modes[]={0,5,6,7,8,9,11,12,13};
    for(int alg=1;alg<=7;alg++)for(size_t i=0;i<sizeof run_modes/sizeof *run_modes;i++){
        mode=run_modes[i];reset_cache();memset(buf,0x77,32);
        struct hwcrypto_request r={.version=1,.algorithm=alg,.length=32,
            .key_length=16,.input=(uintptr_t)buf,.output=(uintptr_t)buf,
            .output_length=alg==5||alg==6?0:32};
        memcpy(r.state,state,32);struct hwcrypto_request saved=r;
        fd=ossl_hwcrypto_begin((unsigned)alg);assert(fd==37);
        int ok=ossl_hwcrypto_run(fd,&r);assert(ok==(mode==0));
        if(ok){for(int k=0;k<32;k++){assert(r.state[k]==0xb8);assert(buf[k]==(alg==5||alg==6?0x77:0xa7));}}
        if(!ok){assert(!memcmp(&saved,&r,sizeof r));for(int k=0;k<32;k++)assert(buf[k]==0x77);}
        if(mode==5||mode==7){mode=0;assert(ossl_hwcrypto_run(fd,&r));}
        else if(mode!=0){int n=submits;mode=0;assert(!ossl_hwcrypto_run(fd,&r));assert(submits==n);}
        ossl_hwcrypto_end(fd);
    }
    assert(opens==closes);assert(submits==77);
    puts("PASS 77 submissions across 7 algorithms: positive/negative capability cache, transient retry, late-copy rollback, permanent protocol failure, owned fd lifecycle");
}
