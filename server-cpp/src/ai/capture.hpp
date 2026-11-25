#pragma once

// MCP's screenshot (service/mcp/capture): one JPEG of the screen at the stream size, quality 51-60, with a capture
// lease held and the stale picture after a wake dropped; retried every 100 ms while there is no picture, until the
// timeout (3 s by default, at most 30 s; 0: one try). One capture at a time. Blocking.

#include "ai/mcp.hpp"

#include <functional>
#include <memory>
#include <optional>
#include <string>

namespace nkos::ai::capture {

struct Lease {
	virtual ~Lease() = default;
	// The first picture after the capture came on is from before: drop it (once).
	virtual bool claim_fresh() = 0;
};

struct Source {
	// ReadMjpeg: kvm_vision's result (0 a JPEG, 5 unchanged, < 0 none) and the bytes.
	std::function<int(std::uint16_t width, std::uint16_t height, std::uint16_t quality, std::string &jpeg)> read;
	// The stream size (CheckScreen, GetScreen).
	std::function<void(std::uint16_t &width, std::uint16_t &height)> screen;
	// AcquireHdmiCaptureLeaseForRead; nullptr: none.
	std::function<std::unique_ptr<Lease>()> lease;
	// The pause between tries (100 ms); a variable for the check.
	std::chrono::milliseconds retry{100};
};

// Snapshotter.Capture; error set when it failed outright.
mcp::Snapshot snapshot(const Source &source, int quality, std::optional<int> timeout_ms, std::string &error);

// image/jpeg's DecodeConfig: the size from the header; false when it is not a JPEG Go decodes.
bool jpeg_size(const std::string &data, int &width, int &height);

} // namespace nkos::ai::capture
