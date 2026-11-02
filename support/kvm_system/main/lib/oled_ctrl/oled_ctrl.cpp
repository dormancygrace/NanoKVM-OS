#include "oled_ctrl.h"
#include "kvm_platform.h"
#include "oled_detect.h"

#include <mutex>
#include <string>
#include <time.h>

// oled_ctrl deliberately knows only whether the preference is persistent.
// oled_ui owns the short-lived network-address override and its synchronization.
bool OLED_IPWindowActive(void);

// Chosen by oled_exist(); the board has one OLED bus.
static kvms::I2C *oled_i2c = nullptr;
static int oled_address = 0;

uint8_t OLED_state = 0;
uint8_t kvm_hw_ver = 0;

// The display is written over I2C bit-banged by the kernel (i2c-gpio, a
// udelay per bit). Each byte of a changed line was sent separately, with a
// start, address, control byte and stop around it. Two things cut that down:
//  * Only bytes that differ from what the controller's RAM already holds are
//    sent. Drawing sets a position (oled_cursor) and writes data; a copy of
//    the RAM (oled_ram) skips unchanged bytes, and the position goes out only
//    where the controller's own cursor is not already there. The copy is
//    dropped (everything is sent again) at initialization, after scrolling,
//    and once a minute, in case the display lost its RAM.
//  * Bytes written inside an OledBatch are queued and go out as one transfer
//    per run of commands or of data (the SSD1306 reads any number after one
//    control byte), when the kind changes, the queue is full or the
//    outermost batch ends. Without a batch a byte goes out at once.
// The lock keeps one drawing operation's bytes together across threads.
static std::recursive_mutex oled_lock;
static int oled_batch_depth = 0;
static uint8_t oled_run[1 + 128];
static int oled_run_len = 0;

// SSD1306 page addressing has eight RAM pages; after column 127 the cursor
// wraps to column 0 of the same page. The legacy PCIe layout sends B8..BB:
// keep those addresses untracked instead of assuming they refer to separate
// RAM. oled_cursor sends them verbatim and invalidates the shadow, preserving
// clears on controllers where the high page bit is ignored.
enum { OLED_PAGES = 8, OLED_COLUMNS = 128 };
static uint8_t oled_ram[OLED_PAGES][OLED_COLUMNS];
static bool oled_ram_known[OLED_PAGES][OLED_COLUMNS];
static uint64_t oled_ram_since_ms = 0;
static bool oled_ram_lost = false;
static constexpr uint64_t OLED_RAM_REFRESH_MS = 60000;
// Where the next data byte goes (want_*), and where the controller's
// cursor is (at_*); valid only when known.
static bool oled_want_known = false, oled_at_known = false;
static int oled_want_page, oled_want_column, oled_at_page, oled_at_column;

static uint64_t oled_now_ms(void)
{
	struct timespec now;
	clock_gettime(CLOCK_MONOTONIC, &now);
	return uint64_t(now.tv_sec) * 1000 + now.tv_nsec / 1000000;
}

static void oled_run_flush(void)
{
	const int len = 1 + oled_run_len;
	oled_run_len = 0;
	if (len > 1 && OLED_state && oled_i2c && oled_i2c->writeto(oled_address, oled_run, len) != len) {
		oled_ram_lost = true;
		oled_at_known = false;
	}
	// While the RAM copy is wrong (a lost transfer, a line past the last
	// column) nothing is skipped; once the drawing is done it is dropped.
	if (oled_ram_lost && oled_batch_depth == 0) OLED_ForgetRAM();
}

static void oled_send(uint8_t mode, uint8_t byte)
{
	if (oled_run_len && (oled_run[0] != mode || oled_run_len == (int)sizeof(oled_run) - 1))
		oled_run_flush();
	oled_run[0] = mode;
	oled_run[1 + oled_run_len++] = byte;
	if (oled_batch_depth == 0) oled_run_flush();
}

// Everything is sent again from here on.
void OLED_ForgetRAM(void)
{
	std::lock_guard<std::recursive_mutex> lock(oled_lock);
	memset(oled_ram_known, 0, sizeof(oled_ram_known));
	oled_ram_since_ms = oled_now_ms();
	oled_ram_lost = false;
	oled_at_known = false;
}

OledBatch::OledBatch()
{
	oled_lock.lock();
	++oled_batch_depth;
}

OledBatch::~OledBatch()
{
	if (--oled_batch_depth == 0) oled_run_flush();
	oled_lock.unlock();
}

// The position of the next data byte, in the controller's pages and columns.
static void oled_cursor(int page, int column)
{
	std::lock_guard<std::recursive_mutex> lock(oled_lock);
	if (oled_now_ms() - oled_ram_since_ms >= OLED_RAM_REFRESH_MS) OLED_ForgetRAM();
	oled_want_known = page >= 0 && page < OLED_PAGES && column >= 0 && column < OLED_COLUMNS;
	oled_want_page = page;
	oled_want_column = column;
	if (!oled_want_known) {
		// Past the last column (a long line): the position goes out as it
		// always did, and where the controller puts the bytes is not
		// tracked; everything is sent again after this drawing.
		oled_send(OLED_CMD, 0xb0 + page);
		oled_send(OLED_CMD, 0x10 | ((column & 0xf0) >> 4));
		oled_send(OLED_CMD, column & 0x0f);
		oled_at_known = false;
		oled_ram_lost = true;
	}
}

static void oled_data(uint8_t byte)
{
	if (!oled_want_known) {
		// A data write without a position from oled_cursor, or past the
		// last column: as written.
		oled_send(OLED_DATA, byte);
		oled_at_known = false;
		return;
	}
	const int page = oled_want_page, column = oled_want_column;
	if (oled_ram_lost || !oled_ram_known[page][column] || oled_ram[page][column] != byte) {
		if (!oled_at_known || oled_at_page != page || oled_at_column != column) {
			oled_send(OLED_CMD, 0xb0 + page);
			oled_send(OLED_CMD, 0x10 | (column >> 4));
			oled_send(OLED_CMD, column & 0x0f);
		}
		oled_send(OLED_DATA, byte);
		oled_ram[page][column] = byte;
		oled_ram_known[page][column] = true;
		oled_at_known = column + 1 < OLED_COLUMNS;
		oled_at_page = page;
		oled_at_column = column + 1;
	}
	oled_want_column = (column + 1) % OLED_COLUMNS;
}

/* mode = OLED_CMD
 * 		= OLED_DATA         */
void oled_write_register(uint8_t mode, uint8_t data)
{
	if(!OLED_state || !oled_i2c) return;
	std::lock_guard<std::recursive_mutex> lock(oled_lock);
	if (mode == OLED_DATA) {
		oled_data(data);
		return;
	}
	// A command written directly may move the controller's cursor.
	oled_send(OLED_CMD, data);
	oled_at_known = false;
}

// Read a complete, bounded text file. Only DT strings may end with a NUL;
// a malformed or partially read override must never apply just its prefix.
static bool read_text(const char *path, std::string *result, bool device_tree = false)
{
	result->clear();
	FILE *fp = fopen(path, "r");
	if (!fp) return false;
	std::string text;
	char buf[256];
	size_t n;
	while ((n = fread(buf, 1, sizeof(buf), fp)) > 0) {
		if (text.size() + n > 4096) {
			fclose(fp);
			return false;
		}
		text.append(buf, n);
	}
	const bool failed = ferror(fp);
	fclose(fp);
	if (failed) return false;
	if (device_tree && !text.empty() && text.back() == '\0') text.pop_back();
	if (text.find('\0') != std::string::npos) return false;
	*result = text;
	return true;
}

static bool oled_answers(int bus, int address, void *)
{
	if (!oled_i2c) oled_i2c = new kvms::I2C(bus);
	return oled_i2c->answers(address);
}

int oled_exist(void)
{
	std::string revision, hw, override_text;
	read_text("/proc/device-tree/sipeed,board-revision", &revision, true);
	read_text("/etc/kvm/hw", &hw);
	const bool manual = read_text("/etc/kvm/oled", &override_text);
	const OledBoard board = oled_board(revision.empty() ? NULL : revision.c_str(), hw.c_str());
	kvm_hw_ver = board == OledBoard::Pcie ? 2 : board == OledBoard::Alpha ? 0 : 1;

	OledTarget target;
	if (!oled_detect(board, manual ? override_text.c_str() : NULL, oled_answers, NULL, &target)) {
		printf("OLED: none found\r\n");
		return 0;
	}
	oled_address = target.address;
	// The layout follows the board (or the override), not the address.
	kvm_hw_ver = target.narrow ? 2 : target.bus == 1 ? 0 : 1;
	printf("OLED: i2c-%d 0x%02x %s\r\n", target.bus, target.address, target.narrow ? "narrow" : "wide");
	return 1;
}

// 坐标设置
void OLED_Set_Pos(uint8_t x, uint8_t y) 
{ 
	OledBatch batch;
	// oled_write_register(OLED_CMD, 0xb0+y);
	// oled_write_register(OLED_CMD, ((x&0xf0)>>4)|0x10);
	// oled_write_register(OLED_CMD, (x&0x0f));
	if(kvm_hw_ver == 2){
		x += 32;
		y += 8;
	}
	oled_cursor(y, x);
}

// Persisted display-off also applies to startup and button/wifi wakeups.
bool OLED_IsDisabled(void)
{
    FILE *fp = fopen("/etc/kvm/oled_sleep", "r");
    if (!fp) return false;
    char text[16] = {};
    bool disabled = fgets(text, sizeof(text), fp) && strtol(text, nullptr, 10) == -1;
    fclose(fp);
    return disabled;
}

//开启OLED显示    
void OLED_Display_On()
{
	OledBatch batch;
    if (OLED_IsDisabled() && !OLED_IPWindowActive()) return;
	oled_write_register(OLED_CMD, 0X8D);
	oled_write_register(OLED_CMD, 0X14);
	oled_write_register(OLED_CMD, 0XAF);
}

//关闭OLED显示     
void OLED_Display_Off()
{
	OledBatch batch;
	oled_write_register(OLED_CMD, 0X8D);
	oled_write_register(OLED_CMD, 0X10);
	oled_write_register(OLED_CMD, 0XAE);
}

//清屏函数,清完屏,整个屏幕是黑色的!和没点亮一样!!!	  
void OLED_Clear(void)  
{  
	OledBatch batch;
	uint8_t i,n;		    
	for(i=0;i<8;i++)  
	{  
		oled_cursor(i, 0);
		for(n=0;n<128;n++)oled_write_register(OLED_DATA, 0x00); 
	} //更新显示
}

void OLED_Fill(void)  
{  
	OledBatch batch;
	uint8_t i,n;		    
	for(i=0;i<8;i++)  
	{  
		oled_cursor(i, 0);
		for(n=0;n<128;n++)oled_write_register(OLED_DATA, 0xFF); 
	} //更新显示
}

// The font index of a character: the 4-pixel font has no lower case (its
// table ends at ']'), so letters are drawn upper case there; anything the
// font lacks is a space. The index used to run past the table, for example
// for the lower-case letters of the Wi-Fi password the PCIe display shows.
static uint8_t oled_glyph(char chr, uint8_t sizey)
{
	const size_t glyphs = sizey == 4 ? sizeof(oled_asc2_0804) / sizeof(oled_asc2_0804[0])
		: sizey == 16 ? sizeof(oled_asc2_1608) / sizeof(oled_asc2_1608[0])
		: sizeof(oled_asc2_0806) / sizeof(oled_asc2_0806[0]);
	if (sizey == 4 && chr >= 'a' && chr <= 'z') chr = chr - 'a' + 'A';
	const unsigned index = (unsigned char)chr - ' ';
	return index < glyphs ? index : 0;
}

//在指定位置显示一个字符,包括部分字符
//x:0~127
//y:0~63				 
//sizey:选择字体 6x8  8x16
void OLED_ShowChar(uint8_t x,uint8_t y,char chr,uint8_t sizey)
{
	OledBatch batch;
	uint8_t c=0,sizex=sizey/2;
	uint16_t i=0,size1;
	if(sizey==8)size1=6;
	else if(sizey==4)size1=4;
	else size1=(sizey/8+((sizey%8)?1:0))*(sizey/2);
	c=oled_glyph(chr, sizey);
	OLED_Set_Pos(x, y);
	for(i=0; i<size1; i++)
	{
		if(i%sizex==0&&sizey==16) OLED_Set_Pos(x, y++);
		if(sizey==8) oled_write_register(OLED_DATA, oled_asc2_0806[c][i]); //6X8字号
		else if(sizey==16) oled_write_register(OLED_DATA, oled_asc2_1608[c][i]);//8x16字号
		else if(sizey==4) oled_write_register(OLED_DATA, oled_asc2_0804[c][i]);//8x4字号
		else return;
	}
}
void OLED_ShowCharTurn(uint8_t x,uint8_t y,char chr,uint8_t sizey)
{
	OledBatch batch;
	uint8_t c=0,sizex=sizey/2;
	uint16_t i=0,size1;
	if(sizey==8)size1=6;
	else size1=(sizey/8+((sizey%8)?1:0))*(sizey/2);
	c=oled_glyph(chr, sizey);
	OLED_Set_Pos(x, y);
	for(i=0; i<size1; i++)
	{
		if(i%sizex==0&&sizey!=8) OLED_Set_Pos(x, y++);
		if(sizey==8) oled_write_register(OLED_DATA, ~oled_asc2_0806[c][i]); //6X8字号
		else if(sizey==16) oled_write_register(OLED_DATA, ~oled_asc2_1608[c][i]);//8x16字号
		else return;
	}
}

//显示一个字符号串
void OLED_ShowString(uint8_t x, uint8_t y, const char *chr, uint8_t sizey)
{
	OledBatch batch;
	uint8_t j=0;
	while (chr[j]!='\0')
	{		
		OLED_ShowChar(x, y, chr[j++], sizey);
		if(sizey==8)x+=6;
		else if(sizey==4)x+=4;
		else x+=sizey/2;
	}
}
//反显一个字符号串
void OLED_ShowStringTurn(uint8_t x, uint8_t y, const char *chr, uint8_t sizey)
{
	OledBatch batch;
	uint8_t j=0;
	while (chr[j]!='\0')
	{		
		OLED_ShowCharTurn(x, y, chr[j++], sizey);
		if(sizey==8)x+=6;
		else x+=sizey/2;
	}
}

//反显函数
void OLED_ColorTurn(uint8_t i)
{
	OledBatch batch;
	if(i==0)
	{
		oled_write_register(OLED_CMD, 0xA6); //正常显示
	}
	if(i==1)
	{
		oled_write_register(OLED_CMD, 0xA7);//反色显示
	}
}

//屏幕旋转180度
void OLED_DisplayTurn(uint8_t i)
{
	OledBatch batch;
	if(i==0)
	{
		oled_write_register(OLED_CMD, 0xC8);//正常显示
		oled_write_register(OLED_CMD, 0xA1);
	}
	if(i==1)
	{
		oled_write_register(OLED_CMD, 0xC0);//反转显示
		oled_write_register(OLED_CMD, 0xA0);
	}
}

//初始化				    
void OLED_Init(void)
{
	OledBatch batch;
	// pinmap::set_pin_function("A19", "GPIOP19");
    
	// OLED_RES_Clr();
  	// delay_ms(200);
	// OLED_RES_Set();
	
	OLED_ForgetRAM();
	oled_write_register(OLED_CMD, 0xAE);//--turn off oled_alpha panel
	oled_write_register(OLED_CMD, 0x00);//---set low column address
	oled_write_register(OLED_CMD, 0x10);//---set high column address
	oled_write_register(OLED_CMD, 0x40);//--set start line address  Set Mapping RAM Display Start Line (0x00~0x3F)
	oled_write_register(OLED_CMD, 0x81);//--set contrast control register
	oled_write_register(OLED_CMD, 0xCF);// Set SEG Output Current Brightness
	oled_write_register(OLED_CMD, 0xA1);//--Set SEG/Column Mapping     0xa0左右反置 0xa1正常
	oled_write_register(OLED_CMD, 0xC8);//Set COM/Row Scan Direction   0xc0上下反置 0xc8正常
	oled_write_register(OLED_CMD, 0xA6);//--set normal display
	oled_write_register(OLED_CMD, 0xA8);//--set multiplex ratio(1 to 64)
	oled_write_register(OLED_CMD, 0x3f);//--1/64 duty
	oled_write_register(OLED_CMD, 0xD3);//-set display offset	Shift Mapping RAM Counter (0x00~0x3F)
	oled_write_register(OLED_CMD, 0x00);//-not offset
	oled_write_register(OLED_CMD, 0xd5);//--set display clock divide ratio/oscillator frequency
	oled_write_register(OLED_CMD, 0x80);//--set divide ratio, Set Clock as 100 Frames/Sec
	oled_write_register(OLED_CMD, 0xD9);//--set pre-charge period
	oled_write_register(OLED_CMD, 0xF1);//Set Pre-Charge as 15 Clocks & Discharge as 1 Clock
	oled_write_register(OLED_CMD, 0xDA);//--set com pins hardware configuration
	oled_write_register(OLED_CMD, 0x12);//
	oled_write_register(OLED_CMD, 0xDB);//--set vcomh
	oled_write_register(OLED_CMD, 0x40);//Set VCOM Deselect Level
	oled_write_register(OLED_CMD, 0x20);//-Set Page Addressing Mode (0x00/0x01/0x02)
	oled_write_register(OLED_CMD, 0x02);//
	oled_write_register(OLED_CMD, 0x8D);//--set Charge Pump enable/disable
	oled_write_register(OLED_CMD, 0x14);//--set(0x10) disable
	oled_write_register(OLED_CMD, 0xA4);// Disable Entire Display On (0xa4/0xa5)
	oled_write_register(OLED_CMD, 0xA6);// Disable Inverse Display On (0xa6/a7) 
	OLED_Clear();
	if (OLED_IsDisabled() && !OLED_IPWindowActive()) OLED_Display_Off();
    else OLED_Display_On(); // respects persistent display-off before first frame
}

void OLED_ShowString_AlignRight(uint8_t x_end, uint8_t y, const char *chr, uint8_t size)
{
	OledBatch batch;
	uint8_t j = 0;
	uint8_t x = x_end;
	while(chr[j] != '\0'){
		j ++;
	}
	while (j)
	{	
		if(size == 8) x -= 6;
		else x -= 4;
		OLED_ShowChar(x, y, chr[j-1], size);
		j --;
	}
}

void OLED_Revolve(void)
{
	OledBatch batch;
	oled_write_register(OLED_CMD, 0xA0);
	oled_write_register(OLED_CMD, 0xC0);
}

// A frame of width columns and pages pages; with the last one shown, only
// the runs of bytes that changed are written. The narrow panel's window is
// not in the RAM copy (oled_cursor), so this is what keeps its writes small.
void OLED_ShowFrame(const uint8_t *frame, const uint8_t *previous, int width, int pages)
{
	OledBatch batch;
	for (int page = 0; page < pages; page++) {
		const uint8_t *row = frame + page * width;
		const uint8_t *before = previous ? previous + page * width : NULL;
		for (int x = 0; x < width;) {
			if (before && row[x] == before[x]) {
				x++;
				continue;
			}
			OLED_Set_Pos(x, page);
			while (x < width && (!before || row[x] != before[x])) oled_write_register(OLED_DATA, row[x++]);
		}
	}
}

void OLED_ShowIMG(uint8_t x,uint8_t y,const char *chr,uint8_t width,uint8_t height)
{
	OledBatch batch;
	uint16_t size = width * height;
	for(int count = 0; count < size; count++){
		if(count%width == 0){
			OLED_Set_Pos(x, y+(count/width));
		}
		oled_write_register(OLED_DATA, chr[count]);
	}
}
