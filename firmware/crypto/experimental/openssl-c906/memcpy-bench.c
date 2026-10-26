/* C906 compiler memcpy expansion probe; synthetic buffers, no application data. */
#define _POSIX_C_SOURCE 200809L
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <time.h>
#define JOIN_(a,b) a##b
#define JOIN(a,b) JOIN_(a,b)
#define SIZES(F) F(16) F(32) F(64) F(128) F(256) F(512) F(1024) F(4096) F(16384)
#ifdef COPY_IMPL
#define COPY(N) __attribute__((noinline,noclone)) void JOIN(PREFIX,N)(void *restrict d, const void *restrict s) { __builtin_memcpy(d,s,N); }
SIZES(COPY)
#else
#define DECLARE(N) void JOIN(scalar_,N)(void *,const void *); void JOIN(auto_,N)(void *,const void *);
SIZES(DECLARE)
struct operation { size_t size; void (*fn[2])(void *,const void *); };
#define ENTRY(N) {N,{JOIN(scalar_,N),JOIN(auto_,N)}},
static const struct operation operations[]={ SIZES(ENTRY) };
static volatile unsigned sink;
static uint64_t ns(clockid_t clock) { struct timespec t; if(clock_gettime(clock,&t))abort(); return (uint64_t)t.tv_sec*1000000000+t.tv_nsec; }
int main(void) {
 unsigned char *src,*dst;
 if(posix_memalign((void **)&src,64,16448)||posix_memalign((void **)&dst,64,16448))abort();
 for(size_t i=0;i<16448;i++)src[i]=(unsigned char)(i*13+7);
 unsigned tests=0;
 for(size_t i=0;i<sizeof operations/sizeof operations[0];i++)for(int strategy=0;strategy<2;strategy++)for(size_t off=0;off<32;off++) {
  size_t n=operations[i].size;memset(dst,0xa5,16448);operations[i].fn[strategy](dst+off,src+off);
  if(memcmp(dst+off,src+off,n))abort();
  for(size_t j=0;j<off;j++)if(dst[j]!=0xa5)abort();
  for(size_t j=off+n;j<16448;j++)if(dst[j]!=0xa5)abort();
  tests++;
 }
 printf("{\"kind\":\"correctness\",\"cases\":%u}\n",tests);fflush(stdout);
 for(unsigned run=1;run<=3;run++)for(size_t i=0;i<sizeof operations/sizeof operations[0];i++)for(size_t off=0;off<=1;off++)for(unsigned order=0;order<2;order++) {
  unsigned strategy=(run+order)%2;size_t n=operations[i].size;uint64_t count=0,wall=ns(CLOCK_MONOTONIC),start=ns(CLOCK_PROCESS_CPUTIME_ID),end;
  do {for(unsigned j=0;j<4096;j++)operations[i].fn[strategy](dst+off,src+off);count+=4096;end=ns(CLOCK_PROCESS_CPUTIME_ID);}while(end-start<120000000);
  wall=ns(CLOCK_MONOTONIC)-wall;sink^=dst[off+n-1];
  printf("{\"kind\":\"bench\",\"run\":%u,\"bytes\":%zu,\"offset\":%zu,\"strategy\":\"%s\",\"iterations\":%llu,\"cpu_ns\":%llu,\"wall_ns\":%llu,\"mib_per_cpu_second\":%.6f}\n",run,n,off,strategy?"auto":"scalar",(unsigned long long)count,(unsigned long long)(end-start),(unsigned long long)wall,(double)count*n*1e9/(1048576.0*(end-start)));fflush(stdout);
 }
 free(src);free(dst);return 0;
}
#endif
