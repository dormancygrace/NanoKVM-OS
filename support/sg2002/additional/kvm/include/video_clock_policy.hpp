// SPDX-License-Identifier: GPL-3.0-or-later
#pragma once
#include <fstream>
#include <string>

namespace nanokvm {
// Called before opening/reopening capture, never from the per-frame path.
inline bool video_clocks_ready(unsigned receiver, const std::string &parameters =
    "/sys/module/sg2002_video_clocks/parameters")
{
    if (receiver != 0 && receiver != 3) return true; // UXC/UE retain DT clocks.
    const auto read = [&](const char *name) {
        std::string value;
        std::getline(std::ifstream(parameters + "/" + name), value);
        return value;
    };
    return read("receiver") == (receiver == 0 ? "c" : "d") && read("ready") == "Y" &&
           read("rates") == "396000000 360000000 300000000";
}
} // namespace nanokvm
