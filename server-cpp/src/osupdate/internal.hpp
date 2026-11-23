#pragma once

// Shared between the files of src/osupdate (not for other families).

#include "osupdate/apkrun.hpp"
#include "osupdate/osupdate.hpp"

#include <chrono>
#include <optional>
#include <string>
#include <vector>

namespace nkos::osupdate {

extern const std::vector<std::string> core_packages;
bool is_core_package(const std::string &name);
bool prerelease(const std::string &version);

// CheckUpdates' context: 2 * noticeTimeout + 10 s.
inline constexpr auto check_timeout = std::chrono::seconds(130);

Error write_repositories(const std::string &data);
Error save_notice(const UpdateNotice &n);
Error save_apk_status_idle();
Error save_apk_status(const ApkStatus &s);
Error remove_rollback_recovery();
// exec.Command(argv...).Run(): stdin, stdout and stderr /dev/null; Go's error text.
Error run_wait(const std::vector<std::string> &argv);
Error save_software_status(const SoftwareStatus &s);

// lockNotice: the notice transaction's lock (/run/nanokvm-apk-ui/notice.lock). Without wait, a lock held elsewhere
// gives nullopt with no error.
std::optional<UpdateLock> lock_notice(bool wait, bool &busy, Error &error);

using apkrun::run_quiet;

namespace json {
std::string str(std::string_view s);
std::string boolean(bool b);
std::string strings(const std::optional<std::vector<std::string>> &list);
} // namespace json

namespace detail {
bool apk_package_name(std::string_view s);
bool apk_bare_version(std::string_view s);
bool apk_package_version(std::string_view s, std::string &name, std::string &version);
bool apk_versioned_name(std::string_view s, std::string &name);
std::size_t step_number(std::string_view s);
bool parse_apk_step(const std::string &raw, std::string &action, std::string &name, std::string &detail);
} // namespace detail

} // namespace nkos::osupdate
