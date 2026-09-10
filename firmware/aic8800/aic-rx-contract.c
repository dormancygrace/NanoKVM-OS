
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <errno.h>
#include <stdio.h>
#include <assert.h>
typedef uint8_t u8;
typedef uint32_t u32;
#define HCI_EVENT_PKT 4
#define HCI_ACLDATA_PKT 2
#define HCI_SCODATA_PKT 3
#define HCI_EVENT_HDR_SIZE 2
#define HCI_ACL_HDR_SIZE 4
#define HCI_SCO_HDR_SIZE 3
#define GFP_ATOMIC 0
struct sk_buff { unsigned char *bytes; unsigned char type; };
struct hci_dev { struct { unsigned long byte_rx,err_rx; } stat; };
static struct hci_dev *ghdev;
static int allocations, frees, fail_alloc, delivery_result, deliveries, locked;
#define spin_lock_irqsave(lock,flags) do { (void)(flags); assert(!locked); locked=1; } while(0)
#define spin_unlock_irqrestore(lock,flags) do { (void)(flags); assert(locked); locked=0; } while(0)
#define hci_skb_pkt_type(skb) ((skb)->type)
static struct sk_buff *alloc_skb(size_t len,int unused) {
    (void)unused;
    if(fail_alloc) return NULL;
    struct sk_buff *p=malloc(sizeof(*p)); assert(p);
    p->bytes=malloc(len);assert(p->bytes);allocations++;return p;
}
static void skb_put_data(struct sk_buff *p,const void *data,size_t len) { memcpy(p->bytes,data,len); }
static void kfree_skb(struct sk_buff *p) { free(p->bytes);free(p);frees++; }
static int hci_recv_frame(struct hci_dev *hdev,struct sk_buff *p) {
    assert(locked && hdev==ghdev);deliveries++;kfree_skb(p);return delivery_result;
}
int bt_sdio_recv(u8 *packet, u32 length)
{
    struct sk_buff *skb;
    struct hci_dev *hdev;
    unsigned long flags;
    unsigned int header;
    int ret;

    if (!packet || length < 2)
        return -EINVAL;
    switch (packet[0]) {
    case HCI_EVENT_PKT: header = HCI_EVENT_HDR_SIZE; break;
    case HCI_ACLDATA_PKT: header = HCI_ACL_HDR_SIZE; break;
    case HCI_SCODATA_PKT: header = HCI_SCO_HDR_SIZE; break;
    default: return -EINVAL;
    }
    if (length - 1 < header)
        return -EINVAL;
    skb = alloc_skb(length - 1, GFP_ATOMIC);
    if (!skb)
        return -ENOMEM;
    skb_put_data(skb, packet + 1, length - 1);
    hci_skb_pkt_type(skb) = packet[0];
    spin_lock_irqsave(&btsdio_rx_lock, flags);
    hdev = ghdev;
    if (!hdev) {
        spin_unlock_irqrestore(&btsdio_rx_lock, flags);
        kfree_skb(skb);
        return -ENODEV;
    }
    hdev->stat.byte_rx += length;
    /* hci_recv_frame consumes skb on both success and error. */
    ret = hci_recv_frame(hdev, skb);
    if (ret)
        hdev->stat.err_rx++;
    spin_unlock_irqrestore(&btsdio_rx_lock, flags);
    return ret;
}


int main(void) {
    struct hci_dev dev={0}; unsigned char data[12]={HCI_EVENT_PKT};
    ghdev=&dev;
    assert(bt_sdio_recv(NULL,0)==-EINVAL);
    assert(bt_sdio_recv(data,0)==-EINVAL);
    assert(bt_sdio_recv(data,1)==-EINVAL);
    assert(bt_sdio_recv(data,2)==-EINVAL);
    data[0]=99;assert(bt_sdio_recv(data,6)==-EINVAL);
    data[0]=HCI_ACLDATA_PKT;assert(bt_sdio_recv(data,4)==-EINVAL);
    data[0]=HCI_SCODATA_PKT;assert(bt_sdio_recv(data,3)==-EINVAL);
    data[0]=HCI_EVENT_PKT;
    fail_alloc=1;assert(bt_sdio_recv(data,3)==-ENOMEM);fail_alloc=0;
    ghdev=NULL;assert(bt_sdio_recv(data,3)==-ENODEV);ghdev=&dev;
    delivery_result=-ENXIO;assert(bt_sdio_recv(data,3)==-ENXIO);
    assert(dev.stat.err_rx==1);
    delivery_result=0;assert(bt_sdio_recv(data,3)==0);
    assert(deliveries==2 && allocations==frees && !locked);
    printf("PASS actual bt_sdio_recv: malformed headers, OOM, detached HCI, ownership on rejection/success; allocations=%d frees=%d\n",allocations,frees);
}
