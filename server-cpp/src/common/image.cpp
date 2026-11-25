#include "common/image.hpp"

#include <spng.h>
#include <turbojpeg.h>

#include <cstdlib>
#include <cstring>
#include <memory>

namespace nkos::image {

namespace {

struct SpngFree {
	void operator()(spng_ctx *c) const { spng_ctx_free(c); }
};
struct TjFree {
	void operator()(void *h) const { tj3Destroy(h); }
};
using Spng = std::unique_ptr<spng_ctx, SpngFree>;
using Tj = std::unique_ptr<void, TjFree>;

constexpr unsigned char png_signature[8] = {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'};

std::optional<Rgba> decode_png(std::string_view data, int max_width, int max_height)
{
	Spng ctx(spng_ctx_new(0));
	if (!ctx) return std::nullopt;
	spng_set_image_limits(ctx.get(), static_cast<uint32_t>(max_width), static_cast<uint32_t>(max_height));
	// Ancillary chunks are not used: keep their memory small.
	spng_set_chunk_limits(ctx.get(), 1u << 20, 8u << 20);
	if (spng_set_png_buffer(ctx.get(), data.data(), data.size())) return std::nullopt;
	spng_ihdr ihdr{};
	if (spng_get_ihdr(ctx.get(), &ihdr)) return std::nullopt;
	size_t bytes = 0;
	if (spng_decoded_image_size(ctx.get(), SPNG_FMT_RGBA8, &bytes)) return std::nullopt;
	Rgba image;
	image.width = static_cast<int>(ihdr.width);
	image.height = static_cast<int>(ihdr.height);
	image.pixels.resize(bytes);
	if (spng_decode_image(ctx.get(), image.pixels.data(), bytes, SPNG_FMT_RGBA8, SPNG_DECODE_TRNS)) return std::nullopt;
	return image;
}

std::optional<Rgba> decode_jpeg(std::string_view data, int max_width, int max_height)
{
	Tj h(tj3Init(TJINIT_DECOMPRESS));
	if (!h) return std::nullopt;
	auto bytes = reinterpret_cast<const unsigned char *>(data.data());
	if (tj3DecompressHeader(h.get(), bytes, data.size()) < 0) return std::nullopt;
	int width = tj3Get(h.get(), TJPARAM_JPEGWIDTH), height = tj3Get(h.get(), TJPARAM_JPEGHEIGHT);
	if (width < 1 || height < 1 || width > max_width || height > max_height) return std::nullopt;
	int colorspace = tj3Get(h.get(), TJPARAM_COLORSPACE);
	Rgba image;
	image.width = width;
	image.height = height;
	image.pixels.resize(static_cast<std::size_t>(width) * height * 4);
	if (colorspace == TJCS_CMYK || colorspace == TJCS_YCCK) {
		// Adobe's inverted CMYK, as libjpeg returns it: each value is 255 minus the ink.
		if (tj3Decompress8(h.get(), bytes, data.size(), image.pixels.data(), 0, TJPF_CMYK) < 0) return std::nullopt;
		for (std::size_t i = 0; i < image.pixels.size(); i += 4) {
			unsigned c = image.pixels[i], m = image.pixels[i + 1], y = image.pixels[i + 2], k = image.pixels[i + 3];
			image.pixels[i] = static_cast<unsigned char>(c * k / 255);
			image.pixels[i + 1] = static_cast<unsigned char>(m * k / 255);
			image.pixels[i + 2] = static_cast<unsigned char>(y * k / 255);
			image.pixels[i + 3] = 255;
		}
		return image;
	}
	if (tj3Decompress8(h.get(), bytes, data.size(), image.pixels.data(), 0, TJPF_RGBA) < 0) return std::nullopt;
	return image;
}

} // namespace

Format sniff(std::string_view data)
{
	if (data.size() >= 8 && std::memcmp(data.data(), png_signature, 8) == 0) return Format::png;
	if (data.size() >= 3 && static_cast<unsigned char>(data[0]) == 0xff && static_cast<unsigned char>(data[1]) == 0xd8 &&
	    static_cast<unsigned char>(data[2]) == 0xff)
		return Format::jpeg;
	return Format::unknown;
}

std::optional<Size> size(std::string_view data)
{
	switch (sniff(data)) {
	case Format::png: {
		Spng ctx(spng_ctx_new(0));
		if (!ctx || spng_set_png_buffer(ctx.get(), data.data(), data.size())) return std::nullopt;
		spng_ihdr ihdr{};
		if (spng_get_ihdr(ctx.get(), &ihdr)) return std::nullopt;
		return Size{static_cast<int>(ihdr.width), static_cast<int>(ihdr.height)};
	}
	case Format::jpeg: {
		Tj h(tj3Init(TJINIT_DECOMPRESS));
		if (!h || tj3DecompressHeader(h.get(), reinterpret_cast<const unsigned char *>(data.data()), data.size()) < 0)
			return std::nullopt;
		return Size{tj3Get(h.get(), TJPARAM_JPEGWIDTH), tj3Get(h.get(), TJPARAM_JPEGHEIGHT)};
	}
	default:
		return std::nullopt;
	}
}

std::optional<Rgba> decode(std::string_view data, int max_width, int max_height)
{
	switch (sniff(data)) {
	case Format::png: return decode_png(data, max_width, max_height);
	case Format::jpeg: return decode_jpeg(data, max_width, max_height);
	default: return std::nullopt;
	}
}

std::optional<std::string> encode_png(const Rgba &image)
{
	if (image.width < 1 || image.height < 1 ||
	    image.pixels.size() != static_cast<std::size_t>(image.width) * image.height * 4)
		return std::nullopt;
	bool opaque = true;
	for (std::size_t i = 3; i < image.pixels.size() && opaque; i += 4) opaque = image.pixels[i] == 255;
	std::vector<unsigned char> rgb;
	const unsigned char *pixels = image.pixels.data();
	std::size_t bytes = image.pixels.size();
	if (opaque) {
		rgb.resize(static_cast<std::size_t>(image.width) * image.height * 3);
		for (std::size_t i = 0, j = 0; i < image.pixels.size(); i += 4, j += 3) std::memcpy(&rgb[j], &image.pixels[i], 3);
		pixels = rgb.data();
		bytes = rgb.size();
	}
	Spng ctx(spng_ctx_new(SPNG_CTX_ENCODER));
	if (!ctx) return std::nullopt;
	spng_set_option(ctx.get(), SPNG_ENCODE_TO_BUFFER, 1);
	spng_ihdr ihdr{};
	ihdr.width = static_cast<uint32_t>(image.width);
	ihdr.height = static_cast<uint32_t>(image.height);
	ihdr.bit_depth = 8;
	ihdr.color_type = opaque ? SPNG_COLOR_TYPE_TRUECOLOR : SPNG_COLOR_TYPE_TRUECOLOR_ALPHA;
	if (spng_set_ihdr(ctx.get(), &ihdr)) return std::nullopt;
	if (spng_encode_image(ctx.get(), pixels, bytes, SPNG_FMT_PNG, SPNG_ENCODE_FINALIZE)) return std::nullopt;
	size_t size = 0;
	int error = 0;
	void *buffer = spng_get_png_buffer(ctx.get(), &size, &error);
	if (!buffer || error) return std::nullopt;
	std::string out(static_cast<const char *>(buffer), size);
	std::free(buffer);
	return out;
}

std::optional<std::string> encode_jpeg(const unsigned char *pixels, int width, int height, int pitch, Pixels format,
                                       int quality, bool full_chroma)
{
	Tj h(tj3Init(TJINIT_COMPRESS));
	if (!h) return std::nullopt;
	int pf = format == Pixels::rgb ? TJPF_RGB : format == Pixels::rgba ? TJPF_RGBA : TJPF_GRAY;
	tj3Set(h.get(), TJPARAM_QUALITY, quality);
	tj3Set(h.get(), TJPARAM_SUBSAMP, format == Pixels::gray ? TJSAMP_GRAY : full_chroma ? TJSAMP_444 : TJSAMP_420);
	unsigned char *out = nullptr;
	size_t size = 0;
	if (tj3Compress8(h.get(), pixels, width, pitch, height, pf, &out, &size) < 0) {
		tj3Free(out);
		return std::nullopt;
	}
	std::string result(reinterpret_cast<const char *>(out), size);
	tj3Free(out);
	return result;
}

std::optional<std::string> encode_jpeg_yuv420(const unsigned char *y, const unsigned char *cb, const unsigned char *cr,
                                              int width, int height, int quality)
{
	Tj h(tj3Init(TJINIT_COMPRESS));
	if (!h) return std::nullopt;
	tj3Set(h.get(), TJPARAM_QUALITY, quality);
	tj3Set(h.get(), TJPARAM_SUBSAMP, TJSAMP_420);
	const unsigned char *planes[3] = {y, cb, cr};
	int strides[3] = {width, width / 2, width / 2};
	unsigned char *out = nullptr;
	size_t size = 0;
	if (tj3CompressFromYUVPlanes8(h.get(), planes, width, strides, height, &out, &size) < 0) {
		tj3Free(out);
		return std::nullopt;
	}
	std::string result(reinterpret_cast<const char *>(out), size);
	tj3Free(out);
	return result;
}

} // namespace nkos::image
