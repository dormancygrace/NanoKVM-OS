#pragma once
#include <cstdint>
#include "../../../../../firmware/mpi/nanokvm_receiver_timing.h"

namespace nanokvm {
enum class ReceiverKind { C, UXC, D, Unknown };
inline ReceiverKind receiver_kind(unsigned board_model)
{
    return board_model == 1 ? ReceiverKind::UXC :
        board_model == 3 ? ReceiverKind::D : ReceiverKind::C;
}
inline const char *receiver_name(ReceiverKind kind)
{
    switch (kind) {
    case ReceiverKind::C: return "LT6911C";
    case ReceiverKind::UXC: return "LT6911UXC";
    case ReceiverKind::D: return "LT6911D";
    default: return "unknown";
    }
}
inline unsigned receiver_settle_ms(ReceiverKind kind)
{
    return nanokvm_receiver_settle_ms(kind == ReceiverKind::UXC ? 'u' :
        kind == ReceiverKind::D ? 'd' : 'c');
}
/* IDs come from each model's hardware-ID bank, never firmware/date bytes.
 * C's A000 low byte is first; normalize the legacy 16 05 wire pair to 0516.
 * UXC's 8100 and D's E100 have the high byte first. */
inline uint16_t receiver_chip_id(ReceiverKind bank, const uint8_t bytes[2])
{
    return bank == ReceiverKind::C ? (uint16_t(bytes[1]) << 8) | bytes[0] :
        (uint16_t(bytes[0]) << 8) | bytes[1];
}
inline ReceiverKind identify_receiver(ReceiverKind bank, uint16_t id)
{
    if (bank == ReceiverKind::C && id == 0x0516) return ReceiverKind::C;
    if (bank == ReceiverKind::UXC && id == 0x1704) return ReceiverKind::UXC;
    if (bank == ReceiverKind::D && id == 0x2102) return ReceiverKind::D;
    return ReceiverKind::Unknown;
}
struct ReceiverIdentity {
    ReceiverKind model = ReceiverKind::Unknown;
    uint16_t chip_id = 0;
    int error = 0, cleanup_error = 0;
};
} // namespace nanokvm
