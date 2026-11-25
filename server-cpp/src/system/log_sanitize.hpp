#pragma once

// What the log viewer and the archive may show of a log, as the Go server's logs/sanitize.go: valid UTF-8, no
// terminal escapes or control and format characters, no unfinished last entry, and every entry that may carry a
// credential (keywords, URLs with user info or sign-in links, PEM blocks, long base64 lines) hidden whole. At most
// 20000 lines and 1 MiB, the newest. The Go patterns (RE2) are matched by hand here, with RE2's case folding.

#include <cstddef>
#include <string>
#include <string_view>

namespace nkos::system::logs {

inline constexpr std::size_t max_bytes = 1024 * 1024;
inline constexpr std::size_t max_lines = 20000;

struct Sanitized {
	std::string content;
	int lines = 0;
	bool truncated = false;
};

// truncated: data starts within an entry (its first line is dropped).
Sanitized sanitize_tail(std::string_view data, bool truncated);

} // namespace nkos::system::logs
