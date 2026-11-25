#include "usb/gadget.hpp"

#include "common/log.hpp"
#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"

#include <openssl/evp.h>

#include <algorithm>
#include <cerrno>
#include <climits>
#include <cstdlib>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <format>
#include <glob.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::usb {

namespace {

constexpr int in_endpoint_limit = 6, out_endpoint_limit = 7;
constexpr std::size_t max_usb_string = 126;
constexpr const char *fallback_serial_number = "0123456789ABCDEF";
constexpr const char *remote_media_device = "/dev/nbd0";

struct Cost {
	int in, out;
};
// Sorted by name, as Go writes the map.
const std::vector<std::pair<std::string, Cost>> costs = {
	{"absolute", {1, 1}}, {"audio", {0, 1}}, {"disk", {1, 1}}, {"hid", {3, 3}},
	{"keyboard", {1, 1}}, {"network", {2, 1}}, {"relative", {1, 1}}, {"serial", {2, 1}},
};
Cost cost(const std::string &name)
{
	for (auto &[n, c] : costs)
		if (n == name) return c;
	return {0, 0};
}

std::string join(const std::string &dir, const std::string &name) { return dir + "/" + name; }

// os.PathError's text.
std::string path_error(const char *op, const std::string &path, int err)
{
	return std::string(op) + " " + path + ": " + gotext::errno_text(err);
}

// os.ReadFile: "" or the error.
std::string read_file(const std::string &path, std::string &out, std::size_t limit = SIZE_MAX)
{
	out.clear();
	int fd = ::open(path.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return path_error("open", path, errno);
	char buf[4096];
	for (;;) {
		std::size_t want = std::min(sizeof buf, limit - out.size());
		if (want == 0) break;
		ssize_t n = ::read(fd, buf, want);
		if (n < 0 && errno == EINTR) continue;
		if (n < 0) {
			int e = errno;
			::close(fd);
			return path_error("read", path, e);
		}
		if (n == 0) break;
		out.append(buf, static_cast<std::size_t>(n));
	}
	::close(fd);
	return {};
}

std::optional<std::string> read(const std::string &path)
{
	std::string data;
	if (!read_file(path, data).empty()) return std::nullopt;
	return data;
}

// deviceExists: os.Stat, logging what is not "does not exist".
bool device_exists(const std::string &path)
{
	struct stat st{};
	if (::stat(path.c_str(), &st) == 0) return true;
	if (errno != ENOENT) log::error("check file {} err: {}", path, path_error("stat", path, errno));
	return false;
}

bool lexists(const std::string &path)
{
	struct stat st{};
	return ::lstat(path.c_str(), &st) == 0;
}

bool is_symlink(const std::string &path)
{
	struct stat st{};
	return ::lstat(path.c_str(), &st) == 0 && S_ISLNK(st.st_mode);
}

std::string dir_of(const std::string &path)
{
	auto slash = path.rfind('/');
	if (slash == std::string::npos) return ".";
	return slash == 0 ? "/" : path.substr(0, slash);
}

std::string sync_dir(const std::string &dir)
{
	int fd = ::open(dir.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
	if (fd < 0) return path_error("open", dir, errno);
	std::string error;
	if (::fsync(fd) != 0) error = path_error("sync", dir, errno);
	::close(fd);
	return error;
}

// os.MkdirAll(dir, 0755).
std::string mkdir_all(const std::string &dir)
{
	struct stat st{};
	if (::stat(dir.c_str(), &st) == 0) {
		if (S_ISDIR(st.st_mode)) return {};
		return "mkdir " + dir + ": not a directory";
	}
	std::string parent = dir_of(dir);
	if (parent != dir && parent != "/" && parent != ".")
		if (auto e = mkdir_all(parent); !e.empty()) return e;
	if (::mkdir(dir.c_str(), 0755) != 0 && errno != EEXIST) return path_error("mkdir", dir, errno);
	return {};
}

// Joins errors as errors.Join does: one per line.
std::string join_errors(const std::vector<std::string> &errors)
{
	std::string out;
	for (auto &e : errors)
		if (!e.empty()) out += (out.empty() ? "" : "\n") + e;
	return out;
}

bool hex_digit(char c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'); }

std::string valid_usb_string(const std::string &value)
{
	if (value.empty() || value.size() > max_usb_string)
		return std::format("USB strings are 1 to {} characters", max_usb_string);
	for (unsigned char c : value)
		if (c < 0x20 || c > 0x7e) return "USB strings are printable ASCII without line breaks";
	if (gotext::trim_space(value) != value) return "USB strings do not start or end with spaces";
	return {};
}

bool valid_serial(const std::string &v)
{
	if (v.empty() || v.size() > 126) return false;
	for (unsigned char c : v)
		if (!((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '.' || c == '_' || c == '-'))
			return false;
	return true;
}

bool valid_storage(const std::string &s, std::size_t limit)
{
	if (s.size() > limit || gotext::trim_space(s) != s) return false;
	for (unsigned char c : s)
		if (c < 32 || c > 126) return false;
	return true;
}


const char *b(bool v) { return v ? "true" : "false"; }

} // namespace

// --- identity ---

const Identity default_identity{"0x3346", "0x1009", "sipeed", "NanoKVM", ""};

std::optional<std::string> normalize_usb_id(const std::string &raw, std::string &error)
{
	std::string value = gotext::trim_space(raw);
	bool pattern = value.size() >= 3 && value.size() <= 6 && value[0] == '0' && (value[1] == 'x' || value[1] == 'X');
	for (std::size_t i = 2; pattern && i < value.size(); i++) pattern = hex_digit(value[i]);
	if (!pattern) {
		error = "USB ID " + gotext::quote(value) + " is not a hexadecimal number like 0x1234";
		return std::nullopt;
	}
	unsigned id = static_cast<unsigned>(std::strtoul(value.c_str() + 2, nullptr, 16));
	if (id == 0) {
		error = "USB ID 0x0000 is not a valid vendor or product ID";
		return std::nullopt;
	}
	return std::format("0x{:04x}", id);
}

Identity normalized(const Identity &i)
{
	Identity out = default_identity;
	std::string ignored;
	if (auto v = normalize_usb_id(i.vendor_id, ignored)) out.vendor_id = *v;
	if (auto v = normalize_usb_id(i.product_id, ignored)) out.product_id = *v;
	if (!i.manufacturer.empty()) out.manufacturer = i.manufacturer;
	if (!i.product.empty()) out.product = i.product;
	out.serial_number = i.serial_number;
	return out;
}

std::string validate(const Identity &i)
{
	std::string error;
	if (!i.vendor_id.empty() && !normalize_usb_id(i.vendor_id, error)) return error;
	if (!i.product_id.empty() && !normalize_usb_id(i.product_id, error)) return error;
	for (auto *s : {&i.manufacturer, &i.product})
		if (!s->empty())
			if (auto e = valid_usb_string(*s); !e.empty()) return e;
	if (!i.serial_number.empty() && !valid_serial(i.serial_number))
		return "a USB serial number is 1 to 126 letters, digits, dots, dashes or underscores";
	return {};
}

bool is_default(const Identity &i) { return normalized(i) == default_identity; }

bool identity_from_request(const std::optional<Identity> &request, Identity &out, std::string &error)
{
	out = {};
	if (!request) return true;
	error = validate(*request);
	if (!error.empty()) return false;
	out = normalized(*request);
	return true;
}

std::map<std::string, std::optional<std::string>> identity_files(const Identity &i)
{
	Identity n = normalized(i);
	std::map<std::string, std::optional<std::string>> out;
	auto set = [&](const char *name, const std::string &value, const std::string &fallback) {
		out[name] = value == fallback ? std::nullopt : std::optional<std::string>(value);
	};
	set("usb.vid", n.vendor_id, default_identity.vendor_id);
	set("usb.pid", n.product_id, default_identity.product_id);
	set("usb.manufacturer", n.manufacturer, default_identity.manufacturer);
	set("usb.product", n.product, default_identity.product);
	set("usb.serialnumber", n.serial_number, "");
	return out;
}

Identity read_identity(const std::string &dir)
{
	auto value = [&](const char *name) { return gotext::trim_space(read(join(dir, name)).value_or("")); };
	Identity i{value("usb.vid"), value("usb.pid"), value("usb.manufacturer"), value("usb.product"), value("usb.serialnumber")};
	std::string ignored;
	if (!normalize_usb_id(i.vendor_id, ignored)) i.vendor_id.clear();
	if (!normalize_usb_id(i.product_id, ignored)) i.product_id.clear();
	if (!valid_usb_string(i.manufacturer).empty()) i.manufacturer.clear();
	if (!valid_usb_string(i.product).empty()) i.product.clear();
	if (!valid_serial(i.serial_number)) i.serial_number.clear();
	return normalized(i);
}

std::string gadget_identity_at(const std::string &root, Identity &out)
{
	out = {};
	std::string data;
	auto value = [&](const char *rel, std::string &field) {
		std::string e = read_file(join(root, rel), data);
		field = gotext::trim_space(data);
		return e;
	};
	if (auto e = value("idVendor", out.vendor_id); !e.empty()) return e;
	if (auto e = value("idProduct", out.product_id); !e.empty()) return e;
	if (auto e = value("strings/0x409/manufacturer", out.manufacturer); !e.empty()) return e;
	if (auto e = value("strings/0x409/product", out.product); !e.empty()) return e;
	out = normalized(out);
	return {};
}

std::string gadget_serial_number_at(const std::string &root)
{
	return gotext::trim_space(read(join(root, "strings/0x409/serialnumber")).value_or(""));
}

std::string chip_serial_number(const std::string &uid_path)
{
	auto data = read(uid_path);
	if (!data) return fallback_serial_number;
	// strings.Fields: the second field.
	std::vector<std::string> fields;
	std::string current;
	for (std::size_t i = 0; i < data->size();) {
		std::size_t n;
		char32_t r = gotext::decode_rune(std::string_view(*data).substr(i), n);
		if (gotext::is_space(r)) {
			if (!current.empty()) fields.push_back(std::move(current));
			current.clear();
		} else {
			current.append(*data, i, n);
		}
		i += n;
	}
	if (!current.empty()) fields.push_back(current);
	if (fields.size() < 2) return fallback_serial_number;
	std::string uid = fields[1];
	std::erase(uid, '_');
	return uid.size() >= 12 ? uid : fallback_serial_number;
}

// --- storage identity ---

const std::vector<std::string> storage_identity_files = {"usb.storage.vendor", "usb.storage.image", "usb.storage.setup",
                                                         "usb.storage.user"};

StorageIdentity normalized(const StorageIdentity &i)
{
	StorageIdentity out = i;
	if (out.vendor.empty()) out.vendor = "NanoKVM";
	if (out.setup.empty()) out.setup = "Setup Disk";
	if (out.user.empty()) out.user = "User Disk";
	return out;
}

std::string validate(const StorageIdentity &i)
{
	const std::string *values[] = {&i.vendor, &i.image, &i.setup, &i.user};
	for (int n = 0; n < 4; n++) {
		std::size_t limit = n == 0 ? 8 : 16;
		if (!valid_storage(*values[n], limit))
			return std::format("storage identity fields must be printable ASCII, at most {} characters, without leading or "
			                   "trailing spaces", limit);
	}
	return {};
}

std::map<std::string, std::optional<std::string>> file_values(const StorageIdentity &raw)
{
	StorageIdentity i = normalized(raw);
	const std::string values[] = {i.vendor, i.image, i.setup, i.user};
	const std::string defaults[] = {"NanoKVM", "", "Setup Disk", "User Disk"};
	std::map<std::string, std::optional<std::string>> out;
	for (std::size_t n = 0; n < storage_identity_files.size(); n++)
		out[storage_identity_files[n]] = values[n] != defaults[n] ? std::optional<std::string>(values[n]) : std::nullopt;
	return out;
}

StorageIdentity read_storage_identity(const std::string &dir)
{
	auto value = [&](const std::string &name, std::size_t limit) -> std::string {
		std::string data;
		if (!read_file(join(dir, name), data, limit + 2).empty()) return {};
		if (!data.empty() && data.back() == '\n') data.pop_back();
		return valid_storage(data, limit) ? data : std::string();
	};
	return normalized(StorageIdentity{value(storage_identity_files[0], 8), value(storage_identity_files[1], 16),
	                                  value(storage_identity_files[2], 16), value(storage_identity_files[3], 16)});
}

std::string inquiry(const StorageIdentity &raw, const std::string &kind)
{
	StorageIdentity i = normalized(raw);
	std::string product = i.image;
	if (kind == "setup") product = i.setup;
	else if (kind == "user") product = i.user;
	else if (kind == "cdrom") {
		if (product.empty()) product = "USB CD/DVD-ROM";
	} else if (kind == "remote") {
		if (product.empty()) product = "Remote ISO";
	} else if (product.empty()) {
		product = "USB Mass Storage";
	}
	return gotext::pad_right(i.vendor, 8) + gotext::pad_right(product, 16) + "0520";
}

// --- composition ---

std::string gadget_mode_at(const std::string &gadget, std::string &error)
{
	std::string flag = join(gadget, "bcdDevice"), data;
	if (auto e = read_file(flag, data); !e.empty()) {
		log::error("failed to read {}: {}", flag, e);
		error = e;
		return {};
	}
	std::string key = gotext::trim_space(data), mode;
	if (key == "0x0510" || key == "0x0511" || key == "0x0710" || key == "0x0711") mode = "normal";
	else if (key == "0x0623" || key == "0x0624" || key == "0x0720" || key == "0x0721") mode = "hid-only";
	else {
		log::error("invalid mode flag: {}", key);
		error = "invalid mode flag";
		return {};
	}
	if (mode == "hid-only") {
		// bcdDevice can survive a partial profile change: configured network, storage or audio functions win.
		std::string configs = join(gadget, "configs/c.1");
		DIR *d = ::opendir(configs.c_str());
		if (!d) {
			error = "read USB configuration: " + path_error("open", configs, errno);
			return {};
		}
		std::vector<std::string> names;
		while (auto *e = ::readdir(d))
			if (std::strcmp(e->d_name, ".") && std::strcmp(e->d_name, "..")) names.emplace_back(e->d_name);
		::closedir(d);
		std::sort(names.begin(), names.end());
		for (auto &name : names) {
			std::string path = join(configs, name);
			if (!is_symlink(path)) continue;
			char target[PATH_MAX];
			ssize_t n = ::readlink(path.c_str(), target, sizeof target - 1);
			if (n < 0) {
				error = "read USB function: " + path_error("readlink", path, errno);
				return {};
			}
			std::string t(target, static_cast<std::size_t>(n));
			while (t.size() > 1 && t.back() == '/') t.pop_back();
			std::string function = t.substr(t.rfind('/') == std::string::npos ? 0 : t.rfind('/') + 1);
			if (function.starts_with("rndis.") || function.starts_with("ncm.") || function.starts_with("mass_storage.") ||
			    function.starts_with("uac1."))
				return "normal";
		}
	}
	return mode;
}

Composition read_composition(const std::string &dir, const std::string &gadget)
{
	Composition s;
	std::string error;
	s.mode = gadget_mode_at(gadget, error);
	// The gadget may not be bound while booting or without a cable: the installed default profile is normal.
	if (!error.empty()) s.mode = "normal";
	bool hid = !device_exists(join(dir, "disable_hid"));
	bool network = device_exists(join(dir, "usb.rndis0")) || device_exists(join(dir, "usb.ncm"));
	bool disk = device_exists(join(dir, "usb.disk0"));
	if (s.mode == "hid-only") network = disk = false;
	s.keyboard = hid && !device_exists(join(dir, "usb.disable_keyboard"));
	s.relative = hid && !device_exists(join(dir, "usb.disable_relative"));
	s.absolute = hid && !device_exists(join(dir, "usb.disable_absolute"));
	s.network = network;
	s.disk = disk;
	s.serial = device_exists(join(dir, "usb.acm"));
	s.audio = s.mode != "hid-only" && device_exists(join(dir, "usb.audio"));
	s.identity = read_identity(dir);
	s.storage_identity = read_storage_identity(dir);
	s.user_disk = disk && gotext::trim_space(read(join(dir, "usb.userdisk")).value_or("")) == user_disk_image;
	return s;
}

std::pair<int, int> endpoint_usage(const Composition &s)
{
	int in = 0, out = 0;
	auto add = [&](const char *name, bool on) {
		if (!on) return;
		in += cost(name).in;
		out += cost(name).out;
	};
	add("keyboard", s.keyboard);
	add("relative", s.relative);
	add("absolute", s.absolute);
	add("network", s.network);
	add("disk", s.disk);
	add("serial", s.serial);
	add("audio", s.audio);
	return {in, out};
}

std::string validate(const Composition &s)
{
	if (s.mode != "normal" && s.mode != "hid-only") return "invalid USB mode";
	if (s.mode == "hid-only" && (s.network || s.disk || s.audio))
		return "USB network, disk and audio are unavailable in HID-only mode";
	auto [in, out] = endpoint_usage(s);
	if (in > in_endpoint_limit || out > out_endpoint_limit) return "USB endpoint budget exceeded";
	if (s.user_disk && !s.disk) return "the user disk needs the virtual disk";
	if (auto e = validate(s.storage_identity); !e.empty()) return e;
	return validate(s.identity);
}

bool empty(const Composition &s)
{
	return !s.keyboard && !s.relative && !s.absolute && !s.network && !s.disk && !s.serial && !s.audio;
}

bool enabled(const Composition &s, const std::string &device)
{
	if (device == "network") return s.network;
	if (device == "disk") return s.disk;
	if (device == "audio") return s.audio;
	if (device == "serial") return s.serial;
	return false;
}

Composition with(Composition s, const std::string &device, bool on)
{
	if (device == "network") s.network = on;
	else if (device == "disk") s.disk = on;
	else if (device == "audio") {
		s.audio = on;
		if (on) s.mode = "normal";
	} else if (device == "serial") s.serial = on;
	return s;
}

std::string revision(const Composition &s)
{
	Identity id = normalized(s.identity);
	StorageIdentity st = normalized(s.storage_identity);
	using gotext::quote;
	std::string text = std::format("{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}:{}", quote(s.mode), b(s.keyboard), b(s.relative),
	                               b(s.absolute), b(s.network), b(s.disk), b(s.serial), b(s.audio), quote(id.vendor_id),
	                               quote(id.product_id), quote(id.manufacturer), quote(id.product), b(s.user_disk),
	                               quote(id.serial_number));
	text += std::format(":{{{} {} {} {}}}", quote(st.vendor), quote(st.image), quote(st.setup), quote(st.user));
	unsigned char sum[32];
	EVP_Digest(text.data(), text.size(), sum, nullptr, EVP_sha256(), nullptr);
	std::string hex;
	for (unsigned char c : sum) hex += std::format("{:02x}", c);
	return hex;
}

std::string response_json(Composition s, const std::string &role, const std::string &port_power,
                          const std::string &chip_serial)
{
	// As a host, and switched off, the port presents no function; the composition stays in its flags.
	if (role != "gadget")
		s.keyboard = s.relative = s.absolute = s.network = s.disk = s.serial = s.audio = s.user_disk = false;
	auto [in, out] = endpoint_usage(s);
	Identity id = normalized(s.identity);
	StorageIdentity st = normalized(s.storage_identity);
	using gotext::json_string;
	std::string j = std::format(R"({{"keyboard":{},"relative":{},"absolute":{},"revision":{},"network":{},"media":false,)"
	                            R"("disk":{},"serial":{},"audio":{},"hid":{},"mode":{},)",
	                            b(s.keyboard), b(s.relative), b(s.absolute), json_string(revision(s)), b(s.network),
	                            b(s.disk), b(s.serial), b(s.audio), b(s.keyboard || s.relative || s.absolute),
	                            json_string(s.mode));
	j += std::format(R"("budget":{{"inUsed":{},"inLimit":{},"outUsed":{},"outLimit":{}}},"costs":{{)", in,
	                 in_endpoint_limit, out, out_endpoint_limit);
	for (std::size_t i = 0; i < costs.size(); i++)
		j += std::format(R"({}"{}":{{"in":{},"out":{}}})", i ? "," : "", costs[i].first, costs[i].second.in,
		                 costs[i].second.out);
	j += std::format(R"(}},"storageIdentity":{{"vendor":{},"image":{},"setup":{},"user":{}}},)", json_string(st.vendor),
	                 json_string(st.image), json_string(st.setup), json_string(st.user));
	j += std::format(R"("identity":{{"vendorId":{},"productId":{},"manufacturer":{},"product":{},"serialNumber":{},)"
	                 R"("chipSerialNumber":{},"default":{}}},)",
	                 json_string(id.vendor_id), json_string(id.product_id), json_string(id.manufacturer),
	                 json_string(id.product), json_string(id.serial_number), json_string(chip_serial),
	                 b(is_default(s.identity)));
	j += std::format(R"("userDisk":{},"role":{},"portPower":{}}})", b(s.user_disk), json_string(role),
	                 json_string(port_power));
	return j;
}

// --- boot files ---

const std::vector<std::string> composition_files = [] {
	std::vector<std::string> v = {"usb.rndis0", "usb.ncm", "usb.disk0", "usb.acm", "usb.audio", "disable_hid",
	                              "usb.disable_keyboard", "usb.disable_relative", "usb.disable_absolute",
	                              "usb.vid", "usb.pid", "usb.manufacturer", "usb.product", "usb.serialnumber",
	                              "usb.userdisk"};
	v.insert(v.end(), storage_identity_files.begin(), storage_identity_files.end());
	return v;
}();

std::string snapshot_file(const std::string &path, FileSnapshot &out)
{
	out = {};
	struct stat st{};
	if (::lstat(path.c_str(), &st) != 0) {
		if (errno == ENOENT) return {};
		return path_error("lstat", path, errno);
	}
	if (!S_ISREG(st.st_mode)) return "USB configuration is not a regular file: " + path;
	std::string data;
	auto e = read_file(path, data);
	out = {true, static_cast<mode_t>(st.st_mode & 0777), std::move(data)};
	return e;
}

std::string write_usb_file(const std::string &path, const FileSnapshot &snapshot)
{
	std::string dir = dir_of(path);
	if (!snapshot.exists) {
		if (::unlink(path.c_str()) != 0) {
			if (errno == ENOENT) return {};
			return path_error("remove", path, errno);
		}
		return sync_dir(dir);
	}
	std::string tmp = dir + "/.usb-composition-XXXXXX";
	int fd = ::mkstemp(tmp.data());
	if (fd < 0) return path_error("open", tmp, errno);
	auto fail = [&](const char *op) {
		std::string e = path_error(op, tmp, errno);
		::close(fd);
		::unlink(tmp.c_str());
		return e;
	};
	if (::fchmod(fd, snapshot.mode) != 0) return fail("chmod");
	for (std::size_t done = 0; done < snapshot.data.size();) {
		ssize_t n = ::write(fd, snapshot.data.data() + done, snapshot.data.size() - done);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) return fail("write");
		done += static_cast<std::size_t>(n);
	}
	if (::fsync(fd) != 0) return fail("sync");
	if (::close(fd) != 0) {
		std::string e = path_error("close", tmp, errno);
		::unlink(tmp.c_str());
		return e;
	}
	if (::rename(tmp.c_str(), path.c_str()) != 0) {
		std::string e = "rename " + tmp + " " + path + ": " + gotext::errno_text(errno);
		::unlink(tmp.c_str());
		return e;
	}
	return sync_dir(dir);
}

namespace {

std::map<std::string, std::optional<std::string>> content_files(const Composition &s)
{
	auto files = identity_files(s.identity);
	for (auto &[k, v] : file_values(s.storage_identity)) files[k] = v;
	files["usb.userdisk"] = std::nullopt;
	if (s.user_disk) files["usb.userdisk"] = std::string(user_disk_image);
	return files;
}

} // namespace

std::string CompositionStore::apply(const Composition &current, const Composition &candidate) const
{
	if (auto e = validate(candidate); !e.empty()) return e;
	if (current == candidate) return {};
	// The flags directory comes with the root file system; a root without it gets one here.
	if (auto e = mkdir_all(boot_dir); !e.empty()) return e;
	// The legacy script may be a compatibility symlink: snapshot and restore the actual file.
	std::string script = script_path;
	struct stat st{};
	if (::lstat(script.c_str(), &st) == 0) {
		if (S_ISLNK(st.st_mode)) {
			char resolved[PATH_MAX];
			if (!::realpath(script.c_str(), resolved))
				return "resolve USB init script: " + path_error("lstat", script, errno);
			script = resolved;
		}
	} else if (errno != ENOENT) {
		return path_error("lstat", script, errno);
	}
	std::vector<std::string> paths;
	for (auto &name : composition_files) paths.push_back(join(boot_dir, name));
	paths.push_back(script);
	std::map<std::string, FileSnapshot> before;
	for (auto &path : paths) {
		FileSnapshot snapshot;
		if (auto e = snapshot_file(path, snapshot); !e.empty()) return e;
		before[path] = std::move(snapshot);
	}
	bool stopped = false;
	auto rollback = [&](const std::string &cause) {
		std::vector<std::string> errors;
		if (stopped) errors.push_back(run("stop"));
		for (auto &path : paths) errors.push_back(write_usb_file(path, before[path]));
		if (stopped) {
			if (auto e = run("start"); !e.empty()) errors.push_back(e);
			else errors.push_back(verify(current));
		}
		if (auto e = join_errors(errors); !e.empty()) return cause + "; restoring USB configuration also failed: " + e;
		return cause;
	};
	if (auto e = install(candidate.mode); !e.empty()) return rollback(e);
	stopped = true;
	if (auto e = run("stop"); !e.empty()) return rollback(e);

	// USB networking uses NCM; a legacy RNDIS preference goes on apply.
	std::map<std::string, bool> flags = {
		{"usb.rndis0", false}, {"usb.ncm", candidate.network}, {"usb.disk0", candidate.disk},
		{"usb.acm", candidate.serial}, {"usb.audio", candidate.audio},
		{"disable_hid", !candidate.keyboard && !candidate.relative && !candidate.absolute},
		{"usb.disable_keyboard", !candidate.keyboard}, {"usb.disable_relative", !candidate.relative},
		{"usb.disable_absolute", !candidate.absolute},
	};
	auto contents = content_files(candidate);
	for (auto &name : composition_files) {
		std::string path = join(boot_dir, name);
		const FileSnapshot &was = before[path];
		if (auto it = contents.find(name); it != contents.end()) {
			// A value, compared by content; absent when not in use.
			FileSnapshot want;
			if (it->second) want = {true, 0644, *it->second + "\n"};
			if (want.exists == was.exists && (!want.exists || want.data == was.data)) continue;
			if (auto e = write_usb_file(path, want); !e.empty()) return rollback(e);
			continue;
		}
		// A flag, by presence; its content (an image path, say) is kept.
		if (flags[name] == was.exists) continue;
		if (auto e = write_usb_file(path, FileSnapshot{flags[name], 0644, {}}); !e.empty()) return rollback(e);
	}
	if (auto e = run("start"); !e.empty()) return rollback(e);
	if (auto e = verify(candidate); !e.empty()) return rollback(e);
	return {};
}

std::string verify_storage_identity_at(const std::string &root, const StorageIdentity &identity)
{
	const char *kinds[] = {"disk", "setup", "user"};
	for (int n = 0; n < 3; n++) {
		std::string kind = kinds[n];
		std::string lun = root + "/functions/mass_storage.disk0/lun." + std::to_string(n);
		struct stat st{};
		if (n > 0 && ::stat(lun.c_str(), &st) != 0 && errno == ENOENT) continue;
		std::string data;
		if (n == 0) {
			if (auto e = read_file(lun + "/cdrom", data); !e.empty()) return e;
			if (gotext::trim_space(data) == "1") {
				kind = "cdrom";
				if (auto e = read_file(lun + "/file", data); !e.empty()) return e;
				if (gotext::trim_space(data) == remote_media_device) kind = "remote";
			}
		}
		if (auto e = read_file(lun + "/inquiry_string", data); !e.empty()) return e;
		if (!data.empty() && data.back() == '\n') data.pop_back();
		if (data != inquiry(identity, kind)) return std::format("storage identity does not match on LUN {}", n);
	}
	return {};
}

std::string verify_composition_at(const std::string &root, const Composition &s)
{
	std::string udc;
	if (auto e = read_file(root + "/UDC", udc); !e.empty()) return e;
	bool bound = !gotext::trim_space(udc).empty();
	if (bound == empty(s)) return "USB controller binding does not match the composition";
	auto linked = [&](const char *name) { return is_symlink(root + "/configs/c.1/" + name); };
	if (linked("hid.GS0") != s.keyboard || linked("hid.GS1") != s.relative || linked("hid.GS2") != s.absolute ||
	    linked("mass_storage.disk0") != s.disk || linked("acm.GS0") != s.serial || linked("uac1.audio0") != s.audio ||
	    (linked("rndis.usb0") || linked("ncm.usb0")) != s.network)
		return "USB functions do not match the requested composition";
	if (s.disk) {
		if (auto e = verify_storage_identity_at(root, s.storage_identity); !e.empty()) return e;
		// The user disk is the third LUN of the storage function.
		struct stat st{};
		bool lun = ::stat((root + "/functions/mass_storage.disk0/lun.2").c_str(), &st) == 0;
		if (lun != s.user_disk) return "the user disk LUN does not match the requested composition";
	}
	if (bound) {
		Identity presented;
		if (auto e = gadget_identity_at(root, presented); !e.empty()) return "read the USB identity: " + e;
		Identity want = normalized(s.identity);
		std::string serial = want.serial_number;
		want.serial_number.clear();
		if (presented != want) return "USB identity does not match the requested one";
		// The chip UID is checked by no one: hid-only without serial has none.
		if (!serial.empty() && gadget_serial_number_at(root) != serial) return "USB serial number does not match the requested one";
	}
	return {};
}

// --- role ---

std::string role_flags(const std::string &dir)
{
	if (lexists(join(dir, "usb.host"))) return "host";
	if (lexists(join(dir, "usb.off"))) return "off";
	return "gadget";
}

std::vector<std::pair<std::string, bool>> role_writes(const std::string &role)
{
	// The flag of the new role is written before the other is removed: a change between host and off cut short
	// leaves both, never neither (which would be the gadget role at the next boot).
	std::pair<std::string, bool> host{"usb.host", role == "host"}, off{"usb.off", role == "off"};
	if (off.second) return {off, host};
	return {host, off};
}

std::string RoleStore::set(const std::string &role) const
{
	std::string before = role_flags(dir);
	if (before == role) return verify(role);
	if (auto e = mkdir_all(dir); !e.empty()) return e;
	if (exclude)
		if (auto e = exclude(); !e.empty()) return e;
	auto write = [&](const std::string &r) -> std::string {
		for (auto &[name, exists] : role_writes(r))
			if (auto e = write_usb_file(join(dir, name), FileSnapshot{exists, 0644, {}}); !e.empty()) return e;
		return {};
	};
	auto apply = [&](const std::string &r) -> std::string {
		if (auto e = write(r); !e.empty()) return e;
		if (auto e = run("stop_start"); !e.empty()) return e;
		return verify(r);
	};
	if (auto e = apply(role); !e.empty()) {
		if (auto back = apply(before); !back.empty())
			return e + "; returning to the " + before + " role also failed: " + back;
		return e;
	}
	return {};
}

std::string verify_role_at(const std::string &role_glob, const std::string &gadget, const std::string &role,
                           const Composition &s)
{
	std::string want = role == "host" ? "host" : "device";
	std::vector<std::string> paths;
	glob_t g{};
	if (::glob(role_glob.c_str(), 0, nullptr, &g) == 0)
		for (std::size_t i = 0; i < g.gl_pathc; i++) paths.emplace_back(g.gl_pathv[i]);
	::globfree(&g);
	std::sort(paths.begin(), paths.end());
	if (paths.empty() && role == "host") return "this kernel has no USB role switch";
	for (auto &path : paths) {
		std::string data;
		if (auto e = read_file(path, data); !e.empty()) return e;
		// "none" is the default role, a device.
		std::string got = gotext::trim_space(data);
		if (got == "none") got = "device";
		if (got != want) return "the USB controller is a " + got + ", not a " + want;
	}
	std::string udc;
	if (auto e = read_file(gadget + "/UDC", udc); !e.empty()) return e;
	bool bound = !gotext::trim_space(udc).empty();
	if (bound != (role == "gadget" && !empty(s))) return "the USB gadget binding does not match the role";
	return {};
}

namespace {
// ^[0-9]+-[0-9]+(\.[0-9]+)*$
bool host_device_name(const std::string &name)
{
	std::size_t i = 0;
	auto digits = [&] {
		std::size_t start = i;
		while (i < name.size() && name[i] >= '0' && name[i] <= '9') i++;
		return i > start;
	};
	if (!digits() || i >= name.size() || name[i++] != '-' || !digits()) return false;
	while (i < name.size()) {
		if (name[i++] != '.' || !digits()) return false;
	}
	return true;
}
} // namespace

std::vector<HostDevice> host_devices_at(const std::string &root)
{
	std::vector<HostDevice> devices;
	DIR *d = ::opendir(root.c_str());
	if (!d) return devices;
	std::vector<std::string> names;
	while (auto *e = ::readdir(d))
		if (std::strcmp(e->d_name, ".") && std::strcmp(e->d_name, "..")) names.emplace_back(e->d_name);
	::closedir(d);
	std::sort(names.begin(), names.end());
	auto value = [](const std::string &dir, const char *name) { return gotext::trim_space(read(dir + "/" + name).value_or("")); };
	for (auto &name : names) {
		if (!host_device_name(name)) continue;
		std::string dir = join(root, name);
		std::string vendor = value(dir, "idVendor");
		if (vendor.empty()) continue;
		HostDevice device{name, vendor, value(dir, "idProduct"), value(dir, "manufacturer"), value(dir, "product"),
		                  value(dir, "speed"), value(dir, "bDeviceClass"), {}, value(dir, "bMaxPower")};
		// A device of class 00 names its classes per interface.
		std::vector<std::string> interfaces;
		glob_t g{};
		// filepath.Glob takes the name as a pattern too; port names hold no pattern characters.
		if (::glob((root + "/" + name + ":*/bInterfaceClass").c_str(), 0, nullptr, &g) == 0)
			for (std::size_t i = 0; i < g.gl_pathc; i++) interfaces.emplace_back(g.gl_pathv[i]);
		::globfree(&g);
		std::sort(interfaces.begin(), interfaces.end());
		for (auto &path : interfaces) {
			auto data = read(path);
			if (!data) continue;
			std::string cls = gotext::trim_space(*data);
			if (std::find(device.interface_classes.begin(), device.interface_classes.end(), cls) == device.interface_classes.end())
				device.interface_classes.push_back(cls);
		}
		std::sort(device.interface_classes.begin(), device.interface_classes.end());
		devices.push_back(std::move(device));
	}
	std::stable_sort(devices.begin(), devices.end(), [](const HostDevice &a, const HostDevice &b) { return a.port < b.port; });
	return devices;
}

std::string host_devices_json(const std::vector<HostDevice> &devices, const std::string &port_power)
{
	using gotext::json_string;
	std::string j = "{\"devices\":[";
	for (std::size_t i = 0; i < devices.size(); i++) {
		auto &d = devices[i];
		std::string classes = "null";
		if (!d.interface_classes.empty()) {
			classes = "[";
			for (std::size_t k = 0; k < d.interface_classes.size(); k++)
				classes += (k ? "," : "") + json_string(d.interface_classes[k]);
			classes += "]";
		}
		j += std::format(R"({}{{"port":{},"vendorId":{},"productId":{},"manufacturer":{},"product":{},"speed":{},)"
		                 R"("class":{},"interfaceClasses":{},"maxPower":{}}})",
		                 i ? "," : "", json_string(d.port), json_string(d.vendor_id), json_string(d.product_id),
		                 json_string(d.manufacturer), json_string(d.product), json_string(d.speed),
		                 json_string(d.device_class), classes, json_string(d.max_power));
	}
	return j + "],\"portPower\":" + json_string(port_power) + "}";
}

std::string port_power()
{
	if (gotext::trim_space(read("/etc/kvm/hw").value_or("")) == "pcie") return "none";
	struct stat st{};
	if (::stat("/etc/kvm/oled_exist", &st) != 0) return "shared";
	return "board";
}

// --- inittab, USB network flags ---

std::string remove_legacy_acm_getty_line(const std::string &data, bool &changed)
{
	changed = false;
	std::string out;
	for (std::size_t start = 0; start < data.size();) {
		auto nl = data.find('\n', start);
		std::size_t end = nl == std::string::npos ? data.size() : nl + 1;
		std::string line = data.substr(start, end - start);
		start = end;
		std::string trimmed = gotext::trim_space(line);
		if (trimmed.starts_with("acm::respawn:") && trimmed.find("ttyGS0") != std::string::npos) {
			changed = true;
			continue;
		}
		out += line;
	}
	return out;
}

std::string rewrite_inittab(const std::string &path, bool &changed)
{
	changed = false;
	std::string data;
	if (auto e = read_file(path, data); !e.empty()) return e;
	bool removed;
	std::string updated = remove_legacy_acm_getty_line(data, removed);
	if (!removed) return {};
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0) return path_error("stat", path, errno);
	std::string dir = dir_of(path);
	std::string tmp = dir + "/.inittab-XXXXXX";
	int fd = ::mkstemp(tmp.data());
	if (fd < 0) return path_error("open", tmp, errno);
	bool ok = ::fchmod(fd, st.st_mode & 0777) == 0;
	for (std::size_t done = 0; ok && done < updated.size();) {
		ssize_t n = ::write(fd, updated.data() + done, updated.size() - done);
		if (n < 0 && errno == EINTR) continue;
		ok = n > 0;
		if (ok) done += static_cast<std::size_t>(n);
	}
	ok = ok && ::fsync(fd) == 0;
	ok = ::close(fd) == 0 && ok;
	ok = ok && ::rename(tmp.c_str(), path.c_str()) == 0;
	if (!ok) {
		std::string e = path_error("write", tmp, errno);
		::unlink(tmp.c_str());
		return e;
	}
	changed = true;
	return {};
}

std::string save_usb_flag(const std::string &boot, const std::string &name, bool enabled,
                          const std::function<std::string()> &apply)
{
	std::string path = join(boot, name);
	if (auto e = mkdir_all(boot); !e.empty()) return e;
	FileSnapshot before;
	if (auto e = snapshot_file(path, before); !e.empty()) return e;
	if (auto e = write_usb_file(path, FileSnapshot{enabled, 0644, {}}); !e.empty()) return e;
	std::string e = apply();
	if (e.empty()) return {};
	if (auto rollback = write_usb_file(path, before); !rollback.empty()) return e + "; restore setting: " + rollback;
	if (auto rollback = apply(); !rollback.empty()) return e + "; restore runtime: " + rollback;
	return e;
}

} // namespace nkos::usb
