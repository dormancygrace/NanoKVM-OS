#include "usb/composition.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "storage/storage.hpp"
#include "usb/core_hooks.hpp"
#include "usb/gadget.hpp"
#include "usb/gotext.hpp"
#include "usb/remote_access.hpp"
#include "usb/requests.hpp"
#include "usb/usbinternet.hpp"
#include "video/audio.hpp"

#include <cerrno>
#include <climits>
#include <ctime>
#include <fcntl.h>
#include <format>
#include <future>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::usb {

namespace {

constexpr const char *user_disk_busy = "the user disk is busy; wait for the current operation to finish";
constexpr const char *not_gadget = "the USB port is not in gadget mode";
constexpr const char *user_disk_missing = "create the user disk first";

Composition live() { return read_composition(flags_dir, gadget_root); }

std::string live_json(const Composition &s)
{
	return response_json(s, role_flags(flags_dir), port_power(), chip_serial_number());
}

// audio.Stop: ALSA is let go before the gadget changes (the audio hub lives on the loop thread).
std::string stop_audio()
{
	std::promise<void> done;
	auto f = done.get_future();
	loop::post([&done] {
		video::audio::stop();
		done.set_value();
	});
	f.wait();
	return {};
}

// sh S03usbdev <action>, 20 s.
std::string run_script(const std::string &action)
{
	process::Options options;
	options.timeout = std::chrono::seconds(20);
	auto r = process::run({"sh", usb_dev_script, action}, options);
	if (r.ok()) return {};
	std::string why = !r.started ? "exec: \"sh\": " + r.error : r.timed_out ? "signal: killed"
	                  : r.signal ? "signal: " + std::to_string(r.signal) : "exit status " + std::to_string(r.exit_code);
	return "USB " + action + ": " + why + ": " + r.output;
}

// hid.InstallModeScript: the mode's script of the application package replaces the active one (the actual file
// behind Alpine's compatibility symlink), without a rebind.
std::string install_mode_script(const std::string &mode)
{
	std::string src = mode_normal_script;
	if (mode == "hid-only") src = mode_hid_only_script;
	else if (mode != "normal") return "invalid HID mode " + gotext::quote(mode);
	std::string dst = usb_dev_script;
	struct stat st{};
	if (::lstat(dst.c_str(), &st) == 0 && S_ISLNK(st.st_mode)) {
		char resolved[PATH_MAX];
		if (!::realpath(dst.c_str(), resolved)) return "resolve USB init script: lstat " + dst + ": " + gotext::errno_text(errno);
		dst = resolved;
	} else if (::lstat(dst.c_str(), &st) != 0 && errno != ENOENT) {
		return "lstat " + dst + ": " + gotext::errno_text(errno);
	}
	int in = ::open(src.c_str(), O_RDONLY | O_CLOEXEC);
	if (in < 0) {
		std::string e = "open " + src + ": " + gotext::errno_text(errno);
		log::error("failed to open {}: {}", src, e);
		return e;
	}
	struct stat src_st{};
	::fstat(in, &src_st);
	std::string tmp = dst.substr(0, dst.rfind('/')) + "/.S03usbdev-XXXXXX";
	int out = ::mkstemp(tmp.data());
	if (out < 0) {
		std::string e = "open " + tmp + ": " + gotext::errno_text(errno);
		::close(in);
		log::error("failed to create temp {}: {}", usb_dev_script, e);
		return e;
	}
	std::string error;
	if (::fchmod(out, src_st.st_mode & 07777) != 0) error = "chmod " + tmp + ": " + gotext::errno_text(errno);
	char buf[8192];
	for (ssize_t n; error.empty() && (n = ::read(in, buf, sizeof buf)) != 0;) {
		if (n < 0) {
			if (errno == EINTR) continue;
			error = "read " + src + ": " + gotext::errno_text(errno);
		} else if (::write(out, buf, static_cast<std::size_t>(n)) != n) {
			error = "write " + tmp + ": " + gotext::errno_text(errno);
		}
	}
	if (error.empty() && ::fsync(out) != 0) error = "sync " + tmp + ": " + gotext::errno_text(errno);
	::close(in);
	if (::close(out) != 0 && error.empty()) error = "close " + tmp + ": " + gotext::errno_text(errno);
	if (error.empty() && ::rename(tmp.c_str(), dst.c_str()) != 0)
		error = "rename " + tmp + " " + dst + ": " + gotext::errno_text(errno);
	::unlink(tmp.c_str());
	if (!error.empty()) log::error("failed to install the USB init script: {}", error);
	return error;
}

// applyUSBNetwork: S30usbnet sets up or removes the USB network's address, DHCP and input filter (20 s).
std::string apply_usb_network()
{
	process::Options options;
	options.timeout = std::chrono::seconds(20);
	options.capture = false;
	auto r = process::run({"sh", "/etc/init.d/S30usbnet", "start"}, options);
	if (r.ok()) return {};
	return "apply USB network: " + std::string(r.timed_out ? "signal: killed" : r.started ? "exit status " + std::to_string(r.exit_code)
	                                                                                      : "exec: \"sh\": " + r.error);
}

// retireLegacyACMGetty: the browser terminal owns ttyGS0, so a getty that inittab starts on it goes for good.
std::string retire_legacy_acm_getty()
{
	bool changed = false;
	if (auto e = rewrite_inittab(inittab_path, changed); !e.empty()) return e;
	if (!changed) return {};
	// BusyBox init re-reads inittab on SIGHUP and releases the old getty.
	auto r = process::run({"kill", "-HUP", "1"});
	return r.ok() ? "" : "exit status " + std::to_string(r.exit_code);
}

enum class Failure { none, busy, missing, not_gadget, other };
struct ApplyResult {
	Failure failure = Failure::none;
	std::string error;
};

// applyLiveUSBComposition; the caller holds storage::gadget_mutex().
ApplyResult apply_live(const Composition &current, const Composition &candidate)
{
	if (current == candidate) return {};
	// In the host role, and switched off, there is no gadget to rebuild: the role is changed first.
	if (role_flags(flags_dir) != "gadget") return {Failure::not_gadget, not_gadget};
	// Attaching or detaching the user disk excludes creating or deleting the image for the whole apply.
	std::function<void()> unlock_disk;
	struct Unlock {
		std::function<void()> &f;
		~Unlock()
		{
			if (f) f();
		}
	} unlock{unlock_disk};
	if (current.user_disk || candidate.user_disk) {
		std::string error;
		auto unlock_fn = storage::lock_user_disk_for_composition(error);
		if (!unlock_fn) return {error == user_disk_busy ? Failure::busy : Failure::other, error};
		unlock_disk = *unlock_fn;
		if (candidate.user_disk && !storage::user_disk_exists()) return {Failure::missing, user_disk_missing};
	}
	if (auto e = stop_audio(); !e.empty()) return {Failure::other, e};
	if (candidate.serial && !current.serial)
		if (auto e = retire_legacy_acm_getty(); !e.empty()) return {Failure::other, e};
	CompositionStore store{flags_dir, usb_dev_script, install_mode_script, run_script,
	                       [](const Composition &s) { return verify_composition_at(gadget_root, s); }};
	auto reopen = input::hid::hold_closed();
	std::string error = store.apply(current, candidate);
	// S03usbdev only binds the gadget: a USB network that comes or goes needs its address, DHCP and input filter
	// now. The composition stands either way; the USB network's status reports a failure.
	if (error.empty() && current.network != candidate.network)
		if (auto e = apply_usb_network(); !e.empty()) log::error("USB network after composition change: {}", e);
	reopen();
	if (!error.empty()) return {Failure::other, error};
	return {};
}

// setLiveUSBRole; the caller holds storage::gadget_mutex().
std::string set_live_role(const std::string &role)
{
	RoleStore store{flags_dir, run_script,
	                [](const std::string &r) { return verify_role_at(usb_role_switch_glob, gadget_root, r, live()); },
	                stop_audio};
	auto reopen = input::hid::hold_closed();
	bool network_before = role_flags(flags_dir) == "gadget";
	std::string error = store.set(role);
	// The USB network exists with the bound gadget only: its address, DHCP and input filter follow.
	if (error.empty() && network_before != (role == "gadget") && live().network)
		if (auto e = apply_usb_network(); !e.empty()) log::error("USB network after role change: {}", e);
	reopen();
	return error;
}

// setUSBNetworkFlag: one USB network flag saved and applied without a gadget rebind.
void set_network_flag(http::Request &req, http::Response &res, const std::string &name,
                      bool (*current)(const usbinternet::Runtime &), const std::string &what)
{
	bool enabled = false, expected = false;
	if (!bind_flag({req.header("content-type"), req.body, req.query}, enabled, expected))
		return api::err(res, -1, "invalid " + what + " setting");
	http::defer(req, res, [name, current, what, enabled, expected](http::Response &out) {
		std::lock_guard lock(storage::gadget_mutex());
		auto runtime = usbinternet::default_runtime();
		if (current(runtime) != expected) return api::err(out, -5, what + " setting changed; refresh and try again");
		struct stat st{};
		if (::stat("/usr/sbin/nkos-usb-internet", &st) != 0) {
			if (errno != ENOENT) log::error("check file /usr/sbin/nkos-usb-internet err: {}", gotext::errno_text(errno));
			return api::err(out, -4, what + " requires updated Alpine base package");
		}
		if (auto e = save_usb_flag(runtime.boot, name, enabled, apply_usb_network); !e.empty()) {
			log::error("{}: {}", what, e);
			return api::err(out, -3, "failed to apply " + what + " setting");
		}
		api::send(out, 0, "success",
		          usbinternet::status_json(usbinternet::read_status(runtime, static_cast<long long>(std::time(nullptr)))));
	});
}

} // namespace

std::string state_json() { return live_json(live()); }

// EnableRemoteAccessUSB: RustDesk turned on switches USB to its preset.
std::string enable_remote_access_usb(bool with_audio)
{
	std::lock_guard lock(storage::gadget_mutex());
	Composition current = live();
	return enable_remote_access(remote_access_marker_path, current, with_audio, [&current](const Composition &candidate) {
		if (current == candidate) return verify_composition_at(gadget_root, candidate);
		return apply_live(current, candidate).error;
	});
}

// EnsureRemoteAccessUSB: sound turned on while RustDesk runs adds what it needs; other functions stay.
std::string ensure_remote_access_usb(bool with_audio)
{
	std::lock_guard lock(storage::gadget_mutex());
	Composition current = live();
	Composition candidate = remote_access_defaults(current, with_audio);
	if (auto e = validate(candidate); !e.empty()) return e;
	if (current == candidate) return verify_composition_at(gadget_root, candidate);
	return follow_remote_access_marker(remote_access_marker_path, current, candidate,
	                                   [&current](const Composition &next) { return apply_live(current, next).error; });
}

// RestoreUSBAfterRemoteAccess: the composition RustDesk replaced comes back.
std::string restore_usb_after_remote_access()
{
	std::lock_guard lock(storage::gadget_mutex());
	Composition current = live();
	return restore_remote_access(remote_access_marker_path, current, [&current](const Composition &b) {
		Composition before = b;
		// The user disk image may have been deleted while RustDesk was on.
		if (before.user_disk && !storage::user_disk_exists()) before.user_disk = false;
		return apply_live(current, before).error;
	});
}

void add_routes(http::Router &router)
{
	const auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/device/virtual", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::lock_guard lock(storage::gadget_mutex());
			api::send(out, 0, "success", state_json());
		});
	}, admin));

	// The single-device toggle of older clients, through the same budget, rollback and rebind as a composition.
	router.add("POST", "/api/vm/device/virtual", auth::guarded([](http::Request &req, http::Response &res) {
		std::string device;
		if (!bind_device({req.header("content-type"), req.body, req.query}, device)) return api::err(res, -1, "invalid argument");
		if (device != "network" && device != "disk" && device != "serial" && device != "audio")
			return api::err(res, -2, "invalid arguments");
		http::defer(req, res, [device](http::Response &out) {
			std::lock_guard lock(storage::gadget_mutex());
			Composition current = live();
			Composition candidate = with(current, device, !enabled(current, device));
			if (auto e = validate(candidate); !e.empty()) return api::err(out, -4, e);
			if (auto r = apply_live(current, candidate); r.failure != Failure::none) {
				log::error("update USB device: {}", r.error);
				return api::err(out, -3, "operation failed");
			}
			api::send(out, 0, "success", std::format(R"({{"on":{}}})", enabled(candidate, device) ? "true" : "false"));
		});
	}, admin));

	// A whole composition in one apply.
	router.add("PUT", "/api/vm/device/virtual", auth::guarded([](http::Request &req, http::Response &res) {
		CompositionRequest r;
		if (!bind_composition({req.header("content-type"), req.body, req.query}, r))
			return api::err(res, -1, "invalid composition");
		Composition candidate;
		candidate.mode = r.mode;
		candidate.keyboard = *r.keyboard;
		candidate.relative = *r.relative;
		candidate.absolute = *r.absolute;
		candidate.network = *r.network;
		candidate.disk = *r.disk;
		candidate.serial = *r.serial;
		candidate.audio = *r.audio;
		std::string error;
		if (!identity_from_request(r.identity, candidate.identity, error)) return api::err(res, -4, error);
		if (r.storage_identity) candidate.storage_identity = normalized(*r.storage_identity);
		if (auto e = validate(candidate); !e.empty()) return api::err(res, -4, e);
		http::defer(req, res, [r, candidate](http::Response &out) mutable {
			std::lock_guard lock(storage::gadget_mutex());
			Composition current = live();
			// An older client sends no identity: it keeps the one in place.
			if (!r.identity) candidate.identity = current.identity;
			if (!r.storage_identity) candidate.storage_identity = current.storage_identity;
			// The same for the user disk; it needs the disk function and an image.
			candidate.user_disk = r.user_disk ? *r.user_disk : current.user_disk;
			if (candidate.user_disk && !candidate.disk) candidate.user_disk = false;
			if (candidate.user_disk && !storage::user_disk_exists()) return api::err(out, -4, user_disk_missing);
			if (revision(current) != r.revision) return api::err(out, -5, "USB composition changed; refresh and try again");
			if (auto a = apply_live(current, candidate); a.failure != Failure::none) {
				log::error("apply USB composition: {}", a.error);
				switch (a.failure) {
				case Failure::busy: return api::err(out, -6, a.error);
				case Failure::missing: return api::err(out, -4, a.error);
				case Failure::not_gadget: return api::err(out, -7, a.error);
				default: return api::err(out, -3, "failed to apply USB composition");
				}
			}
			api::send(out, 0, "success", state_json());
		});
	}, admin));

	router.add("PUT", "/api/vm/usb/role", auth::guarded([](http::Request &req, http::Response &res) {
		std::string role;
		if (!bind_role({req.header("content-type"), req.body, req.query}, role)) return api::err(res, -1, "invalid role");
		http::defer(req, res, [role](http::Response &out) {
			std::lock_guard lock(storage::gadget_mutex());
			if (auto e = set_live_role(role); !e.empty()) {
				log::error("set USB role {}: {}", role, e);
				return api::err(out, -3, "failed to change the USB role");
			}
			api::send(out, 0, "success", state_json());
		});
	}, admin));

	router.add("GET", "/api/vm/usb/devices", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			api::send(out, 0, "success", host_devices_json(host_devices_at("/sys/bus/usb/devices"), port_power()));
		});
	}, admin));

	router.add("GET", "/api/vm/usb-internet", auth::guarded([](http::Request &, http::Response &res) {
		auto runtime = usbinternet::default_runtime();
		api::send(res, 0, "success",
		          usbinternet::status_json(usbinternet::read_status(runtime, static_cast<long long>(std::time(nullptr)))));
	}, admin));
	router.add("PUT", "/api/vm/usb-internet", auth::guarded([](http::Request &req, http::Response &res) {
		set_network_flag(req, res, "usb.internet", usbinternet::enabled, "USB internet");
	}, admin));
	// Whether the managed computer may open every service of the NanoKVM over USB (the usb0 input filter off).
	router.add("PUT", "/api/vm/usb-host-access", auth::guarded([](http::Request &req, http::Response &res) {
		set_network_flag(req, res, usbinternet::host_access_flag, usbinternet::host_access, "USB host access");
	}, admin));
}

} // namespace nkos::usb
