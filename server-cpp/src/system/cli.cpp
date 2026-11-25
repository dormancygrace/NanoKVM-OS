#include "system/cli.hpp"

#include "auth/store.hpp"
#include "osupdate/osupdate.hpp"
#include "recovery/recovery.hpp"

#include <cstdio>
#include <string>
#include <vector>

namespace nkos::system {

namespace {

int totp_reset(const std::vector<std::string> &args)
{
	if (args.size() != 1 || args[0].empty() || args[0] == "-h" || args[0] == "--help") {
		std::fputs("usage: nkos-totp-reset USER\n", stderr);
		std::fputs("Turns off two-factor authentication for a NanoKVM web account.\n", stderr);
		return 2;
	}
	bool was = false;
	std::string error;
	auto user = auth::store().modify(args[0], [&](auth::User &u, bool &save) -> std::optional<std::string> {
		was = u.totp.has_value();
		u.totp.reset();
		save = was;
		return std::nullopt;
	}, error);
	if (!user) {
		std::fprintf(stderr, "nkos-totp-reset: %s: %s\n", args[0].c_str(), error.c_str());
		return 1;
	}
	if (was) std::printf("Two-factor authentication is off for %s.\n", args[0].c_str());
	else std::printf("%s does not use two-factor authentication.\n", args[0].c_str());
	return 0;
}

std::string stage_factory_reset()
{
	std::string error;
	auto lock = osupdate::lock(error);
	if (!lock) return error;
	recovery::Engine e = recovery::live();
	recovery::Plan plan;
	recovery::Preview preview;
	recovery::Options options{"reset", "", false, true};
	if (!e.prepare(nullptr, options, plan, preview, error)) return error;
	return e.stage(plan);
}

int factory_reset(const std::vector<std::string> &args)
{
	if (!args.empty()) {
		std::fputs("usage: NanoKVM-Server factory-reset\n", stderr);
		std::fputs("Resets the settings (installed software stays) and reboots.\n", stderr);
		return 2;
	}
	if (!recovery::available()) {
		std::fputs("factory-reset: update nanokvm-base before resetting settings\n", stderr);
		return 1;
	}
	if (auto e = stage_factory_reset(); !e.empty()) {
		std::fprintf(stderr, "factory-reset: %s\n", e.c_str());
		return 1;
	}
	if (auto e = recovery::reboot(); !e.empty()) {
		std::fprintf(stderr, "factory-reset: reboot: %s\n", e.c_str());
		return 1;
	}
	return 0;
}

} // namespace

int run_cli(int argc, char **argv)
{
	if (argc < 2) return -1;
	std::string mode = argv[1];
	std::vector<std::string> args(argv + 2, argv + argc);
	if (mode == "totp-reset") return totp_reset(args);
	if (mode == "factory-reset") return factory_reset(args);
	return -1;
}

} // namespace nkos::system
