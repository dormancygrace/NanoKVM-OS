#pragma once

// The USB gadget's composition, role and identity, as the Go server's service/vm virtual-device.go,
// usb-composition.go, usb-role.go, usb_identity.go and internal/storageidentity, and the status of USB internet
// sharing (usbinternet.Runtime.ReadStatus). S03usbdev reads the flags in /etc/kvm/usb at every start: which
// functions the computer sees (keyboard, relative and absolute mouse, network, virtual disk, serial console,
// speaker), the port's role (gadget, host, off), the identity the computer sees and the virtual disks' SCSI names.
//
// Everything here takes the directories it reads and the commands it runs as arguments (Go's injection points), so
// that the same code runs on the device and against fixture trees. Errors are Go's texts; "" is success.

#include <functional>
#include <map>
#include <optional>
#include <string>
#include <utility>
#include <vector>

#include <sys/types.h>

namespace nkos::usb {

inline constexpr const char *flags_dir = "/etc/kvm/usb";
inline constexpr const char *gadget_root = "/sys/kernel/config/usb_gadget/g0";
inline constexpr const char *usb_dev_script = "/etc/init.d/S03usbdev";
inline constexpr const char *mode_normal_script = "/kvmapp/system/init.d/S03usbdev";
inline constexpr const char *mode_hid_only_script = "/kvmapp/system/init.d/S03usbhid";
inline constexpr const char *user_disk_image = "/data/.nanokvm/user-disk.img";
inline constexpr const char *chip_uid_path = "/sys/class/cvi-base/base_uid";
inline constexpr const char *usb_role_switch_glob = "/sys/class/usb_role/*/role";
inline constexpr const char *inittab_path = "/etc/inittab";

// --- identity (usb_identity.go) ---

struct Identity {
	std::string vendor_id, product_id, manufacturer, product, serial_number;  // "" serial: the chip UID
	bool operator==(const Identity &) const = default;
};
extern const Identity default_identity;

// normalizeUSBID: 0xNNNN, or the error.
std::optional<std::string> normalize_usb_id(const std::string &value, std::string &error);
Identity normalized(const Identity &i);
std::string validate(const Identity &i);
bool is_default(const Identity &i);
// identityFromRequest: the request's identity checked as typed, then normalised; nullopt (no identity) is the zero one.
bool identity_from_request(const std::optional<Identity> &request, Identity &out, std::string &error);
// What the flags directory holds for the identity: a value per file that differs from the default, nullopt to remove.
std::map<std::string, std::optional<std::string>> identity_files(const Identity &i);
Identity read_identity(const std::string &dir);
std::string gadget_identity_at(const std::string &root, Identity &out);
std::string gadget_serial_number_at(const std::string &root);
std::string chip_serial_number(const std::string &uid_path = chip_uid_path);

// --- storage identity (internal/storageidentity) ---

struct StorageIdentity {
	std::string vendor, image, setup, user;
	bool operator==(const StorageIdentity &) const = default;
};
extern const std::vector<std::string> storage_identity_files;
StorageIdentity normalized(const StorageIdentity &i);
std::string validate(const StorageIdentity &i);
std::map<std::string, std::optional<std::string>> file_values(const StorageIdentity &i);
StorageIdentity read_storage_identity(const std::string &dir);
std::string inquiry(const StorageIdentity &i, const std::string &kind);

// --- composition ---

struct Composition {
	std::string mode;
	bool keyboard = false, relative = false, absolute = false, network = false, disk = false, serial = false,
	     audio = false;
	Identity identity;
	StorageIdentity storage_identity;
	bool user_disk = false;
	bool operator==(const Composition &) const = default;
};

// hid.getMode for a gadget directory: "normal" or "hid-only", or the error (logged as Go does).
std::string gadget_mode_at(const std::string &gadget, std::string &error);
// getUSBComposition: the flags of dir with the gadget's mode ("normal" when it cannot be read).
Composition read_composition(const std::string &dir, const std::string &gadget);
std::string validate(const Composition &s);
std::pair<int, int> endpoint_usage(const Composition &s);
bool empty(const Composition &s);
bool enabled(const Composition &s, const std::string &device);
Composition with(Composition s, const std::string &device, bool on);
std::string revision(const Composition &s);
// The JSON of GET /api/vm/device/virtual (proto.GetVirtualDeviceRsp) for a composition in a role.
std::string response_json(Composition s, const std::string &role, const std::string &port_power,
                          const std::string &chip_serial);

// --- the boot files of a composition ---

struct FileSnapshot {
	bool exists = false;
	mode_t mode = 0;
	std::string data;
};
std::string snapshot_file(const std::string &path, FileSnapshot &out);
// Replaces (through a temporary file and a rename) or removes the file, then syncs the directory.
std::string write_usb_file(const std::string &path, const FileSnapshot &snapshot);
extern const std::vector<std::string> composition_files;

// usbCompositionStore: one stop/start for a whole draft; failures restore the previous files and running state.
struct CompositionStore {
	std::string boot_dir, script_path;
	std::function<std::string(const std::string &mode)> install;
	std::function<std::string(const std::string &action)> run;
	std::function<std::string(const Composition &)> verify;
	std::string apply(const Composition &current, const Composition &candidate) const;
};
std::string verify_composition_at(const std::string &root, const Composition &s);
std::string verify_storage_identity_at(const std::string &root, const StorageIdentity &identity);

// --- role (usb-role.go) ---

std::string role_flags(const std::string &dir);
std::vector<std::pair<std::string, bool>> role_writes(const std::string &role);
struct RoleStore {
	std::string dir;
	std::function<std::string(const std::string &action)> run;
	std::function<std::string(const std::string &role)> verify;
	std::function<std::string()> exclude;  // may be empty
	std::string set(const std::string &role) const;
};
std::string verify_role_at(const std::string &role_glob, const std::string &gadget, const std::string &role,
                           const Composition &s);

struct HostDevice {
	std::string port, vendor_id, product_id, manufacturer, product, speed, device_class;
	std::vector<std::string> interface_classes;  // empty: null in the JSON
	std::string max_power;
};
std::vector<HostDevice> host_devices_at(const std::string &root);
std::string host_devices_json(const std::vector<HostDevice> &devices, const std::string &port_power);
// usbPortPower: none (PCIe), board (Cube) or shared (Lite).
std::string port_power();

// --- the serial function's legacy getty, flags of the USB network ---

// removeLegacyACMGettyLine.
std::string remove_legacy_acm_getty_line(const std::string &data, bool &changed);
// retireLegacyACMGetty without its SIGHUP to init: true when the file was rewritten.
std::string rewrite_inittab(const std::string &path, bool &changed);
// saveUSBFlag: writes or removes boot/name, applies; a failed apply restores the file and applies again.
std::string save_usb_flag(const std::string &boot, const std::string &name, bool enabled,
                          const std::function<std::string()> &apply);

} // namespace nkos::usb
