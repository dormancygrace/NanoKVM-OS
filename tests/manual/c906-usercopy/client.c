// SPDX-License-Identifier: GPL-2.0-only
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <errno.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <time.h>
#include "usercopy_test.h"
static int fd;
static int negative_control;
/* Accept a residual larger than the exact one (fewer bytes reported copied
 * than were accessible), as the kernel's usercopy contract allows: the
 * upstream fast-unaligned path reports from the start of its 8-word block. */
static int conservative;
static unsigned long tests,faults,under[3],max_under[3];
static unsigned char *user,*output,*guard;
static void die(const char *s) { perror(s);exit(1); }
static void req(struct sm_req *q) { if(ioctl(fd,SM_IOCTL,q))die("ioctl");if(q->status){fprintf(stderr,"SUM leaked\n");exit(1);} }
static void fail(const char *why,unsigned v,unsigned dir,unsigned n,unsigned a,unsigned b,unsigned i)
{
 fprintf(stderr,"FAIL %s variant=%u dir=%u n=%u useroffset/prefix=%u kernoffset=%u index=%u test=%lu\n",why,v,dir,n,a,b,i,tests);exit(1);
}
static void bounded(void)
{
 const unsigned lengths[]={191,192,193,255,256,257,511,512,513,1023,4095,4096};
 for(unsigned v=0;v<3;v++)for(unsigned dir=0;dir<2;dir++)
 for(unsigned l=0;l<161+sizeof(lengths)/sizeof(lengths[0]);l++)
 for(unsigned edge=0;edge<2;edge++)for(unsigned b=0;b<8;b++) {
  unsigned n=l<161?l:lengths[l-161];
  unsigned char *p=edge?guard+8192-n:guard+4096;
  if(mprotect(guard,12288,PROT_READ|PROT_WRITE))die("reset guards");
  memset(guard,0xa5,12288);
  if(!dir)for(unsigned i=0;i<n;i++)p[i]=sm_pattern(i+17);
  if(mprotect(guard,4096,PROT_NONE) || mprotect(guard+8192,4096,PROT_NONE))die("set guards");
  struct sm_req q={.variant=v,.direction=dir,.n=n,.offset=b,.user=(uintptr_t)p,.output=(uintptr_t)output};
  req(&q);
  if(q.residual)fail("bounded residual",v,dir,n,edge,b,q.residual);
  for(unsigned i=0;i<n;i++)
   if((dir?p[i]:output[8+b+i])!=(dir?sm_pattern(8+b+i):sm_pattern(i+17)))
    fail("bounded payload",v,dir,n,edge,b,i);
  if(!dir) {
   for(unsigned i=0;i<8+b;i++)if(output[i]!=0xa5)fail("bounded kernel prefix",v,dir,n,edge,b,i);
   for(unsigned i=8+b+n;i<n+32;i++)if(output[i]!=0xa5)fail("bounded kernel suffix",v,dir,n,edge,b,i);
  }
  if(mprotect(guard,12288,PROT_READ|PROT_WRITE))die("read guards");
  for(unsigned i=0;i<12288;i++)
   if((guard+i<p || guard+i>=p+n) && guard[i]!=0xa5)fail("bounded user guard",v,dir,n,edge,b,i);
  tests++;
 }
}
static void correct(void)
{
 const unsigned lengths[]={191,192,193,255,256,257,511,512,513,1023,1024,1025,1400,4095,4096,4097,16383,16384,16385,32768};
 for(unsigned v=0;v<3;v++)for(unsigned dir=0;dir<2;dir++)
 for(unsigned l=0;l<161+sizeof(lengths)/sizeof(lengths[0]);l++) {
  unsigned n=l<161?l:lengths[l-161];
  for(unsigned a=0;a<8;a++)for(unsigned b=0;b<8;b++) {
   unsigned char *p=user+16+a;
   memset(user,0xa5,n+64);
   if(!dir)for(unsigned i=0;i<n;i++)p[i]=sm_pattern(i+17);
   struct sm_req q={.variant=v,.direction=dir,.n=n,.offset=b,.user=(uintptr_t)p,.output=(uintptr_t)output};
   req(&q);
   if(q.residual)fail("unexpected residual",v,dir,n,a,b,q.residual);
   if (negative_control && n == 1) output[8+b] ^= 1;
   for(unsigned i=0;i<n;i++)
    if((dir?p[i]:output[8+b+i])!=(dir?sm_pattern(8+b+i):sm_pattern(i+17)))
     fail("payload",v,dir,n,a,b,i);
   for(unsigned i=0;i<16+a;i++)if(user[i]!=0xa5)fail("user prefix guard",v,dir,n,a,b,i);
   for(unsigned i=16+a+n;i<n+64;i++)if(user[i]!=0xa5)fail("user suffix guard",v,dir,n,a,b,i);
   if(!dir) {
    for(unsigned i=0;i<8+b;i++)if(output[i]!=0xa5)fail("kernel prefix guard",v,dir,n,a,b,i);
    for(unsigned i=8+b+n;i<n+32;i++)if(output[i]!=0xa5)fail("kernel suffix guard",v,dir,n,a,b,i);
   }
   tests++;
  }
 }
 fprintf(stderr,"Nonfaulting usercopy PASS checks=%lu\n",tests);
 const unsigned prefixes[]={129,191,255,256,257,511,512,513,1023,4095,4096};
 const unsigned extras[]={1,7,8,63,64,65,128};
 for(unsigned prot=0;prot<2;prot++)
 for(unsigned v=0;v<3;v++)for(unsigned dir=0;dir<2;dir++) {
  if(prot && !dir)continue; /* Read-only second page still allows reads. */
  for(unsigned l=0;l<129+sizeof(prefixes)/sizeof(prefixes[0]);l++) {
   unsigned prefix=l<129?l:prefixes[l-129];
   for(unsigned e=0;e<sizeof(extras)/sizeof(extras[0]);e++)
   for(unsigned b=0;b<8;b++)for(unsigned zero=(v==2 && !dir)?1:0;zero<(dir?1:2);zero++) {
    unsigned n=prefix+extras[e];unsigned char *p=guard+8192-prefix;
    if(mprotect(guard+8192,4096,PROT_READ|PROT_WRITE))die("mprotect reset");
    memset(guard,0xa5,12288);
    if(!dir)for(unsigned i=0;i<prefix;i++)p[i]=sm_pattern(i+17);
    if(mprotect(guard+8192,4096,prot?PROT_READ:PROT_NONE))die("mprotect");
    struct sm_req q={.variant=v,.direction=dir,.n=n,.offset=b,.zero=zero,.user=(uintptr_t)p,.output=(uintptr_t)output};
    req(&q);
    if(q.residual>n || q.residual<n-prefix)fail("invalid residual",v,dir,n,prefix,b,q.residual);
    unsigned copied=n-q.residual;
    if(copied!=prefix && !conservative)fail("residual not exact",v,dir,n,prefix,b,copied);
    if(copied<prefix){under[v]++;if(prefix-copied>max_under[v])max_under[v]=prefix-copied;}
    for(unsigned i=0;i<copied;i++)
     if((dir?p[i]:output[8+b+i])!=(dir?sm_pattern(8+b+i):sm_pattern(i+17)))
      fail("fault copied prefix",v,dir,n,prefix,b,i);
    if(!dir) {
     if(zero)for(unsigned i=copied;i<n;i++)if(output[8+b+i])fail("zero padding",v,dir,n,prefix,b,i);
     if(!zero)for(unsigned i=prefix;i<n;i++)if(output[8+b+i]!=0xa5)fail("write past valid source",v,dir,n,prefix,b,i);
     for(unsigned i=0;i<8+b;i++)if(output[i]!=0xa5)fail("fault kernel prefix",v,dir,n,prefix,b,i);
     for(unsigned i=8+b+n;i<n+32;i++)if(output[i]!=0xa5)fail("fault kernel suffix",v,dir,n,prefix,b,i);
    }
    if(mprotect(guard+8192,4096,PROT_READ|PROT_WRITE))die("mprotect verify");
    for(unsigned i=0;i<4096;i++)if(guard[8192+i]!=0xa5)fail("protected page modified",v,dir,n,prefix,b,i);
    if(p>guard && p[-1]!=0xa5)fail("fault user prefix",v,dir,n,prefix,b,0);
    tests++;faults++;
   }
  }
 }
 bounded();
 printf("{\"usercopy_checks\":%lu,\"fault_checks\":%lu,\"under_copy_cases\":[%lu,%lu,%lu],\"max_under_copy\":[%lu,%lu,%lu],\"passed\":true}\n",
 tests,faults,under[0],under[1],under[2],max_under[0],max_under[1],max_under[2]);
}
int main(int argc, char **argv)
{
 if (argc == 2 && !strcmp(argv[1], "--negative-control")) negative_control = 1;
 else if (argc == 2 && !strcmp(argv[1], "--conservative")) conservative = 1;
 else if (argc != 1) { fprintf(stderr, "usage: %s [--negative-control | --conservative]\n", argv[0]); return 2; }
 fd=open("/dev/c906_usercopy_test",O_RDWR|O_CLOEXEC);if(fd<0)die("open");
 user=mmap(NULL,65536,PROT_READ|PROT_WRITE,MAP_ANONYMOUS|MAP_PRIVATE,-1,0);
 output=malloc(65536);
 guard=mmap(NULL,12288,PROT_READ|PROT_WRITE,MAP_ANONYMOUS|MAP_PRIVATE,-1,0);
 if(user==MAP_FAILED || guard==MAP_FAILED || !output)die("allocation");
 correct();
 close(fd);
 return 0;
}
