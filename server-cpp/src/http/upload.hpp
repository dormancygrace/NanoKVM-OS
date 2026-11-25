#pragma once

// Request bodies taken as they arrive (uploads of images and files, larger than the server keeps in memory), with
// a multipart/form-data parser that hands out parts as they come and a file writer that puts the bytes on the card
// from a thread of its own.
//
// HTTP/2 gives no way to pause a stream's reception in libwebsockets: what the client sends arrives. The writer keeps
// at most 16 MiB queued; beyond that the loop thread waits for the card (an upload faster than the card holds the
// loop for as long as the card needs, which is what the Go server's request goroutine cost the client).

#include "http/router.hpp"

#include <atomic>
#include <condition_variable>
#include <deque>
#include <functional>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <thread>

namespace nkos::http {

// multipart/form-data, fed in pieces of any size. Part headers are limited to 16 KiB.
class MultipartStream {
public:
	struct Callbacks {
		// A part starts; filename is set for a file part (the base of the browser's name, as Go's FileName). false
		// stops the parse (error() then says why).
		std::function<bool(const std::string &name, const std::optional<std::string> &filename)> begin;
		std::function<bool(std::string_view data)> data;
		std::function<bool()> end;
	};
	// nullopt when the content type is not multipart/form-data with a boundary.
	static std::optional<MultipartStream> make(std::string_view content_type, Callbacks callbacks);

	bool feed(std::string_view chunk);
	// The body is all in: true when it ended with the closing delimiter.
	bool finish();
	const std::string &error() const { return error_; }

private:
	enum class State { preamble, headers, data, after_delimiter, done, failed };
	std::string delimiter_;  // "\r\n--" + boundary
	Callbacks cb_;
	State state_ = State::preamble;
	std::string buf_;
	std::string error_;
	bool fail(std::string why);
	bool step();
};

// Writes to a file from a thread of its own. write() from the loop thread; done gets the result on the loop thread
// after finish() (empty error: written and synced), or nothing after abort(). write() never waits for the disk:
// with more than 16 MiB queued the writer holds the body of the request that feeds it (feeds(), see
// http::pause_request_body) and lets it go on once less than 8 MiB is queued, so a slow card slows that upload
// and nothing else on the loop.
class FileWriter {
public:
	// Opens path with flags (O_WRONLY and O_CLOEXEC are added) and mode; nullptr with error set when it fails.
	static std::shared_ptr<FileWriter> open(const std::string &path, int flags, unsigned mode, std::string &error);
	~FileWriter();
	void write(std::string_view data);
	// The request whose body is written here. Loop thread, before the first write.
	void feeds(std::uint64_t request_id) { request_ = request_id; }
	// Whether the request is held for the disk now (a stall timer must not count that time). Loop thread.
	bool holding() const { return holding_; }
	void finish(std::function<void(const std::string &error)> done);
	void abort();
	std::uint64_t written() const { return written_; }
	int fd() const { return fd_; }

private:
	FileWriter() = default;
	void run();
	int fd_ = -1;
	std::thread thread_;
	std::mutex mutex_;
	std::condition_variable cv_;
	std::uint64_t request_ = 0;
	std::atomic<bool> holding_{false};
	std::deque<std::string> queue_;
	std::size_t queued_ = 0;
	bool finishing_ = false, aborting_ = false;
	std::string error_;
	std::function<void(const std::string &)> done_;
	std::uint64_t written_ = 0;
};

} // namespace nkos::http
