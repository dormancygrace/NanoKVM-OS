// The work of the nkos-update helper, as the Go server's osupdate.RunAPK, RunScheduledIndexRefresh and the
// rollback recovery service (apk.go, index_schedule.go, rollback_recovery.go, internal/apkrollback): run detached
// from the web server, so that upgrading or restarting it cannot stop an apk transaction. Output goes to the
// helper's stdout and stderr (the operation log).

#include "common/process.hpp"
#include "osupdate/files.hpp"
#include "osupdate/gostr.hpp"
#include "osupdate/internal.hpp"

#include <cerrno>
#include <csignal>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

extern char **environ;

namespace nkos::osupdate {

namespace {

constexpr const char *constraint = ".nanokvm-stable-rollback";
constexpr const char *recovery_script = "/etc/init.d/nanokvm-rollback-recovery";
constexpr const char *recovery_link = "/etc/runlevels/boot/nanokvm-rollback-recovery";

// internal/apkrollback/recovery.initd, installed and synced before a rollback.
constexpr const char *recovery_service = R"(#!/sbin/openrc-run
# Installed and synced before rollback. Not package-owned: survives restoring
# an older updater that does not know about release channels or this constraint.
description="Clear an interrupted NanoKVM rollback constraint"
depend() {
    need localmount
    before networking nanokvm-app
}
start() {
    # APK serializes database writes itself. No index refresh or network needed.
    # Keep the service registered on failure so the next boot retries.
    [ -r /lib/apk/db/installed ] && [ -r /etc/apk/world ] || return 1
    if grep -q '^P:\.nanokvm-stable-rollback$' /lib/apk/db/installed ||
       grep -qE '^\.nanokvm-stable-rollback([=<>~].*)?$' /etc/apk/world; then
        /sbin/apk --no-network --wait 30 del .nanokvm-stable-rollback || return 1
    fi
    rm -f /etc/runlevels/boot/nanokvm-rollback-recovery || return 1
    rm -f /etc/init.d/nanokvm-rollback-recovery || return 1
}
)";

void say(const std::string &text) { (void)!::write(1, text.data(), text.size()); }

Error sync_path(const std::string &path)
{
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return goerr::path_error("open", path, errno);
	Error e = ::fsync(fd) == 0 ? "" : goerr::path_error("sync", path, errno);
	::close(fd);
	return e;
}

std::string dir_of(const std::string &p) { return p.substr(0, p.rfind('/')); }

Error install_rollback_recovery()
{
	for (const auto &dir : {dir_of(recovery_script), dir_of(recovery_link)})
		if (auto e = files::mkdir_all(dir, 0755); !e.empty()) return e;
	if (auto e = write_atomic(recovery_script, recovery_service, 0755); !e.empty()) return e;
	if (auto e = sync_path(recovery_script); !e.empty()) return e;
	if (::symlink(recovery_script, recovery_link) != 0) {
		int e = errno;
		if (e != EEXIST) return "symlink " + std::string(recovery_script) + " " + recovery_link + ": " + goerr::errno_text(e);
		char target[4096];
		ssize_t n = ::readlink(recovery_link, target, sizeof target);
		if (n < 0 || std::string(target, static_cast<std::size_t>(n)) != recovery_script) return "unexpected rollback recovery service link";
	}
	// Both the script and its boot registration are durable before APK replaces the updater.
	for (const auto &dir : {dir_of(recovery_script), dir_of(recovery_link)})
		if (auto e = sync_path(dir); !e.empty()) return e;
	return "";
}

} // namespace

Error remove_rollback_recovery()
{
	for (const char *path : {recovery_link, recovery_script})
		if (int e = files::remove(path); e && e != ENOENT) return goerr::path_error("remove", path, e);
	return "";
}

Error run_wait(const std::vector<std::string> &argv)
{
	int report[2];
	if (::pipe2(report, O_CLOEXEC) != 0) return goerr::errno_text(errno);
	std::vector<char *> args;
	for (const auto &a : argv) args.push_back(const_cast<char *>(a.c_str()));
	args.push_back(nullptr);
	pid_t pid = ::fork();
	if (pid < 0) {
		::close(report[0]);
		::close(report[1]);
		return goerr::start_error(argv[0], errno);
	}
	if (pid == 0) {
		int null = ::open("/dev/null", O_RDWR);
		::dup2(null, 0);
		::dup2(null, 1);
		::dup2(null, 2);
		sigset_t none;
		sigemptyset(&none);
		::sigprocmask(SIG_SETMASK, &none, nullptr);
		process::cloexec_inherited();
		::execve(args[0], args.data(), environ);
		int e = errno;
		(void)!::write(report[1], &e, sizeof e);
		::_exit(127);
	}
	::close(report[1]);
	int exec_errno = 0;
	bool failed = ::read(report[0], &exec_errno, sizeof exec_errno) == sizeof exec_errno;
	::close(report[0]);
	int status = 0;
	while (::waitpid(pid, &status, 0) < 0 && errno == EINTR) {
	}
	if (failed) return goerr::start_error(argv[0], exec_errno);
	if (WIFEXITED(status) && WEXITSTATUS(status) == 0) return "";
	return WIFEXITED(status) ? goerr::exit_text(WEXITSTATUS(status), 0) : goerr::exit_text(-1, WTERMSIG(status));
}

Error run_apk(const std::string &action)
{
	Error result;
	auto finish = [&](Error e) {
		if (!e.empty()) {
			ApkStatus failed;
			failed.state = "failed";
			failed.message = e;
			(void)save_apk_status(failed);
		}
		return e;
	};
	if (action == "reboot") {
		std::this_thread::sleep_for(std::chrono::seconds(1));
		return finish(run_wait({"/sbin/reboot"}));
	}
	if (action != "check" && action != "install" && action != "rollback") return finish("invalid package operation");
	if (action == "rollback") {
		auto status = get_channel();
		if (!status.available || status.channel != "stable" || !status.rollback_available)
			return finish("select Stable with a prerelease installed before rolling back");
	}
	auto deadline = apkrun::Clock::now() + std::chrono::minutes(30);
	auto command = [&](std::vector<std::string> args) {
		apkrun::Cmd cmd;
		cmd.path = apk_executable;
		cmd.args = std::move(args);
		cmd.deadline = deadline;
		return cmd;
	};
	auto run = [&](std::vector<std::string> args) {
		auto r = apkrun::run(command(std::move(args)));
		return r.ok ? Error() : r.error;
	};
	if (auto e = run({"update"}); !e.empty()) {
		// The page has one check for both: an unreachable Alpine mirror must not keep the NanoKVM OS repository
		// from being checked.
		if (action == "check") (void)check_updates(true);
		return finish("APK index refresh failed: " + e);
	}
	if (rollback_constraint_installed())
		if (auto e = run({"del", constraint}); !e.empty()) return finish("cannot clear interrupted rollback constraint: " + e);
	if (action == "check") {
		auto listing = apkrun::combined_output(command({"version", "--limit", "<"}));
		if (!listing.ok) return finish("APK upgrade check failed: " + gostr::trim_space(listing.output));
		std::vector<std::string> skip, bounded;
		upgrade_selections(skip, bounded);
		std::string upgrades = installable_upgrades(listing.output, skip);
		if (!bounded.empty()) {
			auto plan = apkrun::combined_output(command({"upgrade", "--simulate"}));
			if (!plan.ok) return finish("APK upgrade plan failed: " + gostr::trim_space(plan.output));
			upgrades = gostr::trim_space(upgrades + "\n" + bounded_upgrades(plan.output, bounded));
		}
		ApkStatus s;
		s.state = upgrades.empty() ? "up-to-date" : "ready";
		s.upgrades = upgrades;
		// The notice has an index of its own; a manual check refreshes it too.
		(void)check_updates(true);
		return finish(save_apk_status(s));
	}
	if (action == "install") {
		auto names = nanokvm_file_pins();
		if (!names.empty()) {
			say("Taking " + gostr::join(names, ", ") + " back to the NanoKVM repositories: installed from local files\n");
			std::vector<std::string> args{"add"};
			args.insert(args.end(), names.begin(), names.end());
			// When resolving the repository selections fails, the remaining upgrades are still attempted.
			if (auto e = run(args); !e.empty()) say("Kept the local file selections: " + e + "\n");
		}
	}
	std::vector<std::string> args{"upgrade"};
	if (action == "rollback") {
		int err = 0;
		auto data = files::read(repositories_path, err);
		if (!data) return finish(files::read_error(repositories_path, err));
		std::string branch;
		ChannelStatus status;
		int i = channel_line(*data, branch, status);
		if (!status.available || status.channel != "stable") return finish("stable repository changed");
		std::string repo = gostr::trim_space(gostr::split(*data, "\n")[static_cast<std::size_t>(i)]);
		std::vector<std::string> query{"--repositories-file=/dev/null", "--repository=" + repo, "search", "--exact"};
		query.insert(query.end(), core_packages.begin(), core_packages.end());
		// search fails when a name is not in the index (a core package a release from before it lacks); it still
		// lists the others, and stable_package_targets checks that nothing else is missing.
		auto search = apkrun::output_split(command(query));
		if (!search.ok && (search.not_started || gostr::trim_space(search.output).empty()))
			return finish("cannot read stable packages: " + gostr::trim_space(search.stderr_output));
		Error e;
		auto targets = stable_package_targets(search.output, e);
		if (!e.empty()) return finish(e);
		auto names = tpu_dependents(targets, files::read(installed_db_path).value_or(""));
		if (!names.empty()) return finish("stable has no TPU runtime; remove " + gostr::join(names, ", ") + " first");
		args = {"add", "--virtual", constraint};
		args.insert(args.end(), targets.begin(), targets.end());
		// APK checks the complete dependency graph before installing anything.
		auto simulate = args;
		simulate.push_back("--simulate");
		if (auto e2 = run(simulate); !e2.empty()) return finish("stable rollback dependency check failed: " + e2);
		if (auto e2 = install_rollback_recovery(); !e2.empty()) return finish("cannot prepare persistent rollback recovery: " + e2);
		// The hold also goes when a package script fails after APK installed it.
		auto cleanup = [&](Error r) {
			if (rollback_constraint_installed())
				if (auto e3 = run({"del", constraint}); !e3.empty() && r.empty())
					r = "stable installed but temporary constraint cleanup failed; reboot to recover: " + e3;
			return finish(r);
		};
		if (auto e2 = run(args); !e2.empty()) return cleanup("APK package transaction failed: " + e2);
		if (auto e2 = run({"del", constraint}); !e2.empty()) return cleanup("stable constraint cleanup failed; reboot to recover: " + e2);
		if (auto e2 = remove_rollback_recovery(); !e2.empty()) return cleanup(e2);
		(void)check_updates(false);
		ApkStatus s;
		s.state = "installed";
		return cleanup(save_apk_status(s));
	}
	if (auto e = run(args); !e.empty()) return finish("APK package transaction failed: " + e);
	(void)check_updates(false);
	ApkStatus s;
	s.state = "installed";
	return finish(save_apk_status(s));
}

Error run_scheduled_index_refresh()
{
	Error e;
	auto l = lock(e);
	if (!l) return e;
	auto schedule = get_index_schedule(e);
	if (!e.empty() || !schedule.enabled) return e;
	if (auto err = save_software_status(SoftwareStatus{"running", "refresh", "", "", ""}); !err.empty()) return err;
	std::string log_path = std::string(apk_state_dir) + "/software.log";
	int log = ::open(log_path.c_str(), O_CREAT | O_TRUNC | O_WRONLY | O_CLOEXEC, 0600);
	if (log < 0) {
		Error err = goerr::path_error("open", log_path, errno);
		(void)save_software_status(SoftwareStatus{"failed", "refresh", "", err, ""});
		return err;
	}
	// The run's output goes to the visible operation log; the helper's own streams come back afterwards.
	int saved_out = ::dup(1), saved_err = ::dup(2);
	::dup2(log, 1);
	::dup2(log, 2);
	::close(log);
	Error result = run_software("refresh", "");
	::dup2(saved_out, 1);
	::dup2(saved_err, 2);
	::close(saved_out);
	::close(saved_err);
	return result;
}

} // namespace nkos::osupdate
