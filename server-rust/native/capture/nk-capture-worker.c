#define _GNU_SOURCE
#include "nk-capture-protocol.h"
#include <errno.h>
#include <fcntl.h>
#include <stddef.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <unistd.h>
#include "nk-capture-abi.h"
_Static_assert(sizeof(uint32_t)==4&&sizeof(uint16_t)==2,"wire integer widths");
_Static_assert(sizeof(uintptr_t)==sizeof(void*),"callback context width");
static int send_packet(int fd,const uint8_t*p){ssize_t n;do{n=send(fd,p,NK_PACKET_SIZE,MSG_NOSIGNAL);}while(n<0&&errno==EINTR);return n==NK_PACKET_SIZE?0:-1;}
/* Own any received descriptor immediately, including malformed packets. */
static int receive_packet(int fd,uint8_t*p,int*received){
    union{struct cmsghdr align;uint8_t data[CMSG_SPACE(sizeof(int)*253)];}control;
    struct iovec iov={.iov_base=p,.iov_len=NK_PACKET_SIZE};
    struct msghdr msg={.msg_iov=&iov,.msg_iovlen=1,.msg_control=control.data,.msg_controllen=sizeof(control.data)};
    *received=-1;ssize_t n;do{n=recvmsg(fd,&msg,MSG_CMSG_CLOEXEC);}while(n<0&&errno==EINTR);
    if(n<0)return (int)n;
    unsigned descriptors=0;
    for(struct cmsghdr*c=CMSG_FIRSTHDR(&msg);c;c=CMSG_NXTHDR(&msg,c)){
        if(c->cmsg_level!=SOL_SOCKET||c->cmsg_type!=SCM_RIGHTS||c->cmsg_len<CMSG_LEN(0))continue;
        size_t count=(c->cmsg_len-CMSG_LEN(0))/sizeof(int);int*fds=(int*)CMSG_DATA(c);
        for(size_t i=0;i<count;i++){if(descriptors++==0)*received=fds[i];else close(fds[i]);}
    }
    if(n!=NK_PACKET_SIZE||(msg.msg_flags&(MSG_TRUNC|MSG_CTRUNC))||descriptors>1){if(*received>=0)close(*received);*received=-1;errno=EPROTO;return -1;}
    return NK_PACKET_SIZE;
}
static void response(uint8_t*p,const uint8_t*request,uint8_t kind,int32_t status,uint32_t total){
    memset(p,0,NK_PACKET_SIZE);memcpy(p,"NKC1",4);p[4]=request[4];p[5]=1;p[6]=kind;nk_put32(p+8,nk_u32(request+8));nk_put32(p+12,(uint32_t)status);nk_put32(p+16,total);
}
struct frame{int socket;const uint8_t*request;uint8_t*map;uint32_t total,next;uint16_t headroom;int failed;};
static int append(uintptr_t context,const uint8_t*data,uint32_t size,uint32_t offset,uint32_t total){
    struct frame*f=(struct frame*)context;
    if(f->failed||!data||!size||!total||total>NK_FRAME_MAX||offset!=f->next||offset>total||size>total-offset){f->failed=1;return -1;}
    if(!f->map){
        uint8_t packet[NK_PACKET_SIZE];response(packet,f->request,NK_FRAME_OFFER,0,total);
        if(send_packet(f->socket,packet)<0){f->failed=1;return -1;}
        struct timeval timeout={.tv_sec=2};if(setsockopt(f->socket,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout))<0){f->failed=1;return -1;}
        int owned=-1;int result=receive_packet(f->socket,packet,&owned);timeout.tv_sec=0;int restored=setsockopt(f->socket,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));
        struct stat st;int seals=owned>=0?fcntl(owned,F_GET_SEALS):-1;
        if(result!=NK_PACKET_SIZE||restored<0||!nk_valid(packet,NK_FRAME_ACCEPT)||packet[4]!=f->request[4]||nk_u32(packet+8)!=nk_u32(f->request+8)||nk_u32(packet+12)!=0||nk_u32(packet+16)!=total||nk_u32(packet+20)!=0||owned<0||fstat(owned,&st)<0||!S_ISREG(st.st_mode)||st.st_size!=(off_t)(total+f->headroom)||seals<0||(seals&(F_SEAL_GROW|F_SEAL_SHRINK))!=(F_SEAL_GROW|F_SEAL_SHRINK)){
            if(owned>=0){close(owned);}f->failed=1;return -1;
        }
        void*map=mmap(NULL,total+f->headroom,PROT_WRITE,MAP_SHARED,owned,0);close(owned);
        if(map==MAP_FAILED||map==NULL){if(map==NULL)munmap(map,total+f->headroom);f->failed=1;return -1;}f->map=map;f->total=total;
    }
    if(f->total!=total){f->failed=1;return -1;}
    memcpy(f->map+f->headroom+offset,data,size);f->next+=size;return 0;
}
int nk_capture_run(int fd){
    int type=0;socklen_t length=sizeof(type);struct ucred peer;socklen_t peer_size=sizeof(peer);
    if(getsockopt(fd,SOL_SOCKET,SO_TYPE,&type,&length)<0||type!=SOCK_SEQPACKET||getsockopt(fd,SOL_SOCKET,SO_PEERCRED,&peer,&peer_size)<0||peer.uid!=geteuid()){close(fd);return -1;}
    int initialized=0,outcome=0;
    for(;;){uint8_t request[NK_PACKET_SIZE],packet[NK_PACKET_SIZE];int unwanted=-1;int n=receive_packet(fd,request,&unwanted);if(unwanted>=0){close(unwanted);outcome=-1;break;}if(n==0)break;if(n!=NK_PACKET_SIZE||!nk_valid(request,NK_REQUEST)||nk_u32(request+8)==0||(nk_u16(request+22)!=0&&(request[4]!=NK_VIDEO||nk_u16(request+22)!=NK_DIRECT_HEADROOM))){outcome=-1;break;}
        int status=-EINVAL;uint32_t total=0;uint8_t opcode=request[4],param=request[21];
        if(opcode==NK_INIT){if(initialized)status=-EALREADY;else if(param<=1&&(nk_u16(request+16)==420||nk_u16(request+16)==422)){status=set_h265_gop_mode(param);if(status==0){kvmv_init(0);initialized=1;status=set_mjpeg_chroma(nk_u16(request+16)==422);if(status!=0){kvmv_deinit();initialized=0;}}}}
        else if(opcode==NK_CLOSE){if(initialized){kvmv_deinit();initialized=0;}status=0;}
        else if(!initialized)status=-EPIPE;
        else switch(opcode){
            case NK_HDMI:if(param<=1)status=kvmv_hdmi_control(param);break;
            case NK_SIGNAL:status=kvmv_hdmi_signal_active();break;
            case NK_GOP:if(param>=1&&param<=100){set_h264_gop(param);status=0;}break;
            case NK_GOP_MODE:status=get_h265_gop_mode();break;
            case NK_CHROMA:if(param<=1)status=set_mjpeg_chroma(param);break;
            case NK_CHROMA_STATUS:status=get_mjpeg_chroma_status();break;
            case NK_KEYFRAME:kvmv_request_keyframe();status=0;break;
            case NK_FRAME_DETECT:set_frame_detact(param);status=0;break;
            case NK_EDID:if(param<=1)status=kvmv_edid_maintenance(param);break;
            case NK_MJPEG:case NK_VIDEO:{
                uint16_t quality=nk_u16(request+16);if(opcode==NK_MJPEG&&(quality<1||quality>100))break;
                if(opcode==NK_VIDEO&&(quality<500||quality>20000||(request[18]!=1&&request[18]!=2)||request[19]<1||request[19]>100||request[20]<10||request[20]>120))break;
                struct frame frame={.socket=fd,.request=request,.headroom=nk_u16(request+22)};
                if(opcode==NK_MJPEG)status=kvmv_read_mjpeg_sink(nk_u16(request+12),nk_u16(request+14),quality,append,(uintptr_t)&frame);
                else status=kvmv_read_video_sink(nk_u16(request+12),nk_u16(request+14),request[18],quality,request[19],request[20],append,(uintptr_t)&frame);
                int success=opcode==NK_MJPEG?status==0:(status==3||status==4);
                if(success){if(frame.failed||!frame.map||frame.next!=frame.total)status=-EPROTO;else total=frame.total;}
                if(frame.map&&munmap(frame.map,frame.total+frame.headroom)<0){status=-EPROTO;total=0;}
                break;
            }
            default:break;
        }
        response(packet,request,NK_RESULT,status,total);if(send_packet(fd,packet)<0){outcome=-1;break;}if(opcode==NK_CLOSE)break;
    }
    if(initialized){kvmv_deinit();}close(fd);return outcome;
}

