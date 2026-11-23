#include "usb/remote_access.hpp"

#include "common/gojson.hpp"
#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"

#include <cerrno>
#include <unistd.h>

namespace nkos::usb {

namespace {

// usbFlags: a composition without its identity, which RustDesk leaves alone.
struct Flags {
	std::string mode;
	bool keyboard = false, relative = false, absolute = false, network = false, disk = false, serial = false,
	     audio = false, user_disk = false;
	bool operator==(const Flags &) const = default;
};

Flags flags_of(const Composition &s)
{
	return {s.mode, s.keyboard, s.relative, s.absolute, s.network, s.disk, s.serial, s.audio, s.user_disk};
}

Composition with_identity(const Flags &f, const Identity &identity)
{
	Composition s;
	s.mode = f.mode;
	s.keyboard = f.keyboard;
	s.relative = f.relative;
	s.absolute = f.absolute;
	s.network = f.network;
	s.disk = f.disk;
	s.serial = f.serial;
	s.audio = f.audio;
	s.user_disk = f.user_disk;
	s.identity = identity;
	return s;
}

struct Marker {
	Flags before, applied;
};

std::vector<goj::Field> flag_fields(Flags &f)
{
	return {{"mode", [&f](goj::Reader &r) { r.string(f.mode); }},
	        {"keyboard", [&f](goj::Reader &r) { r.boolean(f.keyboard); }},
	        {"relative", [&f](goj::Reader &r) { r.boolean(f.relative); }},
	        {"absolute", [&f](goj::Reader &r) { r.boolean(f.absolute); }},
	        {"network", [&f](goj::Reader &r) { r.boolean(f.network); }},
	        {"disk", [&f](goj::Reader &r) { r.boolean(f.disk); }},
	        {"serial", [&f](goj::Reader &r) { r.boolean(f.serial); }},
	        {"audio", [&f](goj::Reader &r) { r.boolean(f.audio); }},
	        {"userDisk", [&f](goj::Reader &r) { r.boolean(f.user_disk); }}};
}

// readRemoteAccessMarker: a file that cannot be read or decoded is no marker.
bool read_marker(const std::string &path, Marker &m)
{
	m = {};
	FileSnapshot snapshot;
	if (!snapshot_file(path, snapshot).empty() || !snapshot.exists) {
		// os.ReadFile reads what is there, a regular file or not; only a regular one can hold a marker.
		return false;
	}
	std::string error;
	Marker decoded;
	bool ok = goj::unmarshal(snapshot.data, "remoteAccessMarker", [&decoded](goj::Reader &r) {
		r.object("remoteAccessMarker", "vm.remoteAccessMarker",
		         {{"before", [&decoded](goj::Reader &v) { v.object("usbFlags", "vm.usbFlags", flag_fields(decoded.before)); }},
		          {"applied", [&decoded](goj::Reader &v) { v.object("usbFlags", "vm.usbFlags", flag_fields(decoded.applied)); }}});
	}, error);
	if (!ok) return false;
	m = decoded;
	return true;
}

std::string flags_json(const Flags &f)
{
	auto b = [](bool v) { return v ? "true" : "false"; };
	return "{\"mode\":" + gojson::quote(f.mode) + ",\"keyboard\":" + b(f.keyboard) + ",\"relative\":" + b(f.relative) +
	       ",\"absolute\":" + b(f.absolute) + ",\"network\":" + b(f.network) + ",\"disk\":" + b(f.disk) +
	       ",\"serial\":" + b(f.serial) + ",\"audio\":" + b(f.audio) + ",\"userDisk\":" + b(f.user_disk) + "}";
}

std::string write_marker(const std::string &path, const Marker &m)
{
	return write_usb_file(path, {true, 0644, "{\"before\":" + flags_json(m.before) + ",\"applied\":" + flags_json(m.applied) + "}\n"});
}

// os.Remove: unlink, then rmdir; the unlink's error unless the path is a directory. missing: the error is ENOENT.
std::string remove_path(const std::string &path, bool *missing = nullptr)
{
	if (::unlink(path.c_str()) == 0) return {};
	int e = errno;
	if (::rmdir(path.c_str()) == 0) return {};
	if (errno != ENOTDIR) e = errno;
	if (missing) *missing = e == ENOENT;
	return "remove " + path + ": " + gotext::errno_text(e);
}

// applyWithRemoteAccessMarker: the marker is written before the gadget is touched; a failed apply (which rolls
// itself back) puts the previous marker back too.
std::string apply_with_marker(const std::string &path, const Marker &marker, const Composition &candidate,
                              const ApplyComposition &apply)
{
	FileSnapshot previous;
	if (auto e = snapshot_file(path, previous); !e.empty()) return e;
	if (auto e = write_marker(path, marker); !e.empty()) return e;
	if (auto e = apply(candidate); !e.empty()) {
		if (auto r = write_usb_file(path, previous); !r.empty()) return e + "\nrestore RustDesk USB marker: " + r;
		return e;
	}
	return {};
}

} // namespace

Composition remote_access_profile(const Composition &s, bool with_audio)
{
	Composition p;
	p.mode = "normal";
	p.keyboard = p.relative = p.absolute = true;
	p.disk = true;
	p.audio = with_audio;
	p.identity = s.identity;
	p.storage_identity = s.storage_identity;
	p.user_disk = s.disk && s.user_disk;
	return p;
}

Composition remote_access_defaults(Composition s, bool with_audio)
{
	s.keyboard = s.relative = s.absolute = true;
	if (with_audio) {
		s.audio = true;
		s.mode = "normal";
	}
	return s;
}

std::string enable_remote_access(const std::string &path, const Composition &current, bool with_audio,
                                 const ApplyComposition &apply)
{
	Composition candidate = remote_access_profile(current, with_audio);
	if (auto e = validate(candidate); !e.empty()) return e;
	// A marker left by a failed restore still holds the first composition while nothing has changed since.
	Marker marker;
	bool ok = read_marker(path, marker);
	if (!ok || flags_of(current) != marker.applied) marker.before = flags_of(current);
	marker.applied = flags_of(candidate);
	return apply_with_marker(path, marker, candidate, apply);
}

std::string follow_remote_access_marker(const std::string &path, const Composition &current, const Composition &candidate,
                                        const ApplyComposition &apply)
{
	Marker marker;
	if (!read_marker(path, marker) || flags_of(current) != marker.applied) return apply(candidate);
	marker.applied = flags_of(candidate);
	return apply_with_marker(path, marker, candidate, apply);
}

std::string restore_remote_access(const std::string &path, const Composition &current, const ApplyComposition &apply)
{
	Marker marker;
	if (!read_marker(path, marker)) {
		bool missing = false;
		if (auto e = remove_path(path, &missing); !e.empty() && !missing) return e;
		return {};
	}
	if (flags_of(current) == marker.applied) {
		Composition before = with_identity(marker.before, current.identity);
		before.storage_identity = current.storage_identity;
		if (auto e = validate(before); !e.empty()) return e;
		// Kept on failure: turning RustDesk on and off again retries.
		if (auto e = apply(before); !e.empty()) return e;
	}
	return remove_path(path);
}

} // namespace nkos::usb
