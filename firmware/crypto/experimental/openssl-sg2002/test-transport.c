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
#define ossl_safe_getenv getenv
#define NKOS_SG2002_TEST
#include "sg2002.c"
#undef open
#undef close
#undef fstat
#undef ioctl

static int mode, opens, closes, submits;
int fake_open(const char *path, int flags, ...)
{
    assert(!strcmp(path,"/dev/sg2002-aes-probe"));assert(flags&O_CLOEXEC);
    if (mode==1) {errno=EACCES;return -1;}
    opens++;return 37;
}
int fake_close(int fd) {assert(fd==37);closes++;return 0;}
int fake_fstat(int fd, struct stat *st) {assert(fd==37);memset(st,0,sizeof *st);st->st_mode=mode==2?S_IFREG:S_IFCHR;return 0;}
int fake_ioctl(int fd,unsigned long cmd,...)
{
    assert(fd==37);va_list ap;va_start(ap,cmd);void *p=va_arg(ap,void*);va_end(ap);
    if (cmd==SG2002_CRYPTO_INFO) {
        struct sg2002_crypto_info *i=p;memset(i,0,sizeof *i);
        i->version=mode==3?99:1;i->algorithms=0xfe;i->max_input=4096;i->max_output=8192;i->poisoned=mode==4;
        return 0;
    }
    assert(cmd==SG2002_CRYPTO_RUN);submits++;
    struct sg2002_crypto_request *r=p;
    int hash=r->algorithm==5||r->algorithm==6;
    if(!hash)memset((void*)(uintptr_t)r->output,0xa7,r->length);
    memset(r->state,0xb8,sizeof r->state);
    r->status=hash?2:1;
    if(mode==5||mode==6||mode==7) {errno=mode==5?EINTR:mode==6?EIO:EFAULT;return -1;}
    if(mode==8)r->status=0;
    if(mode==9)r->output_length=8193;
    return 0;
}
int main(void)
{
    unsigned char buf[32],state[32];memset(state,0x11,32);
    for(mode=1;mode<=4;mode++) {atomic_store(&failed,0);assert(ossl_sg2002_begin(1)==-1);}
    for(int alg=1;alg<=7;alg++)for(mode=0;mode<=9;mode++) {
        if(mode>0&&mode<5)continue;
        atomic_store(&failed,0);memset(buf,0x77,32);
        struct sg2002_crypto_request r={.version=1,.algorithm=alg,.length=32,
          .key_length=16,.input=(uintptr_t)buf,.output=(uintptr_t)buf,
          .output_length=alg==5||alg==6?0:32};
        memcpy(r.state,state,32);struct sg2002_crypto_request before=r;
        int fd=ossl_sg2002_begin((unsigned)alg);assert(fd==37);
        int ok=ossl_sg2002_run(fd,&r);assert(ok==(mode==0));
        if(ok) {for(int k=0;k<32;k++) {assert(r.state[k]==0xb8);assert(buf[k]==(alg==5||alg==6?0x77:0xa7));}}
        if(!ok) {assert(!memcmp(&before,&r,sizeof r));for(int k=0;k<32;k++)assert(buf[k]==0x77);}
        if(mode==5||mode==7) {int saved=mode;mode=0;assert(ossl_sg2002_run(fd,&r));mode=saved;}
        if(mode==6||mode==8||mode==9) {int n=submits,saved=mode;mode=0;assert(!ossl_sg2002_run(fd,&r));assert(submits==n);mode=saved;}
        ossl_sg2002_end(fd);
    }
    assert(opens==closes);assert(submits==56);
    puts("PASS all 7 DMA algorithms: unavailable/ABI/poison, interrupted retry, late-copy rollback, permanent failure, fd lifecycle");
}
