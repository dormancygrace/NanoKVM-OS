#!/usr/bin/env python3
"""Compile actual CV181x ownership functions with counting/failing clock stubs."""
import argparse, pathlib, subprocess, tempfile
p=argparse.ArgumentParser();p.add_argument('--source',type=pathlib.Path,required=True);a=p.parse_args()
s=a.source.read_text();start=s.index('static void vpu_cfg_clk_enable(',s.index('static void sram_share_config'));end=s.index('static void vpu_cfg_clk_disable(',start)
pre=r"""
#include <assert.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <errno.h>
#define GENMASK(h,l) (((1U<<((h)+1))-1) & ~((1U<<(l))-1))
#define IS_ERR_OR_NULL(p) (!(p) || (intptr_t)(p)<0)
#define PTR_ERR(p) ((int)(intptr_t)(p))
typedef uint32_t u32;
#define BIT(i) (1U<<(i))
#define ARRAY_SIZE(a) ((int)(sizeof(a)/sizeof((a)[0])))
#define H264_CORE_IDX 0
#define H265_CORE_IDX 1
#define dev_err(...) ((void)0)
struct clk {int count,id;};
struct cvi_vpu_device {void *dev; struct clk *clk_vc_src0,*clk_cfg_reg_vc,*clk_axi_video_codec,*clk_h264c,*clk_apb_h264c,*clk_h265c,*clk_apb_h265c,*clk_vc_src1,*clk_vc_src2;u32 nkos_clock_holds;int nkos_clock_error;};
static struct {int ctrl_register;} vcodec_dev;
static int nkos_clock_lock, fail=-1;
static void mutex_lock(int *x){(void)x;}
static void mutex_unlock(int *x){(void)x;}
static int clk_prepare_enable(struct clk *c){if(c->id==fail)return -1;c->count++;return 0;}
static void clk_disable_unprepare(struct clk *c){assert(c->count>0);c->count--;}
static void sram_share_config(int *p,int m){(void)p;(void)m;}
static void vpu_clk_put(struct cvi_vpu_device *v){(void)v;}
static void vpu_clk_get(struct cvi_vpu_device *v){(void)v;}
"""
post=r"""
int main(void){
 struct clk c[9]; for(int i=0;i<9;i++){c[i]=(struct clk){.id=i};}
 struct cvi_vpu_device v={.clk_vc_src0=&c[0],.clk_cfg_reg_vc=&c[1],.clk_axi_video_codec=&c[2],.clk_h264c=&c[3],.clk_apb_h264c=&c[4],.clk_h265c=&c[5],.clk_apb_h265c=&c[6],.clk_vc_src1=&c[7],.clk_vc_src2=&c[8]};
 for(int n=0;n<10000;n++)vpu_cfg_clk_enable(&v,BIT(H264_CORE_IDX));
 for(int i=0;i<7;i++)assert(c[i].count==(i<5));
 for(int n=0;n<10000;n++)vpu_cfg_clk_enable(&v,BIT(n%2));
 for(int i=0;i<7;i++)assert(c[i].count==1);
 vpu_cfg_clk_put(&v);for(int i=0;i<7;i++)assert(c[i].count==0);
 for(fail=0;fail<7;fail++){vpu_cfg_clk_enable(&v,BIT(H265_CORE_IDX));assert(v.nkos_clock_holds==0);for(int i=0;i<7;i++)assert(c[i].count==0);}
 fail=-1;vpu_cfg_clk_enable(&v,BIT(H264_CORE_IDX));
 u32 kept=v.nkos_clock_holds;fail=5;vpu_cfg_clk_enable(&v,BIT(H265_CORE_IDX));
 assert(v.nkos_clock_holds==kept);for(int i=0;i<7;i++)assert(c[i].count==(i<5));
 fail=-1;vpu_cfg_clk_put(&v);
 vpu_cfg_clk_get(&v);assert(v.nkos_clock_error==0);assert(v.nkos_clock_holds==127);
 for(int n=0;n<10000;n++)vpu_cfg_clk_enable(&v,BIT(n%2));
 for(int i=0;i<7;i++)assert(c[i].count==1);
 vpu_cfg_clk_put(&v);for(int i=0;i<9;i++)assert(c[i].count==0);
 for(fail=0;fail<7;fail++){vpu_cfg_clk_get(&v);assert(v.nkos_clock_error==-EIO);assert(v.nkos_clock_holds==0);for(int i=0;i<9;i++)assert(c[i].count==0);}
 fail=-1;
 struct clk **slots[]={&v.clk_vc_src0,&v.clk_cfg_reg_vc,&v.clk_axi_video_codec,&v.clk_h264c,&v.clk_apb_h264c,&v.clk_h265c,&v.clk_apb_h265c,&v.clk_vc_src1,&v.clk_vc_src2};
 for(int i=0;i<9;i++){struct clk *saved=*slots[i];*slots[i]=NULL;vpu_cfg_clk_get(&v);assert(v.nkos_clock_error==-ENOENT);assert(v.nkos_clock_holds==0);*slots[i]=(void *)(intptr_t)-EAGAIN;vpu_cfg_clk_get(&v);assert(v.nkos_clock_error==-EAGAIN);*slots[i]=saved;}
 vpu_cfg_clk_get(&v);assert(v.nkos_clock_error==0);vpu_cfg_clk_put(&v);
 puts("PASS probe acquisition and failures, repeated clocks, codec alternation, removal, failures and prior-owner preservation");
}
"""
with tempfile.TemporaryDirectory() as d:
 f=pathlib.Path(d)/'test.c';f.write_text(pre+s[start:end]+post)
 subprocess.run(['cc','-std=c11','-Wall','-Wextra','-Werror',str(f),'-o',d+'/test'],check=True)
 subprocess.run([d+'/test'],check=True)
