#!/usr/bin/env python3
"""Test extracted CIF control functions; kernel primitives/GPIO operations mocked."""
from pathlib import Path
import re, subprocess, os, hashlib, json
src=Path(os.environ['NANOKVM_OSDRV_SOURCE']).resolve()
source=src/'interdrv/cif/chip/cv181x/cif.c'
text=source.read_text()
out=Path(os.environ['NANOKVM_CIF_PROBE_OUTPUT']).resolve()
out.mkdir(exist_ok=True)
def definition(pattern):
    match=re.search(pattern,text,re.M)
    if not match: raise ValueError(pattern)
    pos,depth=match.end(),1
    while depth:
        depth+=(text[pos]=='{')-(text[pos]=='}');pos+=1
    return text[match.start():pos]+'\n'
def function(name):
    return definition(r'^static (?:int|ssize_t) '+name+r'\([^;]*?\n\{')
api=(src/'interdrv/include/chip/cv181x/uapi/linux/cif_uapi.h').read_text()
uapi=re.search(r'struct snsr_rst_gpio_s \{.*?\n\};',api,re.S).group()
prelude=r'''
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <errno.h>
#include <assert.h>
#include <ctype.h>
#include <sys/types.h>
#define MAX_LINK_NUM 2
#define MAX_CIF_PROC_BUF 32
#define ARRAY_SIZE(a) (sizeof(a)/sizeof(a[0]))
#define GPIO_ACTIVE_HIGH 0
#define GPIO_ACTIVE_LOW 1
#define __user
#define loff_t int64_t
struct gpio_desc { int id, low, calls, logical, physical, error; };
struct cvi_link { struct gpio_desc *snsr_reset; };
struct cvi_cif_dev { struct cvi_link link[MAX_LINK_NUM]; };
static int desc_to_gpio(struct gpio_desc *d) { return d->id; }
static int gpiod_is_active_low(struct gpio_desc *d) { return d->low; }
static int gpiod_direction_output(struct gpio_desc *d,int on) {
    d->calls++; if(d->error)return d->error;
    d->logical=on; d->physical=on^d->low; return 0;
}
'''
parts=[prelude,uapi,function('cif_reset_snsr_gpio'),function('cif_gpio_init'),
    re.search(r'static const char \* const dbg_type\[\] = \{.*?\n\};',text,re.S).group(),
    definition(r'^struct cif_debug_request \{')+';',function('cif_parse_debug')]
parts.append(r'''
struct file { struct cvi_cif_dev *device; };
static struct file *file_inode(struct file *f) { return f; }
static struct cvi_cif_dev *pde_data(struct file *f) { return f->device; }
static int copy_failure,proc_result,proc_calls;
static char proc_text[MAX_CIF_PROC_BUF];
static int copy_from_user(void *d,const void *s,size_t n) {
    if(copy_failure)return 1; memcpy(d,s,n);return 0;
}
static int dbg_hdler(struct cvi_cif_dev *d,const char *s) {
    (void)d;assert(strlen(s)<sizeof(proc_text));strcpy(proc_text,s);
    proc_calls++;return proc_result;
}
''')
parts.append(function('cif_proc_write'))
parts.append(r'''
int main(void) {
    struct cvi_cif_dev dev={0};
    struct gpio_desc line={.id=73,.low=1};
    struct snsr_rst_gpio_s request={.devno=0,.snsr_rst_pin=361,.snsr_rst_pol=0};
    assert(cif_gpio_init(&dev,request)==-EOPNOTSUPP);
    assert(cif_reset_snsr_gpio(&dev,0,1)==0); /* optional unwired reset */
    dev.link[0].snsr_reset=&line;
    assert(cif_gpio_init(&dev,request)==-EINVAL); /* old global361 cannot remap */
    request.snsr_rst_pin=73;request.snsr_rst_pol=1;
    assert(cif_gpio_init(&dev,request)==0);
    request.snsr_rst_pol=0;assert(cif_gpio_init(&dev,request)==-EINVAL);
    request.snsr_rst_pol=2;assert(cif_gpio_init(&dev,request)==-EINVAL);
    request.devno=2;assert(cif_gpio_init(&dev,request)==-EINVAL);
    assert(line.calls==0 && dev.link[0].snsr_reset==&line);
    assert(cif_reset_snsr_gpio(&dev,0,1)==0 && line.physical==0);
    assert(cif_reset_snsr_gpio(&dev,0,0)==0 && line.physical==1);
    line.low=0;
    assert(cif_reset_snsr_gpio(&dev,0,1)==0 && line.physical==1);
    assert(cif_reset_snsr_gpio(&dev,0,0)==0 && line.physical==0);
    int calls=line.calls;
    assert(cif_reset_snsr_gpio(&dev,2,1)==-EINVAL && line.calls==calls);
    assert(cif_reset_snsr_gpio(&dev,0,2)==-EINVAL && line.calls==calls);
    line.error=-EIO;assert(cif_reset_snsr_gpio(&dev,0,1)==-EIO);
    puts("PASS DT-owned reset, remap/polarity rejection, optional reset, GPIO error propagation");
    struct cif_debug_request parsed;
    assert(cif_parse_debug("SNSR_R 0 1\n",&parsed)==0 && parsed.type==2 && parsed.value==1);
    assert(cif_parse_debug("snsr_on 1 1 3",&parsed)==0 && parsed.extra==3);
    const char *bad[]={"", "reset", "snsr_r 0", "snsr_r 2 1", "snsr_r -1 1", "reset 0 1", "snsr_on 0 1", "snsr_on 0 1 2 extra", "unknown 0"};
    for(size_t i=0;i<ARRAY_SIZE(bad);++i) assert(cif_parse_debug(bad[i],&parsed)==-EINVAL);
    char longcmd[200];memset(longcmd,'x',sizeof(longcmd));longcmd[199]=0;
    assert(cif_parse_debug(longcmd,&parsed)==-EINVAL);
    puts("PASS bounded debug parser rejects missing/extra fields, invalid device and oversized command");
    struct file file={.device=&dev};loff_t pos=0;
    assert(cif_proc_write(&file,"",0,&pos)==0 && proc_calls==0);
    assert(cif_proc_write(&file,longcmd,32,&pos)==-E2BIG && proc_calls==0);
    copy_failure=1;assert(cif_proc_write(&file,"reset 0",7,&pos)==-EFAULT && proc_calls==0);
    copy_failure=0;assert(cif_proc_write(&file,"reset\0 0",8,&pos)==-EINVAL && proc_calls==0);
    proc_result=-EIO;assert(cif_proc_write(&file,"reset 0",7,&pos)==-EIO);
    assert(!strcmp(proc_text,"reset 0"));
    proc_result=0;assert(cif_proc_write(&file,longcmd,31,&pos)==31 && strlen(proc_text)==31);
    puts("PASS proc input bounds, copy failure, NUL termination and handler error propagation");
    return 0;
}
''')
(out/'probe.c').write_text('\n'.join(parts))
cross=str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT'])/'host/bin/riscv64-buildroot-linux-musl-gcc')
flags=['-std=gnu11','-Wall','-Wextra','-Werror','-Wno-unused-parameter','-Wno-misleading-indentation','-O1','-g']
for cc,extra,name in [('gcc',['-fsanitize=address,undefined','-fno-omit-frame-pointer'],'probe-host'),
    (cross,['-static','-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector','-mtune=thead-c906','-mno-fence-tso'],'probe-c906')]:
    subprocess.run([cc,*flags,*extra,str(out/'probe.c'),'-o',str(out/name)],check=True)
result=subprocess.check_output([str(out/'probe-host')],text=True)
(out/'host.txt').write_text(result)
(out/'source.json').write_text(json.dumps({'source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),
    'probe_sha256':hashlib.sha256((out/'probe.c').read_bytes()).hexdigest()},indent=2)+'\n')
print(result)
