#!/usr/bin/env python3
"""Extract HDMI pulse control code; GPIO, locks and delays are mocked."""
from pathlib import Path
import re,subprocess,hashlib,json,os
k=Path(os.environ['NANOKVM_KERNEL_SOURCE']).resolve()
s=(k/'drivers/misc/nanokvm-hdmi-reset.c').read_text()
out=Path(os.environ['NANOKVM_HDMI_PROBE_OUTPUT']).resolve();out.mkdir(parents=True,exist_ok=True)
def function(name):
    m=re.search(r'^static (?:int|ssize_t) '+name+r'\([^;]*?\n\{',s,re.M)
    p,depth=m.end(),1
    while depth:
        depth+=(s[p]=='{')-(s[p]=='}');p+=1
    return s[m.start():p]
code=r'''
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <errno.h>
#include <sys/types.h>
struct mutex { int locked; };
struct gpio_desc { int calls,values[2],fail_assert,fail_release; };
struct nanokvm_hdmi_reset { struct gpio_desc *gpio; struct mutex lock; };
static int slept;
static int mutex_trylock(struct mutex *m) { if(m->locked)return 0;m->locked=1;return 1; }
static void mutex_unlock(struct mutex *m) { assert(m->locked);m->locked=0; }
static void msleep(unsigned n) { assert(n==100);slept+=n; }
static int gpiod_direction_output(struct gpio_desc *g,int value) {
    assert(g->calls<2);g->values[g->calls++]=value;
    return value?g->fail_assert:g->fail_release;
}
struct device { struct nanokvm_hdmi_reset *data; };
struct device_attribute { int unused; };
static void *dev_get_drvdata(struct device *d) { return d->data; }
static int sysfs_streq(const char *a,const char *b) {
    return !strcmp(a,b) || (!strcmp(b,"1") && !strcmp(a,"1\n"));
}
'''+function('nanokvm_hdmi_pulse')+'\n'+function('pulse_store')+r'''
int main(void) {
    struct gpio_desc gpio={0};
    struct nanokvm_hdmi_reset reset={.gpio=&gpio};
    struct device dev={.data=&reset};
    assert(pulse_store(&dev,0,"0",1)==-EINVAL && gpio.calls==0);
    assert(pulse_store(&dev,0,"1\n",2)==2);
    assert(gpio.calls==2 && gpio.values[0]==1 && gpio.values[1]==0 && slept==200 && !reset.lock.locked);
    gpio=(struct gpio_desc){0};slept=0;reset.lock.locked=1;
    assert(nanokvm_hdmi_pulse(&reset)==-EBUSY && gpio.calls==0 && !slept);
    reset.lock.locked=0;gpio.fail_assert=-EIO;
    assert(nanokvm_hdmi_pulse(&reset)==-EIO && gpio.calls==2 && gpio.values[1]==0 && slept==100 && !reset.lock.locked);
    gpio=(struct gpio_desc){.fail_release=-EPERM};slept=0;
    assert(nanokvm_hdmi_pulse(&reset)==-EPERM && gpio.calls==2 && slept==100 && !reset.lock.locked);
    puts("PASS HDMI pulse ordering, 100+100ms requests, busy rejection, release after assertion error, error propagation");
    puts("GPIO, locks and sleep mocked; no hardware reset or kernel execution claimed");
    return 0;
}
'''
(out/'probe.c').write_text(code)
cross=str(Path(os.environ['NANOKVM_BUILDROOT_OUTPUT'])/'host/bin/riscv64-buildroot-linux-musl-gcc')
for cc,extra,name in [('gcc',['-fsanitize=address,undefined','-fno-omit-frame-pointer'],'host'),
    (cross,['-static','-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector','-mabi=lp64d','-mtune=thead-c906','-mno-fence-tso'],'c906')]:
    subprocess.run([cc,'-Wall','-Wextra','-Werror','-Wno-unused-parameter','-O1','-g',*extra,str(out/'probe.c'),'-o',str(out/name)],check=True)
result=subprocess.check_output([str(out/'host')],text=True)
(out/'host.txt').write_text(result)
(out/'manifest.json').write_text(json.dumps({'source_sha256':hashlib.sha256(s.encode()).hexdigest(),
    'probe_sha256':hashlib.sha256(code.encode()).hexdigest()},indent=2)+'\n')
print(result)
