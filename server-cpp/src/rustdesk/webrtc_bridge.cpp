// webrtc.sock, as the Go server's service/rustdesk webrtc.go, on libdatachannel.
//
// Command connection: u32le-prefixed JSON {"op":"offer","offer":"webrtc://<base64 SDP JSON>"}; the answer event
// {"event":"answer","answer":...,"token":...,"fingerprint":...,"session_key":...} (or {"event":"error"}), then the
// daemon's {"op":"candidate"} commands and the bridge's candidate and connected events. Attach connection:
// {"op":"attach","token":...}; {"event":"ready"} once the data channel is open; then RustDesk-framed messages both
// ways, cut into fragments of at most 60000 bytes (flag byte 1: more follow) on the channel.

#include "rustdesk/bridge_rtc.hpp"
#include "rustdesk/bridge_socket.hpp"
#include "rustdesk/bridge_wire.hpp"

#include "common/base64.hpp"
#include "common/log.hpp"
#include "usb/gojson_read.hpp"
#include "video/rtc_common.hpp"

#include <openssl/rand.h>
#include <rtc/rtc.hpp>

#include <atomic>
#include <condition_variable>
#include <cstring>
#include <deque>
#include <map>
#include <mutex>
#include <set>

namespace nkos::rustdesk {

namespace {

using namespace std::chrono_literals;
using sock::Clock;
using sock::Conn;
using sock::no_deadline;

constexpr std::size_t max_sessions = 4, max_connections = 12, max_queued_inbound = 512u << 10, event_capacity = 32,
                      incoming_capacity = 32;
// What may wait in the data channel's own queue before the bridge stops reading the daemon: one fragment.
constexpr std::size_t max_channel_queue = 64u << 10;

struct Bridge;

struct Session : std::enable_shared_from_this<Session> {
	std::weak_ptr<Bridge> owner;
	std::string token;
	std::shared_ptr<rtc::PeerConnection> pc;
	std::shared_ptr<rtc::DataChannel> channel;
	std::mutex mu;
	std::condition_variable cv;
	bool cancelled = false, opened = false, bound = false;
	std::deque<wire::RtcEvent> events;
	std::deque<std::string> incoming;
	std::size_t queued_bytes = 0;
	std::string fragments;
	std::atomic<bool> attached{false};
	std::once_flag once;

	void close();
	// notify: an event for the command connection; a full queue ends the session.
	void notify(wire::RtcEvent e)
	{
		std::unique_lock lock(mu);
		if (cancelled) return;
		if (events.size() >= event_capacity) {
			lock.unlock();
			return close();
		}
		events.push_back(std::move(e));
		cv.notify_all();
	}
};

struct Bridge : std::enable_shared_from_this<Bridge> {
	std::mutex mu;
	bool closed = false;
	std::map<std::string, std::shared_ptr<Session>> sessions;
	std::set<std::shared_ptr<Conn>> connections;
	std::unique_ptr<sock::Listener> listener;

	void serve(Conn &c);
	void attach(Conn &c, const std::string &token);
	std::shared_ptr<Session> answer(const std::string &endpoint, std::string &answer, std::string &fingerprint,
	                                std::string &key, std::string &error);
};

void Session::close()
{
	std::call_once(once, [this] {
		{
			std::lock_guard lock(mu);
			cancelled = true;
			cv.notify_all();
		}
		// Teardown off libdatachannel's callback threads; the slot is kept until it finishes.
		std::thread([self = shared_from_this()] {
			if (self->channel) self->channel->close();
			if (self->pc) self->pc->close();
			if (auto b = self->owner.lock()) {
				std::lock_guard lock(b->mu);
				b->sessions.erase(self->token);
			}
		}).detach();
	});
}

bool read_json(Conn &c, std::string &out, Clock::time_point deadline)
{
	unsigned char prefix[4];
	if (!c.read_full(prefix, 4, deadline)) return false;
	std::uint32_t n = prefix[0] | prefix[1] << 8 | prefix[2] << 16 | static_cast<std::uint32_t>(prefix[3]) << 24;
	if (n == 0 || n > wire::rtc_max_signal) return false;
	out.resize(n);
	return c.read_full(out.data(), n, deadline);
}

bool write_event(Conn &c, const wire::RtcEvent &e, Clock::time_point deadline)
{
	std::string data = wire::rtc_event_json(e);
	if (data.size() > wire::rtc_max_signal) return false;
	return c.write_all(wire::length_prefix(data.size()) + data, deadline);
}

// webrtc.ICECandidateInit of a candidate command.
struct CandidateInit {
	std::string candidate;
	std::optional<std::string> mid;
};
bool parse_candidate(const std::string &json, CandidateInit &out)
{
	std::string error;
	return usb::goj::unmarshal(json, "", [&out](usb::goj::Reader &v) {
		v.object("", "", {{"candidate", [&out](usb::goj::Reader &x) { x.string(out.candidate); }},
		                  {"sdpMid", [&out](usb::goj::Reader &x) {
			                   if (x.is_null()) return x.skip();
			                   std::string mid;
			                   x.string(mid);
			                   out.mid = mid;
		                   }},
		                  {"sdpMLineIndex", [](usb::goj::Reader &x) {
			                   if (x.is_null()) return x.skip();
			                   long long index = 0;
			                   x.integer(index, "uint16");
		                   }},
		                  {"usernameFragment", [](usb::goj::Reader &x) {
			                   if (x.is_null()) return x.skip();
			                   std::string f;
			                   x.string(f);
		                   }}});
	}, error);
}

void Bridge::serve(Conn &c)
{
	auto deadline = Clock::now() + 3s;
	std::string json;
	wire::RtcCommand command;
	if (!read_json(c, json, deadline) || !wire::parse_rtc_command(json, command)) return;
	if (command.op == "attach") return attach(c, command.token);
	if (command.op != "offer") return;
	std::string answer_endpoint, fingerprint, key, error;
	auto s = answer(command.offer, answer_endpoint, fingerprint, key, error);
	if (!s) {
		(void)write_event(c, {.event = "error", .error = error}, deadline);
		return;
	}
	struct Close {
		std::shared_ptr<Session> s;
		~Close() { s->close(); }
	} close{s};
	if (!write_event(c, {.event = "answer", .answer = answer_endpoint, .token = s->token, .fingerprint = fingerprint,
	                     .session_key = key},
	                 deadline))
		return;
	// The daemon's candidates; anything else (or the end of the connection) ends the session's signalling.
	auto done = std::make_shared<bool>(false);
	std::thread reader([&c, s, done] {
		for (;;) {
			std::string j;
			wire::RtcCommand cmd;
			if (!read_json(c, j, no_deadline) || !wire::parse_rtc_command(j, cmd)) break;
			if (cmd.op != "candidate" || cmd.candidate.size() > 8192) break;
			CandidateInit init;
			if (!parse_candidate(cmd.candidate, init)) break;
			try {
				s->pc->addRemoteCandidate(rtc::Candidate(init.candidate, init.mid.value_or("0")));
			} catch (const std::exception &) {
				break;
			}
		}
		std::lock_guard lock(s->mu);
		*done = true;
		s->cv.notify_all();
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
	// Until the channel opens the session has 20 s.
	auto setup = Clock::now() + 20s;
	bool connected = false;
	for (;;) {
		wire::RtcEvent e;
		{
			std::unique_lock lock(s->mu);
			auto wake = [&] { return !s->events.empty() || s->cancelled || *done; };
			bool ready = true;
			if (connected) s->cv.wait(lock, wake);
			else ready = s->cv.wait_until(lock, setup, wake);
			if (!ready || s->cancelled || *done) return;
			e = std::move(s->events.front());
			s->events.pop_front();
		}
		if (e.event == "connected") connected = true;
		if (!write_event(c, e, Clock::now() + 3s)) return;
	}
}

std::shared_ptr<Session> Bridge::answer(const std::string &endpoint, std::string &answer_out, std::string &fingerprint,
                                        std::string &key, std::string &error)
{
	std::string offer_sdp;
	if (!wire::decode_offer(endpoint, offer_sdp, error)) return nullptr;
	if (!wire::rtc_fingerprint(offer_sdp, key, error)) return nullptr;
	auto s = std::make_shared<Session>();
	{
		std::lock_guard lock(mu);
		if (closed || sessions.size() >= max_sessions) {
			error = "too many pending WebRTC sessions";
			return nullptr;
		}
		unsigned char raw[32];
		if (RAND_bytes(raw, sizeof raw) != 1) {
			error = "random token unavailable";
			return nullptr;
		}
		static const char hex[] = "0123456789abcdef";
		for (unsigned char b : raw) s->token += hex[b >> 4], s->token += hex[b & 15];
		s->owner = weak_from_this();
		sessions[s->token] = s;
	}
	auto failed = [&](std::string why) {
		error = std::move(why);
		s->close();
		return nullptr;
	};
	try {
		auto config = video::rtc_common::configuration(video::rtc_common::ice_servers().first, "", 0);
		config.maxMessageSize = 65536;
		s->pc = std::make_shared<rtc::PeerConnection>(config);
	} catch (const std::exception &e) {
		return failed(e.what());
	}
	std::weak_ptr<Session> weak = s;
	s->pc->onLocalCandidate([weak](rtc::Candidate candidate) {
		if (auto locked = weak.lock()) locked->notify({.event = "candidate", .candidate = video::rtc_common::candidate_json(candidate)});
	});
	s->pc->onStateChange([weak](rtc::PeerConnection::State state) {
		if (state == rtc::PeerConnection::State::Failed || state == rtc::PeerConnection::State::Closed ||
		    state == rtc::PeerConnection::State::Disconnected)
			if (auto locked = weak.lock()) locked->close();
	});
	s->pc->onDataChannel([weak](std::shared_ptr<rtc::DataChannel> dc) {
		auto locked = weak.lock();
		if (!locked) return dc->close();
		auto r = dc->reliability();
		{
			std::lock_guard lock(locked->mu);
			if (r.unordered || r.maxRetransmits || r.maxPacketLifeTime || locked->bound) {
				dc->close();
				return;
			}
			locked->bound = true;
			locked->channel = dc;
		}
		auto opened = [weak] {
			auto s = weak.lock();
			if (!s) return;
			{
				std::lock_guard lock(s->mu);
				if (s->opened) return;
				s->opened = true;
				s->cv.notify_all();
			}
			s->notify({.event = "connected"});
		};
		dc->onOpen(opened);
		dc->onClosed([weak] {
			if (auto s = weak.lock()) s->close();
		});
		dc->onMessage(
			[weak](rtc::binary data) {
				auto s = weak.lock();
				if (!s) return;
				std::unique_lock lock(s->mu);
				std::string payload;
				bool complete = false;
				if (!wire::append_rtc_fragment(s->fragments,
				                               {reinterpret_cast<const char *>(data.data()), data.size()}, payload, complete)) {
					lock.unlock();
					return s->close();
				}
				if (!complete) return;
				s->queued_bytes += payload.size();
				if (s->queued_bytes > max_queued_inbound || s->incoming.size() >= incoming_capacity) {
					lock.unlock();
					return s->close();
				}
				s->incoming.push_back(std::move(payload));
				s->cv.notify_all();
			},
			[weak](rtc::string) {
				if (auto s = weak.lock()) s->close();
			});
		if (dc->isOpen()) opened();
	});
	try {
		s->pc->setRemoteDescription(rtc::Description(offer_sdp, rtc::Description::Type::Offer));
		s->pc->setLocalDescription(rtc::Description::Type::Answer);
	} catch (const std::exception &e) {
		return failed(e.what());
	}
	auto local = s->pc->localDescription();
	if (!local) return failed("no local description");
	std::string answer_sdp = std::string(*local);
	if (!wire::rtc_fingerprint(answer_sdp, fingerprint, error)) return failed(error);
	answer_out = "webrtc://" + base64::encode(video::rtc_common::description_json("answer", answer_sdp));
	return s;
}

void Bridge::attach(Conn &c, const std::string &token)
{
	std::shared_ptr<Session> s;
	{
		std::lock_guard lock(mu);
		if (auto it = sessions.find(token); it != sessions.end()) s = it->second;
	}
	bool expected = false;
	if (!s || !s->attached.compare_exchange_strong(expected, true)) return;
	struct Close {
		std::shared_ptr<Session> s;
		~Close() { s->close(); }
	} close{s};
	{
		std::unique_lock lock(s->mu);
		if (!s->cv.wait_for(lock, 20s, [&] { return s->opened || s->cancelled; }) || s->cancelled) return;
	}
	if (!write_event(c, {.event = "ready"}, Clock::now() + 3s)) return;
	// The daemon's messages onto the channel, in fragments, while the channel's queue has room.
	std::thread sender([&c, s] {
		// One buffer for every fragment: the daemon's bytes are read into it behind the flag byte, and the channel
		// copies the fragment into its own message (no copy of the whole message first, no zero fill).
		std::vector<std::byte> fragment(1 + wire::rtc_max_fragment);
		for (;;) {
			std::uint8_t head[4];
			if (!c.read_byte(head[0], no_deadline)) break;
			// The daemon has 3 s in all to deliver one message, its header and every fragment: only the time spent
			// in its reads is taken from that. Waiting for the channel and sending fall between the reads and are
			// not the daemon's time, so they do not count; nor does each fragment get a deadline of its own, which
			// would give a message of 140 fragments minutes.
			Clock::duration budget = 3s;
			auto read_within_budget = [&c, &budget](void *out, std::size_t n) {
				auto start = Clock::now();
				bool ok = c.read_full(out, n, start + budget);
				budget -= std::min(budget, Clock::now() - start);
				return ok;
			};
			std::size_t width = wire::rustdesk_header_width(head[0]), length = 0;
			if (!read_within_budget(head + 1, width - 1) || !wire::rustdesk_frame_length(head, width, length)) break;
			bool failed = false;
			for (std::size_t offset = 0; offset < length || offset == 0;) {
				std::size_t end = std::min(offset + wire::rtc_max_fragment, length);
				fragment[0] = std::byte{static_cast<unsigned char>(end == length ? 0 : 1)};
				if (!read_within_budget(fragment.data() + 1, end - offset)) return s->close();
				auto wait_until = Clock::now() + 3s;
				while (s->channel->bufferedAmount() > max_channel_queue) {
					{
						std::unique_lock lock(s->mu);
						if (s->cv.wait_for(lock, 5ms, [&] { return s->cancelled; })) return;
					}
					if (Clock::now() > wait_until) return s->close();
				}
				try {
					s->channel->send(fragment.data(), 1 + end - offset);
				} catch (const std::exception &) {
					failed = true;
					break;
				}
				if (end == length) break;
				offset = end;
			}
			if (failed) break;
		}
		s->close();
	});
	struct Join {
		Conn &c;
		std::thread &t;
		~Join()
		{
			c.shutdown();
			t.join();
		}
	} join{c, sender};
	for (;;) {
		std::string payload;
		{
			std::unique_lock lock(s->mu);
			s->cv.wait(lock, [&] { return !s->incoming.empty() || s->cancelled; });
			if (s->cancelled) return;
			payload = std::move(s->incoming.front());
			s->incoming.pop_front();
			s->queued_bytes -= payload.size();
		}
		if (!c.write_all(wire::rustdesk_frame_header(payload.size()) + payload, Clock::now() + 3s)) return;
	}
}

struct Handle : RtcBridge {
	std::shared_ptr<Bridge> b;
	void close() override
	{
		std::vector<std::shared_ptr<Session>> all;
		std::vector<std::shared_ptr<Conn>> conns;
		{
			std::lock_guard lock(b->mu);
			b->closed = true;
			for (auto &[token, s] : b->sessions) all.push_back(s);
			conns.assign(b->connections.begin(), b->connections.end());
		}
		for (auto &c : conns) c->shutdown();
		if (b->listener) b->listener->close();
		for (auto &s : all) s->close();
	}
	~Handle() override { close(); }
};

} // namespace

std::unique_ptr<RtcBridge> start_rtc_bridge(int fd, const std::string &path)
{
	auto b = std::make_shared<Bridge>();
	std::weak_ptr<Bridge> weak = b;
	b->listener = std::make_unique<sock::Listener>(fd, path, [weak](std::shared_ptr<Conn> c) {
		auto locked = weak.lock();
		if (!locked) return;
		{
			std::lock_guard lock(locked->mu);
			if (locked->closed || locked->connections.size() >= max_connections) return;
			locked->connections.insert(c);
		}
		std::thread([locked, c] {
			locked->serve(*c);
			c->shutdown();
			std::lock_guard lock(locked->mu);
			locked->connections.erase(c);
		}).detach();
	});
	auto h = std::make_unique<Handle>();
	h->b = b;
	return h;
}

} // namespace nkos::rustdesk
