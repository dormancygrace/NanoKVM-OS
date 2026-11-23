// The RustDesk bridge, as the Go server's service/rustdesk bridge.go, control.go, audio.go and pointer.go: the
// daemon's media, control and audio sockets (webrtc.sock in webrtc_bridge.cpp). It has no network listener; the
// sockets are mode 0600 in a 0700 directory and take root peers only.
//
// Each connection runs on a thread of its own and blocks on its socket. The device's state (the video source, the
// HID lanes and input control, the audio capture) belongs to the loop thread: a connection reaches it through
// loop::post and waits, with a deadline, for the answer.

#include "rustdesk/bridge_rtc.hpp"
#include "rustdesk/bridge_socket.hpp"
#include "rustdesk/bridge_wire.hpp"
#include "rustdesk/internal.hpp"

#include "common/log.hpp"
#include "common/loop.hpp"
#include "input/control.hpp"
#include "input/hid.hpp"
#include "input/ws.hpp"
#include "usb/gotext.hpp"
#include "video/audio.hpp"
#include "video/screen.hpp"
#include "video/source.hpp"

#include <array>
#include <condition_variable>
#include <deque>
#include <fstream>
#include <map>
#include <mutex>
#include <set>
#include <sstream>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::rustdesk {

namespace {

using namespace std::chrono_literals;
using sock::Clock;
using sock::Conn;
using sock::no_deadline;
using wire::Kind;

constexpr std::size_t max_media_connections = 8, max_audio_connections = 8, max_hid_sessions = 8;
constexpr std::size_t max_frame = 8u << 20;
constexpr auto hid_session_idle = 10s;

// --- the loop thread ------------------------------------------------------------------------------------------------

// Runs fn on the loop thread and waits for it until the deadline. fn runs entirely or not at all: once the waiter
// has given up, a late fn is skipped.
bool on_loop(std::function<void()> fn, Clock::time_point deadline)
{
	struct State {
		std::mutex mu;
		std::condition_variable cv;
		bool done = false, abandoned = false;
	};
	auto state = std::make_shared<State>();
	loop::post([state, fn = std::move(fn)] {
		std::lock_guard lock(state->mu);
		if (state->abandoned) return;
		fn();
		state->done = true;
		state->cv.notify_all();
	});
	std::unique_lock lock(state->mu);
	if (!state->cv.wait_until(lock, deadline, [&] { return state->done; })) state->abandoned = true;
	return state->done;
}

// A frame's buffer goes back to the frame pool, which belongs to the loop thread.
void release_on_loop(video::Frame frame)
{
	if (frame.storage) loop::post([f = std::move(frame)] {});
}

// readMediaDimension: a runtime size file, 0 when it is missing or invalid.
std::uint16_t read_dimension(const char *path)
{
	std::ifstream in(path, std::ios::binary);
	if (!in) return 0;
	std::ostringstream s;
	s << in.rdbuf();
	return wire::parse_dimension(s.str());
}

// mediaDimensions after the capture's own stream limit (the loop thread's part): the stream size files, then the
// input size.
std::pair<std::uint16_t, std::uint16_t> media_dimensions(std::uint16_t width, std::uint16_t height)
{
	if (auto w = read_dimension("/run/nanokvm/stream_width")) width = w;
	if (auto h = read_dimension("/run/nanokvm/stream_height")) height = h;
	if (width == 0 || height == 0) {
		width = read_dimension("/run/nanokvm/width");
		height = read_dimension("/run/nanokvm/height");
	}
	return {width, height};
}

// --- HID sessions (loop thread) ---------------------------------------------------------------------------------

// One remote client's input: its lease, its lanes to the HID writers, what it holds down. Loop thread.
struct HidSession {
	std::string id;
	bool closed = false;
	Clock::time_point touched = Clock::now();
	std::shared_ptr<input::control::ManualSession> manual = input::control::ManualSession::create();
	std::unique_ptr<input::hid::Lane> lane = std::make_unique<input::hid::Lane>();
	bool relative_only = false;
	wire::RelativePointer pointer;
	std::map<Kind, std::vector<std::uint8_t>> held;
	std::size_t inflight = 0;  // reports queued on the lanes and not answered yet
	bool releasing = false;
	std::vector<std::function<void()>> on_closed;  // close records waiting for the release
};

input::control::ReportKind report_kind(Kind k) { return static_cast<input::control::ReportKind>(static_cast<int>(k)); }

// The answer of a record, set on the loop thread (now or after its writes), read by the connection.
struct Pending {
	std::mutex mu;
	std::condition_variable cv;
	bool done = false;
	std::uint8_t status = wire::status_ok;
	void set(std::uint8_t s)
	{
		std::lock_guard lock(mu);
		if (done) return;
		status = s;
		done = true;
		cv.notify_all();
	}
	std::uint8_t wait(Clock::time_point deadline)
	{
		std::unique_lock lock(mu);
		if (!cv.wait_until(lock, deadline, [&] { return done; })) return wire::status_timeout;
		return status;
	}
};

struct State;

// The release of a closed session once its queued reports are answered (Go's close waits for the record in flight
// the same way, on the session's mutex): the held keys and buttons, then the manual session and the lease end.
void release_session(const std::shared_ptr<HidSession> &s)
{
	if (s->releasing || s->inflight > 0) return;
	s->releasing = true;
	auto finish = [s] {
		s->manual->close();
		s->lane.reset();
		input::ws::release_external_input(s->id);
		for (auto &f : s->on_closed) f();
		s->on_closed.clear();
	};
	struct Count {
		int left = 0;
		std::function<void()> finish;
	};
	auto count = std::make_shared<Count>();
	count->finish = finish;
	for (Kind kind : {Kind::keyboard, Kind::relative, Kind::absolute}) {
		auto it = s->held.find(kind);
		if (it == s->held.end()) continue;
		auto reservation = s->manual->reserve(report_kind(kind), false, true);
		if (!reservation) continue;
		input::hid::Report r;
		r.data = wire::release_report(kind, it->second);
		r.reservation = reservation;
		r.complete = [count, s, kind](bool ok) {
			if (!ok) log::error("RustDesk HID release failed");
			else s->held.erase(kind);
			if (--count->left == 0) count->finish();
		};
		count->left++;
		if (kind == Kind::keyboard) s->lane->keyboard(std::move(r));
		else s->lane->mouse(std::move(r));
	}
	if (count->left == 0) finish();
}

// hidSession.close.
void close_session(const std::shared_ptr<HidSession> &s)
{
	if (s->closed) return;
	s->closed = true;
	release_session(s);
}

// --- the bridge's state -------------------------------------------------------------------------------------------

struct State : std::enable_shared_from_this<State> {
	std::mutex mu;
	bool open = false;
	std::uint64_t generation = 0;
	std::unique_ptr<sock::Listener> media, control, audio;
	std::unique_ptr<RtcBridge> rtc;
	std::set<std::shared_ptr<Conn>> media_conns, control_conns, audio_conns;
	// Loop thread.
	std::map<std::string, std::shared_ptr<HidSession>> sessions;

	// Admits a connection of a kind under its limit, while the listener of this generation is open.
	bool admit(std::set<std::shared_ptr<Conn>> &set, std::size_t limit, std::uint64_t gen, const std::shared_ptr<Conn> &c)
	{
		std::lock_guard lock(mu);
		if (!open || generation != gen || set.size() >= limit) return false;
		set.insert(c);
		return true;
	}
	void leave(std::set<std::shared_ptr<Conn>> &set, const std::shared_ptr<Conn> &c)
	{
		std::lock_guard lock(mu);
		set.erase(c);
	}

	void serve_media(Conn &c);
	void serve_control(Conn &c);
	void serve_audio(Conn &c);
	std::uint8_t control_frame(const std::string &id, const std::uint8_t *records, std::size_t count);
	std::uint8_t control_record(const std::string &id, const std::uint8_t *record, Clock::time_point deadline);
};

// --- media.sock ---------------------------------------------------------------------------------------------------

// A connection's share of the device stream: the frames the source hands it (24 at most; when they are not taken
// fast enough the queue is dropped and the next key frame starts it again, as VideoSubscription.send).
struct MediaQueue : video::Subscriber {
	struct Item {
		video::Frame frame;
		std::uint16_t width = 0, height = 0;  // the capture's stream limit when the frame came
	};
	std::mutex mu;
	std::condition_variable cv;
	std::deque<Item> items;
	bool ended_ = false, closed = false, waiting = false;

	// Loop thread.
	void frame(const video::Frame &frame) override
	{
		std::unique_lock lock(mu);
		if (closed || ended_) return;
		if (waiting && frame.result >= 0) {
			if (!frame.key()) return;
			waiting = false;
		}
		auto screen = video::capture_screen();
		if (items.size() >= 24) {
			std::deque<Item> dropped;
			dropped.swap(items);
			video::request_keyframe();
			waiting = true;
			if (!frame.key()) {
				lock.unlock();
				return;
			}
			waiting = false;
		}
		items.push_back({frame, screen.width, screen.height});
		cv.notify_all();
	}
	void ended() override
	{
		std::lock_guard lock(mu);
		ended_ = true;
		cv.notify_all();
	}
	// Subscription.Close from the connection's side.
	void close()
	{
		std::lock_guard lock(mu);
		closed = true;
		cv.notify_all();
	}
	// NextContext: a frame, or false when the subscription ended or the deadline passed.
	bool next(Item &out, Clock::time_point deadline)
	{
		std::unique_lock lock(mu);
		auto ready = [&] { return !items.empty() || ended_ || closed; };
		if (deadline == no_deadline) cv.wait(lock, ready);
		else if (!cv.wait_until(lock, deadline, ready)) return false;
		if (ended_ || closed) return false;
		out = std::move(items.front());
		items.pop_front();
		return true;
	}
};

void State::serve_media(Conn &c)
{
	std::string line;
	if (!c.read_line(line, 4096, Clock::now() + 3s) || line.size() > 2048) return;
	wire::MediaRequest req;
	if (!wire::parse_media_request(line, req)) return;
	std::optional<video::Codec> requested;
	if (req.codec == "h264") requested = video::Codec::h264;
	else if (req.codec == "h265") requested = video::Codec::h265;
	auto queue = std::make_shared<MediaQueue>();
	std::optional<video::SubscribeError> failed;
	video::Codec codec = video::default_codec;
	if (!on_loop([&] {
		    failed = video::subscribe(queue.get(), requested);
		    if (!failed) codec = video::active_codec().value_or(requested.value_or(video::default_codec));
	    }, Clock::now() + 3s))
		failed = video::SubscribeError{"HDMI encoder is unavailable"};
	if (failed) {
		(void)c.write_all(wire::media_error(failed->message), Clock::now() + 2s);
		return;
	}
	// Leaving: the subscription, the frame rate this client asked for, and the frames still queued go on the loop.
	struct Leave {
		std::shared_ptr<MediaQueue> q;
		~Leave()
		{
			q->close();
			loop::post([q = q] {
				video::unsubscribe(q.get());
				video::set_viewer_rate(q.get(), 0);
				std::lock_guard lock(q->mu);
				q->items.clear();
			});
		}
	} leave{queue};

	// The client's requests while frames flow: key frames and its frame rate. A long line or the end of the
	// connection closes the subscription.
	std::mutex done_mu;
	std::condition_variable done_cv;
	bool done = false;
	std::thread reader([&] {
		std::string l;
		while (c.read_line(l, 4096, no_deadline) && l.size() <= 256) {
			long long fps = 0;
			switch (wire::parse_media_command(l, fps)) {
			case wire::MediaCommand::keyframe: loop::post([] { video::request_keyframe(); }); break;
			case wire::MediaCommand::fps:
				loop::post([q = queue, fps] { video::set_viewer_rate(q.get(), static_cast<int>(std::min(fps, 1000LL))); });
				break;
			default: break;
			}
		}
		queue->close();
		std::lock_guard lock(done_mu);
		done = true;
	});
	struct Join {
		Conn &c;
		std::thread &t;
		~Join()
		{
			c.shutdown();
			t.join();
		}
	} join{c, reader};

	auto startup = Clock::now() + 2s;
	bool ready = false;
	Clock::time_point failing{};
	int native = video::native_codec(codec);
	for (std::uint64_t seq = 1;; seq++) {
		MediaQueue::Item item;
		bool ok;
		if (ready) {
			ok = queue->next(item, no_deadline);
		} else {
			// waitInitialMediaFrame: the encoder's first errors are skipped until the startup budget runs out.
			for (;;) {
				ok = queue->next(item, startup);
				if (!ok || item.frame.result >= 0) break;
				release_on_loop(std::move(item.frame));
			}
		}
		// A rate change of the capture fails the encoder's next frame; a new encoder follows with a key frame.
		if (ok && ready && item.frame.result < 0 && (failing == Clock::time_point{} || Clock::now() - failing < 2s)) {
			if (failing == Clock::time_point{}) failing = Clock::now();
			release_on_loop(std::move(item.frame));
			seq--;
			continue;
		}
		if (!ok || item.frame.result < 0 || item.frame.size > max_frame) {
			release_on_loop(std::move(item.frame));
			(void)c.write_all(wire::media_error("HDMI encoder is unavailable"), Clock::now() + 2s);
			return;
		}
		failing = {};
		auto [width, height] = media_dimensions(item.width, item.height);
		if (width == 0 || height == 0) {
			release_on_loop(std::move(item.frame));
			if (!ready && Clock::now() < startup) {
				loop::post([] { video::request_keyframe(); });
				continue;
			}
			(void)c.write_all(wire::media_error("HDMI dimensions are unavailable"), Clock::now() + 2s);
			return;
		}
		ready = true;
		bool info = req.video == "info";
		const video::Frame &f = item.frame;
		std::string header = wire::frame_header(native, f.key(), width, height, seq, f.timestamp, f.duration.count(),
		                                        info ? 0 : static_cast<std::uint32_t>(f.size));
		auto deadline = Clock::now() + 2s;
		bool written = c.write_all(header, deadline) &&
		               (info || c.write_all({reinterpret_cast<const char *>(f.data()), f.size}, deadline));
		release_on_loop(std::move(item.frame));
		if (!written || info) return;
		std::lock_guard lock(done_mu);
		if (done) return;
	}
}

// --- control.sock (OKHC) ------------------------------------------------------------------------------------------

// A connection that ends leaves its session to the idle expiry: the add-on may reconnect for the same session, and
// its held keys must not be released under it.
void State::serve_control(Conn &c)
{
	std::uint8_t hello[24];
	if (!c.read_full(hello, sizeof hello, Clock::now() + 3s) || std::string_view(reinterpret_cast<char *>(hello), 4) != "OKHC" ||
	    hello[4] != wire::control_version)
		return;
	static const char hex[] = "0123456789abcdef";
	std::string id;
	for (int i = 8; i < 24; i++) id += hex[hello[i] >> 4], id += hex[hello[i] & 15];
	std::uint8_t frame[wire::control_record_size * wire::control_max_records];
	for (;;) {
		std::uint8_t count = 0;
		if (!c.read_byte(count, no_deadline) || count == 0 || count > wire::control_max_records) return;
		if (!c.read_full(frame, wire::control_record_size * count, no_deadline)) return;
		std::uint8_t status = control_frame(id, frame, count);
		if (!c.write_all({reinterpret_cast<char *>(&status), 1}, Clock::now() + 2s)) return;
	}
}

std::uint8_t State::control_frame(const std::string &id, const std::uint8_t *records, std::size_t count)
{
	if (records[0] == wire::record_prepare) {
		if (count != 1) return wire::status_invalid;
		if (auto e = service().prepare_usb(); !e.empty()) {
			log::error("RustDesk USB preparation failed: {}", e);
			return wire::status_failed;
		}
		return wire::status_ok;
	}
	// The add-on gives a frame up after three seconds and then releases what it held on a new connection: nothing of
	// this frame may be written after that, so its records share one deadline.
	auto deadline = Clock::now() + 2s;
	for (std::size_t i = 0; i < count; i++)
		if (auto status = control_record(id, records + i * wire::control_record_size, deadline); status != wire::status_ok)
			return status;
	return wire::status_ok;
}

std::uint8_t State::control_record(const std::string &id, const std::uint8_t *record, Clock::time_point deadline)
{
	std::uint8_t record_kind = record[0];
	if (record_kind < wire::record_keyboard || record_kind > wire::record_close) return wire::status_invalid;
	auto pending = std::make_shared<Pending>();
	std::array<std::uint8_t, 8> rec;
	std::copy(record, record + 8, rec.begin());
	auto self = shared_from_this();
	bool ran = on_loop([self, id, rec, pending, deadline] {
		using namespace input;
		std::uint8_t kind_byte = rec[0];
		// Only a report takes the input lease; a heartbeat or close of a session that has none is answered without.
		std::shared_ptr<HidSession> s;
		if (auto it = self->sessions.find(id); it != self->sessions.end()) {
			s = it->second;
		} else if (kind_byte <= wire::record_absolute) {
			if (self->sessions.size() >= max_hid_sessions) return pending->set(wire::status_rejected);
			s = std::make_shared<HidSession>();
			s->id = id;
			std::weak_ptr<HidSession> weak = s;
			if (!ws::acquire_external_input(id, [weak] {
				    if (auto locked = weak.lock()) close_session(locked);
			    }))
				return pending->set(wire::status_lease_held);
			hid::open();
			auto functions = hid::input_functions();
			s->relative_only = functions.relative && !functions.absolute;
			if (s->relative_only) {
				auto screen = video::capture_screen();
				auto [w, h] = media_dimensions(screen.width, screen.height);
				s->pointer.width = w;
				s->pointer.height = h;
			}
			self->sessions[id] = s;
		}
		if (kind_byte == wire::record_close) {
			if (!s) return pending->set(wire::status_ok);
			self->sessions.erase(id);
			if (s->closed) return pending->set(wire::status_ok);
			s->on_closed.push_back([pending] { pending->set(wire::status_ok); });
			return close_session(s);
		}
		if (!s) return pending->set(wire::status_ok);
		if (s->closed) return pending->set(wire::status_rejected);
		s->touched = Clock::now();
		if (kind_byte == wire::record_heartbeat) return pending->set(wire::status_ok);

		Kind kind;
		std::vector<std::vector<std::uint8_t>> reports;
		if (!wire::record_reports(rec.data(), s->relative_only ? &s->pointer : nullptr, kind, reports))
			return pending->set(wire::status_invalid);
		bool held = wire::report_held(kind, reports[0]);
		std::string lease = id;
		auto reservation = s->manual->reserve(report_kind(kind), held, false,
		                                      [lease](control::Mode mode) { return ws::allows_input_lease(lease) && control::allow_manual(mode); });
		if (!reservation) return pending->set(wire::status_rejected);
		reservation->set_deadline(deadline);
		// The writers run the reports in order; the last answer decides the record.
		struct Progress {
			std::size_t left = 0;
			bool ok = true;
		};
		auto progress = std::make_shared<Progress>();
		progress->left = reports.size();
		s->inflight += reports.size();
		for (auto &report : reports) {
			hid::Report r;
			r.data = report;
			r.reservation = reservation;
			r.complete_reservation = false;
			r.reset = [weak = std::weak_ptr<HidSession>(s)](control::ReportKind k) {
				if (auto locked = weak.lock()) {
					locked->held.erase(static_cast<Kind>(static_cast<int>(k)));
					locked->manual->reset(k);
				}
			};
			r.complete = [s, kind, report, progress, reservation, pending, deadline, lease, kind_byte](bool ok) {
				// writeReport, once the report is written: the other mouse let go first (the lane wrote its
				// release), and what this report holds down is what a release must lift. A report that failed or
				// was refused changes nothing here; the lane reports what it released after a failure (reset).
				if (ok) {
					if (kind == Kind::relative) s->held.erase(Kind::absolute);
					else if (kind == Kind::absolute) s->held.erase(Kind::relative);
					if (wire::report_held(kind, report)) s->held[kind] = report;
					else s->held.erase(kind);
				}
				if (!ok) progress->ok = false;
				if (--s->inflight == 0 && s->closed) release_session(s);
				if (--progress->left > 0) return;
				std::uint8_t status = wire::status_ok;
				if (!progress->ok) {
					if (Clock::now() >= deadline) status = wire::status_timeout;
					else if (s->closed || !ws::allows_input_lease(lease) || !reservation->valid()) status = wire::status_rejected;
					else status = wire::status_failed;
					log::error("RustDesk HID report failed: record={} status={}", kind_byte, status);
				}
				reservation->complete(progress->ok);
				pending->set(status);
			};
			if (kind == Kind::keyboard) s->lane->keyboard(std::move(r));
			else s->lane->mouse(std::move(r));
		}
	}, deadline);
	if (!ran) return wire::status_timeout;
	return pending->wait(deadline + 100ms);
}

// --- audio.sock (OKAF) --------------------------------------------------------------------------------------------

struct AudioQueue {
	std::mutex mu;
	std::condition_variable cv;
	std::deque<std::shared_ptr<const std::string>> packets;
	bool ended = false, cancelled = false;
	std::unique_ptr<video::audio::Listener> listener;  // loop thread
};

void State::serve_audio(Conn &c)
{
	std::string line;
	if (!c.read_line(line, 256, Clock::now() + 3s) || line.size() > 256) return;
	std::string what;
	if (!wire::parse_audio_request(line, what)) return;
	auto write = [&c](std::uint8_t codec, std::string_view data) {
		if (data.size() > wire::max_audio_packet) return false;
		auto deadline = Clock::now() + 2s;
		return c.write_all(wire::audio_header(codec, data.size()), deadline) && c.write_all(data, deadline);
	};
	if (!video::audio::enabled()) {
		(void)write(0, {});
		return;
	}
	if (what == "info") {
		(void)write(1, {});
		return;
	}
	auto q = std::make_shared<AudioQueue>();
	std::string error;
	bool ran = on_loop([&] {
		q->listener = video::audio::listen(error);
		if (!q->listener) return;
		std::weak_ptr<AudioQueue> weak = q;
		q->listener->packet = [weak](const video::audio::Packet &p) {
			auto locked = weak.lock();
			if (!locked) return;
			std::lock_guard lock(locked->mu);
			// A reader that falls behind loses the oldest sound, not the newest.
			if (locked->packets.size() >= 32) locked->packets.pop_front();
			locked->packets.push_back(p.data);
			locked->cv.notify_all();
		};
		q->listener->ended = [weak] {
			if (auto locked = weak.lock()) {
				std::lock_guard lock(locked->mu);
				locked->ended = true;
				locked->cv.notify_all();
			}
		};
	}, Clock::now() + 3s);
	struct Leave {
		std::shared_ptr<AudioQueue> q;
		~Leave()
		{
			loop::post([q = q] { q->listener.reset(); });
		}
	} leave{q};
	if (!ran || !q->listener) {
		(void)write(0, {});
		return;
	}
	if (!write(1, {})) return;
	// The peer's end (or anything it sends), mute or the bridge stopping releases the shared capture even when no
	// sound arrives.
	std::thread reader([&c, q] {
		std::uint8_t one;
		(void)c.read_byte(one, no_deadline);
		std::lock_guard lock(q->mu);
		q->cancelled = true;
		q->cv.notify_all();
	});
	struct Join {
		Conn &c;
		std::thread &t;
		~Join()
		{
			c.shutdown();
			t.join();
		}
	} join{c, reader};
	for (;;) {
		std::shared_ptr<const std::string> p;
		{
			std::unique_lock lock(q->mu);
			q->cv.wait(lock, [&] { return !q->packets.empty() || q->ended || q->cancelled; });
			if (q->ended || q->cancelled) return;
			p = std::move(q->packets.front());
			q->packets.pop_front();
		}
		if (!video::audio::enabled() || !p || p->empty() || p->size() > wire::max_audio_packet) return;
		if (!write(1, *p)) return;
	}
}

// --- the bridge ---------------------------------------------------------------------------------------------------

class SocketBridge : public Bridge {
public:
	std::string start() override
	{
		std::lock_guard lock(state_->mu);
		if (state_->open) return {};
		struct stat st{};
		if (::stat(paths.runtime_dir.c_str(), &st) == 0) {
			if (!S_ISDIR(st.st_mode)) return "mkdir " + paths.runtime_dir + ": not a directory";
		} else if (::mkdir(paths.runtime_dir.c_str(), 0700) != 0 && errno != EEXIST) {
			return "mkdir " + paths.runtime_dir + ": " + usb::gotext::errno_text(errno);
		}
		if (::chmod(paths.runtime_dir.c_str(), 0700) != 0)
			return "chmod " + paths.runtime_dir + ": " + usb::gotext::errno_text(errno);
		std::string error;
		std::array<std::string, 4> names = {"media.sock", "control.sock", "webrtc.sock", "audio.sock"};
		std::array<int, 4> fds{-1, -1, -1, -1};
		for (std::size_t i = 0; i < names.size(); i++) {
			fds[i] = sock::listen_socket(paths.runtime_dir + "/" + names[i], error);
			if (fds[i] < 0) {
				for (std::size_t j = 0; j < i; j++) {
					::close(fds[j]);
					::unlink((paths.runtime_dir + "/" + names[j]).c_str());
				}
				return error;
			}
		}
		std::uint64_t gen = ++state_->generation;
		state_->open = true;
		std::weak_ptr<State> weak = state_;
		auto serve = [weak, gen](std::set<std::shared_ptr<Conn>> State::*set, std::size_t limit, void (State::*fn)(Conn &)) {
			return [weak, gen, set, limit, fn](std::shared_ptr<Conn> c) {
				auto s = weak.lock();
				if (!s || !s->admit((*s).*set, limit, gen, c)) return;
				std::thread([s, c, set, fn] {
					((*s).*fn)(*c);
					c->shutdown();
					s->leave((*s).*set, c);
				}).detach();
			};
		};
		state_->media = std::make_unique<sock::Listener>(fds[0], paths.runtime_dir + "/media.sock",
		                                                 serve(&State::media_conns, max_media_connections, &State::serve_media));
		state_->control = std::make_unique<sock::Listener>(fds[1], paths.runtime_dir + "/control.sock",
		                                                   serve(&State::control_conns, wire::control_max_connections, &State::serve_control));
		state_->rtc = start_rtc_bridge(fds[2], paths.runtime_dir + "/webrtc.sock");
		state_->audio = std::make_unique<sock::Listener>(fds[3], paths.runtime_dir + "/audio.sock",
		                                                 serve(&State::audio_conns, max_audio_connections, &State::serve_audio));
		return {};
	}

	void stop() override
	{
		std::unique_ptr<sock::Listener> media, control, audio;
		std::unique_ptr<RtcBridge> rtc;
		std::vector<std::shared_ptr<Conn>> conns;
		{
			std::lock_guard lock(state_->mu);
			if (!state_->open) return;
			state_->open = false;
			media = std::move(state_->media);
			control = std::move(state_->control);
			audio = std::move(state_->audio);
			rtc = std::move(state_->rtc);
			for (auto *set : {&state_->media_conns, &state_->control_conns, &state_->audio_conns})
				conns.insert(conns.end(), set->begin(), set->end());
		}
		for (auto &c : conns) c->shutdown();
		media->close();
		control->close();
		if (rtc) rtc->close();
		audio->close();
		loop::post([s = state_] {
			auto sessions = std::move(s->sessions);
			s->sessions.clear();
			for (auto &[id, session] : sessions) close_session(session);
		});
	}

	// expire: sessions closed elsewhere, or silent for 10 s, end.
	void expire() override
	{
		loop::post([s = state_] {
			auto now = Clock::now();
			for (auto it = s->sessions.begin(); it != s->sessions.end();) {
				auto session = it->second;
				if (session->closed || now - session->touched > hid_session_idle) {
					it = s->sessions.erase(it);
					close_session(session);
				} else {
					++it;
				}
			}
		});
	}

	bool audio_enabled() override { return video::audio::enabled(); }

private:
	std::shared_ptr<State> state_ = std::make_shared<State>();
};

} // namespace

std::shared_ptr<Bridge> make_bridge() { return std::make_shared<SocketBridge>(); }

} // namespace nkos::rustdesk
