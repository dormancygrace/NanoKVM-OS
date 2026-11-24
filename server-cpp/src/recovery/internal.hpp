#pragma once

// Shared between the files of src/recovery (not for other families).

#include "osupdate/godecode.hpp"
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
// A setting earlier releases backed up that no longer exists: archives holding it stay valid, restore leaves it out.
bool retired(const std::string &p);
bool users(const std::string &p);
bool portable(const std::string &p);
// Settings groups: the name of the group a settings path belongs to, or ""; the groups a backup of files covers;
// all groups, sorted (those a new archive covers); validGroups (unknown or repeated names).
std::string group(const std::string &p);
std::vector<std::string> covered_groups(const std::vector<File> &files);
std::vector<std::string> all_groups();
Error valid_groups(const std::vector<std::string> &names);
// json.Unmarshal of a []string into an existing value (covers): null clears it, null elements keep the value at
// their index.
bool decode_groups(const godecode::Value &v, std::vector<std::string> &out);
// ^[A-Za-z0-9_.][A-Za-z0-9+_.-]{0,127}$
bool package_name(const std::string &s);
// versionPattern: ^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-._][A-Za-z0-9.-]+)?$ with its three groups.
Error retire_pending(const std::string &root);
Error remove_acme_binaries(const std::string &root);
// Options.UnmarshalJSON on a decoded value.
bool decode_options_value(const godecode::Value &v, Options &o);
bool version_pattern(const std::string &s, std::string &major, std::string &minor, std::string &patch);

} // namespace nkos::recovery
