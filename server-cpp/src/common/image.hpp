#pragma once

// PNG (libspng on zlib-ng) and JPEG (libjpeg-turbo, TurboJPEG API) for the uploads and pictures the server makes:
// branding, screen preview, watermark, screen watch. Decoding gives 8-bit RGBA; PNG encodes RGB when every pixel is
// opaque, RGBA otherwise. Any thread.

#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::image {

enum class Format { unknown, png, jpeg };
Format sniff(std::string_view data);

struct Size {
	int width = 0, height = 0;
};
// From the header alone (Go's image.DecodeConfig).
std::optional<Size> size(std::string_view data);

struct Rgba {
	int width = 0, height = 0;
	std::vector<unsigned char> pixels;  // width * height * 4, rows top down
};
// A PNG or JPEG image of at most max_width x max_height.
std::optional<Rgba> decode(std::string_view data, int max_width, int max_height);

std::optional<std::string> encode_png(const Rgba &image);

enum class Pixels { rgb, rgba, gray };
// A baseline JPEG of quality 1-100 with 4:2:0 chroma (4:4:4 when full_chroma).
std::optional<std::string> encode_jpeg(const unsigned char *pixels, int width, int height, int pitch, Pixels format,
                                       int quality, bool full_chroma = false);

// A JPEG of Y, Cb and Cr planes, the chroma halved both ways (4:2:0); width and height even.
std::optional<std::string> encode_jpeg_yuv420(const unsigned char *y, const unsigned char *cb, const unsigned char *cr,
                                              int width, int height, int quality);

} // namespace nkos::image
