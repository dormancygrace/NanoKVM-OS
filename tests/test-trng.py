#!/usr/bin/env python3
"""Run the U-Boot TRNG driver's command/read code against a fault-injecting MMIO model.

The driver is added by platform/uboot/0010-rng-cv181x-trng.patch; its source is taken from the patch.
"""
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
patch = (ROOT / 'platform/uboot/0010-rng-cv181x-trng.patch').read_text()
hunk = patch[patch.index('+++ b/drivers/rng/cv181x_trng.c'):]
hunk = hunk[hunk.index('\n@@') + 1:]
hunk = hunk[hunk.index('\n') + 1:]
lines = []
for line in hunk.splitlines():
    if not line.startswith('+'):
        break
    lines.append(line[1:])
source = '\n'.join(lines) + '\n'
# Keep the production state machine intact; only replace U-Boot/MMIO services.
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
struct udevice { int unused; };
#define __iomem
#define BIT(n) (1u << (n))
#define min(a,b) ((a)<(b)?(a):(b))
#define dev_err(...) ((void)0)
static u32 regs[64];
static unsigned commands[4096], command_count, random_count;
static unsigned alarm_at, timeout_at;
static bool busy;
struct cv181x_trng_priv;
static void *priv_ptr;
#define dev_get_priv(dev) ((struct cv181x_trng_priv *)priv_ptr)
static u32 readl(void *addr) {
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
#define readl_poll_timeout(addr,val,cond,timeout) ({ \
 (val)=readl(addr); (cond)?0:-ETIMEDOUT; })
"""
post = r"""
static struct cv181x_trng_priv priv;
static struct udevice dev;
static void reset(void) {
 memset(regs,0,sizeof(regs)); priv.base=regs; priv_ptr=&priv;
 command_count=random_count=alarm_at=timeout_at=0; busy=false;
}
static void expect(const unsigned *want, unsigned n) {
 assert(command_count==n);
 for(unsigned i=0;i<n;i++) assert(commands[i]==want[i]);
}
int main(void) {
 u32 out[16];
 /* kaslr-seed: a stale alarm and status are cleared first; one output, then zeroize. */
 reset(); regs[0x18/4]=BIT(4); regs[0x14/4]=~0u;
 assert(cv181x_trng_read(&dev,out,8)==0);
 expect((unsigned[]){15,1,3,6,15}, 5);
 assert(out[0]==16 && out[1]==17);
 /* rng-seed: 32 bytes, two outputs. */
 reset();
 assert(cv181x_trng_read(&dev,out,32)==0);
 expect((unsigned[]){15,1,3,6,6,15}, 6);
 assert(out[0]==16 && out[3]==19 && out[4]==32 && out[7]==35);
 /* A length that is not a multiple of 16 copies only what was asked for. */
 reset(); memset(out,0xa5,sizeof(out));
 assert(cv181x_trng_read(&dev,out,20)==0);
 assert(out[4]==32 && out[5]==0xa5a5a5a5u);
 /* Alarm on an output: an error, and the core is still zeroized (alarm cleared first). */
 reset(); alarm_at=4;
 assert(cv181x_trng_read(&dev,out,32)==-EIO);
 expect((unsigned[]){15,1,3,6,15}, 5);
 /* Timeout while instantiating: no output command, zeroize after. */
 reset(); timeout_at=3;
 assert(cv181x_trng_read(&dev,out,32)==-ETIMEDOUT);
 expect((unsigned[]){15,1,3,15}, 4);
 /* A core that stays busy: no command is written, bounded error. */
 reset(); busy=true;
 assert(cv181x_trng_read(&dev,out,32)==-ETIMEDOUT && command_count==0);
 /* The output is good but the final zeroize fails: reported, not ignored. */
 reset(); timeout_at=6;
 assert(cv181x_trng_read(&dev,out,32)==-EIO);
 puts("PASS U-Boot TRNG seed, partial output, alarms, timeouts and final zeroize");
}
"""
with tempfile.TemporaryDirectory(prefix='nkos-trng-') as temp:
    temp = Path(temp)
    (temp / 'test.c').write_text(pre + core + post)
    subprocess.run(['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror', '-Wno-pointer-arith',
                    '-Wno-unused-parameter', '-Wno-sign-compare', str(temp / 'test.c'), '-o', str(temp / 'test')], check=True)
    subprocess.run([str(temp / 'test')], check=True)
