#pragma once

// Files as the Go AI services read and write them: errors in Go's words, and the atomic save they all use (a 0600
// temporary file in the same directory, synced, renamed over the target, the directory synced).

#include <string>
#include <string_view>

namespace nkos::ai {

// syscall.Errno's text.
std::string errno_text(int e);
// os.ReadFile: 0 or the errno.
int read_file(const std::string &path, std::string &out);
// The atomic save: "" or the error ("create <what> directory: ...", "write temporary <what>: ...", ...).
std::string save_file(const std::string &path, std::string_view data, const std::string &temp_prefix, const std::string &what);

} // namespace nkos::ai
