#include "oled_detect.h"

#include <cstdlib>
#include <cstring>
#include <string>

OledBoard oled_board(const char *revision, const char *hw)
{
    if (revision) {
        if (!strcmp(revision, "alpha")) return OledBoard::Alpha;
        if (!strcmp(revision, "beta")) return OledBoard::Beta;
        if (!strcmp(revision, "pcie")) return OledBoard::Pcie;
        if (!strcmp(revision, "lite")) return OledBoard::Lite;
    }
    if (hw && hw[0] == 'b') return OledBoard::Beta;
    if (hw && hw[0] == 'p') return OledBoard::Pcie;
    return OledBoard::Alpha;
}

namespace {

struct Override {
    bool off = false;
    int bus = -1;
    int address = -1;
    int narrow = -1;
};

bool parse_number(const std::string &text, int low, int high, int *value)
{
    if (text.empty()) return false;
    char *end = nullptr;
    const long parsed = strtol(text.c_str(), &end, 0);
    if (*end || parsed < low || parsed > high) return false;
    *value = (int)parsed;
    return true;
}

bool parse_override(const char *text, Override *result)
{
    Override parsed;
    std::string rest(text);
    size_t start = 0;
    while (start <= rest.size()) {
        size_t end = rest.find('\n', start);
        if (end == std::string::npos) end = rest.size();
        std::string line = rest.substr(start, end - start);
        start = end + 1;
        const size_t first = line.find_first_not_of(" \t\r");
        if (first == std::string::npos || line[first] == '#') continue;
        line = line.substr(first, line.find_last_not_of(" \t\r") - first + 1);
        if (line == "off") {
            parsed.off = true;
            continue;
        }
        const size_t equals = line.find('=');
        if (equals == std::string::npos) return false;
        const std::string key = line.substr(0, equals), value = line.substr(equals + 1);
        if (key == "bus") {
            if (!parse_number(value, 0, 255, &parsed.bus)) return false;
        } else if (key == "address") {
            if (!parse_number(value, 0x03, 0x77, &parsed.address)) return false;
        } else if (key == "layout") {
            if (value == "wide") parsed.narrow = 0;
            else if (value == "narrow") parsed.narrow = 1;
            else return false;
        } else {
            return false;
        }
    }
    *result = parsed;
    return true;
}

} // namespace

bool oled_detect(OledBoard board, const char *override_text,
                 bool (*answers)(int bus, int address, void *context), void *context,
                 OledTarget *target)
{
    int bus = board == OledBoard::Alpha ? 1 : 5;
    bool narrow = board == OledBoard::Pcie;
    int addresses[2] = {0x3D, 0x3C};
    int count = 2;
    if (board == OledBoard::Alpha) {
        count = 1;
    } else if (board == OledBoard::Pcie) {
        addresses[0] = 0x3C;
        addresses[1] = 0x3D;
    }

    Override manual;
    if (override_text && parse_override(override_text, &manual)) {
        if (manual.off) return false;
        if (manual.bus >= 0) bus = manual.bus;
        if (manual.narrow >= 0) narrow = manual.narrow;
        if (manual.address >= 0) {
            addresses[0] = manual.address;
            count = 1;
        }
    }

    for (int i = 0; i < count; i++) {
        if (answers(bus, addresses[i], context)) {
            *target = {bus, addresses[i], narrow};
            return true;
        }
    }
    return false;
}
