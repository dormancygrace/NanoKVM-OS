#include "http/upload.hpp"

#include "common/loop.hpp"

#include <algorithm>
#include <cctype>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <unistd.h>

namespace nkos::http {

namespace {

constexpr std::size_t max_part_headers = 16 << 10;
constexpr std::size_t queue_high = 16u << 20, queue_low = 8u << 20;

std::string lower(std::string_view s)
{
	std::string out(s);
	std::transform(out.begin(), out.end(), out.begin(), [](unsigned char c) { return std::tolower(c); });
	return out;
}

std::string_view trim(std::string_view s)
{
	while (!s.empty() && (s.front() == ' ' || s.front() == '\t')) s.remove_prefix(1);
	while (!s.empty() && (s.back() == ' ' || s.back() == '\t' || s.back() == '\r')) s.remove_suffix(1);
	return s;
}

// A parameter of a header value (boundary=..., name="..."), unquoted.
std::optional<std::string> header_param(std::string_view value, std::string_view key)
{
	std::size_t i = value.find(';');
	if (i == std::string_view::npos) return std::nullopt;
	while (i < value.size()) {
		while (i < value.size() && (value[i] == ' ' || value[i] == '\t' || value[i] == ';')) i++;
		std::size_t k0 = i;
		while (i < value.size() && value[i] != '=' && value[i] != ';') i++;
		std::string k = lower(trim(value.substr(k0, i - k0)));
		std::string v;
		if (i < value.size() && value[i] == '=') {
			i++;
			if (i < value.size() && value[i] == '"') {
				for (i++; i < value.size() && value[i] != '"'; i++) {
					if (value[i] == '\\' && i + 1 < value.size()) i++;
					v += value[i];
				}
				if (i < value.size()) i++;
			} else {
				std::size_t v0 = i;
				while (i < value.size() && value[i] != ';') i++;
				v = std::string(trim(value.substr(v0, i - v0)));
			}
		}
		if (k == key) return v;
	}
	return std::nullopt;
}

std::string base_name(std::string name)
{
	while (name.size() > 1 && name.back() == '/') name.pop_back();
	if (name == "/") return name;
	auto slash = name.rfind('/');
	if (slash != std::string::npos) name = name.substr(slash + 1);
	return name.empty() ? "." : name;
}

} // namespace

std::optional<MultipartStream> MultipartStream::make(std::string_view type, Callbacks callbacks)
{
	if (lower(trim(type.substr(0, type.find(';')))) != "multipart/form-data") return std::nullopt;
	auto boundary = header_param(type, "boundary");
	if (!boundary || boundary->empty() || boundary->size() > 200) return std::nullopt;
	MultipartStream m;
	// The first delimiter may start the body; with a leading CRLF every delimiter looks the same.
	m.buf_ = "\r\n";
	m.delimiter_ = "\r\n--" + *boundary;
	m.cb_ = std::move(callbacks);
	return m;
}

bool MultipartStream::fail(std::string why)
{
	state_ = State::failed;
	error_ = std::move(why);
	return false;
}

bool MultipartStream::feed(std::string_view chunk)
{
	if (state_ == State::failed) return false;
	if (state_ == State::done) return true;  // the epilogue is ignored
	buf_.append(chunk);
	while (step()) {
	}
	return state_ != State::failed;
}

// One step of the parse on buf_; false when it needs more input (or failed, or is done).
bool MultipartStream::step()
{
	switch (state_) {
	case State::preamble: {
		auto at = buf_.find(delimiter_);
		if (at == std::string::npos) {
			// Keep what could be the start of a delimiter.
			if (buf_.size() > delimiter_.size()) buf_.erase(0, buf_.size() - delimiter_.size());
			return false;
		}
		buf_.erase(0, at + delimiter_.size());
		state_ = State::after_delimiter;
		return true;
	}
	case State::after_delimiter: {
		if (buf_.size() < 2) return false;
		if (buf_.compare(0, 2, "--") == 0) {
			state_ = State::done;
			return false;
		}
		// Transport padding before the line end is allowed.
		auto eol = buf_.find("\r\n");
		if (eol == std::string::npos) {
			if (buf_.size() > 256) return fail("invalid multipart data");
			return false;
		}
		buf_.erase(0, eol + 2);
		state_ = State::headers;
		return true;
	}
	case State::headers: {
		std::size_t end;
		std::string_view headers;
		if (buf_.compare(0, 2, "\r\n") == 0) {
			end = 2;
		} else {
			auto at = buf_.find("\r\n\r\n");
			if (at == std::string::npos) {
				if (buf_.size() > max_part_headers) return fail("multipart part headers too large");
				return false;
			}
			headers = std::string_view(buf_).substr(0, at);
			end = at + 4;
		}
		std::string name;
		std::optional<std::string> filename;
		while (!headers.empty()) {
			auto eol = headers.find("\r\n");
			std::string_view h = headers.substr(0, eol);
			headers = eol == std::string_view::npos ? std::string_view{} : headers.substr(eol + 2);
			auto colon = h.find(':');
			if (colon == std::string_view::npos || lower(trim(h.substr(0, colon))) != "content-disposition") continue;
			std::string_view value = trim(h.substr(colon + 1));
			name = header_param(value, "name").value_or("");
			if (auto f = header_param(value, "filename")) filename = base_name(*f);
		}
		buf_.erase(0, end);
		state_ = State::data;
		if (cb_.begin && !cb_.begin(name, filename)) return fail(error_.empty() ? "upload refused" : error_);
		return true;
	}
	case State::data: {
		auto at = buf_.find(delimiter_);
		if (at == std::string::npos) {
			// Everything but what could be the start of the delimiter is part data.
			if (buf_.size() >= delimiter_.size()) {
				std::size_t n = buf_.size() - (delimiter_.size() - 1);
				if (cb_.data && !cb_.data(std::string_view(buf_).substr(0, n))) return fail("upload refused");
				buf_.erase(0, n);
			}
			return false;
		}
		if (at && cb_.data && !cb_.data(std::string_view(buf_).substr(0, at))) return fail("upload refused");
		buf_.erase(0, at + delimiter_.size());
		if (cb_.end && !cb_.end()) return fail("upload refused");
		state_ = State::after_delimiter;
		return true;
	}
	default:
		return false;
	}
}

bool MultipartStream::finish()
{
	if (state_ == State::done) return true;
	if (state_ != State::failed) fail("invalid multipart data");
	return false;
}

std::shared_ptr<FileWriter> FileWriter::open(const std::string &path, int flags, unsigned mode, std::string &error)
{
	int fd = ::open(path.c_str(), flags | O_WRONLY | O_CLOEXEC, mode);
	if (fd < 0) {
		error = std::strerror(errno);
		return nullptr;
	}
	std::shared_ptr<FileWriter> w(new FileWriter);
	w->fd_ = fd;
	w->thread_ = std::thread([raw = w.get()] { raw->run(); });
	return w;
}

FileWriter::~FileWriter()
{
	{
		std::lock_guard lock(mutex_);
		aborting_ = true;
	}
	cv_.notify_all();
	if (thread_.joinable()) thread_.join();
	if (fd_ >= 0) ::close(fd_);
}

void FileWriter::write(std::string_view data)
{
	std::unique_lock lock(mutex_);
	if (aborting_ || finishing_ || !error_.empty()) return;
	queue_.emplace_back(data);
	queued_ += data.size();
	cv_.notify_one();
	// The card is slower than the network: the request waits, not the loop (see the header).
	if (queued_ > queue_high && request_ && !holding_ && pause_request_body(request_, true)) holding_ = true;
}

void FileWriter::finish(std::function<void(const std::string &)> done)
{
	{
		std::lock_guard lock(mutex_);
		finishing_ = true;
		done_ = std::move(done);
	}
	cv_.notify_one();
}

void FileWriter::abort()
{
	{
		std::lock_guard lock(mutex_);
		aborting_ = true;
		done_ = nullptr;
	}
	cv_.notify_one();
}

void FileWriter::run()
{
	while (true) {
		std::string chunk;
		{
			std::unique_lock lock(mutex_);
			cv_.wait(lock, [this] { return !queue_.empty() || finishing_ || aborting_; });
			if (aborting_) return;
			if (queue_.empty()) break;  // finishing, all written
			chunk = std::move(queue_.front());
			queue_.pop_front();
		}
		std::string failure;
		for (std::size_t off = 0; off < chunk.size();) {
			ssize_t n = ::write(fd_, chunk.data() + off, chunk.size() - off);
			if (n < 0 && errno == EINTR) continue;
			if (n <= 0) {
				failure = n < 0 ? std::strerror(errno) : "short write";
				break;
			}
			off += static_cast<std::size_t>(n);
			written_ += static_cast<std::uint64_t>(n);
		}
		std::lock_guard lock(mutex_);
		queued_ -= chunk.size();
		if (!failure.empty() && error_.empty()) {
			// Nothing more is written; finish() or abort() reports it. write() queues nothing after an error.
			error_ = failure;
			queue_.clear();
			queued_ = 0;
		}
		// The request may be gone by the time this runs on the loop; resuming one that is gone does nothing.
		if (queued_ < queue_low && holding_.exchange(false))
			loop::post([id = request_] { pause_request_body(id, false); });
	}
	std::string result;
	{
		std::lock_guard lock(mutex_);
		result = error_;
	}
	if (result.empty() && ::fsync(fd_) != 0) result = std::strerror(errno);
	std::function<void(const std::string &)> done;
	{
		std::lock_guard lock(mutex_);
		done = std::move(done_);
	}
	if (done) loop::post([done = std::move(done), result] { done(result); });
}

} // namespace nkos::http
