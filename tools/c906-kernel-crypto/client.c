// SPDX-License-Identifier: GPL-2.0-only
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/syscall.h>
#include <sys/time.h>
#include <unistd.h>
#include "protocol.h"
_Static_assert(sizeof(struct kc_request) == 72, "protocol layout");
static int fd;
static unsigned long original_fcsr;
static void die(const char *s) { perror(s); exit(1); }
static long raw_syscall3(long n, long x, long y, long z)
{
	register long a0 __asm__("a0") = x;
	register long a1 __asm__("a1") = y;
	register long a2 __asm__("a2") = z;
	register long a7 __asm__("a7") = n;
	__asm__ volatile("ecall" : "+r"(a0) : "r"(a1), "r"(a2), "r"(a7) : "memory");
	return a0;
}
#include "vector_state_helpers.h"
static void restore_controls(void)
{
	__asm__ volatile("csrw 0x003,%0\ncsrw 0x008,zero" :: "r"(original_fcsr) : "memory");
}
static void execute(struct kc_request *r)
{
	long ret;
	do ret = raw_syscall3(__NR_ioctl, fd, KC_RUN, (long)r); while (ret == -EINTR);
	if (ret < 0) {
		errno = (int)-ret;
		fprintf(stderr, "FAIL op=%u mode=%u n=%"PRIu64" src=%u dst=%u pattern=%u seed=%x flags=%u result=%"PRIu64"\n",
			r->operation,r->variant,(uint64_t)r->bytes,r->offset,r->dst_offset,r->pattern,
			r->seed,r->flags,(uint64_t)r->result);
		die("crypto ioctl");
	}
	if (r->completed != r->iterations || ((r->variant == 0 || r->variant == 1 || r->variant == 3) && r->vector_calls)) {
		fprintf(stderr,"FAIL completion or scalar fallback count\n"); exit(1);
	}
	if (r->variant == 2 && r->bytes &&
		((r->operation != KC_CRC32 && r->operation != KC_CRC32C) || r->bytes >= 512) &&
		!r->vector_calls) { fprintf(stderr,"FAIL missing vector entry\n"); exit(1); }
}
static void active_run(struct kc_request *, unsigned);
static void kernel_bench(void)
{
	const size_t sizes[]={64,256,512,768,1420,1500,4096,8192,16384,65536};
	puts("operation,variant,bytes,offset,dst_offset,pattern,seed,flags,repetition,iterations,elapsed_ns,vector_calls");
	for(unsigned rep=0;rep<7;rep++)for(unsigned op=0;op<KC_OPERATIONS;op++)
		for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++)for(unsigned offset=0;offset<2;offset++) {
			struct kc_request r={.operation=op,.variant=0,.bytes=sizes[i],.offset=offset?7:0,
				.dst_offset=offset?3:0,.pattern=0,.seed=0xffffffffU,
				.flags=(op==KC_AEAD_SG || op==KC_AEAD_SG_DECRYPT)?4:0};
			active_run(&r,32);
			printf("%u,0,%"PRIu64",%u,%u,%u,%u,%u,%u,%"PRIu64",%"PRIu64",%"PRIu64"\n",
				op,(uint64_t)r.bytes,r.offset,r.dst_offset,r.pattern,r.seed,r.flags,rep,
				(uint64_t)r.iterations,(uint64_t)r.elapsed_ns,(uint64_t)r.vector_calls);
		}
}
static void active_run(struct kc_request *r, unsigned calls)
{
	unsigned char data[512] __attribute__((aligned(16)));
	struct vector_state before,after;
	uint64_t elapsed = 0, entries = 0;
	for (unsigned i=0;i<sizeof(data);i++) data[i]=(unsigned char)(i*11+7);
	for (unsigned call=0;call<calls;call++) {
		r->iterations=1;
		seed_vector(data,r->pattern&3,r->offset&1); capture_vector(&before);
		execute(r); capture_vector(&after); restore_controls();
		if (before.fcsr!=after.fcsr || before.rounding!=after.rounding || before.saturation!=after.saturation) {
			fprintf(stderr,"FAIL active FCSR/VXRM/VXSAT op=%u mode=%u\n",r->operation,r->variant); exit(1);
		}
		elapsed+=r->elapsed_ns; entries+=r->vector_calls;
	}
	r->iterations=r->completed=calls; r->elapsed_ns=elapsed; r->vector_calls=entries;
}
static void validate(int smoke)
{
	const size_t sizes[]={0,1,2,3,7,8,9,15,16,31,32,63,64,65,127,128,129,255,256,257,
		511,512,513,768,1024,1420,1450,1500,2048,4095,4096,4097,8192,16384,65535,65536};
	const unsigned offsets[]={0,1,2,3,4,5,6,7}, seeds[]={0,0xffffffffU,0xfffffffeU};
	unsigned long cases=0;
	for(unsigned op=0;op<KC_OPERATIONS;op++)
		for(unsigned mode=0;mode<9;mode++) {
			if ((op!=KC_CRC32 && op!=KC_CRC32C && mode==1) || (op>=KC_CRC32 && mode>=4) ||
				((op==KC_AEAD_SG || op==KC_AEAD_SG_DECRYPT) && mode==3)) continue;
			for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++) {
				size_t n=sizes[i];
				if ((mode==3 && n>4096) || (smoke && n!=64 && n!=1450 && n!=4096)) continue;
				for(unsigned offset=0;offset<(smoke?2U:8U);offset++)
					for(unsigned pattern=0;pattern<(smoke?1U:4U);pattern++)
						for(unsigned seed=0;seed<(smoke?1U:3U);seed++) {
							struct kc_request r={.operation=op,.variant=mode,.bytes=n,.iterations=1,
								.offset=offsets[offset],.dst_offset=offsets[7-offset],.pattern=pattern,
								.seed=seeds[seed],.flags=(op==KC_AEAD_SG || op==KC_AEAD_SG_DECRYPT)?4U:(pattern&1)};
							execute(&r); cases++;
							if(op==KC_AEAD_SG || op==KC_AEAD_SG_DECRYPT){r.flags=2|(pattern&1?4:0);execute(&r);cases++;}
						}
			}
		}
	if(!smoke) {
		const size_t boundaries[]={1,63,64,65,127,128,129,255,256,257,1450,4096,8192};
		const unsigned operations[]={KC_CHACHA20,KC_COPY_CSUM};
		for(unsigned op=0;op<2;op++)for(unsigned i=0;i<sizeof(boundaries)/sizeof(boundaries[0]);i++)
			for(unsigned src=0;src<8;src++)for(unsigned dst=0;dst<8;dst++)for(unsigned mode=0;mode<3;mode+=2) {
				struct kc_request r={.operation=operations[op],.variant=mode,.bytes=boundaries[i],
					.iterations=1,.offset=src,.dst_offset=dst,.pattern=1,.seed=0xfffffffeU};
				execute(&r);cases++;
			}
	}
	for(unsigned op=0;op<KC_OPERATIONS;op++) {
		if(op!=KC_CHACHA20 && op!=KC_CRC32 && op!=KC_CRC32C)continue;
		for(unsigned mode=0;mode<3;mode++) {
			struct kc_request r={.operation=op,.variant=mode,.bytes=op==KC_CHACHA20?64:9,
				.iterations=1,.seed=0xffffffffU,.flags=8}; execute(&r);cases++;
		}
	}
	printf("PASS cases=%lu RFC8439_CRC_KAT counter_wrap tails alignments inplace SG_split bad_tag guards IRQ_fallback\n",cases);
}
static void context_test(int production)
{
	struct vector_state before,after;
	unsigned char data[512] __attribute__((aligned(16)));
	struct sigaction action={.sa_handler=clobber_vector_in_signal,.sa_flags=SA_RESTART};
	struct itimerval timer={.it_interval={.tv_usec=1000},.it_value={.tv_usec=1000}};
	unsigned calls=0,entries=0;
	for(unsigned i=0;i<512;i++)data[i]=(unsigned char)(i*13+1);
	sigemptyset(&action.sa_mask);
	if(sigaction(SIGALRM,&action,NULL)||setitimer(ITIMER_REAL,&timer,NULL))die("context timer");
	for(unsigned repeat=0;repeat<2;repeat++)for(unsigned rm=0;rm<4;rm++)for(unsigned sat=0;sat<2;sat++)
		for(unsigned op=0;op<KC_OPERATIONS;op++)for(unsigned mode=production?0:2;mode<(production?1U:5U);mode++) {
			if((mode==3 && (op==KC_AEAD_SG || op==KC_AEAD_SG_DECRYPT))||(mode==4 && op>=KC_CRC32))continue;
			struct kc_request r={.operation=op,.variant=mode,.bytes=production?16384:4096,.iterations=8,
				.offset=7,.dst_offset=3,.pattern=1,.seed=0xfffffffeU,
				.flags=(op==KC_AEAD_SG || op==KC_AEAD_SG_DECRYPT)?4:1};
			seed_vector(data,rm,sat);capture_vector(&before);
			sig_atomic_t old=signal_count;
			while(signal_count-old<2)__asm__ volatile("nop":::"memory");
			capture_vector(&after);
			if(memcmp(&before,&after,sizeof(before))){fprintf(stderr,"FAIL async all32 vector state\n");exit(1);}
			execute(&r);capture_vector(&after);restore_controls();
			if(before.fcsr!=after.fcsr||before.rounding!=after.rounding||before.saturation!=after.saturation){
				fprintf(stderr,"FAIL context controls op=%u mode=%u rm=%u sat=%u\n",op,mode,rm,sat);exit(1);
			}
			calls++;entries+=(unsigned)r.vector_calls;
		}
	memset(&timer,0,sizeof(timer));if(setitimer(ITIMER_REAL,&timer,NULL))die("stop timer");
	restore_controls();
	if(!signal_count){fprintf(stderr,"FAIL missing asynchronous signals\n");exit(1);}
	printf("PASS %scontext_calls=%u probe_vector_entries=%u signals=%d all32_VL_VTYPE_VSTART FCSR_VXRM_VXSAT\n",
		production?"production_":"",calls,entries,(int)signal_count);
}
static void chunk_test(void)
{
	const size_t sizes[]={65537,131071,131072,131073,196607,196608,196609,262143,262144};
	unsigned cases=0;
	for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++)
		for(unsigned src=0;src<8;src++)for(unsigned dst=0;dst<8;dst++)
			for(unsigned pattern=0;pattern<4;pattern++)for(unsigned mode=0;mode<3;mode+=2) {
				struct kc_request r={.operation=KC_COPY_CSUM,.variant=mode,.bytes=sizes[i],
					.offset=src,.dst_offset=dst,.pattern=pattern,.seed=0xffffffffU};
				active_run(&r,1);cases++;
			}
	printf("PASS chunk_cases=%u max_bytes=%u all64_alignments four_patterns active_vector_controls\n",cases,KC_MAX);
}
static void bench(int thresholds)
{
	const size_t sizes[]={64,256,512,768,1420,1500,4096,8192,16384,65536};
	puts("operation,variant,bytes,offset,dst_offset,pattern,seed,flags,repetition,iterations,elapsed_ns,vector_calls");
	unsigned repetitions=thresholds?7:5, modes=thresholds?9:5;
	for(unsigned rep=0;rep<repetitions;rep++)for(unsigned op=0;op<KC_OPERATIONS;op++)
		for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++)for(unsigned offset=0;offset<2;offset++)
			for(unsigned pos=0;pos<modes;pos++) {
				unsigned mode=rep&1?modes-1-pos:pos;
				if(mode==3 || (mode==1 && op!=KC_CRC32 && op!=KC_CRC32C) || (mode>=4 && op>=KC_CRC32) ||
					(thresholds && mode==2 && op<KC_CRC32))continue;
				struct kc_request r={.operation=op,.variant=mode,.bytes=sizes[i],.offset=offset?7:0,
					.dst_offset=offset?3:0,.pattern=0,.seed=0xffffffffU,
					.flags=(op==KC_AEAD_SG || op==KC_AEAD_SG_DECRYPT)?4:0};
				active_run(&r,thresholds?32:8);
				printf("%u,%u,%"PRIu64",%u,%u,%u,%u,%u,%u,%"PRIu64",%"PRIu64",%"PRIu64"\n",
					op,mode,(uint64_t)r.bytes,r.offset,r.dst_offset,r.pattern,r.seed,r.flags,rep,
					(uint64_t)r.iterations,(uint64_t)r.elapsed_ns,(uint64_t)r.vector_calls);
			}
}
int main(int argc,char **argv)
{
	if(argc!=3){fprintf(stderr,"usage: %s DEVICE smoke|validate|context|kernel-context|chunks|bench|thresholds|kernel\n",argv[0]);return 2;}
	__asm__ volatile("csrr %0,0x003":"=r"(original_fcsr));atexit(restore_controls);
	fd=open(argv[1],O_RDWR);if(fd<0)die("open");
	unsigned long width;
	__asm__ volatile(".option push\n.option arch,rv64gc_xtheadvector\nth.vsetvli %0,zero,e8,m8\n.option pop":"=r"(width));
	if(width!=128){fprintf(stderr,"Requires VLEN128 C906\n");return 2;}
	if(!strcmp(argv[2],"smoke"))validate(1);
	else if(!strcmp(argv[2],"validate"))validate(0);
	else if(!strcmp(argv[2],"context"))context_test(0);
	else if(!strcmp(argv[2],"kernel-context"))context_test(1);
	else if(!strcmp(argv[2],"chunks"))chunk_test();
	else if(!strcmp(argv[2],"bench"))bench(0);
	else if(!strcmp(argv[2],"thresholds"))bench(1);
	else if(!strcmp(argv[2],"kernel"))kernel_bench();
	else return 2;
	close(fd);return 0;
}
