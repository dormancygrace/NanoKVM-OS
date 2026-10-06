// SPDX-License-Identifier: GPL-2.0-only
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include "protocol.h"
_Static_assert(sizeof(struct lz_request)==48,"protocol size");
static int fd;
static void execute(struct lz_request *r) {
    if(ioctl(fd,LZ_RUN,r)){fprintf(stderr,"FAIL v=%u p=%u off=%u op=%u bytes=%llu\n",r->variant,r->pattern,r->offset,r->operation,(unsigned long long)r->bytes);perror("lz4 ioctl");exit(1);}
}
int main(int argc,char **argv) {
    unsigned long cases=0;
    if(argc!=3)return 2;
    fd=open(argv[1],O_RDWR);if(fd<0){perror("device");return 1;}
    if(!strcmp(argv[2],"qualify")){
        const unsigned sizes[]={0,1,4,7,8,9,15,16,17,31,32,63,64,127,128,255,256,511,512,1023,1024,2047,2048,4095,4096,4097,8191,8192,8193,16384,65535,65536};
        for(unsigned n=0;n<sizeof(sizes)/sizeof(sizes[0]);n++)
        for(unsigned v=0;v<LZ_VARIANTS;v++)for(unsigned p=0;p<8;p++)for(unsigned o=0;o<8;o++)for(unsigned op=0;op<2;op++){
            struct lz_request r={.bytes=sizes[n],.iterations=1,.variant=v,.pattern=p,.offset=o,.operation=op};
            execute(&r);cases++;
        }
        printf("PASS lz4_cases=%lu actual_kernel_reference compressed_byte_identity decompression tails offsets overlap_patterns guards\n",cases);
    }else if(!strcmp(argv[2],"bench")||!strcmp(argv[2],"page-bench")){
        int page_only=!strcmp(argv[2],"page-bench");
        const unsigned sizes[]={256,512,1024,2048,4096,8192,16384,65536},offsets[]={0,1,7};
        puts("bytes,variant,pattern,offset,operation,repetition,iterations,elapsed_ns,output");
        for(unsigned rep=0;rep<(page_only?21:7);rep++)for(unsigned n=0;n<sizeof(sizes)/sizeof(sizes[0]);n++)
        for(unsigned p=0;p<8;p++)for(unsigned o=0;o<(page_only?1:3);o++)for(unsigned op=0;op<2;op++)for(unsigned pos=0;pos<LZ_VARIANTS;pos++){
            if(page_only&&sizes[n]!=4096)continue;
            unsigned iters=page_only?128:65536/sizes[n];if(iters>128)iters=128;if(iters<4)iters=4;
            struct lz_request r={.bytes=sizes[n],.iterations=iters,.variant=rep&1?LZ_VARIANTS-1-pos:pos,.pattern=p,.offset=offsets[o],.operation=op};
            execute(&r);
            printf("%llu,%u,%u,%u,%u,%u,%llu,%llu,%u\n",(unsigned long long)r.bytes,r.variant,r.pattern,r.offset,r.operation,rep,(unsigned long long)r.iterations,(unsigned long long)r.elapsed_ns,r.output);
        }
    }else return 2;
    close(fd);return 0;
}
