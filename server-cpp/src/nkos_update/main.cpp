// nkos-update: runs native APK operations detached from NanoKVM-Server, so that upgrading or restarting the web
// server cannot interrupt a transaction; also the early-boot settings recovery and the scheduled index refresh.
// The same command line as the Go helper (server/cmd/nkos-update):
//   recovery-resume | recovery-reboot | refresh-indexes | apk ACTION
//   apk-inherited ACTION | software-inherited ACTION PACKAGE   (the update lock inherited as descriptor 3)

#include "osupdate/goerr.hpp"
#include "osupdate/goerr.hpp"
#include "osupdate/internal.hpp"
#include "osupdate/osupdate.hpp"
#include "recovery/recovery.hpp"

#include <cstdio>
#include <fcntl.h>
#include <string>
#include <sys/file.h>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace {

const char *const usage = "usage: nkos-update apk ACTION | apk-inherited ACTION | software-inherited ACTION PACKAGE | refresh-indexes";

int fail(const std::string &error)
{
	std::fprintf(stderr, "%s\n", error.c_str());
	return 1;
}

} // namespace

int main(int argc, char **argv)
{
	using namespace nkos;
	std::vector<std::string> args(argv, argv + argc);
	if (::geteuid() != 0) return fail("root required");
	if (args.size() == 2 && args[1] == "recovery-resume") {
		if (auto e = recovery::boot(); !e.empty()) return fail(e);
		return 0;
	}
	if (args.size() == 2 && args[1] == "recovery-reboot") {
		std::this_thread::sleep_for(std::chrono::seconds(2));
		if (auto e = osupdate::run_wait({"/sbin/reboot"}); !e.empty()) return fail(e);
		return 0;
	}
	if (args.size() == 2 && args[1] == "refresh-indexes") {
		if (auto e = osupdate::run_scheduled_index_refresh(); !e.empty()) return fail(e);
		return 0;
	}
	// SSH recovery uses the same detached worker and lock as the web UI.
	if (args.size() == 3 && args[1] == "apk") {
		if (auto e = osupdate::start_apk(args[2]); !e.empty()) return fail(e);
		std::printf("Started; see /run/nanokvm-apk-ui/operation.log\n");
		return 0;
	}
	bool apk = args.size() == 3 && args[1] == "apk-inherited";
	bool software = args.size() == 4 && args[1] == "software-inherited";
	if (!apk && !software) return fail(usage);
	// The inherited descriptor must be the canonical lock file itself.
	struct stat canonical{}, inherited{};
	bool canonical_ok = ::lstat((std::string(osupdate::base) + "/lock").c_str(), &canonical) == 0;
	bool inherited_ok = ::fstat(3, &inherited) == 0;
	if (!canonical_ok || !inherited_ok || !S_ISREG(canonical.st_mode) || canonical.st_dev != inherited.st_dev ||
	    canonical.st_ino != inherited.st_ino)
		return fail("invalid inherited update lock");
	// The helper keeps the lock; the services it launches do not.
	::fcntl(3, F_SETFD, FD_CLOEXEC);
	if (::flock(3, LOCK_EX | LOCK_NB) != 0) return fail(goerr::errno_text(errno));
	std::string e = apk ? osupdate::run_apk(args[2]) : osupdate::run_software(args[2], args[3]);
	if (!e.empty()) return fail(e);
	return 0;
}
