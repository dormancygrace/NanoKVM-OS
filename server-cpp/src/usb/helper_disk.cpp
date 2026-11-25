#include "usb/helper_disk.hpp"

#include "common/log.hpp"
#include "config/config.hpp"
#include "usb/gotext.hpp"

#include <openssl/evp.h>

#include <cerrno>
#include <cstdint>
#include <cstring>
#include <format>
#include <fstream>
#include <set>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::usb::helperdisk {

namespace {

constexpr std::size_t sector = 512;
constexpr std::size_t sectors_per_cluster = 8;  // 4 KiB
constexpr std::size_t total_sectors = 24576;    // 12 MiB: room for the agents, few enough clusters for FAT12
constexpr std::size_t reserved_sectors = 1;
constexpr std::size_t fats = 2;
constexpr std::size_t root_entries = 224;
constexpr std::size_t sectors_per_fat = 9;
constexpr unsigned char media = 0xf8;
constexpr std::size_t root_sectors = root_entries * 32 / sector;
constexpr std::size_t data_sector = reserved_sectors + fats * sectors_per_fat + root_sectors;
constexpr std::size_t data_clusters = (total_sectors - data_sector) / sectors_per_cluster;
constexpr std::uint16_t fat_date = (2026 - 1980) << 9 | 1 << 5 | 1;  // 2026-01-01
constexpr unsigned char attr_archive = 0x20, attr_label = 0x08;
constexpr const char *label = "NANOKVM    ";

// The stub mkfs.fat writes: a BIOS that boots from the disk prints the message, waits for a key and goes on.
const std::string boot_code = std::string("\x0e\x1f\xbe\x5b\x7c\xac\x22\xc0\x74\x0b\x56\xb4\x0e\xbb\x07\x00\xcd\x10\x5e\xeb\xf0"
                                          "\x32\xe4\xcd\x16\xcd\x19\xeb\xfe", 29) +
                              "This is not a bootable disk. It holds the NanoKVM helper programs.\r\nRemove it from the "
                              "boot order and press any key ...\r\n";

const char *readme = R"(NanoKVM helper disk
===================

This disk comes from the NanoKVM's virtual USB drive. It is read-only.

NanoKVM-Sensors-Setup-Windows.exe
    The computer sensors agent for Windows. It sends this computer's
    temperatures, voltages and fan speeds to the NanoKVM (Settings >
    Services > Computer sensors). Double-click it: it installs the agent,
    updates an older one, or removes it. It offers to install the PawnIO
    driver, which processor and board sensors need.
    From an administrator's console instead:
        NanoKVM-Sensors-Setup-Windows.exe install
        NanoKVM-Sensors-Setup-Windows.exe uninstall
        NanoKVM-Sensors-Setup-Windows.exe --help

NanoKVM-Sensors-Setup-Linux.sh
    The computer sensors agent for Linux (the kernel's sensors, hwmon). As
    root, with this disk mounted on /mnt:
        sh /mnt/NanoKVM-Sensors-Setup-Linux.sh              ask: install,
                                                            update or remove
        sh /mnt/NanoKVM-Sensors-Setup-Linux.sh install
        sh /mnt/NanoKVM-Sensors-Setup-Linux.sh uninstall
        sh /mnt/NanoKVM-Sensors-Setup-Linux.sh --help

NanoKVM-Serial-Console-Setup-Linux.sh
    A login console on the NanoKVM's USB serial port for a Linux computer.
    As root, with this disk mounted on /mnt:
        sh /mnt/NanoKVM-Serial-Console-Setup-Linux.sh          report only
        sh /mnt/NanoKVM-Serial-Console-Setup-Linux.sh apply    make the changes
        sh /mnt/NanoKVM-Serial-Console-Setup-Linux.sh undo     remove them
)";

void put16(std::string &b, std::size_t at, std::uint16_t v)
{
	b[at] = static_cast<char>(v & 0xff);
	b[at + 1] = static_cast<char>(v >> 8);
}
void put32(std::string &b, std::size_t at, std::uint32_t v)
{
	for (int i = 0; i < 4; i++) b[at + i] = static_cast<char>((v >> (8 * i)) & 0xff);
}

std::string upper(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::toupper(static_cast<unsigned char>(c)));
	return s;
}
std::string pad(std::string s, std::size_t n) { return s.size() >= n ? s : s + std::string(n - s.size(), ' '); }

// The 8.3 alias BASE~N.EXT of a long name and the VFAT entries carrying the long name.
bool names(const std::string &name, std::size_t n, std::string &base, std::string &ext, std::string &lfn, std::string &error)
{
	bool ok = !name.empty() && name.size() <= 100 && name.front() != '.' && name.back() != '.';
	for (char c : name)
		ok = ok && ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.');
	if (!ok) {
		error = "not a file name for the helper disk: " + gotext::quote(name);
		return false;
	}
	std::string stem = name;
	ext.clear();
	if (auto dot = name.rfind('.'); dot != std::string::npos && dot > 0) {
		stem = name.substr(0, dot);
		ext = upper(name.substr(dot + 1));
	}
	if (ext.size() > 3) ext.resize(3);
	std::erase(stem, '.');
	stem = upper(stem);
	if (stem.size() > 6) stem.resize(6);
	base = stem + "~" + std::to_string(n);
	if (base.size() > 8) {
		error = "too many files for the alias of " + gotext::quote(name);
		return false;
	}
	// The checksum of the 8.3 name ties the long entries to it.
	std::string short_name = pad(base, 8) + pad(ext, 3);
	unsigned char sum = 0;
	for (int i = 0; i < 11; i++) sum = static_cast<unsigned char>(((sum >> 1) | (sum << 7)) + static_cast<unsigned char>(short_name[i]));
	// 13 UTF-16 units an entry: the name, one zero, then 0xFFFF; the last part first, marked 0x40.
	std::vector<std::uint16_t> units(name.begin(), name.end());
	if (units.size() % 13 != 0) {
		units.push_back(0);
		while (units.size() % 13 != 0) units.push_back(0xffff);
	}
	std::size_t count = units.size() / 13;
	lfn.clear();
	static const int offsets[] = {1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30};
	for (std::size_t seq = count; seq >= 1; seq--) {
		std::string e(32, '\0');
		e[0] = static_cast<char>(seq | (seq == count ? 0x40 : 0));
		e[11] = 0x0f;
		e[13] = static_cast<char>(sum);
		for (int i = 0; i < 13; i++) put16(e, offsets[i], units[(seq - 1) * 13 + i]);
		lfn += e;
	}
	return true;
}

std::string dir_entry(const std::string &base, const std::string &ext, unsigned char attr, std::uint16_t cluster, std::uint32_t size)
{
	std::string e(32, '\0');
	std::string n = pad(base, 8) + pad(ext, 3);
	e.replace(0, std::min<std::size_t>(n.size(), 32), n.substr(0, 32));
	e[11] = static_cast<char>(attr);
	put16(e, 16, fat_date);  // created
	put16(e, 18, fat_date);  // accessed
	put16(e, 24, fat_date);  // written
	put16(e, 26, cluster);
	put32(e, 28, size);
	return e;
}

std::string lower(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return s;
}

} // namespace

bool build(const std::vector<File> &files, std::string &image, std::string &error)
{
	std::vector<std::uint16_t> fat = {0xf00 | media, 0xfff};
	std::string root = dir_entry(std::string(label, 8), std::string(label + 8, 3), attr_label, 0, 0);
	image.assign(size, '\0');
	std::size_t next = 2;
	std::set<std::string> seen;
	EVP_MD_CTX *id = EVP_MD_CTX_new();
	EVP_DigestInit_ex(id, EVP_sha256(), nullptr);
	struct Free {
		EVP_MD_CTX *c;
		~Free() { EVP_MD_CTX_free(c); }
	} free_id{id};
	for (std::size_t n = 0; n < files.size(); n++) {
		const File &f = files[n];
		std::string base, ext, lfn;
		if (!names(f.name, n + 1, base, ext, lfn, error)) return false;
		if (!seen.insert(lower(f.name)).second) {
			error = "two files named " + f.name;
			return false;
		}
		std::size_t count = (f.data.size() + sectors_per_cluster * sector - 1) / (sectors_per_cluster * sector);
		if (next - 2 + count > data_clusters) {
			error = "the files do not fit on the helper disk";
			return false;
		}
		std::size_t first = 0;
		if (count > 0) {
			first = next;
			for (std::size_t i = 1; i < count; i++) fat.push_back(static_cast<std::uint16_t>(first + i));
			fat.push_back(0xfff);
			image.replace((data_sector + (first - 2) * sectors_per_cluster) * sector, f.data.size(), f.data);
			next += count;
		}
		root += lfn;
		root += dir_entry(base, ext, attr_archive, static_cast<std::uint16_t>(first), static_cast<std::uint32_t>(f.data.size()));
		if (root.size() > root_entries * 32) {
			error = "too many files for the helper disk";
			return false;
		}
		std::string head = std::format("{} {}\n", f.name, f.data.size());
		EVP_DigestUpdate(id, head.data(), head.size());
		EVP_DigestUpdate(id, f.data.data(), f.data.size());
	}
	unsigned char sum[32];
	EVP_DigestFinal_ex(id, sum, nullptr);

	// The boot sector; the volume id follows the contents, so the computer sees a changed disk as another one.
	image.replace(0, 3, "\xeb\x3c\x90");
	image.replace(3, 8, "NANOKVM ");
	put16(image, 11, sector);
	image[13] = static_cast<char>(sectors_per_cluster);
	put16(image, 14, reserved_sectors);
	image[16] = static_cast<char>(fats);
	put16(image, 17, root_entries);
	put16(image, 19, total_sectors);
	image[21] = static_cast<char>(media);
	put16(image, 22, sectors_per_fat);
	put16(image, 24, 63);   // sectors per track
	put16(image, 26, 255);  // heads
	image[36] = static_cast<char>(0x80);
	image[38] = 0x29;
	image.replace(39, 4, reinterpret_cast<const char *>(sum), 4);
	image.replace(43, 11, label);
	image.replace(54, 8, "FAT12   ");
	image.replace(0x3e, boot_code.size(), boot_code);
	image[510] = 0x55;
	image[511] = static_cast<char>(0xaa);

	// FAT12: two entries in three bytes.
	std::string table(sectors_per_fat * sector, '\0');
	for (std::size_t i = 0; i < fat.size(); i += 2) {
		std::uint16_t a = fat[i], b = i + 1 < fat.size() ? fat[i + 1] : 0;
		std::size_t o = i / 2 * 3;
		table[o] = static_cast<char>(a & 0xff);
		table[o + 1] = static_cast<char>(((a >> 8) & 0x0f) | ((b & 0x0f) << 4));
		table[o + 2] = static_cast<char>((b >> 4) & 0xff);
	}
	for (std::size_t n = 0; n < fats; n++) image.replace((reserved_sectors + n * sectors_per_fat) * sector, table.size(), table);
	image.replace((reserved_sectors + fats * sectors_per_fat) * sector, root.size(), root);
	return true;
}

namespace {

bool read_file(const std::string &path, std::string &out)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return false;
	out.assign(std::istreambuf_iterator<char>(in), std::istreambuf_iterator<char>());
	return !in.bad();
}

std::string mkdir_all(const std::string &dir)
{
	struct stat st{};
	if (dir.empty() || ::stat(dir.c_str(), &st) == 0) return {};
	auto slash = dir.rfind('/');
	if (slash != std::string::npos && slash > 0)
		if (auto e = mkdir_all(dir.substr(0, slash)); !e.empty()) return e;
	if (::mkdir(dir.c_str(), 0755) != 0 && errno != EEXIST) return "mkdir " + dir + ": " + gotext::errno_text(errno);
	return {};
}

std::string signature(const std::vector<std::string> &paths)
{
	std::string out;
	for (auto &path : paths) {
		struct stat st{};
		if (::stat(path.c_str(), &st) == 0)
			out += std::format("{} {} {}\n", path, static_cast<long long>(st.st_size),
			                   static_cast<long long>(st.st_mtim.tv_sec) * 1'000'000'000LL + st.st_mtim.tv_nsec);
		else
			out += path + " -\n";
	}
	return out;
}

} // namespace

std::string publish(const std::string &path, const std::string &image, bool &written)
{
	written = false;
	std::string old;
	if (read_file(path, old) && old == image) return {};
	auto slash = path.rfind('/');
	if (auto e = mkdir_all(slash == std::string::npos ? "." : path.substr(0, slash)); !e.empty()) return e;
	if (!config::atomic_write(path, image, 0644)) return "write " + path + ": " + gotext::errno_text(errno);
	written = true;
	return {};
}

void Composer::reconcile(bool sensors_on)
{
	if (!sensors_on) {
		built_from.clear();
		if (::unlink(disk_file.c_str()) != 0 && errno != ENOENT)
			log::error("helper disk: remove {}: {}", disk_file, gotext::errno_text(errno));
		return;
	}
	std::string sig = signature({agent, linux_agent, console_script});
	struct stat st{};
	if (::stat(disk_file.c_str(), &st) == 0 && sig == built_from) return;
	std::string agent_data;
	if (!read_file(agent, agent_data)) {
		log::error("helper disk: open {}: {}", agent, gotext::errno_text(errno));
		return;
	}
	std::string text = readme, crlf;
	for (char c : text) crlf += c == '\n' ? std::string("\r\n") : std::string(1, c);
	std::vector<File> files = {{"README.txt", crlf}};
	// The console helper is part of NanoKVM OS; an image without it is still better than none.
	if (std::string script; read_file(console_script, script)) files.push_back({"NanoKVM-Serial-Console-Setup-Linux.sh", script});
	files.push_back({"NanoKVM-Sensors-Setup-Windows.exe", agent_data});
	if (std::string script; read_file(linux_agent, script)) files.push_back({"NanoKVM-Sensors-Setup-Linux.sh", script});
	std::string image, error;
	if (!build(files, image, error)) {
		log::error("helper disk: {}", error);
		return;
	}
	bool written = false;
	if (auto e = publish(disk_file, image, written); !e.empty()) {
		// Not remembered as built: the next pass tries again.
		log::error("helper disk: {}", e);
		return;
	}
	if (written) log::info("helper disk: built with the computer sensors agent");
	built_from = sig;
}

} // namespace nkos::usb::helperdisk
