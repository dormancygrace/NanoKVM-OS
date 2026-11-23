#pragma once

// The storage family of the API (administrators), as the Go server's router/storage.go and router/download.go with
// service/storage, service/download and remotemedia: the images of /data and the virtual drive, network boot, the
// user disk and its files, remote media (a browser's file, an HTTP or SMB URL) served to the drive over NBD, image
// downloads from a URL with curl (resumed after a restart) and browser uploads of images.

#include "http/router.hpp"

#include <chrono>
#include <functional>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

namespace nkos::storage {

// The SMB reader: the server binary started with --nanokvm-smb-reader (descriptor 3: the control socket, 4: the
// shared memory). Returns -1 when argv asks for nothing of the kind; otherwise it runs the reader and exits.
int run_smb_helper_if_requested(int argc, char **argv);

// What the Go server's storageRouter does before its routes: undo network boot and remote media a previous server
// left on the drive. Blocking (it may restart USB networking); call before the listeners, where Go calls it.
void cleanup_at_start();
// download.NewService: the transfer sentinel of a previous process goes, interrupted transfers are cleaned up and a
// download with a resume checkpoint continues (in a thread of its own). Call where Go's downloadRouter runs.
void start_downloads();

void add_routes(http::Router &router);

// --- For the other families (MCP virtual media, Redfish), as service/storage's exported methods ---

// hid.GetHid()'s lock in the Go server: image mounts, medium changes and the USB gadget's composition changes take
// it, so that a composition apply never runs between the checks and the writes of a mount.
std::mutex &gadget_mutex();

struct Medium {
	bool inserted = false;
	std::string source;  // local, browser, http, smb, netboot
	std::string image;
	bool cdrom = false;
};
// Medium(); error text in error.
std::optional<Medium> medium(std::string &error);
// Images(): the images of /data a drive may take.
std::vector<std::string> images(std::string &error);
// MountedImage(): the image on the virtual disk and whether it is a CD-ROM; error set without USB storage or when the
// state cannot be read.
std::string mounted_image_file(bool &cdrom, std::string &error);
// MountImageFile(name, cdrom): "" ejects. Empty on success, else the message.
std::string mount_image_file(const std::string &name, bool cdrom);
// InsertImageFile: only into an empty drive. drive_in_use is set for ErrDriveInUse.
std::string insert_image_file(const std::string &name, bool cdrom, bool &drive_in_use);
// InsertURL: an http(s) or smb URL; waits until the computer has it or timeout passes. Blocking.
std::string insert_url(const std::string &url, const std::string &username, const std::string &password,
                       std::chrono::milliseconds timeout);
// EjectMedium: whatever put it in (network boot keeps its image: "Turn off network boot first").
std::string eject_medium();
// LockUserDiskForComposition: the composition change that attaches or detaches the user disk holds it; nullopt
// and the reason when the disk is busy.
std::optional<std::function<void()>> lock_user_disk_for_composition(std::string &error);
std::string user_disk_image_path();
bool user_disk_exists();

} // namespace nkos::storage
