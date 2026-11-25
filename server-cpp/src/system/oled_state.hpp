#pragma once

// What the device's OLED shows from the server (/run/nanokvm/atx, /run/nanokvm/viewers), as the Go server's
// oled_state.go. Loop thread.

namespace nkos::system::oled {

void publish_atx(bool enabled);
// Written only when the count changes.
void publish_viewers(int count);

} // namespace nkos::system::oled
