#include "video/monitor.hpp"

#include "common/log.hpp"
#include "video/screen.hpp"

#include <glaze/glaze.hpp>
#include <openssl/evp.h>

#include <format>
#include <fstream>
#include <map>
#include <sstream>
#include <sys/stat.h>

namespace nkos::video::monitor {

namespace {

constexpr const char *edid_dir = "/usr/share/nanokvm/edid";
constexpr const char *resolution_file = "/etc/kvm/monitor_resolution";
constexpr const char *portrait_file = "/etc/kvm/monitor_portrait";
constexpr const char *portrait_resolution_file = "/etc/kvm/monitor_portrait_resolution";
constexpr const char *identity_file = "/etc/kvm/monitor_identity";
constexpr const char *hash_file = "/etc/kvm/monitor_edid_sha256";
constexpr const char *power_cycle_file = "/etc/kvm/monitor_power_cycle_pending";
constexpr std::uint16_t retired_portrait = 2304;

struct Profile {
	std::uint16_t width, height;
	const char *aspect;
};
const std::map<std::uint16_t, Profile> wider = {
	{1200, {1920, 1200, "16:10"}}, {1600, {2560, 1600, "16:10"}}, {2560, {2560, 1080, "21:9"}},
	{3440, {3440, 1440, "21:9"}},  {1280, {1280, 1440, "8:9"}},   {2880, {2560, 2880, "8:9"}},
};
const std::map<std::uint16_t, std::vector<int>> wider_rates = {
	{1200, {60, 50, 30}}, {1600, {50, 30}}, {2560, {60, 50, 30}}, {3440, {40, 30}}, {1280, {60, 50, 30}}, {2880, {30}},
};
const std::map<std::uint16_t, std::vector<int>> slow_wider_rates = {{1200, {50, 30}}, {2560, {30}}};
const std::map<std::uint16_t, std::vector<int>> monitor_rates = {
	{720, {120, 60, 30}}, {1080, {90, 75, 60, 30}}, {1440, {60, 50, 40, 30}}, {2160, {30}}};
const std::map<std::uint16_t, std::vector<int>> portrait_rate_table = {
	{portrait_hd, {120, 60, 30}}, {portrait_default, {90, 75, 60, 30}}, {portrait_max, {60, 50, 30}}};
const std::map<std::uint16_t, std::uint16_t> portrait_widths = {{portrait_hd, 720}, {portrait_default, 1080}, {portrait_max, 1440}};

std::optional<std::string> read(const std::string &path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return std::nullopt;
	std::ostringstream s;
	s << in.rdbuf();
	return s.str();
}

std::string trim(const std::string &s)
{
	auto a = s.find_first_not_of(" \t\r\n");
	if (a == std::string::npos) return {};
	return s.substr(a, s.find_last_not_of(" \t\r\n") - a + 1);
}

bool regular(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && S_ISREG(st.st_mode);
}

bool exists(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0;
}

// Read once per value the scripts publish before the server starts; anything unknown is read again.
std::string identity_value(const char *path, std::initializer_list<const char *> known)
{
	static std::map<std::string, std::string> cache;
	if (auto it = cache.find(path); it != cache.end()) return it->second;
	std::string v = trim(read(path).value_or(""));
	for (auto *k : known)
		if (v == k) cache[path] = v;
	return v;
}

bool valid_profile(std::uint16_t id) { return wider.count(id) || id == 0 || id == 720 || id == 1080 || id == 1440 || id == 2160; }

std::vector<int> wider_rates_for(std::uint16_t id)
{
	if (!profile_supported()) return {};
	auto &table = high_refresh_supported() ? wider_rates : slow_wider_rates;
	auto it = table.find(id);
	return it == table.end() ? std::vector<int>{} : it->second;
}

std::string rate_profile_path(std::uint16_t id, int rate)
{
	if (auto it = wider.find(id); it != wider.end())
		return std::format("{}/NanoKVM-monitor-{}x{}-{}.bin", edid_dir, it->second.width, it->second.height, rate);
	return std::format("{}/NanoKVM-monitor-{}-{}.bin", edid_dir, id, rate);
}

std::string profile_path(std::uint16_t height)
{
	if (auto r = wider_rates_for(height); !r.empty()) return rate_profile_path(height, r[0]);
	if (profile_supported() && !high_refresh_supported())
		return std::format("{}/NanoKVM-cube-monitor-{}.bin", edid_dir, height == 0 ? 1080 : height);
	if (height == 0)
		return std::format("{}/{}", edid_dir, high_refresh_supported() ? "NanoKVM-final-video-profiles.bin" : "NanoKVM-stock.bin");
	return std::format("{}/NanoKVM-monitor-{}.bin", edid_dir, height);
}

bool portrait_stride_supported()
{
	return trim(read("/sys/module/cv181x_vi/parameters/yuv_bypass_aligned_stride").value_or("")) == "Y";
}

bool portrait_supported_for(const VideoPool &p)
{
	return high_refresh_supported() && portrait_stride_supported() && profile_supported() && p.qhd() &&
	       regular(std::string(edid_dir) + "/NanoKVM-portrait-1080x1920.bin");
}

bool portrait_max_supported_for(const VideoPool &p)
{
	return high_refresh_supported() && profile_supported() && p.mib >= 54 &&
	       regular(std::string(edid_dir) + "/NanoKVM-portrait-1440x2560.bin");
}

struct Identity {
	std::string manufacturer;
	std::uint16_t productCode = 0;
	std::string name;
	std::optional<std::uint32_t> serialNumber;
	std::string serialText;
	std::uint8_t week = 0;
	std::uint16_t year = 0;
};

bool edid_text(const std::string &s)
{
	if (s.size() > 13) return false;
	for (unsigned char c : s)
		if (c < 0x20 || c > 0x7e) return false;
	return true;
}

bool valid(const Identity &id)
{
	if (id.manufacturer.size() != 3) return false;
	for (char c : id.manufacturer)
		if (c < 'A' || c > 'Z') return false;
	if (id.name.empty() || !edid_text(id.name) || !edid_text(id.serialText)) return false;
	if (!id.serialText.empty() && !id.serialNumber) return false;
	if (id.week > 54 && id.week != 255) return false;
	return id.year >= 1990 && id.year <= 2245;
}

std::optional<Identity> saved_identity()
{
	auto data = read(identity_file);
	if (!data) return std::nullopt;
	Identity id;
	if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(id, *data); e) {
		log::warn("ignoring the saved monitor identity: {}", glz::format_error(e, *data));
		return std::nullopt;
	}
	if (!valid(id)) {
		log::warn("ignoring the saved monitor identity: invalid");
		return std::nullopt;
	}
	return id;
}

void text_descriptor(unsigned char *dst, std::size_t len, const std::string &text)
{
	std::size_t n = std::min(len, text.size());
	std::copy(text.begin(), text.begin() + static_cast<long>(n), dst);
	if (n < len) {
		dst[n] = '\n';
		for (std::size_t i = n + 1; i < len; i++) dst[i] = ' ';
	}
}

// The EDID with the identity written in, as withMonitorIdentity.
std::optional<std::string> with_identity(std::string edid, const Identity &id)
{
	if (edid.size() < 128 || edid.compare(0, 8, std::string("\x00\xff\xff\xff\xff\xff\xff\x00", 8)) != 0) return std::nullopt;
	auto *out = reinterpret_cast<unsigned char *>(edid.data());
	std::uint16_t m = static_cast<std::uint16_t>((id.manufacturer[0] - '@') << 10 | (id.manufacturer[1] - '@') << 5 | (id.manufacturer[2] - '@'));
	out[8] = static_cast<unsigned char>(m >> 8);
	out[9] = static_cast<unsigned char>(m);
	out[10] = static_cast<unsigned char>(id.productCode);
	out[11] = static_cast<unsigned char>(id.productCode >> 8);
	std::uint32_t serial = out[12] | out[13] << 8 | out[14] << 16 | static_cast<std::uint32_t>(out[15]) << 24;
	std::string text = std::to_string(serial);
	if (id.serialNumber) {
		serial = *id.serialNumber;
		for (int i = 0; i < 4; i++) out[12 + i] = static_cast<unsigned char>(serial >> (8 * i));
		text = id.serialText.empty() ? std::to_string(serial) : id.serialText;
	}
	out[16] = id.week;
	out[17] = static_cast<unsigned char>(id.year - 1990);
	bool named = false;
	for (int pos = 54; pos + 18 <= 126; pos += 18) {
		if (out[pos] || out[pos + 1] || out[pos + 2]) continue;
		if (out[pos + 3] == 0xfc) {
			text_descriptor(out + pos + 5, 13, id.name);
			named = true;
		} else if (out[pos + 3] == 0xff) {
			text_descriptor(out + pos + 5, 13, text);
		}
	}
	if (!named) return std::nullopt;
	unsigned char sum = 0;
	for (int i = 0; i < 127; i++) sum = static_cast<unsigned char>(sum + out[i]);
	out[127] = static_cast<unsigned char>(-sum);
	return edid;
}

std::optional<std::string> profile_sha256(const std::string &path, const std::optional<Identity> &id)
{
	auto data = read(path);
	if (!data) return std::nullopt;
	if (id) {
		data = with_identity(*data, *id);
		if (!data) return std::nullopt;
	}
	unsigned char sum[32];
	EVP_Digest(data->data(), data->size(), sum, nullptr, EVP_sha256(), nullptr);
	std::string hex;
	for (unsigned char b : sum) hex += std::format("{:02x}", b);
	return hex;
}

bool programmed(const std::string &path, std::uint16_t height)
{
	auto id = saved_identity();
	auto want = profile_sha256(path, id);
	if (!want) return false;
	auto recorded = read(hash_file);
	if (!recorded) {
		if (requires_power_cycle()) return false;
		auto legacy = profile_sha256(profile_path(height), id);
		return legacy && *legacy == *want;
	}
	return trim(*recorded) == *want;
}

} // namespace

std::string board() { return identity_value("/etc/kvm/hw", {"alpha", "beta", "pcie"}); }
std::string receiver_chip() { return identity_value("/etc/kvm/hdmi_version", {"c", "ux", "d", "ue"}); }

std::string receiver_model()
{
	std::string c = receiver_chip();
	return c == "c" ? "LT6911C" : c == "ux" ? "LT6911UXC" : c == "d" ? "LT6911D" : "";
}

bool profile_supported()
{
	std::string b = board(), c = receiver_chip();
	if (b == "pcie") return c == "ux" || c == "d";
	if (b == "alpha" || b == "beta") return c == "c" || c == "ux" || c == "d";
	return false;
}

bool high_refresh_supported()
{
	std::string c = receiver_chip();
	return profile_supported() && (c == "ux" || c == "d");
}

bool requires_power_cycle()
{
	std::string b = board();
	return (b == "alpha" || b == "beta") && receiver_chip() != "d";
}

bool power_cycle_pending() { return exists(power_cycle_file) && requires_power_cycle(); }
bool refresh_follows_stream() { return high_refresh_supported() && !requires_power_cycle(); }

std::pair<std::uint16_t, std::uint16_t> size(std::uint16_t id)
{
	if (auto it = wider.find(id); it != wider.end()) return {it->second.width, it->second.height};
	if (id == 0 || !valid_profile(id)) return {0, 0};
	return {resolution_width(id), id};
}

std::string aspect(std::uint16_t id)
{
	if (auto it = wider.find(id); it != wider.end()) return it->second.aspect;
	return "16:9";
}

bool wider_profile(std::uint16_t id) { return wider.count(id) != 0; }
bool wider_profile_offered(std::uint16_t id) { return !wider_rates_for(id).empty(); }
std::uint16_t auto_height() { return high_refresh_supported() ? 1080 : 0; }

std::vector<int> rates(std::uint16_t height)
{
	if (profile_supported() && !high_refresh_supported()) {
		if (height == 0 || height == 720 || height == 1080) return {60};
		return wider_rates_for(height);
	}
	if (height == 0) height = auto_height();
	if (auto it = wider_rates.find(height); it != wider_rates.end()) return it->second;
	if (auto it = monitor_rates.find(height); it != monitor_rates.end()) return it->second;
	return {};
}

std::string pool_need(std::uint16_t id)
{
	auto [w, h] = size(id);
	long pixels = static_cast<long>(w) * h;
	if (pixels > 2560L * 1440) return "uhd";
	if (pixels > 1920L * 1088 || h > 1088) return "qhd";
	return "";
}

std::uint16_t selected_resolution()
{
	int value = read_video_value(resolution_file);
	if (value < 0 || value > 65535) return 0;
	auto h = static_cast<std::uint16_t>(value);
	if (resolution_width(h) == 0 && h != 0 && !wider_profile(h)) return 0;
	if (wider_profile(h) && !wider_profile_offered(h)) return 0;
	return h;
}

bool portrait_enabled()
{
	std::string v = trim(read(portrait_file).value_or(""));
	for (auto &c : v) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return v == "1" || v == "true" || v == "yes" || v == "on";
}

bool portrait_supported() { return portrait_supported_for(booted_video_pool()); }
bool portrait_max_supported() { return portrait_max_supported_for(booted_video_pool()); }
bool portrait_supported_for_shared_pool() { return portrait_supported_for(VideoPool{144, true}); }
bool portrait_max_supported_for_shared_pool() { return portrait_max_supported_for(VideoPool{144, true}); }

std::uint16_t portrait_resolution()
{
	int value = read_video_value(portrait_resolution_file);
	if (value < 0 || value > 65535) return portrait_default;
	auto r = static_cast<std::uint16_t>(value);
	if (r == retired_portrait) {
		// The retired 1296x2304 profile: 1080x1920 for an H.264 device, 1440x2560 otherwise.
		std::string codec;
		for (const char *path : {"/etc/kvm/encoder_codec", "/kvmapp/kvm/type"}) {
			codec = trim(read(path).value_or(""));
			for (auto &c : codec) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
			if (!codec.empty()) break;
		}
		return codec == "h264" ? portrait_default : portrait_max;
	}
	if (r != portrait_hd && r != portrait_default && r != portrait_max) return portrait_default;
	return r;
}

std::uint16_t portrait_width(std::uint16_t resolution)
{
	auto it = portrait_widths.find(resolution);
	return it == portrait_widths.end() ? 0 : it->second;
}

std::vector<int> portrait_rates(std::uint16_t resolution)
{
	auto it = portrait_rate_table.find(resolution);
	return it == portrait_rate_table.end() ? std::vector<int>{} : it->second;
}

int refresh_hz()
{
	std::uint16_t height = selected_resolution();
	if (portrait_enabled()) {
		std::uint16_t r = portrait_resolution();
		for (int rate : portrait_rates(r))
			if (programmed(std::format("{}/NanoKVM-portrait-{}x{}-{}.bin", edid_dir, portrait_width(r), r, rate), height))
				return rate;
		return 0;
	}
	for (int rate : rates(height)) {
		std::uint16_t effective = height == 0 ? auto_height() : height;
		if (programmed(rate_profile_path(effective, rate), height)) return rate;
	}
	return 0;
}

bool stock_profile() { return requires_power_cycle() && !exists(hash_file); }

std::string identity_json()
{
	auto id = saved_identity();
	if (!id) return "null";
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = false}>(*id, out);
	return out;
}

} // namespace nkos::video::monitor
