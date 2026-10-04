// SPDX-License-Identifier: GPL-2.0-only
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/time.h>
#include <unistd.h>
#include <time.h>
#include "protocol.h"
#ifdef __riscv
#include <asm/unistd.h>
static inline long raw_syscall3(long number,long first,long second,long third)
{
	register long a0 __asm__("a0")=first;
	register long a1 __asm__("a1")=second;
	register long a2 __asm__("a2")=third;
	register long a7 __asm__("a7")=number;
	__asm__ volatile("ecall":"+r"(a0):"r"(a1),"r"(a2),"r"(a7):"memory");
	return a0;
}
#endif

static int fd;
static unsigned long checks;
static unsigned conservative_scalar_faults, conservative_vector_load_faults;
static uint64_t call_wall_ns,call_cpu_ns;
static uint64_t now_ns(clockid_t kind)
{
	struct timespec ts;
#ifdef __riscv
	long result=raw_syscall3(__NR_clock_gettime,kind,(long)&ts,0);
	if(result<0){errno=(int)-result;perror("clock_gettime");exit(1);}
#else
	if(clock_gettime(kind,&ts)){perror("clock_gettime");exit(1);}
#endif
	return (uint64_t)ts.tv_sec*1000000000ULL+(uint64_t)ts.tv_nsec;
}
static void die(const char *message)
{
	perror(message);
	exit(1);
}
static unsigned char pattern(size_t i) { return (unsigned char)(i * 37 + 11); }
static void execute(struct c906_request *r)
{
	int result;
	uint64_t cpu=now_ns(CLOCK_THREAD_CPUTIME_ID),wall=now_ns(CLOCK_MONOTONIC_RAW);
	do {
#ifdef __riscv
		long status=raw_syscall3(__NR_ioctl,fd,C906_RUN,(long)r);
		result=(int)status;
		if(status<0)errno=(int)-status;
#else
		result=ioctl(fd,C906_RUN,r);
#endif
	} while(result<0 && errno==EINTR);
	call_wall_ns=now_ns(CLOCK_MONOTONIC_RAW)-wall;
	call_cpu_ns=now_ns(CLOCK_THREAD_CPUTIME_ID)-cpu;
	if (result < 0) die("C906_RUN");
	if (!r->completed || r->completed > r->iterations ||
	    (r->vector && r->bytes && !r->vector_calls) ||
	    (!r->vector && r->vector_calls)) {
		fprintf(stderr, "Unexpected execution counts\n"); exit(1);
	}
}
static void snapshot(unsigned char *dst, size_t bytes)
{
	size_t got = 0;
	while (got < bytes) {
		ssize_t n = read(fd, dst + got, bytes - got);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) die("snapshot");
		got += (size_t)n;
	}
}
static void assert_byte(unsigned char got, unsigned char expected,
			const char *where, size_t index)
{
	if (got != expected) {
		fprintf(stderr, "FAIL %s index=%zu got=%02x expected=%02x\n",
			where, index, got, expected); exit(1);
	}
}
static void one_valid_case(unsigned operation, unsigned vector, size_t n,
			   unsigned source_offset, unsigned destination_offset,
			   uint64_t iterations, unsigned repetition, int report)
{
	size_t total = C906_MAX_BYTES + 4 * C906_PAD;
	unsigned char *user = malloc(total), *result = malloc(total);
	size_t begin = C906_PAD + destination_offset;
	size_t count = begin + n + C906_PAD, i;
	struct c906_request r = {
		.bytes=n, .iterations=iterations, .operation=operation,
		.vector=vector, .source_offset=source_offset,
		.destination_offset=destination_offset, .fill=0x5c,
	};
	if (!user || !result) die("malloc");
	memset(user, 0xa5, total);
	if (operation == C906_FROM_USER) {
		for (i=0; i<n; i++) user[C906_PAD + source_offset + i] = pattern(i + 113);
		r.user_pointer=(uintptr_t)(user + C906_PAD + source_offset);
	} else {
		r.user_pointer=(uintptr_t)(user + begin);
	}
	execute(&r);
	if (r.not_copied || r.completed != iterations) {
		fprintf(stderr,"FAIL complete copy op=%u mode=%u n=%zu remain=%"PRIu64"\n",
			operation,vector,n,(uint64_t)r.not_copied);exit(1);
	}
	if (operation == C906_TO_USER) {
		for (i=0; i<count; i++) {
			unsigned char want = (i>=begin && i<begin+n) ?
				pattern(C906_PAD + source_offset + i-begin) : 0xa5;
			assert_byte(user[i],want,"to-user data/guard",i);
		}
	} else {
		snapshot(result,count);
		for (i=0; i<count; i++) {
			unsigned char want=0xa5;
			if (i>=begin && i<begin+n) {
				if (operation==C906_FROM_USER) want=pattern(i-begin+113);
				else if (operation==C906_COPY) want=pattern(C906_PAD+source_offset+i-begin);
				else want=0x5c;
			}
			assert_byte(result[i],want,"kernel data/guard",i);
		}
	}
	if (report)
		printf("%u,%u,%zu,%u,%u,%u,%"PRIu64",%"PRIu64",%"PRIu64",%"PRIu64",%"PRIu64"\n",
			operation,vector,n,source_offset,destination_offset,repetition,
			(uint64_t)r.completed,(uint64_t)r.elapsed_ns,call_wall_ns,call_cpu_ns,(uint64_t)r.vector_calls);
	free(result); free(user); checks++;
}
static void fault_cases(void)
{
	size_t page=(size_t)sysconf(_SC_PAGESIZE), i;
	unsigned char *mapping=mmap(NULL,3*page,PROT_NONE,MAP_PRIVATE|MAP_ANONYMOUS,-1,0);
	unsigned char *result=malloc(C906_MAX_BYTES + 4*C906_PAD);
	const size_t prefixes[]={1,7,15,16,31,63,64,127,128,129,255,256,513};
	if (mapping==MAP_FAILED || !result) die("fault mmap/malloc");
	if (mprotect(mapping+page,page,PROT_READ|PROT_WRITE)) die("mprotect");
	for (unsigned tail_index=0; tail_index<2; tail_index++) {
		size_t failing_tail=tail_index?16385:257;
	for (unsigned vector=0; vector<2; vector++) {
		for (unsigned operation=C906_FROM_USER; operation<=C906_TO_USER; operation++) {
			for (size_t j=0;j<sizeof(prefixes)/sizeof(prefixes[0]);j++) {
				size_t accessible=prefixes[j], n=accessible+failing_tail;
				unsigned char *user=mapping+2*page-accessible;
				memset(mapping+page,0xa5,page);
				if (operation==C906_FROM_USER)
					for (i=0;i<accessible;i++)user[i]=pattern(i+113);
				struct c906_request r={.user_pointer=(uintptr_t)user,
					.bytes=n,.iterations=1,.operation=operation,.vector=vector};
				execute(&r);
				if (r.not_copied<failing_tail || r.not_copied>n ||
				    (vector && operation==C906_TO_USER && r.not_copied!=failing_tail)) {
					fprintf(stderr,"FAIL partial op=%u vector=%u accessible=%zu remain=%"PRIu64"\n",
						operation,vector,accessible,(uint64_t)r.not_copied);exit(1);
				}
				size_t copied=n-r.not_copied;
				if(r.not_copied>failing_tail) {
					if(vector)conservative_vector_load_faults++;
					else conservative_scalar_faults++;
				}
				if (operation==C906_FROM_USER) {
					size_t count=2*C906_PAD+n;
					snapshot(result,count);
					for(i=0;i<count;i++) {
						unsigned char want=0xa5;
						if(i>=C906_PAD && i<C906_PAD+copied)want=pattern(i-C906_PAD+113);
						else if(i>=C906_PAD+copied && i<C906_PAD+n)want=0;
						assert_byte(result[i],want,"partial source/zero/guard",i);
					}
				} else {
					for(i=0;i<page;i++) {
						unsigned char want=i>=page-accessible ?
							pattern(C906_PAD+i-(page-accessible)) : 0xa5;
						assert_byte(mapping[page+i],want,"partial destination/guard",i);
					}
				}
				checks++;
			}
			/* Completely inaccessible source/destination. */
			struct c906_request r={.user_pointer=(uintptr_t)(mapping+1),
				.bytes=failing_tail,.iterations=1,.operation=operation,.vector=vector};
			execute(&r);
			if(r.not_copied!=failing_tail){fprintf(stderr,"FAIL inaccessible pointer\n");exit(1);}
			if(operation==C906_FROM_USER) {
				snapshot(result,2*C906_PAD+failing_tail);
				for(i=0;i<2*C906_PAD+failing_tail;i++)
					assert_byte(result[i],i>=C906_PAD&&i<C906_PAD+failing_tail?0:0xa5,
						    "inaccessible source",i);
			}
			checks++;
		}
	}
	}
	free(result); munmap(mapping,3*page);
}
static void validate(void)
{
	const size_t sizes[]={0,1,2,7,15,16,17,63,64,127,128,129,255,256,257,511,512,513,
		767,768,769,1023,1024,1025,2047,2048,2049,4095,4096,4097,16384,65536,1048576};
	const unsigned offsets[][2]={{0,0},{1,0},{0,1},{7,15},{15,7},{31,63}};
	for(unsigned op=0;op<4;op++)
		for(unsigned vector=0;vector<2;vector++)
			for(size_t j=0;j<sizeof(sizes)/sizeof(sizes[0]);j++)
				for(size_t k=0;k<sizeof(offsets)/sizeof(offsets[0]);k++)
					one_valid_case(op,vector,sizes[j],offsets[k][0],offsets[k][1],1,0,0);
	fault_cases();
	printf("PASS byte_guard_fault_cases=%lu conservative_scalar_faults=%u conservative_vector_load_faults=%u vector_store_faults_exact\n",checks,conservative_scalar_faults,conservative_vector_load_faults);
}
static void benchmark(void)
{
	const size_t sizes[]={16,64,128,256,512,768,1024,2048,4096,16384,65536,1048576};
	const unsigned offsets[][2]={{0,0},{1,7}};
	puts("operation,vector,bytes,src_offset,dst_offset,repetition,iterations,elapsed_ns,call_wall_ns,call_cpu_ns,vector_calls");
	for(unsigned repetition=0;repetition<5;repetition++)
		for(unsigned op=0;op<4;op++)
			for(size_t j=0;j<sizeof(sizes)/sizeof(sizes[0]);j++)
				for(size_t k=0;k<sizeof(offsets)/sizeof(offsets[0]);k++)
					for(unsigned order=0;order<2;order++) {
						unsigned mode=(order+repetition)&1;
						uint64_t iterations=8*1024*1024/sizes[j];
						if(iterations<64)iterations=64;
						if(iterations>20000)iterations=20000;
						one_valid_case(op,mode,sizes[j],offsets[k][0],offsets[k][1],
							       iterations,repetition,1);
					}
}
/* Context-state qualification is appended below. */
static int integrated_bench;
static void context_test(void);
static void benchmark_active(int sweep);
int main(int argc,char **argv)
{
	if(argc!=3 || (strcmp(argv[2],"validate") && strcmp(argv[2],"bench") &&
		       strcmp(argv[2],"context") && strcmp(argv[2],"bench-active") && strcmp(argv[2],"bench-align") && strcmp(argv[2],"bench-kernel"))) {
		fprintf(stderr,"Usage: %s /dev/c906-vector-probe validate|bench|context|bench-active|bench-align|bench-kernel\n",argv[0]);
		return 2;
	}
	fd=open(argv[1],O_RDWR);
	if(fd<0)die("open probe");
	if(!strcmp(argv[2],"validate"))validate();
	else if(!strcmp(argv[2],"bench"))benchmark();
	else if(!strcmp(argv[2],"bench-active"))benchmark_active(0);
	else if(!strcmp(argv[2],"bench-align"))benchmark_active(1);
	else if(!strcmp(argv[2],"bench-kernel")){integrated_bench=1;benchmark_active(1);}
	else context_test();
	close(fd);return 0;
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
	unsigned char *user=malloc(16384+4*C906_PAD);
	unsigned long vector_bytes, original_fcsr;
	struct sigaction action={.sa_handler=clobber_vector_in_signal,.sa_flags=SA_RESTART};
	struct itimerval timer={.it_interval={.tv_usec=1000},.it_value={.tv_usec=1000}};
	unsigned calls=0,vector_entries=0;
	if(!user)die("context malloc");
	memset(user,0x57,16384+4*C906_PAD);
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
	for(unsigned repeat=0;repeat<8;repeat++)
		for(unsigned rounding=0;rounding<4;rounding++)
			for(unsigned saturation=0;saturation<2;saturation++)
				for(unsigned operation=0;operation<4;operation++)
				for(unsigned mode=0;mode<2;mode++) {
					struct c906_request r={.user_pointer=(uintptr_t)(user+3),
						.bytes=16384,.iterations=64,.operation=operation,.vector=mode,.fill=0x5c,
						.source_offset=1,.destination_offset=7};
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
					if(r.not_copied || r.vector_calls!=(mode?r.iterations:0)) {
						fprintf(stderr,"FAIL context operation progress\n");exit(1);
					}
					calls++;vector_entries+=(unsigned)r.vector_calls;
				}
	memset(&timer,0,sizeof(timer));
	if(setitimer(ITIMER_REAL,&timer,NULL))die("stop timer");
	__asm__ volatile("csrw 0x003,%0\ncsrw 0x008,zero"::"r"(original_fcsr):"memory");
	free(user);
	if(!signal_count){fprintf(stderr,"FAIL no signals delivered\n");exit(1);}
	printf("PASS context_calls=%u vector_entries=%u signals=%d async_all32_registers_VL_VTYPE_VSTART syscall_VXRM_VXSAT_FCSR\n",
		calls,vector_entries,(int)signal_count);
}

static void benchmark_active(int sweep)
{
	const size_t full_sizes[]={16,64,128,256,512,768,1024,2048,4096,16384,65536,1048576};
	const size_t sweep_sizes[]={4096,8192,16384,65536};
	const size_t *sizes=sweep?sweep_sizes:full_sizes;
	size_t size_count=sweep?sizeof(sweep_sizes)/sizeof(sweep_sizes[0]):sizeof(full_sizes)/sizeof(full_sizes[0]);
	unsigned offsets[64][2]={{0,0},{1,7}};
	size_t offset_count=sweep?64:2;
	if(sweep)for(unsigned i=0;i<64;i++){offsets[i][0]=i/8;offsets[i][1]=i%8;}
	unsigned char seed[512] __attribute__((aligned(16)));
	unsigned char *user=malloc(C906_MAX_BYTES+4*C906_PAD);
	unsigned char *result=malloc(C906_MAX_BYTES+4*C906_PAD);
	unsigned long original_fcsr,vector_bytes;
	struct vector_state before,after;
	if(!user||!result)die("active malloc");
	__asm__ volatile(".option push\n.option arch,rv64gc_xtheadvector\n"
			 "th.vsetvli %0,zero,e8,m8\n.option pop":"=r"(vector_bytes));
	if(vector_bytes!=128){fprintf(stderr,"Requires VLEN128 C906\n");exit(1);}
	__asm__ volatile("csrr %0,0x003":"=r"(original_fcsr));
	for(size_t i=0;i<sizeof(seed);i++)seed[i]=pattern(i+37);
	puts("operation,vector,bytes,src_offset,dst_offset,repetition,iterations,elapsed_ns,call_wall_ns,call_cpu_ns,vector_calls");
	for(unsigned repetition=0;repetition<5;repetition++)
		for(unsigned operation=0;operation<(sweep?2U:4U);operation++)
			for(size_t j=0;j<size_count;j++)
				for(size_t k=0;k<offset_count;k++)
					for(unsigned order=0;order<2;order++) {
						unsigned vector=(order+repetition)&1;
                        if(integrated_bench && vector)continue;
                        if(integrated_bench && sizes[j]!=16384 && k>1)continue;
						unsigned source_offset=offsets[k][0],destination_offset=offsets[k][1];
						size_t n=sizes[j],begin=C906_PAD+destination_offset;
						size_t total=begin+n+C906_PAD;
						uint64_t ns=0,wall_ns=0,cpu_ns=0,entries=0;
						struct c906_request r={.bytes=n,.iterations=1,.operation=operation,
							.vector=vector,.source_offset=source_offset,
							.destination_offset=destination_offset,.fill=0x5c};
						for(unsigned sample=0;sample<16;sample++) {
							memset(user,0xa5,total);
							if(operation==C906_FROM_USER) {
								for(size_t i=0;i<n;i++)user[C906_PAD+source_offset+i]=pattern(i+113);
								r.user_pointer=(uintptr_t)(user+C906_PAD+source_offset);
							} else r.user_pointer=(uintptr_t)(user+begin);
							seed_vector(seed,0,0);
							capture_vector(&before);
							execute(&r);
							capture_vector(&after);
							__asm__ volatile("csrw 0x003,%0\ncsrw 0x008,zero"::"r"(original_fcsr):"memory");
							if(r.not_copied||r.completed!=1||before.fcsr!=after.fcsr||before.rounding!=after.rounding||before.saturation!=after.saturation) {
								fprintf(stderr,"FAIL active-entry copy/context\n");exit(1);
							}
							ns+=r.elapsed_ns;wall_ns+=call_wall_ns;cpu_ns+=call_cpu_ns;entries+=r.vector_calls;
						}
						if(operation!=C906_TO_USER)snapshot(result,total);
						for(size_t i=0;i<total;i++) {
							unsigned char want=0xa5;
							if(i>=begin&&i<begin+n) {
								if(operation==C906_FROM_USER)want=pattern(i-begin+113);
								else if(operation==C906_FILL)want=0x5c;
								else want=pattern(C906_PAD+source_offset+i-begin);
							}
							assert_byte(operation==C906_TO_USER?user[i]:result[i],want,"active-entry bytes/guard",i);
						}
						printf("%u,%u,%zu,%u,%u,%u,16,%"PRIu64",%"PRIu64",%"PRIu64",%"PRIu64"\n",
							operation,vector,n,source_offset,destination_offset,repetition,ns,wall_ns,cpu_ns,entries);
					}
	free(result);free(user);
}
#else
static void context_test(void)
{
	fprintf(stderr,"RVV context qualification requires RISC-V target\n");exit(2);
}
static void benchmark_active(int sweep)
{
	(void)sweep;
	fprintf(stderr,"Active-vector timing requires RISC-V target\n");exit(2);
}
#endif
