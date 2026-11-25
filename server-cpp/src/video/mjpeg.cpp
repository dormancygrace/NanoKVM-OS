#include "video/mjpeg.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "config/config.hpp"
#include "http/websocket.hpp"
#include "system/failsafe.hpp"
#include "video/capture_status.hpp"
#include "video/frame.hpp"
#include "video/hdmi.hpp"
#include "video/screen.hpp"
#include "video/source.hpp"
#include "video/videogate.hpp"
#include "video/vision.hpp"

#include <algorithm>
#include <chrono>
#include <cstring>
#include <ctime>
#include <deque>
#include <fstream>
#include <memory>
#include <optional>
#include <string>
#include <sys/stat.h>
#include <vector>

namespace nkos::video::mjpeg {

namespace {

using namespace std::chrono_literals;
using Clock = std::chrono::steady_clock;

constexpr const char *blocked_message = "MJPEG is unavailable while the HDMI input is 3840x2160: use H.264 or H.265";
constexpr int blocked_result = -8;
constexpr int unchanged_result = 5;
constexpr int tiles_result = 6;
constexpr int tiles_unsupported = -9;
constexpr int max_long_side = 2560;
constexpr auto refusal_hold = 500ms;
constexpr auto duplicate_refresh = 5s;
constexpr auto write_timeout = 5s;
constexpr int next_to_video_fps = 30;
constexpr std::uint16_t max_tiles = 24;
constexpr std::size_t tiled_queue = 8;
constexpr std::size_t tiles_header = 8, tiles_record = 12, whole_headroom = tiles_header + tiles_record;
constexpr std::uint8_t frame_detect_interval = 60;
constexpr const char *frame_detect_file = "/etc/kvm/frame_detect";
constexpr const char *ive_timeout_parameter = "/sys/module/cv181x_ive/parameters/timeout_ms";

const std::string part_header = "--frame\r\nContent-Type: image/jpeg\r\n\r\n";
// Chromium commits the preceding image when the next part headers arrive: they follow every picture at once.
const std::string next_part = "\r\n" + part_header;

struct Picture {
	std::shared_ptr<std::vector<unsigned char>> storage;
	std::size_t offset = 0, size = 0;
	const unsigned char *data() const { return storage->data() + offset; }
	bool empty() const { return !storage || size == 0; }
};

// SG2002 JPEGs start with SOI and an APP9 sequence counter that changes even when the picture does not: those
// bytes are left out of the comparison.
bool same_image(const Picture &a, const Picture &b)
{
	if (a.empty() || b.empty() || a.size != b.size) return false;
	static const unsigned char prefix[] = {0xff, 0xd8, 0xff, 0xe9, 0x00, 0x04};
	if (a.size > 8 && std::memcmp(a.data(), prefix, 6) == 0 && std::memcmp(b.data(), prefix, 6) == 0)
		return std::memcmp(a.data() + 8, b.data() + 8, a.size - 8) == 0;
	return std::memcmp(a.data(), b.data(), a.size) == 0;
}

std::uint16_t be16(const unsigned char *p) { return static_cast<std::uint16_t>(p[0] << 8 | p[1]); }
void put_le16(unsigned char *p, std::uint16_t v) { p[0] = v & 0xff, p[1] = v >> 8; }
void put_le32(unsigned char *p, std::uint32_t v)
{
	for (int i = 0; i < 4; i++) p[i] = static_cast<unsigned char>(v >> (8 * i));
}

// The picture size from a JPEG's start-of-frame segment.
bool jpeg_size(const unsigned char *jpeg, std::size_t size, int &width, int &height)
{
	if (size < 4 || jpeg[0] != 0xff || jpeg[1] != 0xd8) return false;
	for (std::size_t at = 2; at + 9 <= size;) {
		if (jpeg[at] != 0xff) return false;
		unsigned char marker = jpeg[at + 1];
		if (marker == 0xff) {
			at++;
			continue;
		}
		int length = be16(jpeg + at + 2);
		if (marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc) {
			height = be16(jpeg + at + 5);
			width = be16(jpeg + at + 7);
			return width > 0 && height > 0;
		}
		if (marker == 0xda || length < 2) return false;
		at += 2 + static_cast<std::size_t>(length);
	}
	return false;
}

// A whole-picture tile message of a JPEG: its header written into the headroom before the picture when there is
// room (no copy), otherwise into a copy.
std::optional<http::Message> whole_message(const Picture &p)
{
	int width = 0, height = 0;
	if (!jpeg_size(p.data(), p.size, width, height)) return std::nullopt;
	http::Message m;
	if (p.offset >= whole_headroom + http::websocket_headroom) {
		m.storage = p.storage;
		m.offset = p.offset - whole_headroom;
	} else {
		m.storage = std::make_shared<std::vector<unsigned char>>(http::websocket_headroom + whole_headroom + p.size);
		m.offset = http::websocket_headroom;
		std::memcpy(m.storage->data() + m.offset + whole_headroom, p.data(), p.size);
	}
	m.size = whole_headroom + p.size;
	unsigned char *h = m.storage->data() + m.offset;
	std::memset(h, 0, whole_headroom);
	h[0] = 1;
	h[1] = 1;  // the whole picture
	put_le16(h + 2, 1);
	put_le16(h + 4, static_cast<std::uint16_t>(width));
	put_le16(h + 6, static_cast<std::uint16_t>(height));
	put_le16(h + tiles_header + 4, static_cast<std::uint16_t>(width));
	put_le16(h + tiles_header + 6, static_cast<std::uint16_t>(height));
	put_le32(h + tiles_header + 8, static_cast<std::uint32_t>(p.size));
	return m;
}

// The hardware JPEG quality from what the viewers take: down while they are busy with earlier pictures when the next
// capture is due, back up to the configured quality while they keep up.
struct AdaptiveQuality {
	static constexpr int min_quality = 30;
	static constexpr auto window_length = 500ms;
	static constexpr double busy_share_limit = 0.25;
	int quality = 0;
	Clock::time_point window{};
	int ticks = 0, busy = 0;

	int next(int ceiling, bool was_busy, Clock::time_point now)
	{
		if (quality == 0 || quality > ceiling) quality = ceiling;
		if (window == Clock::time_point{}) window = now;
		ticks++;
		if (was_busy) busy++;
		if (now - window < window_length) return quality;
		double share = static_cast<double>(busy) / ticks;
		if (share > busy_share_limit) quality -= std::max(5, quality / 5);
		else if (busy == 0) quality += 5;
		quality = std::min(std::max(quality, std::min(min_quality, ceiling)), ceiling);
		window = now;
		ticks = busy = 0;
		return quality;
	}
};

struct Client;
void add_client(std::shared_ptr<Client> client);
void remove_client(Client *client);
void check_tiles();

struct Client : http::WsHandler, std::enable_shared_from_this<Client> {
	bool tiled = false;
	// Classic: the multipart response.
	std::shared_ptr<http::Stream> stream;
	// Tiled: the WebSocket and its messages, oldest first; a viewer that misses one waits for a whole picture.
	http::WebSocket *ws = nullptr;
	std::deque<http::Message> frames;
	bool resync = true;
	// Owned by the capture loop.
	std::uint64_t generation = 0;
	Clock::time_point last_offer{};
	// A write that does not progress for write_timeout drops the viewer (it would hold the capture up).
	std::uint64_t progress = 0, taken = 0;
	Clock::time_point stalled_since{};

	bool stalled(Clock::time_point now)
	{
		if (gone() || due()) {
			stalled_since = {};
			return false;
		}
		std::uint64_t p = tiled ? taken : stream->written();
		if (stalled_since == Clock::time_point{} || p != progress) {
			progress = p;
			stalled_since = now;
			return false;
		}
		return now - stalled_since > write_timeout;
	}
	void drop()
	{
		if (tiled) {
			if (ws) ws->abort();
		} else {
			stream->abort();
		}
	}

	bool gone() const { return tiled ? !ws || ws->closing() : !stream || stream->closed(); }

	// Will take a picture now: nothing waits to be written.
	bool due() const
	{
		if (gone()) return false;
		if (tiled) return frames.empty() && ws->queue_.empty();
		return stream->queue_.empty() ||
		       (stream->queue_.size() == 1 && stream->front_sent_ == 0 && stream->queue_.back() == part_header);
	}

	// Classic: the newest picture replaces one still waiting, so a slow connection cannot pile them up.
	void offer(const Picture &p)
	{
		std::string part(reinterpret_cast<const char *>(p.data()), p.size);
		part += next_part;
		bool back_unsent = stream->queue_.size() > 1 || (!stream->queue_.empty() && stream->front_sent_ == 0);
		if (back_unsent && stream->queue_.back() != part_header) stream->queue_.back() = std::move(part);
		else stream->write(std::move(part));
	}

	// Tiled: a whole picture replaces the queued messages it makes stale; a full queue is dropped and the viewer
	// waits for the next whole picture.
	bool offer_tile(const http::Message &m, bool whole)
	{
		if (resync && !whole) return false;
		if (whole) {
			frames.clear();
			frames.push_back(m);
			resync = false;
		} else if (frames.size() < tiled_queue) {
			frames.push_back(m);
			resync = false;
		} else {
			frames.clear();
			resync = true;
			return false;
		}
		if (ws) ws->wake();
		return true;
	}

	void opened(http::WebSocket &socket) override
	{
		ws = &socket;
		socket.max_message = 64;  // the viewer sends nothing but control frames
		check_tiles();
		add_client(shared_from_this());
	}
	std::optional<http::Message> next(http::WebSocket &) override
	{
		if (frames.empty()) return std::nullopt;
		http::Message m = std::move(frames.front());
		frames.pop_front();
		taken++;
		return m;
	}
	bool ready(http::WebSocket &) override { return !frames.empty(); }
	void closed(http::WebSocket &) override
	{
		ws = nullptr;
		frames.clear();
		remove_client(this);
	}
};

std::vector<std::shared_ptr<Client>> clients;

enum class Tiles { unknown, checking, available, unavailable };

struct {
	bool running = false, reading = false;
	std::uint64_t generation = 0;
	int fps = 1;
	bool video_active = false;
	Clock::time_point next_video_check{};
	// The last picture read and its generation: viewers get a picture once per generation (or every 5 s).
	Picture last;
	std::uint64_t delivery = 0;
	Clock::time_point next_whole{};
	bool want_whole = true, tiles_used = false;
	AdaptiveQuality adaptive;
	Clock::duration read_time{};
	Clock::time_point refused_until{};
	bool blocked_logged = false;
	Tiles tiles = Tiles::unknown;
} s;
// The video gate, held while the stream runs.
std::unique_ptr<videogate::Hold> gate;

int mjpeg_rate(int screen_fps, bool video_active)
{
	if (screen_fps <= 0) return 1;
	if (video_active && screen_fps > next_to_video_fps) return next_to_video_fps;
	return screen_fps;
}

void log_blocked()
{
	if (!s.blocked_logged) {
		s.blocked_logged = true;
		log::info("{}", blocked_message);
	}
}

// MJPEG unavailable now (the input size, read once per connection).
bool blocked()
{
	auto [w, h] = capture_input_size();
	if (std::max(w, h) <= max_long_side) {
		s.blocked_logged = false;
		return false;
	}
	log_blocked();
	return true;
}

void refuse(http::Response &res)
{
	res.json(409, std::string(R"({"code":-3,"msg":)") + api::quote(blocked_message) + R"(,"data":null})");
}

// The IVE driver is loaded on first use (it is kept out at boot); a driver that gives up on a full HD operation
// too early cannot find changes. Until the check has run, viewers get whole pictures.
void check_tiles()
{
	if (s.tiles != Tiles::unknown) return;
	s.tiles = Tiles::checking;
	loop::blocking([] {
		struct stat st;
		bool ok = true;
		if (::stat(ive_timeout_parameter, &st) != 0) {
			auto r = process::run({"modprobe", "cv181x_ive"}, {.timeout = 10s});
			if (!r.ok()) {
				log::warn("tiled MJPEG unavailable: cannot load the IVE driver: {}", r.output);
				ok = false;
			}
		}
		if (ok) {
			std::ifstream in(ive_timeout_parameter);
			int timeout = 0;
			if (!(in >> timeout) || timeout < 20) {
				log::warn("tiled MJPEG unavailable: the IVE driver gives up on full HD operations too early");
				ok = false;
			}
		}
		loop::post([ok] { s.tiles = ok ? Tiles::available : Tiles::unavailable; });
	});
}

bool tiles_only()
{
	if (clients.empty()) return false;
	for (const auto &c : clients)
		if (!c->tiled) return false;
	return s.tiles == Tiles::available;
}

// Offers a picture to the viewers that have not had its generation (or not for 5 s).
bool deliver(const Picture &p, Clock::time_point now)
{
	if (!same_image(s.last, p)) {
		s.last = p;
		s.delivery++;
	}
	bool sent = false;
	std::optional<http::Message> whole;
	bool whole_failed = false;
	for (const auto &c : clients) {
		if (c->gone()) continue;
		if (c->generation == s.delivery && c->last_offer != Clock::time_point{} && now - c->last_offer < duplicate_refresh)
			continue;
		if (c->tiled) {
			if (!whole && !whole_failed) {
				whole = whole_message(p);
				whole_failed = !whole;
			}
			if (!whole) continue;
			c->offer_tile(*whole, true);
		} else {
			c->offer(p);
		}
		c->generation = s.delivery;
		c->last_offer = now;
		sent = true;
	}
	return sent;
}

void tick(std::uint64_t generation);

void schedule(std::uint64_t generation)
{
	loop::after(std::chrono::milliseconds(1000 / s.fps), [generation] { tick(generation); });
}

void stop_loop()
{
	s.running = false;
	s.generation++;
	gate.reset();
	if (s.tiles_used) {
		vision::release_mjpeg_tiles();
		s.tiles_used = false;
	}
	s.last = {};
	vision::release_jpeg_buffers();
	log::debug("mjpeg stream stopped due to no clients");
}

void on_read(std::uint64_t generation, bool tiled_read, Clock::time_point started, vision::Jpeg j)
{
	s.reading = false;
	if (generation != s.generation || !s.running) return;
	auto now = Clock::now();
	s.read_time += (now - started - s.read_time) / 4;
	if (j.result == blocked_result) s.refused_until = now + refusal_hold;
	int status = tiled_read && j.result == tiles_result ? 0 : j.result;
	capture_status::update("mjpeg", status);
	if (j.result == blocked_result) log_blocked();
	else if (j.result >= 0) s.blocked_logged = false;
	if (j.result < 0 && j.result != blocked_result && !(tiled_read && j.result == tiles_unsupported))
		log::error("failed to read {}kvm image: {}", tiled_read ? "tiled " : "", j.result);

	Picture p{j.storage, j.offset, j.size};
	if (!tiled_read) {
		if (j.result < 0 || j.result == unchanged_result || p.empty()) {
			s.last = {};  // signal loss and recovery force a fresh delivery
			return;
		}
		if (deliver(p, now)) count_mjpeg_frame();
		return;
	}
	if (j.result == unchanged_result) return;
	if (j.result == 0 && !p.empty()) {
		if (deliver(p, now)) count_mjpeg_frame();
		return;
	}
	if (j.result != tiles_result || p.empty()) {
		s.want_whole = true;
		s.last = {};
		return;
	}
	s.last = {};  // the viewers now hold more than the last bare JPEG
	bool whole = p.size >= tiles_header && (p.data()[1] & 1);
	if (whole) {
		s.want_whole = false;
		s.next_whole = now + duplicate_refresh;
	}
	http::Message m{p.storage, p.offset, p.size, true};
	bool sent = false;
	for (const auto &c : clients)
		if (!c->gone() && c->offer_tile(m, whole)) sent = true;
	if (sent) count_mjpeg_frame();
}

void tick(std::uint64_t generation)
{
	if (generation != s.generation || !s.running) return;
	Screen screen = capture_screen();
	if (clients.empty()) return stop_loop();
	auto now = Clock::now();
	// The video state is checked once a second, not on every frame.
	if (now > s.next_video_check || (screen.fps != 0 && screen.fps < s.fps)) {
		s.next_video_check = now + 1s;
		s.video_active = active_codec().has_value();
		if (int rate = mjpeg_rate(screen.fps, s.video_active); rate != s.fps && screen.fps != 0) s.fps = rate;
	}
	schedule(generation);
	for (const auto &c : clients)
		if (c->stalled(now)) {
			log::error("mjpeg viewer stalled for {} s; dropping it", write_timeout.count());
			c->drop();
		}
	if (s.reading) return;

	// Read only when a viewer will take the picture: on a slow link the quality steps down instead.
	bool read = std::any_of(clients.begin(), clients.end(), [](const auto &c) { return c->due(); });
	int quality = s.adaptive.next(screen.quality, !read, now);
	if (!read) return;

	// The input may have become 3840x2160 since the viewers connected: native capture refuses it, and the refusal
	// holds for a moment.
	if (now < s.refused_until) {
		log_blocked();
		capture_status::update("mjpeg", blocked_result);
		s.last = {};
		s.want_whole = true;
		return;
	}

	s.reading = true;
	auto q = static_cast<std::uint16_t>(quality);
	if (tiles_only()) {
		bool key = s.want_whole || now >= s.next_whole;
		for (const auto &c : clients) key = key || c->resync;
		s.tiles_used = true;
		vision::read_mjpeg_tiles(screen.width, screen.height, q, key, max_tiles,
		                         [generation, now](vision::Jpeg j) { on_read(generation, true, now, std::move(j)); });
		return;
	}
	s.want_whole = true;
	vision::read_mjpeg(screen.width, screen.height, q,
	                   [generation, now](vision::Jpeg j) { on_read(generation, false, now, std::move(j)); });
}

void add_client(std::shared_ptr<Client> client)
{
	clients.push_back(std::move(client));
	hdmi::set_viewers("mjpeg", static_cast<int>(clients.size()));
	if (s.running) return;
	s.running = true;
	auto generation = ++s.generation;
	check_screen();
	s.video_active = active_codec().has_value();
	s.fps = mjpeg_rate(capture_screen().fps, s.video_active);
	s.next_video_check = Clock::now() + 1s;
	s.last = {};
	s.want_whole = true;
	s.adaptive = {};
	s.read_time = {};
	s.reading = false;
	log::debug("mjpeg stream started");
	// Background OCR has ended before the JPEG encoder takes its memory (the viewer update that woke the capture held
	// the gate already).
	gate = videogate::hold([generation] {
		if (generation == s.generation && s.running) schedule(generation);
	});
	if (gate->granted()) schedule(generation);
}

void remove_client(Client *client)
{
	auto it = std::find_if(clients.begin(), clients.end(), [client](const auto &c) { return c.get() == client; });
	if (it == clients.end()) return;
	auto keep = *it;  // the client may be running this call
	clients.erase(it);
	hdmi::set_viewers("mjpeg", static_cast<int>(clients.size()));
	log::debug("mjpeg connection removed, remaining clients: {}", clients.size());
	loop::post([keep] {});
}

bool frame_detect_enabled()
{
	std::ifstream in(frame_detect_file);
	std::string v;
	return in >> v && v == "1";
}

std::string server_date()
{
	std::time_t t = std::time(nullptr);
	std::tm tm{};
	localtime_r(&t, &tm);
	char buf[64];
	std::strftime(buf, sizeof buf, "%a, %d %b %Y %H:%M:%S %Z", &tm);
	return buf;
}

} // namespace

void add_routes(http::Router &router)
{
	router.add("GET", "/api/stream/mjpeg", auth::guarded([](http::Request &req, http::Response &res) {
		if (!system::failsafe::require_video(res)) return;
		if (blocked()) return refuse(res);
		auto client = std::make_shared<Client>();
		client->stream = std::make_shared<http::Stream>();
		res.content_type = "multipart/x-mixed-replace; boundary=frame";
		res.set_header("Cache-Control", "no-cache");
		res.set_header("Pragma", "no-cache");
		res.set_header("X-Server-Date", server_date());
		res.stream = client->stream;
		res.done = true;
		client->stream->write(part_header);
		// The stream ends with its session: sign-out, a revoked or changed account, the token's expiry, idle sign-out.
		auto session = auth::join_session(req, [stream = client->stream] { stream->abort(); });
		client->stream->on_close = [raw = client.get(), session] {
			auth::leave_session(session);
			remove_client(raw);
		};
		add_client(client);
	}, {{}, false, true}));

	router.ws("/api/stream/mjpeg/tiles",
	          auth::guarded_ws([](http::Request &, http::Response &res) -> std::shared_ptr<http::WsHandler> {
		          if (!system::failsafe::require_video(res)) return nullptr;
		          if (blocked()) {
			          refuse(res);
			          return nullptr;
		          }
		          auto client = std::make_shared<Client>();
		          client->tiled = true;
		          return client;
	          }));

	router.add("POST", "/api/stream/mjpeg/detect", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<bool> enabled;
		if (params) enabled = params->boolean("enabled", ok);
		if (!params || !ok) return api::err(res, -1, "invalid parameters");
		bool on = enabled.value_or(false);
		if (!config::atomic_write(frame_detect_file, on ? "1\n" : "0\n", 0644)) {
			log::error("save frame detect failed");
			return api::err(res, -2, "save frame detect failed");
		}
		vision::set_frame_detect(on ? frame_detect_interval : 0);
		api::ok(res);
		log::debug("update frame detect: {}", on);
	}, auth::Guard{{"admin"}}));

	router.add("POST", "/api/stream/mjpeg/detect/stop", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<long long> duration;
		if (params) duration = params->integer("duration", ok);
		if (!params || !ok) return api::err(res, -1, "invalid parameters");
		// Nothing to pause, and restoring would enable it against the saved choice.
		if (!frame_detect_enabled()) return api::ok(res);
		auto pause = std::chrono::seconds(duration.value_or(0) > 0 ? *duration : 10);
		vision::set_frame_detect(0);
		res.pending = true;
		loop::after(std::chrono::duration_cast<std::chrono::milliseconds>(pause), [id = req.id] {
			// The saved choice, which an administrator may have changed meanwhile.
			vision::set_frame_detect(frame_detect_enabled() ? frame_detect_interval : 0);
			http::Response out;
			api::ok(out);
			http::complete(id, std::move(out));
		});
	}));
}

} // namespace nkos::video::mjpeg
