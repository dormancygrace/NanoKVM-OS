#pragma once

// Key names of the AI tools: MCP's press_keys (KeyboardEvent.code names, service/mcp/keyboard.go), PicoClaw's
// hotkey action (codes, aliases and single characters, service/picoclaw/keyboard_map.go), and the US layout the
// typing tools use (service/hid's char map).

#include <cstdint>
#include <string>
#include <string_view>

namespace nkos::ai {

// The US layout of service/hid (paste): modifiers and usage of a character.
bool us_char(char32_t r, std::uint8_t &modifiers, std::uint8_t &code);

// MCP's modifierMap and keyCodeMap.
bool mcp_modifier(std::string_view name, std::uint8_t &bit);
bool mcp_key_code(std::string_view name, std::uint8_t &code);

// PicoClaw's resolveKey: the HID value and whether it is a modifier; error "unsupported key: ..." / "empty key".
bool picoclaw_key(const std::string &key, std::uint8_t &code, bool &modifier, std::string &error);

} // namespace nkos::ai
