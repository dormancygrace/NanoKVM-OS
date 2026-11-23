#pragma once

// One encoded access unit as the capture hands it on: shared by every viewer, never copied for fan-out. The bytes
// before the data are headroom for the transports' framing (the Direct header, then the WebSocket header), which
// they write in place.

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <vector>

namespace nkos::video {

// Room before an access unit: the 9-byte Direct header plus the WebSocket header (LWS_PRE, checked where it is used).
inline constexpr std::size_t frame_headroom = 64;

// Native results (kvm_vision.h).
inline constexpr int result_key = 3;
inline constexpr int result_delta = 4;

// A buffer of at least bytes for a frame (headroom included), from the pool of buffers earlier frames let go of.
// Its bytes are not cleared and it may be larger. Made on the loop thread; a frame may be let go of on any thread.
std::shared_ptr<std::vector<unsigned char>> frame_buffer(std::size_t bytes);

struct Frame {
	std::shared_ptr<std::vector<unsigned char>> storage;  // at least offset + size bytes
	std::size_t offset = 0;  // where the access unit starts in storage
	std::size_t size = 0;
	int result = 0;          // < 0: no picture (the native error)
	std::int64_t timestamp = 0;  // microseconds since the session started
	std::chrono::microseconds duration{};  // one frame interval at the stream rate

	unsigned char *data() const { return storage->data() + offset; }
	bool key() const { return result == result_key; }
};

} // namespace nkos::video
