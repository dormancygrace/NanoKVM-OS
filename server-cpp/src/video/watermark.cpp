#include "video/watermark.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "common/image.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "system/failsafe.hpp"

#include <glaze/glaze.hpp>
#include <kvm_vision.h>

#include <atomic>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <format>
#include <fstream>
#include <iterator>
#include <mutex>
#include <sys/stat.h>
#include <unistd.h>
#include <vector>

namespace nkos::video::watermark {

namespace {

constexpr int ref_width = 1920;
constexpr int max_blocks = 8;
constexpr int max_width = 960, max_height = 270, max_area = max_width * max_height;
constexpr std::size_t max_bytes = 256 << 10, max_total_bytes = 360 << 10, max_file = 512 << 10;
constexpr std::size_t max_request = max_total_bytes / 3 * 4 + (64 << 10);
constexpr int max_margin = 200;
constexpr std::size_t max_design = 8192;
const char *const file = "/etc/kvm/watermark.json";
const char *const rgn_device = "/dev/cvi-rgn";

std::mutex mutex;  // the file and the native watermark
std::atomic<bool> unapplied_flag{false};
std::atomic<std::uint64_t> change_count{0};

struct Block {
	int x = 0;
	int y = 0;
};
struct Settings {
	bool enabled = false;
	int margin = 24;
	std::string design;
	std::vector<Block> blocks;
};
// The file: the settings and each block's PNG (base64, as Go writes []byte).
struct File {
	bool enabled = false;
	int margin = 24;
	std::string design;
	std::optional<std::vector<Block>> blocks;
	std::optional<std::vector<std::string>> pictures;
};
constexpr glz::opts lenient{.error_on_unknown_keys = false};

std::optional<std::string> validate(const Settings &s)
{
	if (s.margin < 0 || s.margin > max_margin) return std::format("margin must be 0-{}", max_margin);
	if (s.design.size() > max_design) return std::format("design must be at most {} bytes", max_design);
	if (s.blocks.size() > max_blocks) return std::format("at most {} blocks", max_blocks);
	for (auto &b : s.blocks)
		if (b.x < 0 || b.x > 1000 || b.y < 0 || b.y > 1000) return std::string("block places must be 0-1000");
	return std::nullopt;
}

// The saved settings and each block's picture (empty where missing). mutex is held.
std::pair<Settings, std::vector<std::string>> load_locked()
{
	Settings defaults;
	std::ifstream f(file, std::ios::binary);
	if (!f) return {defaults, {}};
	std::string raw((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
	if (raw.size() > max_file) return {defaults, {}};
	File saved;
	if (glz::read<lenient>(saved, raw)) return {defaults, {}};
	Settings s{saved.enabled, saved.margin, saved.design, saved.blocks.value_or(std::vector<Block>{})};
	if (validate(s)) return {defaults, {}};
	std::vector<std::string> pictures(s.blocks.size());
	if (saved.pictures) {
		for (std::size_t i = 0; i < pictures.size() && i < saved.pictures->size(); i++) {
			auto decoded = base64::decode((*saved.pictures)[i]);
			if (!decoded) return {defaults, {}};  // json.Unmarshal fails on bad base64
			pictures[i] = std::move(*decoded);
		}
	}
	return {s, pictures};
}

// A block's PNG checked and decoded; error set otherwise.
std::optional<image::Rgba> decode_block(const std::string &picture, std::string &error)
{
	if (picture.empty() || picture.size() > max_bytes) {
		error = std::format("a block must be a PNG of at most {} KiB", max_bytes >> 10);
		return std::nullopt;
	}
	auto size = image::sniff(picture) == image::Format::png ? image::size(picture) : std::nullopt;
	if (!size) {
		error = "a block must be a PNG";
		return std::nullopt;
	}
	if (size->width < 2 || size->height < 2 || size->width > max_width || size->height > max_height) {
		error = std::format("a block must be 2x2 to {}x{} pixels", max_width, max_height);
		return std::nullopt;
	}
	auto decoded = image::decode(picture, max_width, max_height);
	if (!decoded) error = "a block must be a PNG";
	return decoded;
}

bool write_synced(const std::string &path, const std::string &data)
{
	std::string tmp = path + ".new";
	int fd = ::open(tmp.c_str(), O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0644);
	if (fd < 0) return false;
	bool ok = true;
	for (std::size_t off = 0; ok && off < data.size();) {
		ssize_t n = ::write(fd, data.data() + off, data.size() - off);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) ok = false;
		else off += static_cast<std::size_t>(n);
	}
	ok = ok && ::fsync(fd) == 0;
	ok = ::close(fd) == 0 && ok;
	ok = ok && ::rename(tmp.c_str(), path.c_str()) == 0;
	if (!ok) {
		::unlink(tmp.c_str());
		return false;
	}
	std::string dir = path.substr(0, path.rfind('/'));
	int d = ::open(dir.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
	if (d >= 0) {
		::fsync(d);
		::close(d);
	}
	return true;
}

// Saves the settings and pictures: pictures[i] empty keeps block i's saved picture, which must exist. Error text, or
// nullopt. The file is replaced in one rename.
std::optional<std::string> save(const Settings &s, std::vector<std::optional<std::string>> pictures)
{
	if (auto e = validate(s)) return e;
	if (pictures.size() != s.blocks.size()) return std::string("one picture entry per block");
	std::lock_guard lock(mutex);
	auto [_, saved] = load_locked();
	std::vector<std::string> complete(pictures.size());
	long area = 0;
	std::size_t total = 0;
	for (std::size_t i = 0; i < pictures.size(); i++) {
		std::string picture;
		if (!pictures[i]) {
			if (i >= saved.size() || saved[i].empty()) return std::format("block {} needs a picture", i + 1);
			picture = saved[i];
		} else {
			picture = std::move(*pictures[i]);
		}
		std::string error;
		auto decoded = decode_block(picture, error);
		if (!decoded) return std::format("block {}: {}", i + 1, error);
		area += static_cast<long>(decoded->width) * decoded->height;
		total += picture.size();
		complete[i] = std::move(picture);
	}
	if (area > max_area) return std::format("the blocks together must be at most {} pixels (now {})", max_area, area);
	if (total > max_total_bytes) return std::format("the blocks together must be at most {} KiB of PNG", max_total_bytes >> 10);
	// As Go's json.Marshal of watermarkFile: the settings' fields, then the pictures.
	std::string raw = std::format("{{\"enabled\":{},\"margin\":{},\"design\":{},\"blocks\":[", s.enabled ? "true" : "false",
	                              s.margin, gojson::quote(s.design));
	for (std::size_t i = 0; i < s.blocks.size(); i++)
		raw += std::format("{}{{\"x\":{},\"y\":{}}}", i ? "," : "", s.blocks[i].x, s.blocks[i].y);
	raw += "],\"pictures\":[";
	for (std::size_t i = 0; i < complete.size(); i++) raw += (i ? ",\"" : "\"") + base64::encode(complete[i]) + "\"";
	raw += "]}\n";
	if (raw.size() > max_file) return std::format("the watermark must be at most {} KiB", max_file >> 10);
	if (!write_synced(file, raw)) return std::string(std::strerror(errno));
	return std::nullopt;
}

// Loads the region engine's driver, kept out at boot.
std::optional<std::string> load_region_driver()
{
	struct stat st{};
	if (::stat(rgn_device, &st) == 0) return std::nullopt;
	process::Options options;
	options.timeout = std::chrono::seconds(10);
	auto r = process::run({"modprobe", "cv181x_rgn"}, options);
	if (!r.ok()) return "cannot load the region driver: " + (r.started ? "exit status " + std::to_string(r.exit_code) : r.error);
	return std::nullopt;
}

// Gives the saved watermark to the native library. Error text, or nullopt.
std::optional<std::string> apply()
{
	std::lock_guard lock(mutex);
	// Until this succeeds the library may have another watermark than the saved one, or none.
	unapplied_flag = true;
	auto [s, pictures] = load_locked();
	if (!s.enabled || s.blocks.empty()) {
		kvmv_set_watermark(nullptr, nullptr, 0, 0, 0);
		unapplied_flag = false;
		return std::nullopt;
	}
	std::vector<std::uint32_t> argb;
	std::vector<int> geometry;
	for (std::size_t i = 0; i < pictures.size(); i++) {
		std::string error;
		auto picture = decode_block(pictures[i], error);
		if (!picture) {
			kvmv_set_watermark(nullptr, nullptr, 0, 0, 0);
			return std::format("block {}: {}", i + 1, error);
		}
		// Straight (not premultiplied) alpha, as Go's NRGBA, packed 0xAARRGGBB.
		const auto &p = picture->pixels;
		for (std::size_t j = 0; j < p.size(); j += 4)
			argb.push_back((std::uint32_t(p[j + 3]) << 24) | (std::uint32_t(p[j]) << 16) | (std::uint32_t(p[j + 1]) << 8) | p[j + 2]);
		geometry.insert(geometry.end(), {picture->width, picture->height, s.blocks[i].x, s.blocks[i].y});
	}
	if (auto e = load_region_driver()) return e;
	int shown = kvmv_set_watermark(argb.data(), geometry.data(), static_cast<int>(geometry.size() / 4), ref_width, s.margin);
	// -EIO: the library has it, and a block is missing on an output of a stream; a single picture puts the blocks
	// on for itself and is refused when one fails, so it need not be withheld for that.
	if (shown >= 0 || shown == -EIO) unapplied_flag = false;
	if (shown < 0) return std::format("the native library refused the watermark ({})", shown);
	log::info("watermark on: {} blocks, margin {}", s.blocks.size(), s.margin);
	return std::nullopt;
}

std::string response_json()
{
	Settings s;
	std::vector<std::string> pictures;
	{
		std::lock_guard lock(mutex);
		std::tie(s, pictures) = load_locked();
	}
	std::string blocks;
	for (std::size_t i = 0; i < s.blocks.size(); i++)
		blocks += std::format("{}{{\"x\":{},\"y\":{},\"image\":\"{}\"}}", i ? "," : "", s.blocks[i].x, s.blocks[i].y,
		                      base64::encode(pictures[i]));
	return std::format("{{\"enabled\":{},\"margin\":{},\"design\":{},\"blocks\":[{}],\"refWidth\":{},\"maxWidth\":{},"
	                   "\"maxHeight\":{},\"maxArea\":{},\"maxBlocks\":{},\"maxBytes\":{},\"missing\":{}}}",
	                   s.enabled ? "true" : "false", s.margin, gojson::quote(s.design), blocks, ref_width, max_width,
	                   max_height, max_area, max_blocks, max_bytes, kvmv_watermark_missing());
}

struct BlockReq {
	int x = 0;
	int y = 0;
	std::optional<std::string> image;
};
struct Req {
	bool enabled = false;
	int margin = 0;
	std::string design;
	std::optional<std::vector<BlockReq>> blocks;
};

} // namespace

void apply_at_start()
{
	if (auto e = apply()) log::error("watermark not applied: {}", *e);
}

bool unapplied() { return unapplied_flag; }
std::uint64_t changes() { return change_count; }

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/vm/video/watermark", auth::guarded([](http::Request &req, http::Response &res) {
		// The native count is asked on the device thread, in order with the capture operations.
		res.pending = true;
		loop::device([id = req.id] {
			http::Response out;
			api::send(out, 0, "success", response_json());
			http::complete(id, std::move(out));
		});
	}, admin));
	router.add("POST", "/api/vm/video/watermark", auth::guarded([](http::Request &req, http::Response &res) {
		if (!system::failsafe::require_video(res)) return;
		if (req.body.size() > max_request) return api::err(res, -1, "invalid arguments");
		Req r;
		if (glz::read<lenient>(r, req.body)) return api::err(res, -1, "invalid arguments");
		Settings s{r.enabled, r.margin, r.design, {}};
		std::vector<std::optional<std::string>> pictures;
		for (auto &b : r.blocks.value_or(std::vector<BlockReq>{})) {
			s.blocks.push_back(Block{b.x, b.y});
			if (b.image) {
				auto decoded = base64::decode(*b.image);
				if (!decoded) return api::err(res, -1, "block pictures must be base64");
				pictures.push_back(std::move(*decoded));
			} else {
				pictures.push_back(std::nullopt);
			}
		}
		res.pending = true;
		loop::device([id = req.id, s = std::move(s), pictures = std::move(pictures)]() mutable {
			http::Response out;
			if (auto e = save(s, std::move(pictures))) {
				api::err(out, -2, *e);
				return http::complete(id, std::move(out));
			}
			// The preview's picture with the old watermark is not shown again: neither the cached one nor one
			// being taken while the new one goes on.
			change_count++;
			auto e = apply();
			change_count++;
			if (e) {
				log::error("watermark not applied: {}", *e);
				api::err(out, -3, "saved, but not applied: " + *e);
			} else {
				api::send(out, 0, "success", response_json());
			}
			http::complete(id, std::move(out));
		});
	}, admin));
}

} // namespace nkos::video::watermark
