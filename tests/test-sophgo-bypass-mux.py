#!/usr/bin/env python3
"""Exercise the actual kernel bypass-mux setter against modeled registers."""
import argparse
from pathlib import Path
import subprocess
import tempfile
p=argparse.ArgumentParser()
p.add_argument('kernel',type=Path)
a=p.parse_args()
s=(a.kernel/'drivers/clk/sophgo/clk-cv18xx-ip.c').read_text()
def function(name):
 start=s.index('static int '+name+'(')
 end=s.index('\n}',start)+2
 return s[start:end]
code=r"""
#include <assert.h>
#include <stdint.h>
#include <errno.h>
typedef uint8_t u8;
struct common { int unused; };
struct cv1800_clk_bypass_mux { struct {struct common common;} mux; int bypass; } m;
struct clk_hw { int unused; } hw;
static unsigned int regval, bypass, writes;
static struct cv1800_clk_bypass_mux *hw_to_cv1800_clk_bypass_mux(struct clk_hw *h){return &m;}
static unsigned int clk_hw_get_num_parents(struct clk_hw *h){return 5;}
static int cv1800_clk_setbit(struct common *c,int *b){bypass=1;return 0;}
static int cv1800_clk_clearbit(struct common *c,int *b){bypass=0;return 0;}
/* Existing locked low-level mux writer, hardware selector occupies bits8:9. */
static int mux_set_parent(struct clk_hw *h,u8 index){assert(index<4);regval=(regval&~0x300u)|(index<<8);writes++;return 0;}
"""+function('bypass_mux_set_parent')+r"""
int main(void){
 for(unsigned int from=0;from<5;from++)for(unsigned int to=0;to<5;to++){
  regval=0xa5030009u|((from?from-1:0)<<8);bypass=(from==0);writes=0;
  unsigned int before=regval;
  assert(bypass_mux_set_parent(&hw,to)==0);
  if(to==0){assert(bypass==1);assert(regval==before);assert(writes==0);}
  else{assert(bypass==0);assert(((regval>>8)&3)==to-1);assert((regval&~0x300u)==(before&~0x300u));assert(writes==1);}
 }
 unsigned int before=regval,oldb=bypass;
 assert(bypass_mux_set_parent(&hw,5)==-EINVAL);
 assert(regval==before&&bypass==oldb);
 return 0;
}
"""
with tempfile.TemporaryDirectory(prefix='nkos-clock-') as d:
 c=Path(d)/'test.c';exe=Path(d)/'test';c.write_text(code)
 subprocess.run(['cc','-Wall','-Werror','-o',str(exe),str(c)],check=True)
 subprocess.run([str(exe)],check=True)
print('PASS: 25 parent transitions, bypass, selector offset, unrelated bits, invalid index')
