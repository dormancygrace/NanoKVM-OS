#include "input/region.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "config/config.hpp"

#include <glaze/glaze.hpp>

#include <format>
#include <fstream>
#include <set>
#include <sstream>

namespace nkos::input {

namespace {

constexpr const char *region_file = "/etc/kvm/input-region.json";

struct Resolution {
	int width = 0, height = 0;
	auto operator<=>(const Resolution &) const = default;
};
struct Manual {
	int frameWidth = 0, frameHeight = 0, left = 0, top = 0, width = 0, height = 0;
};
struct Region {
	std::string mode;
	int frameWidth = 0, frameHeight = 0, left = 0, top = 0, width = 0, height = 0;
	std::optional<std::vector<Resolution>> resolutions;
	std::string selectedResolution;
	std::optional<std::vector<Manual>> regions;
	std::string selectedRegion;
};
struct Request {
	std::string mode;
	std::optional<int> frameWidth, frameHeight, left, top, width, height;
	std::optional<std::vector<Resolution>> resolutions;
	std::optional<std::string> selectedResolution;
	std::optional<std::vector<Manual>> regions;
	std::optional<std::string> selectedRegion;
};

std::string resolution_key(const Resolution &r) { return std::format("{}x{}", r.width, r.height); }
std::string region_key(const Manual &r) { return std::format("{}x{}", r.width, r.height); }

bool valid_region(const std::string &mode, int fw, int fh, int left, int top, int w, int h)
{
	if (mode == "off" || mode == "auto") return true;
	if (mode == "manual" && fw == 0 && fh == 0) return true;
	if (mode != "manual") return false;
	if (fw <= 0 || fh <= 0) return false;
	if (left < 0 || top < 0 || w <= 0 || h <= 0) return false;
	return left <= fw - w && top <= fh - h;
}

bool valid_resolutions(const std::vector<Resolution> &rs)
{
	std::set<Resolution> seen;
	for (const auto &r : rs) {
		if (r.width <= 0 || r.height <= 0 || !seen.insert(r).second) return false;
	}
	return true;
}

bool valid_manual(const std::vector<Manual> &rs)
{
	std::set<std::string> seen;
	for (const auto &r : rs) {
		if (!valid_region("manual", r.frameWidth, r.frameHeight, r.left, r.top, r.width, r.height)) return false;
		if (!seen.insert(region_key(r)).second) return false;
	}
	return true;
}

bool valid_selections(const Region &r)
{
	if (!r.selectedResolution.empty() && !r.selectedRegion.empty()) return false;
	if (!r.selectedResolution.empty()) {
		bool found = false;
		for (const auto &res : r.resolutions.value_or(std::vector<Resolution>{})) found = found || resolution_key(res) == r.selectedResolution;
		if (!found) return false;
	}
	if (r.selectedRegion.empty()) return true;
	for (const auto &m : r.regions.value_or(std::vector<Manual>{}))
		if (region_key(m) == r.selectedRegion) return true;
	return false;
}

// The saved region; nullopt when there is none (a missing or invalid file reads as none).
std::optional<Region> read_region(bool &missing)
{
	missing = false;
	std::ifstream in(region_file);
	if (!in) {
		missing = true;
		return std::nullopt;
	}
	std::string data((std::istreambuf_iterator<char>(in)), {});
	Region r;
	if (glz::read<glz::opts{.error_on_unknown_keys = false}>(r, data) || data == "null") return std::nullopt;
	if (r.mode.empty()) r.mode = "manual";
	if (r.regions.value_or(std::vector<Manual>{}).empty() && r.frameWidth > 0 && r.frameHeight > 0) {
		Manual m{r.frameWidth, r.frameHeight, r.left, r.top, r.width, r.height};
		r.regions = std::vector<Manual>{m};
		if (r.selectedResolution.empty()) r.selectedRegion = region_key(m);
	}
	if (!valid_region(r.mode, r.frameWidth, r.frameHeight, r.left, r.top, r.width, r.height)) return std::nullopt;
	if (!valid_manual(r.regions.value_or(std::vector<Manual>{}))) return std::nullopt;
	if (!valid_selections(r)) return std::nullopt;
	return r;
}

std::string json(Region r)
{
	if (r.resolutions && r.resolutions->empty()) r.resolutions.reset();
	if (r.regions && r.regions->empty()) r.regions.reset();
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = true}>(r, out);
	return out;
}

void clear_legacy(Region &r) { r.frameWidth = r.frameHeight = r.left = r.top = r.width = r.height = 0; }

} // namespace

void add_region_routes(http::Router &router)
{
	router.add("GET", "/api/vm/input-region", auth::guarded([](http::Request &, http::Response &res) {
		bool missing = false;
		auto r = read_region(missing);
		if (!r) {
			Region off;
			off.mode = "off";
			return api::send(res, 0, "success", json(off));
		}
		api::send(res, 0, "success", json(*r));
	}));
	router.add("POST", "/api/vm/input-region", auth::guarded([](http::Request &req, http::Response &res) {
		Request q;
		if (req.body.empty() || glz::read<glz::opts{.error_on_unknown_keys = false}>(q, req.body))
			return api::err(res, -1, "invalid arguments");
		bool missing = false;
		auto apply_common = [&](Region &region) -> bool {
			if (q.resolutions) {
				if (!valid_resolutions(*q.resolutions)) {
					api::err(res, -1, "invalid original resolutions");
					return false;
				}
				region.resolutions = *q.resolutions;
			}
			if (q.selectedResolution) region.selectedResolution = *q.selectedResolution;
			if (q.regions) {
				if (!valid_manual(*q.regions)) {
					api::err(res, -1, "invalid manual regions");
					return false;
				}
				region.regions = *q.regions;
				if (q.regions->empty()) clear_legacy(region);
			}
			if (q.selectedRegion) region.selectedRegion = *q.selectedRegion;
			if (!valid_selections(region)) {
				api::err(res, -1, "invalid selected resolution");
				return false;
			}
			return true;
		};
		auto save = [&](const Region &region, const char *failure) {
			if (!config::atomic_write(region_file, json(region), 0644)) return api::err(res, -2, failure);
			api::ok(res);
		};
		bool geometry = q.frameWidth || q.frameHeight || q.left || q.top || q.width || q.height;
		if (q.mode == "off" || q.mode == "auto" || (q.mode == "manual" && !geometry)) {
			std::optional<Region> region = read_region(missing);
			Region r = region.value_or(Region{});
			r.mode = q.mode;
			if (!apply_common(r)) return;
			return save(r, "failed to save input region mode");
		}
		if (q.mode != "manual") return api::err(res, -1, "invalid input region mode");
		if (!q.frameWidth || !q.frameHeight || !q.left || !q.top || !q.width || !q.height ||
		    !valid_region("manual", *q.frameWidth, *q.frameHeight, *q.left, *q.top, *q.width, *q.height))
			return api::err(res, -1, "invalid input region");
		Region r;
		r.mode = "manual";
		r.frameWidth = *q.frameWidth, r.frameHeight = *q.frameHeight, r.left = *q.left, r.top = *q.top;
		r.width = *q.width, r.height = *q.height;
		if (auto previous = read_region(missing)) {
			r.resolutions = previous->resolutions;
			r.selectedResolution = previous->selectedResolution;
			r.regions = previous->regions;
			r.selectedRegion = previous->selectedRegion;
		}
		if (!apply_common(r)) return;
		save(r, "failed to save input region");
	}));
}

} // namespace nkos::input
