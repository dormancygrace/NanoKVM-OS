#ifndef OLED_LAYOUT_H_
#define OLED_LAYOUT_H_

// The OLED main page as a layout: text, lines, rectangles and images drawn
// into a frame. Parsing and drawing touch no hardware and no files.
//
// The layout is a text file the server writes from the web editor's layout
// (one line per item; a field list ends at the line's end, so text and image
// data, the last fields, are never cut):
//
//   nanokvm-oled 1 wide|narrow
//   rotation 0|90|180|270
//   burnin none|shift|roam WIDTH HEIGHT
//   pages N SECONDS
//   text X Y FONT ALIGN WIDTH COLOR WHEN TEXT
//   line X0 Y0 X1 Y1 COLOR WHEN
//   rect X Y WIDTH HEIGHT FILL COLOR WHEN
//   image X Y WIDTH HEIGHT COLOR WHEN HEX
//
// Coordinates are pixels of the panel as rotated. FONT is 4 (4x8, capitals
// only), 8 (6x8) or 16 (8x16). A text WIDTH above 0 is a box the text is
// aligned in (left, center or right) and cut to. COLOR 1 lights pixels, 0
// clears them. WHEN is "-" (always) or up to four conditions joined by ","
// that must all hold, each optionally after "!": eth, wifi, hdmi, hid, stream,
// wifi_module (the board has Wi-Fi, connected or not), atx (the ATX switch
// is on) and page1 to page4. With "pages", N pages (up to 4) are shown in
// turn for SECONDS each; an item with a page condition is on that page only.
// Image rows are whole bytes, most significant bit first. Text may hold
// {name} or {name:N} (right-aligned in N characters, -N left-aligned, N up
// to 32): ip eth_ip wifi_ip hostname codec width height fps viewers uptime
// time date. Burn-in protection moves everything every minute: "shift" by a
// pixel, "roam" a WIDTH x HEIGHT block across the panel in whole pages.

#include <stdint.h>
#include <string>
#include <utility>
#include <vector>

// A text's template, a {name:N} width (the widest panel holds 32 characters)
// and the text once expanded.
enum { OLED_LAYOUT_MAX_ITEMS = 128, OLED_LAYOUT_MAX_TEXT = 128, OLED_LAYOUT_MAX_PAD = 32,
       OLED_LAYOUT_MAX_EXPANDED = 256, OLED_LAYOUT_MAX_CONDITIONS = 4 };
enum { OLED_FRAME_BYTES = 128 * 64 / 8 };

struct OledItem {
    char kind = 0; // 't'ext, 'l'ine, 'r'ect, 'i'mage
    int x = 0, y = 0, x2 = 0, y2 = 0, width = 0, height = 0;
    int font = 8;
    char align = 'l';
    bool fill = false, color = true;
    int when[OLED_LAYOUT_MAX_CONDITIONS] = {};  // OledCondition each, 0 = none
    bool when_not[OLED_LAYOUT_MAX_CONDITIONS] = {};
    std::string text;      // text, or the image's bits
};

struct OledLayout {
    bool narrow = false;
    int rotation = 0;
    char burnin = 'n';     // 'n'one, 's'hift, 'r'oam
    int roam_width = 0, roam_height = 0;
    int pages = 1, page_seconds = 5;
    std::vector<OledItem> items;
};

enum OledCondition { OLED_WHEN_ETH = 1, OLED_WHEN_WIFI, OLED_WHEN_HDMI, OLED_WHEN_HID, OLED_WHEN_STREAM,
                     OLED_WHEN_WIFI_MODULE, OLED_WHEN_ATX, OLED_WHEN_PAGE1, OLED_WHEN_PAGE2,
                     OLED_WHEN_PAGE3, OLED_WHEN_PAGE4 };

// What the layout's text and conditions read.
struct OledValues {
    std::string ip, eth_ip, wifi_ip, hostname, codec, uptime, time, date;
    int width = 0, height = 0, fps = 0, viewers = 0;
    int page = 0;  // the page shown, from 0 (the caller counts the time)
    bool eth = false, wifi = false, hdmi = false, hid = false, stream = false, wifi_module = false,
         atx = false;
};

// False with a reason when the text is not a layout.
bool oled_layout_parse(const char *text, OledLayout *layout, std::string *error);

// The built-in layout of each panel.
const char *oled_layout_default(bool narrow);

// The boot logo of each panel, as a layout.
const char *oled_layout_splash(bool narrow);

// A layout that parses and is for this panel.
bool oled_layout_usable(const char *text, bool narrow, OledLayout *layout, std::string *error);

// The first usable of texts (NULL: absent or unreadable), in order; -1 and
// the built-in layout when none is. errors gets the index and reason of each
// refused text.
int oled_layout_pick(const char *const texts[], int count, bool narrow, OledLayout *layout,
                     std::vector<std::pair<int, std::string>> *errors);

// Draws into frame (128x64 or 64x32 pixels, display pages of one byte per
// column, the low bit at the top). step counts the burn-in moves; it is the
// caller's, so a layout read again keeps moving where it was.
void oled_layout_render(const OledLayout &layout, const OledValues &values, unsigned step,
                        uint8_t frame[OLED_FRAME_BYTES]);

// {name} replaced by its value.
std::string oled_layout_expand(const std::string &text, const OledValues &values);

#endif // OLED_LAYOUT_H_
