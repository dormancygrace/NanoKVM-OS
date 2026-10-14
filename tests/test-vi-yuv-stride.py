"""Execute the patched driver's real DMA functions against a register model."""
import argparse
from pathlib import Path
import subprocess
import tempfile

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('osdrv',type=Path)
a=p.parse_args()
def block(text, marker):
    start=text.index(marker);brace=text.index('{',start);depth=1;i=brace+1
    while depth:
        depth += (text[i]=='{')-(text[i]=='}');i+=1
    return text[start:i]
source=(a.osdrv/'interdrv/vi/chip/cv181x/vip/vi_drv.c').read_text()
header=(a.osdrv/'interdrv/include/chip/cv181x/uapi/linux/vi_reg_fields.h').read_text()
code='''
#include <stdint.h>
#include <stdbool.h>
#include <assert.h>
#include <stdio.h>
#define __CV181X__ 1
#define DEFAULT_ALIGN 64
#define ALIGN(x,a) (((x)+(a)-1)&~((a)-1))
#define VI_ALIGN(x) ALIGN(x,16)
typedef uint32_t u32;
enum cvi_isp_raw {ISP_PRERAW_A,ISP_PRERAW_B,ISP_PRERAW_C};
enum {ISP_BLK_ID_DMA_CTL6=6,ISP_BLK_ID_DMA_CTL7,ISP_BLK_ID_DMA_CTL8,ISP_BLK_ID_DMA_CTL9,
 ISP_BLK_ID_DMA_CTL12=12,ISP_BLK_ID_DMA_CTL13,ISP_BLK_ID_DMA_CTL18=18,ISP_BLK_ID_DMA_CTL19,
 ISP_BLK_ID_DMA_CTL28=28,ISP_BLK_ID_DMA_CTL29};
enum {SYS_CONTROL,DMA_SEGLEN,DMA_STRIDE,DMA_SEGNUM};
static uint32_t regs[30][4];
struct pipe {unsigned csibdg_width,csibdg_height;bool is_yuv_bypass_path,is_422_to_420;};
struct isp_ctx {uintptr_t phys_regs[30];struct pipe isp_pipe_cfg[3];};
'''+block(header,'union REG_ISP_DMA_CTL_SYS_CONTROL {')+''';
#define ISP_RD_REG(base,type,reg) regs[base][reg]
#define ISP_WR_REG(base,type,reg,value) (regs[base][reg]=(value))
#define ISP_WR_BITS(base,type,reg,field,value) do { \
 union REG_ISP_DMA_CTL_SYS_CONTROL v={.raw=regs[base][reg]}; \
 v.bits.field=(value);regs[base][reg]=v.raw;} while(0)
static void ispblk_dma_setaddr(struct isp_ctx *ctx,uint32_t id,uint64_t addr){(void)ctx;(void)id;(void)addr;}
'''+block(source,'void ispblk_dma_set_sw_mode(')+'\n'+block(source,'u32 ispblk_dma_yuv_bypass_config(')+'''
int main(void){
 struct isp_ctx c={0};
 for(unsigned i=0;i<30;i++)c.phys_regs[i]=i;
 unsigned widths[]={720,1080,1088,1280,1296,1440,1920,2560};
 unsigned ids[]={ISP_BLK_ID_DMA_CTL6,ISP_BLK_ID_DMA_CTL12,ISP_BLK_ID_DMA_CTL18};
 for(unsigned raw=0;raw<3;raw++)for(unsigned n=0;n<7;n++){
  unsigned id=ids[raw],w=widths[n],pitch=ALIGN(w*2,64);
  c.isp_pipe_cfg[raw]=(struct pipe){w,1280,true,false};
  for(unsigned order=0;order<2;order++){
   regs[id][SYS_CONTROL]=0;
   if(order)ispblk_dma_set_sw_mode(&c,id,false);
   unsigned bytes=ispblk_dma_yuv_bypass_config(&c,id,0,raw);
   if(!order)ispblk_dma_set_sw_mode(&c,id,false);
   union REG_ISP_DMA_CTL_SYS_CONTROL v={.raw=regs[id][SYS_CONTROL]};
   assert(v.bits.STRIDE_SEL==1 && v.bits.SEGLEN_SEL==0 && v.bits.SEGNUM_SEL==0);
   assert(regs[id][DMA_STRIDE]==pitch && regs[id][DMA_SEGLEN]==w*2 && bytes==pitch*1280);
  }
  c.isp_pipe_cfg[raw].is_yuv_bypass_path=false;
  ispblk_dma_set_sw_mode(&c,id,false);
  union REG_ISP_DMA_CTL_SYS_CONTROL v={.raw=regs[id][SYS_CONTROL]};
  assert(v.bits.STRIDE_SEL==0);
  c.isp_pipe_cfg[raw].is_yuv_bypass_path=true;c.isp_pipe_cfg[raw].is_422_to_420=true;
  ispblk_dma_set_sw_mode(&c,id,false);ispblk_dma_yuv_bypass_config(&c,id,0,raw);
  v.raw=regs[id][SYS_CONTROL];assert(v.bits.STRIDE_SEL==0);
 }
 puts("PASS: 48 packed-YUV initialization cases; RAW/conversion mode preserved");
}
'''
with tempfile.TemporaryDirectory(prefix='vi-stride-test-') as tmp:
    tmp=Path(tmp);(tmp/'test.c').write_text(code)
    subprocess.run(['cc','-std=gnu11','-Wall','-Wextra','-Werror',str(tmp/'test.c'),'-o',str(tmp/'test')],check=True)
    subprocess.run([str(tmp/'test')],check=True)
