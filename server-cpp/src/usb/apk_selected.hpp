#pragma once

// apkpkg.Selected, ported with the APK family: whether the APK world names the package (it was chosen, not only
// installed as another package's dependency); false with error set when the world cannot be read.

#include <string>

namespace nkos::apkpkg {

bool selected(const std::string &package, std::string &error);

} // namespace nkos::apkpkg
