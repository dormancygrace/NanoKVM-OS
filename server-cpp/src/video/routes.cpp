#include "video/routes.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "video/direct.hpp"
#include "video/hdmi.hpp"
#include "video/mjpeg.hpp"
#include "video/rtsp.hpp"
#include "video/settings.hpp"
#include "video/screen.hpp"
#include "video/source.hpp"

namespace nkos::video {

namespace {

struct EncoderState {
	bool active = false;
	std::string codec;
	bool selected = false;
};

// The selected codec stays authoritative while old viewers disconnect.
void get_encoder_state(http::Request &, http::Response &res)
{
	EncoderState state;
	auto active = active_codec();
	auto selection = effective_selected_codec();
	state.active = active.has_value();
	state.selected = selection.has_value();
	if (selection) state.codec = codec_name(*selection);
	else if (active) state.codec = codec_name(*active);
	res.set_header("Cache-Control", "no-store");
	api::ok(res, state);
}

// Selecting a codec is an explicit shared-setting change, never a side effect of joining.
void set_encoder_state(http::Request &req, http::Response &res)
{
	if (!req.auth || req.auth->role != "admin") {
		res.status = 403;
		res.done = true;
		return;
	}
	bool ok = true;
	auto params = req.header("content-type").starts_with("application/json") ? api::Params::bind(req) : std::nullopt;
	std::optional<std::string> name;
	if (params) name = params->string("codec", ok);
	auto codec = name && ok ? parse_codec(*name) : std::nullopt;
	if (!codec) return api::err(res, -1, "codec must be h264 or h265");
	// The device codec must be one its viewers can subscribe to.
	if (*codec == Codec::h264) {
		auto [w, h] = capture_input_size();
		if (!h264_allowed_for(w, h, screen().height)) return api::err(res, -3, h264_blocked_message);
	}
	if (auto error = save_selected_codec(*codec)) {
		log::error("cannot save encoder selection: {}", *error);
		return api::err(res, -2, "cannot save encoder selection");
	}
	select_codec(*codec);
	api::ok(res);
}

} // namespace

void add_routes(http::Router &router)
{
	router.add("GET", "/api/stream/state", auth::guarded(get_encoder_state));
	router.add("POST", "/api/stream/state", auth::guarded(set_encoder_state));
	direct::add_routes(router);
	mjpeg::add_routes(router);
	rtsp::add_routes(router);
	hdmi::add_routes(router);
	add_settings_routes(router);
}

} // namespace nkos::video
