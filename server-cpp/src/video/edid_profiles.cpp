#include "video/edid_profiles.hpp"

#include <openssl/evp.h>

#include <algorithm>
#include <cstdio>
#include <format>
#include <set>

namespace nkos::video::edid {

namespace {

const std::string edid_header("\x00\xff\xff\xff\xff\xff\xff\x00", 8);

bool edid_text(const std::string &s)
{
	if (s.size() > 13) return false;
	for (unsigned char c : s)
		if (c < 0x20 || c > 0x7e) return false;
	return true;
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

unsigned char checksum_byte(const unsigned char *block)
{
	unsigned char sum = 0;
	for (int i = 0; i < 127; i++) sum = static_cast<unsigned char>(sum + block[i]);
	return static_cast<unsigned char>(-sum);
}

// The CTA-861 video codes an LT6911C cannot capture.
const std::set<int> &cube_unsafe_vics()
{
	static const std::set<int> vics = [] {
		std::set<int> v{63, 64, 77, 78, 85, 113};
		for (auto [a, b] : {std::pair{86, 107}, std::pair{114, 127}, std::pair{193, 219}})
			for (int i = a; i <= b; i++) v.insert(i);
		return v;
	}();
	return vics;
}

constexpr double cube_max_pixel_rate = 1920.0 * 1080 * 61;

// Go's %.0f: both round the exact binary value.
std::string hz_text(double hz) { return std::format("{:.0f}", hz); }

} // namespace

std::vector<int> wider_rates_for(const Hardware &hw, std::uint16_t id)
{
	if (!hw.profile_supported()) return {};
	const auto &table = hw.high_refresh() ? wider_rates : slow_wider_rates;
	auto it = table.find(id);
	return it == table.end() ? std::vector<int>{} : it->second;
}

bool wider_profile(std::uint16_t id) { return wider.count(id) != 0; }

bool valid_profile(std::uint16_t id) { return wider_profile(id) || id == 0 || id == 720 || id == 1080 || id == 1440 || id == 2160; }

std::pair<std::uint16_t, std::uint16_t> size(std::uint16_t id)
{
	if (auto it = wider.find(id); it != wider.end()) return {it->second.width, it->second.height};
	if (id == 0 || !valid_profile(id)) return {0, 0};
	return {resolution_map.at(id), id};
}

std::string pool_need(std::uint16_t id)
{
	auto [w, h] = size(id);
	long pixels = static_cast<long>(w) * h;
	if (pixels > 2560L * 1440) return "uhd";
	if (pixels > 1920L * 1088 || h > 1088) return "qhd";
	return "";
}

std::uint16_t auto_height(const Hardware &hw) { return hw.high_refresh() ? 1080 : 0; }

std::vector<int> monitor_rates(const Hardware &hw, std::uint16_t height)
{
	if (hw.profile_supported() && !hw.high_refresh()) {
		if (height == 0 || height == 720 || height == 1080) return {60};
		return wider_rates_for(hw, height);
	}
	if (height == 0) height = auto_height(hw);
	if (auto it = wider_rates.find(height); it != wider_rates.end()) return it->second;
	if (auto it = monitor_rate_table.find(height); it != monitor_rate_table.end()) return it->second;
	return {};
}

int slowest_rate_for(const std::vector<int> &rates, int fps)
{
	if (rates.empty()) return 0;
	int best = rates[0];
	for (int r : rates)
		if (r >= fps) best = r;
	return best;
}

int profile_rate(const Hardware &hw, int fps) { return hw.follows_refresh() ? fps : 1 << 30; }

std::string rate_profile_path(const std::string &dir, std::uint16_t id, int rate)
{
	if (auto it = wider.find(id); it != wider.end())
		return std::format("{}/NanoKVM-monitor-{}x{}-{}.bin", dir, it->second.width, it->second.height, rate);
	return std::format("{}/NanoKVM-monitor-{}-{}.bin", dir, id, rate);
}

std::string cube_profile_path(const std::string &dir, std::uint16_t height)
{
	return std::format("{}/NanoKVM-cube-monitor-{}.bin", dir, height == 0 ? 1080 : height);
}

std::string portrait_edid_path(const std::string &dir, std::uint16_t resolution)
{
	if (resolution == portrait_max) return dir + "/NanoKVM-portrait-1440x2560.bin";
	if (resolution == portrait_hd) return dir + "/NanoKVM-portrait-720x1280.bin";
	return dir + "/NanoKVM-portrait-1080x1920.bin";
}

std::string profile_path(const Hardware &hw, const std::string &dir, std::uint16_t height)
{
	if (auto r = wider_rates_for(hw, height); !r.empty()) return rate_profile_path(dir, height, r[0]);
	if (hw.profile_supported() && !hw.high_refresh()) return cube_profile_path(dir, height);
	if (height == 0) return dir + (hw.high_refresh() ? "/NanoKVM-final-video-profiles.bin" : "/NanoKVM-stock.bin");
	return std::format("{}/NanoKVM-monitor-{}.bin", dir, height);
}

std::string profile_path_at(const Hardware &hw, const std::string &dir, std::uint16_t height, int fps, const Exists &regular)
{
	if (hw.high_refresh()) {
		std::uint16_t effective = height == 0 ? auto_height(hw) : height;
		if (int rate = slowest_rate_for(monitor_rates(hw, height), profile_rate(hw, fps)); rate > 0) {
			std::string path = rate_profile_path(dir, effective, rate);
			if (regular(path)) return path;
		}
	}
	return profile_path(hw, dir, height);
}

std::string portrait_profile_path_at(const Hardware &hw, const std::string &dir, std::uint16_t resolution, int fps,
                                     const Exists &regular)
{
	auto rates = portrait_rate_table.find(resolution);
	if (rates != portrait_rate_table.end()) {
		if (int rate = slowest_rate_for(rates->second, profile_rate(hw, fps)); rate > 0) {
			std::string path = std::format("{}/NanoKVM-portrait-{}x{}-{}.bin", dir, portrait_widths.at(resolution), resolution, rate);
			if (regular(path)) return path;
		}
	}
	return portrait_edid_path(dir, resolution);
}

std::optional<std::string> validate(const Identity &id)
{
	bool letters = id.manufacturer.size() == 3 &&
	               std::all_of(id.manufacturer.begin(), id.manufacturer.end(), [](char c) { return c >= 'A' && c <= 'Z'; });
	if (!letters) return "the manufacturer ID must be three letters A-Z";
	if (id.name.empty() || !edid_text(id.name)) return "the monitor name must be 1-13 printable ASCII characters";
	if (!edid_text(id.serialText)) return "the serial number text must be at most 13 printable ASCII characters";
	if (!id.serialText.empty() && !id.serialNumber) return "the serial number text needs a serial number";
	if (id.week > 54 && id.week != 255) return "the week of manufacture must be 0-54, or 255 for a model year";
	if (id.year < 1990 || id.year > 2245) return "the year must be 1990-2245";
	return std::nullopt;
}

std::optional<std::string> with_identity(const std::string &edid, const Identity &id, std::string *error)
{
	auto fail = [&](std::string e) -> std::optional<std::string> {
		if (error) *error = std::move(e);
		return std::nullopt;
	};
	if (auto e = validate(id)) return fail(*e);
	if (edid.size() < 128 || edid.compare(0, 8, edid_header) != 0) return fail("the monitor profile is not an EDID");
	std::string copy = edid;
	auto *out = reinterpret_cast<unsigned char *>(copy.data());
	auto m = static_cast<std::uint16_t>((id.manufacturer[0] - '@') << 10 | (id.manufacturer[1] - '@') << 5 | (id.manufacturer[2] - '@'));
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
		if (out[pos] || out[pos + 1] || out[pos + 2]) continue;  // a detailed timing
		if (out[pos + 3] == 0xfc) {
			text_descriptor(out + pos + 5, 13, id.name);
			named = true;
		} else if (out[pos + 3] == 0xff) {
			text_descriptor(out + pos + 5, 13, text);
		}
	}
	if (!named) return fail("the monitor profile has no monitor name descriptor");
	out[127] = checksum_byte(out);
	return copy;
}

bool edid_valid(const std::string &edid)
{
	if (edid.size() < 128 || edid.size() % 128 != 0 || edid.compare(0, 8, edid_header) != 0) return false;
	auto *e = reinterpret_cast<const unsigned char *>(edid.data());
	std::size_t blocks = static_cast<std::size_t>(e[126]) + 1;
	if (blocks * 128 > edid.size()) return false;
	for (std::size_t b = 0; b < blocks; b++) {
		unsigned char sum = 0;
		for (std::size_t i = 0; i < 128; i++) sum = static_cast<unsigned char>(sum + e[b * 128 + i]);
		if (sum != 0) return false;
	}
	return true;
}

bool declares_rgb_range(const std::string &edid)
{
	auto *e = reinterpret_cast<const unsigned char *>(edid.data());
	if (edid.size() < 128) return false;
	for (std::size_t block = 1; block <= e[126] && (block + 1) * 128 <= edid.size(); block++) {
		const unsigned char *x = e + block * 128;
		int end = x[2];
		if (x[0] != 2 || end < 4 || end > 127) continue;
		for (int i = 4; i < end;) {
			int tag = x[i] >> 5, length = x[i] & 31;
			if (i + 1 + length > end) break;
			if (tag == 7 && length >= 2 && x[i + 1] == 0 && (x[i + 2] & 0x40)) return true;
			i += 1 + length;
		}
	}
	return false;
}

std::optional<std::string> without_rgb_range(const std::string &edid)
{
	auto *e = reinterpret_cast<const unsigned char *>(edid.data());
	if (edid.size() != 256 || !edid_valid(edid) || e[126] != 1 || e[128] != 2) return std::nullopt;
	int end = e[130];
	if (end < 4 || end > 127) return std::nullopt;
	for (int pos = 4; pos < end;) {
		unsigned char header = e[128 + pos];
		int length = header & 31;
		if (pos + 1 + length > end) return std::nullopt;
		if (header >> 5 == 7 && length == 2 && e[128 + pos + 1] == 0 && e[128 + pos + 2] == 0x4A) {
			std::string old = edid;
			auto *ext = reinterpret_cast<unsigned char *>(old.data()) + 128;
			std::copy(ext + pos + 3, ext + 127, ext + pos);
			ext[124] = ext[125] = ext[126] = 0;
			ext[2] = static_cast<unsigned char>(end - 3);
			ext[127] = checksum_byte(ext);
			return old;
		}
		pos += 1 + length;
	}
	return std::nullopt;
}

std::optional<std::string> beyond_cube(const std::string &edid)
{
	if (edid.size() < 128) return std::nullopt;
	auto *e = reinterpret_cast<const unsigned char *>(edid.data());
	auto too_fast = [](int h, int v, double hz) { return h > 1920 || v > 1080 || static_cast<double>(h * v) * hz > cube_max_pixel_rate; };
	auto mode = [](int h, int v, double hz) { return std::format("{}x{} at {} Hz", h, v, hz_text(hz)); };
	auto detailed = [&](const unsigned char *d) -> std::optional<std::string> {
		int clock = d[0] | d[1] << 8;  // 10 kHz units; 0 marks a descriptor
		if (clock == 0) return std::nullopt;
		int h = d[2] | (d[4] & 0xf0) << 4, hb = d[3] | (d[4] & 0x0f) << 8;
		int v = d[5] | (d[7] & 0xf0) << 4, vb = d[6] | (d[7] & 0x0f) << 8;
		if (h + hb == 0 || v + vb == 0) return std::nullopt;
		double hz = static_cast<double>(clock) * 10000 / static_cast<double>((h + hb) * (v + vb));
		if (too_fast(h, v, hz)) return mode(h, v, hz);
		return std::nullopt;
	};
	for (int offset : {54, 72, 90, 108})
		if (auto m = detailed(e + offset)) return m;
	for (int offset = 38; offset < 54; offset += 2) {
		int b1 = e[offset], b2 = e[offset + 1];
		if ((b1 == 1 && b2 == 1) || (b1 == 0 && b2 == 0)) continue;
		int h = (b1 + 31) * 8;
		int ratios[4] = {h * 10 / 16, h * 3 / 4, h * 4 / 5, h * 9 / 16};
		int v = ratios[b2 >> 6];
		double hz = (b2 & 0x3f) + 60;
		if (too_fast(h, v, hz)) return mode(h, v, hz);
	}
	for (std::size_t block = 1; block <= e[126] && (block + 1) * 128 <= edid.size(); block++) {
		const unsigned char *x = e + block * 128;
		if (x[0] != 0x02) continue;
		int end = x[2];
		if (end < 4 || end > 127) end = 4;
		for (int i = 4; i < end;) {
			int tag = x[i] >> 5, length = x[i] & 0x1f;
			if (i + 1 + length > end) break;
			const unsigned char *body = x + i + 1;
			if (tag == 2) {
				for (int k = 0; k < length; k++) {
					int svd = body[k], vic = svd;
					if ((svd & 0x80) && (svd & 0x7f) >= 1 && (svd & 0x7f) <= 64) vic = svd & 0x7f;
					if (cube_unsafe_vics().count(vic)) return std::format("CTA VIC {}", vic);
				}
			} else if (tag == 3 && length >= 8 && body[0] == 0x03 && body[1] == 0x0c && body[2] == 0x00) {
				int flags = body[7], j = 8;
				if (flags & 0x80) j += 2;
				if (flags & 0x40) j += 2;
				if ((flags & 0x20) && j + 1 < length && body[j + 1] >> 5 != 0) return std::string("HDMI 4K VIC");
			} else if (tag == 7 && length > 1 && body[0] == 14) {
				return std::string("YCbCr 4:2:0 4K mode");
			}
			i += 1 + length;
		}
		for (int offset = x[2]; offset >= 4 && offset + 18 <= 127; offset += 18)
			if (auto m = detailed(x + offset)) return m;
	}
	return std::nullopt;
}

std::string sha256_hex(const std::string &data)
{
	unsigned char sum[32];
	EVP_Digest(data.data(), data.size(), sum, nullptr, EVP_sha256(), nullptr);
	std::string hex;
	for (unsigned char b : sum) hex += std::format("{:02x}", b);
	return hex;
}

} // namespace nkos::video::edid
