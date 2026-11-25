#include "rustdesk/bridge_wire.hpp"

#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"

#include <algorithm>

namespace nkos::rustdesk::wire {

namespace goj = usb::goj;

namespace {

void put16(std::string &b, std::size_t at, std::uint16_t v)
{
	b[at] = static_cast<char>(v >> 8);
	b[at + 1] = static_cast<char>(v);
}
void put32(std::string &b, std::size_t at, std::uint32_t v)
{
	for (int i = 0; i < 4; i++) b[at + i] = static_cast<char>(v >> (24 - 8 * i));
}
void put64(std::string &b, std::size_t at, std::uint64_t v)
{
	for (int i = 0; i < 8; i++) b[at + i] = static_cast<char>(v >> (56 - 8 * i));
}

std::string media_base()
{
	std::string h(media_header_size, '\0');
	h.replace(0, 4, "OKVF");
	h[4] = 1;
	return h;
}

// strings.EqualFold against an ASCII lower-case name: ASCII case, and the Kelvin sign and the long s, which fold
// into k and s.
bool fold_equal(std::string_view key, std::string_view name)
{
	std::size_t i = 0;
	for (char n : name) {
		if (i >= key.size()) return false;
		unsigned char c = static_cast<unsigned char>(key[i]);
		if (n == 'k' && key.substr(i, 3) == "\xe2\x84\xaa") {
			i += 3;
			continue;
		}
		if (n == 's' && key.substr(i, 2) == "\xc5\xbf") {
			i += 2;
			continue;
		}
		if (std::tolower(c) != n || c >= 0x80) return false;
		i++;
	}
	return i == key.size();
}

bool json_space(char c) { return c == ' ' || c == '\t' || c == '\r' || c == '\n'; }

} // namespace

std::string frame_header(int native_codec, bool key, std::uint16_t width, std::uint16_t height, std::uint64_t sequence,
                         std::int64_t timestamp_us, std::int64_t duration_us, std::uint32_t length)
{
	std::string h = media_base();
	h[5] = static_cast<char>(native_codec);
	if (key) h[6] = 1;
	put64(h, 8, sequence);
	put64(h, 16, static_cast<std::uint64_t>(timestamp_us));
	put32(h, 24, static_cast<std::uint32_t>(duration_us));
	put16(h, 28, width);
	put16(h, 30, height);
	put32(h, 32, length);
	return h;
}

std::string media_error(std::string_view message)
{
	message = message.substr(0, max_media_error);
	std::string h = media_base();
	put32(h, 32, static_cast<std::uint32_t>(message.size()));
	h += message;
	return h;
}

bool parse_media_request(std::string_view line, MediaRequest &out)
{
	MediaRequest r;
	std::string error;
	bool ok = goj::unmarshal(line, "", [&r](goj::Reader &v) {
		v.object("", "", {{"Version", [&r](goj::Reader &x) { x.integer(r.version); }},
		                  {"Video", [&r](goj::Reader &x) { x.string(r.video); }},
		                  {"Codec", [&r](goj::Reader &x) { x.string(r.codec, "stream.VideoCodec"); }}});
	}, error);
	if (!ok || r.version != 1 || (r.video != "info" && r.video != "encoded") ||
	    (!r.codec.empty() && r.codec != "auto" && r.codec != "h264" && r.codec != "h265"))
		return false;
	out = r;
	return true;
}

MediaCommand parse_media_command(std::string_view line, long long &fps)
{
	if (line == "{\"request\":\"keyframe\"}\n") return MediaCommand::keyframe;
	std::string request;
	long long value = 0;
	std::string error;
	bool ok = goj::unmarshal(line, "", [&](goj::Reader &v) {
		v.object("", "", {{"Request", [&request](goj::Reader &x) { x.string(request); }},
		                  {"FPS", [&value](goj::Reader &x) { x.integer(value); }}});
	}, error);
	if (ok && request == "fps" && value > 0) {
		fps = value;
		return MediaCommand::fps;
	}
	return MediaCommand::none;
}

std::uint16_t parse_dimension(std::string_view text)
{
	std::string s = usb::gotext::trim_space(text);
	if (s.empty()) return 0;
	unsigned long v = 0;
	for (char c : s) {
		if (c < '0' || c > '9') return 0;
		v = v * 10 + static_cast<unsigned>(c - '0');
		if (v > 0xffff) return 0;
	}
	return static_cast<std::uint16_t>(v);
}

std::string audio_header(std::uint8_t codec, std::size_t length)
{
	std::string h(16, '\0');
	h.replace(0, 4, "OKAF");
	h[4] = 1;
	h[5] = static_cast<char>(codec);
	if (codec == 1) {
		h[6] = 2;
		put32(h, 8, 48000);
	}
	put16(h, 12, static_cast<std::uint16_t>(length));
	return h;
}

bool parse_audio_request(std::string_view line, std::string &audio)
{
	long long version = 0;
	std::string value;
	bool unknown = false;
	std::size_t end = 0;
	std::string error;
	bool ok = goj::decode(line, "", [&](goj::Reader &v) {
		v.object("", "", {{"version", [&version](goj::Reader &x) { x.integer(version); }},
		                  {"audio", [&value](goj::Reader &x) { x.string(value); }}});
	}, end, error);
	if (!ok) return false;
	// DisallowUnknownFields: a name that matches neither field.
	std::size_t end2 = 0;
	(void)goj::decode(line, "", [&unknown](goj::Reader &v) {
		v.map("map[string]any", [] {}, [&unknown](const std::string &key, goj::Reader &x) {
			if (!fold_equal(key, "version") && !fold_equal(key, "audio")) unknown = true;
			x.skip();
		});
	}, end2, error);
	if (unknown || version != 1 || (value != "info" && value != "opus")) return false;
	// The next Decode must find the end of the input.
	for (std::size_t i = end; i < line.size(); i++)
		if (!json_space(line[i])) return false;
	audio = value;
	return true;
}

bool report_held(Kind kind, const std::vector<std::uint8_t> &report)
{
	if (report[0] != 0) return true;
	if (kind == Kind::keyboard)
		for (std::size_t i = 2; i < report.size(); i++)
			if (report[i]) return true;
	return false;
}

std::vector<std::uint8_t> release_report(Kind kind, const std::vector<std::uint8_t> &previous)
{
	std::vector<std::uint8_t> r(previous.size(), 0);
	if (kind == Kind::absolute && previous.size() >= 5) std::copy(previous.begin() + 1, previous.begin() + 5, r.begin() + 1);
	return r;
}

std::vector<std::vector<std::uint8_t>> RelativePointer::reports(std::uint8_t buttons, std::int32_t nx, std::int32_t ny,
                                                                std::int8_t wheel)
{
	std::int64_t dx = 0, dy = 0;
	if (known) {
		std::int64_t mx = static_cast<std::int64_t>(nx - x) * width + x_remainder;
		std::int64_t my = static_cast<std::int64_t>(ny - y) * height + y_remainder;
		dx = mx / 32767;
		dy = my / 32767;
		x_remainder = mx % 32767;
		y_remainder = my % 32767;
	}
	x = nx;
	y = ny;
	known = true;
	auto clamp = [](std::int64_t v) { return std::clamp<std::int64_t>(v, -127, 127); };
	std::vector<std::vector<std::uint8_t>> out;
	for (;;) {
		std::int64_t xs = clamp(dx), ys = clamp(dy);
		out.push_back({buttons, static_cast<std::uint8_t>(static_cast<std::int8_t>(xs)),
		               static_cast<std::uint8_t>(static_cast<std::int8_t>(ys)), static_cast<std::uint8_t>(wheel), 0});
		dx -= xs;
		dy -= ys;
		wheel = 0;
		if (dx == 0 && dy == 0) return out;
	}
}

bool record_reports(const std::uint8_t *record, RelativePointer *pointer, Kind &kind,
                    std::vector<std::vector<std::uint8_t>> &reports)
{
	reports.clear();
	switch (record[0]) {
	case record_keyboard: {
		std::vector<std::uint8_t> r(8, 0);
		r[0] = record[1];
		std::copy(record + 2, record + 8, r.begin() + 2);
		kind = Kind::keyboard;
		reports.push_back(std::move(r));
		return true;
	}
	case record_absolute: {
		std::int32_t x = record[2] | record[3] << 8, y = record[4] | record[5] << 8;
		if (x > 32767 || y > 32767) return false;
		if (pointer) {
			kind = Kind::relative;
			reports = pointer->reports(record[1], x, y, static_cast<std::int8_t>(record[6]));
			return true;
		}
		// The record's payload is the USB report up to its padding byte.
		std::vector<std::uint8_t> r(record + 1, record + 7);
		r.push_back(0);
		kind = Kind::absolute;
		reports.push_back(std::move(r));
		return true;
	}
	default:
		if (record[2] == 0x80 || record[3] == 0x80) return false;
		kind = Kind::relative;
		reports.push_back({record[1], record[2], record[3], record[4], 0});
		return true;
	}
}

bool parse_rtc_command(std::string_view json, RtcCommand &out)
{
	RtcCommand c;
	std::string error;
	bool ok = goj::unmarshal(json, "", [&c](goj::Reader &v) {
		v.object("", "", {{"op", [&c](goj::Reader &x) { x.string(c.op); }},
		                  {"offer", [&c](goj::Reader &x) { x.string(c.offer); }},
		                  {"token", [&c](goj::Reader &x) { x.string(c.token); }},
		                  {"candidate", [&c](goj::Reader &x) { x.string(c.candidate); }}});
	}, error);
	out = c;
	return ok;
}

std::string rtc_event_json(const RtcEvent &e)
{
	std::string out = "{\"event\":" + gojson::quote(e.event);
	auto add = [&out](const char *name, const std::string &v) {
		if (!v.empty()) out += std::string(",\"") + name + "\":" + gojson::quote(v);
	};
	add("answer", e.answer);
	add("token", e.token);
	add("fingerprint", e.fingerprint);
	add("session_key", e.session_key);
	add("candidate", e.candidate);
	add("error", e.error);
	return out + "}";
}

std::string length_prefix(std::size_t n)
{
	std::string p(4, '\0');
	for (int i = 0; i < 4; i++) p[i] = static_cast<char>(n >> (8 * i));
	return p;
}

bool decode_offer(std::string_view endpoint, std::string &sdp, std::string &error)
{
	constexpr std::string_view prefix = "webrtc://";
	if (endpoint.size() > rtc_max_signal || !endpoint.starts_with(prefix)) {
		error = "invalid WebRTC endpoint";
		return false;
	}
	auto payload = base64::decode(endpoint.substr(prefix.size()));
	if (!payload) {
		error = "invalid WebRTC endpoint";
		return false;
	}
	// webrtc.SessionDescription: its type unmarshals case-insensitively and refuses an unknown one (null too).
	bool type_error = false, offer = false;
	std::string text, json_error;
	bool ok = goj::unmarshal(*payload, "", [&](goj::Reader &v) {
		v.object("", "", {{"type", [&](goj::Reader &x) {
			                   if (x.is_null()) {
				                   x.skip();
				                   type_error = true;
				                   return;
			                   }
			                   std::string t;
			                   x.string(t);
			                   std::transform(t.begin(), t.end(), t.begin(), [](unsigned char c) { return std::tolower(c); });
			                   if (t != "offer" && t != "pranswer" && t != "answer" && t != "rollback") type_error = true;
			                   offer = t == "offer";
		                   }},
		                  {"sdp", [&text](goj::Reader &x) { x.string(text); }}});
	}, json_error);
	if (!ok || type_error || !offer) {
		error = "invalid SDP offer";
		return false;
	}
	sdp = text;
	return true;
}

bool rtc_fingerprint(std::string_view sdp, std::string &out, std::string &error)
{
	constexpr std::string_view prefix = "a=fingerprint:sha-256 ";
	std::string found;
	std::size_t start = 0;
	for (;;) {
		std::size_t nl = sdp.find('\n', start);
		std::string_view line = sdp.substr(start, nl == std::string_view::npos ? std::string_view::npos : nl - start);
		if (line.starts_with(prefix)) {
			std::string raw = usb::gotext::trim_space(line.substr(prefix.size()));
			std::transform(raw.begin(), raw.end(), raw.begin(), [](unsigned char c) { return std::toupper(c); });
			std::string joined;
			std::size_t parts = 0;
			bool valid = true;
			for (std::size_t at = 0;;) {
				std::size_t colon = raw.find(':', at);
				std::string part = raw.substr(at, colon == std::string::npos ? std::string::npos : colon - at);
				parts++;
				if (part.size() != 2) valid = false;
				joined += part;
				if (colon == std::string::npos) break;
				at = colon + 1;
			}
			if (parts != 32 || !valid) {
				error = "invalid DTLS fingerprint";
				return false;
			}
			for (char c : joined)
				if (!std::isxdigit(static_cast<unsigned char>(c))) {
					error = "invalid DTLS fingerprint";
					return false;
				}
			std::string value = "sha-256 " + raw;
			if (!found.empty() && value != found) {
				error = "conflicting DTLS fingerprints";
				return false;
			}
			found = value;
		}
		if (nl == std::string_view::npos) break;
		start = nl + 1;
	}
	if (found.empty()) {
		error = "missing SHA-256 DTLS fingerprint";
		return false;
	}
	out = found;
	return true;
}

bool append_rtc_fragment(std::string &buffer, std::string_view fragment, std::string &payload, bool &complete)
{
	complete = false;
	if (fragment.empty() || fragment.size() > rtc_max_fragment + 1 || static_cast<unsigned char>(fragment[0]) > 1 ||
	    (fragment[0] == 1 && fragment.size() == 1) || buffer.size() + fragment.size() - 1 > rtc_max_inbound)
		return false;
	buffer.append(fragment.substr(1));
	if (fragment[0] == 1) return true;
	payload = std::move(buffer);
	buffer.clear();
	complete = true;
	return true;
}

std::string rustdesk_frame_header(std::size_t length)
{
	std::size_t width = 1;
	while (width < 4 && length >= (std::size_t(1) << (8 * width - 2))) width++;
	std::uint32_t header = static_cast<std::uint32_t>(length) << 2 | static_cast<std::uint32_t>(width - 1);
	std::string out(width, '\0');
	for (std::size_t i = 0; i < width; i++) out[i] = static_cast<char>(header >> (8 * i));
	return out;
}

bool rustdesk_frame_length(const std::uint8_t *header, std::size_t width, std::size_t &length)
{
	std::uint32_t v = 0;
	for (std::size_t i = 0; i < width; i++) v |= static_cast<std::uint32_t>(header[i]) << (8 * i);
	length = v >> 2;
	return length <= rtc_max_message;
}

} // namespace nkos::rustdesk::wire
