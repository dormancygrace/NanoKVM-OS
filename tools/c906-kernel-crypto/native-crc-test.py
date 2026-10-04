#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Check CRC scalar/combination math on host; this does not test RVV hardware."""
import argparse
from pathlib import Path
import subprocess
import tempfile

HARNESS = r'''
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "candidates.h"
static u32 oracle(u32 crc,const u8 *p,size_t n,u32 poly) {
  while(n--){crc^=*p++;for(unsigned i=0;i<8;i++)crc=(crc>>1)^((0U-(crc&1))&poly);}
  return crc;
}
/* Only the lane-combining C code is tested here. RVV assembly is not executed. */
void kc_crc_lanes(const u8 *p,const u32 *t,u32 *out,size_t count) {
  u32 poly=t[1]==0x77073096?0xedb88320:0x82f63b78;
  for(size_t i=0;i<count;i++)out[i]=oracle(0,p+i*256,256,poly);
}
int main(void) {
  static const size_t sizes[]={0,1,2,7,8,9,63,64,255,256,257,511,512,513,768,
    1024,1420,1500,4095,4096,4097,8192,16384,65535,65536};
  static const u32 seeds[]={0,~0U,0x31415926,0xfffffffe};
  u8 data[65536+8];u32 state=0x31415926;unsigned cases=0;
  kc_tables_init();
  for(unsigned pattern=0;pattern<4;pattern++) {
    for(size_t i=0;i<sizeof(data);i++){
      state^=state<<13;state^=state>>17;state^=state<<5;
      data[i]=pattern==0?(u8)state:pattern==1?(u8)(i*37+11):pattern==2?0:255;
    }
    for(unsigned kind=0;kind<2;kind++)for(unsigned s=0;s<4;s++)
      for(unsigned off=0;off<8;off++)for(unsigned n=0;n<sizeof(sizes)/sizeof(sizes[0]);n++){
        u32 expected=oracle(seeds[s],data+off,sizes[n],kind?0x82f63b78:0xedb88320);
        if(kc_crc_scalar(seeds[s],data+off,sizes[n],kind)!=expected ||
           kc_crc_vector(seeds[s],data+off,sizes[n],kind)!=expected){
          fprintf(stderr,"FAIL kind=%u n=%zu offset=%u seed=%x pattern=%u\n",kind,sizes[n],off,seeds[s],pattern);return 1;
        }cases++;
      }
  }
  if(kc_crc_scalar(~0U,(const u8*)"123456789",9,0)!=0x340bc6d9 ||
     kc_crc_scalar(~0U,(const u8*)"123456789",9,1)!=0x1cf96d7c)return 1;
  printf("PASS host_CRC_math_cases=%u scalar_slicing8 GF2_lane_combination independent_bitwise_oracle KAT; no_RVV_execution\n",cases);
  return 0;
}
'''
def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--cc", default="cc")
    a = p.parse_args()
    with tempfile.TemporaryDirectory(prefix="c906-crc-host-") as tmp:
        d = Path(tmp)
        (d/"linux").mkdir()
        (d/"linux/types.h").write_text("#include <stdint.h>\n#include <stddef.h>\n#include <stdbool.h>\ntypedef uint32_t u32; typedef uint8_t u8;\n#define __aligned(n) __attribute__((aligned(n)))\n")
        (d/"linux/minmax.h").write_text("#define min_t(t,a,b) ((t)(a)<(t)(b)?(t)(a):(t)(b))\n")
        (d/"linux/unaligned.h").write_text("#include <string.h>\nstatic inline u32 get_unaligned_le32(const void *p){const u8 *b=p;return (u32)b[0]|(u32)b[1]<<8|(u32)b[2]<<16|(u32)b[3]<<24;}\n")
        (d/"candidates.h").write_text("#include <linux/types.h>\nvoid kc_tables_init(void);u32 kc_crc_scalar(u32,const u8*,size_t,bool);u32 kc_crc_vector(u32,const u8*,size_t,bool);\n")
        (d/"crc.c").write_bytes((Path(__file__).parent/"crc.c").read_bytes())
        (d/"test.c").write_text(HARNESS)
        subprocess.run([a.cc,"-O2","-Wall","-Wextra","-Werror","-fsanitize=address,undefined",
                        "-I",str(d),str(d/"crc.c"),str(d/"test.c"),"-o",str(d/"test")],check=True)
        subprocess.run([str(d/"test")],check=True)
if __name__ == "__main__":
    main()
