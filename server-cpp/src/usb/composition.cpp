#include "usb/composition.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"

#include <glaze/glaze.hpp>
#include <openssl/evp.h>

#include <cerrno>
#include <cstring>
#include <dirent.h>
#include <format>
#include <fstream>
#include <map>
#include <sstream>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::usb {

namespace {

constexpr int in_endpoint_limit = 6, out_endpoint_limit = 7;
constexpr std::size_t max_usb_string = 126;
constexpr const char *gadget = "/sys/kernel/config/usb_gadget/g0";
constexpr const char *user_disk_image = "/data/.nanokvm/user-disk.img";

struct Cost {
	int in = 0;
	int out = 0;
};
const std::map<std::string, Cost> costs = {
	{"absolute", {1, 1}}, {"audio", {0, 1}}, {"disk", {1, 1}}, {"hid", {3, 3}},
	{"keyboard", {1, 1}}, {"network", {2, 1}}, {"relative", {1, 1}}, {"serial", {2, 1}},
};

std::string flag(const char *name) { return std::string(flags_dir) + "/" + name; }

bool file_exists(const std::string &path)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) return true;
	if (errno != ENOENT) log::error("check file {} err: {}", path, std::strerror(errno));
	return false;
}

bool lexists(const std::string &path)
{
	struct stat st{};
	return ::lstat(path.c_str(), &st) == 0;
}

std::optional<std::string> read_file(const std::string &path, std::size_t limit = 1 << 16)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::string s(limit, '\0');
	in.read(s.data(), static_cast<std::streamsize>(limit));
	s.resize(static_cast<std::size_t>(in.gcount()));
	return s;
}

std::string trim(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

// --- identity ---------------------------------------------------------------------------------------------------

struct Identity {
	std::string vendor_id, product_id, manufacturer, product, serial_number;
	bool operator==(const Identity &) const = default;
};
const Identity default_identity{"0x3346", "0x1009", "sipeed", "NanoKVM", ""};

std::optional<std::string> normalize_usb_id(std::string value)
{
	value = trim(value);
	if (value.size() < 3 || value.size() > 6 || value[0] != '0' || (value[1] != 'x' && value[1] != 'X')) return std::nullopt;
	unsigned id = 0;
	for (std::size_t i = 2; i < value.size(); i++) {
		char c = value[i];
		int d = c >= '0' && c <= '9' ? c - '0' : c >= 'a' && c <= 'f' ? c - 'a' + 10 : c >= 'A' && c <= 'F' ? c - 'A' + 10 : -1;
		if (d < 0) return std::nullopt;
		id = id * 16 + static_cast<unsigned>(d);
	}
	if (id == 0) return std::nullopt;
	return std::format("0x{:04x}", id);
}

bool valid_usb_string(const std::string &v)
{
	if (v.empty() || v.size() > max_usb_string) return false;
	for (unsigned char c : v)
		if (c < 0x20 || c > 0x7e) return false;
	return trim(v) == v && v.front() != ' ' && v.back() != ' ';
}

bool valid_serial(const std::string &v)
{
	if (v.empty() || v.size() > 126) return false;
	for (unsigned char c : v)
		if (!std::isalnum(c) && c != '.' && c != '_' && c != '-') return false;
	return true;
}

Identity normalized(const Identity &i)
{
	Identity out = default_identity;
	if (auto v = normalize_usb_id(i.vendor_id)) out.vendor_id = *v;
	if (auto v = normalize_usb_id(i.product_id)) out.product_id = *v;
	if (!i.manufacturer.empty()) out.manufacturer = i.manufacturer;
	if (!i.product.empty()) out.product = i.product;
	out.serial_number = i.serial_number;
	return out;
}

// Each file stands on its own, as in the scripts: an unreadable or invalid one counts as the factory value.
Identity read_identity()
{
	auto read = [](const char *name) { return trim(read_file(flag(name)).value_or("")); };
	Identity i{read("usb.vid"), read("usb.pid"), read("usb.manufacturer"), read("usb.product"), read("usb.serialnumber")};
	if (!normalize_usb_id(i.vendor_id)) i.vendor_id.clear();
	if (!normalize_usb_id(i.product_id)) i.product_id.clear();
	if (!valid_usb_string(i.manufacturer)) i.manufacturer.clear();
	if (!valid_usb_string(i.product)) i.product.clear();
	if (!valid_serial(i.serial_number)) i.serial_number.clear();
	return normalized(i);
}

std::string chip_serial_number()
{
	std::istringstream in(read_file("/sys/class/cvi-base/base_uid").value_or(""));
	std::string a, uid;
	if (!(in >> a >> uid)) return "0123456789ABCDEF";
	std::erase(uid, '_');
	return uid.size() >= 12 ? uid : "0123456789ABCDEF";
}

// --- storage identity (SCSI INQUIRY names) ----------------------------------------------------------------------

struct StorageIdentity {
	std::string vendor = "NanoKVM", image, setup = "Setup Disk", user = "User Disk";
};

bool valid_storage(const std::string &s, std::size_t limit)
{
	if (s.size() > limit || trim(s) != s) return false;
	for (unsigned char c : s)
		if (c < 32 || c > 126) return false;
	return true;
}

StorageIdentity read_storage_identity()
{
	auto read = [](const char *name, std::size_t limit) -> std::string {
		auto data = read_file(flag(name), limit + 2);
		if (!data) return {};
		std::string s = *data;
		if (!s.empty() && s.back() == '\n') s.pop_back();
		return valid_storage(s, limit) ? s : std::string();
	};
	StorageIdentity i{read("usb.storage.vendor", 8), read("usb.storage.image", 16), read("usb.storage.setup", 16),
	                  read("usb.storage.user", 16)};
	if (i.vendor.empty()) i.vendor = "NanoKVM";
	if (i.setup.empty()) i.setup = "Setup Disk";
	if (i.user.empty()) i.user = "User Disk";
	return i;
}

// --- composition ------------------------------------------------------------------------------------------------

struct Composition {
	std::string mode;
	bool keyboard = false, relative = false, absolute = false, network = false, disk = false, serial = false,
	     audio = false, user_disk = false;
	Identity identity;
	StorageIdentity storage;
};

Composition read_composition()
{
	Composition s;
	s.mode = gadget_mode();
	bool hid = !file_exists(flag("disable_hid"));
	bool network = file_exists(flag("usb.rndis0")) || file_exists(flag("usb.ncm"));
	bool disk = file_exists(flag("usb.disk0"));
	if (s.mode == "hid-only") network = disk = false;
	s.keyboard = hid && !file_exists(flag("usb.disable_keyboard"));
	s.relative = hid && !file_exists(flag("usb.disable_relative"));
	s.absolute = hid && !file_exists(flag("usb.disable_absolute"));
	s.network = network;
	s.disk = disk;
	s.serial = file_exists(flag("usb.acm"));
	s.audio = s.mode != "hid-only" && file_exists(flag("usb.audio"));
	s.identity = read_identity();
	s.storage = read_storage_identity();
	s.user_disk = disk && trim(read_file(flag("usb.userdisk")).value_or("")) == user_disk_image;
	return s;
}

// Go's %q of a printable ASCII string.
std::string quote(const std::string &s)
{
	std::string out = "\"";
	for (char c : s) {
		if (c == '"' || c == '\\') out += '\\';
		out += c;
	}
	return out + "\"";
}

std::string revision(const Composition &s)
{
	Identity id = normalized(s.identity);
	auto t = [](bool b) { return b ? "true" : "false"; };
	std::string text = std::format("{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}", quote(s.mode), t(s.keyboard),
	                               t(s.relative), t(s.absolute), t(s.network), t(s.disk), t(s.serial), t(s.audio),
	                               quote(id.vendor_id), quote(id.product_id), quote(id.manufacturer),
	                               quote(id.product), t(s.user_disk), quote(id.serial_number));
	text += std::format(":{{{} {} {} {}}}", quote(s.storage.vendor), quote(s.storage.image), quote(s.storage.setup),
	                    quote(s.storage.user));
	unsigned char sum[32];
	EVP_Digest(text.data(), text.size(), sum, nullptr, EVP_sha256(), nullptr);
	std::string hex;
	for (unsigned char b : sum) hex += std::format("{:02x}", b);
	return hex;
}

std::string port_power()
{
	if (trim(read_file("/etc/kvm/hw").value_or("")) == "pcie") return "none";
	struct stat st{};
	if (::stat("/etc/kvm/oled_exist", &st) != 0) return "shared";
	return "board";
}

struct Budget {
	int inUsed = 0, inLimit = in_endpoint_limit, outUsed = 0, outLimit = out_endpoint_limit;
};
struct IdentityJson {
	std::string vendorId, productId, manufacturer, product, serialNumber, chipSerialNumber;
	bool default_ = false;
};
struct StorageJson {
	std::string vendor, image, setup, user;
};
struct StateJson {
	bool keyboard, relative, absolute;
	std::string revision;
	bool network, media, disk, serial, audio, hid;
	std::string mode;
	Budget budget;
	std::map<std::string, Cost> costs;
	StorageJson storageIdentity;
	IdentityJson identity;
	bool userDisk;
	std::string role;
	std::string portPower;
};

} // namespace

} // namespace nkos::usb

template <>
struct glz::meta<nkos::usb::IdentityJson> {
	using T = nkos::usb::IdentityJson;
	static constexpr auto value = object("vendorId", &T::vendorId, "productId", &T::productId, "manufacturer",
	                                     &T::manufacturer, "product", &T::product, "serialNumber", &T::serialNumber,
	                                     "chipSerialNumber", &T::chipSerialNumber, "default", &T::default_);
};

namespace nkos::usb {

std::string gadget_mode()
{
	auto data = read_file(std::string(gadget) + "/bcdDevice");
	if (!data) {
		log::error("failed to read {}/bcdDevice", gadget);
		return "normal";
	}
	std::string key = trim(*data);
	std::string mode;
	if (key == "0x0510" || key == "0x0511" || key == "0x0710" || key == "0x0711") mode = "normal";
	else if (key == "0x0623" || key == "0x0624" || key == "0x0720" || key == "0x0721") mode = "hid-only";
	else {
		log::error("invalid mode flag: {}", key);
		return "normal";
	}
	if (mode == "hid-only") {
		// bcdDevice can survive a partial profile change: configured network, storage or audio functions win.
		std::string configs = std::string(gadget) + "/configs/c.1";
		if (DIR *d = ::opendir(configs.c_str())) {
			while (auto *e = ::readdir(d)) {
				if (e->d_type != DT_LNK) continue;
				char target[512];
				ssize_t n = ::readlink((configs + "/" + e->d_name).c_str(), target, sizeof target - 1);
				if (n <= 0) continue;
				std::string t(target, static_cast<std::size_t>(n));
				std::string function = t.substr(t.rfind('/') + 1);
				if (function.starts_with("rndis.") || function.starts_with("ncm.") || function.starts_with("mass_storage.") ||
				    function.starts_with("uac1.")) {
					mode = "normal";
					break;
				}
			}
			::closedir(d);
		}
	}
	return mode;
}

std::string role()
{
	if (lexists(flag("usb.host"))) return "host";
	if (lexists(flag("usb.off"))) return "off";
	return "gadget";
}

std::string state_json()
{
	Composition s = read_composition();
	std::string r = role();
	// As a host, and switched off, the port presents no function.
	if (r != "gadget") s.keyboard = s.relative = s.absolute = s.network = s.disk = s.serial = s.audio = s.user_disk = false;
	Budget budget;
	auto add = [&](const char *name, bool on) {
		if (on) budget.inUsed += costs.at(name).in, budget.outUsed += costs.at(name).out;
	};
	add("keyboard", s.keyboard);
	add("relative", s.relative);
	add("absolute", s.absolute);
	add("network", s.network);
	add("disk", s.disk);
	add("serial", s.serial);
	add("audio", s.audio);
	Identity id = normalized(s.identity);
	StateJson j{s.keyboard, s.relative, s.absolute, revision(s), s.network, false, s.disk, s.serial, s.audio,
	            s.keyboard || s.relative || s.absolute, s.mode, budget, costs,
	            {s.storage.vendor, s.storage.image, s.storage.setup, s.storage.user},
	            {id.vendor_id, id.product_id, id.manufacturer, id.product, id.serial_number, chip_serial_number(),
	             normalized(s.identity) == default_identity},
	            s.user_disk, r, port_power()};
	std::string out;
	(void)glz::write_json(j, out);
	return out;
}

void add_routes(http::Router &router)
{
	router.add("GET", "/api/vm/device/virtual", auth::guarded([](http::Request &, http::Response &res) {
		api::send(res, 0, "success", state_json());
	}, {{"admin"}}));
}

} // namespace nkos::usb
