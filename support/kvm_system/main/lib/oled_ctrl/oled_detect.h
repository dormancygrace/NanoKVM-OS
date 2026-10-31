#ifndef OLED_DETECT_H_
#define OLED_DETECT_H_

// Where the OLED is, decided without touching hardware: the caller passes the
// board names it read and a function that tells whether an address answers.

enum class OledBoard { Alpha, Beta, Pcie, Lite };

struct OledTarget {
    int bus;
    int address;
    bool narrow; // PCIe: a 64x32 window of the controller, rotated
};

// The device tree's sipeed,board-revision decides; images without it fall
// back to the first letter of /etc/kvm/hw, and to Alpha without either.
// Either argument may be NULL.
OledBoard oled_board(const char *revision, const char *hw);

// The board's bus, then its usual address before the other one (0x3C and
// 0x3D are the only ones in use). The layout follows the board, never the
// address: a module added to a Lite stays 128x64 at either address.
//
// override_text is the content of /etc/kvm/oled, or NULL: "off", or lines
// of bus=N, address=0xNN and layout=wide|narrow. A given address is the
// only one tried. A malformed file is ignored.
bool oled_detect(OledBoard board, const char *override_text,
                 bool (*answers)(int bus, int address, void *context), void *context,
                 OledTarget *target);

#endif // OLED_DETECT_H_
