#include "oled_layout.h"
#include "oled_font.h"

#include <ctype.h>
#include <stdlib.h>
#include <string.h>

namespace {

// The screen the roaming status drew before layouts: the address, the
// stream, then the frame rate and link marks, moved a page at a time.
// The roaming status was inspired by IronKVM / yuzi-co (Vadim), 3eb019f5
// and 5c248538.
const char wide_default[] =
    "nanokvm-oled 1 wide\n"
    "rotation 0\n"
    "burnin roam 120 32\n"
    "text 0 0 16 left 0 1 - {ip:-15}\n"
    "text 0 16 8 left 0 1 - {codec:-4} {width:4}X{height:-4}\n"
    "text 0 24 8 left 0 1 - {fps:3} FPS E  W\n"
    "text 54 24 8 left 0 1 eth +\n"
    "text 54 24 8 left 0 1 !eth -\n"
    "text 72 24 8 left 0 1 wifi +\n"
    "text 72 24 8 left 0 1 !wifi -\n";

const char narrow_default[] =
    "nanokvm-oled 1 narrow\n"
    "rotation 0\n"
    "burnin roam 60 24\n"
    "text 0 0 4 left 0 1 - {ip:-15}\n"
    "text 0 8 4 left 0 1 - {codec:-4} {width:4}X{height:-4}\n"
    "text 0 16 4 left 0 1 - {fps:3} FPS E  W\n"
    "text 36 16 4 left 0 1 eth +\n"
    "text 36 16 4 left 0 1 !eth -\n"
    "text 48 16 4 left 0 1 wifi +\n"
    "text 48 16 4 left 0 1 !wifi -\n";

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
    item->when_not = text[0] == '!';
    const std::string name = text.substr(item->when_not ? 1 : 0);
    static const char *const names[] = {"eth", "wifi", "hdmi", "hid", "stream"};
    for (int i = 0; i < 5; i++) {
        if (name == names[i]) {
            item->when = i + 1;
            return true;
        }
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
    if (!item.when) return true;
    const bool state[] = {values.eth, values.wifi, values.hdmi, values.hid, values.stream};
    return state[item.when - 1] != item.when_not;
}

} // namespace

const char *oled_layout_default(bool narrow)
{
    return narrow ? narrow_default : wide_default;
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
