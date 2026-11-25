#include "ai/capture.hpp"

#include <algorithm>
#include <mutex>
#include <thread>

namespace nkos::ai::capture {

namespace {
std::mutex capture_slot;
constexpr std::size_t max_jpeg = 1 << 20;
constexpr const char *blocked_message = "MJPEG is unavailable while the HDMI input is 3840x2160: use H.264 or H.265";
} // namespace

bool jpeg_size(const std::string &d, int &width, int &height)
{
	auto byte = [&](std::size_t i) { return static_cast<unsigned char>(d[i]); };
	if (d.size() < 4 || byte(0) != 0xff || byte(1) != 0xd8) return false;
	// The size comes from the frame header; without a JFIF segment before it, Go reads on to the scan (an Adobe
	// segment may follow), so a header without a scan is no JPEG.
	bool jfif = false, have_size = false;
	std::size_t i = 2;
	while (i + 4 <= d.size()) {
		if (byte(i) != 0xff) return false;
		unsigned char marker = byte(i + 1);
		if (marker == 0xff) {
			i++;
			continue;
		}
		if (marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker == 0x01) {
			i += 2;
			continue;
		}
		if (marker == 0xda) return have_size;
		if (marker == 0xd9) return false;
		std::size_t length = static_cast<std::size_t>(byte(i + 2)) << 8 | byte(i + 3);
		if (length < 2 || i + 2 + length > d.size()) return false;
		if (marker == 0xe0 && length >= 7 && d.compare(i + 4, 5, std::string("JFIF\0", 5)) == 0) jfif = true;
		if (marker == 0xc0 || marker == 0xc1 || marker == 0xc2) {
			if (length < 8) return false;
			int precision = byte(i + 4);
			height = byte(i + 5) << 8 | byte(i + 6);
			width = byte(i + 7) << 8 | byte(i + 8);
			int components = byte(i + 9);
			if (precision != 8) return false;
			if (components != 1 && components != 3 && components != 4) return false;
			if (length != static_cast<std::size_t>(8 + 3 * components)) return false;
			if (jfif) return true;
			have_size = true;
		}
		if (marker >= 0xc3 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc) return false;
		i += 2 + length;
	}
	return false;
}

mcp::Snapshot snapshot(const Source &source, int quality, std::optional<int> timeout_ms, std::string &error)
{
	using Clock = std::chrono::steady_clock;
	if (!source.read || !source.screen) {
		error = "screenshot capture is unavailable";
		return {};
	}
	if (quality == 0) quality = 60;
	quality = std::clamp(quality, 51, 60);
	int timeout = timeout_ms ? std::clamp(*timeout_ms, 0, 30000) : 3000;
	std::lock_guard slot(capture_slot);
	std::unique_ptr<Lease> lease = source.lease ? source.lease() : nullptr;
	bool claim = lease != nullptr;
	auto deadline = Clock::now() + std::chrono::milliseconds(timeout);
	std::uint16_t width = 0, height = 0;
	source.screen(width, height);
	for (;;) {
		if (timeout > 0 && Clock::now() >= deadline) {
			mcp::Snapshot s;
			s.message = "screenshot capture timed out";
			return s;
		}
		std::string data;
		int result = source.read(width, height, static_cast<std::uint16_t>(quality), data);
		mcp::Snapshot s;
		s.ret_code = result;
		s.width = width;
		s.height = height;
		s.jpeg = data;
		if (result >= 0 && result != 5 && !data.empty()) {
			if (claim && lease->claim_fresh()) {
				claim = false;
				continue;
			}
			if (data.size() > max_jpeg) {
				s.jpeg.clear();
				s.message = "captured JPEG exceeds the MCP response size limit";
				return s;
			}
			int w = 0, h = 0;
			if (jpeg_size(data, w, h)) {
				s.ok = true;
				s.width = w;
				s.height = h;
				return s;
			}
			s.message = "captured data is not a valid JPEG";
		} else if (result == -8) {
			s.message = blocked_message;
			return s;
		} else if (result == 5) {
			s.message = "no HDMI signal or frame unavailable";
		} else if (result == -3 || result == -4 || result == -5) {
			s.message = "screenshot capture is temporarily unavailable";
		} else {
			s.message = "failed to capture screenshot";
		}
		if (timeout == 0 || Clock::now() + source.retry > deadline) return s;
		std::this_thread::sleep_for(source.retry);
	}
}

} // namespace nkos::ai::capture
