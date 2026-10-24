// SPDX-License-Identifier: GPL-3.0-or-later
#pragma once
#include <cerrno>
#include <fstream>
#include <sys/stat.h>
#include <string>

namespace nanokvm {
// Called before opening/reopening capture, never from the per-frame path.
inline bool video_clocks_ready(unsigned receiver, const std::string &parameters =
    "/sys/module/sg2002_video_clocks/parameters", const std::string &policy =
    "/run/nanokvm/video_clock_policy")
{
    if (receiver != 0 && receiver != 3) return true; // UXC/UE retain DT clocks.
    const auto read = [](const std::string &path) {
        std::string value;
        std::getline(std::ifstream(path), value);
        return value;
    };
    if (read(parameters + "/receiver") == (receiver == 0 ? "c" : "d") &&
        read(parameters + "/ready") == "Y" &&
        read(parameters + "/rates") == "396000000 360000000 300000000")
        return true;
    // S15/native may identify the fixed chip only after service startup.
    // An explicit startup deferral preserves the legacy C fallback without
    // changing live clocks. A failed or unverified profile stays fail-closed.
    struct stat status;
    return read(policy) == "deferred" &&
        stat(parameters.c_str(), &status) < 0 && errno == ENOENT;
}
} // namespace nanokvm
