#include "video/frame.hpp"

#include <mutex>

namespace nkos::video {

namespace {

// Buffers a frame let go of, for the next frames. A frame is a few hundred KiB: allocated afresh, musl maps and
// zeroes it and unmaps it again when it is freed, page by page (a fifth of the server's time at 19 Mbit/s).
// Buffers keep their size; a frame uses its first Frame::size bytes, so a reused one is not cleared either.
// A frame is made on the loop thread but may be let go of on a sender's thread (WebRTC's writers, the RustDesk
// bridge): the pool has its own lock.
constexpr std::size_t pool_bytes_max = 8u << 20;
std::mutex pool_mutex;
std::vector<std::vector<unsigned char> *> pool;
std::size_t pool_bytes = 0;

void give_back(std::vector<unsigned char> *buffer)
{
	{
		std::lock_guard lock(pool_mutex);
		if (pool_bytes + buffer->capacity() <= pool_bytes_max) {
			pool_bytes += buffer->capacity();
			pool.push_back(buffer);
			return;
		}
	}
	delete buffer;
}

} // namespace

std::shared_ptr<std::vector<unsigned char>> frame_buffer(std::size_t bytes)
{
	std::vector<unsigned char> *buffer = nullptr;
	{
		std::lock_guard lock(pool_mutex);
		// The smallest that fits, else the largest grows (its old bytes are never read again).
		std::size_t best = pool.size();
		for (std::size_t i = 0; i < pool.size(); i++)
			if (pool[i]->size() >= bytes && (best == pool.size() || pool[i]->size() < pool[best]->size())) best = i;
		if (best == pool.size() && !pool.empty()) {
			best = 0;
			for (std::size_t i = 1; i < pool.size(); i++)
				if (pool[i]->size() > pool[best]->size()) best = i;
		}
		if (best < pool.size()) {
			buffer = pool[best];
			pool_bytes -= buffer->capacity();
			pool[best] = pool.back();
			pool.pop_back();
		}
	}
	if (!buffer) buffer = new std::vector<unsigned char>;
	if (buffer->size() < bytes) buffer->resize(bytes);
	return std::shared_ptr<std::vector<unsigned char>>(buffer, give_back);
}

} // namespace nkos::video
