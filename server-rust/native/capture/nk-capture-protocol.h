#ifndef NANOKVM_CAPTURE_PROTOCOL_H
#define NANOKVM_CAPTURE_PROTOCOL_H
#include <stdint.h>
/* Every packet is exactly24 bytes, little endian; no native struct layout. */
#define NK_PACKET_SIZE 24
#define NK_FRAME_MAX (64u*1024u*1024u)
#define NK_DIRECT_HEADROOM 9u
enum nk_opcode {NK_INIT=1,NK_HDMI,NK_SIGNAL,NK_GOP,NK_GOP_MODE,NK_CHROMA,NK_CHROMA_STATUS,NK_KEYFRAME,NK_CLOSE,NK_EDID,NK_MJPEG,NK_VIDEO,NK_FRAME_DETECT};
enum nk_packet_kind {NK_REQUEST=0,NK_RESULT=1,NK_FRAME_OFFER=2,NK_FRAME_ACCEPT=3};
static inline uint16_t nk_u16(const uint8_t*p){return (uint16_t)p[0]|(uint16_t)((uint16_t)p[1]<<8);}
static inline uint32_t nk_u32(const uint8_t*p){return (uint32_t)p[0]|((uint32_t)p[1]<<8)|((uint32_t)p[2]<<16)|((uint32_t)p[3]<<24);}
static inline void nk_put32(uint8_t*p,uint32_t v){for(unsigned i=0;i<4;i++)p[i]=(uint8_t)(v>>(i*8));}
static inline int nk_valid(const uint8_t*p,uint8_t kind){return p[0]=='N'&&p[1]=='K'&&p[2]=='C'&&p[3]=='1'&&p[5]==1&&p[6]==kind&&p[7]==0;}
int nk_capture_run(int fd);
#endif
