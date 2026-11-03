#include "oled_layout.h"
#include "oled_font.h"

#include <algorithm>
#include <ctype.h>
#include <stdlib.h>
#include <string.h>

namespace {

// The built-in layouts. Cube (wide): two screens a minute each, the second
// with the header at the bottom, so no line, mark or label stays lit in one
// place. PCIe (narrow): two pages, five seconds each, the mark and the
// addresses on one, the video and the links on the other. A link box is lit
// while the link is up; the Wi-Fi row is there only with a Wi-Fi module, and
// then the link boxes are small, in place of the uptime.
const char wide_default[] =
    "nanokvm-oled 1 wide\n"
    "rotation 0\n"
    "burnin shift\n"
    "pages 2 60\n"
    "image 0 0 24 24 1 page1 00000000c00001e00003e00007e3000fc7801f87c03f07e07e03f0fc01f8f83cfcfc3c7e7e3c3f3f3c1f1f803f0fc07e07e0fc03e1f801e3f000c7e00007c0000780000300000000\n"
    "text 28 2 8 left 0 1 page1 {hostname}\n"
    "text 28 13 4 left 0 1 page1,!wifi_module UP {uptime}\n"
    "rect 28 11 19 11 1 1 page1,wifi_module,hdmi\n"
    "text 30 13 4 left 0 0 page1,wifi_module,hdmi HDMI\n"
    "rect 28 11 19 11 0 1 page1,wifi_module,!hdmi\n"
    "text 30 13 4 left 0 1 page1,wifi_module,!hdmi HDMI\n"
    "rect 50 11 15 11 1 1 page1,wifi_module,hid\n"
    "text 52 13 4 left 0 0 page1,wifi_module,hid USB\n"
    "rect 50 11 15 11 0 1 page1,wifi_module,!hid\n"
    "text 52 13 4 left 0 1 page1,wifi_module,!hid USB\n"
    "rect 68 11 15 11 1 1 page1,wifi_module,atx\n"
    "text 70 13 4 left 0 0 page1,wifi_module,atx ATX\n"
    "line 0 26 126 26 1 page1\n"
    "image 100 13 11 7 1 page1 0e0031804e409f204e4031800e00\n"
    "text 113 13 8 left 0 1 page1 {viewers}\n"
    "rect 0 29 9 11 1 1 page1,eth\n"
    "text 1 31 8 left 0 0 page1,eth E\n"
    "rect 0 29 9 11 0 1 page1,!eth\n"
    "text 1 31 8 left 0 1 page1,!eth E\n"
    "text 13 31 8 left 0 1 page1,eth {eth_ip}\n"
    "text 13 31 8 left 0 1 page1,!eth not connected\n"
    "rect 0 41 9 11 1 1 page1,wifi_module,wifi\n"
    "text 1 43 8 left 0 0 page1,wifi_module,wifi W\n"
    "rect 0 41 9 11 0 1 page1,wifi_module,!wifi\n"
    "text 1 43 8 left 0 1 page1,wifi_module,!wifi W\n"
    "text 13 43 8 left 0 1 page1,wifi {wifi_ip}\n"
    "text 13 43 8 left 0 1 page1,wifi_module,!wifi not connected\n"
    "rect 25 41 27 11 1 1 page1,!wifi_module,atx,hdmi\n"
    "text 26 43 8 left 0 0 page1,!wifi_module,atx,hdmi HDMI\n"
    "rect 25 41 27 11 0 1 page1,!wifi_module,atx,!hdmi\n"
    "text 26 43 8 left 0 1 page1,!wifi_module,atx,!hdmi HDMI\n"
    "rect 56 41 21 11 1 1 page1,!wifi_module,atx,hid\n"
    "text 57 43 8 left 0 0 page1,!wifi_module,atx,hid USB\n"
    "rect 56 41 21 11 0 1 page1,!wifi_module,atx,!hid\n"
    "text 57 43 8 left 0 1 page1,!wifi_module,atx,!hid USB\n"
    "rect 81 41 21 11 1 1 page1,!wifi_module,atx\n"
    "text 82 43 8 left 0 0 page1,!wifi_module,atx ATX\n"
    "rect 37 41 27 11 1 1 page1,!wifi_module,!atx,hdmi\n"
    "text 38 43 8 left 0 0 page1,!wifi_module,!atx,hdmi HDMI\n"
    "rect 37 41 27 11 0 1 page1,!wifi_module,!atx,!hdmi\n"
    "text 38 43 8 left 0 1 page1,!wifi_module,!atx,!hdmi HDMI\n"
    "rect 69 41 21 11 1 1 page1,!wifi_module,!atx,hid\n"
    "text 70 43 8 left 0 0 page1,!wifi_module,!atx,hid USB\n"
    "rect 69 41 21 11 0 1 page1,!wifi_module,!atx,!hid\n"
    "text 70 43 8 left 0 1 page1,!wifi_module,!atx,!hid USB\n"
    "line 0 53 126 53 1 page1\n"
    "text 0 56 8 left 0 1 page1,stream {codec} {width}x{height}\n"
    "text 66 56 8 right 60 1 page1,stream {fps} FPS\n"
    "text 0 56 8 left 0 1 page1,!stream no video\n"
    "text 0 1 8 left 0 1 page2,stream {codec} {width}x{height}\n"
    "text 66 1 8 right 60 1 page2,stream {fps} FPS\n"
    "text 0 1 8 left 0 1 page2,!stream no video\n"
    "line 0 10 126 10 1 page2\n"
    "rect 0 13 9 11 1 1 page2,eth\n"
    "text 1 15 8 left 0 0 page2,eth E\n"
    "rect 0 13 9 11 0 1 page2,!eth\n"
    "text 1 15 8 left 0 1 page2,!eth E\n"
    "text 13 15 8 left 0 1 page2,eth {eth_ip}\n"
    "text 13 15 8 left 0 1 page2,!eth not connected\n"
    "rect 0 25 9 11 1 1 page2,wifi_module,wifi\n"
    "text 1 27 8 left 0 0 page2,wifi_module,wifi W\n"
    "rect 0 25 9 11 0 1 page2,wifi_module,!wifi\n"
    "text 1 27 8 left 0 1 page2,wifi_module,!wifi W\n"
    "text 13 27 8 left 0 1 page2,wifi {wifi_ip}\n"
    "text 13 27 8 left 0 1 page2,wifi_module,!wifi not connected\n"
    "rect 25 25 27 11 1 1 page2,!wifi_module,atx,hdmi\n"
    "text 26 27 8 left 0 0 page2,!wifi_module,atx,hdmi HDMI\n"
    "rect 25 25 27 11 0 1 page2,!wifi_module,atx,!hdmi\n"
    "text 26 27 8 left 0 1 page2,!wifi_module,atx,!hdmi HDMI\n"
    "rect 56 25 21 11 1 1 page2,!wifi_module,atx,hid\n"
    "text 57 27 8 left 0 0 page2,!wifi_module,atx,hid USB\n"
    "rect 56 25 21 11 0 1 page2,!wifi_module,atx,!hid\n"
    "text 57 27 8 left 0 1 page2,!wifi_module,atx,!hid USB\n"
    "rect 81 25 21 11 1 1 page2,!wifi_module,atx\n"
    "text 82 27 8 left 0 0 page2,!wifi_module,atx ATX\n"
    "rect 37 25 27 11 1 1 page2,!wifi_module,!atx,hdmi\n"
    "text 38 27 8 left 0 0 page2,!wifi_module,!atx,hdmi HDMI\n"
    "rect 37 25 27 11 0 1 page2,!wifi_module,!atx,!hdmi\n"
    "text 38 27 8 left 0 1 page2,!wifi_module,!atx,!hdmi HDMI\n"
    "rect 69 25 21 11 1 1 page2,!wifi_module,!atx,hid\n"
    "text 70 27 8 left 0 0 page2,!wifi_module,!atx,hid USB\n"
    "rect 69 25 21 11 0 1 page2,!wifi_module,!atx,!hid\n"
    "text 70 27 8 left 0 1 page2,!wifi_module,!atx,!hid USB\n"
    "line 0 38 126 38 1 page2\n"
    "image 103 40 24 24 1 page2 00000000c00001e00003e00007e3000fc7801f87c03f07e07e03f0fc01f8f83cfcfc3c7e7e3c3f3f3c1f1f803f0fc07e07e0fc03e1f801e3f000c7e00007c0000780000300000000\n"
    "text 0 42 8 left 0 1 page2 {hostname}\n"
    "text 0 53 4 left 0 1 page2,!wifi_module UP {uptime}\n"
    "rect 0 51 19 11 1 1 page2,wifi_module,hdmi\n"
    "text 2 53 4 left 0 0 page2,wifi_module,hdmi HDMI\n"
    "rect 0 51 19 11 0 1 page2,wifi_module,!hdmi\n"
    "text 2 53 4 left 0 1 page2,wifi_module,!hdmi HDMI\n"
    "rect 22 51 15 11 1 1 page2,wifi_module,hid\n"
    "text 24 53 4 left 0 0 page2,wifi_module,hid USB\n"
    "rect 22 51 15 11 0 1 page2,wifi_module,!hid\n"
    "text 24 53 4 left 0 1 page2,wifi_module,!hid USB\n"
    "rect 40 51 15 11 1 1 page2,wifi_module,atx\n"
    "text 42 53 4 left 0 0 page2,wifi_module,atx ATX\n"
    "image 59 53 11 7 1 page2 0e0031804e409f204e4031800e00\n"
    "text 72 53 8 left 0 1 page2 {viewers}\n";
const char narrow_default[] =
    "nanokvm-oled 1 narrow\n"
    "rotation 0\n"
    "burnin shift\n"
    "pages 2 5\n"
    "image 0 0 16 16 1 page1 0000000007000e001ce038707038e39c738e3b871c0e0e1c0738007000e00000\n"
    "text 19 0 4 left 45 1 page1 {hostname}\n"
    "text 19 8 4 left 45 1 page1 UP {uptime}\n"
    "text 0 16 4 left 0 1 page1 E\n"
    "text 7 16 4 left 57 1 page1,eth {eth_ip}\n"
    "text 7 16 4 left 57 1 page1,!eth --\n"
    "text 0 24 4 left 0 1 page1,wifi_module W\n"
    "text 7 24 4 left 57 1 page1,wifi {wifi_ip}\n"
    "text 7 24 4 left 57 1 page1,wifi_module,!wifi --\n"
    "text 0 0 4 left 0 1 page2,stream {codec} {width}X{height}\n"
    "text 0 0 4 left 0 1 page2,!stream NO VIDEO\n"
    "text 0 8 4 left 0 1 page2,stream {fps} FPS\n"
    "image 44 9 9 5 1 page2 3e0049009c8049003e00\n"
    "text 55 8 4 left 0 1 page2 {viewers}\n"
    "text 0 24 4 left 0 1 page2,eth ETH\n"
    "text 15 24 4 left 0 1 page2,hdmi HDMI\n"
    "text 34 24 4 left 0 1 page2,hid USB\n"
    "text 50 24 4 left 0 1 page2,atx ATX\n";
// The boot logo, which U-Boot draws first (scripts/gen-oled-boot-logo.py
// writes the same pixels), kept until the system is up.
const char wide_splash[] =
    "nanokvm-oled 1 wide\n"
    "rotation 0\n"
    "burnin none\n"
    "image 44 2 40 40 1 - 000000000000000000000003c000000007c00000000fe00000001fe00000003fc00000007fc3c00000ff83e00001ff07f00003fe07f80007fc03fc000ff803fe001ff001ff003fe000ff807fc0007fc0ff807e3fe0ff00ff1ff0ff80ff0ff87fc0ff07fc3fe0ff03fe1ff0ff01ff0ff8ff00ff07fc7e01ff03fe0003fe01ff0007fc00ff800ff8007fc01ff0003fc03fe0001fe07fc0000fe0ff800007c1ff000003c3fe00000003fc00000007f800000007f000000003e000000003c00000000000000000000000\n"
    "text 33 48 8 left 0 1 - NanoKVM\n"
    "rect 79 46 15 11 1 1 -\n"
    "text 80 48 8 left 0 0 - OS\n";
const char narrow_splash[] =
    "nanokvm-oled 1 narrow\n"
    "rotation 0\n"
    "burnin none\n"
    "image 16 0 32 32 1 - 0000000000080000003e0000007e000000fe000001fe100003fc7c0007f87e000ff07f001fe07f803fc03fc07f801fe0ff000ff0fe07e7f8fe07e3fcff07e1fe7f87e0ff3fc7e07f1fe7e07f0ff000ff07f801fe03fc03fc01fe07f800fe0ff0007e1fe0003e3fc000087f8000007f0000007e0000007c000000100000000000\n";
// Splits off the next space-separated field; rest keeps the remainder.
bool field(const char *&rest, std::string *value)
{
    if (*rest == 0) return false;
    const char *space = strchr(rest, ' ');
    const size_t length = space ? size_t(space - rest) : strlen(rest);
    if (length == 0) return false;
    value->assign(rest, length);
    rest += length + (space ? 1 : 0);
    return true;
}

bool number(const char *&rest, int low, int high, int *value)
{
    std::string text;
    if (!field(rest, &text)) return false;
    char *end = nullptr;
    const long parsed = strtol(text.c_str(), &end, 10);
    if (*end || parsed < low || parsed > high) return false;
    *value = int(parsed);
    return true;
}

bool flag(const char *&rest, bool *value)
{
    int parsed = 0;
    if (!number(rest, 0, 1, &parsed)) return false;
    *value = parsed;
    return true;
}

bool condition(const char *&rest, OledItem *item)
{
    std::string text;
    if (!field(rest, &text)) return false;
    if (text == "-") return true;
    static const char *const names[] = {"eth", "wifi", "hdmi", "hid", "stream", "wifi_module", "atx",
                                         "page1", "page2", "page3", "page4"};
    size_t start = 0;
    for (int n = 0; n < OLED_LAYOUT_MAX_CONDITIONS; n++) {
        const size_t end = std::min(text.find(',', start), text.size());
        item->when_not[n] = text[start] == '!';
        const std::string name = text.substr(start + item->when_not[n], end - start - item->when_not[n]);
        for (int i = 0; i < 11 && !item->when[n]; i++)
            if (name == names[i]) item->when[n] = i + 1;
        if (!item->when[n]) return false;
        if (end == text.size()) return true;
        start = end + 1;
    }
    return false;
}

// {name} or {name:N} starting at text[i]; pad_valid when N is -32..32 written
// plainly (an optional minus and at most two digits).
struct Placeholder {
    size_t close = 0;
    std::string name;
    bool has_pad = false, pad_valid = false;
    long pad = 0;
};

bool find_placeholder(const std::string &text, size_t i, Placeholder *placeholder)
{
    if (text[i] != '{') return false;
    const size_t close = text.find('}', i);
    if (close == std::string::npos) return false;
    placeholder->close = close;
    placeholder->name = text.substr(i + 1, close - i - 1);
    const size_t colon = placeholder->name.find(':');
    if (colon == std::string::npos) return true;
    const std::string number = placeholder->name.substr(colon + 1);
    placeholder->name.resize(colon);
    placeholder->has_pad = true;
    const size_t digits = number.size() - (number[0] == '-' ? 1 : 0);
    if (number.empty() || digits == 0 || digits > 2) return true;
    for (size_t k = number.size() - digits; k < number.size(); k++)
        if (!isdigit((unsigned char)number[k])) return true;
    placeholder->pad = strtol(number.c_str(), nullptr, 10);
    placeholder->pad_valid = placeholder->pad >= -OLED_LAYOUT_MAX_PAD && placeholder->pad <= OLED_LAYOUT_MAX_PAD;
    return true;
}

bool variable_value(const std::string &name, const OledValues &values, std::string *value)
{
    if (name == "ip") *value = values.ip;
    else if (name == "eth_ip") *value = values.eth_ip;
    else if (name == "wifi_ip") *value = values.wifi_ip;
    else if (name == "hostname") *value = values.hostname;
    else if (name == "codec") *value = values.codec;
    else if (name == "uptime") *value = values.uptime;
    else if (name == "time") *value = values.time;
    else if (name == "date") *value = values.date;
    else if (name == "width") *value = std::to_string(values.width);
    else if (name == "height") *value = std::to_string(values.height);
    else if (name == "fps") *value = std::to_string(values.fps);
    else if (name == "viewers") *value = std::to_string(values.viewers);
    else return false;
    return true;
}

// Every variable's width can be drawn; scanned as oled_layout_expand scans.
bool placeholders_valid(const std::string &text)
{
    const OledValues none;
    for (size_t i = 0; i < text.size(); i++) {
        Placeholder placeholder;
        std::string value;
        if (!find_placeholder(text, i, &placeholder) || !variable_value(placeholder.name, none, &value)) continue;
        if (placeholder.has_pad && !placeholder.pad_valid) return false;
        i = placeholder.close;
    }
    return true;
}

// Coordinates may start off the panel, a little.
const int LOW = -128, HIGH = 255;

bool parse_item(const std::string &kind, const char *rest, OledItem *item)
{
    std::string word;
    if (kind == "text") {
        item->kind = 't';
        if (!number(rest, LOW, HIGH, &item->x) || !number(rest, LOW, HIGH, &item->y) ||
            !number(rest, 4, 16, &item->font) || (item->font != 4 && item->font != 8 && item->font != 16) ||
            !field(rest, &word) || (word != "left" && word != "center" && word != "right") ||
            !number(rest, 0, 128, &item->width) || !flag(rest, &item->color) || !condition(rest, item))
            return false;
        item->align = word[0];
        item->text = rest;
        return item->text.size() <= OLED_LAYOUT_MAX_TEXT && placeholders_valid(item->text);
    }
    if (kind == "line") {
        item->kind = 'l';
        return number(rest, LOW, HIGH, &item->x) && number(rest, LOW, HIGH, &item->y) &&
            number(rest, LOW, HIGH, &item->x2) && number(rest, LOW, HIGH, &item->y2) &&
            flag(rest, &item->color) && condition(rest, item) && *rest == 0;
    }
    if (kind == "rect") {
        item->kind = 'r';
        return number(rest, LOW, HIGH, &item->x) && number(rest, LOW, HIGH, &item->y) &&
            number(rest, 1, 128, &item->width) && number(rest, 1, 128, &item->height) &&
            flag(rest, &item->fill) && flag(rest, &item->color) && condition(rest, item) && *rest == 0;
    }
    if (kind == "image") {
        item->kind = 'i';
        if (!number(rest, LOW, HIGH, &item->x) || !number(rest, LOW, HIGH, &item->y) ||
            !number(rest, 1, 128, &item->width) || !number(rest, 1, 128, &item->height) ||
            !flag(rest, &item->color) || !condition(rest, item) || !field(rest, &word) || *rest)
            return false;
        if (word.size() != size_t((item->width + 7) / 8 * item->height * 2)) return false;
        item->text.clear();
        for (size_t i = 0; i < word.size(); i += 2) {
            char *end = nullptr;
            const std::string pair = word.substr(i, 2);
            const long byte = strtol(pair.c_str(), &end, 16);
            if (*end || !isxdigit((unsigned char)pair[0])) return false;
            item->text.push_back(char(byte));
        }
        return true;
    }
    return false;
}

// One frame: rotation and the burn-in move applied to every pixel.
struct Canvas {
    uint8_t *frame;
    int width, height; // the panel's
    int rotation, dx, dy;

    int logical_width() const { return rotation % 180 ? height : width; }
    int logical_height() const { return rotation % 180 ? width : height; }

    void set(int x, int y, bool on) const
    {
        x += dx;
        y += dy;
        if (x < 0 || y < 0 || x >= logical_width() || y >= logical_height()) return;
        int px = x, py = y;
        if (rotation == 90) { px = width - 1 - y; py = x; }
        else if (rotation == 180) { px = width - 1 - x; py = height - 1 - y; }
        else if (rotation == 270) { px = y; py = height - 1 - x; }
        uint8_t &byte = frame[(py / 8) * width + px];
        const uint8_t bit = uint8_t(1u << (py % 8));
        byte = on ? byte | bit : byte & ~bit;
    }
};

int advance(int font) { return font == 4 ? 4 : font == 16 ? 8 : 6; }

// A glyph column, as in oled_ctrl's OLED_ShowChar: the 4-pixel font has no
// lower case, and a character a font lacks is a space.
uint16_t glyph_column(char chr, int font, int column)
{
    if (font == 4 && chr >= 'a' && chr <= 'z') chr = chr - 'a' + 'A';
    unsigned index = (unsigned char)chr - ' ';
    if (font == 4) {
        if (index >= sizeof(oled_asc2_0804) / sizeof(oled_asc2_0804[0])) index = 0;
        return oled_asc2_0804[index][column];
    }
    if (font == 16) {
        if (index >= sizeof(oled_asc2_1608) / sizeof(oled_asc2_1608[0])) index = 0;
        return uint16_t(oled_asc2_1608[index][column] | oled_asc2_1608[index][8 + column] << 8);
    }
    if (index >= sizeof(oled_asc2_0806) / sizeof(oled_asc2_0806[0])) index = 0;
    return oled_asc2_0806[index][column];
}

void draw_text(const Canvas &canvas, const OledItem &item, const std::string &text)
{
    const int step = advance(item.font);
    const int rows = item.font == 16 ? 16 : 8;
    int x = item.x;
    const int clip_left = item.width ? item.x : LOW * 2;
    const int clip_right = item.width ? item.x + item.width : HIGH * 4;
    if (item.width) {
        const int spare = item.width - int(text.size()) * step;
        if (item.align == 'r') x += spare;
        else if (item.align == 'c') x += spare / 2;
    }
    for (char chr : text) {
        for (int column = 0; column < step; column++) {
            const int px = x + column;
            if (px < clip_left || px >= clip_right) continue;
            const uint16_t bits = glyph_column(chr, item.font, column);
            for (int row = 0; row < rows; row++)
                if (bits >> row & 1) canvas.set(px, item.y + row, item.color);
        }
        x += step;
    }
}

void draw_line(const Canvas &canvas, const OledItem &item)
{
    int x = item.x, y = item.y;
    const int dx = abs(item.x2 - x), sx = x < item.x2 ? 1 : -1;
    const int dy = -abs(item.y2 - y), sy = y < item.y2 ? 1 : -1;
    int error = dx + dy;
    for (;;) {
        canvas.set(x, y, item.color);
        if (x == item.x2 && y == item.y2) return;
        const int twice = 2 * error;
        if (twice >= dy) { error += dy; x += sx; }
        if (twice <= dx) { error += dx; y += sy; }
    }
}

void draw_rect(const Canvas &canvas, const OledItem &item)
{
    for (int row = 0; row < item.height; row++)
        for (int column = 0; column < item.width; column++)
            if (item.fill || row == 0 || column == 0 || row == item.height - 1 || column == item.width - 1)
                canvas.set(item.x + column, item.y + row, item.color);
}

void draw_image(const Canvas &canvas, const OledItem &item)
{
    const int stride = (item.width + 7) / 8;
    for (int row = 0; row < item.height; row++)
        for (int column = 0; column < item.width; column++)
            if ((unsigned char)item.text[row * stride + column / 8] >> (7 - column % 8) & 1)
                canvas.set(item.x + column, item.y + row, item.color);
}

bool condition_holds(const OledItem &item, const OledValues &values)
{
    const bool state[] = {values.eth, values.wifi, values.hdmi, values.hid, values.stream, values.wifi_module,
                          values.atx, values.page == 0, values.page == 1, values.page == 2, values.page == 3};
    for (int n = 0; n < OLED_LAYOUT_MAX_CONDITIONS && item.when[n]; n++)
        if (state[item.when[n] - 1] == item.when_not[n]) return false;
    return true;
}

} // namespace

const char *oled_layout_default(bool narrow)
{
    return narrow ? narrow_default : wide_default;
}

const char *oled_layout_splash(bool narrow)
{
    return narrow ? narrow_splash : wide_splash;
}

bool oled_layout_parse(const char *text, OledLayout *layout, std::string *error)
{
    OledLayout parsed;
    const char *cursor = text;
    int line_number = 0;
    bool header = false;
    while (*cursor) {
        const char *end = strchr(cursor, '\n');
        std::string line(cursor, end ? size_t(end - cursor) : strlen(cursor));
        cursor = end ? end + 1 : cursor + line.size();
        ++line_number;
        if (!line.empty() && line.back() == '\r') line.pop_back();
        if (line.empty() || line[0] == '#') continue;
        const char *rest = line.c_str();
        std::string kind;
        field(rest, &kind);
        bool ok = false;
        if (!header) {
            std::string version, panel;
            ok = kind == "nanokvm-oled" && field(rest, &version) && version == "1" &&
                field(rest, &panel) && (panel == "wide" || panel == "narrow") && !*rest;
            parsed.narrow = panel == "narrow";
            header = true;
        } else if (kind == "rotation") {
            ok = number(rest, 0, 270, &parsed.rotation) && parsed.rotation % 90 == 0 && !*rest;
        } else if (kind == "burnin") {
            std::string mode;
            ok = field(rest, &mode);
            if (ok && mode == "roam") {
                parsed.burnin = 'r';
                ok = number(rest, 1, 128, &parsed.roam_width) && number(rest, 1, 128, &parsed.roam_height) && !*rest;
            } else if (ok) {
                parsed.burnin = mode == "shift" ? 's' : 'n';
                ok = (mode == "shift" || mode == "none") && !*rest;
            }
        } else if (kind == "pages") {
            ok = number(rest, 1, 4, &parsed.pages) && number(rest, 1, 60, &parsed.page_seconds) && !*rest;
        } else if (parsed.items.size() < OLED_LAYOUT_MAX_ITEMS) {
            OledItem item;
            ok = parse_item(kind, rest, &item);
            if (ok) parsed.items.push_back(item);
        }
        if (!ok) {
            if (error) *error = "line " + std::to_string(line_number) + ": " + line.substr(0, 40);
            return false;
        }
    }
    if (!header) {
        if (error) *error = "empty layout";
        return false;
    }
    *layout = parsed;
    return true;
}

std::string oled_layout_expand(const std::string &text, const OledValues &values)
{
    std::string out;
    for (size_t i = 0; i < text.size() && out.size() < OLED_LAYOUT_MAX_EXPANDED; i++) {
        Placeholder placeholder;
        std::string value;
        if (!find_placeholder(text, i, &placeholder) || !variable_value(placeholder.name, values, &value)) {
            out += text[i];
            continue;
        }
        // The parser refuses other widths; a layout given straight to this
        // function is clamped, never allowed to ask for a huge string.
        int pad = placeholder.has_pad ? int(placeholder.pad) : 0;
        if (pad > OLED_LAYOUT_MAX_PAD) pad = OLED_LAYOUT_MAX_PAD;
        if (pad < -OLED_LAYOUT_MAX_PAD) pad = -OLED_LAYOUT_MAX_PAD;
        const size_t wanted = size_t(pad < 0 ? -pad : pad);
        if (value.size() > wanted && pad) value.resize(wanted);
        if (pad > 0) value.insert(0, wanted - value.size(), ' ');
        else if (pad < 0) value.append(wanted - value.size(), ' ');
        out += value;
        i = placeholder.close;
    }
    if (out.size() > OLED_LAYOUT_MAX_EXPANDED) out.resize(OLED_LAYOUT_MAX_EXPANDED);
    return out;
}

bool oled_layout_usable(const char *text, bool narrow, OledLayout *layout, std::string *error)
{
    if (!oled_layout_parse(text, layout, error)) return false;
    if (layout->narrow == narrow) return true;
    if (error) *error = "for the other panel";
    return false;
}

int oled_layout_pick(const char *const texts[], int count, bool narrow, OledLayout *layout,
                     std::vector<std::pair<int, std::string>> *errors)
{
    for (int i = 0; i < count; i++) {
        if (!texts[i]) continue;
        std::string error;
        if (oled_layout_usable(texts[i], narrow, layout, &error)) return i;
        if (errors) errors->emplace_back(i, error);
    }
    oled_layout_parse(oled_layout_default(narrow), layout, nullptr);
    return -1;
}

void oled_layout_render(const OledLayout &layout, const OledValues &values, unsigned step,
                        uint8_t frame[OLED_FRAME_BYTES])
{
    Canvas canvas = {frame, layout.narrow ? 64 : 128, layout.narrow ? 32 : 64, layout.rotation, 0, 0};
    memset(frame, 0, OLED_FRAME_BYTES);
    if (layout.burnin == 's') {
        static const int moves[4][2] = {{0, 0}, {1, 0}, {1, 1}, {0, 1}};
        canvas.dx = moves[step % 4][0];
        canvas.dy = moves[step % 4][1];
    } else if (layout.burnin == 'r') {
        // As the roaming status moved: 13 pixels across, a page down.
        const int spare_x = canvas.logical_width() - layout.roam_width;
        const int spare_pages = canvas.logical_height() / 8 - (layout.roam_height + 7) / 8;
        canvas.dx = spare_x > 0 ? int(step * 13 % unsigned(spare_x + 1)) : 0;
        canvas.dy = spare_pages > 0 ? int(step % unsigned(spare_pages + 1)) * 8 : 0;
    }
    for (const OledItem &item : layout.items) {
        if (!condition_holds(item, values)) continue;
        if (item.kind == 't') draw_text(canvas, item, oled_layout_expand(item.text, values));
        else if (item.kind == 'l') draw_line(canvas, item);
        else if (item.kind == 'r') draw_rect(canvas, item);
        else draw_image(canvas, item);
    }
}
