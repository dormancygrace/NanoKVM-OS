// Remote media on the virtual drive, as the Go server's service/storage/remote*.go: a session reserves the empty
// drive, opens its source (a file in the browser read over the page's WebSocket, or an HTTP or SMB URL the device
// reads itself), attaches it to /dev/nbd0 and puts that on the drive as a CD; it ends with a disconnect, an eject
// by the computer, the source failing or (for the browser) the page going away. Also the start-up cleanup of a
// drive a previous server left naming the NBD device.

#include "storage/internal.hpp"
#include "storage/storage.hpp"

#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/websocket.hpp"

#include <glaze/glaze.hpp>

#include <cerrno>
#include <cstring>
#include <deque>
#include <fcntl.h>
#include <sys/file.h>
#include <unistd.h>

namespace nkos::storage {

struct RemoteSession {
	std::string kind, name = "remote.iso", location, actor;
	std::uint64_t size = 0;
	bool mounted = false;  // under service().mu
	std::atomic<std::uint64_t> read_bytes{0};
	Event closed, released;
	std::shared_ptr<Context> ctx;  // URL sources: cancelled by close
	// Browser sessions: sends a JSON text message to the page, closes its socket (both through the loop).
	std::function<void(std::string)> send;
	std::function<void()> close_socket;
	// The outcome of connecting, once.
	std::mutex ready_mu;
	std::condition_variable ready_cv;
	std::optional<Error> ready;

	void close()
	{
		if (closed.fired()) return;
		closed.fire();
		if (ctx) ctx->cancel();
		if (close_socket) close_socket();
	}
	void set_ready(const Error &e)
	{
		std::lock_guard lock(ready_mu);
		if (ready) return;
		ready = e;
		ready_cv.notify_all();
	}
};

namespace {

const char *err_remote_ejected = "Remote ISO was ejected or USB storage was disabled";

std::string trimmed_file(const std::string &path)
{
	std::string data;
	(void)go::read_file(path, data);
	return go::trim_space(data);
}

Error reserve_remote(const std::shared_ptr<RemoteSession> &r)
{
	Service &s = service();
	std::lock_guard lock(s.mu);
	if (s.remote) return Error::of("Remote media is already connected", "in-use");
	if (s.netboot) return Error::of("Turn off network boot before connecting an ISO", "in-use");
	std::lock_guard h(gadget_mutex());
	if (require_active_usb_storage()) return Error::of("Enable USB storage before connecting an ISO");
	std::string current;
	if (go::read_file(mount_device, current)) return Error::of("Cannot read USB storage state");
	if (!go::trim_space(current).empty()) return Error::of("Unmount the current image first", "in-use");
	s.remote = r;
	s.remote_error.clear();
	s.remote_kind = r->kind;
	return {};
}

void release_remote(const std::shared_ptr<RemoteSession> &r, const Error &err)
{
	r->close();
	{
		Service &s = service();
		std::lock_guard lock(s.mu);
		if (s.remote == r) {
			s.remote = nullptr;
			if (err && err.tag != "canceled" && err.text != err_remote_ejected) s.remote_error = err.text;
		}
	}
	r->released.fire();
}

Error canceled_error() { return Error::of("context canceled", "canceled"); }

// The common NBD and gadget lifetime of a session. close cancels the source before the kernel attachment goes, so
// a blocked network read cannot hold up the USB teardown.
Error serve_remote(const std::shared_ptr<RemoteSession> &r, Source &source, const std::function<Error()> &mounted)
{
	std::unique_ptr<NbdDevice> device;
	std::mutex wake_mu;
	std::condition_variable wake;
	std::optional<Error> read_error;
	auto notify = [&] {
		std::lock_guard lock(wake_mu);
		wake.notify_all();
	};
	auto closed_sub = r->closed.subscribe(notify);
	std::uint64_t stopped_sub = 0;

	auto body = [&]() -> Error {
		if (r->closed.fired()) return canceled_error();
		if (auto e = validate_optical(r->size, dvd_supported())) return e;
		Error open_error;
		bool browser = r->kind == "browser";
		device = NbdDevice::open(r->size, [&, browser](std::uint64_t offset, std::uint32_t length, std::span<const char> &data) {
			Error e = source.read_block(offset, length, data);
			if (!e && data.size() != length) e = Error::of("Remote image returned an incomplete block");
			if (e) {
				std::lock_guard lock(wake_mu);
				if (!read_error) read_error = e;
				wake.notify_all();
			} else if (!browser) {
				r->read_bytes += data.size();
			}
			return e;
		}, open_error);
		if (!device) return open_error;
		stopped_sub = device->stopped.subscribe(notify);
		if (r->closed.fired()) return canceled_error();
		Error err;
		{
			Service &s = service();
			std::lock_guard lock(s.mu);
			std::lock_guard h(gadget_mutex());
			err = require_active_usb_storage();
			if (!err) {
				std::string current;
				if (go::read_file(mount_device, current) || !go::trim_space(current).empty())
					err = Error::of("USB medium changed while connecting");
			}
			bool changed = false;
			DriveRestore restore;
			if (!err) err = set_drive(true, inquiry("remote"), changed, restore);
			if (!err && (err = go::write_file(mount_device, nbd_device, 0600))) restore();
			if (!err && changed) {
				// A failed reconnection leaves the medium in: the cleanup ejects it, and the drive is put back
				// once it is empty.
				if ((err = reconnect_usb())) {
					if (!go::write_file(mount_device, "\n", 0600) || !go::write_file(forced_eject_device, "1", 0600)) restore();
				}
			}
			if (!err) r->mounted = true;
		}
		if (err) return Error::of("Could not connect the remote ISO to USB storage");
		if (mounted)
			if (auto e = mounted()) return e;
		for (;;) {
			{
				std::unique_lock lock(wake_mu);
				wake.wait_for(lock, std::chrono::seconds(2));
				if (r->closed.fired()) return canceled_error();
				if (read_error) return *read_error;
				if (device->stopped.fired()) return Error::of("Remote media device disconnected");
			}
			std::lock_guard h(gadget_mutex());
			std::string data;
			Error read_err = go::read_file(mount_device, data);
			Error active = require_active_usb_storage();
			if (read_err || active || go::trim_space(data) != nbd_device) return Error::of(err_remote_ejected);
		}
	};
	Error result = body();

	r->closed.unsubscribe(closed_sub);
	if (result && result.tag != "canceled" && r->send)
		r->send("{\"message\":" + gojson::quote(result.text) + ",\"type\":\"error\"}");
	r->close();
	if (device) {
		device->stopped.unsubscribe(stopped_sub);
		device->close();
	}
	source.close();
	if (device) {
		Service &s = service();
		std::lock_guard lock(s.mu);
		std::lock_guard h(gadget_mutex());
		if (trimmed_file(mount_device) == nbd_device)
			if (go::write_file(mount_device, "\n", 0600)) (void)go::write_file(forced_eject_device, "1", 0600);
		if (r->mounted) record_medium(r->actor, "");
		r->mounted = false;
	}
	return result;
}

using Opener = std::function<std::unique_ptr<Source>(const std::shared_ptr<Context> &, Error &)>;

// Reserves the drive and connects a URL source in a thread of its own; the session's ready gets the outcome.
std::shared_ptr<RemoteSession> start_remote(const std::string &kind, const std::string &location, const std::string &actor,
                                            Opener open, Error &error)
{
	auto r = std::make_shared<RemoteSession>();
	r->kind = kind;
	r->location = location;
	r->actor = actor;
	r->ctx = Context::make();
	if ((error = reserve_remote(r))) {
		r->close();
		return nullptr;
	}
	std::thread([r, open = std::move(open)] {
		Error err;
		auto source = open(r->ctx, err);
		if (source) {
			{
				std::lock_guard lock(service().mu);
				r->name = source->name;
				r->size = source->size;
			}
			err = serve_remote(r, *source, [r]() -> Error {
				record_medium(r->actor, r->name);
				r->set_ready({});
				return {};
			});
		}
		r->set_ready(err);
		source.reset();
		release_remote(r, err);
	}).detach();
	return r;
}

// --- The browser's file ---

constexpr std::uint32_t remote_sequential_min = 64u << 10;
constexpr auto remote_read_timeout = std::chrono::seconds(15);

char *aligned_buffer(std::unique_ptr<char[]> &owner)
{
	owner.reset(new char[max_read + 63]);
	auto p = reinterpret_cast<std::uintptr_t>(owner.get());
	return reinterpret_cast<char *>((p + 63) & ~std::uintptr_t(63));
}

// The browser source (remote_reader.go, remote_readahead.go): one NBD consumer, the page's answers arriving on the
// loop thread. Each read asks the page for exactly its bytes until two sequential transitions of at least 64 KiB
// show a stream; then a producer keeps up to two blocks of 1 MiB coming ahead.
class BrowserReader : public Source {
public:
	explicit BrowserReader(std::shared_ptr<RemoteSession> session) : session_(std::move(session))
	{
		output_ = aligned_buffer(output_owner_);
		watchdog_ = std::thread([this] { watch(); });
	}
	~BrowserReader() override
	{
		close();
		{
			std::lock_guard lock(watch_mu_);
			watch_quit_ = true;
		}
		watch_cv_.notify_all();
		watchdog_.join();
	}

	void close() override
	{
		session_->close();
		std::lock_guard lock(mu_);
		if (pipe_) {
			pipe_->stop();
			pipe_.reset();
		}
	}

	// A message of the page (loop thread).
	void receive(std::string_view message, bool binary)
	{
		std::unique_lock lock(slot_mu_);
		if (!binary || !slot_ || slot_->answered) {
			// Unsolicited data must never overwrite a lent NBD slice.
			lock.unlock();
			session_->close();
			return;
		}
		Error err;
		if (message.size() < 4) {
			err = Error::of(message.empty() ? "EOF" : "unexpected EOF");
		} else {
			auto *p = reinterpret_cast<const unsigned char *>(message.data());
			std::uint32_t id = static_cast<std::uint32_t>(p[0]) << 24 | static_cast<std::uint32_t>(p[1]) << 16 |
			                   static_cast<std::uint32_t>(p[2]) << 8 | p[3];
			std::string_view payload = message.substr(4);
			if (id != slot_->id) err = Error::of("wrong browser response ID");
			else if (payload.size() < slot_->target.size()) err = Error::of(payload.empty() ? "EOF" : "unexpected EOF");
			else if (payload.size() > slot_->target.size()) err = Error::of("oversized browser response");
			else std::memcpy(slot_->target.data(), payload.data(), payload.size());
		}
		slot_->answered = true;
		slot_->err = err;
		slot_cv_.notify_all();
		if (err) {
			lock.unlock();
			session_->close();
		}
	}

	Error read_block(std::uint64_t offset, std::uint32_t length, std::span<const char> &data) override
	{
		std::lock_guard lock(mu_);
		if (session_->closed.fired()) return Error::of("browser disconnected");
		if (length == 0 || length > max_read || offset > session_->size || length > session_->size - offset)
			return Error::of("invalid read bounds");
		// One deadline covers a queued prefetch, draining it on a seek, the replacement request and its answer.
		// The session closes when it passes: the protocol cannot cancel an answer without losing its framing.
		arm();
		Error err = read_locked(offset, length, data);
		if (disarm()) {
			data = {};
			return Error::of("browser read timed out");
		}
		return err;
	}

	// One request to the page and its answer into target.
	Error fetch(std::uint64_t offset, std::span<char> target)
	{
		auto &s = *session_;
		std::uint32_t id;
		{
			std::lock_guard lock(slot_mu_);
			if (s.closed.fired()) return Error::of("browser disconnected");
			id = ++read_id_;
			slot_ = Slot{id, target};
		}
		s.send("{\"id\":" + std::to_string(id) + ",\"length\":" + std::to_string(target.size()) + ",\"offset\":" +
		       std::to_string(offset) + ",\"type\":\"read\"}");
		auto sub = s.closed.subscribe([this] {
			std::lock_guard lock(slot_mu_);
			slot_cv_.notify_all();
		});
		std::unique_lock lock(slot_mu_);
		bool answered = slot_cv_.wait_for(lock, remote_read_timeout, [&] { return slot_->answered || s.closed.fired(); });
		Slot slot = *slot_;
		slot_.reset();
		lock.unlock();
		s.closed.unsubscribe(sub);
		if (slot.answered && slot.err) {
			s.close();
			return slot.err;
		}
		if (slot.answered) {
			s.read_bytes += target.size();
			return {};
		}
		if (s.closed.fired()) return Error::of("browser disconnected");
		(void)answered;
		s.close();
		return Error::of("browser read timed out");
	}

private:
	struct Slot {
		std::uint32_t id;
		std::span<char> target;
		bool answered = false;
		Error err;
	};

	// Two buffers bound both memory and overfetch: the current chunk stays lent until the next NBD read, the other
	// can receive the next block.
	class ReadAhead {
	public:
		ReadAhead(BrowserReader &r, std::uint64_t offset) : r_(r), position_(offset)
		{
			for (int i = 0; i < 2; i++) {
				if (!r.prefetch_[i]) r.prefetch_[i] = aligned_buffer(r.prefetch_owner_[i]);
				free_.push_back(r.prefetch_[i]);
			}
			sub_ = r.session_->closed.subscribe([this] {
				std::lock_guard lock(mu_);
				cv_.notify_all();
			});
			producer_ = std::thread([this, offset]() mutable { produce(offset); });
		}
		~ReadAhead() { stop(); }

		void stop()
		{
			{
				std::lock_guard lock(mu_);
				cancel_ = true;
			}
			cv_.notify_all();
			if (producer_.joinable()) producer_.join();
			r_.session_->closed.unsubscribe(sub_);
		}

		std::uint64_t position() const { return position_; }

		Error advance()
		{
			std::unique_lock lock(mu_);
			if (current_) {
				position_ = current_->offset + current_->length;
				free_.push_back(current_->buffer);
				current_.reset();
				cv_.notify_all();
			}
			cv_.wait(lock, [this] { return !chunks_.empty() || done_; });
			if (chunks_.empty()) return Error::of("EOF");
			Chunk c = chunks_.front();
			chunks_.pop_front();
			if (c.err) return c.err;
			current_ = c;
			return {};
		}

		// The data, whether the window had it (false: the caller reads directly), or an error.
		Error read(std::uint64_t offset, std::uint32_t length, char *output, std::span<const char> &data, bool &ok)
		{
			ok = false;
			if (!current_) {
				if (offset < position_) return {};
				if (auto e = advance()) return e;
			}
			if (offset == current_->offset + current_->length) {
				if (auto e = advance()) return e;
			}
			const Chunk *c = &*current_;
			if (offset < c->offset || offset >= c->offset + c->length) return {};
			std::size_t start = static_cast<std::size_t>(offset - c->offset);
			if (start + length <= c->length) {
				data = {c->buffer + start, length};
				ok = true;
				return {};
			}
			std::size_t copied = c->length - start;
			std::memcpy(output, c->buffer + start, copied);
			while (copied < length) {
				if (auto e = advance()) return e;
				if (current_->offset != offset + copied) return Error::of("noncontiguous browser prefetch");
				std::size_t n = std::min<std::size_t>(length - copied, current_->length);
				std::memcpy(output + copied, current_->buffer, n);
				copied += n;
			}
			data = {output, length};
			ok = true;
			return {};
		}

	private:
		struct Chunk {
			std::uint64_t offset = 0;
			std::size_t length = 0;
			char *buffer = nullptr;
			Error err;
		};

		void produce(std::uint64_t offset)
		{
			auto &session = *r_.session_;
			while (offset < session.size) {
				char *buffer;
				{
					std::unique_lock lock(mu_);
					cv_.wait(lock, [&] { return cancel_ || session.closed.fired() || !free_.empty(); });
					if (cancel_ || session.closed.fired()) break;
					buffer = free_.front();
					free_.pop_front();
				}
				std::size_t length = static_cast<std::size_t>(std::min<std::uint64_t>(max_read, session.size - offset));
				Error err = r_.fetch(offset, std::span<char>(buffer, length));
				{
					std::lock_guard lock(mu_);
					if (cancel_ || session.closed.fired()) break;
					chunks_.push_back({offset, length, buffer, err});
					cv_.notify_all();
				}
				if (err) break;
				offset += length;
			}
			std::lock_guard lock(mu_);
			done_ = true;
			cv_.notify_all();
		}

		BrowserReader &r_;
		std::mutex mu_;
		std::condition_variable cv_;
		std::deque<char *> free_;
		std::deque<Chunk> chunks_;
		std::optional<Chunk> current_;
		std::uint64_t position_;
		bool cancel_ = false, done_ = false;
		std::uint64_t sub_ = 0;
		std::thread producer_;
	};

	Error read_locked(std::uint64_t offset, std::uint32_t length, std::span<const char> &data)
	{
		if (offset == next_ && length >= remote_sequential_min) {
			if (streak_ < 2) streak_++;
		} else {
			streak_ = 0;
		}
		next_ = offset + length;
		if (!pipe_) {
			if (streak_ < 2) return read_direct(offset, length, data);
			pipe_ = std::make_unique<ReadAhead>(*this, offset);
		}
		bool ok = false;
		Error err = pipe_->read(offset, length, output_, data, ok);
		if (err || ok) return err;
		// A seek drains the one outstanding answer: the protocol has no cancellation.
		pipe_->stop();
		pipe_.reset();
		streak_ = 0;
		return read_direct(offset, length, data);
	}

	Error read_direct(std::uint64_t offset, std::uint32_t length, std::span<const char> &data)
	{
		if (auto e = fetch(offset, std::span<char>(output_, length))) return e;
		data = {output_, length};
		return {};
	}

	void arm()
	{
		std::lock_guard lock(watch_mu_);
		armed_ = true;
		fired_ = false;
		deadline_ = SteadyClock::now() + remote_read_timeout;
		watch_cv_.notify_all();
	}

	bool disarm()
	{
		std::lock_guard lock(watch_mu_);
		armed_ = false;
		return fired_;
	}

	void watch()
	{
		std::unique_lock lock(watch_mu_);
		while (!watch_quit_) {
			if (!armed_) {
				watch_cv_.wait(lock);
				continue;
			}
			if (SteadyClock::now() >= deadline_) {
				fired_ = true;
				armed_ = false;
				session_->close();
				continue;
			}
			watch_cv_.wait_until(lock, deadline_);
		}
	}

	std::shared_ptr<RemoteSession> session_;
	std::mutex mu_;  // read and close
	std::unique_ptr<ReadAhead> pipe_;
	std::unique_ptr<char[]> output_owner_, prefetch_owner_[2];
	char *output_ = nullptr, *prefetch_[2] = {nullptr, nullptr};
	std::uint64_t next_ = ~0ull;
	std::uint8_t streak_ = 0;
	std::mutex slot_mu_;
	std::condition_variable slot_cv_;
	std::optional<Slot> slot_;
	std::uint32_t read_id_ = 0;
	std::mutex watch_mu_;
	std::condition_variable watch_cv_;
	bool armed_ = false, fired_ = false, watch_quit_ = false;
	SteadyClock::time_point deadline_;
	std::thread watchdog_;
};

struct Hello {
	std::string type, name;
	std::uint64_t size = 0;
};

} // namespace

} // namespace nkos::storage

template <>
struct glz::meta<nkos::storage::Hello> {
	using T = nkos::storage::Hello;
	static constexpr auto value = object("type", &T::type, "name", &T::name, "size", &T::size);
};

namespace nkos::storage {

namespace {

// The page's WebSocket (loop thread): its first message opens the session, the following ones answer reads.
struct BrowserHandler : http::WsHandler, std::enable_shared_from_this<BrowserHandler> {
	http::WebSocket *ws = nullptr;
	std::shared_ptr<RemoteSession> session;
	std::shared_ptr<BrowserReader> reader;
	bool hello = false;

	void opened(http::WebSocket &socket) override
	{
		ws = &socket;
		socket.max_message = max_read + 4;
		socket.read_timeout = std::chrono::seconds(10);
		std::weak_ptr<BrowserHandler> weak = weak_from_this();
		session->send = [weak](std::string json) {
			loop::post([weak, json = std::move(json)] {
				if (auto self = weak.lock(); self && self->ws) self->ws->send(http::make_message(json, false));
			});
		};
		session->close_socket = [weak] {
			loop::post([weak] {
				// A close that sends what is queued first: an error message reaches the page before the close.
				if (auto self = weak.lock(); self && self->ws) self->ws->close(1000, "");
			});
		};
	}

	void fail(const std::string &message)
	{
		ws->send(http::make_message("{\"message\":" + gojson::quote(message) + ",\"type\":\"error\"}", false));
		ws->close(1000, "");
		session->close();
	}

	void received(http::WebSocket &socket, std::string_view data, bool binary) override
	{
		if (hello) {
			if (reader) reader->receive(data, binary);
			else session->close();
			return;
		}
		hello = true;
		Hello h;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(h, std::string(data))) {
			socket.abort();
			session->close();
			return;
		}
		bool bad = h.type != "open" || !go::valid_utf8(h.name) || h.name.size() > 200 ||
		           h.name.find_first_of(std::string_view("\0\r\n", 3)) != std::string::npos ||
		           !go::equal_fold(go::ext(h.name), ".iso");
		if (bad) return fail("Select an ISO file");
		if (auto e = validate_image(h.size)) return fail(e.text);
		if (auto e = validate_optical(h.size, dvd_supported())) return fail(e.text);
		session->name = go::base(h.name);
		session->size = h.size;
		// Keep a reader active while the computer is idle, so a disconnect or a revoked session tears the block
		// device down at once.
		socket.read_timeout = std::chrono::seconds(45);
		reader = std::make_shared<BrowserReader>(session);
		auto r = session;
		auto source = reader;
		std::thread([r, source] {
			if (auto e = reserve_remote(r)) {
				r->send("{\"message\":" + gojson::quote(e.text) + ",\"type\":\"error\"}");
				r->close();
				return;
			}
			(void)serve_remote(r, *source, [r]() -> Error {
				record_medium(r->actor, r->name);
				r->send("{\"name\":" + gojson::quote(r->name) + ",\"size\":" + std::to_string(r->size) + ",\"type\":\"mounted\"}");
				return {};
			});
			release_remote(r, {});
		}).detach();
	}

	void closed(http::WebSocket &) override
	{
		ws = nullptr;
		session->close();
	}
};

std::string web_actor(const http::Request &req) { return req.auth ? req.auth->username + " (web)" : std::string(); }

// ShouldBindJSON into the URL options (a body of at most 8 KiB).
struct UrlOptionsJson {
	std::optional<std::string> url, username, password, domain;
};

} // namespace

} // namespace nkos::storage

template <>
struct glz::meta<nkos::storage::UrlOptionsJson> {
	using T = nkos::storage::UrlOptionsJson;
	static constexpr auto value =
		object("url", &T::url, "username", &T::username, "password", &T::password, "domain", &T::domain);
};

namespace nkos::storage {

namespace {

bool bind_url_options(const http::Request &req, UrlOptionsJson &o)
{
	if (req.body.size() > 8192) return false;
	auto a = req.body.find_first_not_of(" \t\r\n");
	if (a == std::string::npos || req.body[a] != '{') return false;
	return !glz::read<glz::opts{.error_on_unknown_keys = false}>(o, req.body);
}

void connect_remote_url(http::Request &req, http::Response &res, const std::string &kind, Opener open)
{
	std::string actor = web_actor(req);
	http::defer(req, res, [kind, actor, open = std::move(open)](http::Response &out) {
		Error err;
		if (!start_remote(kind, "", actor, open, err)) {
			out.json(409, "{\"code\":-2,\"msg\":" + api::quote(err.text) + "}");
			return;
		}
		api::ok(out);
	});
}

// A server that crashed or restarted leaves the drive naming the dead NBD device; the PID the kernel keeps for it is
// then the old server's.
bool nbd_owner_running(const std::string &pid)
{
	if (pid.empty() || pid == std::to_string(::getpid())) return false;
	std::string comm;
	return !go::read_file("/proc/" + pid + "/comm", comm) && go::trim_space(comm) == "NanoKVM-Server";
}

} // namespace

void cleanup_remote()
{
	// The same lock as NbdDevice::open, held while configfs is examined: the PID alone races with a new attachment.
	int fd = ::open(nbd_device.c_str(), O_RDONLY | O_CLOEXEC);
	if (fd < 0) return;
	if (::flock(fd, LOCK_EX | LOCK_NB) != 0) {
		::close(fd);
		return;
	}
	{
		std::lock_guard h(gadget_mutex());
		std::string pid;
		Error e = go::read_file(nbd_pid, pid);
		if ((!e || e.sys == ENOENT) && !nbd_owner_running(go::trim_space(pid))) {
			if (trimmed_file(mount_device) == nbd_device)
				if (go::write_file(mount_device, "\n", 0600)) (void)go::write_file(forced_eject_device, "1", 0600);
		}
	}
	::close(fd);
}

void add_remote_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	router.add("GET", "/api/storage/remote", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			Service &s = service();
			std::lock_guard lock(s.mu);
			std::string data;
			if (auto r = s.remote) {
				data = "{\"connected\":true,\"mounted\":" + std::string(r->mounted ? "true" : "false") +
				       ",\"name\":" + gojson::quote(r->name) + ",\"readBytes\":" + std::to_string(r->read_bytes.load()) +
				       ",\"size\":" + std::to_string(r->size) + ",\"source\":" + gojson::quote(r->kind) + "}";
			} else {
				data = "{\"connected\":false,\"error\":" + gojson::quote(s.remote_error) + ",\"source\":" +
				       gojson::quote(s.remote_kind) + "}";
			}
			api::send(out, 0, "success", data);
		});
	}, admin));
	router.add("POST", "/api/storage/remote/disconnect", auth::guarded([](http::Request &req, http::Response &res) {
		http::defer(req, res, [](http::Response &out) {
			std::shared_ptr<RemoteSession> r;
			{
				std::lock_guard lock(service().mu);
				r = service().remote;
			}
			if (r) r->close();
			api::ok(out);
		});
	}, admin));
	router.add("POST", "/api/storage/remote/http", auth::guarded([](http::Request &req, http::Response &res) {
		UrlOptionsJson j;
		if (!bind_url_options(req, j)) return api::err(res, -1, "Invalid HTTP image options");
		HttpOptions o{j.url.value_or(""), j.username.value_or(""), j.password.value_or("")};
		if (auto e = validate_http_options(o)) return api::err(res, -1, e.text);
		connect_remote_url(req, res, "http", [o](const std::shared_ptr<Context> &ctx, Error &e) { return open_http(ctx, o, e); });
	}, admin));
	router.add("POST", "/api/storage/remote/smb", auth::guarded([](http::Request &req, http::Response &res) {
		UrlOptionsJson j;
		if (!bind_url_options(req, j)) return api::err(res, -1, "Invalid SMB image options");
		SmbOptions o{j.url.value_or(""), j.username.value_or(""), j.password.value_or(""), j.domain.value_or("")};
		if (auto e = validate_smb_options(o)) return api::err(res, -1, e.text);
		connect_remote_url(req, res, "smb", [o](const std::shared_ptr<Context> &ctx, Error &e) { return open_smb(ctx, o, e); });
	}, admin));
	router.ws("/api/storage/remote/connect", auth::guarded_ws([](http::Request &req, http::Response &) -> std::shared_ptr<http::WsHandler> {
		auto h = std::make_shared<BrowserHandler>();
		h->session = std::make_shared<RemoteSession>();
		h->session->kind = "browser";
		h->session->actor = web_actor(req);
		return h;
	}, {{"admin"}, false, true}));
}

// --- For the other families ---

std::optional<Medium> medium(std::string &error)
{
	Service &s = service();
	std::shared_ptr<RemoteSession> r;
	bool netboot;
	Medium remote;
	{
		std::lock_guard lock(s.mu);
		r = s.remote;
		netboot = s.netboot != nullptr;
		if (r) remote = {r->mounted, r->kind, r->location.empty() ? r->name : r->location, true};
	}
	if (r) return remote;
	std::string image;
	{
		std::lock_guard h(gadget_mutex());
		if (require_active_usb_storage()) {
			error = "USB storage is off: enable it in Settings > USB Gadget";
			return std::nullopt;
		}
	}
	if (auto e = mounted_image(image)) {
		error = e.text;
		return std::nullopt;
	}
	if (image.empty()) return Medium{};
	std::string flag;
	if (auto e = go::read_file(cdrom_flag, flag)) {
		error = e.text;
		return std::nullopt;
	}
	bool cdrom = go::trim_space(flag) == "1";
	return Medium{true, netboot ? "netboot" : "local", image, cdrom};
}

std::string insert_url(const std::string &raw, const std::string &username, const std::string &password,
                       std::chrono::milliseconds timeout)
{
	auto u = go::parse_url(raw);
	if (!u) return "The image address is not a URL";
	go::Url shown;
	shown.scheme = u->scheme;
	shown.host = u->host;
	shown.path = u->path;
	Error err;
	std::shared_ptr<RemoteSession> r;
	if (go::equal_fold(u->scheme, "smb")) {
		SmbOptions o{raw, username, password, ""};
		if (auto e = validate_smb_options(o)) return e.text;
		r = start_remote("smb", shown.string(), "", [o](const std::shared_ptr<Context> &ctx, Error &e) { return open_smb(ctx, o, e); }, err);
	} else {
		HttpOptions o{raw, username, password};
		if (auto e = validate_http_options(o)) return e.text;
		r = start_remote("http", shown.string(), "", [o](const std::shared_ptr<Context> &ctx, Error &e) { return open_http(ctx, o, e); }, err);
	}
	if (!r) return err.text;
	std::unique_lock lock(r->ready_mu);
	if (r->ready_cv.wait_for(lock, timeout, [&] { return r->ready.has_value(); })) return r->ready->text;
	lock.unlock();
	r->close();
	r->released.wait();
	return "The image did not connect in time";
}

std::string eject_medium()
{
	Service &s = service();
	std::shared_ptr<RemoteSession> r;
	bool netboot;
	{
		std::lock_guard lock(s.mu);
		r = s.remote;
		netboot = s.netboot != nullptr;
	}
	if (r) {
		r->close();
		if (!r->released.wait_until(SteadyClock::now() + std::chrono::seconds(10)))
			return "The remote image did not disconnect in time";
		return {};
	}
	if (netboot) return "Turn off network boot first";
	std::lock_guard lock(s.mu);
	if (s.remote) return "Disconnect remote media before mounting another image";
	if (s.netboot) return "Turn off network boot before mounting another image";
	return mount_local_image("", false, true).msg;
}

} // namespace nkos::storage
