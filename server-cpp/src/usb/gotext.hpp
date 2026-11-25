#pragma once

// Text as the Go server treats it, where its answers show it: UTF-8 decoding (utf8.DecodeRune), unicode.IsPrint,
// strings.TrimSpace, strconv.Quote (%q), and a string or a float64 as encoding/json writes them.

#include <cstddef>
#include <string>
#include <string_view>

namespace nkos::usb::gotext {

// utf8.DecodeRuneInString: the rune at the start of s and its length; an invalid sequence is U+FFFD of length 1
// (0 for an empty s).
char32_t decode_rune(std::string_view s, std::size_t &size);
void append_rune(std::string &out, char32_t r);
bool valid_utf8(std::string_view s);
std::size_t rune_count(std::string_view s);

bool is_print(char32_t r);
bool is_space(char32_t r);
std::string trim_space(std::string_view s);

// strconv.Quote, strconv.QuoteRune.
std::string quote(std::string_view s);
std::string quote_rune(char32_t r);

// json.Marshal of a string (HTML characters, U+2028 and U+2029 escaped, invalid UTF-8 as U+FFFD).
std::string json_string(std::string_view s);
// json.Marshal of a float64 (finite).
std::string json_float(double f);

// syscall.Errno.Error (Go's own table of the Linux errors).
std::string errno_text(int err);
// fmt's %-Ns: s padded with spaces to width runes.
std::string pad_right(std::string_view s, std::size_t width);

} // namespace nkos::usb::gotext
