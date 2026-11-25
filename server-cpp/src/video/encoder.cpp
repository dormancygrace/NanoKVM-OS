#include "video/encoder.hpp"

#include "config/config.hpp"

#include <cerrno>
#include <cstring>
#include <fstream>

namespace nkos::video {

namespace {
constexpr const char *selection_file = "/etc/kvm/encoder_codec";
}

std::optional<Codec> parse_codec(std::string_view name)
{
	if (name == "h264") return Codec::h264;
	if (name == "h265") return Codec::h265;
	return std::nullopt;
}

std::optional<Codec> selected_codec()
{
	std::ifstream in(selection_file);
	std::string text;
	std::getline(in, text);
	auto first = text.find_first_not_of(" \t\r\n");
	if (first == std::string::npos) return std::nullopt;
	text = text.substr(first, text.find_last_not_of(" \t\r\n") - first + 1);
	return parse_codec(text);
}

std::optional<std::string> save_selected_codec(Codec codec)
{
	if (!config::atomic_write(selection_file, std::string(codec_name(codec)) + "\n", 0600))
		return std::string(std::strerror(errno));
	return std::nullopt;
}

} // namespace nkos::video
