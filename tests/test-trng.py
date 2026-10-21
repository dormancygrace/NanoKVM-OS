#!/usr/bin/env python3
"""Run the actual TRNG command/read code against a fault-injecting MMIO model."""
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
source = (ROOT / 'platform/modules/cv181x-trng/cv181x_trng.c').read_text()
# Keep the production state machine intact; only replace kernel/MMIO services.
core = source[source.index('#define TRNG_CTRL'):source.index('static int cv181x_trng_probe(')]
pre = r"""
#include <assert.h>
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <errno.h>
typedef uint32_t u32;
struct hwrng { int unused; };
struct device { int unused; };
#define __iomem
#define BIT(n) (1u << (n))
#define container_of(p,t,m) ((t *)((char *)(p) - offsetof(t,m)))
#define min(a,b) ((a)<(b)?(a):(b))
#define dev_err(...) ((void)0)
#define memzero_explicit(p,n) memset(p,0,n)
static u32 regs[64];
static unsigned commands[4096], command_count, random_count, polls, reads;
static unsigned alarm_at, timeout_at;
static bool busy;
static u32 readl(void *addr) {
 reads++;
 if(addr == &regs[0x0c/4] && busy) return BIT(31);
 return *(u32 *)addr;
}
static void writel(u32 val, void *addr) {
 unsigned off = (u32 *)addr - regs;
 if(off == 0x18/4 || off == 0x14/4) { regs[off] &= ~val; return; }
 regs[off] = val;
 if(off != 0) return;
 assert(command_count < 4096);
 commands[command_count++] = val;
 if(val == 0xf) assert(regs[0x18/4] == 0); /* Never zeroize a pending alarm. */
 if(command_count == timeout_at) return;
 regs[0x14/4] |= val == 0xf ? BIT(0) : BIT(4);
 if(command_count == alarm_at) regs[0x18/4] = BIT(4);
 if(val == 6) {
  random_count++;
  for(unsigned i=0;i<4;i++) regs[0x24/4+i] = random_count*16+i;
 }
}
/* A command either completes immediately or exhausts the simulated deadline. */
#define readl_poll_timeout(addr,val,cond,delay,timeout) ({ \
 polls++; (val)=readl(addr); (cond)?0:-ETIMEDOUT; })
"""
post = r"""
static void reset(struct cv181x_trng *t) {
 memset(regs,0,sizeof(regs)); memset(t,0,sizeof(*t)); t->base=regs;
 command_count=random_count=polls=reads=alarm_at=timeout_at=0; busy=false;
}
int main(void) {
 struct cv181x_trng t;
 unsigned char out[64];
 /* Nonblocking calls must neither sleep/poll nor advance hardware state,
  * including when recovery or periodic reseeding would be necessary. */
 for(int mode=0;mode<3;mode++) {
  reset(&t); t.seeded=mode!=0; t.generated=mode==2?1024:0;
  memset(out,0xa5,sizeof(out));
  assert(cv181x_trng_read(&t.rng,out,sizeof(out),false)==0);
  assert(command_count==0 && polls==0 && reads==0);
  for(unsigned i=0;i<sizeof(out);i++) assert(out[i]==0xa5);
 }
 reset(&t); regs[0x18/4]=BIT(4); regs[0x14/4]=~0u;
 assert(cv181x_trng_init(&t.rng)==0 && t.seeded);
 assert(command_count==3 && commands[0]==15 && commands[1]==1 && commands[2]==3);
 assert(cv181x_trng_read(&t.rng,out,32,true)==32 && t.generated==2);
 assert(((u32 *)out)[0]==16 && ((u32 *)out)[4]==32);
 t.generated=1023; unsigned before=command_count;
 assert(cv181x_trng_read(&t.rng,out,32,true)==32 && t.generated==1);
 assert(command_count==before+5 && commands[before]==6 && commands[before+1]==15);
 /* Alarm: no suspect output, invalidate state, clear alarm before recovery. */
 memset(out,0xa5,sizeof(out)); alarm_at=command_count+1;
 assert(cv181x_trng_read(&t.rng,out,32,true)==-EIO && !t.seeded);
 for(unsigned i=0;i<sizeof(out);i++) assert(out[i]==0xa5);
 alarm_at=0; before=command_count;
 assert(cv181x_trng_read(&t.rng,out,32,true)==32 && commands[before]==15);
 /* A later error returns only the previous, validated block. */
 memset(out,0xa5,sizeof(out)); alarm_at=command_count+2;
 assert(cv181x_trng_read(&t.rng,out,32,true)==16 && !t.seeded);
 for(unsigned i=16;i<sizeof(out);i++) assert(out[i]==0xa5);
 alarm_at=0;
 assert(cv181x_trng_read(&t.rng,out,32,true)==32);
 timeout_at=command_count+1;
 assert(cv181x_trng_read(&t.rng,out,32,true)==-ETIMEDOUT && !t.seeded);
 timeout_at=0;
 assert(cv181x_trng_read(&t.rng,out,32,true)==32);
 busy=true; before=command_count;
 assert(cv181x_trng_read(&t.rng,out,32,true)==-ETIMEDOUT && command_count==before);
 busy=false;
 assert(cv181x_trng_read(&t.rng,out,32,true)==32);
 cv181x_trng_cleanup(&t.rng);
 assert(!t.seeded && commands[command_count-1]==15);
 for(unsigned failure=1;failure<=3;failure++) {
  reset(&t); timeout_at=failure;
  assert(cv181x_trng_init(&t.rng)==-ETIMEDOUT && !t.seeded);
  assert(command_count==failure);
 }
 puts("PASS TRNG nonblocking, seed/reseed, alarms, timeouts, partial output and cleanup");
}
"""
with tempfile.TemporaryDirectory(prefix='nkos-trng-') as temp:
    temp = Path(temp)
    (temp / 'test.c').write_text(pre + core + post)
    subprocess.run(['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror',
                    '-Wno-unused-parameter', '-Wno-sign-compare', str(temp / 'test.c'), '-o', str(temp / 'test')], check=True)
    subprocess.run([str(temp / 'test')], check=True)
