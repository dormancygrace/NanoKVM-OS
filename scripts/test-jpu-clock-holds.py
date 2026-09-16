#!/usr/bin/env python3
"""Exercise actual JPU clock functions with failing/counting clock stubs."""
import argparse, pathlib, subprocess, tempfile
parser = argparse.ArgumentParser()
parser.add_argument('--source', type=pathlib.Path, required=True)
a = parser.parse_args()
s = a.source.read_text()
start = s.index('static void jpu_cfg_clk_enable(struct cvi_jpu_device *jdev)\n{')
end = s.index('static void jpu_cfg_clk_disable(', start)
pre = r"""
#include <assert.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <errno.h>
typedef uint32_t u32;
#define BIT(i) (1U<<(i))
#define ARRAY_SIZE(a) ((int)(sizeof(a)/sizeof((a)[0])))
#define IS_ERR_OR_NULL(p) (!(p) || (intptr_t)(p)<0)
#define PTR_ERR(p) ((int)(intptr_t)(p))
#define dev_err(...) ((void)0)
struct clk {int count,id;};
struct cvi_jpu_device {void *dev; struct clk *clk_vc_src0,*clk_cfg_reg_vc,*clk_axi_video_codec,*clk_jpeg,*clk_apb_jpeg;u32 nkos_clock_holds;int nkos_clock_error;};
static int nkos_jpu_clock_lock, fail=-1, action_fail;
static void (*release_action)(void *);
static void *release_data;
static void mutex_lock(int *x){(void)x;}
static void mutex_unlock(int *x){(void)x;}
static int clk_prepare_enable(struct clk *c){if(c->id==fail)return -EIO;c->count++;return 0;}
static void clk_disable_unprepare(struct clk *c){assert(c->count>0);c->count--;}
static void jpu_clk_get(struct cvi_jpu_device *v){(void)v;}
static int devm_add_action_or_reset(void *dev, void (*fn)(void *), void *data){(void)dev;if(action_fail){fn(data);return -ENOMEM;}release_action=fn;release_data=data;return 0;}
"""
post = r"""
int main(void){
 struct clk c[5]; for(int i=0;i<5;i++)c[i]=(struct clk){.id=i};
 struct cvi_jpu_device v={.clk_vc_src0=&c[0],.clk_cfg_reg_vc=&c[1],.clk_axi_video_codec=&c[2],.clk_jpeg=&c[3],.clk_apb_jpeg=&c[4]};
 for(int cycle=0;cycle<100;cycle++){
  jpu_cfg_clk_get(&v);assert(!v.nkos_clock_error);
  for(int n=0;n<10000;n++)jpu_cfg_clk_enable(&v);
  for(int i=0;i<5;i++)assert(c[i].count==1);
  release_action(release_data);assert(!v.nkos_clock_holds);
  for(int i=0;i<5;i++)assert(c[i].count==0);
 }
 for(fail=0;fail<5;fail++){
  v.nkos_clock_error=0;jpu_cfg_clk_get(&v);assert(v.nkos_clock_error==-EIO);
  assert(!v.nkos_clock_holds);for(int i=0;i<5;i++)assert(c[i].count==0);
 }
 fail=-1;v.nkos_clock_error=0;action_fail=1;jpu_cfg_clk_get(&v);
 assert(v.nkos_clock_error==-ENOMEM);assert(!v.nkos_clock_holds);
 for(int i=0;i<5;i++)assert(c[i].count==0);
 action_fail=0;
 struct clk **slots[]={&v.clk_vc_src0,&v.clk_cfg_reg_vc,&v.clk_axi_video_codec,&v.clk_jpeg,&v.clk_apb_jpeg};
 for(int i=0;i<5;i++){struct clk *saved=*slots[i];*slots[i]=NULL;v.nkos_clock_error=0;jpu_cfg_clk_get(&v);assert(v.nkos_clock_error==-ENOENT);assert(!v.nkos_clock_holds);*slots[i]=(void *)(intptr_t)-EAGAIN;jpu_cfg_clk_get(&v);assert(v.nkos_clock_error==-EAGAIN);*slots[i]=saved;}
 puts("PASS JPU repeated enables, lifecycle, all enable/handle failures and devres registration failure");
}
"""
with tempfile.TemporaryDirectory() as d:
 f=pathlib.Path(d)/'test.c';f.write_text(pre+s[start:end]+post)
 subprocess.run(['cc','-std=c11','-Wall','-Wextra','-Werror',str(f),'-o',d+'/test'],check=True)
 subprocess.run([d+'/test'],check=True)
