#include "video/rtsp.hpp"

#include "api/response.hpp"
#include "auth/brute_force.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "config/config.hpp"
#include "system/failsafe.hpp"
#include "video/audio.hpp"
#include "video/hdmi.hpp"
#include "video/source.hpp"

#include <BasicUsageEnvironment.hh>
#include <liveMedia.hh>

#include <glaze/glaze.hpp>
#include <openssl/rand.h>

#include <arpa/inet.h>
#include <fcntl.h>
#include <sys/socket.h>
#include <unistd.h>
#include <sys/time.h>

#include <atomic>
#include <cerrno>
#include <cctype>
#include <cstdint>
#include <map>
#include <set>
#include <climits>
#include <chrono>
#include <cstring>
#include <format>
#include <deque>
#include <fstream>
#include <future>
#include <memory>
#include <mutex>
#include <optional>
#include <sstream>
#include <string>
#include <thread>
#include <vector>

namespace nkos::video::rtsp {
namespace {

using namespace std::chrono_literals;

const std::string settings_file = "/etc/kvm/rtsp.json";
constexpr const char *stream_name = "stream";
constexpr const char *username = "stream";
constexpr const char *realm = "NanoKVM";
constexpr int default_port = 8554;
// Frames waiting for the RTSP thread (a second at 60 fps); past this the clients restart at the next IDR.
constexpr std::size_t max_queued_frames = 60;
// Frames still queued past this (about 200 ms at 60 fps) mean the clients take the stream late rather than losing
// it: they count as lagging for the adaptive bit rate, before the queue overflows.
constexpr std::size_t backlog_lag_frames = 12;
// A client with more than this waiting in its output queue (live555 patch 0002; about 25 ms of a 20 Mbit/s stream)
// takes the stream late. All RTSP clients share one encoder rate, so that is lag for the adaptive bit rate only when
// every client is that far behind: one slow client does not lower the picture of the others. Its queue holds at most
// 2 MiB; past that it is disconnected.
constexpr unsigned backlog_lag_bytes = 64 << 10;

// The smallest output queue of the clients receiving the stream (0 without one): set on the RTSP thread, read on the
// loop thread.
std::atomic<unsigned> least_backlog{0};
// The parameter sets for the SDP come from the first key frame; without a picture DESCRIBE answers without them.
constexpr unsigned sdp_wait_ms = 3000;
constexpr unsigned max_connections = 32;
// RTP packets over the RTSP TCP connection: much larger than a UDP datagram, so a 20 Mbit/s stream takes ~150
// packets a second instead of ~1800 (each two send() calls and live555's per-packet work).
constexpr unsigned rtp_packet_bytes = 16384;
// Opus packets waiting for the RTSP thread (two seconds).
constexpr std::size_t max_queued_audio = 100;

using SteadyClock = std::chrono::steady_clock;

// Presentation times of video and audio on one clock, so players line them up (RTCP sender reports): the loop
// thread's monotonic time, as wall-clock time.
timeval wall_time(SteadyClock::time_point t)
{
	static const auto offset = std::chrono::system_clock::now().time_since_epoch() - SteadyClock::now().time_since_epoch();
	auto us = std::chrono::duration_cast<std::chrono::microseconds>(t.time_since_epoch() + offset).count();
	timeval tv{};
	tv.tv_sec = static_cast<time_t>(us / 1000000);
	tv.tv_usec = static_cast<suseconds_t>(us % 1000000);
	return tv;
}

// ---------------------------------------------------------------------------------------------------------------
// Settings

struct Settings {
	bool enabled = false;
	int port = default_port;
	std::string password;
};

Settings load_settings()
{
	Settings s;
	std::ifstream in(settings_file);
	if (!in) return s;
	std::stringstream text;
	text << in.rdbuf();
	if (auto e = glz::read<glz::opts{.error_on_unknown_keys = false}>(s, text.str())) {
		log::error("rtsp: {} is not valid; the stream stays off", settings_file);
		return Settings{};
	}
	if (s.port < 1 || s.port > 65535) s.port = default_port;
	return s;
}

bool save_settings(const Settings &s)
{
	std::string json;
	(void)glz::write_json(s, json);
	return config::atomic_write(settings_file, json, 0600);
}

// 16 letters and digits: about 95 bits.
bool valid_password(const std::string &p)
{
	if (p.size() < 8 || p.size() > 64) return false;
	for (char c : p)
		if (!std::isalnum(static_cast<unsigned char>(c))) return false;
	return true;
}

std::string new_password();

// The settings to run with: an enabled stream with no valid password gets a new one, saved before the listener
// opens. nullopt with error set when that fails: the stream stays off.
std::optional<Settings> checked_settings(std::string &error)
{
	Settings s = load_settings();
	if (!s.enabled || valid_password(s.password)) return s;
	s.password = new_password();
	if (s.password.empty() || !save_settings(s)) {
		error = "no valid password could be made and saved";
		return std::nullopt;
	}
	log::info("rtsp: the stored password was not valid; a new one was saved");
	return s;
}

std::string new_password()
{
	static constexpr char alphabet[] = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
	constexpr unsigned n = sizeof alphabet - 1;
	std::string out;
	while (out.size() < 16) {
		unsigned char b[32];
		if (RAND_bytes(b, sizeof b) != 1) return {};
		for (unsigned char c : b)
			if (c < 256 / n * n && out.size() < 16) out += alphabet[c % n];
	}
	return out;
}

// Runs fn on the loop thread and waits for its result; nullopt when the loop does not answer (it is stopping).
template <typename T>
std::optional<T> on_loop(std::function<T()> fn)
{
	auto promise = std::make_shared<std::promise<T>>();
	auto future = promise->get_future();
	loop::post([promise, fn = std::move(fn)] { promise->set_value(fn()); });
	if (future.wait_for(2s) != std::future_status::ready) return std::nullopt;
	return future.get();
}

// live555 opens its sockets without O_CLOEXEC: a program the server starts would keep the port and the clients'
// connections open after the RTSP server closed them.
void close_on_exec(int fd)
{
	if (fd >= 0) fcntl(fd, F_SETFD, fcntl(fd, F_GETFD) | FD_CLOEXEC);
}

std::string address_of(const sockaddr_storage &a)
{
	char buf[INET6_ADDRSTRLEN] = {};
	if (a.ss_family == AF_INET)
		inet_ntop(AF_INET, &reinterpret_cast<const sockaddr_in &>(a).sin_addr, buf, sizeof buf);
	else if (a.ss_family == AF_INET6) {
		auto &in6 = reinterpret_cast<const sockaddr_in6 &>(a).sin6_addr;
		if (IN6_IS_ADDR_V4MAPPED(&in6)) inet_ntop(AF_INET, &in6.s6_addr[12], buf, sizeof buf);
		else inet_ntop(AF_INET6, &in6, buf, sizeof buf);
	}
	return buf;
}

// ---------------------------------------------------------------------------------------------------------------
// The device stream, handed from the loop thread to the RTSP thread

class FrameSource;

// One subscription to the device stream for every RTSP client (live555 shares one source among them). Frames are
// queued on the loop thread and taken on the RTSP thread.
class Feed : public Subscriber {
public:
	struct Queued {
		Frame frame;
		SteadyClock::time_point at;  // when it reached the loop thread
	};

	// Its clients' lag counts toward the adaptive bit rate (source.hpp): they are one viewer.
	bool reports_lag() const override { return true; }

	// Loop thread.
	void frame(const Frame &frame) override
	{
		if (frame.result >= 0 && frame.size == 0) return;
		bool overflow = false, lost = false;
		{
			std::lock_guard lock(mutex_);
			if (!scheduler_ || !source_) return;
			// A picture the encoder could not hand over: the next ones refer to it. RTSP clients cannot ask for a
			// key frame, and the encoder sends one only when asked.
			if (frame.result < 0) {
				lost = !want_key_;
				want_key_ = true;
			} else if (want_key_ && !idr(frame)) {
				if (lagging_) report_lag(this);
				return;
			}
		}
		if (lost) {
			lost_frames++;
			request_keyframe();
			return;
		}
		if (frame.result < 0) return;
		{
			std::lock_guard lock(mutex_);
			if (!scheduler_ || !source_) return;
			if (want_key_ && !idr(frame)) return;
			if (queue_.size() >= max_queued_frames) {
				queue_.clear();
				want_key_ = true;
				lagging_ = true;
				overflow = true;
			} else {
				want_key_ = false;
				lagging_ = false;
				queue_.push_back({frame, SteadyClock::now()});
				if (queue_.size() > backlog_lag_frames || least_backlog.load() > backlog_lag_bytes) report_lag(this);
				scheduler_->triggerEvent(event_, this);
			}
		}
		if (overflow) {
			overflows++;
			report_lag(this);
			log::info("rtsp: clients fell behind; they resume at the next key frame");
			request_keyframe();
		}
	}
	// Loop thread: the session ended (the codec changed): the clients' SDP is stale.
	void ended() override
	{
		subscribed_ = false;
		hdmi::set_viewers("rtsp", 0);
		if (restart_clients) restart_clients();
	}

	// Loop thread: a source started or stopped on the RTSP thread.
	void use()
	{
		// A new source waits for an IDR, which the encoder sends only when asked.
		users_++;
		if (!subscribed_) join();
		else request_keyframe();
	}
	void release()
	{
		if (users_ == 0 || --users_ > 0) return;
		if (subscribed_) unsubscribe(this);
		subscribed_ = false;
		hdmi::set_viewers("rtsp", 0);
	}
	bool active() const { return users_ > 0; }

	// RTSP thread.
	void attach(TaskScheduler *scheduler, EventTriggerId event)
	{
		std::lock_guard lock(mutex_);
		scheduler_ = scheduler;
		event_ = event;
	}
	void detach()
	{
		std::lock_guard lock(mutex_);
		scheduler_ = nullptr;
		queue_.clear();
	}
	void set_source(FrameSource *source)
	{
		std::lock_guard lock(mutex_);
		source_ = source;
		queue_.clear();
		want_key_ = true;
		lagging_ = false;
	}
	void clear_source(FrameSource *source)
	{
		std::lock_guard lock(mutex_);
		if (source_ == source) source_ = nullptr;
	}
	FrameSource *source()
	{
		std::lock_guard lock(mutex_);
		return source_;
	}
	std::optional<Queued> take()
	{
		std::lock_guard lock(mutex_);
		if (queue_.empty()) return std::nullopt;
		Queued f = std::move(queue_.front());
		queue_.pop_front();
		return f;
	}

	// Loop thread: since the server started, pictures the encoder lost and queue overflows; either makes the
	// clients wait for a key frame.
	unsigned lost_frames = 0, overflows = 0;

	// Set on the loop thread while the server runs: restarts it, which closes every client connection.
	std::function<void()> restart_clients;

private:
	// Without video (capture off, no device stream) the clients wait: it is tried again every 2 s while they do.
	void join()
	{
		if (users_ == 0 || subscribed_) return;
		std::optional<SubscribeError> error;
		if (system::failsafe::video_disabled()) error = SubscribeError{"video is disabled"};
		else error = subscribe(this, std::nullopt);
		if (error) {
			if (!retrying_) log::info("rtsp: no video for the clients yet: {}", error->message);
			if (!retrying_) {
				retrying_ = true;
				loop::after(std::chrono::seconds(2), [this] {
					retrying_ = false;
					join();
				});
			}
			return;
		}
		subscribed_ = true;
		hdmi::set_viewers("rtsp", 1);
		request_keyframe();
	}

	// The encoder's key frames include the SmartP background pictures, which refer to a long-term picture; a
	// client starts or resumes only at an IDR picture. Loop thread.
	static bool idr(const Frame &f)
	{
		if (!f.key()) return false;
		bool h264 = active_codec() == Codec::h264;
		const unsigned char *p = f.data(), *end = p + f.size;
		for (; p + 3 < end; p++) {
			if (p[0] || p[1] || p[2] != 1) continue;
			unsigned type = h264 ? p[3] & 0x1f : (p[3] >> 1) & 0x3f;
			if (h264 ? type == 5 : type == 19 || type == 20) return true;
			if (h264 ? type >= 1 && type <= 4 : type <= 31) return false;  // another picture's slice
		}
		return false;
	}

	std::mutex mutex_;
	TaskScheduler *scheduler_ = nullptr;
	EventTriggerId event_ = 0;
	FrameSource *source_ = nullptr;
	std::deque<Queued> queue_;
	bool want_key_ = true;
	bool lagging_ = false;  // dropped pictures (a full queue) until an IDR resumes the clients
	// Loop thread.
	int users_ = 0;
	bool subscribed_ = false, retrying_ = false;
};

Feed feed;

// The access units of the device stream as NAL units, one per delivery, for live555's discrete framers.
class FrameSource : public FramedSource {
public:
	static FrameSource *createNew(UsageEnvironment &env) { return new FrameSource(env); }

	// RTSP thread: a frame is queued.
	void deliver()
	{
		if (!isCurrentlyAwaitingData()) return;
		if (next_ >= nals_.size()) {
			auto f = feed.take();
			if (!f) return;
			split(f->frame);
			time_ = wall_time(f->at);
			if (nals_.empty()) return;
		}
		auto [data, size] = nals_[next_++];
		if (size > fMaxSize) {
			fNumTruncatedBytes = size - fMaxSize;
			size = fMaxSize;
		} else {
			fNumTruncatedBytes = 0;
		}
		std::memcpy(fTo, data, size);
		fFrameSize = size;
		fPresentationTime = time_;
		fDurationInMicroseconds = 0;
		if (next_ >= nals_.size()) {
			nals_.clear();
			storage_.reset();
		}
		FramedSource::afterGetting(this);
	}

protected:
	explicit FrameSource(UsageEnvironment &env) : FramedSource(env)
	{
		feed.set_source(this);
		loop::post([] { feed.use(); });
	}
	~FrameSource() override
	{
		feed.clear_source(this);
		loop::post([] { feed.release(); });
	}
	void doGetNextFrame() override { deliver(); }

private:
	// The NAL units of an Annex B access unit, without start codes and access unit delimiters.
	void split(const Frame &f)
	{
		nals_.clear();
		next_ = 0;
		storage_ = f.storage;
		const unsigned char *p = f.data(), *end = p + f.size;
		auto start_code = [&](const unsigned char *q) {
			return q + 3 <= end && q[0] == 0 && q[1] == 0 && q[2] == 1;
		};
		const unsigned char *nal = nullptr;
		auto close = [&](const unsigned char *stop) {
			if (!nal) return;
			while (stop > nal && stop[-1] == 0) stop--;  // the leading zero of a four-byte start code
			if (stop > nal && !delimiter(nal)) nals_.emplace_back(nal, static_cast<unsigned>(stop - nal));
		};
		for (const unsigned char *q = p; q < end;) {
			if (start_code(q)) {
				close(q);
				q += 3;
				nal = q;
			} else {
				q++;
			}
		}
		close(end);
	}
	bool delimiter(const unsigned char *nal) const
	{
		return codec_ == Codec::h264 ? (nal[0] & 0x1f) == 9 : ((nal[0] >> 1) & 0x3f) == 35;
	}

public:
	Codec codec_ = default_codec;

private:
	std::shared_ptr<std::vector<unsigned char>> storage_;
	std::vector<std::pair<const unsigned char *, unsigned>> nals_;
	std::size_t next_ = 0;
	timeval time_{};
};

void frame_queued(void *)
{
	if (auto *source = feed.source()) source->deliver();
}

class AudioSource;

// One listener of the shared Opus capture (video/audio.hpp) for every RTSP client, while one plays audio; packets
// are queued on the loop thread and taken on the RTSP thread.
class AudioFeed {
public:
	// Loop thread: a source started or stopped on the RTSP thread.
	void use()
	{
		if (users_++ > 0 || listener_) return;
		std::string error;
		if (!audio::acquire_session()) {
			log::info("rtsp: no audio for the clients: every audio slot is taken");
			return;
		}
		listener_ = audio::listen(error);
		if (!listener_) {
			audio::release_session();
			log::info("rtsp: no audio for the clients: {}", error);
			return;
		}
		listener_->packet = [this](const audio::Packet &packet) { queue(packet); };
		listener_->ended = [this] { stop(); };
	}
	void release()
	{
		if (users_ == 0 || --users_ > 0) return;
		stop();
	}

	// RTSP thread.
	void attach(TaskScheduler *scheduler, EventTriggerId event)
	{
		std::lock_guard lock(mutex_);
		scheduler_ = scheduler;
		event_ = event;
	}
	void detach()
	{
		std::lock_guard lock(mutex_);
		scheduler_ = nullptr;
		queue_.clear();
	}
	void set_source(AudioSource *source)
	{
		std::lock_guard lock(mutex_);
		source_ = source;
		queue_.clear();
	}
	void clear_source(AudioSource *source)
	{
		std::lock_guard lock(mutex_);
		if (source_ == source) source_ = nullptr;
	}
	AudioSource *source()
	{
		std::lock_guard lock(mutex_);
		return source_;
	}
	std::optional<audio::Packet> take()
	{
		std::lock_guard lock(mutex_);
		if (queue_.empty()) return std::nullopt;
		auto packet = std::move(queue_.front());
		queue_.pop_front();
		return packet;
	}

private:
	void queue(const audio::Packet &packet)
	{
		std::lock_guard lock(mutex_);
		if (!scheduler_ || !source_) return;
		if (queue_.size() >= max_queued_audio) queue_.pop_front();  // a stalled client loses the oldest sound
		queue_.push_back(packet);
		scheduler_->triggerEvent(event_, this);
	}
	void stop()
	{
		if (!listener_) return;
		listener_.reset();
		audio::release_session();
	}

	std::mutex mutex_;
	TaskScheduler *scheduler_ = nullptr;
	EventTriggerId event_ = 0;
	AudioSource *source_ = nullptr;
	std::deque<audio::Packet> queue_;
	// Loop thread.
	int users_ = 0;
	std::unique_ptr<audio::Listener> listener_;
};

AudioFeed audio_feed;

// The Opus packets of the shared capture, one per delivery.
class AudioSource : public FramedSource {
public:
	static AudioSource *createNew(UsageEnvironment &env) { return new AudioSource(env); }

	// RTSP thread: a packet is queued.
	void deliver()
	{
		if (!isCurrentlyAwaitingData()) return;
		auto packet = audio_feed.take();
		if (!packet) return;
		unsigned size = static_cast<unsigned>(packet->data->size());
		if (size > fMaxSize) {
			fNumTruncatedBytes = size - fMaxSize;
			size = fMaxSize;
		} else {
			fNumTruncatedBytes = 0;
		}
		std::memcpy(fTo, packet->data->data(), size);
		fFrameSize = size;
		fPresentationTime = wall_time(packet->at);
		fDurationInMicroseconds = 0;
		FramedSource::afterGetting(this);
	}

protected:
	explicit AudioSource(UsageEnvironment &env) : FramedSource(env)
	{
		audio_feed.set_source(this);
		loop::post([] { audio_feed.use(); });
	}
	~AudioSource() override
	{
		audio_feed.clear_source(this);
		loop::post([] { audio_feed.release(); });
	}
	void doGetNextFrame() override { deliver(); }
};

void audio_queued(void *)
{
	if (auto *source = audio_feed.source()) source->deliver();
}

// ---------------------------------------------------------------------------------------------------------------
// live555 objects (RTSP thread)

// A client started the video track over its TCP connection on this channel (defined with the server below).
void video_started(int socket, unsigned char channel);

class Subsession : public OnDemandServerMediaSubsession {
public:
	static Subsession *createNew(UsageEnvironment &env, Codec codec) { return new Subsession(env, codec); }
	Codec codec() const { return codec_; }

protected:
	Subsession(UsageEnvironment &env, Codec codec) : OnDemandServerMediaSubsession(env, True), codec_(codec) {}
	~Subsession() override { delete[] aux_; }

	FramedSource *createNewStreamSource(unsigned, unsigned &bitrate) override
	{
		bitrate = 20000;  // kbit/s: the highest the settings offer
		auto *source = FrameSource::createNew(envir());
		source->codec_ = codec_;
		if (codec_ == Codec::h264) return H264VideoStreamDiscreteFramer::createNew(envir(), source);
		return H265VideoStreamDiscreteFramer::createNew(envir(), source);
	}
	// A client joining a stream that already runs starts at an IDR too.
	void startStream(unsigned id, void *token, TaskFunc *rr, void *rr_data, unsigned short &seq, unsigned &ts,
	                 ServerRequestAlternativeByteHandler *alt, void *alt_data) override
	{
		// SETUP also runs for tracks the client never plays. Count the video once live555 attaches its RTP channel.
		auto *key = reinterpret_cast<char const *>(static_cast<std::uintptr_t>(id));
		auto *dest = static_cast<Destinations const *>(fDestinationsHashTable->Lookup(key));
		if (token && dest && dest->isTCP) video_started(dest->tcpSocketNum, dest->rtpChannelId);
		OnDemandServerMediaSubsession::startStream(id, token, rr, rr_data, seq, ts, alt, alt_data);
		loop::post([] { request_keyframe(); });
	}
	RTPSink *createNewRTPSink(Groupsock *gs, unsigned char payload_type, FramedSource *) override
	{
		MultiFramedRTPSink *sink;
		if (codec_ == Codec::h264) sink = H264VideoRTPSink::createNew(envir(), gs, payload_type);
		else sink = H265VideoRTPSink::createNew(envir(), gs, payload_type);
		sink->setPacketSizes(rtp_packet_bytes, rtp_packet_bytes);
		return sink;
	}
	// The parameter sets (sprop-*) of the SDP: play the stream into the sink until its framer has seen them, as
	// live555's file subsessions do, for at most sdp_wait_ms.
	char const *getAuxSDPLine(RTPSink *sink, FramedSource *source) override
	{
		if (aux_) return aux_;
		if (!dummy_) {
			dummy_ = sink;
			waited_ = 0;
			done_ = 0;
			sink->startPlaying(*source, after_playing, this);
			check(this);
		}
		envir().taskScheduler().doEventLoop(&done_);
		return aux_;
	}

private:
	static void after_playing(void *self)
	{
		auto *s = static_cast<Subsession *>(self);
		s->envir().taskScheduler().unscheduleDelayedTask(s->check_task_);
		s->done_ = ~0;
	}
	static void check(void *self)
	{
		auto *s = static_cast<Subsession *>(self);
		s->check_task_ = nullptr;
		char const *line = s->dummy_ ? s->dummy_->auxSDPLine() : nullptr;
		if (line || s->waited_ >= sdp_wait_ms) {
			s->aux_ = strDup(line ? line : "");
			s->dummy_ = nullptr;
			s->done_ = ~0;
			return;
		}
		s->waited_ += 100;
		s->check_task_ = s->envir().taskScheduler().scheduleDelayedTask(100000, check, s);
	}

	Codec codec_;
	char *aux_ = nullptr;
	RTPSink *dummy_ = nullptr;
	EventLoopWatchVariable done_{0};
	unsigned waited_ = 0;
	TaskToken check_task_ = nullptr;
};

// The Opus track: RFC 7587 (OPUS/48000/2, stereo signalled in the fmtp).
class AudioSubsession : public OnDemandServerMediaSubsession {
public:
	static AudioSubsession *createNew(UsageEnvironment &env) { return new AudioSubsession(env); }

protected:
	explicit AudioSubsession(UsageEnvironment &env) : OnDemandServerMediaSubsession(env, True) {}
	~AudioSubsession() override { delete[] aux_; }

	FramedSource *createNewStreamSource(unsigned, unsigned &bitrate) override
	{
		bitrate = 192;
		return AudioSource::createNew(envir());
	}
	RTPSink *createNewRTPSink(Groupsock *gs, unsigned char payload_type, FramedSource *) override
	{
		payload_type_ = payload_type;
		return SimpleRTPSink::createNew(envir(), gs, payload_type, audio::sample_rate, "audio", "OPUS",
		                                audio::channels, False);
	}
	char const *getAuxSDPLine(RTPSink *, FramedSource *) override
	{
		if (!aux_) {
			std::string line = std::format("a=fmtp:{} {}\r\n", payload_type_, audio::opus_fmtp);
			aux_ = strDup(line.c_str());
		}
		return aux_;
	}

private:
	unsigned char payload_type_ = 97;
	char *aux_ = nullptr;
};

class Server : public RTSPServer {
public:
	// warning: the address family that could not listen, when the other could.
	static Server *create(UsageEnvironment &env, Port port, UserAuthenticationDatabase *auth, std::string &warning)
	{
		int v4 = setUpOurSocket(env, port, AF_INET);
		if (v4 < 0) warning = std::format("IPv4: {}", env.getResultMsg());
		int v6 = setUpOurSocket(env, port, AF_INET6);
		if (v6 < 0) warning = std::format("IPv6: {}", env.getResultMsg());
		if (v4 < 0 && v6 < 0) return nullptr;
		close_on_exec(v4);
		close_on_exec(v6);
		return new Server(env, v4, v6, port, auth);
	}

	// The stream at the codec the device streams now; a stale one (the codec changed) is replaced while no client
	// uses it.
	void lookupServerMediaSession(char const *name, lookupServerMediaSessionCompletionFunc *done, void *data,
	                              Boolean first) override
	{
		if (std::string_view(name) != stream_name) return (*done)(data, nullptr);
		if (first) {
			struct Want {
				std::optional<Codec> codec;  // nullopt: the clients' codec stays
				bool audio = false;
			};
			auto want = on_loop<Want>([]() -> Want {
				if (feed.active()) return {std::nullopt, false};
				if (auto c = active_codec()) return {c, audio::enabled()};
				return {effective_selected_codec().value_or(default_codec), audio::enabled()};
			});
			if (want && want->codec && (!subsession_ || subsession_->codec() != *want->codec || audio_ != want->audio))
				replace(*want->codec, want->audio);
		}
		if (!session_) replace(default_codec, false);
		(*done)(data, session_);
	}

protected:
	Server(UsageEnvironment &env, int v4, int v6, Port port, UserAuthenticationDatabase *auth)
		: RTSPServer(env, v4, v6, port, auth, 65)
	{
	}

	// A send buffer for about 100 ms of a 20 Mbit/s stream over RTP over TCP (live555 asks for 50 KiB): enough for a
	// short Wi-Fi pause; a longer one shows as a backlog in the queue, which lowers the bit rate, instead of seconds of
	// delay hidden in the socket. A connection the server closes is reset: the video
	// still queued for it is of no use, and a closed connection with data still queued (FIN_WAIT1) keeps the IPv4
	// port from being bound again when the server restarts.
	// At most max_connections connections, authenticated or not: each holds a descriptor of the server process.
	ClientConnection *createNewClientConnection(int socket, sockaddr_storage const &addr) override
	{
		if (fClientConnections->numEntries() >= max_connections) {
			log::info("rtsp: refused a connection from {}: {} are open", address_of(addr), max_connections);
			::close(socket);
			return nullptr;
		}
		close_on_exec(socket);
		RTPInterfaceForgetSocket(socket);
		closed(socket);
		int bytes = 256 << 10;
		if (setsockopt(socket, SOL_SOCKET, SO_SNDBUFFORCE, &bytes, sizeof bytes) != 0)
			log::error("rtsp: cannot enlarge the send buffer: {}", std::strerror(errno));
		linger reset{1, 0};
		if (setsockopt(socket, SOL_SOCKET, SO_LINGER, &reset, sizeof reset) != 0)
			log::error("rtsp: cannot set SO_LINGER: {}", std::strerror(errno));
		return RTSPServer::createNewClientConnection(socket, addr);
	}

public:
	// RTSP thread: the output queue of a client, after a packet or a drain (live555 patch 0002). It counts only while
	// the client has a video track: audio and control alone do not take the video late.
	void backlog(int socket, unsigned bytes)
	{
		if (!video_channels_.contains(socket)) return;
		backlogs_[socket] = bytes;
		update_least();
	}
	// RTSP thread: a client started the video track on this channel.
	void video_started(int socket, unsigned char channel) { video_channels_[socket].insert(channel); }
	// RTSP thread: a track stopped streaming over the socket (TEARDOWN, or the end of the connection).
	void stream_removed(int socket, unsigned char channel)
	{
		auto it = video_channels_.find(socket);
		if (it == video_channels_.end() || !it->second.erase(channel) || !it->second.empty()) return;
		video_channels_.erase(it);
		backlogs_.erase(socket);
		update_least();
	}
	// RTSP thread: the client's connection closed, or it was disconnected.
	void closed(int socket)
	{
		video_channels_.erase(socket);
		backlogs_.erase(socket);
		update_least();
	}

protected:
	// A client that has failed too often is refused like a web login, even with the right password.
	Boolean specialClientUserAccessCheck(int, sockaddr_storage const &addr, char const *, char const *) override
	{
		std::string key = auth::login_key(address_of(addr));
		auto ok = on_loop<bool>([key] {
			if (auth::check_login_attempt(key)) return false;
			auth::clear_login_attempt(key, username);
			return true;
		});
		return ok && *ok;
	}
	void specialHandlingOfAuthenticationFailure(int, sockaddr_storage const &addr, char const *) override
	{
		std::string address = address_of(addr);
		log::info("rtsp: authentication failed from {}", address);
		// A refused client's retries during a lockout fail too; counted, they would make it endless for a
		// program that reconnects on its own.
		loop::post([key = auth::login_key(address)] {
			if (!auth::check_login_attempt(key)) (void)auth::record_login_failure(key, username);
		});
	}

private:
	void replace(Codec codec, bool audio)
	{
		if (session_) {
			removeServerMediaSession(session_);
			session_ = nullptr;
		}
		session_ = ServerMediaSession::createNew(envir(), stream_name, "NanoKVM", "NanoKVM video");
		subsession_ = Subsession::createNew(envir(), codec);
		session_->addSubsession(subsession_);
		if (audio) session_->addSubsession(AudioSubsession::createNew(envir()));
		audio_ = audio;
		addServerMediaSession(session_);
	}

	ServerMediaSession *session_ = nullptr;
	Subsession *subsession_ = nullptr;
	bool audio_ = false;
	std::map<int, unsigned> backlogs_;  // output queues of the clients receiving the video
	std::map<int, std::set<unsigned char>> video_channels_;  // video channels of each client's connection

	void update_least()
	{
		unsigned least = backlogs_.empty() ? 0 : UINT_MAX;
		for (auto &[s, b] : backlogs_) least = std::min(least, b);
		least_backlog.store(least);
	}
};

// ---------------------------------------------------------------------------------------------------------------
// The RTSP thread

// The server of the RTSP thread, for live555's backlog hook (a plain function). RTSP thread.
Server *active_server = nullptr;

void video_started(int socket, unsigned char channel)
{
	if (active_server) active_server->video_started(socket, channel);
}

class Runner {
public:
	// Loop thread: an error message when the server cannot listen.
	std::optional<std::string> start(const Settings &s)
	{
		std::promise<std::optional<std::string>> started;
		auto result = started.get_future();
		thread_ = std::thread([this, s, &started] { run(s, started); });
		auto error = result.get();
		if (error) {
			thread_.join();
			return error;
		}
		return std::nullopt;
	}
	// After start: why one address family does not listen ("" when both do).
	const std::string &warning() const { return warning_; }
	// Any thread but the RTSP thread; it waits for the RTSP thread to end.
	void stop()
	{
		command([this] { watch_ = ~0; });
		if (thread_.joinable()) thread_.join();
	}

private:
	void run(Settings s, std::promise<std::optional<std::string>> &started)
	{
		OutPacketBuffer::maxSize = 2'000'000;  // a key frame of a 4K picture
		TaskScheduler *scheduler = BasicTaskScheduler::createNew();
		UsageEnvironment *env = BasicUsageEnvironment::createNew(*scheduler);
		auto *db = new UserAuthenticationDatabase(realm);
		db->addUserRecord(username, s.password.c_str());
		server_ = Server::create(*env, Port(static_cast<portNumBits>(s.port)), db, warning_);
		if (!server_) {
			std::string error = std::format("port {} cannot be used: {}", s.port, env->getResultMsg());
			delete db;
			env->reclaim();
			delete scheduler;
			started.set_value(error);
			return;
		}
		// RTP over TCP only: over UDP every lost packet breaks the picture until the next key frame, which RTSP
		// clients cannot ask for.
		server_->disableStreamingRTPOverUDP();
		active_server = server_;
		RTPInterfaceBacklogHook = [](int socket, unsigned bytes) {
			if (active_server) active_server->backlog(socket, bytes);
		};
		RTPInterfaceClosedHook = [](int socket) {
			if (active_server) active_server->closed(socket);
		};
		RTPInterfaceStreamRemovedHook = [](int socket, unsigned char channel) {
			if (active_server) active_server->stream_removed(socket, channel);
		};
		command_event_ = scheduler->createEventTrigger(run_commands);
		frame_event_ = scheduler->createEventTrigger(frame_queued);
		audio_event_ = scheduler->createEventTrigger(audio_queued);
		scheduler_ = scheduler;
		feed.attach(scheduler, frame_event_);
		audio_feed.attach(scheduler, audio_event_);
		if (!warning_.empty()) log::error("rtsp: {}", warning_);
		log::info("rtsp: listening on port {}", s.port);
		started.set_value(std::nullopt);

		scheduler->doEventLoop(&watch_);

		feed.detach();
		audio_feed.detach();
		RTPInterfaceBacklogHook = nullptr;
		RTPInterfaceClosedHook = nullptr;
		RTPInterfaceStreamRemovedHook = nullptr;
		active_server = nullptr;
		least_backlog.store(0);
		Medium::close(server_);
		server_ = nullptr;
		RTPInterfaceForgetAll();
		{
			std::lock_guard lock(mutex_);
			scheduler_ = nullptr;
			commands_.clear();
		}
		delete db;
		env->reclaim();
		delete scheduler;
		log::info("rtsp: stopped");
	}

	// Runs fn on the RTSP thread. Loop thread.
	void command(std::function<void()> fn)
	{
		std::lock_guard lock(mutex_);
		if (!scheduler_) return;
		commands_.push_back(std::move(fn));
		scheduler_->triggerEvent(command_event_, this);
	}
	static void run_commands(void *self)
	{
		auto *r = static_cast<Runner *>(self);
		std::deque<std::function<void()>> todo;
		{
			std::lock_guard lock(r->mutex_);
			todo.swap(r->commands_);
		}
		for (auto &fn : todo) fn();
	}

	std::thread thread_;
	std::string warning_;
	EventLoopWatchVariable watch_{0};
	Server *server_ = nullptr;
	std::mutex mutex_;
	TaskScheduler *scheduler_ = nullptr;
	EventTriggerId command_event_ = 0, frame_event_ = 0, audio_event_ = 0;
	std::deque<std::function<void()>> commands_;
};

// Loop thread.
std::shared_ptr<Runner> runner;
std::string last_error;  // why the server does not run, or listens on one address family only
// A stopping server can take seconds (a client's blocking send, the wait for the first picture); it stops on a
// pool thread, and the next one starts once it has.
int stops = 0;
std::function<void()> next_start;

void apply(const Settings &s, std::string error = {});
void stop_with(const std::string &error);

// Starts a server for s; error is the reason shown when it does not run.
void launch(const Settings &s, std::string error = {})
{
	last_error = std::move(error);
	if (!s.enabled) return;
	auto r = std::make_shared<Runner>();
	if (auto error = r->start(s)) {
		last_error = *error;
		log::error("rtsp: {}", *error);
		return;
	}
	last_error = r->warning();
	runner = std::move(r);
	// The clients' session descriptions name the old codec: they reconnect and get the new one.
	feed.restart_clients = [] {
		loop::post([] {
			if (!runner) return;
			std::string error;
			if (auto s = checked_settings(error)) return apply(*s);
			stop_with(error);
		});
	};
}

void start_next()
{
	if (stops > 0 || !next_start) return;
	auto fn = std::move(next_start);
	next_start = nullptr;
	fn();
}

void apply(const Settings &s, std::string error)
{
	feed.restart_clients = nullptr;
	next_start = [s, error = std::move(error)] { launch(s, error); };
	if (auto old = std::move(runner)) {
		stops++;
		loop::blocking([old] {
			old->stop();
			loop::post([] {
				stops--;
				start_next();
			});
		});
	}
	start_next();
}

// No valid password could be made and saved: the running server stops (on the pool thread, as for any change), and
// the page shows why.
void stop_with(const std::string &error)
{
	log::error("rtsp: {}", error);
	Settings off = load_settings();
	off.enabled = false;
	apply(off, error);
}

std::string settings_json(const Settings &s)
{
	return std::format(R"({{"enabled":{},"port":{},"path":"/{}","username":"{}","password":{},"running":{},"error":{},"streaming":{},"lostFrames":{},"overflows":{}}})",
	                   s.enabled, s.port, stream_name, username, api::quote(s.password),
	                   runner != nullptr || (s.enabled && next_start != nullptr),
	                   api::quote(last_error), feed.active(), feed.lost_frames, feed.overflows);
}

struct SettingsBody {
	std::optional<bool> enabled;
	std::optional<int> port;
	std::optional<bool> newPassword;
};

} // namespace

void start()
{
	std::string error;
	if (auto s = checked_settings(error)) return apply(*s);
	stop_with(error);
}

void stop()
{
	feed.restart_clients = nullptr;
	next_start = nullptr;
	if (runner) {
		runner->stop();
		runner.reset();
	}
}

void add_routes(http::Router &router)
{
	const auth::Guard admin{{"admin"}};
	router.add("GET", "/api/stream/rtsp", auth::guarded([](http::Request &, http::Response &res) {
		api::send(res, 0, "success", settings_json(load_settings()));
	}, admin));
	router.add("POST", "/api/stream/rtsp", auth::guarded([](http::Request &req, http::Response &res) {
		SettingsBody body;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(body, req.body))
			return api::err(res, -1, "invalid request");
		Settings s = load_settings();
		if (body.port) {
			if (*body.port < 1 || *body.port > 65535 || *body.port == 80 || *body.port == 443)
				return api::err(res, -1, "the port must be 1-65535, not 80 or 443");
			s.port = *body.port;
		}
		if (body.enabled) s.enabled = *body.enabled;
		if ((body.newPassword && *body.newPassword) || (s.enabled && !valid_password(s.password))) {
			s.password = new_password();
			if (s.password.empty()) return api::err(res, -2, "no random password");
		}
		if (!save_settings(s)) {
			log::error("rtsp: cannot write {}", settings_file);
			return api::err(res, -2, "the setting could not be saved");
		}
		apply(s);
		api::send(res, 0, "success", settings_json(s));
	}, admin));
}

} // namespace nkos::video::rtsp
