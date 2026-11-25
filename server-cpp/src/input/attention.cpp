#include "input/attention.hpp"

namespace nkos::input::attention {

namespace {
int visible_pages = 0;
std::chrono::steady_clock::time_point last_input{};  // none before any input
} // namespace

void page_shown() { visible_pages++; }
void page_hidden() { visible_pages--; }
void note_input(std::chrono::steady_clock::time_point now) { last_input = now; }

bool watching(std::chrono::steady_clock::time_point now, std::chrono::steady_clock::duration within)
{
	return visible_pages > 0 && last_input != std::chrono::steady_clock::time_point{} && now - last_input < within;
}

} // namespace nkos::input::attention
