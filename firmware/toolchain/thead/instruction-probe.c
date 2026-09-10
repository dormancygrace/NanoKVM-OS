/* SPDX-License-Identifier: GPL-2.0-only */
#define _POSIX_C_SOURCE 200809L
#include <signal.h>
#include <setjmp.h>
#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include <sys/resource.h>
static sigjmp_buf recovery;
static void trap(int signo){siglongjmp(recovery,signo);}
extern uint64_t probe_ba(void *);
extern uint64_t probe_bb(void *);
extern uint64_t probe_bs(void *);
extern uint64_t probe_condmov(void *);
extern uint64_t probe_mac(void *);
extern uint64_t probe_memidx(void *);
extern uint64_t probe_mempair(void *);
extern uint64_t probe_fmemidx(void *);
extern uint64_t probe_sync(void *);
extern uint64_t probe_vector(void *);
struct item {const char *name;uint64_t (*fn)(void *);uint64_t expected;};
static const struct item items[]={
{"xtheadba",probe_ba,23},
{"xtheadbb",probe_bb,86},
{"xtheadbs",probe_bs,1},
{"xtheadcondmov",probe_condmov,11},
{"xtheadmac",probe_mac,13},
{"xtheadmemidx",probe_memidx,22},
{"xtheadmempair",probe_mempair,33},
{"xtheadfmemidx",probe_fmemidx,22},
{"xtheadsync",probe_sync,1},
{"xtheadvector",probe_vector,16},
};
int main(void){
 const struct rlimit lim={0,0};setrlimit(RLIMIT_CORE,&lim);
 struct sigaction sa={0};sa.sa_handler=trap;sigemptyset(&sa.sa_mask);
 if(sigaction(SIGILL,&sa,0)||sigaction(SIGSEGV,&sa,0)||sigaction(SIGBUS,&sa,0))return 2;
 setvbuf(stdout,0,_IOLBF,0);
 for(volatile unsigned i=0;i<sizeof(items)/sizeof(items[0]);i++){
  uint64_t data[4]={11,22,33,44};int signal=sigsetjmp(recovery,1);
  if(signal){printf("UNAVAILABLE,%s,signal=%d\n",items[i].name,signal);continue;}
  uint64_t result=items[i].fn(data);int good=result==items[i].expected;
  if(!strcmp(items[i].name,"xtheadvector"))for(unsigned n=0;n<16;n++)good&=((unsigned char *)data)[n]==8;
  printf("%s,%s,result=%llu\n",good?"PASS":"MISMATCH",items[i].name,(unsigned long long)result);
  if(!good)return 1;
 }
 return 0;
}
