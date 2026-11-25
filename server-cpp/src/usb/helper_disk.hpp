#pragma once

// The helper disk, as the Go server's helperdisk package and service/vm/helper_disk.go: the small read-only FAT12
// disk the computer sees next to the virtual drive (a LUN of it, set up by S03usbdev), with the programs to run there.
// NanoKVM OS ships one with the serial console helper alone; while the computer sensors are on, the server puts
// together another with that helper and the sensors agents, and S03usbdev attaches it instead. Same layout as the
// build's: no partition table, files in the root directory with VFAT long names, fixed timestamps, so the same files
// give the same bytes.

#include <string>
#include <vector>

namespace nkos::usb::helperdisk {

inline constexpr std::size_t size = 24576 * 512;

struct File {
	std::string name;  // letters, digits, '.', '_' and '-', at most 100 characters
	std::string data;
};

// helperdisk.Build: the image, or Go's error text.
bool build(const std::vector<File> &files, std::string &image, std::string &error);
// helperdisk.Publish: writes the image unless the file holds it already; written says whether it did.
std::string publish(const std::string &path, const std::string &image, bool &written);

// reconcileHelperDisk with its files: built while the sensors are on (when its inputs changed since the last
// build), removed when they are off. Not thread-safe: the caller serialises (Go's hostSensorsApply).
struct Composer {
	std::string disk_file = "/var/lib/nanokvm/helper-disk.img";
	std::string agent = "/usr/share/nanokvm/host-sensors/nanokvm-sensors.exe";
	std::string linux_agent = "/usr/share/nanokvm/host-sensors/nanokvm-sensors.sh";
	std::string console_script = "/kvmapp/system/share/serial-console/nanokvm-serial-console.sh";
	std::string built_from;
	void reconcile(bool sensors_on);
};

} // namespace nkos::usb::helperdisk
