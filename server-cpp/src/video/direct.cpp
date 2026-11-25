#include "video/direct.hpp"

#include "auth/session.hpp"
#include "common/log.hpp"
#include "http/websocket.hpp"
#include "system/failsafe.hpp"
#include "video/capture_status.hpp"
#include "video/hdmi.hpp"
#include "video/source.hpp"

#include <openssl/rand.h>

#include <algorithm>
#include <chrono>
#include <cstring>
#include <deque>
#include <format>
#include <set>

// The Direct transport: Annex B access units over a WebSocket, decoded by WebCodecs in the page. As the Go
// server's service/stream/direct. Messages to the page: 5 + u32 instance (the first one; a page that finds another
// instance knows the server restarted under a dead socket), then per frame u8 key + u64 timestamp (us) + the
// access unit. From the page: 2 + u64 ACK (cumulative, with ?flow=N), 3 resync (drop the queue, wait for an IDR),
// 4 key frame request (a recording starts).

namespace nkos::video::direct {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

constexpr unsigned char frame_ack_message = 2, stream_resync_message = 3, keyframe_request_message = 4,
                        instance_message = 5;

// The pending queue holds about queue_delay of video, so a stall costs the same at any rate; the frame count only
// bounds it.
constexpr int default_queue_frames = 8, max_queue_frames = 24;
constexpr auto queue_delay = 150ms;
constexpr std::size_t default_queue_bytes = 2 * 1024 * 1024;
// The capture applies a request at most every 500 ms and the encoder then needs a frame or two for the IDR.
constexpr auto keyframe_request_interval = 1s;
// A viewer that drops again soon after an IDR resumed it cannot take the stream: its requests space out (doubling up
// to the maximum), since every IDR goes to all viewers and costs the bits of many pictures. A viewer that keeps up
// for relapse_calm asks at keyframe_request_interval again.
constexpr auto relapse_window = 3s, relapse_calm = 10s, keyframe_request_interval_max = 8s;
// A frame over the byte limit suggests the next IDR will not fit either: requests pause, the pause doubling while
// oversized frames keep arriving after it ran out, and forgotten after a whole maximum without one.
constexpr auto oversized_backoff_min = 2s, oversized_backoff_max = 30s;
// Keep in step with direct.worker.ts (decoderHighWatermark + flowControlWindow < maxPendingDecodes).
constexpr int max_flow_window = 8;
constexpr auto drop_log_interval = 1s;

const std::uint32_t instance_id = [] {
	std::uint32_t v = 0;
	RAND_bytes(reinterpret_cast<unsigned char *>(&v), sizeof v);
	return v;
}();

int queue_depth_for(std::chrono::microseconds duration)
{
	if (duration.count() <= 0) return default_queue_frames;
	int frames = static_cast<int>((std::chrono::duration_cast<std::chrono::microseconds>(queue_delay) + duration / 2) / duration);
	return std::clamp(frames, default_queue_frames, max_queue_frames);
}

std::string ms(Clock::duration d)
{
	return std::format("{}ms", std::chrono::duration_cast<std::chrono::milliseconds>(d).count());
}

struct Outbound {
	Frame frame;  // its 9 bytes before the data hold the Direct header
	bool key() const { return frame.key(); }
	std::size_t bytes() const { return frame.size + 9; }
};

// The drop sequences of a viewer: begun when the queue discards deltas and waits for a key frame, ended by an
// accepted key frame. Reasons: queue-frames (the browser or network does not drain at the capture rate),
// queue-bytes (large frames), discontinuity (no viewer could take a frame, so the shared source moved past),
// oversized-delta / oversized-keyframe (one frame over the byte limit), resync (the browser asked), join.
struct Diagnostics {
	std::uint64_t overflows = 0, overflow_frames = 0, overflow_bytes = 0, discontinuities = 0, discontinuity_frames = 0,
	              oversized_deltas = 0, oversized_keyframes = 0, resyncs = 0, deltas_refused = 0, keyframe_requests = 0,
	              recoveries = 0, sequence_drops = 0;
	Clock::duration max_recovery{};
	Clock::time_point wait_since, last_log;
	std::string wait_reason;

	void begin(const char *reason, Clock::time_point now)
	{
		wait_since = now;
		wait_reason = reason;
		sequence_drops = 0;
	}
	void recovered(Clock::time_point now)
	{
		auto waited = std::max(now - wait_since, Clock::duration{});
		recoveries++;
		max_recovery = std::max(max_recovery, waited);
		log::debug("direct stream recovered: reason={} waited={} refused_deltas={} keyframe_requests={}", wait_reason,
		           ms(waited), sequence_drops, keyframe_requests);
	}
	std::string summary() const
	{
		if (overflows + discontinuities + oversized_deltas + oversized_keyframes + resyncs == 0) return {};
		return std::format("overflows={} overflow_frames={} overflow_bytes={} discontinuities={} discontinuity_frames={} "
		                   "oversized_deltas={} oversized_keyframes={} resyncs={} refused_deltas={} keyframe_requests={} "
		                   "recoveries={} max_recovery={}",
		                   overflows, overflow_frames, overflow_bytes, discontinuities, discontinuity_frames,
		                   oversized_deltas, oversized_keyframes, resyncs, deltas_refused, keyframe_requests, recoveries,
		                   ms(max_recovery));
	}
};

class FrameQueue {
public:
	FrameQueue() { diag_.begin("join", Clock::now()); }

	void enable_flow_control(int window)
	{
		clear();
		in_flight_.clear();
		window_ = std::clamp(window, 1, max_flow_window);
		flow_controlled_ = true;
		waiting_for_keyframe_ = true;
		lagging_ = false;
		diag_.begin("join", Clock::now());
		reset_oversized();
	}

	// active, flow controlled, can advance.
	bool capture_demand() const
	{
		if (closed_) return false;
		if (!flow_controlled_) return true;
		return waiting_for_keyframe_ || (static_cast<int>(frames_.size()) < max_frames_ && queued_bytes_ < max_bytes_);
	}

	bool offer(const Outbound &frame)
	{
		if (closed_) return false;
		if (frame.frame.duration.count() > 0) max_frames_ = queue_depth_for(frame.frame.duration);
		auto now = Clock::now();
		if (frame.key()) {
			std::size_t queued_frames = frames_.size(), queued_bytes = queued_bytes_;
			clear();
			if (frame.bytes() > max_bytes_) {
				start_wait("oversized-keyframe");
				begin_oversized(now);
				diag_.oversized_keyframes++;
				note("oversized-keyframe", queued_frames, queued_bytes, frame.bytes());
				return false;
			}
			if (waiting_for_keyframe_) {
				diag_.recovered(now);
				recovered_at_ = now;
			}
			waiting_for_keyframe_ = false;
			lagging_ = false;
			reset_oversized();
			push(frame);
			return true;
		}
		bool oversized = frame.bytes() > max_bytes_;
		if (waiting_for_keyframe_) {
			// The decoder is still configured and cannot tell frames stopped: keep asking for the IDR here.
			// A picture lost after a drop counts against the bit rate (not while a new viewer waits for its first).
			if (lagging_) video::report_lag(this);
			diag_.deltas_refused++;
			diag_.sequence_drops++;
			if (oversized) diag_.oversized_deltas++;
			drop_delta(oversized);
			return false;
		}
		if (static_cast<int>(frames_.size()) >= max_frames_ || queued_bytes_ + frame.bytes() > max_bytes_) {
			const char *reason = static_cast<int>(frames_.size()) < max_frames_ ? "queue-bytes" : "queue-frames";
			if (oversized) reason = "oversized-delta";
			diag_.overflows++;
			diag_.overflow_frames += frames_.size() + 1;
			diag_.overflow_bytes += queued_bytes_ + frame.bytes();
			if (oversized) diag_.oversized_deltas++;
			std::size_t queued_frames = frames_.size(), queued_bytes = queued_bytes_;
			clear();
			start_wait(reason);
			drop_delta(oversized);
			note(reason, queued_frames, queued_bytes, frame.bytes());
			return false;
		}
		push(frame);
		return true;
	}

	// pop_for_write() has a frame.
	bool writable() const
	{
		return !closed_ && !frames_.empty() && (!flow_controlled_ || static_cast<int>(in_flight_.size()) < window_);
	}

	std::optional<Outbound> pop_for_write()
	{
		if (closed_ || frames_.empty() || (flow_controlled_ && static_cast<int>(in_flight_.size()) >= window_))
			return std::nullopt;
		Outbound frame = std::move(frames_.front());
		frames_.pop_front();
		queued_bytes_ -= frame.bytes();
		if (flow_controlled_) in_flight_.push_back(frame.frame.timestamp);
		return frame;
	}

	// True when credits came back (the writer may have queued frames).
	bool acknowledge(std::int64_t timestamp)
	{
		std::size_t n = 0;
		while (n < in_flight_.size() && in_flight_[n] <= timestamp) n++;
		if (n == 0) return false;
		in_flight_.erase(in_flight_.begin(), in_flight_.begin() + static_cast<long>(n));
		return true;
	}

	void request_resync()
	{
		std::size_t queued_frames = frames_.size(), queued_bytes = queued_bytes_;
		clear();
		in_flight_.clear();
		if (start_wait("resync")) {
			diag_.resyncs++;
			note("resync", queued_frames, queued_bytes, 0);
		}
		reset_oversized();
		// The caller forwards the browser's request itself; the refused deltas do not repeat it.
		last_keyframe_request_ = Clock::now();
	}

	void mark_discontinuity()
	{
		std::size_t queued_frames = frames_.size(), queued_bytes = queued_bytes_;
		clear();
		if (start_wait("discontinuity")) {
			diag_.discontinuities++;
			diag_.discontinuity_frames += queued_frames;
			note("discontinuity", queued_frames, queued_bytes, 0);
		}
		// Only an IDR can resume this viewer; do not wait for the natural GOP boundary.
		request_keyframe_limited();
	}

	void close()
	{
		closed_ = true;
		clear();
		in_flight_.clear();
	}

	std::string summary() const { return diag_.summary(); }

private:
	bool start_wait(const char *reason)
	{
		lagging_ = true;
		video::report_lag(this);
		if (recovered_at_ != Clock::time_point{}) {
			auto since = Clock::now() - recovered_at_;
			if (since < relapse_window)
				request_interval_ = std::min<Clock::duration>(request_interval_ * 2, keyframe_request_interval_max);
			else if (since >= relapse_calm)
				request_interval_ = keyframe_request_interval;
		}
		if (waiting_for_keyframe_) return false;
		waiting_for_keyframe_ = true;
		diag_.begin(reason, Clock::now());
		return true;
	}

	void note(const char *reason, std::size_t queued_frames, std::size_t queued_bytes, std::size_t frame_bytes)
	{
		auto now = Clock::now();
		if (diag_.last_log != Clock::time_point{} && now - diag_.last_log < drop_log_interval) return;
		diag_.last_log = now;
		log::debug("direct stream drop: reason={} frame_bytes={} queued={}/{} frames {}/{} bytes in_flight={}/{} "
		           "overflows={} discontinuities={} oversized={}/{} refused_deltas={} keyframe_requests={} "
		           "oversized_backoff={}",
		           reason, frame_bytes, queued_frames, max_frames_, queued_bytes, max_bytes_, in_flight_.size(), window_,
		           diag_.overflows, diag_.discontinuities, diag_.oversized_deltas, diag_.oversized_keyframes,
		           diag_.deltas_refused, diag_.keyframe_requests, ms(oversized_backoff_));
	}

	void begin_oversized(Clock::time_point now)
	{
		auto end = oversized_at_ + oversized_backoff_;
		if (oversized_backoff_ == Clock::duration{} || now - end >= oversized_backoff_max) {
			oversized_backoff_ = oversized_backoff_min;
		} else if (now < end) {
			return;
		} else {
			oversized_backoff_ = std::min<Clock::duration>(oversized_backoff_ * 2, oversized_backoff_max);
		}
		oversized_at_ = now;
	}
	bool oversized_suppressed(Clock::time_point now) const
	{
		return oversized_backoff_ != Clock::duration{} && now < oversized_at_ + oversized_backoff_;
	}
	void reset_oversized()
	{
		oversized_at_ = {};
		oversized_backoff_ = {};
	}

	// One request per drop sequence plus a repeat while the stream stays stalled; none while an oversized frame
	// holds requests back.
	void request_keyframe_limited()
	{
		auto now = Clock::now();
		if (oversized_suppressed(now)) return;
		if (last_keyframe_request_ != Clock::time_point{} && now - last_keyframe_request_ < request_interval_)
			return;
		last_keyframe_request_ = now;
		diag_.keyframe_requests++;
		video::request_keyframe();
	}

	// After a refused delta: a recovery IDR, or for a delta over the byte limit the suppression window (with the
	// request that a run-out window held back made first, so a stream of oversized deltas still gets a retry).
	void drop_delta(bool oversized)
	{
		if (oversized) {
			auto now = Clock::now();
			if (oversized_backoff_ != Clock::duration{} && !oversized_suppressed(now)) request_keyframe_limited();
			begin_oversized(now);
			return;
		}
		request_keyframe_limited();
	}

	void push(const Outbound &frame)
	{
		frames_.push_back(frame);
		queued_bytes_ += frame.bytes();
	}
	void clear()
	{
		frames_.clear();
		queued_bytes_ = 0;
	}

	std::deque<Outbound> frames_;
	std::size_t queued_bytes_ = 0;
	std::deque<std::int64_t> in_flight_;
	int max_frames_ = default_queue_frames;
	std::size_t max_bytes_ = default_queue_bytes;
	int window_ = 0;
	bool flow_controlled_ = false, waiting_for_keyframe_ = true, closed_ = false;
	bool lagging_ = false;  // dropped pictures; reported until an IDR resumes the viewer
	Clock::time_point last_keyframe_request_{}, oversized_at_{}, recovered_at_{};
	Clock::duration request_interval_ = keyframe_request_interval;
	Clock::duration oversized_backoff_{};
	Diagnostics diag_;
};

struct Client;

// The viewers on one subscription of the device stream.
class Streamer : public Subscriber {
public:
	bool reports_lag() const override { return true; }
	// An error message when the client cannot join (codec conflict, H.264 blocked).
	std::optional<std::string> add(Client *client, Codec codec);
	void remove(Client *client);
	void frame(const Frame &frame) override;
	void ended() override;

private:
	std::set<Client *> clients_;
	std::optional<Codec> codec_;
};

Streamer streamer;

struct Client : http::WsHandler {
	http::WebSocket *ws = nullptr;
	FrameQueue queue;
	Codec codec;
	int flow = 0;
	bool probe = false;
	bool joined = false;

	void opened(http::WebSocket &socket) override
	{
		ws = &socket;
		ws->max_message = 64;
		unsigned char m[5] = {instance_message};
		std::memcpy(m + 1, &instance_id, 4);  // little endian on the target
		ws->send(http::make_message(std::string_view(reinterpret_cast<char *>(m), 5)));
		if (probe) {
			ws->close(1000, "");
			return;
		}
		if (flow > 0) queue.enable_flow_control(flow);
		if (auto error = streamer.add(this, codec)) {
			ws->close(1008, *error);
			return;
		}
		joined = true;
		log::debug("direct video websocket connected: {}", ws->request().peer);
	}

	void received(http::WebSocket &socket, std::string_view data, bool binary) override
	{
		if (!binary || data.empty()) return;
		auto type = static_cast<unsigned char>(data[0]);
		if (type == frame_ack_message && data.size() == 9) {
			std::int64_t timestamp;
			std::memcpy(&timestamp, data.data() + 1, 8);
			if (queue.acknowledge(timestamp) && queue.writable()) socket.wake();
		} else if (type == stream_resync_message && data.size() == 1) {
			queue.request_resync();
			video::request_keyframe();
		} else if (type == keyframe_request_message && data.size() == 1) {
			video::request_keyframe();
		}
	}

	std::optional<http::Message> next(http::WebSocket &) override
	{
		auto frame = queue.pop_for_write();
		if (!frame) return std::nullopt;
		return http::Message{frame->frame.storage, frame->frame.offset - 9, frame->frame.size + 9, true};
	}

	bool ready(http::WebSocket &) override { return queue.writable(); }

	void closed(http::WebSocket &socket) override
	{
		if (joined) streamer.remove(this);
		queue.close();
		ws = nullptr;
		if (auto summary = queue.summary(); !summary.empty())
			log::info("direct video client {} lost frames: {}", socket.request().peer, summary);
		log::debug("direct video websocket disconnected: {}", socket.request().peer);
	}

	void offer(const Outbound &frame)
	{
		if (queue.offer(frame) && ws && queue.writable()) ws->wake();
	}
	void end(const std::string &reason)
	{
		joined = false;
		queue.close();
		if (ws) ws->close(1008, reason);
	}
};

std::optional<std::string> Streamer::add(Client *client, Codec codec)
{
	if (!clients_.empty() && codec_ && *codec_ != codec)
		return std::format("video encoder is already in use with {{Codec:{}}}; requested {{Codec:{}}}",
		                   codec_name(*codec_), codec_name(codec));
	if (clients_.empty()) {
		if (auto error = subscribe(this, codec)) return error->message;
		codec_ = codec;
	}
	clients_.insert(client);
	hdmi::set_viewers("direct", static_cast<int>(clients_.size()));
	return std::nullopt;
}

void Streamer::remove(Client *client)
{
	if (!clients_.erase(client)) return;
	hdmi::set_viewers("direct", static_cast<int>(clients_.size()));
	if (clients_.empty()) {
		unsubscribe(this);
		codec_.reset();
	}
	log::debug("h264 websocket disconnected, remaining clients: {}", clients_.size());
}

void Streamer::frame(const Frame &frame)
{
	bool demand = false;
	for (auto *c : clients_) demand = demand || c->queue.capture_demand();
	if (!demand) {
		// The shared source already advanced: a viewer whose whole window is in flight cannot resume on the next
		// delta, which may reference the frame skipped here; it waits for the next key frame.
		for (auto *c : clients_) c->queue.mark_discontinuity();
		return;
	}
	capture_status::update("direct", frame.result);
	if (frame.result < 0 || frame.size == 0) return;
	Outbound out{frame};
	unsigned char *header = frame.data() - 9;
	header[0] = frame.key() ? 1 : 0;
	std::uint64_t ts = static_cast<std::uint64_t>(frame.timestamp);
	std::memcpy(header + 1, &ts, 8);
	auto targets = std::vector<Client *>(clients_.begin(), clients_.end());
	for (auto *c : targets) c->offer(out);
}

void Streamer::ended()
{
	auto targets = std::vector<Client *>(clients_.begin(), clients_.end());
	clients_.clear();
	codec_.reset();
	hdmi::set_viewers("direct", 0);
	for (auto *c : targets) c->end("encoder-reconfigured");
}

} // namespace

void add_routes(http::Router &router)
{
	router.ws("/api/stream/video/direct",
	          auth::guarded_ws([](http::Request &req, http::Response &res) -> std::shared_ptr<http::WsHandler> {
		          if (!system::failsafe::require_video(res)) return nullptr;
		          Codec codec = default_codec;
		          std::string name = req.query_value("codec");
		          if (!name.empty()) {
			          auto parsed = parse_codec(name);
			          if (!parsed) {
				          res.text(400, std::format("invalid codec \"{}\": expected h264 or h265", name));
				          return nullptr;
			          }
			          codec = *parsed;
		          }
		          if (std::string rc = req.query_value("rc"); !rc.empty() && rc != "cbr") {
			          res.text(400, std::format("invalid rate control \"{}\": only cbr is supported", rc));
			          return nullptr;
		          }
		          auto client = std::make_shared<Client>();
		          client->codec = codec;
		          client->probe = req.query_value("probe") == "1";
		          std::string flow = req.query_value("flow");
		          if (!flow.empty() && flow.find_first_not_of("0123456789") == std::string::npos && flow.size() < 4)
			          client->flow = std::stoi(flow);
		          return client;
	          }));
}

} // namespace nkos::video::direct
