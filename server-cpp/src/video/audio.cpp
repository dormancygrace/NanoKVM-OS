#include "video/audio.hpp"

#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "http/server.hpp"
#include "http/websocket.hpp"
#include "video/rtc_common.hpp"

#include <glaze/glaze.hpp>

#include <cerrno>
#include <csignal>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <fstream>
#include <set>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

namespace nkos::video::audio {

namespace {

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

constexpr std::size_t max_packet = 1275;
constexpr std::size_t max_listeners = 8;
constexpr int max_sessions = 8;
const char *const helper = "/kvmapp/system/bin/usb-audio-capture";

int sessions = 0;

// The shared capture: the helper process, its output on the loop, the listeners.
struct Hub {
	std::set<Listener *> listeners;
	pid_t pid = -1;
	http::FdWatch watch = 0;
	bool running = false;     // a capture run (with retries) is on
	bool exiting = false;     // the previous helper has not been reaped yet
	std::string buffer;
	std::uint64_t index = 0;
	std::chrono::milliseconds backoff = 100ms;
	std::uint64_t generation = 0;

	void start_run();
	void attempt();
	void readable(int fd);
	void helper_gone(const std::string &why);
	void end_run();
};
Hub hub;

std::optional<std::string> capture_card()
{
	DIR *d = ::opendir("/sys/class/sound");
	if (!d) return std::nullopt;
	std::optional<std::string> found;
	while (dirent *e = ::readdir(d)) {
		std::string name = e->d_name;
		if (name.rfind("card", 0) != 0) continue;
		std::ifstream f("/sys/class/sound/" + name + "/id");
		std::string id;
		std::getline(f, id);
		while (!id.empty() && (id.back() == ' ' || id.back() == '\n' || id.back() == '\t')) id.pop_back();
		if (id != "UAC1Gadget") continue;
		std::string number = name.substr(4);
		if (!number.empty() && number.size() <= 3 && number.find_first_not_of("0123456789") == std::string::npos &&
		    std::stoi(number) < 256) {
			found = number;
			break;
		}
	}
	::closedir(d);
	return found;
}

void Hub::start_run()
{
	running = true;
	backoff = 100ms;
	generation++;
	attempt();
}

// One helper process; on its end the capture is tried again (the card may have a new number after USB
// re-enumeration), until no listener is left or audio is disabled.
void Hub::attempt()
{
	if (!running) return;
	if (!enabled()) return end_run();
	auto card = capture_card();
	std::string why = "USB audio capture card is unavailable";
	if (card) {
		int out[2];
		if (::pipe2(out, O_CLOEXEC) == 0) {
			pid_t child = ::fork();
			if (child == 0) {
				::dup2(out[1], 1);
				struct sigaction sa{};
				sa.sa_handler = SIG_DFL;
				for (int s : {SIGPIPE, SIGTERM, SIGINT, SIGHUP}) ::sigaction(s, &sa, nullptr);
				sigset_t none;
				sigemptyset(&none);
				::sigprocmask(SIG_SETMASK, &none, nullptr);
				process::cloexec_inherited();
				::execl(helper, helper, card->c_str(), static_cast<char *>(nullptr));
				::_exit(127);
			}
			::close(out[1]);
			if (child > 0) {
				pid = child;
				buffer.clear();
				::fcntl(out[0], F_SETFL, O_NONBLOCK);
				int fd = out[0];
				std::uint64_t g = generation;
				watch = http::watch_fd(fd, http::FdCallbacks{
					[this, fd, g] {
						if (g == generation) readable(fd);
					},
					{},
					[this, g] {
						watch = 0;
						if (g == generation) helper_gone("EOF");
					},
				});
				if (watch) return;
				::close(fd);
				why = "cannot watch the capture";
			} else {
				::close(out[0]);
				why = std::strerror(errno);
			}
		} else {
			why = std::strerror(errno);
		}
	}
	helper_gone(why);
}

void Hub::readable(int fd)
{
	char buf[4096];
	while (true) {
		ssize_t n = ::read(fd, buf, sizeof buf);
		if (n > 0) {
			buffer.append(buf, static_cast<std::size_t>(n));
			continue;
		}
		if (n < 0 && (errno == EAGAIN || errno == EINTR)) break;
		return helper_gone(n == 0 ? "EOF" : std::strerror(errno));
	}
	// Packets: a 16-bit big-endian length, then the Opus packet.
	std::size_t at = 0;
	while (buffer.size() - at >= 2) {
		std::size_t size = (static_cast<unsigned char>(buffer[at]) << 8) | static_cast<unsigned char>(buffer[at + 1]);
		if (size < 1 || size > max_packet) return helper_gone("invalid Opus packet length");
		if (buffer.size() - at - 2 < size) break;
		Packet p{std::make_shared<const std::string>(buffer.substr(at + 2, size)), index++, Clock::now()};
		at += 2 + size;
		backoff = 100ms;
		auto targets = std::vector<Listener *>(listeners.begin(), listeners.end());
		for (auto *l : targets)
			if (listeners.count(l) && l->packet) l->packet(p);
	}
	buffer.erase(0, at);
}

// The helper ended or could not start: reap it off the loop, then try again after the backoff.
void Hub::helper_gone(const std::string &why)
{
	if (watch) {
		http::unwatch_fd(watch);
		watch = 0;
	}
	if (pid > 0) {
		pid_t p = pid;
		pid = -1;
		exiting = true;
		::kill(p, SIGKILL);
		loop::blocking([p] {
			int status = 0;
			while (::waitpid(p, &status, 0) < 0 && errno == EINTR) {
			}
			loop::post([] { hub.exiting = false; });
		});
	}
	if (!running) return;
	if (!enabled()) return end_run();
	log::warn("USB audio capture interrupted, retrying in {}ms: {}", backoff.count(), why);
	std::uint64_t g = ++generation;
	auto delay = backoff;
	backoff = std::min<std::chrono::milliseconds>(backoff * 2, 2s);
	loop::after(delay, [this, g] {
		if (g == generation) attempt();
	});
}

// No more capture: every listener is told.
void Hub::end_run()
{
	running = false;
	generation++;
	if (watch || pid > 0) helper_gone("stopped");
	auto targets = std::vector<Listener *>(listeners.begin(), listeners.end());
	listeners.clear();
	for (auto *l : targets)
		if (l->ended) l->ended();
}

// The standalone audio connection (GET /api/stream/audio): the page offers, the server answers.
struct Signal {
	std::string event;
	glz::raw_json data;
};
constexpr glz::opts lenient{.error_on_unknown_keys = false};

struct Standalone : http::WsHandler, std::enable_shared_from_this<Standalone> {
	http::WebSocket *ws = nullptr;
	std::string bind_address;
	std::shared_ptr<rtc::PeerConnection> pc;
	std::shared_ptr<rtc::Track> track;
	std::shared_ptr<rtc::RtpPacketizationConfig> rtp;
	std::unique_ptr<Listener> listener;
	bool offered = false, connected = false, failed = false, done = false;
	Clock::time_point started = Clock::now();
	std::uint64_t last = 0;
	Clock::time_point last_at;
	bool have_last = false;
	std::uint32_t ts = rtc_common::random_u32();
	std::uint64_t timer = 0;

	void write(const std::string &event, const std::string &raw)
	{
		if (ws && !ws->closing()) ws->send(http::make_message("{\"data\":" + raw + ",\"event\":\"" + event + "\"}", false));
	}

	void finish()
	{
		if (done) return;
		done = true;
		listener.reset();
		release_session();
		if (pc) {
			auto c = std::move(pc);
			loop::blocking([c] {
				try {
					c->close();
				} catch (...) {
				}
			});
		}
		if (ws && !ws->closing()) ws->close(1000, "");
	}

	void opened(http::WebSocket &socket) override
	{
		ws = &socket;
		ws->max_message = 64 << 10;
		auto [servers, json] = rtc_common::ice_servers();
		try {
			pc = std::make_shared<rtc::PeerConnection>(rtc_common::configuration(servers, bind_address, 0));
		} catch (const std::exception &) {
			return finish();
		}
		std::weak_ptr<Standalone> weak = shared_from_this();
		pc->onLocalCandidate([weak](rtc::Candidate c) {
			std::string json = rtc_common::candidate_json(c);
			loop::post([weak, json] {
				if (auto s = weak.lock(); s && !s->done) s->write("candidate", json);
			});
		});
		pc->onStateChange([weak](rtc::PeerConnection::State state) {
			loop::post([weak, state] {
				auto s = weak.lock();
				if (!s || s->done) return;
				s->connected = state == rtc::PeerConnection::State::Connected;
				s->failed = state == rtc::PeerConnection::State::Failed || state == rtc::PeerConnection::State::Closed;
			});
		});
		write("ice-servers", json);
		tick(++timer);
	}

	// Once a second, as the Go connection: gone when audio is disabled or the peer failed; a listener once
	// connected; nothing after 30 s without a connection.
	void tick(std::uint64_t t)
	{
		if (done || t != timer) return;
		if (!enabled() || failed) return finish();
		if (!listener && connected) {
			std::string error;
			listener = listen(error);
			if (!listener) {
				write("error", gojson::quote(error));
				return finish();
			}
			listener->packet = [this](const Packet &p) { send(p); };
			listener->ended = [this] {
				std::weak_ptr<Standalone> weak = shared_from_this();
				loop::post([gone = listener.release(), weak] {
					delete gone;
					if (auto s = weak.lock()) s->finish();
				});
			};
		}
		if (!listener && Clock::now() - started > 30s) return finish();
		std::weak_ptr<Standalone> weak = shared_from_this();
		loop::after(1s, [weak, t] {
			if (auto s = weak.lock()) s->tick(t);
		});
	}

	void send(const Packet &p)
	{
		if (!track || !rtp) return;
		std::uint64_t dropped = 0;
		if (have_last && p.index > last + 1) dropped = std::min<std::uint64_t>(p.index - last - 1, 65535);
		if (have_last) {
			auto gap = pause_gap(p.at - last_at, dropped);
			ts += static_cast<std::uint32_t>(std::chrono::duration_cast<std::chrono::microseconds>(gap).count() * sample_rate / 1000000);
		}
		ts += static_cast<std::uint32_t>(dropped * 960);
		try {
			track->sendFrame(reinterpret_cast<const rtc::byte *>(p.data->data()), p.data->size(), rtc::FrameInfo(ts));
		} catch (const std::exception &) {
			return finish();
		}
		ts += 960;
		last = p.index;
		last_at = p.at;
		have_last = true;
	}

	void received(http::WebSocket &, std::string_view text, bool) override
	{
		if (done) return;
		Signal m;
		if (glz::read<lenient>(m, std::string(text))) return finish();
		if (m.event == "offer") {
			if (offered) return finish();
			offered = true;
			rtc_offer(m.data.str);
		} else if (m.event == "candidate") {
			if (!offered) return finish();
			rtc_candidate(m.data.str);
		} else {
			finish();
		}
	}

	void rtc_offer(const std::string &raw);
	void rtc_candidate(const std::string &raw);

	void closed(http::WebSocket &) override
	{
		ws = nullptr;
		finish();
	}
};

struct Description {
	std::string type;
	std::string sdp;
};
struct CandidateInit {
	std::string candidate;
	std::optional<std::string> sdpMid;
};

void Standalone::rtc_offer(const std::string &raw)
{
	Description d;
	if (glz::read<lenient>(d, raw) || d.type != "offer") return finish();
	try {
		pc->setRemoteDescription(rtc::Description(d.sdp, d.type));
		auto remote = pc->remoteDescription();
		for (int i = 0; remote && i < remote->mediaCount(); i++) {
			auto entry = remote->media(i);
			auto *media = std::get_if<rtc::Description::Media *>(&entry);
			if (!media || (*media)->type() != "audio") continue;
			auto reciprocated = (*media)->reciprocate();
			auto pt = rtc_common::find_payload(reciprocated, "opus", {});
			if (!pt) return finish();
			std::uint32_t ssrc = rtc_common::random_u32();
			rtc_common::trim_to(reciprocated, *pt, std::string(opus_fmtp), ssrc, "audio", "nanokvm-usb-audio");
			track = pc->addTrack(reciprocated);
			rtp = std::make_shared<rtc::RtpPacketizationConfig>(ssrc, "audio", static_cast<std::uint8_t>(*pt), sample_rate);
			auto packetizer = std::make_shared<rtc::OpusRtpPacketizer>(rtp);
			packetizer->addToChain(std::make_shared<rtc::RtcpSrReporter>(rtp));
			track->setMediaHandler(packetizer);
			break;
		}
		if (!track) return finish();
		std::weak_ptr<Standalone> weak = shared_from_this();
		pc->onLocalDescription([weak](rtc::Description description) {
			std::string json = rtc_common::description_json(description.typeString(), std::string(description));
			loop::post([weak, json] {
				if (auto s = weak.lock(); s && !s->done) s->write("answer", json);
			});
		});
		pc->setLocalDescription(rtc::Description::Type::Answer);
	} catch (const std::exception &) {
		finish();
	}
}

void Standalone::rtc_candidate(const std::string &raw)
{
	CandidateInit c;
	if (glz::read<lenient>(c, raw)) return finish();
	try {
		pc->addRemoteCandidate(rtc::Candidate(c.candidate, c.sdpMid.value_or("0")));
	} catch (const std::exception &) {
		finish();
	}
}

} // namespace

Listener::~Listener() { hub.listeners.erase(this); if (hub.listeners.empty() && hub.running) hub.end_run(); }

bool enabled()
{
	struct stat st{};
	return ::lstat("/sys/kernel/config/usb_gadget/g0/configs/c.1/uac1.audio0", &st) == 0 && S_ISLNK(st.st_mode);
}

std::unique_ptr<Listener> listen(std::string &error)
{
	if (!enabled()) {
		error = "USB audio is disabled";
		return nullptr;
	}
	if (hub.listeners.size() >= max_listeners) {
		error = "too many audio listeners";
		return nullptr;
	}
	if (hub.listeners.empty()) {
		// Never race an old helper that still owns the exclusive ALSA capture.
		if (hub.exiting) {
			error = "USB audio is restarting";
			return nullptr;
		}
	} else if (!hub.running) {
		error = "USB audio capture stopped";
		return nullptr;
	}
	auto l = std::make_unique<Listener>();
	hub.listeners.insert(l.get());
	if (!hub.running) hub.start_run();
	return l;
}

bool acquire_session()
{
	if (sessions >= max_sessions) return false;
	sessions++;
	return true;
}

void release_session()
{
	if (sessions > 0) sessions--;
}

std::chrono::nanoseconds pause_gap(std::chrono::nanoseconds elapsed, std::uint64_t dropped)
{
	auto gap = elapsed - std::chrono::duration_cast<std::chrono::nanoseconds>(frame_duration) * static_cast<long>(dropped + 1);
	if (gap < 500ms) return std::chrono::nanoseconds{0};
	return gap;
}

void stop()
{
	if (hub.running || hub.pid > 0) hub.end_run();
}

void add_routes(http::Router &router)
{
	router.ws("/api/stream/audio", auth::guarded_ws([](http::Request &req, http::Response &res) -> std::shared_ptr<http::WsHandler> {
		if (!enabled()) {
			res.text(409, "USB audio is disabled");
			return nullptr;
		}
		if (!acquire_session()) {
			res.text(503, "too many audio sessions");
			return nullptr;
		}
		auto s = std::make_shared<Standalone>();
		s->bind_address = rtc_common::local_address(req);
		return s;
	}));
}

} // namespace nkos::video::audio
