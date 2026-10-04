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
#include "screen_protocol.h"
_Static_assert(sizeof(struct screen_request) == 64, "protocol size");
static int fd, active_bench, lazy_bench, integrated_bench;
static void bench_run(struct screen_request *r);
static void die(const char *s) { perror(s); exit(1); }
#ifdef __riscv
static long raw_syscall3(long n, long x, long y, long z)
{
	register long a0 __asm__("a0") = x;
	register long a1 __asm__("a1") = y;
	register long a2 __asm__("a2") = z;
	register long a7 __asm__("a7") = n;
	__asm__ volatile("ecall" : "+r"(a0) : "r"(a1), "r"(a2), "r"(a7) : "memory");
	return a0;
}
#endif
static void execute(struct screen_request *r)
{
	long result;
#ifdef __riscv
	do result = raw_syscall3(__NR_ioctl, fd, SCREEN_RUN, (long)r); while (result == -EINTR);
	if (result < 0) errno = (int)-result;
#else
	do result = ioctl(fd, SCREEN_RUN, r); while (result < 0 && errno == EINTR);
#endif
	if (result < 0) {
		fprintf(stderr, "FAIL op=%u variant=%u n=%" PRIu64 " offset=%u pattern=%u diff=%u result=%" PRIu64 "\n",
			r->operation, r->variant, (uint64_t)r->bytes, r->offset, r->pattern,
			r->difference, (uint64_t)r->result);
		die("screen ioctl");
	}
	uint64_t entries = r->variant == 2 && r->bytes ? r->iterations : 0;
	if (r->completed != r->iterations || (r->variant == 4 ? r->vector_calls > r->iterations : r->vector_calls != entries)) {
		fprintf(stderr, "FAIL operation count or IRQ-disabled fallback\n");
		exit(1);
	}
}
static void validate(void)
{
	const size_t sizes[] = {0,1,2,3,7,8,9,15,16,31,32,63,64,65,127,128,129,255,256,
		257,512,768,1024,1450,2048,4096,8192,16384,65535,65536,65537,262144,1048576};
	uint64_t count = 0;
	for (unsigned op = 0; op < 2; op++)
		for (size_t i = 0; i < sizeof(sizes)/sizeof(sizes[0]); i++)
			for (unsigned offset = 0; offset < 8; offset++)
				for (unsigned pattern = 0; pattern < 8; pattern++)
					for (unsigned mode = 0; mode < 4; mode++) {
						if ((op == SCREEN_CSUM && mode == 1) || (mode == 3 && sizes[i] > 4096)) continue;
						struct screen_request r = {.operation=op,.variant=mode,.bytes=sizes[i],
							.iterations=1,.offset=offset,.pattern=pattern,.difference=(uint32_t)sizes[i]};
						execute(&r);count++;
					}
	for (size_t i = 0; i < sizeof(sizes)/sizeof(sizes[0]); i++) {
		size_t n = sizes[i];
		const size_t boundaries[] = {0,1,7,8,15,16,31,32,63,64,65,127,128,129,255,256,n?n-1:0,n,n+1};
		for (size_t d = 0; d < sizeof(boundaries)/sizeof(boundaries[0]); d++) {
			int duplicate = 0;
			for (size_t prev = 0; prev < d; prev++) if (boundaries[prev] == boundaries[d]) duplicate=1;
			if (duplicate || boundaries[d] > n + 1) continue;
			for (unsigned offset = 0; offset < 8; offset++)
				for (unsigned bit = 0; bit < 8; bit++)
					for (unsigned mode = 0; mode < 4; mode++) {
						if (mode == 3 && n > 4096) continue;
						struct screen_request r = {.operation=SCREEN_PREFIX,.variant=mode,.bytes=n,
							.iterations=1,.offset=offset,.pattern=bit,.difference=(uint32_t)boundaries[d]};
						execute(&r);count++;
					}
		}
	}
	printf("PASS screen_cases=%" PRIu64 " checksum_exact LZ4_exact_bitstream_roundtrip_guards prefix_bit_positions hard_IRQ_scalar_fallback\n", count);
}
static void bench(void)
{
	const size_t sizes[] = {64,256,768,1450,4096,8192,16384,65536,262144,1048576};
	const unsigned offsets[] = {0,1,7};
	puts("operation,variant,bytes,offset,pattern,difference,repetition,iterations,elapsed_ns,vector_calls");
	for (unsigned rep = 0; rep < 5; rep++)
		for (unsigned op = 0; op < 4; op++)
			for (size_t i = 0; i < sizeof(sizes)/sizeof(sizes[0]); i++)
				for (unsigned offset = 0; offset < 3; offset++)
					for (unsigned pattern = 0; pattern < (op==SCREEN_PREFIX?4U:6U); pattern++)
						for (unsigned pos = 0; pos < 3; pos++) {
							unsigned mode = rep & 1 ? 2-pos : pos;
                            if(integrated_bench && (op!=SCREEN_CSUM || mode))continue;
                            if (active_bench && (sizes[i] > 65536 || op == SCREEN_PREFIX)) continue;
                            if (lazy_bench) {
                                if (op == SCREEN_LZ4 || op == SCREEN_LZ4_LIMITED) {
                                    const unsigned modes[] = {0,2,4};
                                    mode = modes[mode];
                                } else if (op != SCREEN_CSUM) continue;
                            }
							if ((op == SCREEN_CSUM && mode == 1) || (mode == 3 && sizes[i] > 4096)) continue;
							size_t n = sizes[i], iterations = (1024*1024)/n;
							if (iterations > 512) iterations=512;
							if (!iterations) iterations=1;
							struct screen_request r = {.operation=op,.variant=mode,.bytes=n,
								.iterations=iterations,.offset=offsets[offset],.pattern=pattern,
								.difference=(uint32_t)(op==SCREEN_PREFIX?(pattern==0?0:pattern==1?8:pattern==2?64:n):n)};
							bench_run(&r);
							printf("%u,%u,%" PRIu64 ",%u,%u,%u,%u,%" PRIu64 ",%" PRIu64 ",%" PRIu64 "\n",
								op,mode,(uint64_t)r.bytes,r.offset,r.pattern,r.difference,rep,
								(uint64_t)r.iterations,(uint64_t)r.elapsed_ns,(uint64_t)r.vector_calls);
						}
}

#ifdef __riscv
struct vector_state {
	uint64_t vl, type, start, rounding, saturation, fcsr;
	unsigned char registers[512] __attribute__((aligned(16)));
};
static volatile sig_atomic_t signal_count;
static void capture_vector(struct vector_state *s)
{
	void *pointer=s->registers;
	__asm__ volatile(
		".option push\n.option arch, rv64gc_xtheadvector\n"
		"csrr %[vl],0xc20\ncsrr %[type],0xc21\ncsrr %[start],0x008\n"
		"csrr %[rounding],0x00a\ncsrr %[saturation],0x009\ncsrr %[fcsr],0x003\n"
		"th.vsetvli t0,zero,e8,m8\nmv t1,%[pointer]\n"
		"th.vsb.v v0,(t1)\naddi t1,t1,128\n"
		"th.vsb.v v8,(t1)\naddi t1,t1,128\n"
		"th.vsb.v v16,(t1)\naddi t1,t1,128\nth.vsb.v v24,(t1)\n"
		"th.vsetvl t0,%[vl],%[type]\ncsrw 0x008,%[start]\n.option pop\n"
		: [vl]"=&r"(s->vl),[type]"=&r"(s->type),[start]"=&r"(s->start),
		  [rounding]"=&r"(s->rounding),[saturation]"=&r"(s->saturation),
		  [fcsr]"=&r"(s->fcsr)
		: [pointer]"r"(pointer)
		: "t0","t1","memory");
}
static void seed_vector(const unsigned char *data,unsigned rounding,unsigned saturation)
{
	__asm__ volatile(
		".option push\n.option arch, rv64gc_xtheadvector\n"
		"th.vsetvli t0,zero,e8,m8\nmv t1,%[pointer]\n"
		"th.vlb.v v0,(t1)\naddi t1,t1,128\n"
		"th.vlb.v v8,(t1)\naddi t1,t1,128\n"
		"th.vlb.v v16,(t1)\naddi t1,t1,128\nth.vlb.v v24,(t1)\n"
		"li t0,7\nth.vsetvli t0,t0,e16,m2\n"
		"csrw 0x00a,%[rounding]\ncsrw 0x009,%[saturation]\nli t0,1\n"
		"csrw 0x008,t0\n.option pop\n"
		: : [pointer]"r"(data),[rounding]"r"((unsigned long)rounding),
		    [saturation]"r"((unsigned long)saturation)
		: "t0","t1","memory","v0","v1","v2","v3","v4","v5","v6","v7",
		  "v8","v9","v10","v11","v12","v13","v14","v15",
		  "v16","v17","v18","v19","v20","v21","v22","v23",
		  "v24","v25","v26","v27","v28","v29","v30","v31");
}
static void clobber_vector_in_signal(int number)
{
	(void)number;
	__asm__ volatile(
		".option push\n.option arch, rv64gc_xtheadvector\n"
		"th.vsetvli t0,zero,e8,m8\nth.vmv.v.i v0,-1\n"
		"th.vmv.v.i v8,7\nth.vmv.v.i v16,11\nth.vmv.v.i v24,3\n"
		"li t0,3\ncsrw 0x00a,t0\nli t0,1\ncsrw 0x009,t0\n.option pop\n"
		: : : "t0","memory","v0","v1","v2","v3","v4","v5","v6","v7",
		  "v8","v9","v10","v11","v12","v13","v14","v15",
		  "v16","v17","v18","v19","v20","v21","v22","v23",
		  "v24","v25","v26","v27","v28","v29","v30","v31");
	signal_count++;
}
static void context_test(void)
{
	struct vector_state before,after;
	unsigned char pattern_data[512] __attribute__((aligned(16)));

	unsigned long vector_bytes, original_fcsr;
	struct sigaction action={.sa_handler=clobber_vector_in_signal,.sa_flags=SA_RESTART};
	struct itimerval timer={.it_interval={.tv_usec=1000},.it_value={.tv_usec=1000}};
	unsigned calls=0,vector_entries=0;


	__asm__ volatile(".option push\n.option arch,rv64gc_xtheadvector\n"
			 "th.vsetvli %0,zero,e8,m8\n.option pop":"=r"(vector_bytes));
	if(vector_bytes!=128){fprintf(stderr,"Requires VLEN128 C906\n");exit(1);}
	__asm__ volatile("csrr %0,0x003":"=r"(original_fcsr));
	for(size_t i=0;i<sizeof(pattern_data);i++)pattern_data[i]=(unsigned char)(i*13+1);
	seed_vector(pattern_data,0,0);capture_vector(&before);capture_vector(&after);
	if(memcmp(&before,&after,sizeof(before))){fprintf(stderr,"FAIL capture-only control vl=%"PRIu64"/%"PRIu64" type=%"PRIu64"/%"PRIu64"\n",before.vl,after.vl,before.type,after.type);exit(1);}
	seed_vector(pattern_data,0,0);capture_vector(&before);
	(void)raw_syscall3(__NR_getpid,0,0,0);capture_vector(&after);
	if(before.fcsr!=after.fcsr || before.rounding!=after.rounding || before.saturation!=after.saturation){fprintf(stderr,"FAIL raw-getpid FCSR/control preservation\n");exit(1);}
	__asm__ volatile("csrw 0x003,%0\ncsrw 0x008,zero"::"r"(original_fcsr):"memory");
	puts("PASS capture-only and raw-getpid FCSR controls; syscall vector registers are unspecified");
	sigemptyset(&action.sa_mask);
	if(sigaction(SIGALRM,&action,NULL)||setitimer(ITIMER_REAL,&timer,NULL))die("context timer");
	for(unsigned repeat=0;repeat<4;repeat++)
		for(unsigned rounding=0;rounding<4;rounding++)
			for(unsigned saturation=0;saturation<2;saturation++)
				for(unsigned operation=0;operation<4;operation++)
				for(unsigned mode=0;mode<5;mode++) {
                    if (mode == 4 && operation != SCREEN_LZ4 && operation != SCREEN_LZ4_LIMITED) continue;
					struct screen_request r={.bytes=operation==SCREEN_CSUM?16384:4096,.iterations=16,
                        .operation=operation,.variant=mode,.offset=7,.pattern=1,.difference=4096};
                    for(size_t i=0;i<sizeof(pattern_data);i++)pattern_data[i]=(unsigned char)(i*13+repeat);
					seed_vector(pattern_data,rounding,saturation);
					capture_vector(&before);
					sig_atomic_t old_signals=signal_count;
					while(signal_count-old_signals<2)
						__asm__ volatile("nop":::"memory");
					capture_vector(&after);
					if(memcmp(&before,&after,sizeof(before))) {
						fprintf(stderr,"FAIL asynchronous signal/scheduler vector state\n");exit(1);
					}
					execute(&r);
					capture_vector(&after);
					__asm__ volatile("csrw 0x003,%0\ncsrw 0x008,zero"::"r"(original_fcsr):"memory");
					if(before.fcsr!=after.fcsr || before.rounding!=after.rounding || before.saturation!=after.saturation) {
						fprintf(stderr,"FAIL RVV context op=%u rm=%u sat=%u repeat=%u"
							" vl=%"PRIu64"/%"PRIu64" type=%"PRIu64"/%"PRIu64
							" start=%"PRIu64"/%"PRIu64" fcsr=%"PRIu64"/%"PRIu64"\n",
							operation,rounding,saturation,repeat,
							before.vl,after.vl,before.type,after.type,before.start,after.start,
							before.fcsr,after.fcsr);exit(1);
					}
					calls++;vector_entries+=(unsigned)r.vector_calls;
				}
	memset(&timer,0,sizeof(timer));
	if(setitimer(ITIMER_REAL,&timer,NULL))die("stop timer");
	__asm__ volatile("csrw 0x003,%0\ncsrw 0x008,zero"::"r"(original_fcsr):"memory");

	if(!signal_count){fprintf(stderr,"FAIL no signals delivered\n");exit(1);}
	printf("PASS context_calls=%u vector_entries=%u signals=%d async_all32_registers_VL_VTYPE_VSTART syscall_VXRM_VXSAT_FCSR\n",
		calls,vector_entries,(int)signal_count);
}

#else
static void context_test(void) { exit(2); }
#endif


static void bench_run(struct screen_request *r)
{
    if (!active_bench) { execute(r); return; }
#ifdef __riscv
    unsigned char data[512] __attribute__((aligned(16)));
    struct vector_state before, after;
    unsigned long original;
    uint64_t elapsed=0, entries=0;
    __asm__ volatile("csrr %0,0x003":"=r"(original));
    for (unsigned i=0;i<512;i++) data[i]=(unsigned char)(i*11+7);
    for (unsigned call=0;call<8;call++) {
        r->iterations=1;
        seed_vector(data, r->pattern & 3, r->offset & 1);
        capture_vector(&before);
        execute(r);
        capture_vector(&after);
        __asm__ volatile("csrw 0x003,%0\ncsrw 0x008,zero"::"r"(original):"memory");
        if (before.fcsr != after.fcsr || before.rounding != after.rounding || before.saturation != after.saturation) {
            fprintf(stderr,"FAIL active benchmark CSR state\n"); exit(1);
        }
        elapsed+=r->elapsed_ns; entries+=r->vector_calls;
    }
    r->iterations=8; r->elapsed_ns=elapsed; r->vector_calls=entries; r->completed=8;
#else
    exit(2);
#endif
}

int main(int argc, char **argv)
{
    if (argc != 3) { fprintf(stderr, "usage: %s DEVICE validate|context|bench|smoke\n", argv[0]); return 2; }
    fd=open(argv[1],O_RDWR);
    if(fd<0) die("open screen probe");
    if(!strcmp(argv[2],"validate")) validate();
    else if(!strcmp(argv[2],"bench")) bench();
    else if(!strcmp(argv[2],"bench-active")) { active_bench=1; bench(); }
    else if(!strcmp(argv[2],"context")) context_test();
    else if(!strcmp(argv[2],"bench-kernel")) { integrated_bench=active_bench=1;bench(); }
    else if(!strcmp(argv[2],"bench-lazy")) { lazy_bench=active_bench=1; bench(); }
    else if(!strcmp(argv[2],"error-context")) {
        const size_t sizes[]={4096,65536};
        const unsigned modes[]={0,2,4};
        unsigned count=0, entered_mask=0;
        active_bench=1;
        for(unsigned i=0;i<2;i++)
            for(unsigned cap=0;cap<6;cap++)
                for(unsigned pattern=0;pattern<8;pattern++)
                    for(unsigned offset=0;offset<2;offset++)
                        for(unsigned mode=0;mode<3;mode++) {
                            const size_t capacities[]={8,16,32,300,1024,sizes[i]};
                            struct screen_request r={.operation=SCREEN_LZ4_LIMITED,.variant=modes[mode],
                                .bytes=sizes[i],.iterations=1,.offset=offset,.pattern=pattern,
                                .difference=(uint32_t)capacities[cap]};
                            bench_run(&r);count++;
                            if(r.variant==4 && !r.result && r.vector_calls)
                                entered_mask |= 1U << ((pattern&3)*2+(offset&1));
                        }
        if(entered_mask!=255) { fprintf(stderr,"FAIL missing post-entry error CSR aliases mask=%x\n",entered_mask);exit(1); }
        printf("PASS error_context_groups=%u kernel_calls=%u post_entry_error_all8_CSR_aliases FCSR_VXRM_VXSAT\n",count,count*8);
    }
    else if(!strcmp(argv[2],"validate-limited")) {
        const size_t sizes[]={0,1,7,63,64,127,128,129,255,256,257,512,1450,4096,16384,65536,1048576};
        unsigned count=0,failures=0,entered_failures=0;
        for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++) {
            size_t n=sizes[i];
            const size_t capacities[]={0,1,8,16,32,n/2,n,n+n/255+16};
            for(unsigned cap=0;cap<sizeof(capacities)/sizeof(capacities[0]);cap++) {
                if(capacities[cap]>n+n/255+16) continue;
                for(unsigned offset=0;offset<8;offset++)
                    for(unsigned pattern=0;pattern<8;pattern++)
                        for(unsigned mode=0;mode<3;mode++) {
                            const unsigned modes[]={0,2,4};
                            struct screen_request r={.operation=SCREEN_LZ4_LIMITED,.variant=modes[mode],
                                .bytes=n,.iterations=1,.offset=offset,.pattern=pattern,.difference=(uint32_t)capacities[cap]};
                            execute(&r); count++;
                            if(!r.result) { failures++; if(r.variant==4 && r.vector_calls) entered_failures++; }
                        }
            }
        }
        if(!entered_failures) { fprintf(stderr,"FAIL no compression error after lazy vector entry\n");exit(1); }
        printf("PASS limited_LZ4_cases=%u expected_failures=%u lazy_entered_error_paths=%u guards_and_context_cleanup\n",count,failures,entered_failures);
    }
    else if(!strcmp(argv[2],"validate-lazy")) {
        const size_t sizes[]={0,1,7,8,15,63,64,65,127,128,129,255,256,257,512,768,1024,1450,2048,4096,8192,16384,65535,65536,65537,262144,1048576};
        unsigned count=0;
        for(unsigned i=0;i<sizeof(sizes)/sizeof(sizes[0]);i++)
            for(unsigned offset=0;offset<8;offset++)
                for(unsigned pattern=0;pattern<8;pattern++) {
                    struct screen_request r={.operation=SCREEN_LZ4,.variant=4,.bytes=sizes[i],.iterations=1,.offset=offset,.pattern=pattern,.difference=(uint32_t)sizes[i]};
                    execute(&r); count++;
                }
        printf("PASS lazy_LZ4_cases=%u exact_bitstream_roundtrip_guards\n",count);
    }
    else if(!strcmp(argv[2],"smoke")) {
        for(unsigned op=0;op<3;op++)
            for(unsigned v=0;v<4;v++)
                for(unsigned n=0;n<20;n++)
                    for(unsigned offset=0;offset<8;offset++) {
                        struct screen_request r={.operation=op,.variant=v,.bytes=n,.iterations=1,
                            .offset=offset,.pattern=3,.difference=n};
                        execute(&r);
                    }
        puts("PASS smoke_cases=1920");
    } else { fprintf(stderr,"invalid mode\n"); return 2; }
    close(fd); return 0;
}
