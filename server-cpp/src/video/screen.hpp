#pragma once

// The stream settings (resolution limit, frame rate, MJPEG quality, H.26x bit rate, MJPEG chroma) from
// /kvmapp/kvm, and the rules that depend on the capture: the video memory of this boot, the rate tiers of the
// encoder, which pictures H.264 can encode. As the Go server's common/screen.go, video_status.go, video_pool.go
// and h264_policy.go. Loop thread only.

#include <cstdint>
#include <string>
#include <string_view>

namespace nkos::video {

struct Screen {
	std::uint16_t width = 0, height = 0;  // the stream limit; 0x0 is the input size
	int fps = 90;
	std::uint16_t quality = 80;           // MJPEG quality 1..100
	std::uint16_t bitrate = 20000;        // H.26x kbit/s
	std::uint16_t mjpeg_chroma = 422;
	bool operator==(const Screen &) const = default;
};

inline constexpr int max_quality_value = 20000;
inline constexpr int default_bitrate = 20000;

const Screen &screen();
// key: "resolution", "quality", "mjpeg_chroma" or "fps"; invalid values are ignored.
void set_screen(std::string_view key, int value);
// Repairs invalid values (CheckScreen).
void check_screen();

// The settings as a stream uses them: the frame rate capped by the input's and the output's rate tier. Tells
// native capture a changed rate (kvmv_set_capture_fps).
Screen capture_screen();
int capture_rate_limit(int width, int height);
// The frame rate one viewer asked for (a RustDesk client, common.ViewerRateLimit): the capture runs at no more than
// the lowest of them (10..120). fps 0 removes the viewer's limit. Loop thread.
void set_viewer_rate(const void *viewer, int fps);

// The HDMI capture size, 0x0 without a signal (/run/nanokvm/width, height; read on every call).
std::pair<int, int> capture_input_size();
int read_video_value(const std::string &path);

// The ION video memory of this boot (Device Tree).
struct VideoPool {
	int mib = 0;
	bool reusable = false;
	bool qhd() const { return mib >= 54; }
	bool uhd() const { return mib >= 104 && (!reusable || mib >= 144); }
};
VideoPool booted_video_pool();

// H.264 runs up to 2560x1440 (1080x1920 in portrait) of encoded picture.
inline constexpr const char *h264_blocked_message =
	"H.264 takes at most 2560x1440 (1080x1920 in portrait): use H.265 or a stream resolution of 1440 or lower "
	"(1080 or lower in portrait)";
bool h264_allowed_for(int input_width, int input_height, std::uint16_t stream_height);
// Whether native capture streams an input at another size under a stream limit (0 is the input size).
bool stream_scaled(int input_width, int input_height, std::uint16_t stream_height);
// The width of a resolution limit (ResolutionMap); 0 when unknown.
std::uint16_t resolution_width(std::uint16_t height);

} // namespace nkos::video
