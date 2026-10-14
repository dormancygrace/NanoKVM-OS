#!/usr/bin/env python3
"""Exercise actual selected SDIO submission functions with transport/queue doubles."""
import argparse, subprocess
from pathlib import Path
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--driver', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
a.output.mkdir(parents=True, exist_ok=True)

def function(text, signature):
    start = text.index(signature)
    opening = text.index('{', start)
    level = 1
    end = opening + 1
    while level:
        level += (text[end] == '{') - (text[end] == '}')
        end += 1
    return text[start:end]

tx = (a.driver / 'rwnx_msg_tx.c').read_text()
main = (a.driver / 'rwnx_main.c').read_text()
source = r'''
#include <assert.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define AICWF_SDIO_SUPPORT 1
#define KERNEL_VERSION(a,b,c) (((a)<<16)+((b)<<8)+(c))
#define LINUX_VERSION_CODE KERNEL_VERSION(7,2,3)
#define AICWFDBG(...) ((void)0)
#define RWNX_DBG(...) ((void)0)
#define sdio_err(...) ((void)0)
#define RWNX_CMD_ARRAY_SIZE 40
#define RWNX_CMD_HIGH_WATER_SIZE 20
#define RWNX_CMD_FLAG_NONBLOCK 1
#define RWNX_CMD_FLAG_REQ_CFM 2
#define RWNX_CMD_FLAG_WAIT_ACK 4
#define BUS_DOWN_ST 0
#define container_of(ptr,type,member) ((type*)((char*)(ptr)-offsetof(type,member)))
typedef unsigned short lmac_msg_id_t;
struct lmac_msg { unsigned short id,dest_id,src_id,param_len; unsigned char param[]; };
struct rwnx_cmd { int used,flags,result,id,reqid,array_id; struct lmac_msg *a2e_msg; void *e2a_msg; };
struct rwnx_cmd_mgr { int (*queue)(struct rwnx_cmd_mgr*,struct rwnx_cmd*); };
struct bus { int state; };
struct sdio { struct bus *bus_if; };
struct rwnx_hw { struct sdio *sdiodev; struct rwnx_cmd_mgr *cmd_mgr; void *ws_tx; };
struct wiphy { int unused; }; struct net_device { int unused; };
static struct rwnx_cmd cmd_array[RWNX_CMD_ARRAY_SIZE];
static int cmd_array_index, locked, wake, delays, allocations, frees, sends, queued, queue_error, deferred;
static struct rwnx_cmd *pending;
#define spin_lock_irqsave(lock,flags) do { (void)(flags); assert(!locked); locked=1; } while(0)
#define spin_unlock_irqrestore(lock,flags) do { (void)(flags); assert(locked); locked=0; } while(0)
static void __attribute__((unused)) mdelay(int ms) { assert(locked); delays+=ms; }
static void rwnx_wakeup_lock(void *unused) { (void)unused; wake++; }
static void rwnx_wakeup_unlock(void *unused) { (void)unused; assert(wake==1); wake--; }
static void kfree(void *ptr) { assert(ptr); frees++; free(ptr); }
static void aicwf_set_cmd_tx(void *dev, struct lmac_msg *msg, unsigned len) {
    (void)dev; assert(msg->id==123 && len==sizeof(*msg)+4); sends++;
}
'''
for signature in ['struct rwnx_cmd *rwnx_cmd_malloc(', 'void rwnx_cmd_free(', 'static void rwnx_msg_free(', 'static int rwnx_send_msg(']:
    source += '\n' + function(tx, signature) + '\n'
source += '\n' + function(main, 'static int rwnx_cfg80211_set_power_mgmt(') + '\n'
source += r'''
static int queue(struct rwnx_cmd_mgr *mgr, struct rwnx_cmd *cmd) {
    (void)mgr; queued++;
    if(queue_error) return queue_error; /* rejected before queue owns it */
    if(deferred) { pending=cmd; return 0; }
    kfree(cmd->a2e_msg); rwnx_cmd_free(cmd); return 0;
}
static void *message(void) {
    struct lmac_msg *m=calloc(1,sizeof(*m)+4); assert(m); allocations++;
    m->id=123; m->param_len=4; return m->param;
}
static void reset(void) {
    assert(!wake && !locked && !pending);
    memset(cmd_array,0,sizeof(cmd_array));
    allocations=frees=sends=queued=delays=queue_error=deferred=0;
}
static void clean(void) {
    assert(!wake && !locked && allocations==frees && !delays);
}
int main(void) {
    struct bus bus={0}; struct sdio sdio={&bus}; struct rwnx_cmd_mgr mgr={queue};
    struct rwnx_hw hw={&sdio,&mgr,NULL};
    reset(); assert(rwnx_send_msg(&hw,message(),1,456,NULL)==-ENETDOWN); assert(!queued); clean();
    bus.state=1;
    reset(); for(int i=0;i<40;i++) cmd_array[i].used=1;
    assert(rwnx_send_msg(&hw,message(),1,456,NULL)==-ENOMEM); assert(!queued); clean();
    reset(); for(int i=0;i<20;i++) cmd_array[i].used=1;
    assert(rwnx_send_msg(&hw,message(),1,456,NULL)==0); assert(queued==1 && !cmd_array[20].used); clean();
    for(int i=0;i<2;i++) {
        reset(); queue_error=i ? -ENOMEM : -EPIPE;
        assert(rwnx_send_msg(&hw,message(),1,456,NULL)==queue_error);
        assert(queued==1 && !cmd_array[0].used); clean();
    }
    reset(); assert(rwnx_send_msg(&hw,message(),0,0,NULL)==0);
    assert(sends==1 && !queued && !cmd_array[0].used); clean();
    reset(); deferred=1;
    assert(rwnx_send_msg(&hw,message(),1,456,NULL)==0);
    assert(pending && pending->used && allocations==1 && frees==0);
    kfree(pending->a2e_msg); rwnx_cmd_free(pending); pending=NULL; clean();
    assert(rwnx_cfg80211_set_power_mgmt(NULL,NULL,true,-1)==-EOPNOTSUPP);
    assert(rwnx_cfg80211_set_power_mgmt(NULL,NULL,false,-1)==-EOPNOTSUPP);
    puts("PASS 9 cases: bus down, full/high-water pool, two queue rejections, no-CFM, deferred ownership, PS enable/disable unsupported");
}
'''
target = a.output / 'contract.c'
target.write_text(source)
subprocess.run(['cc','-std=gnu11','-Wall','-Wextra','-Werror','-Wno-unused-parameter',
                '-fsanitize=address,undefined','-g',str(target),'-o',str(a.output/'contract')],check=True)
subprocess.run([str(a.output/'contract')],check=True)
