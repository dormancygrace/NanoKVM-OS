#!/usr/bin/env python3
"""Execute the real patched gadget reply handlers against bounded host buffers."""
import argparse, re, subprocess, tempfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--kernel-source',type=Path,required=True);a=p.parse_args()
base=a.kernel_source/'drivers/usb/gadget/function'
def function(source,name):
 text=source.read_text();m=re.search(r'(?:static )?(?:int|void) '+name+r'\s*\(',text);assert m,name
 start=text.index('{',m.start());depth=1;end=start+1
 while depth:
  depth+=(text[end]=='{')-(text[end]=='}');end+=1
 return text[m.start():end]+'\n'
code=r"""
#include <assert.h>
#include <stdint.h>
#include <string.h>
#include <errno.h>
#include <stdio.h>
typedef uint8_t u8;typedef uint16_t u16;typedef uint32_t u32;
#define SS_INVALID_COMMAND 1
#define SS_INVALID_FIELD_IN_CDB 2
#define SS_LOGICAL_BLOCK_ADDRESS_OUT_OF_RANGE 3
#define MMC_PROFILE_DVD_ROM 0x10
#define MMC_PROFILE_CD_ROM 8
struct fsg_lun { int cdrom,cd_as_dvd,sense_data; uint64_t num_sectors; };
struct fsg_common { struct fsg_lun *curlun; u8 cmnd[16]; };
struct fsg_buffhd { void *buf; };
static u16 get_unaligned_be16(const void*p){const u8*b=p;return (b[0]<<8)|b[1];}
static u32 get_unaligned_be32(const void*p){const u8*b=p;return ((u32)b[0]<<24)|(b[1]<<16)|(b[2]<<8)|b[3];}
static void put_unaligned_be16(u16 n,void*p){u8*b=p;b[0]=n>>8;b[1]=n;}
static void put_unaligned_be24(u32 n,void*p){u8*b=p;b[0]=n>>16;b[1]=n>>8;b[2]=n;}
static void put_unaligned_be32(u32 n,void*p){u8*b=p;b[0]=n>>24;b[1]=n>>16;b[2]=n>>8;b[3]=n;}
"""
code+=function(base/'storage_common.c','store_cdrom_address')
for name in ['do_get_configuration','do_read_disc_structure','do_read_disc_information','do_read_track_information','do_read_toc','do_read_header']:
 code+=function(base/'f_mass_storage.c',name)
code+=r"""
int main(void){
 u8 storage[2068];struct fsg_buffhd b={storage};
 struct fsg_lun lun={.cdrom=1,.num_sectors=400};struct fsg_common c={.curlun=&lun};
 for(int dvd=0;dvd<=1;dvd++){
  lun.cd_as_dvd=dvd;lun.num_sectors=dvd?0x1000000-0x30000:400;
  memset(c.cmnd,0,16);memset(storage,0xA5,sizeof storage);
  int n=do_get_configuration(&c,&b);assert(n>=8&&n<=256);
  assert(get_unaligned_be16(storage+6)==(dvd?0x10:8));assert(get_unaligned_be32(storage)+4==(u32)n);
  for(int i=256;i<2068;i++)assert(storage[i]==0xA5);
  c.cmnd[1]=2;assert(do_read_toc(&c,&b)==20);
  assert(storage[17]<(dvd?40:256));
  memset(c.cmnd,0,16);assert(do_read_header(&c,&b)==(dvd?-EINVAL:8));
  assert(do_read_disc_information(&c,&b)==34);
  for(int i=16;i<24;i++)assert(storage[i]==(dvd?0:0xff));
  memset(c.cmnd,0,16);c.cmnd[1]=1;c.cmnd[5]=1;
  assert(do_read_track_information(&c,&b)==36);assert(get_unaligned_be32(storage+24)==lun.num_sectors);
 }
 memset(c.cmnd,0,16);memset(storage,0xA5,sizeof storage);
 assert(do_read_disc_structure(&c,&b)==2052);
 assert(storage[13]==0xff&&storage[14]==0xff&&storage[15]==0xff);
 for(int i=2052;i<2068;i++)assert(storage[i]==0xA5);
 c.cmnd[7]=1;assert(do_read_disc_structure(&c,&b)==-EINVAL);
 puts("CD/DVD profiles, TOC, finalized disc, track size, max physical sector and buffer bounds passed");
}
"""
with tempfile.TemporaryDirectory(prefix='nkos-dvd-test-') as tmp:
 path=Path(tmp);(path/'test.c').write_text(code)
 subprocess.run(['cc','-Wall','-Wextra','-Werror','-O2',str(path/'test.c'),'-o',str(path/'test')],check=True)
 subprocess.run([str(path/'test')],check=True)
