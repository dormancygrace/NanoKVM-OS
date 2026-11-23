#pragma once

// The USB composition for RustDesk, as the Go server's service/vm remote-access-usb.go and EnsureRemoteAccessUSB.
// Turning RustDesk on switches USB to its preset (the three HID functions, the virtual disk and, when RustDesk sends
// sound, audio; the identity and the user disk carry over) and remembers the composition it replaced in a marker
// file; turning it off puts that one back, unless the composition was changed in the meantime: then the user's
// choice stays. Sound turned on while RustDesk runs adds audio and keeps the marker in step.
//
// The marker logic takes the marker's path, the current composition and the apply as arguments (Go's injection
// points); the live wrappers (in composition.cpp, next to the apply they use) take storage::gadget_mutex().

#include "usb/gadget.hpp"

#include <functional>
#include <string>

namespace nkos::usb {

inline constexpr const char *remote_access_marker_path = "/etc/kvm/usb-before-rustdesk.json";

using ApplyComposition = std::function<std::string(const Composition &)>;

// remoteAccessProfile and remoteAccessDefaults.
Composition remote_access_profile(const Composition &s, bool with_audio);
Composition remote_access_defaults(Composition s, bool with_audio);

// enableRemoteAccessUSB, followRemoteAccessMarker, restoreRemoteAccessUSB: error text or empty.
std::string enable_remote_access(const std::string &path, const Composition &current, bool with_audio,
                                 const ApplyComposition &apply);
std::string follow_remote_access_marker(const std::string &path, const Composition &current, const Composition &candidate,
                                        const ApplyComposition &apply);
std::string restore_remote_access(const std::string &path, const Composition &current, const ApplyComposition &apply);

// EnableRemoteAccessUSB, EnsureRemoteAccessUSB, RestoreUSBAfterRemoteAccess on the live gadget. Blocking.
std::string enable_remote_access_usb(bool with_audio);
std::string ensure_remote_access_usb(bool with_audio);
std::string restore_usb_after_remote_access();

} // namespace nkos::usb
