#pragma once

// Whether somebody is actually looking at the video, as the Go server's internal/attention: an open tab alone is not
// enough, since a tab left open in the background or on a screen nobody looks at keeps its video stream. Somebody
// watches while a browser page with input is visible and keyboard or mouse input came from a page recently. Loop
// thread only.

#include <chrono>

namespace nkos::input::attention {

// The visible pages of the input sockets.
void page_shown();
void page_hidden();
// Keyboard or mouse input from a page.
void note_input(std::chrono::steady_clock::time_point now);
// A visible page and input within the last `within`.
bool watching(std::chrono::steady_clock::time_point now, std::chrono::steady_clock::duration within);

} // namespace nkos::input::attention
