/* Template: generator inserts the selected driver's actual functions and flags. */
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
#define printk(...) ((void)0)
#define WARN(condition,...) (condition)
#define BIT(n) (1U<<(n))
/* INSERT_FLAGS */
#define RWNX_CMD_ARRAY_SIZE 40
#define RWNX_CMD_HIGH_WATER_SIZE 20
#define RWNX_CMD_E2AMSG_LEN_MAX 256
#define RWNX_80211_CMD_TIMEOUT_MS 6000
#define RWNX_CMD_MGR_STATE_INITED 1
#define RWNX_CMD_MGR_STATE_CRASHED 2
#define BUS_DOWN_ST 0
#define ME_TRAFFIC_IND_REQ 999
#define container_of(ptr,type,member) ((type*)((char*)(ptr)-offsetof(type,member)))
typedef unsigned short lmac_msg_id_t;
typedef unsigned char u8_l;
struct list_head { struct list_head *next, *prev; };
static void INIT_LIST_HEAD(struct list_head *h) { h->next=h->prev=h; }
static int list_empty(const struct list_head *h) { return h->next==h; }
static void list_add_tail(struct list_head *n, struct list_head *h) {
    n->prev=h->prev; n->next=h; h->prev->next=n; h->prev=n;
}
static void list_del(struct list_head *n) {
    n->prev->next=n->next; n->next->prev=n->prev;
    n->next=n->prev=NULL; /* Poison baseline removals; double removal must fail. */
}
static void __attribute__((unused)) list_del_init(struct list_head *n) {
    list_del(n); INIT_LIST_HEAD(n);
}
#define list_entry(ptr,type,member) container_of(ptr,type,member)
#define list_for_each_entry_safe(pos,n,head,member) \
    for(pos=list_entry((head)->next,__typeof__(*pos),member), \
        n=list_entry(pos->member.next,__typeof__(*pos),member); \
        &pos->member!=(head); pos=n,n=list_entry(n->member.next,__typeof__(*n),member))
struct completion { int done; };
static void init_completion(struct completion *c) { c->done=0; }
static void complete(struct completion *c) { c->done++; }
static void spin_lock_bh(int *l) { assert(!*l); *l=1; }
static void spin_unlock_bh(int *l) { assert(*l); *l=0; }
#define lockdep_assert_held(l) assert(*(l))
#define spin_lock_irqsave(l,f) do { (void)(f); spin_lock_bh(l); } while(0)
#define spin_unlock_irqrestore(l,f) do { (void)(f); spin_unlock_bh(l); } while(0)
static int in_softirq(void) { return 0; }
static void mdelay(int ms) { (void)ms; assert(!"unexpected delay"); }
static void msleep(int ms) { (void)ms; assert(!"unexpected sleep"); }
static unsigned long msecs_to_jiffies(unsigned long n) { return n; }
struct lmac_msg { unsigned short id,dest_id,src_id,param_len; unsigned char param[]; };
struct rwnx_cmd_e2amsg { unsigned short id,param_len; unsigned char param[256]; };
struct rwnx_cmd {
    struct list_head list;
    lmac_msg_id_t id,reqid;
    struct lmac_msg *a2e_msg;
    char *e2a_msg;
    uint32_t tkn;
    uint16_t flags;
    struct completion complete;
    uint32_t result;
    unsigned char used;
    int array_id;
};
struct rwnx_cmd_mgr {
    int state,lock;
    uint32_t next_tkn,queue_sz,max_queue_sz;
    struct list_head cmds;
    int (*queue)(struct rwnx_cmd_mgr*,struct rwnx_cmd*);
};
struct bus { int state; };
struct rwnx_hw;
struct aic_sdio_dev {
    struct bus *bus_if;
    struct rwnx_hw *rwnx_hw;
    struct rwnx_cmd_mgr cmd_mgr;
    struct { unsigned wakeup_reg; } sdio_reg;
};
struct rwnx_hw { struct aic_sdio_dev *sdiodev; struct rwnx_cmd_mgr *cmd_mgr; void *ws_tx; };
typedef int (*msg_cb_fct)(struct rwnx_hw*,struct rwnx_cmd*,struct rwnx_cmd_e2amsg*);
static struct rwnx_cmd cmd_array[RWNX_CMD_ARRAY_SIZE];
static int cmd_array_index,cmd_array_lock,wake,allocations,frees,sends,reset_events,wakeup_writes,work_wakes,unsolicited,matched;
static struct rwnx_cmd_mgr *current_mgr;
enum event { TIMEOUT, CONFIRM, DEADLINE_CONFIRM, DRAIN };
static enum event next_event;
static unsigned long wait_for_completion_timeout(struct completion*,unsigned long);
static void rwnx_wakeup_lock(void *p) { assert(!wake); wake++; }
static void rwnx_wakeup_unlock(void *p) { assert(wake==1); wake--; }
static void kfree(void *p) { assert(p); frees++; free(p); }
static void aicwf_set_cmd_tx(void *dev,struct lmac_msg *m,unsigned n) {
    assert(m->id==123 && n==sizeof(*m)+4); sends++;
}
static int aicwf_sdio_writeb(struct aic_sdio_dev *d,unsigned reg,unsigned value) {
    assert(!d->cmd_mgr.lock && value==2); wakeup_writes++; return 0;
}
static void aic8800_start_system_reset_flow(struct aic_sdio_dev *d) { reset_events++; }
static void cmd_dump(const struct rwnx_cmd *c) { }
#define WAKE_CMD_WORK(m) do { assert(!(m)->lock); work_wakes++; } while(0)
/* INSERT_FUNCTIONS */
static int callback(struct rwnx_hw *hw,struct rwnx_cmd *cmd,struct rwnx_cmd_e2amsg *msg) {
    if(cmd) { assert(hw->cmd_mgr->lock); matched++; }
    else { assert(!hw->cmd_mgr->lock); unsolicited++; }
    return 0;
}
static void deliver(void) {
    struct rwnx_cmd_e2amsg msg={.id=456,.param_len=4,.param={0x12,0x34,0x56,0x78}};
    assert(!current_mgr->lock);
    assert(cmd_mgr_msgind(current_mgr,&msg,callback)==0);
}
static unsigned long wait_for_completion_timeout(struct completion *c,unsigned long ticks) {
    assert(!current_mgr->lock && ticks==6000);
    assert(frees==allocations); /* Request copied and freed before waiting. */
    switch(next_event) {
    case TIMEOUT: return 0;
    case CONFIRM: deliver(); assert(c->done); return 1;
    case DEADLINE_CONFIRM: deliver(); assert(c->done); return 0;
    case DRAIN: cmd_mgr_drain(current_mgr); assert(c->done); return 1;
    }
    abort();
}
static void *message(void) {
    struct lmac_msg *m=calloc(1,sizeof(*m)+4); assert(m); allocations++;
    m->id=123; m->param_len=4; return m->param;
}
static void __attribute__((unused)) clean(void) {
    assert(!wake && !cmd_array_lock && !current_mgr->lock);
    assert(allocations==frees && !current_mgr->queue_sz && list_empty(&current_mgr->cmds));
    for(int i=0;i<RWNX_CMD_ARRAY_SIZE;i++) assert(!cmd_array[i].used);
}
static void reset(struct rwnx_cmd_mgr *m) {
    assert(!wake && !cmd_array_lock && !m->lock);
    memset(cmd_array,0,sizeof(cmd_array));
    memset(m,0,sizeof(*m)); INIT_LIST_HEAD(&m->cmds);
    m->max_queue_sz=16; m->state=RWNX_CMD_MGR_STATE_INITED; m->queue=cmd_mgr_queue;
    allocations=frees=sends=reset_events=wakeup_writes=work_wakes=unsolicited=matched=0;
    current_mgr=m;
}
int main(void) {
    struct bus bus={1}; struct aic_sdio_dev dev={.bus_if=&bus};
    struct rwnx_cmd_mgr *m=&dev.cmd_mgr;
    struct rwnx_hw hw={&dev,m,NULL}; dev.rwnx_hw=&hw;
    reset(m); next_event=TIMEOUT;
    char *response=malloc(4); assert(response); memset(response,0,4);
    int result=rwnx_send_msg(&hw,message(),1,456,response);
#ifdef REPRODUCE_OLD_TIMEOUT
    /* Baseline incorrectly reports success, then RX writes through this pointer. */
    assert(result==0); free(response); deliver();
    assert(!"baseline did not reproduce the stale response write");
#else
    assert(result==-ETIMEDOUT);
    assert(m->state==RWNX_CMD_MGR_STATE_CRASHED && !cmd_array[0].e2a_msg);
    assert(reset_events==1 && wakeup_writes==1 && sends==1); clean();
    free(response); deliver(); assert(unsolicited==1 && matched==0); clean();
    /* Crashed queue rejects the next command before any transfer. */
    assert(rwnx_send_msg(&hw,message(),1,456,NULL)==-EPIPE);
    assert(sends==1); clean();
    for(int boundary=0;boundary<2;boundary++) {
        reset(m); next_event=boundary ? DEADLINE_CONFIRM : CONFIRM;
        char bytes[4]={0};
        assert(rwnx_send_msg(&hw,message(),1,456,bytes)==0);
        assert(memcmp(bytes,"\x12\x34\x56\x78",4)==0);
        assert(m->state==RWNX_CMD_MGR_STATE_INITED && !reset_events && !wakeup_writes);
        assert(matched==1 && !cmd_array[0].e2a_msg); clean();
        deliver(); assert(unsolicited==1); clean();
    }
    reset(m); next_event=DRAIN; char bytes[4]={0};
    assert(rwnx_send_msg(&hw,message(),1,456,bytes)==-EINTR);
    assert(!cmd_array[0].e2a_msg && !reset_events && !wakeup_writes);
    assert(memcmp(bytes,"\0\0\0\0",4)==0); clean();
    deliver(); assert(unsolicited==1); clean();
    /* More failures than pool slots, resetting only the simulated queue state. */
    reset(m); next_event=TIMEOUT;
    for(int i=0;i<80;i++) {
        m->state=RWNX_CMD_MGR_STATE_INITED;
        assert(rwnx_send_msg(&hw,message(),1,456,NULL)==-ETIMEDOUT); clean();
    }
    assert(reset_events==80 && wakeup_writes==80);
    puts("PASS: timeout error/ownership, late CFM after freed response, crashed rejection, normal/deadline CFM, drain without double removal, 80 timeout slot reuses");
#endif
    return 0;
}
