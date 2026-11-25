#pragma once

// The latest capture result of each transport ("direct", "h264", "mjpeg"), sent to the pages on /api/ws
// ("capture-status") when it changes from one state to another. As the Go server's stream/capture_status.go.
// Loop thread only.

#include <functional>
#include <string>
#include <vector>

namespace nkos::video::capture_status {

inline constexpr const char *event = "capture-status";

// The JSON of a status (the "data" of the event).
using Subscriber = std::function<void(const std::string &json)>;

void update(const std::string &mode, int result);
// The latest status of each mode, by mode name.
std::vector<std::string> latest();
void subscribe(Subscriber subscriber);

} // namespace nkos::video::capture_status
