#ifndef OLED_CTRL_H_
#define OLED_CTRL_H_

#include <stdint.h>
#include <fcntl.h>
#include <unistd.h>
#include <string.h>
#include <stdio.h>
#include <pthread.h>

#define OLED_CMD		0x00
#define OLED_DATA		0x40

#define KVM_TYPE_none   0x00
#define KVM_TYPE_MJPG   0x01
#define KVM_TYPE_H264   0x02
#define KVM_TYPE_H265   0x03

extern uint8_t OLED_state;
extern uint8_t kvm_hw_ver;

#include "oled_font.h"

int oled_exist(void);
// While one exists (in any thread, nested), display writes are queued and
// sent as one I2C transfer per run of commands or data (oled_ctrl.cpp).
struct OledBatch {
	OledBatch();
	~OledBatch();
	OledBatch(const OledBatch &) = delete;
	OledBatch &operator=(const OledBatch &) = delete;
};
// Forget what the display RAM holds: the next drawing sends every byte.
void OLED_ForgetRAM(void);
void OLED_Clear(void);
void OLED_Fill(void);
void OLED_Init(void);
void OLED_Revolve(void);
void OLED_DisplayTurn(uint8_t i);
void OLED_ColorTurn(uint8_t i);
void OLED_ShowCharTurn(uint8_t x,uint8_t y,char chr,uint8_t sizey);
void OLED_ShowString(uint8_t x, uint8_t y, const char *chr, uint8_t sizey);
void OLED_ShowStringTurn(uint8_t x, uint8_t y, const char *chr, uint8_t sizey);
void OLED_ShowString_AlignRight(uint8_t x_end, uint8_t y, const char *chr, uint8_t size);
void OLED_ShowIMG(uint8_t x,uint8_t y,const char *chr,uint8_t width,uint8_t height);
// A frame in display pages. Only what changed since the frame last shown is
// written, unless all is set or a transfer failed since.
void OLED_ShowFrame(const uint8_t *frame, int width, int pages, bool all);

bool OLED_IsDisabled(void);
#endif // OLED_CTRL_H_
