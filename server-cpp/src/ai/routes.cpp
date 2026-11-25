// The AI families on the server: the routes of router/control.go, router/mcp.go and router/picoclaw.go, and what
// their services use of the rest of the server (HID lanes, capture leases and JPEG reads, screen text, the host's
// state, virtual media, Wake-on-LAN, the BMC's power logic) and the PicoClaw gateway's WebSocket (libcurl).

#include "ai/routes.hpp"

#include "ai/capture.hpp"
#include "ai/control_routes.hpp"
#include "ai/json.hpp"
#include "ai/mcp.hpp"
#include "ai/picoclaw.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "bmc/config.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "config/config.hpp"
#include "http/server.hpp"
#include "http/websocket.hpp"
#include "input/control.hpp"
#include "input/hid.hpp"
#include "network/network.hpp"
#include "storage/storage.hpp"
#include "video/hdmi.hpp"
#include "video/screen.hpp"
#include "video/screen_text.hpp"
#include "video/vision.hpp"

#include <arpa/inet.h>
#include <atomic>
#include <condition_variable>
#include <deque>
#include <curl/curl.h>
#include <future>
#include <netdb.h>
#include <openssl/crypto.h>
#include <poll.h>
#include <set>
#include <tuple>
#include <sys/socket.h>
#include <thread>
#include <unistd.h>

namespace nkos::ai {

namespace control = input::control;
namespace hid = input::hid;

namespace {

// Runs fn on the loop thread and waits for its result (from a blocking thread).
template <typename F>
auto on_loop(F fn) -> decltype(fn())
{
	using R = decltype(fn());
	std::promise<R> promise;
	auto future = promise.get_future();
	loop::post([&] {
		if constexpr (std::is_void_v<R>) {
			fn();
			promise.set_value();
		} else {
			promise.set_value(fn());
		}
	});
	return future.get();
}

// --- HID ---

struct GadgetHid : HidSink {
	// One lane for the AI clients' reports (never destroyed: its close would release what it holds at exit).
	hid::Lane &lane()
	{
		static auto *l = new hid::Lane;
		return *l;
	}
	hid::Report report(const std::vector<std::uint8_t> &data, const std::shared_ptr<control::Cancel> &operation)
	{
		hid::Report r;
		r.data = data;
		if (operation) {
			r.reservation = control::Reservation::for_operation(operation);
			r.complete_reservation = false;
		}
		return r;
	}
	void push(HidDevice d, hid::Report r)
	{
		if (d == HidDevice::keyboard) lane().keyboard(std::move(r));
		else lane().mouse(std::move(r));
	}
	bool write(HidDevice d, const std::vector<std::uint8_t> &data, const std::shared_ptr<control::Cancel> &operation, std::string &error) override
	{
		auto r = report(data, operation);
		auto done = std::make_shared<std::promise<bool>>();
		auto future = done->get_future();
		r.complete = [done](bool ok) { done->set_value(ok); };
		push(d, std::move(r));
		if (future.get()) return true;
		error = d == HidDevice::keyboard ? "write /dev/hidg0 failed" : d == HidDevice::relative ? "write /dev/hidg1 failed" : "write /dev/hidg2 failed";
		return false;
	}
	void post(HidDevice d, std::vector<std::uint8_t> data) override { push(d, report(data, nullptr)); }
};

GadgetHid &gadget()
{
	static GadgetHid g;
	return g;
}

std::string release_hid()
{
	hid::release_all();
	return {};
}

// --- Capture ---

// One read at a time through the leases (hdmiCaptureReadSlot).
std::mutex read_slot;

// AcquireHdmiCaptureLeaseForRead: the capture kept running, the wait for a resumed capture's first frame, the one
// read slot, and whether the next picture is stale.
struct ReadLease : capture::Lease, picoclaw::Platform::ReadLease {
	std::shared_ptr<video::hdmi::Lease> lease;
	bool needs_fresh = false, slot = false;
	~ReadLease() override
	{
		if (slot) read_slot.unlock();
		if (lease) loop::post([l = std::move(lease)]() mutable { l.reset(); });
	}
	bool claim_fresh() override
	{
		if (!needs_fresh) return false;
		return on_loop([] { return video::hdmi::claim_fresh_frame(); });
	}
	static std::unique_ptr<ReadLease> acquire(const control::Cancel *cancel)
	{
		auto l = std::make_unique<ReadLease>();
		auto ready = on_loop([&] {
			l->lease = std::make_shared<video::hdmi::Lease>();
			return l->lease->ready_at();
		});
		auto wait = ready - std::chrono::steady_clock::now();
		if (wait > std::chrono::steady_clock::duration::zero()) {
			if (cancel && !cancel->sleep(wait)) return nullptr;
			if (!cancel) std::this_thread::sleep_for(wait);
		}
		read_slot.lock();
		l->slot = true;
		l->needs_fresh = on_loop([] { return video::hdmi::needs_fresh_frame(); });
		return l;
	}
};

int read_mjpeg(std::uint16_t width, std::uint16_t height, std::uint16_t quality, std::string &jpeg)
{
	auto done = std::make_shared<std::promise<std::pair<int, std::string>>>();
	auto future = done->get_future();
	loop::post([=] {
		video::vision::read_mjpeg(width, height, quality, [done](video::vision::Jpeg j) {
			std::string data;
			if (j.storage && j.size) data.assign(reinterpret_cast<const char *>(j.storage->data() + j.offset), j.size);
			done->set_value({j.result, std::move(data)});
		});
	});
	auto [result, data] = future.get();
	jpeg = std::move(data);
	return result;
}

void stream_size(std::uint16_t &width, std::uint16_t &height, std::uint16_t &quality)
{
	auto s = on_loop([] {
		video::check_screen();
		return video::screen();
	});
	width = s.width;
	height = s.height;
	quality = s.quality;
}

// --- Screen text (kvmcapture.textReader) ---

bool read_text(const std::string &language, const std::optional<mcp::ScreenBox> &area, mcp::ScreenText &out, std::string &error)
{
	video::screen_text::Request req;
	req.language = language;
	if (area) req.crop = std::array<double, 4>{area->x, area->y, area->w, area->h};
	auto done = std::make_shared<std::promise<video::screen_text::Reply>>();
	auto future = done->get_future();
	loop::post([req, done] { video::screen_text::read(req, [done](video::screen_text::Reply r) { done->set_value(std::move(r)); }); });
	auto reply = future.get();
	if (reply.code != 0) {
		error = reply.code == -1 ? "reading the screen text needs the models of a language (Settings > AI > Text recognition)" : reply.message;
		return false;
	}
	double w = reply.width, h = reply.height;
	out.language = reply.language;
	out.width = reply.width;
	out.height = reply.height;
	out.text = video::screen_text::lines(reply.boxes);
	out.pieces_null = false;
	for (const auto &b : reply.boxes) out.pieces.push_back({b.text, b.score, {b.x / w, b.y / h, b.w / w, b.h / h}});
	return true;
}

// --- The host's state (kvmcapture.hostStatus) ---

mcp::HostState host_status()
{
	mcp::HostState s;
	bmc::AtxState atx;
	auto e = bmc::board_atx().state(atx);
	s.power_button = atx.power_button;
	s.reset_button = atx.reset_button;
	s.known = atx.power_led && !e;
	s.on = atx.power_on && !e;
	s.disk_activity = atx.hdd;
	if (e) s.power_error = e.text;
	auto [capture, signal, leds] = on_loop([] { return std::tuple{!video::hdmi::disabled(), video::vision::hdmi_signal_active(), hid::led_status()}; });
	s.capture = capture;
	s.signal = signal;
	s.keys_known = leds.known;
	s.caps_lock = leds.caps_lock;
	s.num_lock = leds.num_lock;
	s.scroll_lock = leds.scroll_lock;
	return s;
}

mcp::Devices &mcp_devices()
{
	static mcp::Devices d = [] {
		mcp::Devices x;
		x.hid = &gadget();
		x.capture = [](int quality, std::optional<int> timeout, std::string &error) {
			capture::Source src;
			src.read = read_mjpeg;
			src.screen = [](std::uint16_t &w, std::uint16_t &h) {
				std::uint16_t q;
				stream_size(w, h, q);
			};
			src.lease = []() -> std::unique_ptr<capture::Lease> { return ReadLease::acquire(nullptr); };
			return capture::snapshot(src, quality, timeout, error);
		};
		x.text_available = [] { return on_loop([] { return video::screen_text::available(); }); };
		x.text_languages = [] { return video::screen_text::language_names(); };
		x.read_text = read_text;
		x.host_status = host_status;
		x.power = [](const std::string &action) {
			std::string error;
			auto config = bmc::load(bmc::config_path, error);
			if (!error.empty()) log::error("BMC settings: {}", error);
			bmc::Power power(bmc::board_atx(), std::chrono::seconds(config.hold_seconds), "MCP");
			auto e = power.act(action);
			return e ? e.text : std::string();
		};
		x.media_images = [](std::string &error) { return storage::images(error); };
		x.media_mounted = [](bool &cdrom, std::string &error) { return storage::mounted_image_file(cdrom, error); };
		x.media_mount = [](const std::string &name, bool cdrom) { return storage::mount_image_file(name, cdrom); };
		x.wake = [](const std::string &mac, const std::string &iface) {
			std::string message;
			return network::wake("", mac, iface, message) == 0 ? std::string() : message;
		};
		return x;
	}();
	return d;
}

// --- The PicoClaw gateway's WebSocket (libcurl) ---

class CurlLink : public picoclaw::Link, public std::enable_shared_from_this<CurlLink> {
public:
	~CurlLink() override
	{
		if (curl_) curl_easy_cleanup(curl_);
		if (headers_) curl_slist_free_all(headers_);
	}

	// Connects; status: 0 connected, else the refused handshake's HTTP status (-1 none).
	static std::shared_ptr<CurlLink> dial(const std::string &url, const std::string &token, std::chrono::milliseconds timeout, int &status,
	                                      std::string &error)
	{
		auto link = std::shared_ptr<CurlLink>(new CurlLink);
		link->curl_ = curl_easy_init();
		if (!link->curl_) {
			status = -1;
			error = "curl initialization failed";
			return nullptr;
		}
		CURL *c = link->curl_;
		curl_easy_setopt(c, CURLOPT_URL, url.c_str());
		curl_easy_setopt(c, CURLOPT_CONNECT_ONLY, 2L);
		curl_easy_setopt(c, CURLOPT_CONNECTTIMEOUT_MS, static_cast<long>(timeout.count() > 0 ? timeout.count() : 10000));
		curl_easy_setopt(c, CURLOPT_TIMEOUT_MS, static_cast<long>(timeout.count() > 0 ? timeout.count() : 10000));
		curl_easy_setopt(c, CURLOPT_NOSIGNAL, 1L);
		curl_easy_setopt(c, CURLOPT_PROXY, "");
		if (!token.empty()) link->headers_ = curl_slist_append(nullptr, ("Authorization: Bearer " + token).c_str());
		if (link->headers_) curl_easy_setopt(c, CURLOPT_HTTPHEADER, link->headers_);
		CURLcode rc = curl_easy_perform(c);
		if (rc != CURLE_OK) {
			long code = 0;
			curl_easy_getinfo(c, CURLINFO_RESPONSE_CODE, &code);
			status = code > 0 ? static_cast<int>(code) : -1;
			error = std::string("websocket: ") + curl_easy_strerror(rc);
			return nullptr;
		}
		curl_easy_setopt(c, CURLOPT_TIMEOUT_MS, 0L);
		curl_socket_t fd = CURL_SOCKET_BAD;
		curl_easy_getinfo(c, CURLINFO_ACTIVESOCKET, &fd);
		link->fd_ = fd;
		status = 0;
		return link;
	}

	void run(std::function<void(const std::string &, bool)> on_message, std::function<void(int, const std::string &)> on_closed,
	         std::chrono::milliseconds read_timeout, std::chrono::milliseconds ping_interval, std::size_t max_message,
	         std::function<bool()> renew_lock)
	{
		auto self = shared_from_this();
		std::thread([self, on_message, on_closed, read_timeout, ping_interval, max_message, renew_lock] {
			std::string message;
			bool binary = false;
			int close_code = 1006;
			std::string close_reason = "relay closed: connection lost";
			auto last_rx = std::chrono::steady_clock::now(), last_ping = last_rx;
			while (!self->closed_) {
				pollfd p{static_cast<int>(self->fd_), POLLIN, 0};
				int r = ::poll(&p, 1, 500);
				auto now = std::chrono::steady_clock::now();
				if (read_timeout.count() > 0 && now - last_rx > read_timeout) {
					close_code = 4005;
					close_reason = "relay closed: read timeout";
					break;
				}
				if (ping_interval.count() > 0 && now - last_ping >= ping_interval) {
					last_ping = now;
					if (!renew_lock()) {
						close_code = 4004;
						close_reason = "session lock lost";
						break;
					}
					std::lock_guard lock(self->mu_);
					std::size_t sent = 0;
					curl_ws_send(self->curl_, "upstream", 8, &sent, 0, CURLWS_PING);
				}
				if (r <= 0) continue;
				for (;;) {
					char buf[16384];
					std::size_t got = 0;
					curl_ws_frame frame{};
					CURLcode rc;
					{
						std::lock_guard lock(self->mu_);
						const curl_ws_frame *meta = nullptr;
						rc = curl_ws_recv(self->curl_, buf, sizeof buf, &got, &meta);
						if (rc == CURLE_OK) frame = *meta;
					}
					if (rc == CURLE_AGAIN) break;
					if (rc != CURLE_OK) {
						self->closed_ = true;
						break;
					}
					last_rx = std::chrono::steady_clock::now();
					if (frame.flags & CURLWS_CLOSE) {
						close_code = got >= 2 ? (static_cast<unsigned char>(buf[0]) << 8 | static_cast<unsigned char>(buf[1])) : 1005;
						close_reason = got > 2 ? std::string(buf + 2, got - 2) : "";
						self->closed_ = true;
						break;
					}
					if (frame.flags & (CURLWS_PING | CURLWS_PONG)) continue;
					const std::size_t room = max_message - message.size();
					if (got > room || frame.bytesleft > static_cast<curl_off_t>(room - got)) {
						close_code = 1009;
						close_reason = "gateway message too large";
						self->closed_ = true;
						break;
					}
					if (frame.flags & CURLWS_BINARY) binary = true;
					message.append(buf, got);
					if (frame.bytesleft == 0 && !(frame.flags & CURLWS_CONT)) {
						on_message(message, binary);
						message.clear();
						binary = false;
					}
				}
			}
			if (!self->closing_) on_closed(close_code, close_reason);
		}).detach();
	}

	bool send_text(const std::string &data) override
	{
		if (closed_) return false;
		std::lock_guard lock(mu_);
		std::size_t done = 0;
		auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(10);
		while (done < data.size() || data.empty()) {
			std::size_t sent = 0;
			CURLcode rc = curl_ws_send(curl_, data.data() + done, data.size() - done, &sent, 0, CURLWS_TEXT);
			if (rc == CURLE_AGAIN) {
				if (std::chrono::steady_clock::now() > deadline) return false;
				pollfd p{static_cast<int>(fd_), POLLOUT, 0};
				::poll(&p, 1, 100);
				continue;
			}
			if (rc != CURLE_OK) return false;
			done += sent;
			if (data.empty()) break;
		}
		return true;
	}

	void close(int code, const std::string &reason) override
	{
		if (closing_.exchange(true)) return;
		std::string payload;
		payload += static_cast<char>((code >> 8) & 0xff);
		payload += static_cast<char>(code & 0xff);
		payload += reason.substr(0, 123);
		{
			std::lock_guard lock(mu_);
			std::size_t sent = 0;
			curl_ws_send(curl_, payload.data(), payload.size(), &sent, 0, CURLWS_CLOSE);
		}
		closed_ = true;
		if (fd_ != CURL_SOCKET_BAD) ::shutdown(static_cast<int>(fd_), SHUT_RDWR);
	}

private:
	CurlLink() = default;
	CURL *curl_ = nullptr;
	curl_slist *headers_ = nullptr;
	curl_socket_t fd_ = CURL_SOCKET_BAD;
	std::mutex mu_;
	std::atomic<bool> closed_{false}, closing_{false};
};

bool port_in_use(const std::string &host_port, std::chrono::milliseconds timeout)
{
	auto colon = host_port.rfind(':');
	std::string host = host_port.substr(0, colon), port = host_port.substr(colon + 1);
	if (!host.empty() && host.front() == '[') host = host.substr(1, host.size() - 2);
	addrinfo hints{}, *res = nullptr;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(host.c_str(), port.c_str(), &hints, &res) != 0 || !res) return false;
	bool in_use = false;
	int fd = ::socket(res->ai_family, SOCK_STREAM | SOCK_NONBLOCK | SOCK_CLOEXEC, 0);
	if (fd >= 0) {
		int r = ::connect(fd, res->ai_addr, res->ai_addrlen);
		if (r == 0) in_use = true;
		else if (errno == EINPROGRESS) {
			pollfd p{fd, POLLOUT, 0};
			if (::poll(&p, 1, static_cast<int>(timeout.count())) > 0) {
				int err = 0;
				socklen_t len = sizeof err;
				getsockopt(fd, SOL_SOCKET, SO_ERROR, &err, &len);
				in_use = err == 0;
			}
		}
		::close(fd);
	}
	freeaddrinfo(res);
	return in_use;
}

// The web page's end of the relay: a WebSocket of the server, used from any thread through the loop.
struct Downstream : picoclaw::Link {
	struct State {
		http::WebSocket *ws = nullptr;  // loop thread
		std::atomic<bool> open{true};
	};
	std::shared_ptr<State> state = std::make_shared<State>();
	bool send_text(const std::string &data) override
	{
		if (!state->open) return false;
		loop::post([s = state, data] {
			if (s->open && s->ws && !s->ws->closing()) s->ws->send(http::make_message(data, false));
		});
		return true;
	}
	void close(int code, const std::string &reason) override
	{
		state->open = false;
		loop::post([s = state, code, reason] {
			if (s->ws && !s->ws->closing()) s->ws->close(code, reason);
		});
	}
};

// One relay's work in order on a thread of its own (the dial and the sends to the gateway may wait). The thread
// ends after the jobs queued before the relay went away.
class Serial {
public:
	Serial() : q_(std::make_shared<Queue>())
	{
		std::thread([q = q_] {
			for (;;) {
				std::function<void()> job;
				{
					std::unique_lock lock(q->mu);
					q->cv.wait(lock, [&] { return q->stop || !q->jobs.empty(); });
					if (q->jobs.empty()) return;
					job = std::move(q->jobs.front());
					q->jobs.pop_front();
				}
				job();
			}
		}).detach();
	}
	~Serial()
	{
		{
			std::lock_guard lock(q_->mu);
			q_->stop = true;
		}
		q_->cv.notify_all();
	}
	void post(std::function<void()> job)
	{
		{
			std::lock_guard lock(q_->mu);
			q_->jobs.push_back(std::move(job));
		}
		q_->cv.notify_one();
	}

private:
	struct Queue {
		std::mutex mu;
		std::condition_variable cv;
		std::deque<std::function<void()>> jobs;
		bool stop = false;
	};
	std::shared_ptr<Queue> q_;
};

struct Relay : http::WsHandler, std::enable_shared_from_this<Relay> {
	std::string session;
	std::shared_ptr<Downstream> downstream = std::make_shared<Downstream>();
	std::shared_ptr<Serial> serial = std::make_shared<Serial>();
	// The loop is the sole producer; the serial worker releases the budget after each send.
	// Count the write in progress and messages waiting behind the dial, including empty ones.
	static constexpr std::size_t max_queued_messages = 64;
	std::atomic<std::size_t> queued_messages{0}, queued_bytes{0};

	void opened(http::WebSocket &ws) override
	{
		int read_timeout = 0, ping = 0, max_bytes = 0;
		picoclaw::relay_settings(read_timeout, ping, max_bytes);
		ws.max_message = static_cast<std::size_t>(max_bytes > 0 ? max_bytes : 1 << 20);
		if (ping > 0) ws.ping_period = std::chrono::seconds(std::max(1, ping / 1000));
		if (read_timeout > 0) ws.read_timeout = std::chrono::seconds(std::max(1, read_timeout / 1000));
		downstream->state->ws = &ws;
		picoclaw::set_downstream(session, downstream);
		auto self = shared_from_this();
		serial->post([self] {
			if (!self->downstream->state->open) return;
			if (!picoclaw::connect_gateway(self->session)) {
				self->downstream->state->open = false;
				return;
			}
			if (self->downstream->state->open) picoclaw::attach_downstream(self->session, self->downstream);
		});
	}
	void received(http::WebSocket &ws, std::string_view data, bool binary) override
	{
		if (!downstream->state->open || ws.closing()) return;
		if (queued_messages >= max_queued_messages || data.size() > ws.max_message - queued_bytes.load()) {
			downstream->state->open = false;
			ws.close(1013, "gateway send queue full");
			return;
		}
		++queued_messages;
		queued_bytes += data.size();
		serial->post([self = shared_from_this(), message = std::string(data), binary] {
			if (self->downstream->state->open) picoclaw::downstream_message(self->session, message, binary);
			self->queued_bytes -= message.size();
			--self->queued_messages;
		});
	}
	void closed(http::WebSocket &ws) override
	{
		downstream->state->ws = nullptr;
		downstream->state->open = false;
		int code = ws.close_code_;
		std::string reason = ws.close_reason_;
		serial->post([id = session, code, reason] { picoclaw::downstream_closed(id, code, reason); });
	}
};

void setup_platform()
{
	auto &p = picoclaw::platform();
	p.hid = &gadget();
	p.read_mjpeg = read_mjpeg;
	p.screen = stream_size;
	p.read_lease = [](const control::Cancel &cancel) -> std::unique_ptr<picoclaw::Platform::ReadLease> { return ReadLease::acquire(&cancel); };
	p.task_lease = [] {
		auto lease = on_loop([] { return std::make_shared<video::hdmi::Lease>(); });
		return std::function<void()>([lease]() mutable { loop::post([l = std::move(lease)]() mutable { l.reset(); }); });
	};
	p.after = [](std::chrono::milliseconds delay, std::function<void()> fn) {
		auto cancelled = std::make_shared<std::atomic<bool>>(false);
		loop::post([delay, fn, cancelled] {
			loop::after(delay, [fn, cancelled] {
				if (!*cancelled) loop::work(fn);
			});
		});
		return std::function<void()>([cancelled] { *cancelled = true; });
	};
	p.release_hid = release_hid;
	p.text = &mcp_devices();
	p.run = [](const std::string &command, std::chrono::milliseconds timeout, std::string &output, std::string &error) {
		auto r = process::run({"sh", "-c", command}, {.timeout = timeout});
		output = r.output;
		error = r.timed_out ? "signal: killed" : !r.started ? r.error : r.exit_code != 0 ? "exit status " + std::to_string(r.exit_code) : "";
		return r.started && !r.timed_out ? r.exit_code : -1;
	};
	p.probe = [](const std::string &url, const std::string &token, std::chrono::milliseconds timeout, std::string &error) {
		int status = -1;
		auto link = CurlLink::dial(url, token, timeout, status, error);
		if (!link) return status;
		link->close(1000, "runtime probe complete");
		return 0;
	};
	p.connect = [](const std::string &url, const std::string &token, std::chrono::milliseconds timeout,
	               std::function<void(const std::string &, bool)> on_message, std::function<void(int, const std::string &)> on_closed, std::function<bool()> renew_lock, int &status,
	               std::string &error) -> std::shared_ptr<picoclaw::Link> {
		auto link = CurlLink::dial(url, token, timeout, status, error);
		if (!link) return nullptr;
		int read_timeout = 0, ping = 0, max_bytes = 0;
		picoclaw::relay_settings(read_timeout, ping, max_bytes);
		link->run(std::move(on_message), std::move(on_closed), std::chrono::milliseconds(read_timeout), std::chrono::milliseconds(ping),
		          static_cast<std::size_t>(max_bytes > 0 ? max_bytes : 1 << 20), std::move(renew_lock));
		return link;
	};
	p.port_in_use = port_in_use;
	p.http_port = config::get().port.http;
}

// --- Routes ---

const http::Router *routes_router = nullptr;

// The loopback routes: a loopback caller with the internal token (CheckLoopbackInternalToken).
bool internal_caller(const http::Request &req)
{
	std::string addr = req.peer;
	if (!addr.empty() && addr.front() == '[') addr = addr.substr(1, addr.find(']') - 1);
	if (addr.rfind("::ffff:", 0) == 0) addr = addr.substr(7);
	bool loopback = addr == "::1" || addr.rfind("127.", 0) == 0;
	if (!loopback) return false;
	std::string token, error;
	if (!picoclaw::internal_token(token, error) || token.empty()) return false;
	std::string provided = req.header(picoclaw::internal_token_header);
	return provided.size() == token.size() && CRYPTO_memcmp(provided.data(), token.data(), token.size()) == 0;
}

// The headers the AI handlers read, kept for the blocking pool (the connection's headers are the loop's).
http::Request detach(const http::Request &req)
{
	return req.detached({"content-type", "authorization", "accept", "origin", "sec-fetch-site", "mcp-protocol-version", "last-event-id",
	                     "mcp-method", "mcp-name", "host", "x-picoclaw-session-id", "x-nanokvm-internal-token"});
}

http::Handler blocking(void (*handler)(http::Request &, http::Response &))
{
	return [handler](http::Request &req, http::Response &res) {
		http::defer(req, res, [copy = detach(req), handler](http::Response &out) mutable { handler(copy, out); });
	};
}

http::CheckedHandler loopback(void (*handler)(http::Request &, http::Response &))
{
	auto run = blocking(handler);
	return {[run](http::Request &req, http::Response &res) {
		        if (!internal_caller(req)) return api::plain_json(res, 401, "unauthorized");
		        run(req, res);
	        },
	        internal_caller};
}

void serve_mcp(http::Request &req, http::Response &res)
{
	mcp::HttpRequest r;
	r.method = req.method;
	r.body = req.body;
	r.host = req.header("host");
	r.header = [&req](std::string_view n) { return req.header(n); };
	r.header_values = [&req](std::string_view n) { return req.header_values(n); };
	std::string peer = req.peer;
	r.loopback = peer == "::1" || peer.rfind("127.", 0) == 0 || peer.rfind("::ffff:127.", 0) == 0;
	auto answer = mcp::serve(r, mcp_devices());
	res.status = answer.status;
	res.content_type = answer.content_type;
	if (!answer.allow.empty()) res.set_header("Allow", answer.allow);
	if (!answer.cache_control.empty()) res.set_header("Cache-Control", answer.cache_control);
	res.body = std::move(answer.body);
	res.done = true;
}

} // namespace

void mcp::add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/mcp/config", auth::guarded(blocking(get_config), admin));
	router.add("POST", "/api/mcp/config", auth::guarded(blocking(set_config), admin));
	router.add("POST", "/api/mcp/key/regenerate", auth::guarded(blocking(regenerate_key), admin));
	router.add("POST", "/api/mcp/host-control", auth::guarded(blocking(set_host_control_route), admin));
	for (const char *method : {"GET", "POST", "PUT", "PATCH", "HEAD", "OPTIONS", "DELETE", "CONNECT", "TRACE"})
		router.add(method, "/api/mcp", blocking(serve_mcp));
}

void control_routes::add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/ai/control/status", auth::guarded(status, admin));
	router.add("PUT", "/api/ai/control/mode", auth::guarded(blocking(set_mode), admin));
}

bool plain_http(const http::Request &request, http::Response &response)
{
	// router.LoopbackHTTPAllowedPaths: a loopback caller with the internal token reaches them over plain HTTP.
	static const std::set<std::string> paths = {"/api/picoclaw/mcp", "/api/picoclaw/runtime/session", "/api/picoclaw/screenshot",
	                                            "/api/picoclaw/actions", "/api/picoclaw/load-image"};
	if (!routes_router || !paths.count(request.path) || !internal_caller(request)) return false;
	auto &req = const_cast<http::Request &>(request);
	routes_router->dispatch(req, response);
	if (!response.pending) routes_router->finished(req, response);
	return true;
}

bool ensure_internal_token()
{
	std::string token, error;
	if (picoclaw::internal_token(token, error)) return true;
	log::error("failed to initialize picoclaw internal token: {}", error);
	return false;
}

void add_routes(http::Router &router)
{
	setup_platform();
	control_routes::release_hid = release_hid;
	auto &h = mcp::hooks();
	h.preempt_picoclaw = picoclaw::preempt_control_leases_for_mcp;
	h.stop_picoclaw = picoclaw::stop_runtime_for_mcp;
	h.release_hid = release_hid;
	h.on_control_change = [](const control::ModeStatus &s) { picoclaw::publish_control_mode_changed(s, "mcp_config"); };
	control::ai_session_holds_input = picoclaw::blocks_manual_input;

	control_routes::add_routes(router);
	mcp::add_routes(router);

	// router/picoclaw.go: the loopback routes for the runtime, the web page's routes for administrators.
	router.add("GET", "/api/picoclaw/screenshot", loopback(picoclaw::screenshot));
	router.add("POST", "/api/picoclaw/actions", loopback(picoclaw::actions));
	router.add("POST", "/api/picoclaw/mcp", loopback(picoclaw::mcp_handler));
	router.add("POST", "/api/picoclaw/load-image", loopback(picoclaw::load_image));
	router.add("GET", "/api/picoclaw/runtime/session", loopback(picoclaw::get_runtime_session));
	auth::Guard admin{{"admin"}};
	router.add("POST", "/api/picoclaw/model/config", auth::guarded(blocking(picoclaw::update_model_config), admin));
	router.add("POST", "/api/picoclaw/host-tools", auth::guarded(blocking(picoclaw::update_host_tools), admin));
	router.add("POST", "/api/picoclaw/agent/profile", auth::guarded(blocking(picoclaw::update_agent_profile), admin));
	router.add("GET", "/api/picoclaw/sessions", auth::guarded(blocking(picoclaw::list_sessions), admin));
	router.add("GET", "/api/picoclaw/sessions/:id", auth::guarded(blocking(picoclaw::get_session), admin));
	router.add("DELETE", "/api/picoclaw/sessions/:id", auth::guarded(blocking(picoclaw::delete_session), admin));
	router.add("GET", "/api/picoclaw/runtime/status", auth::guarded(blocking(picoclaw::get_runtime_status), admin));
	router.add("DELETE", "/api/picoclaw/runtime/session", auth::guarded(blocking(picoclaw::release_runtime_session), admin));
	router.add("POST", "/api/picoclaw/runtime/install", auth::guarded(blocking(picoclaw::install_runtime), admin));
	router.add("POST", "/api/picoclaw/runtime/uninstall", auth::guarded(blocking(picoclaw::uninstall_runtime), admin));
	router.add("POST", "/api/picoclaw/runtime/start", auth::guarded(blocking(picoclaw::start_runtime), admin));
	router.add("POST", "/api/picoclaw/runtime/stop", auth::guarded(blocking(picoclaw::stop_runtime), admin));
	router.ws("/api/picoclaw/gateway/ws", auth::guarded_ws([](http::Request &req, http::Response &res) -> std::shared_ptr<http::WsHandler> {
		std::string session;
		if (auto refusal = picoclaw::admit_gateway(req.query_value("session_id"), session)) {
			res.json(refusal->status, refusal->body.empty() ? std::string("\"gateway is unavailable\"") : refusal->body);
			return nullptr;
		}
		auto relay = std::make_shared<Relay>();
		relay->session = session;
		return relay;
	}, admin));

	routes_router = &router;
}

void start() { picoclaw::start(); }

} // namespace nkos::ai
