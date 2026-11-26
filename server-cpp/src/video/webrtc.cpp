#include "video/webrtc.hpp"

#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"
#include "http/server.hpp"
#include "http/websocket.hpp"
#include "system/failsafe.hpp"
#include "video/capture_status.hpp"
#include "video/encoder.hpp"
#include "video/audio.hpp"
#include "video/hdmi.hpp"
#include "video/rtc_common.hpp"
#include "video/source.hpp"
#include "video/dtls_seal.hpp"
#include "video/srtp_frame.hpp"

#include <glaze/glaze.hpp>
#include <libwebsockets.h>
#include <rtc/rtc.hpp>

#include <arpa/inet.h>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <deque>
#include <format>
#include <future>
#include <map>
#include <mutex>
#include <netinet/in.h>
#include <openssl/rand.h>
#include <set>
#include <sys/socket.h>
#include <thread>

namespace nkos::video::webrtc {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

// The Main Profile with constraint_set1 at Level 4.2 the encoder emits (SPS 4d 40 2a): a strict receiver rejects
// a stream whose answer names another profile.
constexpr const char *h264_profile = "4d402a";
// An RTP packet of at most 1216 bytes (with a 16-byte SRTP tag, UDP and IPv6: 1280, Tailscale's IPv6 MTU).
constexpr std::size_t rtp_mtu = 1216;
constexpr std::size_t rtp_header = 12;
constexpr std::size_t max_signaling = 64 << 10;
// The writer's queue: 32 frames and 2 MiB of video (Wi-Fi stalls); beyond, it drops all and waits for a key frame.
constexpr std::size_t queue_frames = 32, queue_bytes = 2u << 20;
// The most the capture hands over as one frame (its 64 MiB of storage and the packetizer's headroom).
constexpr std::size_t single_frame_bytes = (64u << 20) + 9;
constexpr auto keyframe_interval = 1s, keyframe_max_interval = 8s;

struct Message {
	std::string event;
	std::string data;
};
struct CandidateInit {
	std::string candidate;
	std::optional<std::string> sdpMid;
	std::optional<int> sdpMLineIndex;
};
struct SessionDescription {
	std::string type;
	std::string sdp;
};
constexpr glz::opts lenient{.error_on_unknown_keys = false};

std::string message_json(const std::string &event, const std::string &data)
{
	return "{\"event\":" + gojson::quote(event) + ",\"data\":" + gojson::quote(data) + "}";
}

// The device address the page's signalling arrived at: the media stays on it (icelocal). Empty for a loopback one
// (a local proxy relays a browser elsewhere): every address then.
std::string local_address(const http::Request &req)
{
	if (!req.wsi) return "";
	lws *net = lws_get_network_wsi(req.wsi);
	int fd = lws_get_socket_fd(net ? net : req.wsi);
	sockaddr_storage addr{};
	socklen_t len = sizeof addr;
	if (fd < 0 || ::getsockname(fd, reinterpret_cast<sockaddr *>(&addr), &len) != 0) return "";
	char text[INET6_ADDRSTRLEN] = {};
	if (addr.ss_family == AF_INET) {
		auto *a = reinterpret_cast<sockaddr_in *>(&addr);
		if ((ntohl(a->sin_addr.s_addr) >> 24) == 127) return "";
		inet_ntop(AF_INET, &a->sin_addr, text, sizeof text);
	} else if (addr.ss_family == AF_INET6) {
		auto *a = reinterpret_cast<sockaddr_in6 *>(&addr);
		if (IN6_IS_ADDR_LOOPBACK(&a->sin6_addr)) return "";
		if (IN6_IS_ADDR_V4MAPPED(&a->sin6_addr)) {
			if (a->sin6_addr.s6_addr[12] == 127) return "";
			inet_ntop(AF_INET, &a->sin6_addr.s6_addr[12], text, sizeof text);
		} else {
			inet_ntop(AF_INET6, &a->sin6_addr, text, sizeof text);
		}
	}
	return text;
}

// One peer's video sending: a bounded queue drained by a thread of its own, so that packetizing and SRTP for one
// peer never hold the loop or another peer.
class Writer {
public:
	explicit Writer(std::shared_ptr<rtc::Track> track) : track_(std::move(track))
	{
		thread_ = std::thread([this] { run(); });
	}
	~Writer()
	{
		close();
		thread_.join();
	}
	// Nothing more is sent after the frame in flight, if any. Does not wait. Loop thread.
	void close()
	{
		{
			std::lock_guard lock(mutex_);
			closed_ = true;
			queue_.clear();
			queued_bytes_ = 0;
		}
		cv_.notify_one();
	}
	// The viewer paused: what is queued goes, and the next thing sent is a key frame, as for a new viewer. The
	// writer itself stays with its track, so its thread and the track's media time are the same ones afterwards.
	void suspend()
	{
		std::lock_guard lock(mutex_);
		queue_.clear();
		queued_bytes_ = 0;
		repairing_ = true;
		keyframe_wait_ = {};
	}
	// Loop thread. Returns whether a key frame should be requested now.
	bool offer(const Frame &frame)
	{
		std::lock_guard lock(mutex_);
		if (closed_) return false;
		if (repairing_ && !frame.key()) return keyframe_due();
		if (enqueue(frame)) return false;
		// The peer cannot keep up: drop the queue and wait for a key frame (the browser sees no RTP gap and
		// sends no PLI, so the server asks). A key frame starts the emptied queue itself, also one larger than
		// the queue's bytes: refusing it would leave the viewer waiting for a repair that every large key frame
		// fails the same way.
		queue_.clear();
		queued_bytes_ = 0;
		repairing_ = true;
		if (frame.key() && enqueue(frame)) return false;
		return keyframe_due();
	}

private:
	// One frame beyond the queue's bytes is taken only by an empty queue; the next waits for it to drain.
	bool enqueue(const Frame &frame)
	{
		if (frame.size > single_frame_bytes || queue_.size() >= queue_frames) return false;
		if (!queue_.empty() && queued_bytes_ + frame.size > queue_bytes) return false;
		queue_.push_back(frame);
		queued_bytes_ += frame.size;
		repairing_ = false;
		if (frame.key()) keyframe_wait_ = {};
		cv_.notify_one();
		return true;
	}
	// The track's media time in microseconds. The capture counts from the start of its session, and the session
	// ends when the last viewer pauses: the next one counts from zero again while the track and its SSRC stay. A
	// timestamp that does not advance continues from the previous frame by the time that passed since it.
	std::int64_t media_time(std::int64_t capture)
	{
		auto now = Clock::now();
		if (!have_time_) {
			have_time_ = true;
			media_time_ = capture;
		} else {
			std::int64_t step = capture - last_capture_;
			if (step <= 0)
				step = std::max<std::int64_t>(1, std::chrono::duration_cast<std::chrono::microseconds>(now - last_frame_at_).count());
			media_time_ += step;
		}
		last_capture_ = capture;
		last_frame_at_ = now;
		return media_time_;
	}
	// The first request at once, then after 1, 2, 4 and every 8 s while it keeps waiting: a peer that stays stalled
	// must not make every viewer of the shared encoder pay for a key frame each second.
	bool keyframe_due()
	{
		auto now = Clock::now();
		if (keyframe_wait_ != Clock::duration{} && now - last_request_ < keyframe_wait_) return false;
		keyframe_wait_ = keyframe_wait_ == Clock::duration{} ? std::chrono::duration_cast<Clock::duration>(keyframe_interval)
		                                                     : std::min<Clock::duration>(keyframe_wait_ * 2, keyframe_max_interval);
		last_request_ = now;
		return true;
	}
	void run()
	{
		while (true) {
			Frame frame;
			{
				std::unique_lock lock(mutex_);
				cv_.wait(lock, [this] { return closed_ || !queue_.empty(); });
				if (closed_) return;
				frame = std::move(queue_.front());
				queue_.pop_front();
				queued_bytes_ -= frame.size;
			}
			try {
				// Incomplete frames belong to this writer. The ICE batch counter is shared by all viewers.
				if (!track_->sendFrame(reinterpret_cast<const rtc::byte *>(frame.data()), frame.size,
				                       rtc::FrameInfo(std::chrono::duration<double>(static_cast<double>(media_time(frame.timestamp)) / 1e6))))
					++incomplete_;
				if (auto now = Clock::now(); incomplete_ && now - last_incomplete_log_ >= 10s) {
					log::warn("webrtc video: {} incomplete frames for this viewer since its last report "
					          "({} unsent batch datagrams across all viewers since process start)",
					          incomplete_, rtc::UnsentMediaDatagrams());
					incomplete_ = 0;
					last_incomplete_log_ = now;
				}
			} catch (const std::exception &e) {
				// At most one line per 10 s, with the count of the failures since the last one: a failing peer must
				// not flood the log, and later failures must not vanish either.
				++failures_;
				auto now = Clock::now();
				if (now - last_failure_log_ >= 10s) {
					log::warn("webrtc video write: {} ({} failed frames since the last report)", e.what(), failures_);
					failures_ = 0;
					last_failure_log_ = now;
				}
			}
		}
	}
	std::shared_ptr<rtc::Track> track_;
	std::thread thread_;
	std::mutex mutex_;
	std::condition_variable cv_;
	std::deque<Frame> queue_;
	std::size_t queued_bytes_ = 0;
	bool closed_ = false, repairing_ = true;
	Clock::time_point last_request_{};
	Clock::duration keyframe_wait_{};
	// Writer thread only.
	bool have_time_ = false;
	std::int64_t media_time_ = 0, last_capture_ = 0;
	Clock::time_point last_frame_at_{};
	Clock::time_point last_failure_log_{};
	long failures_ = 0;
	Clock::time_point last_incomplete_log_{};
	long incomplete_ = 0;
};

struct Peer;

// The viewers on the shared stream (one codec at a time), as the Go WebRTCManager.
struct Manager : Subscriber {
	std::set<Peer *> peers;
	std::optional<Codec> codec;
	std::optional<std::string> add(Peer *peer);
	void remove(Peer *peer);
	void frame(const Frame &frame) override;
	void ended() override;
};
Manager manager;

// Every peer alive, for stop(). Loop thread only.
std::set<Peer *> live;

struct Peer : http::WsHandler, std::enable_shared_from_this<Peer> {
	http::WebSocket *ws = nullptr;
	Codec codec = default_codec;
	std::string bind_address;
	std::shared_ptr<rtc::PeerConnection> pc;
	std::shared_ptr<rtc::Track> video;
	std::unique_ptr<Writer> writer;
	// USB audio: the track of the page's audio m-line, the listener while audio runs, the RTP clock.
	std::shared_ptr<rtc::Track> audio_track;
	std::unique_ptr<audio::Listener> audio_listener;
	bool audio_session = false;
	std::uint32_t audio_ts = rtc_common::random_u32();
	std::uint64_t audio_last = 0;
	Clock::time_point audio_last_at, audio_last_sent;
	bool audio_have_last = false;
	bool connected = false, paused = false, viewer = false, done = false;

	Peer() { live.insert(this); }
	~Peer()
	{
		drop_writer();
		live.erase(this);
	}

	// The track is retired. The writer is told to stop here, at once; its destructor joins its thread, which may
	// be inside a send that waits for the socket (up to 500 ms), so that part runs on a pool thread.
	void drop_writer()
	{
		if (!writer) return;
		writer->close();
		loop::blocking([w = std::shared_ptr<Writer>(std::move(writer))] {});
	}

	void send(const std::string &event, const std::string &data)
	{
		if (ws && !ws->closing()) ws->send(http::make_message(message_json(event, data), false));
	}

	void opened(http::WebSocket &socket) override
	{
		ws = &socket;
		ws->max_message = max_signaling;
		auto [servers, json] = rtc_common::ice_servers();
		// The MTU: the SRTP tag, UDP and IPv6 around the RTP packet.
		try {
			pc = std::make_shared<rtc::PeerConnection>(rtc_common::configuration(servers, bind_address, rtp_mtu + 16 + 8 + 40));
		} catch (const std::exception &e) {
			log::error("failed to create webrtc peer connection: {}", e.what());
			ws->close(1011, "");
			return;
		}
		std::weak_ptr<Peer> weak = shared_from_this();
		pc->onLocalDescription([weak](rtc::Description description) {
			std::string sdp = std::string(description);
			std::string type = description.typeString();
			loop::post([weak, sdp, type] {
				auto p = weak.lock();
				if (!p || p->done) return;
				p->send("video-answer", "{\"type\":" + gojson::quote(type) + ",\"sdp\":" + gojson::quote(sdp) + "}");
			});
		});
		pc->onLocalCandidate([weak](rtc::Candidate candidate) {
			std::string json = "{\"candidate\":" + gojson::quote(std::string(candidate)) + ",\"sdpMid\":" +
			                   gojson::quote(candidate.mid()) + ",\"sdpMLineIndex\":0,\"usernameFragment\":null}";
			loop::post([weak, json] {
				if (auto p = weak.lock(); p && !p->done) p->send("video-candidate", json);
			});
		});
		pc->onIceStateChange([weak](rtc::PeerConnection::IceState state) {
			bool on = state == rtc::PeerConnection::IceState::Connected || state == rtc::PeerConnection::IceState::Completed;
			loop::post([weak, on] {
				auto p = weak.lock();
				if (!p || p->done) return;
				p->connected = on;
				p->sync_viewer();
			});
		});
		send("ice-servers", json);
	}

	// Picks the page's payload type for the stream's codec and trims the answer to it.
	bool prepare_video()
	{
		if (!video) return false;
		auto description = video->description();
		std::optional<int> chosen, fallback;
		for (int pt : description.payloadTypes()) {
			auto *map = description.rtpMap(pt);
			if (!map) continue;
			std::string format = map->format;
			for (auto &c : format) c = static_cast<char>(std::toupper(static_cast<unsigned char>(c)));
			std::string fmtp;
			for (auto &f : map->fmtps) fmtp += f + ";";
			if (codec == Codec::h264 && format == "H264" && fmtp.find("packetization-mode=1") != std::string::npos) {
				// The Main profile (profile_idc 0x4d) first, any other with packetization mode 1 as the fallback.
				auto at = fmtp.find("profile-level-id=");
				if (at != std::string::npos && fmtp.compare(at + 17, 2, "4d") == 0 && !chosen) chosen = pt;
				else if (!fallback) fallback = pt;
			} else if (codec == Codec::h265 && format == "H265" && !chosen) {
				chosen = pt;
			}
		}
		if (!chosen) chosen = fallback;
		if (!chosen) return false;
		for (int pt : description.payloadTypes())
			if (pt != *chosen) description.removeFormat(std::to_string(pt));
		if (codec == Codec::h264) {
			auto *map = description.rtpMap(*chosen);
			map->fmtps.clear();
			map->fmtps.push_back(std::string("level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=") + h264_profile);
		}
		description.setDirection(rtc::Description::Direction::SendOnly);
		std::uint32_t ssrc = 0;
		RAND_bytes(reinterpret_cast<unsigned char *>(&ssrc), sizeof ssrc);
		description.addSSRC(ssrc, "video", "nanokvm", "video");
		video->setDescription(description);
		auto rtp = std::make_shared<rtc::RtpPacketizationConfig>(ssrc, "video", static_cast<std::uint8_t>(*chosen), 90000);
		std::shared_ptr<rtc::MediaHandler> packetizer;
		std::size_t fragment = rtp_mtu - rtp_header;
		if (codec == Codec::h264)
			packetizer = std::make_shared<rtc::H264RtpPacketizer>(rtc::NalUnit::Separator::StartSequence, rtp, fragment);
		else
			packetizer = std::make_shared<rtc::H265RtpPacketizer>(rtc::NalUnit::Separator::StartSequence, rtp, fragment);
		packetizer->addToChain(std::make_shared<rtc::RtcpSrReporter>(rtp));
		packetizer->addToChain(std::make_shared<rtc::RtcpNackResponder>());
		packetizer->addToChain(std::make_shared<rtc::PliHandler>([] { loop::post([] { request_keyframe(); }); }));
		video->setMediaHandler(packetizer);
		return true;
	}

	void offer(const std::string &data)
	{
		SessionDescription d;
		if (glz::read<lenient>(d, data)) {
			log::error("failed to unmarshal video offer");
			return;
		}
		if (pc->signalingState() != rtc::PeerConnection::SignalingState::Stable) {
			log::error("video signaling is not stable");
			return;
		}
		try {
			pc->setRemoteDescription(rtc::Description(d.sdp, d.type));
		} catch (const std::exception &e) {
			log::error("failed to set remote description: {}", e.what());
			return;
		}
		// The page's video transceiver (the first video m-line): libdatachannel made a track for it; addTrack with
		// its mid returns that track, answered sendonly at the stream's codec by prepare_video.
		if (!video) {
			auto remote = pc->remoteDescription();
			for (int i = 0; remote && i < remote->mediaCount(); i++) {
				auto entry = remote->media(i);
				if (auto *media = std::get_if<rtc::Description::Media *>(&entry); media && (*media)->type() == "video") {
					auto reciprocated = (*media)->reciprocate();
					video = pc->addTrack(reciprocated);
					break;
				}
			}
		}
		attach_audio();
		if (!prepare_video()) {
			log::error("failed to create answer: no {} in the offer", codec_name(codec));
			return;
		}
		try {
			pc->setLocalDescription(rtc::Description::Type::Answer);
		} catch (const std::exception &e) {
			log::error("failed to create answer: {}", e.what());
		}
	}

	void candidate(const std::string &data)
	{
		CandidateInit c;
		if (glz::read<lenient>(c, data)) {
			log::error("failed to unmarshal candidate");
			return;
		}
		if (c.candidate.empty()) return;
		try {
			pc->addRemoteCandidate(rtc::Candidate(c.candidate, c.sdpMid.value_or("0")));
		} catch (const std::exception &e) {
			log::error("failed to add ICECandidate: {}", e.what());
		}
	}

	// A viewer while connected and not paused (a hidden tab pauses).
	void sync_viewer()
	{
		bool want = connected && !paused && video;
		if (want == viewer) return;
		if (want) {
			if (auto error = manager.add(this)) {
				send("video-error", *error);
				close_all();
				return;
			}
			// One writer for the track's life: a second one on the same track would share its packetizer
			// with a send still in flight, and would start the media time again.
			if (!writer) writer = std::make_unique<Writer>(video);
			viewer = true;
		} else {
			manager.remove(this);
			viewer = false;
			// Joined again later like a new viewer, with a key frame.
			if (writer) writer->suspend();
		}
	}

	void received(http::WebSocket &, std::string_view text, bool) override
	{
		Message m;
		if (glz::read<lenient>(m, std::string(text))) {
			log::error("failed to unmarshal message");
			return;
		}
		if (m.event == "video-offer") offer(m.data);
		else if (m.event == "video-candidate") candidate(m.data);
		else if (m.event == "heartbeat") send("heartbeat", "");
		else if (m.event == "video-pause") {
			paused = true;
			sync_viewer();
		} else if (m.event == "video-resume") {
			paused = false;
			sync_viewer();
		} else if (m.event == "audio-start") {
			if (auto error = start_audio()) send("audio-error", *error);
			else send("audio-started", "");
		} else if (m.event == "audio-stop") {
			stop_audio();
			send("audio-stopped", "");
		} else {
			log::debug("Unhandled message event: {}", m.event);
		}
	}

	// The page's audio transceiver, answered sendonly with the shared encoder's Opus; nothing is sent before
	// audio-start.
	void attach_audio()
	{
		if (audio_track) return;
		auto remote = pc->remoteDescription();
		for (int i = 0; remote && i < remote->mediaCount(); i++) {
			auto entry = remote->media(i);
			auto *media = std::get_if<rtc::Description::Media *>(&entry);
			if (!media || (*media)->type() != "audio" || (*media)->direction() == rtc::Description::Direction::Inactive) continue;
			auto reciprocated = (*media)->reciprocate();
			auto pt = rtc_common::find_payload(reciprocated, "opus", {});
			if (!pt) return;
			std::uint32_t ssrc = rtc_common::random_u32();
			rtc_common::trim_to(reciprocated, *pt, std::string(audio::opus_fmtp), ssrc, "audio", "nanokvm");
			audio_track = pc->addTrack(reciprocated);
			auto rtp = std::make_shared<rtc::RtpPacketizationConfig>(ssrc, "audio", static_cast<std::uint8_t>(*pt), audio::sample_rate);
			auto packetizer = std::make_shared<rtc::OpusRtpPacketizer>(rtp);
			packetizer->addToChain(std::make_shared<rtc::RtcpSrReporter>(rtp));
			audio_track->setMediaHandler(packetizer);
			return;
		}
	}

	std::optional<std::string> start_audio()
	{
		if (!audio_track) return std::string("audio was not negotiated on this connection");
		if (audio_listener) return std::nullopt;
		if (!audio::acquire_session()) return std::string("too many audio sessions");
		std::string error;
		audio_listener = audio::listen(error);
		if (!audio_listener) {
			audio::release_session();
			return error;
		}
		audio_session = true;
		// A restart continues the RTP clock over the time without sound.
		if (audio_last_sent != Clock::time_point{}) {
			auto gap = Clock::now() - audio_last_sent - audio::frame_duration;
			if (gap > Clock::duration::zero())
				audio_ts += static_cast<std::uint32_t>(std::chrono::duration_cast<std::chrono::microseconds>(gap).count() *
				                                       audio::sample_rate / 1000000);
		}
		audio_have_last = false;
		audio_listener->packet = [this](const audio::Packet &p) { send_audio(p); };
		audio_listener->ended = [this] {
			loop::post([gone = audio_listener.release()] { delete gone; });
			if (audio_session) audio::release_session();
			audio_session = false;
		};
		return std::nullopt;
	}

	void send_audio(const audio::Packet &p)
	{
		std::uint64_t dropped = 0;
		if (audio_have_last && p.index > audio_last + 1) dropped = std::min<std::uint64_t>(p.index - audio_last - 1, 65535);
		if (audio_have_last) {
			auto gap = audio::pause_gap(p.at - audio_last_at, dropped);
			audio_ts += static_cast<std::uint32_t>(std::chrono::duration_cast<std::chrono::microseconds>(gap).count() *
			                                       audio::sample_rate / 1000000);
		}
		audio_ts += static_cast<std::uint32_t>(dropped * 960);
		try {
			audio_track->sendFrame(reinterpret_cast<const rtc::byte *>(p.data->data()), p.data->size(), rtc::FrameInfo(audio_ts));
		} catch (const std::exception &e) {
			log::debug("audio track write: {}", e.what());
			return stop_audio();
		}
		audio_ts += 960;
		audio_last = p.index;
		audio_last_at = p.at;
		audio_have_last = true;
		audio_last_sent = Clock::now();
	}

	void stop_audio()
	{
		audio_listener.reset();
		if (audio_session) audio::release_session();
		audio_session = false;
	}

	// now: the server is stopping; the PeerConnection closes here, not on a blocking thread.
	void close_all(bool now = false)
	{
		if (done) return;
		done = true;
		stop_audio();
		if (viewer) {
			manager.remove(this);
			viewer = false;
		}
		drop_writer();
		if (pc) {
			auto c = std::move(pc);
			auto close = [c] {
				try {
					c->close();
				} catch (...) {
				}
			};
			// Closing waits for libdatachannel's threads: not on the loop.
			if (now) close();
			else loop::blocking(close);
		}
		video.reset();
		if (ws && !ws->closing()) ws->close(1000, "");
	}

	void closed(http::WebSocket &) override
	{
		ws = nullptr;
		close_all();
	}

	// The stream ended under the viewer (another codec, H.264 blocked): tell the page and end.
	void ended()
	{
		viewer = false;
		drop_writer();
		send("video-error", "encoder-reconfigured");
		close_all();
	}
};

std::optional<std::string> Manager::add(Peer *peer)
{
	if (!peers.empty() && codec && *codec != peer->codec)
		return std::format("video encoder is already in use with {{Codec:{}}}; requested {{Codec:{}}}",
		                   codec_name(*codec), codec_name(peer->codec));
	if (peers.empty()) {
		if (auto error = subscribe(this, peer->codec)) return error->message;
		codec = peer->codec;
	}
	peers.insert(peer);
	hdmi::set_viewers("webrtc", static_cast<int>(peers.size()));
	return std::nullopt;
}

void Manager::remove(Peer *peer)
{
	if (!peers.erase(peer)) return;
	hdmi::set_viewers("webrtc", static_cast<int>(peers.size()));
	if (peers.empty()) {
		unsubscribe(this);
		codec.reset();
	}
}

void Manager::frame(const Frame &frame)
{
	capture_status::update("h264", frame.result);
	if (frame.result < 0 || frame.size == 0) return;
	bool keyframe = false;
	for (auto *p : peers)
		if (p->writer && p->writer->offer(frame)) keyframe = true;
	if (keyframe) request_keyframe();
}

void Manager::ended()
{
	auto targets = std::vector<Peer *>(peers.begin(), peers.end());
	peers.clear();
	codec.reset();
	hdmi::set_viewers("webrtc", 0);
	for (auto *p : targets) p->ended();
}

} // namespace

void stop()
{
	auto peers = std::vector<Peer *>(live.begin(), live.end());
	for (auto *p : peers) {
		auto keep = p->shared_from_this();
		p->close_all(true);
	}
	// libdatachannel's threads (ICE, DTLS, SCTP, its pool) end before the process does.
	if (rtc::Cleanup().wait_for(std::chrono::seconds(3)) != std::future_status::ready)
		log::warn("webrtc: cleanup did not finish in 3 s");
}

void add_routes(http::Router &router)
{
	// Outbound SRTP of every connection on the CryptoDMA engine, a frame per request; AES-128-GCM DTLS records of a
	// data channel's burst likewise (the RustDesk bridge's connections use the same library).
	srtp_frame::install();
	dtls_seal::install();
	rtc::InitLogger(rtc::LogLevel::Warning, [](rtc::LogLevel level, std::string message) {
		if (level <= rtc::LogLevel::Error) log::error("webrtc: {}", message);
		else log::debug("webrtc: {}", message);
	});
	router.ws("/api/stream/video", auth::guarded_ws([](http::Request &req, http::Response &res) -> std::shared_ptr<http::WsHandler> {
		if (!system::failsafe::require_video(res)) return nullptr;
		auto peer = std::make_shared<Peer>();
		if (std::string name = req.query_value("codec"); !name.empty()) {
			auto parsed = parse_codec(name);
			if (!parsed) {
				res.text(400, std::format("invalid codec \"{}\": expected h264 or h265", name));
				return nullptr;
			}
			peer->codec = *parsed;
		}
		if (std::string rc = req.query_value("rc"); !rc.empty() && rc != "cbr") {
			res.text(400, std::format("invalid rate control \"{}\": only cbr is supported", rc));
			return nullptr;
		}
		peer->bind_address = local_address(req);
		return peer;
	}));
}

} // namespace nkos::video::webrtc
