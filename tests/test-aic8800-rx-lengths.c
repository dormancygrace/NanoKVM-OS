/* Hardware and cfg80211 doubles; driver functions/IPC layouts are extracted. */
#include <assert.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
typedef uint8_t u8, u8_l;
typedef int8_t s8_l;
typedef uint16_t u16, u16_l;
typedef uint32_t u32, u32_l;
typedef uint64_t u64;
#define IPC_E2A_MSG_PARAM_SIZE 256
/* INSERT_STRUCTS */
#define pr_warn_ratelimited(...) ((void)0)
#define txrx_err(...) ((void)0)
#define AICWF_SDIO_SUPPORT
#define RX_HWHRD_LEN 60
#define RX_ALIGNMENT 4
#define CCMP_OR_WEP_INFO 8
#define SDIO_TYPE_CFG 0x10
#define SDIO_TYPE_CFG_CMD_RSP 0x11
#define SDIO_TYPE_CFG_DATA_CFM 0x12
#define SDIO_TYPE_CFG_PRINT 0x13
#define BUS_DOWN_ST 0
#define GFP_KERNEL 0
#define GFP_ATOMIC 0
#define roundup(x,y) (((x)+(y)-1)&~((y)-1))
#define min_t(t,a,b) ((t)(a)<(t)(b)?(t)(a):(t)(b))
#define spin_lock_irqsave(a,b) ((void)(b))
#define spin_unlock_irqrestore(a,b) ((void)(b))
#define BUG_ON(x) assert(!(x))
#define atomic_dec(a) (--*(a))
#define kmalloc(n,f) malloc(n)
#define kfree free
struct sk_buff { u8 *data, *allocation; unsigned len, capacity; };
struct rwnx_hw { void *wiphy; int sdio_env; };
struct bus { int state; };
struct sdio { struct rwnx_hw *rwnx_hw; struct bus *bus_if; };
struct rx_buff { u8 *data, *start, *end, *read; };
struct queue { struct sk_buff *skb; struct rx_buff *buf; unsigned qcnt; };
struct aicwf_rx_priv { int rxbuff_lock; int rx_cnt; struct queue rxq; struct sdio *sdiodev; };
static unsigned data_calls, msg_calls, pulls;
static bool fail_alloc;
static struct sk_buff *alloc_skb(unsigned n) {
    struct sk_buff *s = calloc(1, sizeof(*s)); assert(s);
    s->allocation = s->data = calloc(1, n ? n : 1); assert(s->data);
    s->capacity = n; return s;
}
static struct sk_buff *__dev_alloc_skb(unsigned n, int f) {
    return fail_alloc ? NULL : alloc_skb(n);
}
static void dev_kfree_skb(struct sk_buff *s) { free(s->allocation); free(s); }
#define aicwf_dev_skb_free dev_kfree_skb
static void skb_put(struct sk_buff *s, unsigned n) { assert(n <= s->capacity); s->len = n; }
#ifndef CONFIG_PREALLOC_RX_SKB
static void skb_pull(struct sk_buff *s, unsigned n) {
    assert(n && n <= s->len); assert(++pulls < 100); s->data += n; s->len -= n;
}
static bool aicwf_is_framequeue_empty(struct queue *q) { return !q->skb; }
static struct sk_buff *aicwf_frame_dequeue(struct queue *q) {
    struct sk_buff *s = q->skb; q->skb = NULL; return s;
}
#else
static struct rx_buff *rxbuff_dequeue(struct queue *q) {
    struct rx_buff *b=q->buf; q->buf=NULL; --q->qcnt; return b;
}
static void aicwf_prealloc_rxbuff_free(struct rx_buff *b, int *lock) { free(b->data); free(b); }
#endif
static void rwnx_rxdataind_aicwf(struct rwnx_hw *hw, struct sk_buff *s, void *p) {
    ++data_calls; dev_kfree_skb(s);
}
static void rwnx_rx_handle_msg(struct rwnx_hw *hw, struct ipc_e2a_msg *m) { ++msg_calls; }
static void aicwf_sdio_host_tx_cfm_handler(void *p, u32 *m) { abort(); }
static void rwnx_rx_handle_print(struct rwnx_hw *hw, u8 *m, unsigned n) { abort(); }
#define KERNEL_VERSION(a,b,c) (((a)<<16)|((b)<<8)|(c))
#define LINUX_VERSION_CODE KERNEL_VERSION(7,2,10)
#define CFG80211_BSS_FTYPE_UNKNOWN 0
#define le16_to_cpu(x) (x)
#define div_u64(a,b) ((a)/(b))
struct timespec64 { long tv_sec, tv_nsec; };
static long ktime_get_boottime(void) { return 123; }
static struct timespec64 ktime_to_timespec64(long t) { return (struct timespec64){t,0}; }
struct ieee80211_mgmt {
    u8 header[16], bssid[6], seq[2];
    union { struct { u64 timestamp; u16 beacon_int, capab_info; u8 variable[]; } __attribute__((packed)) probe_resp; } u;
} __attribute__((packed));
_Static_assert(offsetof(struct ieee80211_mgmt,u.probe_resp.variable)==36,"802.11 frame layout");
struct cfg80211_bss { int unused; };
struct ieee80211_channel { int unused; };
struct rwnx_cmd { int unused; };
static struct ieee80211_channel channel;
static struct cfg80211_bss bss;
static unsigned bss_calls, channel_calls, expected_ie;
static struct ieee80211_channel *ieee80211_get_channel(void *w, unsigned f) { ++channel_calls; return &channel; }
static struct cfg80211_bss *cfg80211_inform_bss(void *w, void *ch, int type,
        u8 *mac, u64 tsf, u16 cap, u16 interval, u8 *ie, size_t n, int rssi, int flags) {
    assert(n == expected_ie); for(size_t i=0;i<n;i++) assert(ie[i]==0x5a);
    ++bss_calls; return &bss;
}
static void cfg80211_put_bss(void *w, struct cfg80211_bss *b) { assert(b==&bss); }
/* INSERT_FUNCTIONS */
static void test_transport(void) {
    const unsigned h = offsetof(struct ipc_e2a_msg,param);
    for (unsigned n=0;n<80;n++) for(unsigned declared=0;declared<90;declared++) {
        u8 *buf=calloc(1,n+4); assert(buf);
        struct ipc_e2a_msg *m=(void*)(buf+4);
        if(n>=h) m->param_len=declared;
        assert(aicwf_e2a_msg_fits(buf,n)==(n>=h && declared<=n-h));
        if(n>=h) assert(m->param_len==declared);
        free(buf);
    }
    puts("transport: 7200 short/exact/padded/truncated cases passed");
}
static void test_scan(void) {
    const unsigned h=offsetof(struct scanu_result_ind,payload);
    struct rwnx_hw hw={0};
    for(unsigned n=0;n<100;n++) for(unsigned frame=0;frame<110;frame++) {
        struct ipc_e2a_msg *m=calloc(1,offsetof(struct ipc_e2a_msg,param)+n); assert(m);
        m->param_len=n;
        struct scanu_result_ind *ind=(void*)m->param;
        if(n>=2) ind->length=frame;
        if(n>=h+36) memset((u8*)ind+h+36,0x5a,n-h-36);
        bss_calls=channel_calls=0; expected_ie=frame>=36?frame-36:0;
        rwnx_rx_scanu_result_ind(&hw,NULL,m);
        bool valid=n>=h && frame>=36 && h+frame<=n;
        assert(bss_calls==valid && channel_calls==valid);
        free(m);
    }
    puts("scan: 11000 short/header/frame/IE cases passed");
}
static void run_frame(unsigned actual, unsigned declared, bool config, bool fail) {
    struct sk_buff *s=alloc_skb(actual); skb_put(s,actual);
    if(actual>=4) { s->data[0]=declared; s->data[1]=declared>>8; s->data[2]=config?SDIO_TYPE_CFG_CMD_RSP:0; }
    const unsigned h=offsetof(struct ipc_e2a_msg,param);
    if(config && actual>=4+h) { struct ipc_e2a_msg *m=(void*)(s->data+4); m->param_len=0; }
    struct rwnx_hw hw={0}; struct bus bus={1}; struct sdio sd={&hw,&bus};
    struct aicwf_rx_priv priv={.rx_cnt=1,.sdiodev=&sd};
#ifdef CONFIG_PREALLOC_RX_SKB
    struct rx_buff *b=calloc(1,sizeof(*b)); assert(b);
    b->data=b->start=b->read=s->data; b->end=b->data+s->len;
    free(s); priv.rxq.buf=b; priv.rxq.qcnt=1;
#else
    priv.rxq.skb=s;
#endif
    data_calls=msg_calls=pulls=0; fail_alloc=fail;
    assert(aicwf_process_rxframes(&priv)==0);
    bool fits=actual>=4 && declared && declared+(config?4:RX_HWHRD_LEN)<=actual;
    assert(data_calls==(!config && fits && !fail));
    assert(msg_calls==(config && fits && declared>=h));
    assert(!priv.rxq.skb && !priv.rxq.buf && !priv.rxq.qcnt && priv.rx_cnt==0);
}
static void test_frames(void) {
    for(unsigned n=0;n<200;n++) for(unsigned size=0;size<200;size++)
        for(unsigned config=0;config<2;config++) for(unsigned fail=0;fail<2;fail++)
            run_frame(n,size,config,fail);
    for(unsigned config=0;config<2;config++) run_frame(64,65535,config,false);
    #ifdef CONFIG_PREALLOC_RX_SKB
    puts("SDIO preallocated: 160002 short/oversize/unpadded/allocation-failure cases passed");
#else
    puts("SDIO skb: 160002 short/oversize/unpadded/allocation-failure cases passed");
#endif
}
int main(int argc,char **argv) {
    assert(argc==2);
    if(!strcmp(argv[1],"all") || !strcmp(argv[1],"scan")) test_scan();
    if(!strcmp(argv[1],"all") || !strcmp(argv[1],"transport")) test_transport();
    if(!strcmp(argv[1],"all") || !strcmp(argv[1],"frames")) test_frames();
}
