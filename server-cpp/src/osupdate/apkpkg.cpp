#include "osupdate/apkpkg.hpp"

#include "common/log.hpp"
#include "osupdate/apkrun.hpp"
#include "osupdate/files.hpp"
#include "osupdate/gostr.hpp"
#include "osupdate/internal.hpp"
#include "osupdate/osupdate.hpp"

#include <algorithm>
#include <cerrno>
#include <thread>

namespace nkos::apkpkg {

namespace {

constexpr const char *base_marker = "/etc/kvm/base-packages";
const std::vector<std::string> base_packages{"lldpd", "lldpd-openrc", "net-snmp", "net-snmp-openrc", "nut", "nut-openrc", "openvpn"};

bool valid(const std::string &name) { return osupdate::valid_package_name(name); }

std::string tail(const std::string &output)
{
	std::string s = output.size() > 4096 ? output.substr(output.size() - 4096) : output;
	return gostr::trim_space(s);
}

struct Apk {
	apkrun::Clock::time_point deadline;
	// CombinedOutput of /sbin/apk args: ok, and the output.
	bool operator()(std::vector<std::string> args, std::string &output) const
	{
		apkrun::Cmd cmd;
		cmd.path = "/sbin/apk";
		cmd.args = std::move(args);
		cmd.deadline = deadline;
		auto r = apkrun::combined_output(cmd);
		output = r.output;
		return r.ok;
	}
};

std::vector<std::string> with(std::vector<std::string> head, const std::vector<std::string> &names)
{
	head.insert(head.end(), names.begin(), names.end());
	return head;
}

// installCommunity: the fix packages reinstalled first (apk fix); with alone, a transaction that would change an
// installed package is refused (changes is then set).
std::string install_community(bool alone, const std::vector<std::string> &fix, const std::vector<std::string> &names, bool &changes)
{
	changes = false;
	if (fix.size() + names.size() == 0) return "no APK package to install";
	for (const auto &list : {fix, names})
		for (const auto &name : list)
			if (!valid(name)) return "invalid APK package name";
	std::string error;
	auto lock = osupdate::lock(error);
	if (!lock) return error;
	if (auto e = osupdate::enable_community_locked(); !e.empty()) return "cannot enable the Alpine community repository: " + e;
	Apk apk{apkrun::Clock::now() + std::chrono::minutes(30)};
	std::string output;
	if (!apk({"update"}, output)) return "apk update failed: " + tail(output);
	for (const auto &[applet, list] : {std::pair<std::string, std::vector<std::string>>{"fix", fix}, {"add", names}}) {
		if (list.empty()) continue;
		if (alone) {
			if (!apk(with({applet, "--simulate", "--"}, list), output)) return "apk " + applet + " failed: " + tail(output);
			auto changed = osupdate::installed_changes(output, fix);
			if (!changed.empty()) {
				changes = true;
				return "installing " + gostr::join(list, ", ") + " " + changes_installed + " (" + gostr::join(changed, ", ") + ")";
			}
		}
		if (!apk(with({applet, "--"}, list), output)) return "apk " + applet + " failed: " + tail(output);
	}
	return "";
}


bool base_work(std::vector<std::string> &fix, std::vector<std::string> &add, std::string &error)
{
	std::vector<std::string> missing, broken;
	if (!osupdate::package_state(base_packages, missing, broken, error)) return false;
	int err = 0;
	auto world = osupdate::files::read(osupdate::world_path, err);
	if (!world) return error = osupdate::files::read_error(osupdate::world_path, err), false;
	fix.clear();
	add.clear();
	base_plan(missing, broken, *world, fix, add);
	return true;
}

} // namespace

bool world_selected(const std::string &world, const std::string &name)
{
	for (auto entry : gostr::fields(world)) {
		auto i = entry.find_first_of("=<>~@");
		if (i != std::string::npos) entry.resize(i);
		if (entry == name) return true;
	}
	return false;
}

std::string run(const std::string &action, const std::string &name)
{
	std::string error;
	auto lock = osupdate::lock(error);
	if (!lock) return error;
	return osupdate::run_software(action, name);
}

std::string install_community(const std::vector<std::string> &names)
{
	bool changes = false;
	return install_community(false, {}, names, changes);
}

std::string install_only(const std::vector<std::string> &names)
{
	if (names.empty()) return "no APK package to install";
	for (const auto &name : names)
		if (!valid(name)) return "invalid APK package name";
	std::string error;
	auto lock = osupdate::lock(error);
	if (!lock) return error;
	Apk apk{apkrun::Clock::now() + std::chrono::minutes(30)};
	std::string output;
	if (!apk({"update"}, output)) return "apk update failed: " + tail(output);
	if (!apk(with({"add", "--simulate", "--"}, names), output)) return "apk add failed: " + tail(output);
	auto changed = osupdate::other_install_changes(output, names);
	if (!changed.empty())
		return "installing " + gostr::join(names, ", ") + " would change other packages (" + gostr::join(changed, ", ") +
		       "); update NanoKVM OS first";
	if (!apk(with({"add", "--"}, names), output)) return "apk add failed: " + tail(output);
	return "";
}

namespace {

std::string valid_names(const std::vector<std::string> &names)
{
	if (names.empty()) return "no APK package to remove";
	for (const auto &name : names)
		if (!valid(name)) return "invalid APK package name";
	return "";
}

std::string del(const Apk &apk, const std::vector<std::string> &names)
{
	std::string output;
	if (!apk(with({"del", "--"}, names), output)) return "apk del failed: " + tail(output);
	return "";
}

std::string join(const std::vector<std::string> &names)
{
	std::string out;
	for (std::size_t i = 0; i < names.size(); i++) out += (i ? ", " : "") + names[i];
	return out;
}

} // namespace

std::string remove(const std::vector<std::string> &names)
{
	if (auto e = valid_names(names); !e.empty()) return e;
	std::string error;
	auto lock = osupdate::lock(error);
	if (!lock) return error;
	return del(Apk{apkrun::Clock::now() + std::chrono::minutes(5)}, names);
}

std::string remove_after(const std::function<Prepared()> &prepare, const std::function<std::string()> &then,
                         const std::vector<std::string> &names, bool &removed)
{
	removed = false;
	if (auto e = valid_names(names); !e.empty()) return e;
	std::string error;
	auto lock = osupdate::lock(error);
	if (!lock) return error;
	auto present = osupdate::installed(names, error);
	if (!present) return "cannot read the installed packages: " + error;
	if (present->empty()) return then ? then() : "";
	Apk apk{apkrun::Clock::now() + std::chrono::minutes(5)};
	std::string output;
	if (!apk(with({"del", "--simulate", "--"}, *present), output)) return "apk del failed: " + tail(output);
	if (auto kept = osupdate::kept_by_removal(output, *present); !kept.empty())
		return "apk would not remove " + join(kept) + ": " + tail(output);
	std::function<std::string()> undo;
	if (prepare) {
		auto p = prepare();
		if (!p.error.empty()) return p.error;
		undo = std::move(p.undo);
	}
	std::string err = del(apk, *present);
	if (err.empty()) {
		// Only a database read proves the removal; a failed one counts as a package left installed.
		std::string check;
		auto left = osupdate::installed(names, check);
		if (!left) err = "cannot confirm that apk removed " + join(*present) + ": " + check;
		else if (!left->empty()) err = "apk del left " + join(*left) + " installed";
	}
	if (!err.empty()) {
		// errors.Join: the texts on separate lines, a nil one left out.
		if (undo)
			if (auto u = undo(); !u.empty()) err += "\n" + u;
		return err;
	}
	removed = true;
	return then ? then() : "";
}

std::string keep(const std::string &name)
{
	if (!valid(name)) return "invalid APK package name";
	if (world_selected(osupdate::files::read(osupdate::world_path).value_or(""), name)) return "";
	std::string error;
	auto lock = osupdate::lock(error);
	if (!lock) return error;
	Apk apk{apkrun::Clock::now() + std::chrono::minutes(5)};
	std::string output;
	if (!apk({"add", "--no-network", "--", name}, output)) return "apk add failed: " + tail(output);
	return "";
}

bool selected(const std::string &name, std::string &error)
{
	int err = 0;
	auto world = osupdate::files::read(osupdate::world_path, err);
	if (!world) {
		error = osupdate::files::read_error(osupdate::world_path, err);
		return false;
	}
	return world_selected(*world, name);
}

void base_plan(const std::vector<std::string> &missing, const std::vector<std::string> &broken, const std::string &world,
               std::vector<std::string> &fix, std::vector<std::string> &add)
{
	auto entries = gostr::fields(world);
	auto has = [](const std::vector<std::string> &list, const std::string &s) { return std::find(list.begin(), list.end(), s) != list.end(); };
	for (const auto &name : base_packages) {
		if (has(entries, "!" + name)) continue;
		bool chosen = world_selected(world, name);
		if (!chosen) add.push_back(name);
		if (has(broken, name) || (chosen && has(missing, name))) fix.push_back(name);
	}
	for (const auto &name : broken)
		if (!has(base_packages, name)) fix.push_back(name);
}

namespace {

std::string install_base(bool &changes)
{
	changes = false;
	std::vector<std::string> fix, add;
	std::string error;
	if (!base_work(fix, add, error)) return error;
	if (fix.size() + add.size() > 0) {
		if (auto e = install_community(true, fix, add, changes); !e.empty()) return e;
		if (!base_work(fix, add, error)) return error;
		if (fix.size() + add.size() > 0) {
			std::vector<std::string> all = fix;
			all.insert(all.end(), add.begin(), add.end());
			return "not installed completely: " + gostr::join(all, ", ");
		}
		log::info("base packages installed");
	}
	if (auto e = osupdate::files::mkdir_all("/etc/kvm", 0755); !e.empty()) return e;
	int fd = ::open(base_marker, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0644);
	if (fd < 0) return goerr::path_error("open", base_marker, errno);
	::close(fd);
	return "";
}

} // namespace

std::string install_base()
{
	bool changes = false;
	return install_base(changes);
}

void start()
{
	if (osupdate::files::exists(base_marker)) return;
	std::thread([] {
		auto delay = std::chrono::minutes(2), refused = std::chrono::minutes(5);
		std::string last;
		for (;;) {
			std::this_thread::sleep_for(delay);
			bool changes = false;
			std::string e = install_base(changes);
			if (e.empty()) return;
			if (e != last) {
				log::warn("base packages: {}; trying again later", e);
				last = e;
			}
			delay = std::chrono::minutes(5);
			if (changes) {
				delay = refused;
				refused = std::min(2 * refused, std::chrono::minutes(6 * 60));
			}
		}
	}).detach();
}

} // namespace nkos::apkpkg
