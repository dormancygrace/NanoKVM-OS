#pragma once

// Shared between the files of src/recovery (not for other families).

#include "recovery/recovery.hpp"

#include <functional>
#include <optional>
#include <string>
#include <vector>

namespace nkos::recovery {

std::string go_clean(const std::string &p);
const std::vector<std::string> &settings_list();
const std::vector<std::string> &settings_trees();
std::optional<std::vector<File>> inventory(const std::string &root, Error &error);
bool allowed(const std::string &p);
bool users(const std::string &p);
bool portable(const std::string &p);
// ^[A-Za-z0-9_.][A-Za-z0-9+_.-]{0,127}$
bool package_name(const std::string &s);
// versionPattern: ^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-._][A-Za-z0-9.-]+)?$ with its three groups.
bool version_pattern(const std::string &s, std::string &major, std::string &minor, std::string &patch);

} // namespace nkos::recovery
