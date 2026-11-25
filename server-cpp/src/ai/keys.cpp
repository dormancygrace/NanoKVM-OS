#include "ai/keys.hpp"

#include "ai/json.hpp"
#include "input/keyboard_routes.hpp"

#include <map>

namespace nkos::ai {

namespace {

const std::map<std::string_view, std::uint8_t> modifiers = {
    {"ControlLeft", 1 << 0}, {"ShiftLeft", 1 << 1}, {"AltLeft", 1 << 2}, {"MetaLeft", 1 << 3},
    {"ControlRight", 1 << 4}, {"ShiftRight", 1 << 5}, {"AltRight", 1 << 6}, {"MetaRight", 1 << 7},
};

// service/mcp/keyboard.go keyCodeMap.
const std::map<std::string_view, std::uint8_t> mcp_codes = {
    {"KeyA", 0x04}, {"KeyB", 0x05}, {"KeyC", 0x06}, {"KeyD", 0x07}, {"KeyE", 0x08}, {"KeyF", 0x09}, {"KeyG", 0x0a},
    {"KeyH", 0x0b}, {"KeyI", 0x0c}, {"KeyJ", 0x0d}, {"KeyK", 0x0e}, {"KeyL", 0x0f}, {"KeyM", 0x10}, {"KeyN", 0x11},
    {"KeyO", 0x12}, {"KeyP", 0x13}, {"KeyQ", 0x14}, {"KeyR", 0x15}, {"KeyS", 0x16}, {"KeyT", 0x17}, {"KeyU", 0x18},
    {"KeyV", 0x19}, {"KeyW", 0x1a}, {"KeyX", 0x1b}, {"KeyY", 0x1c}, {"KeyZ", 0x1d},
    {"Digit1", 0x1e}, {"Digit2", 0x1f}, {"Digit3", 0x20}, {"Digit4", 0x21}, {"Digit5", 0x22},
    {"Digit6", 0x23}, {"Digit7", 0x24}, {"Digit8", 0x25}, {"Digit9", 0x26}, {"Digit0", 0x27},
    {"Enter", 0x28}, {"Escape", 0x29}, {"Backspace", 0x2a}, {"Tab", 0x2b}, {"Space", 0x2c}, {"Minus", 0x2d},
    {"Equal", 0x2e}, {"BracketLeft", 0x2f}, {"BracketRight", 0x30}, {"Backslash", 0x31}, {"IntlHash", 0x32},
    {"Semicolon", 0x33}, {"Quote", 0x34}, {"Backquote", 0x35}, {"Comma", 0x36}, {"Period", 0x37}, {"Slash", 0x38},
    {"CapsLock", 0x39},
    {"F1", 0x3a}, {"F2", 0x3b}, {"F3", 0x3c}, {"F4", 0x3d}, {"F5", 0x3e}, {"F6", 0x3f},
    {"F7", 0x40}, {"F8", 0x41}, {"F9", 0x42}, {"F10", 0x43}, {"F11", 0x44}, {"F12", 0x45},
    {"PrintScreen", 0x46}, {"ScrollLock", 0x47}, {"Pause", 0x48}, {"Insert", 0x49}, {"Home", 0x4a}, {"PageUp", 0x4b},
    {"Delete", 0x4c}, {"End", 0x4d}, {"PageDown", 0x4e}, {"ArrowRight", 0x4f}, {"ArrowLeft", 0x50},
    {"ArrowDown", 0x51}, {"ArrowUp", 0x52},
    {"NumLock", 0x53}, {"NumpadDivide", 0x54}, {"NumpadMultiply", 0x55}, {"NumpadSubtract", 0x56}, {"NumpadAdd", 0x57},
    {"NumpadEnter", 0x58}, {"Numpad1", 0x59}, {"Numpad2", 0x5a}, {"Numpad3", 0x5b}, {"Numpad4", 0x5c},
    {"Numpad5", 0x5d}, {"Numpad6", 0x5e}, {"Numpad7", 0x5f}, {"Numpad8", 0x60}, {"Numpad9", 0x61},
    {"Numpad0", 0x62}, {"NumpadDecimal", 0x63}, {"IntlBackslash", 0x64}, {"ContextMenu", 0x65},
};

// service/picoclaw/keyboard_map.go keycodeMap.
const std::map<std::string_view, std::uint8_t> pico_codes = [] {
	std::map<std::string_view, std::uint8_t> m = mcp_codes;
	const std::pair<std::string_view, std::uint8_t> more[] = {
	    {"Power", 0x66}, {"NumpadEqual", 0x67}, {"F13", 0x68}, {"F14", 0x69}, {"F15", 0x6a}, {"F16", 0x6b},
	    {"F17", 0x6c}, {"F18", 0x6d}, {"F19", 0x6e}, {"F20", 0x6f}, {"F21", 0x70}, {"F22", 0x71}, {"F23", 0x72},
	    {"F24", 0x73}, {"Execute", 0x74}, {"Help", 0x75}, {"Props", 0x76}, {"Select", 0x77}, {"Stop", 0x78},
	    {"Again", 0x79}, {"Undo", 0x7a}, {"Cut", 0x7b}, {"Copy", 0x7c}, {"Paste", 0x7d}, {"Find", 0x7e},
	    {"AudioVolumeMute", 0x7f}, {"AudioVolumeUp", 0x80}, {"AudioVolumeDown", 0x81}, {"VolumeMute", 0x7f},
	    {"VolumeUp", 0x80}, {"VolumeDown", 0x81}, {"LockingCapsLock", 0x82}, {"LockingNumLock", 0x83},
	    {"LockingScrollLock", 0x84}, {"NumpadComma", 0x85}, {"NumpadEqual2", 0x86}, {"IntlRo", 0x87},
	    {"KanaMode", 0x88}, {"IntlYen", 0x89}, {"Convert", 0x8a}, {"NonConvert", 0x8b}, {"International6", 0x8c},
	    {"International7", 0x8d}, {"International8", 0x8e}, {"International9", 0x8f}, {"Lang1", 0x90},
	    {"Lang2", 0x91}, {"Lang3", 0x92}, {"Lang4", 0x93}, {"Lang5", 0x94}, {"Lang6", 0x95}, {"Lang7", 0x96},
	    {"Lang8", 0x97}, {"Lang9", 0x98}, {"NumpadParenLeft", 0xb6}, {"NumpadParenRight", 0xb7},
	    {"NumpadBackspace", 0xbb}, {"NumpadMemoryStore", 0xd0}, {"NumpadMemoryRecall", 0xd1},
	    {"NumpadMemoryClear", 0xd2}, {"NumpadMemoryAdd", 0xd3}, {"NumpadMemorySubtract", 0xd4},
	    {"NumpadClear", 0xd8}, {"NumpadClearEntry", 0xd9},
	};
	for (const auto &[k, v] : more) m[k] = v;
	return m;
}();

const std::map<std::string_view, std::string_view> aliases = {
    {"CTRL", "ControlLeft"}, {"CONTROL", "ControlLeft"}, {"SHIFT", "ShiftLeft"}, {"ALT", "AltLeft"},
    {"OPTION", "AltLeft"}, {"META", "MetaLeft"}, {"WIN", "MetaLeft"}, {"WINDOWS", "MetaLeft"},
    {"COMMAND", "MetaLeft"}, {"CMD", "MetaLeft"}, {"SUPER", "MetaLeft"}, {"ESC", "Escape"}, {"RETURN", "Enter"},
    {"DEL", "Delete"}, {"INS", "Insert"}, {"UP", "ArrowUp"}, {"DOWN", "ArrowDown"}, {"LEFT", "ArrowLeft"},
    {"RIGHT", "ArrowRight"}, {"SPACEBAR", "Space"}, {"SPACE", "Space"}, {"TAB", "Tab"}, {"ENTER", "Enter"},
    {"BACKSPACE", "Backspace"}, {"PGUP", "PageUp"}, {"PGDN", "PageDown"},
};

} // namespace

bool us_char(char32_t r, std::uint8_t &mods, std::uint8_t &code) { return input::us_key(r, mods, code); }

bool mcp_modifier(std::string_view name, std::uint8_t &bit)
{
	auto it = modifiers.find(name);
	if (it == modifiers.end()) return false;
	bit = it->second;
	return true;
}

bool mcp_key_code(std::string_view name, std::uint8_t &code)
{
	auto it = mcp_codes.find(name);
	if (it == mcp_codes.end()) return false;
	code = it->second;
	return true;
}

bool picoclaw_key(const std::string &key, std::uint8_t &code, bool &modifier, std::string &error)
{
	std::string trimmed = json::trim_space(key);
	if (trimmed.empty()) {
		error = "empty key";
		return false;
	}
	auto lookup = [&](std::string_view name) {
		if (auto m = modifiers.find(name); m != modifiers.end()) {
			code = m->second;
			modifier = true;
			return true;
		}
		if (auto k = pico_codes.find(name); k != pico_codes.end()) {
			code = k->second;
			modifier = false;
			return true;
		}
		return false;
	};
	if (lookup(trimmed)) return true;
	// strings.ToUpper (ASCII letters; other characters as they are).
	std::string upper = json::to_upper(trimmed);
	if (auto a = aliases.find(upper); a != aliases.end()) {
		if (lookup(a->second)) return true;
		code = 0;
		modifier = false;
		return true;
	}
	if (upper.size() == 1 && upper[0] >= 'A' && upper[0] <= 'Z') return lookup("Key" + upper) || (code = 0, modifier = false, true);
	if (upper.size() == 1 && upper[0] >= '0' && upper[0] <= '9') return lookup("Digit" + upper) || (code = 0, modifier = false, true);
	if (auto m = modifiers.find(upper); m != modifiers.end()) {
		code = m->second;
		modifier = true;
		return true;
	}
	if (auto k = pico_codes.find(upper); k != pico_codes.end()) {
		code = k->second;
		modifier = false;
		return true;
	}
	error = "unsupported key: " + key;
	return false;
}

} // namespace nkos::ai
