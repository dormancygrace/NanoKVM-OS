// SPDX-License-Identifier: GPL-2.0-only
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/fs.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <sys/sysmacros.h>
#include <time.h>
#include <unistd.h>
#define PAGE 4096u
static uint64_t ns(clockid_t clock) {
    struct timespec t;
    if(clock_gettime(clock,&t)){perror("clock");exit(1);}
    return (uint64_t)t.tv_sec*1000000000u+t.tv_nsec;
}
static uint8_t pattern(unsigned i,unsigned p,uint32_t *rng) {
    *rng^=*rng<<13;*rng^=*rng>>17;*rng^=*rng<<5;
    switch(p){case 0:return 0;case 1:return (uint8_t)*rng;case 2:return (uint8_t)(i%16);
    case 3:return (uint8_t)(i%7);case 4:return i%512<480?0:(uint8_t)*rng;
    case 5:return " {\"value\":1234,\"enabled\":true} \n"[i%30];
    case 6:return (uint8_t)((i/64)%32);default:return i%256<128?(uint8_t)*rng:(uint8_t)(i%31);}
}
static void transfer(int fd,uint8_t *buf,size_t bytes,int read_op) {
    size_t done=0;
    while(done<bytes){
        ssize_t n=read_op?pread(fd,buf+done,bytes-done,done):pwrite(fd,buf+done,bytes-done,done);
        if(n<0&&errno==EINTR)continue;
        if(n<=0){perror("zram transfer");exit(1);}done+=n;
    }
}
static int guards(const uint8_t *buf,size_t bytes) {
    for(size_t i=0;i<PAGE;i++)if(buf[i]!=0xa7||buf[PAGE+bytes+i]!=0xa7)return 0;
    return 1;
}
int main(int argc,char **argv) {
    int periodic=argc==4&&!strcmp(argv[3],"periodic-kat");
    if(argc!=3&&!periodic)return 2;
    char device[256],*end;
    if(!realpath(argv[1],device)||strncmp(device,"/dev/zram",9))return 2;
    unsigned long number=strtoul(device+9,&end,10);
    if(*end||!number||number>255)return 2; /* Never production zram0. */
    unsigned long pages=strtoul(argv[2],&end,10);
    if(*end||pages<128||pages>2048)return 2;
    size_t bytes=pages*PAGE;uint64_t capacity;struct stat st;
    int fd=open(device,O_RDWR|O_DIRECT|O_CLOEXEC);
    if(fd<0||fstat(fd,&st)||!S_ISBLK(st.st_mode)||ioctl(fd,BLKGETSIZE64,&capacity)||capacity<bytes){perror("secondary zram");return 1;}
    char sysfs[128];snprintf(sysfs,sizeof(sysfs),"/sys/block/zram%lu/dev",number);
    FILE *info=fopen(sysfs,"r");unsigned ma,mi;
    if(!info||fscanf(info,"%u:%u",&ma,&mi)!=2||ma!=major(st.st_rdev)||mi!=minor(st.st_rdev))return 2;
    fclose(info);
    uint8_t *input,*output;
    if(posix_memalign((void **)&input,PAGE,bytes+2*PAGE)||posix_memalign((void **)&output,PAGE,bytes+2*PAGE))return 1;
    memset(input,0xa7,bytes+2*PAGE);memset(output,0xa7,bytes+2*PAGE);
    for(unsigned long p=0;p<pages;p++){
        uint32_t rng=0x71542391u^(uint32_t)p;
        for(unsigned i=0;i<PAGE;i++)input[PAGE+p*PAGE+i]=periodic?(uint8_t)((i%(1u<<((p%8)+1)))*37+11):pattern(i,p%8,&rng);
    }
    transfer(fd,input+PAGE,bytes,0);transfer(fd,output+PAGE,bytes,1);
    if(memcmp(input+PAGE,output+PAGE,bytes)||!guards(input,bytes)||!guards(output,bytes))return 1;
    puts("operation,pages,repetition,elapsed_ns,cpu_ns");
    for(unsigned rep=0;rep<(periodic?1u:21u);rep++)for(unsigned read_op=0;read_op<2;read_op++){
        if(read_op)memset(output+PAGE,0xa7,bytes);
        uint64_t cpu=ns(CLOCK_PROCESS_CPUTIME_ID),wall=ns(CLOCK_MONOTONIC_RAW);
        transfer(fd,(read_op?output:input)+PAGE,bytes,read_op);
        wall=ns(CLOCK_MONOTONIC_RAW)-wall;cpu=ns(CLOCK_PROCESS_CPUTIME_ID)-cpu;
        if(!guards(input,bytes)||!guards(output,bytes)||(read_op&&memcmp(input+PAGE,output+PAGE,bytes)))return 1;
        printf("%s,%lu,%u,%llu,%llu\n",read_op?"read":"write",pages,rep,(unsigned long long)wall,(unsigned long long)cpu);
    }
    free(input);free(output);close(fd);return 0;
}
