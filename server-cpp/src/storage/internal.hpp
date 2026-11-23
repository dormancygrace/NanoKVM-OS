#pragma once

// The storage family's shared parts, between its files: the paths it works on (variables, as the Go package's, so
// that a check can point them at fixtures), the virtual drive's gadget state, the image files of /data, the
// cancellation tokens of the remote sources and the service state the routes share.

#include "storage/go.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <span>
#include <string>
#include <thread>
#include <vector>

namespace nkos::storage {

using go::Error;
using SteadyClock = std::chrono::steady_clock;

// --- Paths (service/storage, service/download, hid.USBFlagsDir) ---
inline std::string image_directory = "/data";
inline std::string gadget_dir = "/sys/kernel/config/usb_gadget/g0";
inline std::string usb_gadget_udc = "/sys/kernel/config/usb_gadget/g0/UDC";
inline std::string usb_storage_link = "/sys/kernel/config/usb_gadget/g0/configs/c.1/mass_storage.disk0";
inline std::string cdrom_flag = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/cdrom";
inline std::string mount_device = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/file";
inline std::string forced_eject_device = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/forced_eject";
inline std::string inquiry_string = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/inquiry_string";
inline std::string ro_flag = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/ro";
inline std::string usb_flags_dir = "/etc/kvm/usb";
inline std::string usb_network_link = "/sys/kernel/config/usb_gadget/g0/configs/c.1/ncm.usb0";
inline std::string nbd_device = "/dev/nbd0";
inline std::string nbd_pid = "/sys/block/nbd0/pid";
inline std::string nbd_max_sectors = "/sys/block/nbd0/queue/max_sectors_kb";

// The medium swap pause of mountLocalImage.
inline std::chrono::milliseconds medium_swap_pause{2500};

// --- Cancellation (context.Context, reduced) ---
class Context : public std::enable_shared_from_this<Context> {
public:
	static std::shared_ptr<Context> make(std::shared_ptr<Context> parent = nullptr,
	                                     SteadyClock::time_point deadline = SteadyClock::time_point::max());
	void cancel();
	bool canceled() const;
	bool expired() const { return SteadyClock::now() >= deadline_; }
	bool done() const { return canceled() || expired(); }
	SteadyClock::time_point deadline() const { return deadline_; }
	// context.AfterFunc for cancellation: f runs once on cancel (at once if canceled). stop returns whether f was
	// kept from running.
	std::uint64_t on_cancel(std::function<void()> f);
	bool stop(std::uint64_t id);

private:
	mutable std::mutex mu_;
	bool canceled_ = false;
	SteadyClock::time_point deadline_ = SteadyClock::time_point::max();
	std::shared_ptr<Context> parent_;
	std::uint64_t parent_hook_ = 0, next_ = 1;
	std::vector<std::pair<std::uint64_t, std::function<void()>>> hooks_;
	friend struct ContextAccess;
};

// A one-shot event with waiters (a closed Go channel).
class Event {
public:
	void fire();
	bool fired() const;
	void wait();
	// false on timeout.
	bool wait_until(SteadyClock::time_point t);
	std::uint64_t subscribe(std::function<void()> f);  // runs at once when fired already
	// Also waits for a subscriber the event is running now (never call it from a subscriber).
	void unsubscribe(std::uint64_t id);

private:
	mutable std::mutex mu_;
	std::condition_variable cv_;
	bool fired_ = false;
	std::uint64_t next_ = 1;
	std::vector<std::pair<std::uint64_t, std::function<void()>>> subs_;
	std::mutex fire_mu_;  // held while the subscribers run
};

// --- Gadget and images (service/storage/image.go, image_guard.go) ---

// hid.GetHid()'s lock: image and medium changes and gadget composition changes take it.
std::mutex &gadget_mutex();

// hid.GetMode: "normal" or "hid-only", or an error.
Error hid_mode(std::string &mode);
// storageidentity.Read(usb_flags_dir).Inquiry(kind).
std::string inquiry(const std::string &kind);
// Callers hold the gadget lock.
Error require_active_usb_storage();
Error eject_local_image(bool force);
// setDrive: the drive made a CD (read-only) or a disk with name; changed tells whether the computer must
// reconnect. restore puts the old values back.
struct DriveRestore {
	std::vector<std::string> paths, old;
	void operator()() const;
};
Error set_drive(bool cdrom, const std::string &name, bool &changed, DriveRestore &restore);
std::string drive_identity();
Error reconnect_usb();
bool dvd_supported();

struct MountError {
	int code = 0;
	std::string msg;
	bool conflict = false;
	explicit operator bool() const { return code != 0; }
};
// mountLocalImage: callers hold the service lock; it takes the gadget lock.
MountError mount_local_image(const std::string &file, bool cdrom, bool force);

Error image_file(const std::string &name, std::string &path);
Error list_images(std::vector<std::string> &images, std::map<std::string, std::int64_t> &sizes);
Error mounted_image(std::string &image);
Error image_unused(const std::string &path);
Error check_image_unused(const std::string &path);
// The rename of a finished transfer; canceled is asked under the gadget lock (the commit point).
Error install_image(const std::function<bool()> &canceled, const std::string &temp, const std::string &dest);
Error remove_image(const std::string &path);

void record_medium(const std::string &actor, const std::string &image);

// Gin binds a JSON body by the json tags and a form by the form tags, or the Go field names without one: whether
// the request is JSON.
bool json_content_type(const std::string &content_type);

// --- Remote media (remotemedia) ---
inline constexpr std::uint32_t max_read = 1024 * 1024;
inline constexpr std::uint64_t max_image_size = 512ull * 1024 * 1024 * 1024;
inline constexpr std::uint64_t max_optical_size = (0x1000000ull - 0x030000) * 2048;
inline constexpr std::uint64_t legacy_optical_size = (256ull * 60 * 75 - 1) * 2048;
Error validate_image(std::uint64_t size);
Error validate_optical(std::uint64_t size, bool dvd);

// A block read: the data (borrowed until the next call) or an error.
using ReadBlock = std::function<Error(std::uint64_t offset, std::uint32_t length, std::span<const char> &data)>;
// The NBD transmission phase over a socket descriptor (remotemedia.Serve); returns why it ended.
Error nbd_serve(int fd, std::uint64_t size, const ReadBlock &read);

// A read-only random-access image (remotemedia.Source).
struct Source {
	virtual ~Source() = default;
	virtual Error read_block(std::uint64_t offset, std::uint32_t length, std::span<const char> &data) = 0;
	virtual void close() = 0;
	std::string name;
	std::uint64_t size = 0;
};

// The read cache of a network source (remotemedia/window.go).
using FetchBlock = std::function<Error(const std::shared_ptr<Context> &ctx, SteadyClock::time_point started,
                                       std::uint64_t offset, std::span<char> target)>;
class Window {
public:
	Window(std::shared_ptr<Context> ctx, FetchBlock fetch, char *buffer0, char *buffer1);
	~Window();
	Error read(std::uint64_t offset, std::uint32_t length, std::span<const char> &data);
	void close();
	std::uint64_t size = 0;
	char *buffers[2];
	std::uint64_t start = 0;
	std::size_t valid = 0;

private:
	struct Prefetch;
	void prefetch_next();
	void discard();
	std::shared_ptr<Context> ctx_;
	FetchBlock fetch_;
	std::mutex mu_;
	int current_ = 0;
	std::shared_ptr<Prefetch> ahead_;
	std::uint64_t next_ = ~0ull;
	std::uint8_t streak_ = 0;
	std::unique_ptr<char[]> own_[2];
};

struct HttpOptions {
	std::string url, username, password;
};
struct SmbOptions {
	std::string url, username, password, domain;
};
bool valid_http_url(const go::Url &u);
// ^bytes ([0-9]+)-([0-9]+)/([0-9]+)$ with start <= end < total.
bool parse_content_range(std::string_view value, std::uint64_t &start, std::uint64_t &end, std::uint64_t &total);
bool strong_http_etag(const std::string &s);
Error validate_http_options(const HttpOptions &o);
Error validate_smb_options(const SmbOptions &o);
Error parse_smb_options(const SmbOptions &o, std::string &address, std::string &share, std::string &name);
std::unique_ptr<Source> open_http(const std::shared_ptr<Context> &ctx, const HttpOptions &o, Error &error);
std::unique_ptr<Source> open_smb(const std::shared_ptr<Context> &ctx, const SmbOptions &o, Error &error);

// The kernel attachment of /dev/nbd0 (remotemedia.Device).
class NbdDevice {
public:
	static std::unique_ptr<NbdDevice> open(std::uint64_t size, ReadBlock read, Error &error);
	~NbdDevice();
	void close();
	Event stopped;

private:
	NbdDevice() = default;
	int file_ = -1, kernel_ = -1, user_ = -1;
	std::thread serve_, doit_;
	std::once_flag once_;
	std::atomic<int> doit_result_{0};
};

// SHA-256 (OpenSSL EVP).
class Sha256 {
public:
	Sha256();
	~Sha256();
	Sha256(const Sha256 &) = delete;
	Sha256 &operator=(const Sha256 &) = delete;
	void update(std::string_view data);
	std::string digest();  // 32 bytes
	static std::string hex(std::string_view bytes);

private:
	void *ctx_;
};

// strconv.Quote.
std::string go_quote(std::string_view s);

// --- Download (service/download) ---
inline std::string transfer_sentinel_path = "/run/nanokvm-transfer-in-progress";
inline std::string image_resume_path = "/run/nanokvm-download-resume.json";
inline std::string image_curl_path = "/usr/bin/curl";
inline std::chrono::milliseconds image_retry_delay{1000};
inline constexpr int image_retry_limit = 6;
inline constexpr std::uint64_t image_free_reserve = 128ull << 20;
inline const char *err_image_space = "not enough free space for the image (128 MiB reserved)";
// imageWriteBudget, replaceable by a check.
inline std::function<Error(int fd, std::int64_t &budget)> image_write_budget;
Error file_image_budget(int fd, std::int64_t &budget);

Error validate_image_filename(const std::string &name);  // validateRemoteImageFilename
Error validate_raw_image_size(std::int64_t size);
Error validate_uploaded_image(const std::string &path, const std::string &name);
bool image_transfer_temp(const std::string &name);
Error parse_sha256(const std::string &value, std::optional<std::string> &sum);

struct ImageResume {
	std::string url, path, filename, sha256, etag;
	std::int64_t total = 0;
};
std::string image_resume_json(const ImageResume &job);
Error save_image_resume(const std::string &path, const ImageResume &job);
Error load_image_resume(const std::string &path, ImageResume &job);
bool strong_etag(const std::string &s);

struct ImageHeaders {
	int status = 0;
	std::int64_t length = -1, start = -1, total = -1;
	std::string etag;
};
ImageHeaders parse_image_headers(std::string_view data);
ImageHeaders read_image_headers(const std::string &path);
std::vector<std::string> curl_image_args(const std::string &url, const std::string &headers, bool prefer_chacha);
bool curl_lost_connection(int code);

struct TransferProgress {
	std::int64_t bytes = 0, total = 0;
	double bytes_per_second = 0;
	std::string percentage() const;
};
class TransferMeter {
public:
	explicit TransferMeter(std::int64_t offset, SteadyClock::time_point now = SteadyClock::now());
	TransferProgress sample(std::int64_t bytes, std::int64_t total, SteadyClock::time_point now);

private:
	std::mutex mu_;
	std::int64_t previous_bytes_;
	SteadyClock::time_point previous_time_;
};

// --- User disk ---
inline std::string user_disk_dir = "/data/.nanokvm";
inline std::string user_disk_image = "/data/.nanokvm/user-disk.img";
inline std::string user_disk_lun = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.2/file";
inline std::string user_disk_mountpoint = "/run/nanokvm/user-disk";
inline std::string mountinfo_path = "/proc/self/mountinfo";
// The private mount of the image (mountUserDisk): the root directory's descriptor. Replaceable by a check, which
// cannot mount.
inline std::function<Error(bool writable, int &root)> user_disk_mount_hook;
inline std::function<Error(int root)> user_disk_unmount_hook;
std::string user_disk_flag();
Error user_disk_path(const std::string &value, std::string &name);
Error user_disk_available();
std::string user_disk_state_json();
// The JSON of a listing page of an open directory (listUserDisk), for a root descriptor.
Error list_user_disk(int root, const std::string &name, int offset, std::string &json);

// --- Network boot ---
inline std::string netboot_dir = "/data/.nanokvm/netboot";
inline std::string usbnet_run = "/run/nanokvm-usbnet";
inline std::string usbnet_net = "/sys/class/net";
inline std::string usb_internet_run = "/etc/init.d/S30usbnet";
inline std::string usb_internet_tool = "/usr/sbin/nkos-usb-internet";
// usbinternet's flags directory: the new layout once both markers exist, /boot before.
inline std::string usb_flags_layout_app = "/usr/share/nanokvm/usb-flags-layout";
inline std::string usb_flags_layout_active = "/etc/kvm/usb/.layout-v1";
inline std::string usb_flags_layout_dir = "/etc/kvm/usb";
inline std::string usb_flags_legacy_dir = "/boot";
struct NetbootState {
	std::string ipxe;
	std::optional<std::map<std::string, std::string>> ipxe_files;
	std::string netbootxyz, checked_at;
	bool secure_boot = false, sharing_by_netboot = false;
};
Error load_netboot_state(NetbootState &st);
std::string netboot_state_json(const NetbootState &st);
std::string netboot_script(const std::string &tag);
struct GithubAsset {
	std::string name, digest;
	std::int64_t size = 0;
};
struct GithubRelease {
	std::string tag;
	std::vector<GithubAsset> assets;
};
Error parse_github_release(const std::string &repo, const std::string &body, GithubRelease &release);
Error ipxe_digests(const GithubRelease &rel, std::map<std::string, std::string> &digests);
bool ipxe_tag(const std::string &s);
bool netbootxyz_tag(const std::string &s);
// usbinternet.Runtime.ReadStatus as JSON, and the netboot port.
std::string usb_flags_boot();
std::string usb_internet_status_json();
std::string usb_internet_address();
// netip.ParsePrefix(address).Addr().String(), or "".
std::string prefix_address(const std::string &prefix);
int netboot_port_file();

// --- Service state (service/storage/service.go) ---
struct RemoteSession;
struct NetbootSession;
struct Service {
	std::mutex mu;
	std::shared_ptr<RemoteSession> remote;
	std::string remote_kind, remote_error;
	std::shared_ptr<NetbootSession> netboot;
	std::mutex netboot_op;
};
Service &service();

} // namespace nkos::storage
